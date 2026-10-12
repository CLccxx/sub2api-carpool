package handler

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type controlStatusStub struct {
	actor int64
	car   string
	calls int
}

func (s *controlStatusStub) ConsoleForMember(_ context.Context, actor int64, car string) (carpool.ControlConsole, error) {
	s.actor, s.car, s.calls = actor, car, s.calls+1
	return carpool.ControlConsole{CarID: car, AccessStatus: "unactivated"}, nil
}
func (s *controlStatusStub) ControlSyncStatus(_ context.Context, car string, _ carpool.PageQuery) (carpool.ControlSyncPage, error) {
	s.car, s.calls = car, s.calls+1
	return carpool.ControlSyncPage{Items: []carpool.ControlSyncMember{}}, nil
}
func (s *controlStatusStub) RetryControlSync(_ context.Context, actor int64, car string) error {
	s.actor, s.car, s.calls = actor, car, s.calls+1
	return nil
}

func TestControlStatusHTTPStrictScopedAndNoStore(t *testing.T) {
	s := &controlStatusStub{}
	h := &CarpoolControlHandler{main: &controlHandlerFake{}, sync: s}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 22})
		c.Next()
	})
	r.GET("/cars/:id/console", h.MemberConsole)
	r.GET("/cars/:id/sync", h.SyncStatus)
	r.POST("/cars/:id/sync/retry", h.RetrySync)
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"GET", "/cars/own/console?user_id=1", "", 400},
		{"GET", "/cars/own/sync?node_id=other", "", 400},
		{"GET", "/cars/own/sync?page_size=101", "", 400},
		{"POST", "/cars/own/sync/retry?activate=true", "{}", 400},
		{"POST", "/cars/own/sync/retry", `{"status":"active"}`, 400},
		{"GET", "/cars/own/console", "", 200},
		{"GET", "/cars/own/sync", "", 200},
		{"POST", "/cars/own/sync/retry", "{}", 200},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		require.Equal(t, tc.want, w.Code, w.Body.String())
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	}
	require.Equal(t, 3, s.calls)
	require.EqualValues(t, 22, s.actor)
	require.Equal(t, "own", s.car)
}

func TestControlStatusHTTPDisabledAndUnauthenticated(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		h := &CarpoolControlHandler{}
		want := 404
		if enabled {
			h.main = &controlHandlerFake{}
			h.sync = &controlStatusStub{}
			want = 401
		}
		r := gin.New()
		r.GET("/:id", h.MemberConsole)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/own", nil))
		require.Equal(t, want, w.Code)
	}
}
