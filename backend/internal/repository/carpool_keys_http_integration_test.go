//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestManagedKeysHTTPNativeAuthIsolation(t *testing.T) {
	ctx := context.Background()
	node, nodeCfg := nativeManagedNode(t)
	client := testEntClient(t)
	users := NewUserRepository(client, integrationDB)
	api := service.ProvideAPIKeyService(carpool.NewGate(integrationDB, nodeCfg), NewAPIKeyRepository(client, integrationDB), users, NewGroupRepository(client, integrationDB), NewUserSubscriptionRepository(client), NewUserGroupRateRepository(integrationDB), nil, &config.Config{CarpoolNode: nodeCfg}, nil, nil)
	keys := service.NewCarpoolManagedKeys(node, client, api)
	cfg := &config.Config{}
	cfg.JWT.Secret = "synthetic-managed-http-test-signing-key"
	cfg.JWT.AccessTokenExpireMinutes = 15
	admin := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@http-admin.invalid", Role: service.RoleAdmin})
	require.Equal(t, service.RoleAdmin, admin.Role)
	settingsRepo := NewSettingRepository(client)
	adminKey := "synthetic-admin-" + uuid.NewString()
	require.NoError(t, settingsRepo.Set(ctx, service.SettingKeyAdminAPIKey, adminKey))
	settings := service.NewSettingService(settingsRepo, cfg)
	auth := service.NewAuthService(nil, users, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	h := handler.NewCarpoolControlHandler(nil, node, keys)
	r := gin.New()
	g := r.Group("/api/v1/admin/carpool-control/v1", gin.HandlerFunc(middleware.NewAdminAuthMiddleware(auth, service.NewUserService(users, nil, nil, nil), settings, nil)))
	g.Use(h.EnforceControlTarget)
	g.POST("/members/:subject/keys", h.CreateMemberKey)
	g.PATCH("/members/:subject/keys/:key_id", h.UpdateMemberKey)
	g.DELETE("/members/:subject/keys/:key_id", h.DeleteMemberKey)
	subject := uuid.NewString()
	member, err := node.Provision(ctx, node.Target(), subject)
	require.NoError(t, err)
	other := uuid.NewString()
	_, err = node.Provision(ctx, node.Target(), other)
	require.NoError(t, err)
	operation := uuid.NewString()
	command := func(method, sub, suffix, body, key, token string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/api/v1/admin/carpool-control/v1/members/"+sub+"/keys"+suffix, strings.NewReader(body))
		req.Header.Set("x-api-key", key)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Idempotency-Key", operation)
		target := node.Target()
		req.Header.Set(carpool.ControlHeaderMain, target.MainID)
		req.Header.Set(carpool.ControlHeaderNode, target.NodeID)
		req.Header.Set(carpool.ControlHeaderCar, target.CarID)
		req.Header.Set(carpool.ControlHeaderInstance, target.InstanceID)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, want, w.Code, w.Body.String())
		require.NotContains(t, w.Body.String(), adminKey)
		return w
	}
	command("POST", subject, "", `{"name":"one"}`, "", "", 401)
	command("POST", subject, "", `{"name":"one"}`, "wrong-admin", "", 401)
	command("POST", subject, "", `{"name":"one"}`, adminKey, "", 403)
	user, err := users.GetByID(ctx, member.UserID)
	require.NoError(t, err)
	token, err := auth.GenerateToken(ctx, user)
	require.NoError(t, err)
	command("POST", subject, "", `{"name":"one"}`, "", token, 403)
	now := time.Now().UTC().Truncate(time.Microsecond)
	start, end, lease := now.Add(-time.Hour), now.Add(time.Hour), now.Add(time.Minute)
	require.NoError(t, node.ApplyGrant(ctx, node.Target(), subject, carpool.ManagedGrant{Version: 1, Status: "active", ValidFrom: &start, ValidUntil: &end, LeaseUntil: &lease}))
	command("POST", subject, "", `{"name":"one","user_id":1}`, adminKey, "", 400)
	w := command("POST", subject, "", `{"name":"one"}`, adminKey, "", 200)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	var result struct {
		Data carpool.ManagedKey `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.NotEmpty(t, result.Data.Key)
	secret := result.Data.Key
	suffix := "/" + strconv.FormatInt(result.Data.ID, 10)
	command("PATCH", other, suffix, `{"name":"steal"}`, adminKey, "", 404)
	command("DELETE", other, suffix, "", adminKey, "", 404)
	w = command("PATCH", subject, suffix, `{"name":"renamed"}`, adminKey, "", 200)
	require.NotContains(t, w.Body.String(), secret)
	require.NoError(t, node.ApplyGrant(ctx, node.Target(), subject, carpool.ManagedGrant{Version: 2, Status: "paused"}))
	command("PATCH", subject, suffix, `{"status":"active"}`, adminKey, "", 403)
	command("PATCH", subject, suffix, `{"status":"disabled"}`, adminKey, "", 200)
	w = command("POST", subject, "", `{"name":"one"}`, adminKey, "", 403)
	require.NotContains(t, w.Body.String(), secret)
	command("DELETE", subject, suffix, "", adminKey, "", 200)
	command("DELETE", subject, suffix, "", adminKey, "", 200)
}
