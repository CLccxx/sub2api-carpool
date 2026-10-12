//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type carpoolPublicRepoStub struct {
	settingPublicRepoStub
	t *testing.T
}

func (s *carpoolPublicRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	s.t.Helper()
	require.NotContains(s.t, keys, "carpool_main_enabled", "visibility must not read a mutable setting")
	require.NotContains(s.t, keys, "carpool_test_orders_enabled", "visibility must not read a mutable setting")
	require.NotContains(s.t, keys, "carpool_manual_enrollment_enabled", "visibility must not read a mutable setting")
	require.NotContains(s.t, keys, "carpool_control_enabled", "visibility must not read a mutable setting")
	// Return conflicting values even when not requested to detect accidental DB overrides.
	return s.values, nil
}

func TestCarpoolPublicSettings_ConfigOnlyAndInjectionMirror(t *testing.T) {
	tests := []struct {
		name       string
		nilConfig  bool
		main       bool
		testOrders bool
		manual     bool
		node       bool
		wantMain   bool
		wantTest   bool
		wantManual bool
	}{
		{name: "nil config", nilConfig: true},
		{name: "defaults"},
		{name: "test orders without main", testOrders: true},
		{name: "main only", main: true, wantMain: true},
		{name: "main with test orders", main: true, testOrders: true, wantMain: true, wantTest: true},
		{name: "manual without main", manual: true},
		{name: "manual", main: true, manual: true, wantMain: true, wantManual: true},
		{name: "conflicting modes fail closed", main: true, manual: true, testOrders: true, wantMain: true},
		{name: "manual on node", main: true, manual: true, node: true},
		{name: "node", node: true},
		{name: "node with test orders", node: true, testOrders: true},
		{name: "node with main", node: true, main: true},
		{name: "node with main and test orders", node: true, main: true, testOrders: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, dbValue := range []string{"missing", "true", "false"} {
				t.Run(dbValue, func(t *testing.T) {
					var cfg *config.Config
					if !tc.nilConfig {
						cfg = &config.Config{}
						cfg.CarpoolMain.Enabled = tc.main
						cfg.CarpoolMain.TestOrdersEnabled = tc.testOrders
						cfg.CarpoolMain.ManualEnrollmentEnabled = tc.manual
						cfg.CarpoolNode.Enabled = tc.node
					}
					values := map[string]string{}
					if dbValue != "missing" {
						values["carpool_main_enabled"] = dbValue
						values["carpool_test_orders_enabled"] = dbValue
						values["carpool_manual_enrollment_enabled"] = dbValue
						values["carpool_control_enabled"] = dbValue
					}
					svc := NewSettingService(&carpoolPublicRepoStub{
						settingPublicRepoStub: settingPublicRepoStub{values: values},
						t:                     t,
					}, cfg)
					settings, err := svc.GetPublicSettings(context.Background())
					require.NoError(t, err)
					injection, err := svc.GetPublicSettingsForInjection(context.Background())
					require.NoError(t, err)
					for name, payload := range map[string]any{"public": settings, "injection": injection} {
						t.Run(name, func(t *testing.T) {
							encoded, err := json.Marshal(payload)
							require.NoError(t, err)
							var fields map[string]any
							require.NoError(t, json.Unmarshal(encoded, &fields))
							for key, want := range map[string]bool{
								"carpool_main_enabled":              tc.wantMain,
								"carpool_test_orders_enabled":       tc.wantTest,
								"carpool_manual_enrollment_enabled": tc.wantManual,
								"carpool_control_enabled":           false,
							} {
								_, present := fields[key]
								require.True(t, present, "%s missing; false flags must remain present", key)
								require.Equal(t, want, fields[key], key)
							}
						})
					}
				})
			}
		})
	}
}
