package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

type carpoolQuotaAccountRepo struct {
	AccountRepository
	account *Account
}

func (r *carpoolQuotaAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

type carpoolQuotaUsageRepo struct {
	UsageLogRepository
	anchor time.Time
	reads  []time.Time
}

func (r *carpoolQuotaUsageRepo) GetAccountWindowStats(_ context.Context, _ int64, start time.Time) (*usagestats.AccountStats, error) {
	r.reads = append(r.reads, start)
	if start.Equal(r.anchor) {
		return &usagestats.AccountStats{Cost: 500}, nil
	}
	return &usagestats.AccountStats{Cost: 9000}, nil
}

func TestCarpoolQuotaReaderUsesCoherentWindowForNativeCost(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(time.Hour)
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
		"codex_usage_updated_at": now.Format(time.RFC3339),
		"codex_5h_used_percent":  5.0, "codex_5h_reset_at": reset.Format(time.RFC3339),
		"codex_7d_used_percent": 90.0, "codex_7d_reset_at": reset.Add(time.Hour).Format(time.RFC3339),
		"codex_7d_observation": map[string]any{"observed_at": now, "resets_at": reset, "utilization": 25.0},
	}}
	logs := &carpoolQuotaUsageRepo{anchor: reset.Add(-7 * 24 * time.Hour)}
	s := &AccountUsageService{accountRepo: &carpoolQuotaAccountRepo{account: account}, usageLogRepo: logs, cache: NewUsageCache()}
	sample, err := s.readCarpoolQuota(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, 25.0, sample.Utilization)
	require.True(t, reset.Equal(sample.ResetsAt))
	require.Equal(t, 2000.0, *sample.EstimatedTotal)
	require.Equal(t, logs.anchor, logs.reads[len(logs.reads)-1], "cost and percentage must describe the same native weekly window")
}

func TestCodexWeeklyObservationIsCoherentAcrossPartialUpdates(t *testing.T) {
	stamp := time.Now().UTC().Add(-5 * time.Minute).Truncate(time.Second)
	percent, seconds, minutes := 25.0, 3600, 10080
	full := &OpenAICodexUsageSnapshot{PrimaryUsedPercent: &percent, PrimaryResetAfterSeconds: &seconds, PrimaryWindowMinutes: &minutes, UpdatedAt: stamp.Format(time.RFC3339)}
	extra := buildCodexUsageExtraUpdates(full, stamp)
	require.Contains(t, extra, "codex_7d_observation")
	coherent := extra["codex_7d_observation"]
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra}
	now := stamp.Add(5 * time.Minute)
	shortMinutes, newPercent, newSeconds := 300, 90.0, 7200
	for _, partial := range []*OpenAICodexUsageSnapshot{
		{PrimaryUsedPercent: &newPercent, PrimaryResetAfterSeconds: &seconds, PrimaryWindowMinutes: &shortMinutes},
		{PrimaryUsedPercent: &newPercent, PrimaryWindowMinutes: &minutes},
		{PrimaryResetAfterSeconds: &newSeconds, PrimaryWindowMinutes: &minutes},
	} {
		updates := buildCodexUsageExtraUpdates(partial, now)
		require.NotContains(t, updates, "codex_7d_observation", "an incomplete window must not overwrite the last coherent observation")
		mergeAccountExtra(account, updates)
		require.Equal(t, coherent, account.Extra["codex_7d_observation"])
	}
	// Exercise the same representation read back from PostgreSQL JSONB.
	encoded, err := json.Marshal(account.Extra)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(encoded, &account.Extra))
	reset := stamp.Add(time.Hour)
	usage := &UsageInfo{UpdatedAt: &now, SevenDay: &UsageProgress{Utilization: newPercent, ResetsAt: &reset, WindowStats: &WindowStats{Cost: 500}}}
	sample, err := carpoolQuotaObservation(account, usage, now, nil)
	require.NoError(t, err)
	require.True(t, stamp.Equal(sample.ObservedAt), "5h-only and partial updates cannot freshen the weekly sample")
	require.Equal(t, percent, sample.Utilization, "never mix a newer partial percent with the old weekly reset")
	require.Equal(t, 2000.0, *sample.EstimatedTotal)
	wrongReset := reset.Add(time.Hour)
	usage.SevenDay.ResetsAt = &wrongReset
	_, err = carpoolQuotaObservation(account, usage, now, nil)
	require.Error(t, err, "account cost from another window cannot be used for this observation")
}

func TestCarpoolCodexRejectsUnversionedOrIncompleteWeeklyObservation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(time.Hour)
	usage := &UsageInfo{SevenDay: &UsageProgress{Utilization: 25, ResetsAt: &reset, WindowStats: &WindowStats{Cost: 500}}}
	for _, observation := range []any{
		nil,
		map[string]any{"observed_at": now, "resets_at": reset},
		map[string]any{"observed_at": now, "resets_at": reset, "utilization": nil},
		map[string]any{"observed_at": now, "resets_at": reset, "utilization": "unknown"},
		map[string]any{"observed_at": now, "utilization": 25},
	} {
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"codex_usage_updated_at": now.Format(time.RFC3339), "codex_7d_observation": observation}}
		_, err := carpoolQuotaObservation(account, usage, now, nil)
		require.Error(t, err, "missing coherent observation is unknown, never genuine zero")
	}
}

func TestCodexSparkWeeklyObservationOnlyChangesWithWeeklyWindow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	limits := &OpenAIRateLimit{
		PrimaryWindow:   &OpenAIRateLimitWindow{UsedPercent: 5, LimitWindowSeconds: 18000, ResetAfterSeconds: 3600},
		SecondaryWindow: &OpenAIRateLimitWindow{UsedPercent: 25, LimitWindowSeconds: 604800, ResetAfterSeconds: 86400},
	}
	usage := &OpenAIQuotaUsage{AdditionalRateLimits: []OpenAIAdditionalRateLimit{{MeteredFeature: "codex_bengalfox", RateLimit: limits}}}
	updates := buildCodexSparkWindowExtraUpdates(usage, now)
	require.Contains(t, updates, "codex_7d_observation")
	encoded, err := json.Marshal(updates["codex_7d_observation"])
	require.NoError(t, err)
	var observation map[string]any
	require.NoError(t, json.Unmarshal(encoded, &observation))
	require.Equal(t, 25.0, observation["utilization"])
	require.Equal(t, now.Format(time.RFC3339), observation["observed_at"])
	require.Equal(t, now.Add(24*time.Hour).Format(time.RFC3339), observation["resets_at"])
	limits.SecondaryWindow = nil
	updates = buildCodexSparkWindowExtraUpdates(usage, now.Add(time.Minute))
	require.NotContains(t, updates, "codex_7d_observation")
}
