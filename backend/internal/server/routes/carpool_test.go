package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

var carpoolRoutes = []struct{ method, path string }{
	{"GET", "/carpool/cars"}, {"GET", "/carpool/cars/c"}, {"POST", "/carpool/cars/c/reservations"},
	{"GET", "/carpool/orders/o"}, {"POST", "/carpool/orders/o/cancel"}, {"GET", "/carpool/mine"},
	{"GET", "/admin/carpool/cars"}, {"POST", "/admin/carpool/cars"}, {"GET", "/admin/carpool/cars/c"},
	{"PATCH", "/admin/carpool/cars/c"}, {"POST", "/admin/carpool/cars/c/publish"},
	{"POST", "/admin/carpool/cars/c/cancel"}, {"GET", "/admin/carpool/cars/c/members"}, {"POST", "/admin/carpool/nodes"},
	{"POST", "/admin/carpool/cars/c/members"}, {"GET", "/admin/carpool/orders"}, {"GET", "/admin/carpool/orders/o"},
	{"POST", "/admin/carpool/orders/o/withdraw"}, {"POST", "/admin/carpool/orders/o/refund"},
}

func carpoolRouteHandlers(enabled, node bool) *handler.Handlers {
	cfg := &config.Config{CarpoolMain: carpool.MainConfig{Enabled: enabled}, CarpoolNode: carpool.Config{Enabled: node}}
	return &handler.Handlers{Admin: &handler.AdminHandlers{}, Carpool: handler.NewCarpoolHandler(carpool.NewMainService(nil, cfg.CarpoolMain), cfg)}
}

func TestCarpoolRoutesAbsentWhenDisabledOrNode(t *testing.T) {
	for _, h := range []*handler.Handlers{{}, carpoolRouteHandlers(false, false), carpoolRouteHandlers(true, true)} {
		r := gin.New()
		registerCarpoolUserRoutes(r.Group(""), h)
		registerCarpoolAdminRoutes(r.Group("/admin"), h)
		require.Empty(t, r.Routes())
	}
}

func TestCarpoolRoutesUseExistingAuthGroups(t *testing.T) {
	r := gin.New()
	h := carpoolRouteHandlers(true, false)
	deny := func(c *gin.Context) {
		if c.GetHeader("Authorization") == "user" {
			c.AbortWithStatus(403)
		} else {
			c.AbortWithStatus(401)
		}
	}
	next := func(c *gin.Context) { c.Next() }
	RegisterUserRoutes(r.Group("/api/v1"), h, middleware.JWTAuthMiddleware(deny), middleware.AuditLogMiddleware(next), nil, nil)
	RegisterAdminRoutes(r.Group("/api/v1"), h, middleware.AdminAuthMiddleware(deny), middleware.AuditLogMiddleware(next), middleware.StepUpAuthMiddleware(next), nil, nil)
	for _, route := range carpoolRoutes {
		for _, auth := range []string{"", "user"} {
			req := httptest.NewRequest(route.method, "/api/v1"+route.path, nil)
			req.Header.Set("Authorization", auth)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			want := 401
			if auth == "user" {
				want = 403
			}
			require.Equal(t, want, w.Code, route.path)
		}
	}
	for _, route := range r.Routes() {
		require.False(t, strings.Contains(route.Path, "carpool") && strings.Contains(route.Path, "confirm"), route.Path)
	}
}

type carpoolComplianceRepo struct{ service.SettingRepository }

func (*carpoolComplianceRepo) GetValue(context.Context, string) (string, error) {
	return "", service.ErrSettingNotFound
}

func TestCarpoolAdminComplianceAndAudit(t *testing.T) {
	r := gin.New()
	var audited int
	auth := func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		c.Next()
	}
	audit := func(c *gin.Context) { audited++; c.Next() }
	settings := service.NewSettingService(&carpoolComplianceRepo{}, &config.Config{})
	RegisterAdminRoutes(r.Group("/api/v1"), carpoolRouteHandlers(true, false), middleware.AdminAuthMiddleware(auth), middleware.AuditLogMiddleware(audit), middleware.StepUpAuthMiddleware(auth), settings, nil)
	for _, route := range carpoolRoutes {
		if !strings.HasPrefix(route.path, "/admin/") {
			continue
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(route.method, "/api/v1"+route.path, nil))
		require.Equal(t, http.StatusLocked, w.Code, w.Body.String())
	}
	require.Equal(t, 13, audited)
}
