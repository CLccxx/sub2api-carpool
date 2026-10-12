package carpool

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManualConfigurationIsolation(t *testing.T) {
	for _, input := range []string{
		`{"ManualEnrollmentEnabled":true}`,
		`{"Enabled":true,"TestOrdersEnabled":true,"ManualEnrollmentEnabled":true}`,
	} {
		var cfg MainConfig
		require.NoError(t, json.Unmarshal([]byte(input), &cfg))
		require.Error(t, cfg.Validate(false), input)
	}
	var cfg MainConfig
	require.NoError(t, json.Unmarshal([]byte(`{"Enabled":true,"ManualEnrollmentEnabled":true}`), &cfg))
	require.NoError(t, cfg.Validate(false))
	require.Error(t, cfg.Validate(true))
}
