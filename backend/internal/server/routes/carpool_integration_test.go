//go:build integration

package routes

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

type carpoolHTTPUserRepository struct {
	service.UserRepository
	users map[int64]service.User
}

func (r *carpoolHTTPUserRepository) GetByID(_ context.Context, id int64) (*service.User, error) {
	user, ok := r.users[id]
	if !ok {
		return nil, service.ErrUserNotFound
	}
	return &user, nil
}

func (*carpoolHTTPUserRepository) UpdateUserLastActiveAt(context.Context, int64, time.Time) error {
	return nil
}

func (*carpoolHTTPUserRepository) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

func TestCarpoolHTTPPostgresProductionAuth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	container, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23",
		tcpostgres.WithDatabase("carpool_http_test"), tcpostgres.WithUsername("carpool"),
		tcpostgres.WithPassword("local-test-only"), tcpostgres.BasicWaitStrategies())
	if container != nil {
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cleanupCancel()
			require.NoError(t, container.Terminate(cleanupCtx))
		})
	}
	require.NoError(t, err)
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	db.SetMaxOpenConns(4)
	_, err = db.ExecContext(ctx, `CREATE TABLE users (
		id BIGINT PRIMARY KEY, status TEXT NOT NULL, deleted_at TIMESTAMPTZ,
		balance NUMERIC NOT NULL DEFAULT 10
	);
	INSERT INTO users(id,status) VALUES (1,'active'),(2,'active'),(3,'active');`)
	require.NoError(t, err)
	require.NoError(t, carpool.Migrate(ctx, db))
	var migrated int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM carpool_schema_migrations WHERE filename='migrations/002_main_carpool.sql'`).Scan(&migrated))
	require.Equal(t, 1, migrated)

	cfg := &config.Config{CarpoolMain: carpool.MainConfig{Enabled: true, TestOrdersEnabled: true}}
	cfg.JWT.Secret = "carpool-local-integration-jwt-secret-only"
	cfg.JWT.AccessTokenExpireMinutes = 15
	now := time.Now().UTC()
	users := &carpoolHTTPUserRepository{users: map[int64]service.User{
		1: {ID: 1, Email: "admin@example.test", Role: service.RoleAdmin, Status: service.StatusActive, TokenVersion: 1, TokenVersionResolved: true, LastActiveAt: &now},
		2: {ID: 2, Email: "user1@example.test", Role: service.RoleUser, Status: service.StatusActive, TokenVersion: 1, TokenVersionResolved: true, LastActiveAt: &now},
		3: {ID: 3, Email: "user2@example.test", Role: service.RoleUser, Status: service.StatusActive, TokenVersion: 1, TokenVersionResolved: true, LastActiveAt: &now},
	}}
	auth := service.NewAuthService(nil, users, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	userService := service.NewUserService(users, nil, nil, nil)
	tokens := make(map[int64]string)
	for id := range users.users {
		user, getErr := users.GetByID(ctx, id)
		require.NoError(t, getErr)
		token, tokenErr := auth.GenerateToken(ctx, user)
		require.NoError(t, tokenErr)
		claims, validateErr := auth.ValidateToken(token)
		require.NoError(t, validateErr)
		require.EqualValues(t, 1, claims.TokenVersion)
		tokens[id] = token
	}
	main := carpool.NewMainService(db, cfg.CarpoolMain)
	h := &handler.Handlers{Carpool: handler.NewCarpoolHandler(main, cfg)}
	control, err := carpool.NewControlStore(ctx, main, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)), []string{"https://node.example"})
	require.NoError(t, err)
	h.CarpoolControl = handler.NewCarpoolControlHandler(control, nil, nil)
	router := gin.New()
	registerCarpoolUserRoutes(router.Group("/api/v1", gin.HandlerFunc(middleware.NewJWTAuthMiddleware(auth, userService, nil, nil))), h)
	// This isolated auth/handler transaction harness omits compliance, and neither
	// records nor accepts a real acknowledgement. TestCarpoolAdminComplianceAndAudit
	// separately verifies the full production registration rejects these calls with 423.
	registerCarpoolAdminRoutes(router.Group("/api/v1/admin", gin.HandlerFunc(middleware.NewAdminAuthMiddleware(auth, userService, nil, nil))), h)

	request := func(method, path, token, body, key string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body)).WithContext(ctx)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, want, w.Code, "%s %s: %s", method, path, w.Body.String())
		return w
	}
	decode := func(w *httptest.ResponseRecorder, dst any) {
		t.Helper()
		var envelope struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
		require.Zero(t, envelope.Code)
		require.NoError(t, json.Unmarshal(envelope.Data, dst))
	}
	for _, path := range []string{"/carpool/cars", "/admin/carpool/cars"} {
		for _, token := range []string{"", "invalid.jwt.token"} {
			request(http.MethodGet, path, token, "", "", http.StatusUnauthorized)
		}
	}
	request(http.MethodGet, "/admin/carpool/cars", tokens[2], "", "", http.StatusForbidden)
	request(http.MethodPost, "/admin/carpool/cars", tokens[3], "{}", "", http.StatusForbidden)
	stale := users.users[2]
	stale.TokenVersion = 2
	staleToken, err := auth.GenerateToken(ctx, &stale)
	require.NoError(t, err)
	request(http.MethodGet, "/carpool/cars", staleToken, "", "", http.StatusUnauthorized)

	input := carpool.CarInput{Name: "HTTP integration car", Capacity: 2, PriceMinor: 9900, Currency: "CNY", ServiceDays: 30,
		RecruitUntil: now.Add(time.Hour), DeliveryHours: 24, RefundPolicy: "Synthetic orders only; no payment"}
	body, err := json.Marshal(input)
	require.NoError(t, err)
	var car carpool.Car
	decode(request(http.MethodPost, "/admin/carpool/cars", tokens[1], string(body), "", http.StatusOK), &car)
	require.NotEmpty(t, car.ID)
	require.Equal(t, "draft", car.Status)
	adminCarPath, userCarPath := "/admin/carpool/cars/"+car.ID, "/carpool/cars/"+car.ID
	request(http.MethodGet, userCarPath, tokens[2], "", "", http.StatusNotFound)
	input.Name = "Updated HTTP draft"
	body, err = json.Marshal(input)
	require.NoError(t, err)
	decode(request(http.MethodPatch, adminCarPath, tokens[1], string(body), "", http.StatusOK), &car)
	require.Equal(t, input.Name, car.Name)
	decode(request(http.MethodGet, adminCarPath, tokens[1], "", "", http.StatusOK), &car)
	require.Equal(t, input.Name, car.Name)
	var catalog carpool.CarPage
	decode(request(http.MethodGet, "/admin/carpool/cars", tokens[1], "", "", http.StatusOK), &catalog)
	require.Len(t, catalog.Items, 1)
	decode(request(http.MethodPost, adminCarPath+"/publish", tokens[1], "{}", "", http.StatusOK), &car)
	require.Equal(t, "recruiting", car.Status)
	decode(request(http.MethodGet, "/carpool/cars", tokens[2], "", "", http.StatusOK), &catalog)
	require.Len(t, catalog.Items, 1)
	require.Equal(t, car.ID, catalog.Items[0].ID)
	require.False(t, catalog.ServerTime.IsZero())

	request(http.MethodPost, userCarPath+"/reservations", tokens[2], `{"user_id":3}`, "injected-user", http.StatusBadRequest)
	request(http.MethodPost, userCarPath+"/reservations", tokens[2], "{}", "", http.StatusBadRequest)
	var orderCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM carpool_orders`).Scan(&orderCount))
	require.Zero(t, orderCount)
	var first, replay, second carpool.Order
	decode(request(http.MethodPost, userCarPath+"/reservations", tokens[2], "{}", "user1-reserve", http.StatusOK), &first)
	require.EqualValues(t, 2, first.UserID)
	require.Equal(t, car.ID, first.CarID)
	require.Equal(t, "pending", first.Status)
	decode(request(http.MethodPost, userCarPath+"/reservations", tokens[2], "{}", "user1-reserve", http.StatusOK), &replay)
	require.Equal(t, first.ID, replay.ID)
	orderPath := "/carpool/orders/" + first.ID
	request(http.MethodGet, orderPath, tokens[3], "", "", http.StatusNotFound)
	request(http.MethodPost, orderPath+"/cancel", tokens[3], "{}", "", http.StatusNotFound)
	decode(request(http.MethodGet, orderPath, tokens[2], "", "", http.StatusOK), &replay)
	require.Equal(t, "pending", replay.Status)
	decode(request(http.MethodPost, userCarPath+"/reservations", tokens[3], "{}", "user2-reserve", http.StatusOK), &second)
	require.EqualValues(t, 3, second.UserID)
	require.NotEqual(t, first.SeatNo, second.SeatNo)
	for _, order := range []carpool.Order{first, second} {
		var mine carpool.OrderPage
		decode(request(http.MethodGet, "/carpool/mine", tokens[order.UserID], "", "", http.StatusOK), &mine)
		require.Equal(t, 1, mine.Total)
		require.Len(t, mine.Items, 1)
		require.Equal(t, order.ID, mine.Items[0].ID)
		require.Equal(t, order.UserID, mine.Items[0].UserID)
	}
	for _, path := range []string{orderPath + "/confirm", "/admin" + orderPath + "/confirm"} {
		request(http.MethodPost, path, tokens[1], "{}", "", http.StatusNotFound)
	}
	for _, route := range router.Routes() {
		require.NotContains(t, route.Path, "confirm")
	}

	// Simulate only the local test-order CLI, never an HTTP confirmation/payment.
	for _, order := range []carpool.Order{first, second} {
		confirmed, confirmErr := main.ConfirmTestOrder(ctx, order.ID)
		require.NoError(t, confirmErr)
		require.Equal(t, "confirmed", confirmed.Status)
	}
	decode(request(http.MethodGet, userCarPath, tokens[2], "", "", http.StatusOK), &car)
	require.Equal(t, "awaiting_deployment", car.Status)
	var members carpool.MemberPage
	decode(request(http.MethodGet, adminCarPath+"/members", tokens[1], "", "", http.StatusOK), &members)
	require.Len(t, members.Items, 2)
	for _, member := range members.Items {
		require.Equal(t, "waiting_deployment", member.Status)
		require.Nil(t, member.ServiceStart)
		require.Nil(t, member.ServiceEnd)
	}
	decode(request(http.MethodGet, orderPath, tokens[2], "", "", http.StatusOK), &replay)
	require.NotNil(t, replay.Membership)
	require.Equal(t, "waiting_deployment", replay.Membership.Status)
	// Real A JWT auth still cannot turn a confirmed reservation into active node access.
	request(http.MethodPost, userCarPath+"/keys", "", `{"name":"one"}`, uuid.NewString(), http.StatusUnauthorized)
	request(http.MethodPost, userCarPath+"/keys", tokens[2], `{"name":"one"}`, uuid.NewString(), http.StatusForbidden)
	request(http.MethodPost, userCarPath+"/keys", tokens[1], `{"name":"one"}`, uuid.NewString(), http.StatusNotFound)
	request(http.MethodPost, userCarPath+"/keys", tokens[2], `{"name":"one","user_id":1}`, uuid.NewString(), http.StatusBadRequest)
	request(http.MethodGet, userCarPath+"/usage?user_id=1", tokens[2], "", "", http.StatusBadRequest)
	var keyCommands int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM carpool_main_key_commands`).Scan(&keyCommands))
	require.Zero(t, keyCommands)
	var grants, balance int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM carpool_node_memberships`).Scan(&grants))
	require.Zero(t, grants)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT sum(balance) FROM users`).Scan(&balance))
	require.Equal(t, 30, balance)

	// Exercise the admin cancellation transaction without changing the confirmed car.
	var cancelled carpool.Car
	decode(request(http.MethodPost, "/admin/carpool/cars", tokens[1], string(body), "", http.StatusOK), &cancelled)
	decode(request(http.MethodPost, "/admin/carpool/cars/"+cancelled.ID+"/cancel", tokens[1], `{"reason":"integration cleanup","confirm_members":false}`, "", http.StatusOK), &cancelled)
	require.Equal(t, "cancelled", cancelled.Status)
}
