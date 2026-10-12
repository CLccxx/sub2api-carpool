package handler

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCarpoolManualStrictInput(t *testing.T) {
	for name, method := range map[string]func(*CarpoolHandler) gin.HandlerFunc{
		"enroll":   func(h *CarpoolHandler) gin.HandlerFunc { return h.EnrollManual },
		"withdraw": func(h *CarpoolHandler) gin.HandlerFunc { return h.WithdrawManual },
		"refund":   func(h *CarpoolHandler) gin.HandlerFunc { return h.RefundManual },
	} {
		for _, tt := range []struct {
			name, body, key string
			auth            bool
			status          int
		}{
			{"object forwarded to domain", "{}", "retry", true, 200},
			{"missing body", "", "retry", true, 400},
			{"missing key", "{}", "", true, 400},
			{"key whitespace", "{}", " retry", true, 400},
			{"key non ASCII", "{}", "\u00e9", true, 400},
			{"long key", "{}", strings.Repeat("a", 129), true, 400},
			{"identity injection", `{"actor_id":999}`, "retry", true, 400},
			{"unknown field", `{"unexpected":true}`, "retry", true, 400},
			{"null", "null", "retry", true, 400},
			{"array", "[]", "retry", true, 400},
			{"multiple objects", "{} {}", "retry", true, 400},
			{"trailing garbage", "{} x", "retry", true, 400},
			{"oversized", "{}" + strings.Repeat(" ", 65537), "retry", true, 413},
			{"unauthenticated", "{}", "retry", false, 401},
		} {
			t.Run(name+"/"+tt.name, func(t *testing.T) {
				fake := &carpoolHandlerFake{}
				h := &CarpoolHandler{main: fake}
				router := gin.New()
				router.POST("/:id", func(c *gin.Context) {
					if tt.auth {
						c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
					}
					method(h)(c)
				})
				req := httptest.NewRequest("POST", "/target", strings.NewReader(tt.body))
				req.Header.Set("Idempotency-Key", tt.key)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				require.Equal(t, tt.status, w.Code, w.Body.String())
				require.Equal(t, tt.status == 200, fake.called)
				if fake.called {
					require.EqualValues(t, 42, fake.actor)
					require.Equal(t, "target", fake.id)
					require.Equal(t, "retry", fake.key)
				}
			})
		}
	}
}
