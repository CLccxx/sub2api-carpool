//go:build integration

package routes

import (
	"context"
	"database/sql"
	"encoding/json"
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
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestCarpoolManualHTTP(t *testing.T) {
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23", tcpostgres.WithDatabase("manual_http"), tcpostgres.WithUsername("carpool"), tcpostgres.WithPassword("local-test-only"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, container.Terminate(ctx)) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE users(id BIGINT PRIMARY KEY,email TEXT NOT NULL,status TEXT NOT NULL,deleted_at TIMESTAMPTZ);
	 INSERT INTO users VALUES(1,'admin@example.test','active',NULL),(2,'member@example.test','active',NULL),(3,'other@example.test','active',NULL);`)
	require.NoError(t, err)
	require.NoError(t, carpool.Migrate(ctx, db))
	cfg := &config.Config{CarpoolMain: carpool.MainConfig{Enabled: true, ManualEnrollmentEnabled: true}}
	cfg.JWT.Secret = "manual-local-jwt-secret-only"
	cfg.JWT.AccessTokenExpireMinutes = 15
	now := time.Now().UTC()
	users := &carpoolHTTPUserRepository{users: map[int64]service.User{
		1: {ID: 1, Email: "admin@example.test", Role: service.RoleAdmin, Status: service.StatusActive, TokenVersion: 1, TokenVersionResolved: true, LastActiveAt: &now},
		2: {ID: 2, Email: "member@example.test", Role: service.RoleUser, Status: service.StatusActive, TokenVersion: 1, TokenVersionResolved: true, LastActiveAt: &now},
		3: {ID: 3, Email: "other@example.test", Role: service.RoleUser, Status: service.StatusActive, TokenVersion: 1, TokenVersionResolved: true, LastActiveAt: &now},
	}}
	auth := service.NewAuthService(nil, users, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	userService := service.NewUserService(users, nil, nil, nil)
	tokens := map[int64]string{}
	for id := range users.users {
		u, e := users.GetByID(ctx, id)
		require.NoError(t, e)
		tokens[id], e = auth.GenerateToken(ctx, u)
		require.NoError(t, e)
	}
	s := carpool.NewMainService(db, cfg.CarpoolMain)
	h := &handler.Handlers{Carpool: handler.NewCarpoolHandler(s, cfg)}
	router := gin.New()
	registerCarpoolUserRoutes(router.Group("/api/v1", gin.HandlerFunc(middleware.NewJWTAuthMiddleware(auth, userService, nil, nil))), h)
	// Positive API harness only; production compliance remains tested separately.
	registerCarpoolAdminRoutes(router.Group("/api/v1/admin", gin.HandlerFunc(middleware.NewAdminAuthMiddleware(auth, userService, nil, nil))), h)
	request := func(method, path string, actor int64, body any, key string, status int) *httptest.ResponseRecorder {
		t.Helper()
		raw, e := json.Marshal(body)
		require.NoError(t, e)
		req := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(string(raw)))
		if actor != 0 {
			req.Header.Set("Authorization", "Bearer "+tokens[actor])
		}
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, status, w.Code, "%s %s: %s", method, path, w.Body.String())
		return w
	}
	car, err := s.CreateCar(ctx, 1, carpool.CarInput{Name: "Manual HTTP", Capacity: 2, PriceMinor: 9900, Currency: "CNY", ServiceDays: 30, RecruitUntil: now.Add(time.Hour), DeliveryHours: 24})
	require.NoError(t, err)
	_, err = s.PublishCar(ctx, 1, car.ID)
	require.NoError(t, err)
	path := "/admin/carpool/cars/" + car.ID + "/members"
	input := carpool.ManualEnrollmentInput{UserID: 2, AmountMinor: 9900, Channel: "wechat", ReceivedAt: now.Add(-time.Minute), Reference: "private-receipt", Note: "private-note", ConfirmReceived: true}
	request("POST", path, 0, input, "enroll", 401)
	request("POST", path, 2, input, "enroll", 403)
	request("POST", path, 1, input, "", 400)
	request("POST", path, 1, map[string]any{"actor_id": 3}, "enroll", 400)
	w := request("POST", path, 1, input, "enroll", 200)
	var enrolled struct {
		Data carpool.ManualOrder `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &enrolled))
	require.Equal(t, "member@example.test", enrolled.Data.UserEmail)
	request("POST", path, 1, input, "enroll", 200)
	changed := input
	changed.Note = "different request"
	request("POST", path, 1, changed, "enroll", 409)
	orderPath := "/admin/carpool/orders/" + enrolled.Data.ID
	request("GET", orderPath, 2, nil, "", 403)
	public := request("GET", "/carpool/orders/"+enrolled.Data.ID, 2, nil, "", 200)
	require.NotContains(t, public.Body.String(), "private-receipt")
	require.NotContains(t, public.Body.String(), "private-note")
	mine := request("GET", "/carpool/mine", 2, nil, "", 200)
	require.NotContains(t, mine.Body.String(), "private-receipt")
	require.NotContains(t, mine.Body.String(), "private-note")
	require.NotContains(t, mine.Body.String(), "recorded_by")
	request("GET", "/carpool/orders/"+enrolled.Data.ID, 3, nil, "", 404)
	request("POST", "/carpool/cars/"+car.ID+"/reservations", 2, map[string]any{}, "cannot-reserve", 404)
	request("POST", orderPath+"/withdraw", 1, carpool.ManualWithdrawalInput{Reason: "customer request", Confirm: true}, "withdraw", 200)
	w = request("GET", "/admin/carpool/orders?refund_status=pending", 1, nil, "", 200)
	require.Contains(t, w.Body.String(), `"total":1`)
	refund := carpool.ManualRefundInput{AmountMinor: 9900, RefundedAt: time.Now().UTC().Add(-time.Second), Reference: "refund-private", ConfirmRefunded: true}
	request("POST", orderPath+"/refund", 1, refund, "refund", 200)
	request("POST", orderPath+"/refund", 1, refund, "refund", 200)
	public = request("GET", "/carpool/orders/"+enrolled.Data.ID, 2, nil, "", 200)
	require.Contains(t, public.Body.String(), `"status":"refunded"`)
	require.NotContains(t, public.Body.String(), "refund-private")
}
