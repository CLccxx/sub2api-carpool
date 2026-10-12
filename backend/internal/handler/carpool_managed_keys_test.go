package handler

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type managedKeysStub struct {
	calls     int
	operation string
	target    carpool.ManagedTarget
}

func (s *managedKeysStub) Create(_ context.Context, target carpool.ManagedTarget, subject, operation, name string) (service.ManagedKey, error) {
	s.calls++
	s.operation = operation
	s.target = target
	return service.ManagedKey{ID: 7, Name: name, Key: "synthetic-secret"}, nil
}
func (s *managedKeysStub) Update(_ context.Context, _ carpool.ManagedTarget, _ string, _ int64, input service.ManagedKeyUpdate) (service.ManagedKey, error) {
	s.calls++
	return service.ManagedKey{ID: 7, Name: *input.Name}, nil
}
func (s *managedKeysStub) Delete(context.Context, carpool.ManagedTarget, string, int64) error {
	s.calls++
	return nil
}

func TestManagedKeyHTTPStrictBodyAndNoStore(t *testing.T) {
	stub := &managedKeysStub{}
	h := &CarpoolControlHandler{node: &carpool.NodeControl{}, keys: stub}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		c.Next()
	})
	r.POST("/members/:subject/keys", h.CreateMemberKey)
	r.PATCH("/members/:subject/keys/:key_id", h.UpdateMemberKey)
	r.DELETE("/members/:subject/keys/:key_id", h.DeleteMemberKey)
	for _, tc := range []struct {
		method, path, body string
		code               int
	}{
		{"POST", "/members/test/keys", `{"name":"one","user_id":2}`, 400},
		{"POST", "/members/test/keys", `{"name":"one","group_id":2}`, 400},
		{"POST", "/members/test/keys", `{"name":"one","custom_key":"mine"}`, 400},
		{"POST", "/members/test/keys", `{"name":"one"} {}`, 400},
		{"POST", "/members/test/keys", `null`, 400},
		{"PATCH", "/members/test/keys/7", `{"name":"one","quota":999}`, 400},
		{"PATCH", "/members/test/keys/7", `{"name":"one","expires_at":null}`, 400},
		{"PATCH", "/members/test/keys/nope", `{"name":"one"}`, 400},
		{"DELETE", "/members/test/keys/7", `{"user_id":2}`, 400},
		{"POST", "/members/test/keys", `{"name":"one"}`, 200},
		{"PATCH", "/members/test/keys/7", `{"name":"two"}`, 200},
		{"DELETE", "/members/test/keys/7", ``, 200},
	} {
		t.Run(tc.method+tc.path+tc.body, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Idempotency-Key", "synthetic-operation")
			req.Header.Set(carpool.ControlHeaderMain, "main-test")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, tc.code, w.Code, w.Body.String())
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			if tc.method != "POST" {
				require.NotContains(t, w.Body.String(), "synthetic-secret")
			}
		})
	}
	require.Equal(t, 3, stub.calls)
	require.Equal(t, "synthetic-operation", stub.operation)
	require.Equal(t, "main-test", stub.target.MainID)
}
