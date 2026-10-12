//go:build integration

package carpool_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestNativeQuotaPreservesConsumptionAndDeduplicatesCycle(t *testing.T) {
	ctx := context.Background()
	lab := newNativeControlLab(t)
	cfg := carpool.Config{Enabled: true, AuthorizationMode: "managed", MainInstanceID: uuid.NewString(), NodeID: uuid.NewString(), CarID: uuid.NewString()}
	node, err := carpool.NewNodeControl(ctx, lab.db, cfg)
	require.NoError(t, err)
	var account int64
	require.NoError(t, lab.db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,status,schedulable) VALUES('quota fixture','openai','oauth','{}','active',true) RETURNING id`).Scan(&account))
	setup, err := node.InitializeSubscription(ctx, node.Target(), carpool.NativeSetupInput{Capacity: 4, AccountID: account})
	require.NoError(t, err)
	start := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	end := start.Add(30 * 24 * time.Hour)
	for range 4 {
		subject := uuid.NewString()
		_, err = node.Provision(ctx, node.Target(), subject)
		require.NoError(t, err)
		require.NoError(t, node.ApplyGrant(ctx, node.Target(), subject, carpool.ManagedGrant{NativeSubscription: true, Version: 1, Status: "active", ValidFrom: &start, ValidUntil: &end}))
	}
	_, err = lab.db.Exec(`UPDATE user_subscriptions SET weekly_usage_usd=123,daily_usage_usd=9,monthly_usage_usd=456`)
	require.NoError(t, err)
	stamp := time.Now().UTC().Truncate(time.Microsecond)
	estimate := 2000.0
	observation := carpool.QuotaObservation{CapturedAt: stamp, ObservedAt: stamp, ResetsAt: stamp.Add(24 * time.Hour), Utilization: 25, EstimatedTotal: &estimate}
	status, err := node.ApplyQuotaObservation(ctx, account, observation, true)
	require.NoError(t, err)
	require.Equal(t, 475.0, status.WeeklyLimitUSD)
	var minUsed, maxUsed, daily, monthly float64
	readUsage := func() {
		require.NoError(t, lab.db.QueryRow(`SELECT min(weekly_usage_usd),max(weekly_usage_usd),min(daily_usage_usd),min(monthly_usage_usd) FROM user_subscriptions`).Scan(&minUsed, &maxUsed, &daily, &monthly))
	}
	readUsage()
	require.Equal(t, 123.0, minUsed)
	require.Equal(t, minUsed, maxUsed)
	require.Equal(t, 9.0, daily)
	require.Equal(t, 456.0, monthly)
	// An hourly update changes the same native group, never its existing usage.
	estimate = 1000
	observation.CapturedAt = time.Now().UTC()
	observation.ObservedAt = observation.CapturedAt
	status, err = node.ApplyQuotaObservation(ctx, account, observation, false)
	require.NoError(t, err)
	require.Equal(t, 475.0, status.WeeklyLimitUSD, "automatic updates are hourly")
	status, err = node.ApplyQuotaObservation(ctx, account, observation, true)
	require.NoError(t, err)
	require.Equal(t, 225.0, status.WeeklyLimitUSD)
	readUsage()
	require.Equal(t, 123.0, minUsed)
	// Seed the previous authoritative cycle; the next fresh source snapshot is a new week.
	_, err = lab.db.Exec(`UPDATE carpool_native_setup SET cycle_reset_at=clock_timestamp()-interval '1 minute',observed_at=clock_timestamp()-interval '2 minutes'; UPDATE user_subscriptions SET weekly_window_start=clock_timestamp()-interval '8 days'`)
	require.NoError(t, err)
	observation.CapturedAt = time.Now().UTC()
	observation.ObservedAt = observation.CapturedAt
	observation.ResetsAt = observation.ObservedAt.Add(7 * 24 * time.Hour)
	observation.Utilization = 0
	observation.EstimatedTotal = nil
	status, err = node.ApplyQuotaObservation(ctx, account, observation, false)
	require.NoError(t, err)
	require.Equal(t, 450.0, status.WeeklyLimitUSD)
	readUsage()
	require.Zero(t, minUsed)
	require.Zero(t, maxUsed)
	require.Equal(t, 9.0, daily)
	require.Equal(t, 456.0, monthly)
	_, err = lab.db.Exec(`UPDATE user_subscriptions SET weekly_usage_usd=17`)
	require.NoError(t, err)
	node, err = carpool.NewNodeControl(ctx, lab.db, cfg)
	require.NoError(t, err)
	_, err = node.ApplyQuotaObservation(ctx, account, observation, true)
	require.NoError(t, err)
	readUsage()
	require.Equal(t, 17.0, minUsed, "restart and same-cycle retry cannot clear later usage")
	var count int
	require.NoError(t, lab.db.QueryRow(`SELECT count(*) FROM user_subscriptions WHERE expires_at=$1 AND group_id=$2`, end, setup.GroupID).Scan(&count))
	require.Equal(t, 4, count)
	old := observation
	old.CapturedAt = stamp
	old.ObservedAt = stamp
	old.ResetsAt = stamp.Add(time.Hour)
	_, err = node.ApplyQuotaObservation(ctx, account, old, true)
	require.Error(t, err)
	readUsage()
	require.Equal(t, 17.0, minUsed)

	// A native request can advance the weekly window before reconciliation.
	// Header clock jitter must not erase usage recorded in that new window.
	_, err = lab.db.Exec(`UPDATE carpool_native_setup SET cycle_reset_at=clock_timestamp()-interval '1 minute',observed_at=clock_timestamp()-interval '2 minutes'`)
	require.NoError(t, err)
	_, err = lab.db.Exec(`UPDATE user_subscriptions SET weekly_window_start=$1`, observation.ResetsAt.Add(-7*24*time.Hour-time.Second))
	require.NoError(t, err)
	_, err = node.ApplyQuotaObservation(ctx, account, observation, true)
	require.NoError(t, err)
	readUsage()
	require.Equal(t, 17.0, minUsed, "already advanced native windows retain new charges")

	// Cache acknowledgement survives a failure and restart without replaying a reset.
	estimate = 2000
	observation.EstimatedTotal = &estimate
	node.SetSubscriptionInvalidator(func(context.Context, int64, int64) error { return errors.New("cache offline") })
	_, err = node.ApplyQuotaObservation(ctx, account, observation, true)
	require.Error(t, err)
	status, err = node.NativeQuotaStatus(ctx)
	require.NoError(t, err)
	require.True(t, status.CachePending)
	node, err = carpool.NewNodeControl(ctx, lab.db, cfg)
	require.NoError(t, err)
	invalidated := 0
	node.SetSubscriptionInvalidator(func(context.Context, int64, int64) error { invalidated++; return nil })
	require.NoError(t, node.FlushQuotaCaches(ctx))
	require.Equal(t, 4, invalidated)
	status, err = node.NativeQuotaStatus(ctx)
	require.NoError(t, err)
	require.False(t, status.CachePending)
	require.NoError(t, node.FlushQuotaCaches(ctx))
	require.Equal(t, 4, invalidated)
	readUsage()
	require.Equal(t, 17.0, minUsed)

	// A request advances and charges its row while reconciliation waits on the lock.
	// PostgreSQL must recheck the predicate after the request commits.
	_, err = lab.db.Exec(`UPDATE carpool_native_setup SET cycle_reset_at=clock_timestamp()-interval '1 minute',observed_at=clock_timestamp()-interval '2 minutes'; UPDATE user_subscriptions SET weekly_window_start=clock_timestamp()-interval '8 days'`)
	require.NoError(t, err)
	observation.CapturedAt = time.Now().UTC().Truncate(time.Microsecond)
	observation.ObservedAt = observation.CapturedAt
	observation.ResetsAt = observation.ObservedAt.Add(7 * 24 * time.Hour)
	var chargedID int64
	require.NoError(t, lab.db.QueryRow(`SELECT min(id) FROM user_subscriptions`).Scan(&chargedID))
	charge, err := lab.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = charge.Rollback() })
	_, err = charge.ExecContext(ctx, `UPDATE user_subscriptions SET weekly_window_start=$2,weekly_usage_usd=22 WHERE id=$1`, chargedID, observation.ResetsAt.Add(-7*24*time.Hour))
	require.NoError(t, err)
	finished := make(chan error, 1)
	go func() {
		waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_, syncErr := node.ApplyQuotaObservation(waitCtx, account, observation, false)
		finished <- syncErr
	}()
	require.Eventually(t, func() bool {
		var waiting bool
		e := lab.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'UPDATE user_subscriptions SET%weekly_usage_usd=%')`).Scan(&waiting)
		return e == nil && waiting
	}, 3*time.Second, 5*time.Millisecond)
	require.NoError(t, charge.Commit())
	require.NoError(t, <-finished)
	var charged float64
	require.NoError(t, lab.db.QueryRow(`SELECT weekly_usage_usd FROM user_subscriptions WHERE id=$1`, chargedID).Scan(&charged))
	require.Equal(t, 22.0, charged, "a newly committed current-week charge must survive reconciliation")
	_, err = lab.db.Exec(`UPDATE user_subscriptions SET weekly_usage_usd=17`)
	require.NoError(t, err)

	// Stale reads and invalid estimates cannot erase usage or the native limit.
	invalid := observation
	invalid.ObservedAt = time.Now().Add(-time.Hour)
	_, err = node.ApplyQuotaObservation(ctx, account, invalid, true)
	require.Error(t, err)
	invalid = observation
	invalidEstimate := 99.0
	invalid.EstimatedTotal = &invalidEstimate
	_, err = node.ApplyQuotaObservation(ctx, account, invalid, true)
	require.Error(t, err)
	require.NoError(t, node.RecordQuotaReadFailure(ctx, time.Now()))
	status, err = node.NativeQuotaStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, "error", status.State)
	require.Equal(t, 475.0, status.WeeklyLimitUSD)
	readUsage()
	require.Equal(t, 17.0, minUsed)

	// A removed/retyped native group must roll the whole observation back.
	_, err = lab.db.Exec(`UPDATE groups SET subscription_type='standard' WHERE id=$1`, setup.GroupID)
	require.NoError(t, err)
	observation.CapturedAt = time.Now().UTC()
	observation.ObservedAt = observation.CapturedAt
	_, err = node.ApplyQuotaObservation(ctx, account, observation, true)
	require.Error(t, err)
	var lastError string
	require.NoError(t, lab.db.QueryRow(`SELECT last_error_code FROM carpool_native_setup WHERE singleton`).Scan(&lastError))
	require.Equal(t, "source_unavailable", lastError, "failed native group write must not acknowledge a successful observation")
}

