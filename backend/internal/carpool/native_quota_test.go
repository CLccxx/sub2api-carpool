package carpool

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNativeQuotaFormulaAndSnapshotValidity(t *testing.T) {
	for _, capacity := range []int{2, 3, 4} {
		value, err := nativeWeeklyLimit(capacity, nil)
		require.NoError(t, err)
		require.Equal(t, 1800.0/float64(capacity), value)
	}
	estimate := 2000.0
	limit, err := nativeWeeklyLimit(4, &estimate)
	require.NoError(t, err)
	require.Equal(t, 475.0, limit)
	for _, invalid := range []float64{100, 0, -1, math.NaN(), math.Inf(1)} {
		_, err = nativeWeeklyLimit(4, &invalid)
		require.Error(t, err)
	}
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	valid := QuotaObservation{CapturedAt: now, ObservedAt: now.Add(-time.Minute), ResetsAt: now.Add(time.Hour), Utilization: 10, EstimatedTotal: &estimate}
	require.NoError(t, valid.validate(now))
	for _, change := range []func(*QuotaObservation){
		func(s *QuotaObservation) { s.ObservedAt = time.Time{} },
		func(s *QuotaObservation) { s.ObservedAt = now.Add(-2 * time.Hour) },
		func(s *QuotaObservation) { s.ResetsAt = now.Add(-time.Second); s.Utilization = 0 },
		func(s *QuotaObservation) { s.ResetsAt = now.Add(8 * 24 * time.Hour) },
		func(s *QuotaObservation) { s.Utilization = math.NaN() },
		func(s *QuotaObservation) { s.CapturedAt = now.Add(time.Hour) },
	} {
		bad := valid
		change(&bad)
		require.Error(t, bad.validate(now))
	}
	valid.Utilization = 0
	valid.EstimatedTotal = nil
	require.NoError(t, valid.validate(now), "a genuine fresh zero is a valid snapshot, not itself a reset")
}

func TestNativeQuotaCycleRequiresAuthoritativeWindow(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	oldReset := now.Add(time.Hour)
	oldObserved := now.Add(-time.Minute)
	for _, utilization := range []float64{0, 2, 90} {
		sample := QuotaObservation{CapturedAt: now, ObservedAt: now, ResetsAt: oldReset, Utilization: utilization}
		changed, err := nativeCycleChanged(&oldReset, &oldObserved, sample)
		require.NoError(t, err)
		require.False(t, changed)
	}
	oldReset, oldObserved = now.Add(-2*time.Minute), now.Add(-3*time.Minute)
	sample := QuotaObservation{CapturedAt: now, ObservedAt: now, ResetsAt: now.Add(7 * 24 * time.Hour), Utilization: 2}
	changed, err := nativeCycleChanged(&oldReset, &oldObserved, sample)
	require.NoError(t, err)
	require.True(t, changed, "a new official window works even when zero was not sampled")
	sample.ResetsAt = oldReset.Add(time.Second)
	changed, err = nativeCycleChanged(&oldReset, &oldObserved, sample)
	require.NoError(t, err)
	require.False(t, changed, "small header clock jitter does not create a cycle")
	sample.ResetsAt = oldReset.Add(-24 * time.Hour)
	_, err = nativeCycleChanged(&oldReset, &oldObserved, sample)
	require.Error(t, err, "older cycles cannot move native windows backwards")
	sample.ResetsAt = oldReset.Add(nativeWeek - time.Hour)
	_, err = nativeCycleChanged(&oldReset, &oldObserved, sample)
	require.Error(t, err, "an hour-sized discontinuity is ambiguous, not permission to erase usage")
}

func TestNativeQuotaSameWeekCorrectionIsNotAnotherReset(t *testing.T) {
	now := time.Now().UTC()
	oldReset := now.Add(nativeWeek - 2*time.Minute)
	oldObserved := now.Add(-time.Minute)
	sample := QuotaObservation{CapturedAt: now, ObservedAt: now, ResetsAt: now.Add(nativeWeek), Utilization: 0}
	changed, err := nativeCycleChanged(&oldReset, &oldObserved, sample)
	require.NoError(t, err)
	require.False(t, changed, "an early first sample followed by a two-minute correction is still the same week")
}
