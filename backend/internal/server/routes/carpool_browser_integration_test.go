//go:build integration

package routes

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// carpoolBrowserConfig is the stdin contract for tools/carpool/browser.mjs.
// Sites are origin strings, users are ordered 1 (admin), 2, 3 and contain the
// frontend auth_user fields plus token (auth_token). DSN is only for the local
// test-order CLI. Artifacts is a retained, mode-0700 directory, not t.TempDir.
type carpoolBrowserConfig struct {
	Sites     map[string]string    `json:"sites"`
	Users     []carpoolBrowserUser `json:"users"`
	DSN       string               `json:"dsn"`
	CLI       string               `json:"cli"`
	Artifacts string               `json:"artifacts"`
}

// This fixture mirrors the /auth/me DTO fields consumed by the shell. Profile
// identity lookup is intentionally outside this carpool transaction harness.
type carpoolBrowserProfile struct {
	*dto.User
	RunMode    string `json:"run_mode"`
	EmailBound bool   `json:"email_bound"`
}

type carpoolBrowserUser struct {
	carpoolBrowserProfile
	Token string `json:"token"`
}

type carpoolBrowserSettingsRepository struct {
	service.SettingRepository
	values map[string]string
}

func (r *carpoolBrowserSettingsRepository) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			values[key] = value
		}
	}
	return values, nil
}

