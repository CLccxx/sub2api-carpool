//go:build integration

package carpool_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
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
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// All fixture transitions and loopback transport overrides are test-binary only.
// The browser exercises real A handlers, PostgreSQL and native B/C control APIs.
// It does not exercise the model gateway or external SMTP.
func TestControlBrowserNative(t *testing.T) {
	preview := os.Getenv("CARPOOL_CONTROL_PREVIEW") == "1"
	nativeFlow := os.Getenv("CARPOOL_NATIVE_SUBSCRIPTIONS_BROWSER") == "1"
	backgroundSync := preview || os.Getenv("CARPOOL_CONTROL_BACKGROUND_SYNC") == "1"
	if os.Getenv("CARPOOL_CONTROL_BROWSER") != "1" && !preview {
		t.Skip("set CARPOOL_CONTROL_BROWSER=1 for the isolated A/B/C browser test")
	}
	duration := 6 * time.Minute
	if preview {
		duration = 8 * time.Hour
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	gin.SetMode(gin.TestMode)
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../../.."))
	dist := filepath.Join(root, "backend/internal/web/dist")
	_, err := os.Stat(filepath.Join(dist, "index.html"))
	require.NoError(t, err)
	artifacts, err := os.MkdirTemp("", "carpool-control-browser-")
	require.NoError(t, err)
	require.NoError(t, os.Chmod(artifacts, 0700))
	fmt.Fprintln(os.Stdout, "Control browser artifacts:", artifacts)
	lab := newNativeControlLab(t)
	member := lab.user(t, "user")
	other := lab.user(t, "user")
	password := "Synthetic-" + uuid.NewString()
	members := []*service.User{member, other, lab.user(t, "user"), lab.user(t, "user")}
	people := append([]*service.User{lab.admin}, members...)
	for _, user := range people {
		user.PasswordHash, err = lab.auth.HashPassword(password)
		require.NoError(t, err)
		require.NoError(t, lab.users.Update(ctx, user, service.UserUpdateFields{PasswordHash: true}))
	}
	cfg := &config.Config{RunMode: config.RunModeStandard, CarpoolMain: carpool.MainConfig{Enabled: true, ManualEnrollmentEnabled: true}, CarpoolControl: carpool.ControlConfig{Enabled: true}}
	main := carpool.NewMainService(lab.db, cfg.CarpoolMain)
	makeCar := func(name string, capacity, enrolled int) carpool.Car {
		total := int64(140000)
		car, e := main.CreateCar(ctx, lab.admin.ID, carpool.CarInput{Name: name, Capacity: capacity, TotalPriceMinor: &total, Currency: "CNY", ServiceDays: 30, RecruitUntil: time.Now().Add(24 * time.Hour), DeliveryHours: 24})
		require.NoError(t, e)
		car, e = main.PublishCar(ctx, lab.admin.ID, car.ID)
		require.NoError(t, e)
		for _, user := range members[:enrolled] {
			_, e = main.EnrollManual(ctx, lab.admin.ID, car.ID, uuid.NewString(), carpool.ManualEnrollmentInput{UserID: user.ID, AmountMinor: car.PriceMinor, Channel: "wechat", ReceivedAt: time.Now().Add(-time.Minute), ConfirmReceived: true})
			require.NoError(t, e)
		}
		return car
	}
	carB := makeCar("B four-member synthetic car with a deliberately long name for narrow screens", 4, 4)
	carC := makeCar("C two-member synthetic car", 2, 2)
	carP := makeCar("Three-member recruiting synthetic car", 3, 1)
	registeredB, err := main.RegisterNode(ctx, lab.admin.ID, carpool.NodeInput{CarID: carB.ID, Hostname: "b.browser-lab.invalid"})
	require.NoError(t, err)
	registeredC, err := main.RegisterNode(ctx, lab.admin.ID, carpool.NodeInput{CarID: carC.ID, Hostname: "c.browser-lab.invalid"})
	require.NoError(t, err)
	store, err := carpool.NewControlStore(ctx, main, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32)), []string{"https://" + registeredB.Hostname, "https://" + registeredC.Hostname})
	require.NoError(t, err)
	roots := x509.NewCertPool()
	b := newNativeControlNode(t, store.MainID(), registeredB, roots)
	c := newNativeControlNode(t, store.MainID(), registeredC, roots)
	if nativeFlow {
		for _, node := range []nativeControlNode{b, c} {
			now := time.Now().UTC()
			extra, e := json.Marshal(map[string]any{"codex_usage_updated_at": now.Format(time.RFC3339Nano), "codex_5h_used_percent": 0, "codex_5h_reset_at": now.Add(2 * time.Hour).Format(time.RFC3339Nano), "codex_7d_used_percent": 0, "codex_7d_reset_at": now.Add(2 * 24 * time.Hour).Format(time.RFC3339Nano), "codex_7d_observation": map[string]any{"observed_at": now, "resets_at": now.Add(2 * 24 * time.Hour), "utilization": 0}})
			require.NoError(t, e)
			_, err = node.db.Exec(`INSERT INTO accounts(name,platform,type,credentials,status,schedulable,extra) VALUES('Synthetic OAuth account','openai','oauth','{}','active',true,$1::jsonb)`, string(extra))
			require.NoError(t, err)
		}
	}
	addresses := map[string]string{registeredB.Hostname + ":443": b.server.Listener.Addr().String(), registeredC.Hostname + ":443": c.server.Listener.Addr().String()}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		target, exists := addresses[address]
		if !exists {
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
		_, err = store.SaveConnection(ctx, lab.admin.ID, pair.registered.ID, carpool.ControlConnectionInput{Origin: "https://" + pair.registered.Hostname, InstanceID: pair.remote.node.Info().InstanceID, AdminKey: pair.remote.adminKey})
		require.NoError(t, err)
		_, err = store.CheckConnection(ctx, lab.admin.ID, pair.registered.ID)
		require.NoError(t, err)
	}
	drain := func() error {
		if e := store.ReconcileControl(ctx); e != nil {
			return e
		}
		for i := 0; i < 150; i++ {
			worked, e := store.SyncNext(ctx)
			if e != nil {
				return e
			}
			if !worked {
				// SKIP LOCKED can report no claim while a UI status read holds a row.
				// Unlike the production worker this fixture has no next poll unless we wait.
				var pending int
				e = lab.db.QueryRowContext(ctx, `SELECT count(*) FROM carpool_control_grants g JOIN carpool_node_connections c ON c.node_id=g.node_id WHERE c.status='ready' AND g.delivery IN ('pending','leased')`).Scan(&pending)
				if e != nil {
					return e
				}
				if pending == 0 {
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(20 * time.Millisecond):
				}
			}
		}
		return fmt.Errorf("browser fixture synchronization did not settle")
	}
	require.NoError(t, drain())
	if backgroundSync {
		worker := service.ProvideCarpoolControlWorker(store)
		t.Cleanup(worker.Stop)
	}
	settingsRepo := repository.NewSettingRepository(lab.ent)
	for key, value := range map[string]string{service.SettingKeySiteName: "Control Browser Lab", service.SettingKeyCustomMenuItems: "[]", service.SettingKeyCustomEndpoints: "[]", service.SettingKeyChannelMonitorEnabled: "false", service.SettingKeySubscriptionEnabled: "false"} {
		require.NoError(t, settingsRepo.Set(ctx, key, value))
	}
	settings := service.NewSettingService(settingsRepo, cfg)
	users := service.NewUserService(lab.users, nil, nil, nil)
	auth := handler.NewAuthHandler(cfg, lab.auth, users, settings, nil, nil, nil, nil)
	h := handler.NewCarpoolHandler(main, cfg)
	control := handler.NewCarpoolControlHandler(store, nil, nil)
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/setup/status", func(c *gin.Context) { response.Success(c, gin.H{"needs_setup": false}) })
	r.GET("/api/v1/settings/public", handler.NewSettingHandler(settings, "local-control-test").GetPublicSettings)
	r.POST("/api/v1/auth/login", auth.Login)
	userRoutes := r.Group("/api/v1", gin.HandlerFunc(middleware.NewJWTAuthMiddleware(lab.auth, users, settings, nil)))
	userRoutes.GET("/auth/me", auth.GetCurrentUser)
	userRoutes.GET("/carpool/cars", h.ListCars)
	userRoutes.GET("/carpool/cars/:id", h.GetCar)
	userRoutes.POST("/carpool/cars/:id/reservations", h.Reserve)
	userRoutes.GET("/carpool/orders/:id", h.GetOrder)
	userRoutes.POST("/carpool/orders/:id/cancel", h.CancelOrder)
	userRoutes.GET("/carpool/mine", h.Mine)
	userRoutes.GET("/carpool/cars/:id/console", control.MemberConsole)
	userRoutes.GET("/carpool/cars/:id/subscription", control.CarSubscription)
	userRoutes.GET("/carpool/cars/:id/keys", control.MemberKeys)
	userRoutes.POST("/carpool/cars/:id/keys", control.CreateCarKey)
	userRoutes.PATCH("/carpool/cars/:id/keys/:key_id", control.UpdateCarKey)
	userRoutes.DELETE("/carpool/cars/:id/keys/:key_id", control.DeleteCarKey)
	userRoutes.GET("/carpool/cars/:id/usage", control.CarUsage)
	userRoutes.GET("/carpool/cars/:id/usage/stats", control.CarUsageStats)
	for _, endpoint := range []string{"/announcements", "/subscriptions/active", "/subscriptions/progress"} {
		userRoutes.GET(endpoint, func(c *gin.Context) { response.Success(c, []any{}) })
	}
	admin := r.Group("/api/v1/admin", gin.HandlerFunc(middleware.NewAdminAuthMiddleware(lab.auth, users, settings, nil)))
	admin.GET("/carpool/cars", h.AdminListCars)
	admin.POST("/carpool/cars", h.CreateCar)
	admin.GET("/carpool/cars/:id", h.AdminGetCar)
	admin.PATCH("/carpool/cars/:id", h.UpdateCar)
	admin.POST("/carpool/cars/:id/publish", h.PublishCar)
	admin.POST("/carpool/cars/:id/cancel", h.CancelCar)
	admin.GET("/carpool/cars/:id/members", h.ListMembers)
	admin.POST("/carpool/cars/:id/members", h.EnrollManual)
	admin.GET("/carpool/orders", h.AdminListManualOrders)
	admin.GET("/carpool/orders/:id", h.AdminGetManualOrder)
	admin.POST("/carpool/orders/:id/withdraw", h.WithdrawManual)
	admin.POST("/carpool/orders/:id/refund", h.RefundManual)
	admin.GET("/users", func(g *gin.Context) {
		items, page, e := lab.users.ListWithFilters(g.Request.Context(), pagination.DefaultPagination(), service.UserListFilters{Status: service.StatusActive, Search: g.Query("search")})
		if response.ErrorFrom(g, e) {
			return
		}
		profiles := make([]*dto.User, 0, len(items))
		for i := range items {
			profiles = append(profiles, dto.UserFromService(&items[i]))
		}
		response.Success(g, gin.H{"items": profiles, "total": page.Total, "page": page.Page, "page_size": page.PageSize, "pages": page.Pages})
	})
	admin.GET("/users/:id", func(g *gin.Context) {
		id, e := strconv.ParseInt(g.Param("id"), 10, 64)
		if e != nil || id <= 0 {
			response.BadRequest(g, "Invalid user ID")
			return
		}
		user, e := users.GetByID(g.Request.Context(), id)
		if !response.ErrorFrom(g, e) {
			response.Success(g, dto.UserFromService(user))
		}
	})
	admin.POST("/carpool/nodes", h.RegisterNode)
	admin.GET("/carpool/control", control.MainInfo)
	admin.GET("/carpool/cars/:id/connection", control.Connection)
	admin.GET("/carpool/cars/:id/sync", control.SyncStatus)
	admin.GET("/carpool/cars/:id/native", control.CarNativeStatus)
	admin.GET("/carpool/cars/:id/native/accounts", control.CarNativeAccounts)
	admin.GET("/carpool/cars/:id/native/quota", control.CarQuota)
	stepUp := gin.HandlerFunc(middleware.NewStepUpAuthMiddleware(nil, users, settings))
	admin.PUT("/carpool/nodes/:id/connection", stepUp, control.SaveConnection)
	admin.POST("/carpool/nodes/:id/connection/check", stepUp, control.CheckConnection)
	admin.POST("/carpool/cars/:id/sync/retry", stepUp, control.RetrySync)
	admin.POST("/carpool/cars/:id/native/prepare", stepUp, control.PrepareNativeCar)
	admin.POST("/carpool/cars/:id/native/activate", stepUp, control.ActivateNativeCar)
	admin.POST("/carpool/cars/:id/native/quota/sync", stepUp, control.SyncCarQuota)
	admin.POST("/__test__/drain", func(g *gin.Context) {
		if !response.ErrorFrom(g, drain()) {
			response.Success(g, gin.H{"fixture": true})
		}
	})
	// No declaration is accepted. Production compliance/step-up denial routes have separate tests.
	admin.GET("/compliance", func(c *gin.Context) { response.Success(c, gin.H{"required": false}) })
	for _, endpoint := range []string{"/settings", "/payment/config", "/system/version", "/system/check-updates"} {
		admin.GET(endpoint, func(c *gin.Context) {
			response.Success(c, gin.H{"enabled": false, "has_update": false, "custom_menu_items": []any{}})
		})
	}
	admin.POST("/__test__/activate", func(g *gin.Context) {
		_, e := lab.db.ExecContext(ctx, `UPDATE carpool_cars SET status='active',service_start=clock_timestamp()-INTERVAL '1 hour',service_end=clock_timestamp()+INTERVAL '1 hour' WHERE id IN ($1,$2);`, carB.ID, carC.ID)
		if e == nil {
			_, e = lab.db.ExecContext(ctx, `UPDATE carpool_memberships m SET status='active',service_start=c.service_start,service_end=c.service_end FROM carpool_cars c WHERE c.id=m.car_id AND c.status='active'`)
		}
		if e == nil {
			e = drain()
		}
		if !response.ErrorFrom(g, e) {
			response.Success(g, gin.H{"fixture": true})
		}
	})
	admin.POST("/__test__/stop-c", func(g *gin.Context) { c.server.Close(); response.Success(g, gin.H{"fixture": true}) })
	if preview {
		r.GET("/__preview/:role", func(g *gin.Context) {
			user := member
			if g.Param("role") == "admin" {
				user = lab.admin
			} else if g.Param("role") != "member" {
				g.AbortWithStatus(404)
				return
			}
			lab.auth.RecordSuccessfulLogin(g.Request.Context(), user.ID)
			token, e := lab.auth.GenerateToken(g.Request.Context(), user)
			if response.ErrorFrom(g, e) {
				return
			}
			profile, e := json.Marshal(gin.H{"token": token, "user": dto.UserFromService(user)})
			if response.ErrorFrom(g, e) {
				return
			}
			g.Header("Cache-Control", "no-store")
			g.Header("Referrer-Policy", "no-referrer")
			g.Data(200, "text/html; charset=utf-8", []byte(`<!doctype html><meta charset="utf-8"><title>Local control preview</title><script>
const {token,user}=`+string(profile)+`;
localStorage.removeItem('refresh_token');localStorage.removeItem('token_expires_at');
for (const key of Object.keys(sessionStorage)) if (key.startsWith('carpool:manual:')||key.startsWith('carpool:node-key:')) sessionStorage.removeItem(key);
localStorage.setItem('auth_token',token);localStorage.setItem('auth_user',JSON.stringify(user));localStorage.setItem('sub2api_locale','zh');
localStorage.setItem((user.role==='admin'?'admin':'user')+'_guide_'+user.id+'_'+user.role+'_v4_interactive','true');
location.replace(user.role==='admin'?'/admin/carpool':'/carpool/mine');</script>`))
		})
	}
	files := os.DirFS(dist)
	static := http.FileServer(http.FS(files))
	r.NoRoute(func(g *gin.Context) {
		clean := path.Clean("/" + g.Request.URL.Path)
		if strings.HasPrefix(clean, "/api/") || (g.Request.Method != "GET" && g.Request.Method != "HEAD") {
			g.AbortWithStatus(404)
			return
		}
		if info, e := fs.Stat(files, strings.TrimPrefix(clean, "/")); e == nil && info.Mode().IsRegular() {
			static.ServeHTTP(g.Writer, g.Request)
			return
		}
		if path.Ext(clean) != "" {
			g.AbortWithStatus(404)
			return
		}
		g.File(filepath.Join(dist, "index.html"))
	})
	server := httptest.NewServer(r)
	t.Cleanup(server.Close)
	if preview {
		fmt.Fprintln(os.Stdout, "Prepared-only local preview admin:", server.URL+"/__preview/admin")
		fmt.Fprintln(os.Stdout, "Prepared-only local preview member:", server.URL+"/__preview/member")
		stopCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
		<-stopCtx.Done()
		return
	}
	peopleJSON := make([]gin.H, 0, len(people))
	for _, user := range people {
		peopleJSON = append(peopleJSON, gin.H{"id": user.ID, "email": user.Email, "password": password, "role": user.Role})
	}
	input, err := json.Marshal(gin.H{"site": server.URL, "users": peopleJSON, "cars": []carpool.Car{carB, carC, carP}, "artifacts": artifacts, "nativeFlow": nativeFlow, "backgroundSync": backgroundSync})
	require.NoError(t, err)
	cmd := exec.CommandContext(ctx, "node", filepath.Join(root, "tools/carpool/control-browser.mjs"))
	cmd.Dir = root
	cmd.Stdin = bytes.NewReader(input)
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	require.NoError(t, os.WriteFile(filepath.Join(artifacts, "browser.stderr.log"), []byte(strings.ReplaceAll(stderr.String(), password, "[redacted]")), 0600))
	fmt.Fprint(os.Stdout, stdout.String())
	require.NoError(t, runErr, "inspect private browser.stderr.log for details")
}
