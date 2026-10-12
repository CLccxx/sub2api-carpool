//go:build integration

package carpool_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

type nativeControlLab struct {
	db       *sql.DB
	dsn      string
	ent      *dbent.Client
	users    service.UserRepository
	admin    *service.User
	auth     *service.AuthService
	settings *service.SettingService
	adminKey string
}

func newNativeControlLab(t *testing.T) *nativeControlLab {
	t.Helper()
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23", tcpostgres.WithDatabase("control_lab"), tcpostgres.WithUsername("carpool"), tcpostgres.WithPassword("local-test-only"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pg.Terminate(ctx)) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, repository.ApplyMigrations(ctx, db))
	require.NoError(t, carpool.Migrate(ctx, db))
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	users := repository.NewUserRepository(client, db)
	lab := &nativeControlLab{db: db, dsn: dsn, ent: client, users: users, adminKey: "synthetic-" + uuid.NewString()}
	lab.admin = lab.user(t, "admin")
	cfg := &config.Config{}
	cfg.JWT.Secret = "synthetic-" + uuid.NewString()
	cfg.JWT.AccessTokenExpireMinutes = 15
	lab.auth = service.NewAuthService(client, users, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	settingsRepo := repository.NewSettingRepository(client)
	require.NoError(t, settingsRepo.Set(ctx, service.SettingKeyAdminAPIKey, lab.adminKey))
	lab.settings = service.NewSettingService(settingsRepo, cfg)
	return lab
}

func (l *nativeControlLab) user(t *testing.T, role string) *service.User {
	t.Helper()
	u := &service.User{Email: uuid.NewString() + "@control-lab.invalid", PasswordHash: "!synthetic-unusable", Role: role, Status: service.StatusActive, Concurrency: 1}
	require.NoError(t, l.users.Create(context.Background(), u))
	return u
}

type nativeControlNode struct {
	*nativeControlLab
	node    *carpool.NodeControl
	server  *httptest.Server
	groupID int64
	gate    *carpool.Gate
}

func newNativeControlNode(t *testing.T, mainID string, registered carpool.Node, roots *x509.CertPool) nativeControlNode {
	t.Helper()
	ctx := context.Background()
	lab := newNativeControlLab(t)
	lab.user(t, "user")
	lab.user(t, "user")
	groups := repository.NewGroupRepository(lab.ent, lab.db)
	group := &service.Group{Name: "synthetic-control-group", Platform: "openai", Status: service.StatusActive, RateMultiplier: 1}
	require.NoError(t, groups.Create(ctx, group))
	cfg := carpool.Config{Enabled: true, AuthorizationMode: "managed", MainInstanceID: mainID, NodeID: registered.ID, CarID: registered.CarID, GroupID: group.ID}
	node, err := carpool.NewNodeControl(ctx, lab.db, cfg)
	require.NoError(t, err)
	gate := carpool.NewGate(lab.db, cfg)
	keysRepo := repository.NewAPIKeyRepository(lab.ent, lab.db)
	userService := service.NewUserService(lab.users, nil, nil, nil)
	api := service.ProvideAPIKeyService(gate, keysRepo, lab.users, groups, repository.NewUserSubscriptionRepository(lab.ent), repository.NewUserGroupRateRepository(lab.db), nil, &config.Config{CarpoolNode: cfg}, nil, nil)
	usageRepo := repository.NewUsageLogRepository(lab.ent, lab.db)
	accountUsage := service.NewAccountUsageService(repository.NewAccountRepository(lab.ent, lab.db, nil), usageRepo, nil, nil, nil, nil, nil, nil, service.NewUsageCache(), nil, nil)
	subscriptions := service.NewSubscriptionService(groups, repository.NewUserSubscriptionRepository(lab.ent), nil, lab.ent, nil)
	managedKeys := service.ProvideCarpoolManagedKeys(node, lab.ent, api, subscriptions)
	quota := service.ProvideCarpoolQuotaWorker(node, accountUsage, managedKeys)
	t.Cleanup(quota.Stop)
	h := handler.ProvideCarpoolControlHandler(nil, node, managedKeys, quota)
	admin := service.NewAdminService(&config.Config{}, lab.users, repository.NewAdminGroupRepository(lab.ent, lab.db), nil, nil, keysRepo, nil, nil, nil, nil, nil, nil, nil, lab.ent, lab.settings, nil, nil, nil, nil, nil, nil, nil, nil)
	userHandler := adminhandler.NewUserHandler(admin, nil, nil, nil, nil, userService, lab.settings)
	usageHandler := adminhandler.NewUsageHandler(service.NewUsageService(usageRepo, lab.users, lab.ent, nil), api, admin, nil)
	r := gin.New()
	g := r.Group("/api/v1/admin", gin.HandlerFunc(middleware.NewAdminAuthMiddleware(lab.auth, userService, lab.settings, nil)), h.EnforceControlTarget)
	g.GET("/carpool-control/v1/info", h.NodeInfo)
	g.POST("/carpool-control/v1/subscription/initialize", h.InitializeNativeSubscription)
	g.GET("/carpool-control/v1/accounts", h.NativeAccounts)
	g.GET("/carpool-control/v1/subscription/quota", h.NativeQuota)
	g.POST("/carpool-control/v1/subscription/quota/sync", h.SyncNativeQuota)
	g.GET("/carpool-control/v1/members/:subject/subscription", h.NativeMemberSubscription)
	g.PUT("/carpool-control/v1/members/:subject", h.ProvisionMember)
	g.PUT("/carpool-control/v1/members/:subject/grant", h.ApplyMemberGrant)
	g.POST("/carpool-control/v1/members/:subject/keys", h.CreateMemberKey)
	g.PATCH("/carpool-control/v1/members/:subject/keys/:key_id", h.UpdateMemberKey)
	g.DELETE("/carpool-control/v1/members/:subject/keys/:key_id", h.DeleteMemberKey)
	g.GET("/users/:id/api-keys", userHandler.GetUserAPIKeys)
	g.GET("/usage", usageHandler.List)
	g.GET("/usage/stats", usageHandler.Stats)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: registered.Hostname}, DNSNames: []string{registered.Hostname}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(9 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	require.NoError(t, err)
	parsed, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	roots.AddCert(parsed)
	server := httptest.NewUnstartedServer(r)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	return nativeControlNode{nativeControlLab: lab, node: node, server: server, groupID: group.ID, gate: gate}
}

func TestControlNativeHTTPMainToTwoNodes(t *testing.T) {
	ctx := context.Background()
	mainLab := newNativeControlLab(t)
	main := carpool.NewMainService(mainLab.db, carpool.MainConfig{Enabled: true, ManualEnrollmentEnabled: true})
	member := mainLab.user(t, "user")
	other := mainLab.user(t, "user")
	makeCar := func(host string) (carpool.Car, carpool.Node) {
		car, err := main.CreateCar(ctx, mainLab.admin.ID, carpool.CarInput{Name: "synthetic-" + host, Capacity: 2, PriceMinor: 100, Currency: "CNY", ServiceDays: 30, RecruitUntil: time.Now().Add(time.Hour), DeliveryHours: 24})
		require.NoError(t, err)
		_, err = main.PublishCar(ctx, mainLab.admin.ID, car.ID)
		require.NoError(t, err)
		node, err := main.RegisterNode(ctx, mainLab.admin.ID, carpool.NodeInput{CarID: car.ID, Hostname: host})
		require.NoError(t, err)
		for _, u := range []*service.User{member, other} {
			_, err = main.EnrollManual(ctx, mainLab.admin.ID, car.ID, uuid.NewString(), carpool.ManualEnrollmentInput{UserID: u.ID, AmountMinor: 100, Channel: "alipay", ReceivedAt: time.Now().Add(-time.Minute), Reference: uuid.NewString(), ConfirmReceived: true})
			require.NoError(t, err)
		}
		return car, node
	}
	carB, registeredB := makeCar("b.control-lab.invalid")
	carC, registeredC := makeCar("c.control-lab.invalid")
	store, err := carpool.NewControlStore(ctx, main, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)), []string{"https://" + registeredB.Hostname, "https://" + registeredC.Hostname})
	require.NoError(t, err)
	roots := x509.NewCertPool()
	b := newNativeControlNode(t, store.MainID(), registeredB, roots)
	c := newNativeControlNode(t, store.MainID(), registeredC, roots)
	addresses := map[string]string{registeredB.Hostname + ":443": b.server.Listener.Addr().String(), registeredC.Hostname + ":443": c.server.Listener.Addr().String()}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		target, ok := addresses[address]
		if !ok {
			return nil, carpool.ErrControlTarget
		}
		return (&net.Dialer{}).DialContext(ctx, network, target)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	carpool.ConfigureTestTransport(store, transport)
	for _, pair := range []struct {
		registered carpool.Node
		remote     nativeControlNode
	}{{registeredB, b}, {registeredC, c}} {
		_, err = store.SaveConnection(ctx, mainLab.admin.ID, pair.registered.ID, carpool.ControlConnectionInput{Origin: "https://" + pair.registered.Hostname, InstanceID: pair.remote.node.Info().InstanceID, AdminKey: pair.remote.adminKey})
		require.NoError(t, err)
		_, err = store.CheckConnection(ctx, mainLab.admin.ID, pair.registered.ID)
		require.NoError(t, err)
	}
	for i := 0; i < 4; i++ {
		worked, e := store.SyncNext(ctx)
		require.NoError(t, e)
		require.True(t, worked)
	}
	state, err := store.ConsoleForMember(ctx, member.ID, carB.ID)
	require.NoError(t, err)
	require.Equal(t, "unactivated", state.AccessStatus)
	require.Equal(t, "synced", state.SyncStatus)
	_, err = store.CreateMemberKey(ctx, member.ID, carB.ID, uuid.NewString(), "not activated")
	require.ErrorIs(t, err, carpool.ErrRequired)
	// The only active transition is test SQL; production phase 3 has no activation route.
	_, err = mainLab.db.Exec(`UPDATE carpool_cars SET status='active',service_start=clock_timestamp()-INTERVAL '1 hour',service_end=clock_timestamp()+INTERVAL '1 hour';
 UPDATE carpool_memberships m SET status='active',service_start=c.service_start,service_end=c.service_end FROM carpool_cars c WHERE c.id=m.car_id`)
	require.NoError(t, err)
	require.NoError(t, store.ReconcileControl(ctx))
	for i := 0; i < 4; i++ {
		_, err = store.SyncNext(ctx)
		require.NoError(t, err)
	}
	keyB, err := store.CreateMemberKey(ctx, member.ID, carB.ID, uuid.NewString(), "B only")
	require.NoError(t, err)
	keyC, err := store.CreateMemberKey(ctx, member.ID, carC.ID, uuid.NewString(), "C only")
	require.NoError(t, err)
	require.NotEqual(t, keyB.Key, keyC.Key)
	page, err := store.ListMemberKeys(ctx, member.ID, carB.ID, carpool.ControlPageQuery{Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, "B only", page.Items[0].Name)
	require.Empty(t, page.Items[0].Key)
	_, err = store.UpdateMemberKey(ctx, other.ID, carB.ID, keyB.ID, carpool.ManagedKeyUpdate{Name: ptrString("stolen")})
	require.ErrorIs(t, err, carpool.ErrMainNotFound)
	var bUser, cUser int64
	require.NoError(t, b.db.QueryRow(`SELECT user_id FROM api_keys WHERE id=$1`, keyB.ID).Scan(&bUser))
	require.NoError(t, c.db.QueryRow(`SELECT user_id FROM api_keys WHERE id=$1`, keyC.ID).Scan(&cUser))
	require.NotEqual(t, member.ID, bUser)
	require.NotEqual(t, member.ID, cUser)
	require.NoError(t, b.gate.CheckUser(ctx, bUser))
	require.NoError(t, c.gate.CheckUser(ctx, cUser))
	// Native usage handlers operate on actual stored records, not HTTP response stubs.
	for _, row := range []struct {
		remote    nativeControlNode
		user, key int64
		model     string
		tokens    int
	}{{b, bUser, keyB.ID, "synthetic-B", 11}, {c, cUser, keyC.ID, "synthetic-C", 29}} {
		account := &service.Account{Name: "synthetic-usage-account", Platform: "openai", Type: "apikey", Credentials: map[string]any{"api_key": "synthetic-unused"}, Status: service.StatusActive, Concurrency: 1}
		require.NoError(t, repository.NewAccountRepository(row.remote.ent, row.remote.db, nil).Create(ctx, account))
		_, err = repository.NewUsageLogRepository(row.remote.ent, row.remote.db).Create(ctx, &service.UsageLog{UserID: row.user, APIKeyID: row.key, AccountID: account.ID, GroupID: &row.remote.groupID, Model: row.model, RequestID: uuid.NewString(), InputTokens: row.tokens, RateMultiplier: 1, CreatedAt: time.Now().UTC()})
		require.NoError(t, err)
	}
	date := time.Now().UTC().Format("2006-01-02")
	q := carpool.ControlUsageQuery{ControlPageQuery: carpool.ControlPageQuery{Page: 1, PageSize: 20}, StartDate: date, EndDate: date}
	usage, err := store.MemberUsage(ctx, member.ID, carB.ID, q)
	require.NoError(t, err)
	require.Len(t, usage.Items, 1)
	require.Equal(t, "synthetic-B", usage.Items[0].Model)
	stats, err := store.MemberUsageStats(ctx, member.ID, carC.ID, q)
	require.NoError(t, err)
	require.EqualValues(t, 1, stats.TotalRequests)
	otherUsage, err := store.MemberUsage(ctx, other.ID, carB.ID, q)
	require.NoError(t, err)
	require.Empty(t, otherUsage.Items)
	// Real A JWT authentication still scopes the console to the signed-in member.
	h := handler.NewCarpoolControlHandler(store, nil, nil)
	r := gin.New()
	g := r.Group("/api/v1/carpool", gin.HandlerFunc(middleware.NewJWTAuthMiddleware(mainLab.auth, service.NewUserService(mainLab.users, nil, nil, nil), mainLab.settings, nil)))
	g.GET("/cars/:id/console", h.MemberConsole)
	g.GET("/cars/:id/usage", h.CarUsage)
	token, err := mainLab.auth.GenerateToken(ctx, member)
	require.NoError(t, err)
	request := httptest.NewRequest("GET", "/api/v1/carpool/cars/"+carB.ID+"/console", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, request)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.NotContains(t, w.Body.String(), b.adminKey)
	var result struct {
		Data carpool.ControlConsole `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.True(t, result.Data.CanCreateKey)
	_, err = mainLab.db.Exec(`UPDATE carpool_memberships SET status='revoked' WHERE car_id=$1 AND user_id=$2`, carB.ID, member.ID)
	require.NoError(t, err)
	require.NoError(t, store.ReconcileControl(ctx))
	_, err = store.SyncNext(ctx)
	require.NoError(t, err)
	require.ErrorIs(t, b.gate.CheckUser(ctx, bUser), carpool.ErrRequired)
	require.NoError(t, c.gate.CheckUser(ctx, cUser), "revoking B does not revoke C")
	require.NoError(t, store.DeleteMemberKey(ctx, member.ID, carB.ID, keyB.ID))
	_, err = store.CreateMemberKey(ctx, member.ID, carB.ID, uuid.NewString(), "revoked")
	require.ErrorIs(t, err, carpool.ErrRequired)
	c.server.Close()
	_, err = store.MemberUsage(ctx, member.ID, carC.ID, q)
	require.ErrorIs(t, err, carpool.ErrControlTransport, "C failure is not empty usage or B fallback")
	_, err = c.db.Exec(`UPDATE carpool_managed_members SET lease_until=clock_timestamp()-INTERVAL '1 second' WHERE user_id=$1`, cUser)
	require.NoError(t, err)
	require.ErrorIs(t, c.gate.CheckUser(ctx, cUser), carpool.ErrRequired, "without renewal the node fails closed at the local lease deadline")
	_, err = store.ListMemberKeys(ctx, member.ID, carB.ID, carpool.ControlPageQuery{Page: 1, PageSize: 20})
	require.NoError(t, err)
	// Neither internal node user has a main-site email identity or a copied password.
	for _, remote := range []nativeControlNode{b, c} {
		var leaked int
		require.NoError(t, remote.db.QueryRow(`SELECT count(*) FROM auth_identities a JOIN carpool_managed_members m ON m.user_id=a.user_id`).Scan(&leaked))
		require.Zero(t, leaked)
	}
}

func ptrString(v string) *string { return &v }