func TestCarpoolBrowser(t *testing.T) {
	if os.Getenv("CARPOOL_BROWSER_TEST") != "1" {
		t.Skip("set CARPOOL_BROWSER_TEST=1 to run the local browser harness")
	}
	preview := os.Getenv("CARPOOL_BROWSER_PREVIEW") == "1"
	manualBrowser := os.Getenv("CARPOOL_MANUAL_BROWSER") == "1"
	duration := 5 * time.Minute
	if preview {
		duration = 8 * time.Hour
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	gin.SetMode(gin.TestMode)

	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok, "locate browser harness source")
	root, err := filepath.Abs(filepath.Join(filepath.Dir(source), "../../../.."))
	require.NoError(t, err)
	script := filepath.Join(root, "tools/carpool/browser.mjs")
	if manualBrowser {
		script = filepath.Join(root, "tools/carpool/manual-browser.mjs")
	}
	dist := filepath.Join(root, "backend/internal/web/dist")
	cli := "/tmp/carpool-phase1b-test-order"
	if configured := os.Getenv("CARPOOL_BROWSER_CLI"); configured != "" {
		cli = configured
	}
	for _, filename := range []string{script, filepath.Join(dist, "index.html"), cli} {
		info, statErr := os.Stat(filename)
		require.NoError(t, statErr, "parent must build the frontend and CLI and provide browser.mjs")
		require.True(t, info.Mode().IsRegular(), "expected regular file: %s", filename)
		if filename == cli {
			require.NotZero(t, info.Mode().Perm()&0111, "test-order CLI must be executable")
		}
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err, "Node must be on PATH")
	artifacts, err := os.MkdirTemp("", "carpool-browser-")
	require.NoError(t, err)
	require.NoError(t, os.Chmod(artifacts, 0700))
	fmt.Fprintln(os.Stdout, "carpool browser artifacts:", artifacts)

	container, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23",
		tcpostgres.WithDatabase("carpool_browser_test"), tcpostgres.WithUsername("carpool"),
		tcpostgres.WithPassword("local-test-only"), tcpostgres.BasicWaitStrategies(),
		testcontainers.WithLogger(log.New(io.Discard, "", 0)))
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
	if err != nil {
		t.Fatal("open isolated PostgreSQL database failed")
	}
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	db.SetMaxOpenConns(4)
	_, err = db.ExecContext(ctx, `CREATE TABLE users (
		id BIGINT PRIMARY KEY, email TEXT NOT NULL, username TEXT NOT NULL,
		role TEXT NOT NULL, status TEXT NOT NULL, deleted_at TIMESTAMPTZ,
		balance NUMERIC NOT NULL DEFAULT 10
	);
	INSERT INTO users(id,email,username,role,status) VALUES
		(1,'admin@example.test','Browser Admin','admin','active'),
		(2,'user1@example.test','Browser User One','user','active'),
		(3,'user2@example.test','Browser User Two','user','active');`)
	require.NoError(t, err)
	require.NoError(t, carpool.Migrate(ctx, db))
	var migrated int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM carpool_schema_migrations
		WHERE filename='migrations/002_main_carpool.sql'`).Scan(&migrated))
	require.Equal(t, 1, migrated)

	// Shared login fixtures isolate the four route/public-setting variants from
	// node membership admission, which is covered by the node access tests.
	authCfg := &config.Config{RunMode: config.RunModeStandard}
	authCfg.JWT.Secret = "carpool-local-browser-jwt-secret-only"
	authCfg.JWT.AccessTokenExpireMinutes = 15
	if preview {
		authCfg.JWT.AccessTokenExpireMinutes = 480
	}
	now := time.Now().UTC()
	users := &carpoolHTTPUserRepository{users: make(map[int64]service.User)}
	rows, err := db.QueryContext(ctx, "SELECT id,email,username,role FROM users ORDER BY id")
	require.NoError(t, err)
	for rows.Next() {
		user := service.User{Status: service.StatusActive, Balance: 10, Concurrency: 2,
			TokenVersion: 1, TokenVersionResolved: true, LastActiveAt: &now, CreatedAt: now, UpdatedAt: now}
		scanErr := rows.Scan(&user.ID, &user.Email, &user.Username, &user.Role)
		if scanErr != nil {
			_ = rows.Close()
			t.Fatal("load isolated test user failed")
		}
		users.users[user.ID] = user
	}
	require.NoError(t, rows.Close())
	require.NoError(t, rows.Err())
	require.Len(t, users.users, 3)
	auth := service.NewAuthService(nil, users, nil, nil, authCfg, nil, nil, nil, nil, nil, nil, nil, nil)
	userService := service.NewUserService(users, nil, nil, nil)
	input := carpoolBrowserConfig{Sites: make(map[string]string), DSN: dsn, CLI: cli, Artifacts: artifacts}
	secrets := []string{dsn, "[redacted]", authCfg.JWT.Secret, "[redacted]", "local-test-only", "[redacted]"}
	for id := int64(1); id <= 3; id++ {
		user, getErr := users.GetByID(ctx, id)
		require.NoError(t, getErr)
		token, tokenErr := auth.GenerateToken(ctx, user)
		require.NoError(t, tokenErr)
		input.Users = append(input.Users, carpoolBrowserUser{
			carpoolBrowserProfile: carpoolBrowserProfile{User: dto.UserFromService(user), RunMode: config.RunModeStandard, EmailBound: true},
			Token:                 token,
		})
		secrets = append(secrets, token, "[redacted]")
	}
	for _, site := range []struct {
		name                     string
		main, test, node, manual bool
	}{
		{"main", true, true, false, false},
		{"readonly", true, false, false, false},
		{"disabled", false, false, false, false},
		{"node", false, false, true, false},
		{"manual", true, false, false, true},
	} {
		cfg := &config.Config{RunMode: config.RunModeStandard,
			CarpoolMain: carpool.MainConfig{Enabled: site.main, TestOrdersEnabled: site.test, ManualEnrollmentEnabled: site.manual},
			CarpoolNode: carpool.Config{Enabled: site.node, NodeID: "browser-node", CarID: "browser-car", Issuer: "local-browser-harness"}}
		require.NoError(t, cfg.CarpoolMain.Validate(cfg.CarpoolNode.Enabled))
		require.NoError(t, cfg.CarpoolNode.Validate())
		server := carpoolBrowserSite(t, ctx, db, cfg, auth, userService, users, dist)
		input.Sites[site.name] = server.URL
	}
	if preview {
		main := carpool.NewMainService(db, carpool.MainConfig{Enabled: true, TestOrdersEnabled: !manualBrowser, ManualEnrollmentEnabled: manualBrowser})
		for _, capacity := range []int{2, 3, 4} {
			car, err := main.CreateCar(ctx, 1, carpool.CarInput{Name: fmt.Sprintf("Local test car %d", capacity), Capacity: capacity,
				PriceMinor: 9900, Currency: "CNY", ServiceDays: 30, RecruitUntil: time.Now().Add(8 * time.Hour), DeliveryHours: 24,
				RefundPolicy: "Synthetic preview only. No real payment, refund or service activation."})
			require.NoError(t, err)
			_, err = main.PublishCar(ctx, 1, car.ID)
			require.NoError(t, err)
			if capacity == 2 {
				for _, id := range []int64{2, 3} {
					if manualBrowser {
						_, err = main.EnrollManual(ctx, 1, car.ID, fmt.Sprintf("preview-manual-%d", id), carpool.ManualEnrollmentInput{UserID: id, AmountMinor: 9900, Channel: "wechat", ReceivedAt: time.Now().Add(-time.Minute), ConfirmReceived: true})
						require.NoError(t, err)
						continue
					}
					order, err := main.Reserve(ctx, id, car.ID, fmt.Sprintf("preview-%d", id))
					require.NoError(t, err)
					_, err = main.ConfirmTestOrder(ctx, order.ID)
					require.NoError(t, err)
				}
			}
		}
		previewSite := input.Sites["main"]
		if manualBrowser {
			previewSite = input.Sites["manual"]
		}
		fmt.Fprintln(os.Stdout, "Local test preview admin:", previewSite+"/admin/carpool?preview_user=1")
		fmt.Fprintln(os.Stdout, "Local test preview user:", previewSite+"/carpool?preview_user=2")
		fmt.Fprintln(os.Stdout, "Synthetic accounts only; no compliance acknowledgement, payments or production services. Auto-stops after 8 hours.")
		stopCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
		<-stopCtx.Done()
		return
	}

	payload, err := json.Marshal(input)
	require.NoError(t, err)
	cmd := exec.CommandContext(ctx, node, script)
	cmd.Dir = root
	cmd.Stdin = bytes.NewReader(payload)
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	redact := strings.NewReplacer(secrets...)
	// Do not echo stderr or config: browser diagnostics can contain credentials.
	// The private retained file remains useful on process failure or timeout.
	require.NoError(t, os.WriteFile(filepath.Join(artifacts, "node.stderr.log"), []byte(redact.Replace(stderr.String())), 0600))
	fmt.Fprint(os.Stdout, redact.Replace(stdout.String()))
	if runErr != nil {
		t.Fatal("browser runner failed or timed out; inspect node.stderr.log in the artifact directory")
	}
}

func carpoolBrowserSite(t *testing.T, ctx context.Context, db *sql.DB, cfg *config.Config,
	auth *service.AuthService, userService *service.UserService, users *carpoolHTTPUserRepository, dist string,
) *httptest.Server {
	t.Helper()
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.GET("/setup/status", func(c *gin.Context) {
		response.Success(c, gin.H{"needs_setup": false, "step": "completed"})
	})
	settings := service.NewSettingService(&carpoolBrowserSettingsRepository{values: map[string]string{
		service.SettingKeySiteName:              "Carpool Browser Test",
		service.SettingKeySiteSubtitle:          "Local integration harness",
		service.SettingKeyCustomMenuItems:       "[]",
		service.SettingKeyCustomEndpoints:       "[]",
		service.SettingKeyChannelMonitorEnabled: "false",
		service.SettingKeySubscriptionEnabled:   "false",
		service.SettingKeyPromoCodeEnabled:      "false",
		service.SettingKeyContactInfo:           "Local test administrator <b>contact</b>\nWeChat: synthetic-carpool-admin",
	}}, cfg)
	router.GET("/api/v1/settings/public", handler.NewSettingHandler(settings, "local-test-harness").GetPublicSettings)
	h := &handler.Handlers{Carpool: handler.NewCarpoolHandler(carpool.NewMainService(db, cfg.CarpoolMain), cfg)}
	userGroup := router.Group("/api/v1", gin.HandlerFunc(middleware.NewJWTAuthMiddleware(auth, userService, nil, nil)))
	registerCarpoolUserRoutes(userGroup, h)
	userGroup.GET("/auth/me", func(c *gin.Context) {
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok {
			response.Unauthorized(c, "User not authenticated")
			return
		}
		user, err := users.GetByID(c.Request.Context(), subject.UserID)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		response.Success(c, carpoolBrowserProfile{User: dto.UserFromService(user), RunMode: config.RunModeStandard, EmailBound: true})
	})
	for _, endpoint := range []string{"/announcements", "/subscriptions/active", "/subscriptions/progress"} {
		userGroup.GET(endpoint, func(c *gin.Context) { response.Success(c, []any{}) })
	}
	// Disabled/node feature guards redirect ordinary users to the dashboard.
	// Only its known GET dependencies are fixtures; carpool responses stay real.
	userGroup.GET("/usage/dashboard/stats", func(c *gin.Context) {
		response.Success(c, usagestats.UserDashboardStats{})
	})
	userGroup.GET("/usage/dashboard/trend", func(c *gin.Context) {
		response.Success(c, gin.H{"trend": []any{}, "start_date": c.Query("start_date"),
			"end_date": c.Query("end_date"), "granularity": c.Query("granularity")})
	})
	userGroup.GET("/usage/dashboard/models", func(c *gin.Context) {
		response.Success(c, gin.H{"models": []any{}, "start_date": c.Query("start_date"), "end_date": c.Query("end_date")})
	})
	userGroup.GET("/usage", func(c *gin.Context) {
		response.Success(c, gin.H{"items": []any{}, "total": 0, "page": 1, "page_size": 20, "pages": 0})
	})
	userGroup.GET("/user/platform-quotas", func(c *gin.Context) {
		response.Success(c, gin.H{"platform_quotas": []any{}})
	})
	adminGroup := router.Group("/api/v1/admin", gin.HandlerFunc(middleware.NewAdminAuthMiddleware(auth, userService, nil, nil)))
	// Like TestCarpoolHTTPPostgresProductionAuth, this positive transaction
	// harness deliberately omits compliance middleware. The parent must run
	// TestCarpoolAdminComplianceAndAudit separately. No acknowledgement is
	// recorded, and there is intentionally NO /compliance/accept route.
	registerCarpoolAdminRoutes(adminGroup, h)
	adminGroup.GET("/users", func(c *gin.Context) {
		search := strings.ToLower(strings.TrimSpace(c.Query("search")))
		items := []*dto.User{}
		for id := int64(1); id <= 3; id++ {
			user, err := users.GetByID(c.Request.Context(), id)
			if err == nil && user.Status == service.StatusActive && (strings.Contains(strings.ToLower(user.Email), search) || strconv.FormatInt(id, 10) == search) {
				items = append(items, dto.UserFromService(user))
			}
		}
		response.Success(c, gin.H{"items": items, "total": len(items), "page": 1, "page_size": 20, "pages": 1})
	})
	adminGroup.GET("/users/:id", func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid user ID")
			return
		}
		user, err := users.GetByID(c.Request.Context(), id)
		if response.ErrorFrom(c, err) {
			return
		}
		response.Success(c, dto.UserFromService(user))
	})
	adminGroup.GET("/compliance", func(c *gin.Context) {
		response.Success(c, gin.H{"required": false, "version": "local-test-harness"})
	})
	adminGroup.GET("/settings", func(c *gin.Context) {
		response.Success(c, gin.H{"ops_monitoring_enabled": false, "ops_realtime_monitoring_enabled": false,
			"ops_query_mode_default": "auto", "custom_menu_items": []any{}})
	})
	adminGroup.GET("/payment/config", func(c *gin.Context) { response.Success(c, gin.H{"enabled": false}) })
	adminGroup.GET("/system/version", func(c *gin.Context) { response.Success(c, gin.H{"version": "local-test-harness"}) })
	adminGroup.GET("/system/check-updates", func(c *gin.Context) {
		response.Success(c, gin.H{"current_version": "local-test-harness", "latest_version": "local-test-harness",
			"has_update": false, "cached": true, "build_type": "source"})
	})

	files := os.DirFS(dist)
	static := http.FileServer(http.FS(files))
	router.NoRoute(func(c *gin.Context) {
		cleanPath := path.Clean("/" + c.Request.URL.Path)
		if cleanPath == "/api" || strings.HasPrefix(cleanPath, "/api/") {
			response.NotFound(c, "API route not found")
			return
		}
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		// This authentication shortcut exists only in the explicitly opted-in,
		// loopback integration-test preview, never in the production executable.
		if os.Getenv("CARPOOL_BROWSER_PREVIEW") == "1" && c.Query("preview_user") != "" &&
			(cleanPath == "/admin/carpool" || cleanPath == "/carpool" || cleanPath == "/carpool/mine") {
			id, err := strconv.ParseInt(c.Query("preview_user"), 10, 64)
			if err != nil || id < 1 || id > 3 {
				c.AbortWithStatus(http.StatusBadRequest)
				return
			}
			user, err := users.GetByID(ctx, id)
			if err != nil {
				c.AbortWithStatus(http.StatusInternalServerError)
				return
			}
			token, err := auth.GenerateToken(ctx, user)
			if err != nil {
				c.AbortWithStatus(http.StatusInternalServerError)
				return
			}
			profile, err := json.Marshal(carpoolBrowserUser{carpoolBrowserProfile: carpoolBrowserProfile{User: dto.UserFromService(user), RunMode: config.RunModeStandard, EmailBound: true}, Token: token})
			if err != nil {
				c.AbortWithStatus(http.StatusInternalServerError)
				return
			}
			c.Header("Cache-Control", "no-store")
			c.Header("Referrer-Policy", "no-referrer")
			c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(`<!doctype html><html lang="zh"><meta charset="utf-8"><title>Local test preview</title><script>
const {token,...profile}=`+string(profile)+`;
localStorage.removeItem('refresh_token');localStorage.removeItem('token_expires_at');
localStorage.setItem('auth_token',token);localStorage.setItem('auth_user',JSON.stringify(profile));localStorage.setItem('sub2api_locale','zh');
localStorage.setItem((profile.role==='admin'?'admin':'user')+'_guide_'+profile.id+'_'+profile.role+'_v4_interactive','true');
location.replace(location.pathname);
</script></html>`))
			return
		}
		name := strings.TrimPrefix(cleanPath, "/")
		if info, err := fs.Stat(files, name); err == nil && info.Mode().IsRegular() {
			c.Status(http.StatusOK)
			static.ServeHTTP(c.Writer, c.Request)
			return
		}
		if strings.HasPrefix(cleanPath, "/assets/") || path.Ext(cleanPath) != "" {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Status(http.StatusOK)
		c.File(filepath.Join(dist, "index.html"))
	})
	server := httptest.NewUnstartedServer(router)
	server.Config.BaseContext = func(net.Listener) context.Context { return ctx }
	server.Config.ReadHeaderTimeout = 5 * time.Second
	server.Start()
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	return server
}
