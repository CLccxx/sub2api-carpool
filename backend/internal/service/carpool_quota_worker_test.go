package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/stretchr/testify/require"
)

type quotaStoreStub struct {
	accountID  int64
	accountErr error
	flushErr   error
	applyErr   error
	applies    atomic.Int64
	failures   atomic.Int64
	flushes    atomic.Int64
	forced     atomic.Bool
}

func (s *quotaStoreStub) NativeQuotaAccount(context.Context) (int64, error) {
	return s.accountID, s.accountErr
}
func (s *quotaStoreStub) FlushQuotaCaches(context.Context) error { s.flushes.Add(1); return s.flushErr }
func (s *quotaStoreStub) RecordQuotaReadFailure(context.Context, time.Time) error {
	s.failures.Add(1)
	return nil
}
func (s *quotaStoreStub) ApplyQuotaObservation(_ context.Context, id int64, _ carpool.QuotaObservation, force bool) (carpool.NativeQuotaStatus, error) {
	if id != s.accountID {
		return carpool.NativeQuotaStatus{}, errors.New("wrong account")
	}
	s.applies.Add(1)
	s.forced.Store(force)
	return carpool.NativeQuotaStatus{WeeklyLimitUSD: 475}, s.applyErr
}

func TestCarpoolQuotaWorkerReadFailureDoesNotWriteUsage(t *testing.T) {
	store := &quotaStoreStub{accountID: 7}
	w := newCarpoolQuotaWorker(store, func(context.Context, int64) (carpool.QuotaObservation, error) {
		return carpool.QuotaObservation{}, errors.New("private upstream credentials must not escape")
	}, time.Minute, time.Second)
	_, err := w.Reconcile(context.Background(), true)
	require.ErrorIs(t, err, carpool.ErrUnavailable)
	require.EqualValues(t, 0, store.applies.Load())
	require.EqualValues(t, 1, store.failures.Load())
	require.EqualValues(t, 1, store.flushes.Load(), "retry previously committed cache work even when source is down")
	w.Stop()
}

func TestCarpoolQuotaWorkerLifecycleAndManualRefresh(t *testing.T) {
	store := &quotaStoreStub{accountID: 7}
	w := newCarpoolQuotaWorker(store, func(ctx context.Context, id int64) (carpool.QuotaObservation, error) {
		if _, bounded := ctx.Deadline(); !bounded {
			return carpool.QuotaObservation{}, errors.New("no deadline")
		}
		return carpool.QuotaObservation{}, nil
	}, 5*time.Millisecond, time.Second)
	w.Start()
	t.Cleanup(w.Stop)
	require.Eventually(t, func() bool { return store.applies.Load() >= 2 }, time.Second, time.Millisecond)
	w.Stop()
	w.Stop()
	before := store.applies.Load()
	time.Sleep(15 * time.Millisecond)
	require.Equal(t, before, store.applies.Load())
	status, err := w.Reconcile(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, 475.0, status.WeeklyLimitUSD)
	require.True(t, store.forced.Load())
}

func TestCarpoolQuotaWorkerNoSetupAndInvalidSource(t *testing.T) {
	store := &quotaStoreStub{accountErr: carpool.ErrMainNotFound}
	w := newCarpoolQuotaWorker(store, func(context.Context, int64) (carpool.QuotaObservation, error) {
		t.Fatal("unconfigured node must not query an account")
		return carpool.QuotaObservation{}, nil
	}, time.Minute, time.Second)
	_, err := w.Reconcile(context.Background(), false)
	require.ErrorIs(t, err, carpool.ErrMainNotFound)
	w.Stop()
	store.accountErr = nil
	store.accountID = 7
	store.applyErr = carpool.ErrMainInvalid
	w = newCarpoolQuotaWorker(store, func(context.Context, int64) (carpool.QuotaObservation, error) { return carpool.QuotaObservation{}, nil }, time.Minute, time.Second)
	_, err = w.Reconcile(context.Background(), false)
	require.ErrorIs(t, err, carpool.ErrMainInvalid)
	require.EqualValues(t, 1, store.failures.Load(), "invalid or stale source must leave an unavailable indication")
	store.applyErr = carpool.ErrMainConflict
	_, err = w.Reconcile(context.Background(), false)
	require.ErrorIs(t, err, carpool.ErrMainConflict)
	require.EqualValues(t, 2, store.failures.Load(), "ambiguous cycle changes must not leave a ready indication")
	w.Stop()
}

func TestCarpoolQuotaWorkerStopCancelsRead(t *testing.T) {
	store := &quotaStoreStub{accountID: 7}
	started := make(chan struct{})
	w := newCarpoolQuotaWorker(store, func(ctx context.Context, _ int64) (carpool.QuotaObservation, error) {
		close(started)
		<-ctx.Done()
		return carpool.QuotaObservation{}, ctx.Err()
	}, time.Hour, time.Hour)
	w.Start()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	w.Stop()
	require.Zero(t, store.applies.Load())
}

func TestCarpoolQuotaObservationUsesSourceTimeAndNativeAccountCost(t *testing.T) {
	now := time.Now().UTC()
	reset := now.Add(time.Hour)
	weekly := map[string]any{"observed_at": now.Add(-time.Hour), "resets_at": reset, "utilization": 25.0}
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"codex_usage_updated_at": now.Format(time.RFC3339Nano), "codex_7d_observation": weekly}}
	usage := &UsageInfo{UpdatedAt: &now, SevenDay: &UsageProgress{Utilization: 25, ResetsAt: &reset, WindowStats: &WindowStats{Cost: 500, UserCost: 100}}}
	sample, err := carpoolQuotaObservation(account, usage, now, nil)
	require.NoError(t, err)
	require.True(t, sample.ObservedAt.Equal(now.Add(-time.Hour)), "response construction time cannot freshen a stale source")
	require.NotNil(t, sample.EstimatedTotal)
	require.Equal(t, 2000.0, *sample.EstimatedTotal)
	delete(account.Extra, "codex_7d_observation")
	_, err = carpoolQuotaObservation(account, usage, now, nil)
	require.Error(t, err)
	weekly["observed_at"], weekly["utilization"] = now, 0.0
	account.Extra["codex_7d_observation"] = weekly
	usage.SevenDay.Utilization = 0
	sample, err = carpoolQuotaObservation(account, usage, now, nil)
	require.NoError(t, err)
	require.Nil(t, sample.EstimatedTotal)
	usage.SevenDay.ResetsAt = nil
	_, err = carpoolQuotaObservation(account, usage, now, nil)
	require.Error(t, err)
	usage.SevenDay.ResetsAt = &reset
	usage.Error = "upstream error"
	_, err = carpoolQuotaObservation(account, usage, now, nil)
	require.Error(t, err)
	usage.Error = ""
	account.Platform = PlatformAnthropic
	_, err = carpoolQuotaObservation(account, usage, now, nil)
	require.Error(t, err, "active native cache needs its real acquisition time")
	acquired := now.Add(-2 * time.Minute)
	sample, err = carpoolQuotaObservation(account, usage, now, &acquired)
	require.NoError(t, err)
	require.Equal(t, acquired, sample.ObservedAt)
}
