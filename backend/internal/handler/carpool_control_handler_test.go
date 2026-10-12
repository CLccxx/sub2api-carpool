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

type controlHandlerFake struct {
	actor  int64
	called bool
	input  carpool.ControlConnectionInput
}

func (*controlHandlerFake) MainID() string { return "main-a" }
func (f *controlHandlerFake) ConnectionForCar(context.Context, string) (carpool.ControlConnection, error) {
	f.called = true
	return carpool.ControlConnection{CredentialMask: "********", CredentialConfigured: true}, nil
}
func (f *controlHandlerFake) SaveConnection(_ context.Context, actor int64, _ string, input carpool.ControlConnectionInput) (carpool.ControlConnection, error) {
	f.called, f.actor, f.input = true, actor, input
	return carpool.ControlConnection{CredentialMask: "********", CredentialConfigured: true}, nil
}
func (f *controlHandlerFake) CheckConnection(_ context.Context, actor int64, _ string) (carpool.ControlConnection, error) {
	f.called, f.actor = true, actor
	return carpool.ControlConnection{}, carpool.ErrControlAuth
}

func TestCarpoolControlHandlerStrictPrivateInputs(t *testing.T) {
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"origin":"https://node.example","node_instance_id":"node-instance","admin_key":"synthetic-secret","expected_generation":0}`, 200},
		{`{"origin":"https://node.example","actor":99}`, 400},
		{`{"url":"https://node.example/any","user_id":99}`, 400},
		{`{} {}`, 400}, {`null`, 400}, {`[]`, 400}, {strings.Repeat("x", carpoolBodyLimit+1), 413},
	} {
		f := &controlHandlerFake{}
		h := &CarpoolControlHandler{main: f}
		r := gin.New()
		r.PUT("/:id", func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
			h.SaveConnection(c)
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("PUT", "/node", strings.NewReader(tc.body)))
		require.Equal(t, tc.want, w.Code, w.Body.String())
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		require.NotContains(t, w.Body.String(), "synthetic-secret")
		if tc.want == 200 {
			require.EqualValues(t, 7, f.actor)
			require.Equal(t, "synthetic-secret", f.input.AdminKey)
		} else {
			require.False(t, f.called)
		}
	}
}

func TestCarpoolControlHandlerMissingIdentityAndDisabled(t *testing.T) {
	for _, tc := range []struct {
		handler *CarpoolControlHandler
		want    int
	}{{&CarpoolControlHandler{}, 404}, {&CarpoolControlHandler{main: &controlHandlerFake{}}, 401}} {
		r := gin.New()
		r.GET("/:id", tc.handler.Connection)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/car", nil))
		require.Equal(t, tc.want, w.Code)
	}
}
