package config

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCarpoolMainConfig(t *testing.T) {
	for _, tt := range []struct {
		name, main, orders, node        string
		wantMain, wantOrders, wantError bool
	}{
		{name: "defaults"},
		{name: "catalog only", main: "true", wantMain: true},
		{name: "test orders", main: "true", orders: "true", wantMain: true, wantOrders: true},
		{name: "orders require main", orders: "true", wantError: true},
		{name: "mutually exclusive", main: "true", node: "true", wantError: true},
		{name: "invalid boolean", main: "invalid", wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			t.Setenv("CARPOOL_MAIN_ENABLED", tt.main)
			t.Setenv("CARPOOL_MAIN_TEST_ORDERS_ENABLED", tt.orders)
			t.Setenv("CARPOOL_NODE_ENABLED", tt.node)
			cfg, err := Load()
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantMain, cfg.CarpoolMain.Enabled)
			require.Equal(t, tt.wantOrders, cfg.CarpoolMain.TestOrdersEnabled)
		})
	}
}

func TestCarpoolControlConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name      string
		env       map[string]string
		wantError bool
	}{
		{name: "off by default"},
		{name: "requires main", env: map[string]string{"CARPOOL_CONTROL_ENABLED": "true"}, wantError: true},
		{name: "requires root", env: map[string]string{"CARPOOL_CONTROL_ENABLED": "true", "CARPOOL_MAIN_ENABLED": "true", "CARPOOL_CONTROL_ALLOWED_ORIGINS": "https://node.example"}, wantError: true},
		{name: "requires origins", env: map[string]string{"CARPOOL_CONTROL_ENABLED": "true", "CARPOOL_MAIN_ENABLED": "true", "CARPOOL_CONTROL_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))}, wantError: true},
		{name: "configured main", env: map[string]string{"CARPOOL_CONTROL_ENABLED": "true", "CARPOOL_MAIN_ENABLED": "true", "CARPOOL_CONTROL_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32)), "CARPOOL_CONTROL_ALLOWED_ORIGINS": "https://node.example,https://other.example:8443"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			cfg, err := Load()
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.env["CARPOOL_CONTROL_ENABLED"] == "true", cfg.CarpoolControl.Enabled)
			if cfg.CarpoolControl.Enabled {
				require.Len(t, cfg.CarpoolControl.AllowedOrigins, 2)
			}
		})
	}
}

func TestCarpoolManagedNodeConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name      string
		override  map[string]string
		wantError bool
	}{
		{name: "managed without oidc"},
		{name: "missing main identity", override: map[string]string{"CARPOOL_NODE_MAIN_INSTANCE_ID": ""}, wantError: true},
		{name: "invalid node identity", override: map[string]string{"CARPOOL_NODE_NODE_ID": "wrong"}, wantError: true},
		{name: "automatic group bootstrap", override: map[string]string{"CARPOOL_NODE_GROUP_ID": "0"}},
		{name: "invalid negative group", override: map[string]string{"CARPOOL_NODE_GROUP_ID": "-1"}, wantError: true},
		{name: "mixed issuer", override: map[string]string{"CARPOOL_NODE_ISSUER": "https://issuer.example"}, wantError: true},
		{name: "unknown mode", override: map[string]string{"CARPOOL_NODE_AUTHORIZATION_MODE": "unknown"}, wantError: true},
		{name: "simple mode", override: map[string]string{"RUN_MODE": "simple"}, wantError: true},
		{name: "oidc still requires issuer", override: map[string]string{"CARPOOL_NODE_AUTHORIZATION_MODE": "oidc"}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			for k, v := range map[string]string{
				"CARPOOL_NODE_ENABLED": "true", "CARPOOL_NODE_AUTHORIZATION_MODE": "managed", "CARPOOL_NODE_GROUP_ID": "7",
				"CARPOOL_NODE_MAIN_INSTANCE_ID": "d2167c51-71f7-4e30-b7ac-a3dde5bdf77f", "CARPOOL_NODE_NODE_ID": "9cc9a01a-e93e-44d2-b2c7-9dbac6fb670b", "CARPOOL_NODE_CAR_ID": "f81c4712-154c-439b-970a-dc7578d3fcdf",
			} {
				t.Setenv(k, v)
			}
			for k, v := range tc.override {
				t.Setenv(k, v)
			}
			cfg, err := Load()
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.True(t, cfg.CarpoolNode.Managed())
			require.False(t, cfg.OIDC.Enabled)
		})
	}
}

func TestCarpoolControlRejectsSharedEncryptionRoot(t *testing.T) {
	for _, shared := range []string{"jwt", "totp"} {
		t.Run(shared, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			key := bytes.Repeat([]byte{75}, 32)
			t.Setenv("CARPOOL_MAIN_ENABLED", "true")
			t.Setenv("CARPOOL_CONTROL_ENABLED", "true")
			t.Setenv("CARPOOL_CONTROL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
			t.Setenv("CARPOOL_CONTROL_ALLOWED_ORIGINS", "https://node.example")
			if shared == "jwt" {
				t.Setenv("JWT_SECRET", string(key))
			} else {
				t.Setenv("TOTP_ENCRYPTION_KEY", hex.EncodeToString(key))
			}
			_, err := Load()
			require.ErrorContains(t, err, "independent")
		})
	}
}

func TestCarpoolManualEnrollmentEnvironment(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("CARPOOL_MAIN_ENABLED", "true")
	t.Setenv("CARPOOL_MAIN_TEST_ORDERS_ENABLED", "false")
	t.Setenv("CARPOOL_MAIN_MANUAL_ENROLLMENT_ENABLED", "true")
	t.Setenv("CARPOOL_NODE_ENABLED", "false")
	cfg, err := Load()
	require.NoError(t, err)
	require.True(t, cfg.CarpoolMain.ManualEnrollmentEnabled)
	t.Setenv("CARPOOL_MAIN_TEST_ORDERS_ENABLED", "true")
	_, err = Load()
	require.Error(t, err)
}
