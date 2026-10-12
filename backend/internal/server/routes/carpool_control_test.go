package routes

import (
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

var controlRoutes = []struct{ method, path string }{
	{"GET", "/api/v1/admin/carpool/control"},
	{"GET", "/api/v1/admin/carpool/cars/c/connection"},
	{"PUT", "/api/v1/admin/carpool/nodes/n/connection"},
	{"POST", "/api/v1/admin/carpool/nodes/n/connection/check"},
	{"GET", "/api/v1/admin/carpool-control/v1/info"},
	{"PUT", "/api/v1/admin/carpool-control/v1/members/member"},
	{"PUT", "/api/v1/admin/carpool-control/v1/members/member/grant"},
	{"POST", "/api/v1/admin/carpool-control/v1/members/member/keys"},
	{"PATCH", "/api/v1/admin/carpool-control/v1/members/member/keys/7"},
	{"DELETE", "/api/v1/admin/carpool-control/v1/members/member/keys/7"},
	{"GET", "/api/v1/admin/carpool/cars/c/sync"},
	{"POST", "/api/v1/admin/carpool/cars/c/sync/retry"},
	{"GET", "/api/v1/admin/carpool/cars/c/native"},
	{"GET", "/api/v1/admin/carpool/cars/c/native/accounts"},
	{"POST", "/api/v1/admin/carpool/cars/c/native/prepare"},
	{"POST", "/api/v1/admin/carpool/cars/c/native/activate"},
	{"GET", "/api/v1/admin/carpool-control/v1/accounts"},
	{"POST", "/api/v1/admin/carpool-control/v1/subscription/initialize"},
	{"GET", "/api/v1/admin/carpool-control/v1/subscription/quota"},
	{"POST", "/api/v1/admin/carpool-control/v1/subscription/quota/sync"},
	{"GET", "/api/v1/admin/carpool-control/v1/members/member/subscription"},
	{"GET", "/api/v1/admin/carpool/cars/c/native/quota"},
	{"POST", "/api/v1/admin/carpool/cars/c/native/quota/sync"},
}

func TestCarpoolControlRoutesDisabledAndProtected(t *testing.T) {
	next := func(c *gin.Context) { c.Next() }
	r := gin.New()
	registerCarpoolControlAdminRoutes(r.Group("/api/v1/admin"), &handler.Handlers{}, middleware.StepUpAuthMiddleware(next))
	require.Empty(t, r.Routes())
	h := carpoolRouteHandlers(true, false)
	// These non-nil zero services only exercise middleware; blocked requests never use them.
	h.CarpoolControl = handler.NewCarpoolControlHandler(&carpool.ControlStore{}, &carpool.NodeControl{}, nil)
	for _, status := range []int{401, 403} {
		r := gin.New()
		deny := func(c *gin.Context) { c.AbortWithStatus(status) }
		RegisterAdminRoutes(r.Group("/api/v1"), h, middleware.AdminAuthMiddleware(deny), middleware.AuditLogMiddleware(next), middleware.StepUpAuthMiddleware(next), nil, nil)
		for _, route := range controlRoutes {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
			require.Equal(t, status, w.Code, route.path)
		}
	}
	r = gin.New()
	settings := service.NewSettingService(&carpoolComplianceRepo{}, &config.Config{})
	authenticated := func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		c.Next()
	}
	RegisterAdminRoutes(r.Group("/api/v1"), h, middleware.AdminAuthMiddleware(authenticated), middleware.AuditLogMiddleware(next), middleware.StepUpAuthMiddleware(next), settings, nil)
	for _, route := range controlRoutes {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
		require.Equal(t, 423, w.Code, route.path)
	}
	r = gin.New()
	stepUpDenied := middleware.StepUpAuthMiddleware(func(c *gin.Context) { c.AbortWithStatus(403) })
	registerCarpoolControlAdminRoutes(r.Group("/api/v1/admin"), h, stepUpDenied)
	for _, route := range []struct{ method, path string }{controlRoutes[2], controlRoutes[3], controlRoutes[11], controlRoutes[14], controlRoutes[15], controlRoutes[22]} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
		require.Equal(t, 403, w.Code, "credential changes/checks must use step-up middleware")
	}
}

func TestCarpoolMemberControlRoutesUseAuthAndHeavyLimiter(t *testing.T) {
	h := carpoolRouteHandlers(true, false)
	h.CarpoolControl = handler.NewCarpoolControlHandler(&carpool.ControlStore{}, nil, nil)
	paths := []struct{ method, path string }{
		{"GET", "/carpool/cars/c/keys"}, {"POST", "/carpool/cars/c/keys"},
		{"PATCH", "/carpool/cars/c/keys/1"}, {"DELETE", "/carpool/cars/c/keys/1"},
		{"GET", "/carpool/cars/c/usage"}, {"GET", "/carpool/cars/c/usage/stats"},
		{"GET", "/carpool/cars/c/console"},
		{"GET", "/carpool/cars/c/subscription"},
	}
	r := gin.New()
	registerCarpoolUserRoutes(r.Group("", func(c *gin.Context) { c.AbortWithStatus(401) }), h)
	for _, route := range paths {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
		require.Equal(t, 401, w.Code, route.path)
	}
	r = gin.New()
	registerCarpoolUserRoutes(r.Group(""), h, func(c *gin.Context) { c.AbortWithStatus(429) })
	for _, route := range paths[4:6] {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
		require.Equal(t, 429, w.Code)
	}
	r = gin.New()
	h.CarpoolControl = nil
	registerCarpoolUserRoutes(r.Group(""), h)
	for _, route := range paths {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
		require.Equal(t, 404, w.Code)
	}
}