func TestNativeQuotaNewSubscriptionUsesExistingAccountWindow(t *testing.T) {
	ctx := context.Background()
	lab := newNativeControlLab(t)
	cfg := carpool.Config{Enabled: true, AuthorizationMode: "managed", MainInstanceID: uuid.NewString(), NodeID: uuid.NewString(), CarID: uuid.NewString()}
	node, err := carpool.NewNodeControl(ctx, lab.db, cfg)
	require.NoError(t, err)
	var account int64
	require.NoError(t, lab.db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,status,schedulable) VALUES('late member fixture','openai','oauth','{}','active',true) RETURNING id`).Scan(&account))
	_, err = node.InitializeSubscription(ctx, node.Target(), carpool.NativeSetupInput{Capacity: 4, AccountID: account})
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Microsecond)
	reset := now.Add(time.Hour)
	_, err = node.ApplyQuotaObservation(ctx, account, carpool.QuotaObservation{CapturedAt: now, ObservedAt: now, ResetsAt: reset}, false)
	require.NoError(t, err)
	subject := uuid.NewString()
	member, err := node.Provision(ctx, node.Target(), subject)
	require.NoError(t, err)
	end := now.Add(30 * 24 * time.Hour)
	require.NoError(t, node.ApplyGrant(ctx, node.Target(), subject, carpool.ManagedGrant{NativeSubscription: true, Version: 1, Status: "active", ValidFrom: &now, ValidUntil: &end}))
	var anchor time.Time
	require.NoError(t, lab.db.QueryRow(`SELECT weekly_window_start FROM user_subscriptions WHERE user_id=$1`, member.UserID).Scan(&anchor))
	require.True(t, anchor.Equal(reset.Add(-7*24*time.Hour)), "initialization before activation must not leave members on a different week")
}
