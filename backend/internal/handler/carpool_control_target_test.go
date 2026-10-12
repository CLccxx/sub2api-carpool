package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestControlHeadersCannotReachUnboundNativeAdminReads(t *testing.T) {
	for _, node := range []*carpool.NodeControl{nil, {}} {
		h := &CarpoolControlHandler{node: node}
		r := gin.New()
		calls := 0
		r.Use(h.EnforceControlTarget)
		r.GET("/api/v1/admin/users/101/api-keys", func(c *gin.Context) { calls++; c.Status(200) })
		for _, header := range []string{carpool.ControlHeaderMain, carpool.ControlHeaderNode, carpool.ControlHeaderCar, carpool.ControlHeaderInstance} {
			req := httptest.NewRequest("GET", "/api/v1/admin/users/101/api-keys", nil)
			req.Header.Set(header, "stale-instance")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, 400, w.Code, w.Body.String())
		}
		require.Zero(t, calls)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/admin/users/101/api-keys", nil))
		require.Equal(t, 200, w.Code, "normal admin clients without delegation headers are unchanged")
		require.Equal(t, 1, calls)
	}
}
