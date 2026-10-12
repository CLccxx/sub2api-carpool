//go:build unit

package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCarpoolPublicSettings_APIAndInjectionMirror(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name       string
		nilConfig  bool
		main       bool
		testOrders bool
		node       bool
		wantMain   bool
		wantTest   bool
	}{
		{name: "nil config", nilConfig: true},
		{name: "defaults"},
		{name: "test orders without main", testOrders: true},
		{name: "main only", main: true, wantMain: true},
		{name: "main with test orders", main: true, testOrders: true, wantMain: true, wantTest: true},
		{name: "node", node: true},
		{name: "node with test orders", node: true, testOrders: true},
		{name: "node with main", node: true, main: true},
		{name: "node with main and test orders", node: true, main: true, testOrders: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg *config.Config
			if !tc.nilConfig {
				cfg = &config.Config{}
				cfg.CarpoolMain.Enabled = tc.main
				cfg.CarpoolMain.TestOrdersEnabled = tc.testOrders
				cfg.CarpoolNode.Enabled = tc.node
			}
			svc := service.NewSettingService(&settingHandlerPublicRepoStub{}, cfg)
			h := NewSettingHandler(svc, "test-version")
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/public", nil)
			h.GetPublicSettings(c)
			require.Equal(t, http.StatusOK, recorder.Code)
			var resp struct {
				Code int            `json:"code"`
				Data map[string]any `json:"data"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
			require.Zero(t, resp.Code)
			injection, err := svc.GetPublicSettingsForInjection(c.Request.Context())
			require.NoError(t, err)
			encoded, err := json.Marshal(injection)
			require.NoError(t, err)
			var injected map[string]any
			require.NoError(t, json.Unmarshal(encoded, &injected))
			for key, want := range map[string]bool{
				"carpool_main_enabled":        tc.wantMain,
				"carpool_test_orders_enabled": tc.wantTest,
			} {
				_, apiPresent := resp.Data[key]
				_, injectionPresent := injected[key]
				require.True(t, apiPresent, "API missing %s", key)
				require.True(t, injectionPresent, "SSR missing %s", key)
				require.Equal(t, want, resp.Data[key], key)
				require.Equal(t, resp.Data[key], injected[key], "API/SSR drift: %s", key)
			}
		})
	}
}

func TestCarpoolManualPublicSettings(t *testing.T) {
	for _, tc := range []struct{ enabled, manual, testOrders, node, want bool }{
		{}, {enabled: true, manual: true, want: true}, {manual: true},
		{enabled: true, manual: true, testOrders: true}, {enabled: true, manual: true, node: true},
	} {
		cfg := &config.Config{CarpoolMain: carpool.MainConfig{Enabled: tc.enabled, ManualEnrollmentEnabled: tc.manual, TestOrdersEnabled: tc.testOrders}, CarpoolNode: carpool.Config{Enabled: tc.node}}
		svc := service.NewSettingService(&settingHandlerPublicRepoStub{}, cfg)
		h := NewSettingHandler(svc, "test")
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/public", nil)
		h.GetPublicSettings(c)
		require.Equal(t, http.StatusOK, w.Code)
		var body struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.Equal(t, tc.want, body.Data["carpool_manual_enrollment_enabled"])
		payload, err := svc.GetPublicSettingsForInjection(c.Request.Context())
		require.NoError(t, err)
		raw, err := json.Marshal(payload)
		require.NoError(t, err)
		var injected map[string]any
		require.NoError(t, json.Unmarshal(raw, &injected))
		require.Equal(t, tc.want, injected["carpool_manual_enrollment_enabled"])
	}
}

func TestCarpoolPublicSettings_ReadOnlySchema(t *testing.T) {
	for fieldName, jsonName := range map[string]string{
		"CarpoolMainEnabled":             "carpool_main_enabled",
		"CarpoolTestOrdersEnabled":       "carpool_test_orders_enabled",
		"CarpoolManualEnrollmentEnabled": "carpool_manual_enrollment_enabled",
		"CarpoolControlEnabled":          "carpool_control_enabled",
	} {
		t.Run(fieldName, func(t *testing.T) {
			for _, schema := range []reflect.Type{
				reflect.TypeOf(service.PublicSettings{}),
				reflect.TypeOf(dto.PublicSettings{}),
				reflect.TypeOf(service.PublicSettingsInjectionPayload{}),
			} {
				field, ok := schema.FieldByName(fieldName)
				require.True(t, ok, "%s missing %s", schema, fieldName)
				require.Equal(t, reflect.Bool, field.Type.Kind())
				require.Equal(t, jsonName, field.Tag.Get("json"), "explicit boolean without omitempty")
			}
			for _, schema := range []reflect.Type{
				reflect.TypeOf(service.SystemSettings{}),
				reflect.TypeOf(dto.SystemSettings{}),
			} {
				_, ok := schema.FieldByName(fieldName)
				require.False(t, ok, "%s must not expose mutable carpool settings", schema)
			}
		})
	}
}

func TestCarpoolControlPublicSettings(t *testing.T) {
	for _, tc := range []struct{ main, control, node, want bool }{{}, {control: true}, {main: true}, {main: true, control: true, want: true}, {main: true, control: true, node: true}} {
		cfg := &config.Config{CarpoolMain: carpool.MainConfig{Enabled: tc.main}, CarpoolNode: carpool.Config{Enabled: tc.node}, CarpoolControl: carpool.ControlConfig{Enabled: tc.control, EncryptionKey: "synthetic-private-root"}}
		svc := service.NewSettingService(&settingHandlerPublicRepoStub{}, cfg)
		h := NewSettingHandler(svc, "test")
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/public", nil)
		h.GetPublicSettings(c)
		require.Equal(t, http.StatusOK, w.Code)
		var body struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.Equal(t, tc.want, body.Data["carpool_control_enabled"])
		require.NotContains(t, w.Body.String(), "synthetic-private-root")
		payload, err := svc.GetPublicSettingsForInjection(c.Request.Context())
		require.NoError(t, err)
		raw, err := json.Marshal(payload)
		require.NoError(t, err)
		var injected map[string]any
		require.NoError(t, json.Unmarshal(raw, &injected))
		require.Equal(t, tc.want, injected["carpool_control_enabled"])
		require.NotContains(t, string(raw), "synthetic-private-root")
	}
}
