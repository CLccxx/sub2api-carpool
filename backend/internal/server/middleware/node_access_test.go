package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestNodeAccessBothKeyIngressPaths(t *testing.T) {
	for _, google := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "google"}[google], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			node := carpool.Config{Enabled: true, NodeID: "b", CarID: "1", Issuer: "issuer"}
			cfg := &config.Config{RunMode: config.RunModeSimple, CarpoolNode: node}
			key := &service.APIKey{ID: 1, UserID: 7, Key: "fixture-key", Status: service.StatusActive, User: &service.User{ID: 7, Status: service.StatusActive, Role: service.RoleAdmin, Balance: 10}}
			repo := fakeAPIKeyRepo{getByKey: func(context.Context, string) (*service.APIKey, error) { return key, nil }}
			svc := service.ProvideAPIKeyService(carpool.NewGate(db, node), repo, nil, nil, nil, nil, nil, cfg, nil, nil)
			router := gin.New()
			mw := gin.HandlerFunc(NewAPIKeyAuthMiddleware(svc, nil, cfg))
			if google {
				mw = APIKeyAuthGoogle(svc, cfg)
			}
			router.Use(mw)
			router.GET("/v1/usage", func(c *gin.Context) { c.Status(200) })
			// Even admin-owned keys and billing-exempt paths must recheck the gate.
			for _, status := range []int{200, 403, 503} {
				expectation := mock.ExpectQuery("SELECT EXISTS").WithArgs("b", "1", "issuer", int64(7))
				if status == 503 {
					expectation.WillReturnError(errors.New("database unavailable"))
				} else {
					expectation.WillReturnRows(sqlmock.NewRows([]string{"allowed"}).AddRow(status == 200))
				}
				req := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
				req.Header.Set("Authorization", "Bearer fixture-key")
				response := httptest.NewRecorder()
				router.ServeHTTP(response, req)
				require.Equal(t, status, response.Code, response.Body.String())
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
