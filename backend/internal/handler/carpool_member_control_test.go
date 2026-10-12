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

type memberControlStub struct {
	calls          int
	actor          int64
	car, operation string
}

func (s *memberControlStub) CreateMemberKey(_ context.Context, actor int64, car, operation, name string) (carpool.ManagedKey, error) {
	s.calls++
	s.actor = actor
	s.car = car
	s.operation = operation
	return carpool.ManagedKey{ID: 1, Name: name, Key: "synthetic-secret"}, nil
}
func (s *memberControlStub) UpdateMemberKey(_ context.Context, actor int64, car string, id int64, input carpool.ManagedKeyUpdate) (carpool.ManagedKey, error) {
	s.calls++
	s.actor = actor
	s.car = car
	return carpool.ManagedKey{ID: id, Key: "must-not-forward"}, nil
}
func (s *memberControlStub) DeleteMemberKey(_ context.Context, actor int64, car string, id int64) error {
	s.calls++
	s.actor = actor
	s.car = car
	return nil
}
func (s *memberControlStub) ListMemberKeys(_ context.Context, actor int64, car string, q carpool.ControlPageQuery) (carpool.ControlPage[carpool.ManagedKey], error) {
	s.calls++
	s.actor = actor
	s.car = car
	return carpool.ControlPage[carpool.ManagedKey]{Items: []carpool.ManagedKey{}}, nil
}
func (s *memberControlStub) MemberUsage(_ context.Context, actor int64, car string, q carpool.ControlUsageQuery) (carpool.ControlPage[carpool.ManagedUsage], error) {
	s.calls++
	s.actor = actor
	s.car = car
	return carpool.ControlPage[carpool.ManagedUsage]{Items: []carpool.ManagedUsage{}}, nil
}
func (s *memberControlStub) MemberUsageStats(_ context.Context, actor int64, car string, q carpool.ControlUsageQuery) (carpool.ManagedUsageStats, error) {
	s.calls++
	s.actor = actor
	s.car = car
	return carpool.ManagedUsageStats{}, nil
}

func TestMemberControlHTTPUsesActorAndRejectsExtraInput(t *testing.T) {
	stub := &memberControlStub{}
	h := &CarpoolControlHandler{main: &controlHandlerFake{}, members: stub}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 22})
		c.Next()
	})
	r.POST("/cars/:id/keys", h.CreateCarKey)
	r.PATCH("/cars/:id/keys/:key_id", h.UpdateCarKey)
	r.DELETE("/cars/:id/keys/:key_id", h.DeleteCarKey)
	r.GET("/cars/:id/keys", h.MemberKeys)
	r.GET("/cars/:id/usage", h.CarUsage)
	r.GET("/cars/:id/usage/stats", h.CarUsageStats)
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/cars/my-car/keys", `{"name":"one","actor_id":1}`, 400},
		{"POST", "/cars/my-car/keys?node_id=other", `{"name":"one"}`, 400},
		{"PATCH", "/cars/my-car/keys/1", `{"status":"active","group_id":2}`, 400},
		{"DELETE", "/cars/my-car/keys/1", `{"user_id":1}`, 400},
		{"GET", "/cars/my-car/keys?user_id=1", "", 400},
		{"GET", "/cars/my-car/keys?page_size=101", "", 400},
		{"GET", "/cars/my-car/keys?page=1&page=2", "", 400},
		{"GET", "/cars/my-car/usage?start_date=2026-09-01&end_date=2026-09-19&url=https%3A%2F%2Fother.invalid", "", 400},
		{"GET", "/cars/my-car/usage?start_date=2026-01-01&end_date=2026-09-19", "", 400},
		{"POST", "/cars/my-car/keys", `{"name":"one"}`, 200},
		{"PATCH", "/cars/my-car/keys/1", `{"name":"two"}`, 200},
		{"DELETE", "/cars/my-car/keys/1", "", 200},
		{"GET", "/cars/my-car/keys", "", 200},
		{"GET", "/cars/my-car/usage?start_date=2026-09-01&end_date=2026-09-19", "", 200},
		{"GET", "/cars/my-car/usage/stats?start_date=2026-09-01&end_date=2026-09-19", "", 200},
	} {
		t.Run(tc.method+tc.path+tc.body, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Idempotency-Key", "kept-operation")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, tc.want, w.Code, w.Body.String())
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			require.NotContains(t, w.Body.String(), "must-not-forward")
		})
	}
	require.Equal(t, 6, stub.calls)
	require.EqualValues(t, 22, stub.actor)
	require.Equal(t, "my-car", stub.car)
	require.Equal(t, "kept-operation", stub.operation)
}
