package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCarpoolQuotaRoutesRejectCallerSuppliedIdentityAndUnboundRequests(t *testing.T) {
	h := NewCarpoolControlHandler(&carpool.ControlStore{}, &carpool.NodeControl{}, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		c.Next()
	})
	r.GET("/cars/:id/subscription", h.CarSubscription)
	r.GET("/native/quota", h.NativeQuota)
	r.POST("/native/quota/sync", h.SyncNativeQuota)
	for _, path := range []string{"/cars/car/subscription?user_id=2", "/cars/car/subscription?group_id=3", "/cars/car/subscription?subject=other", "/native/quota"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		require.Equal(t, 400, w.Code, path)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/native/quota/sync", nil))
	require.Equal(t, 400, w.Code)
}
