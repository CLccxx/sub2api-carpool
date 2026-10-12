//go:build integration

package carpool_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestNativeQuotaCorrectionsPreserveAlreadyResetCharges(t *testing.T) {
	ctx := context.Background()
	lab := newNativeControlLab(t)
	node, err := carpool.NewNodeControl(ctx, lab.db, carpool.Config{Enabled: true, AuthorizationMode: "managed", MainInstanceID: uuid.NewString(), NodeID: uuid.NewString(), CarID: uuid.NewString()})
	require.NoError(t, err)
	var accountID int64
	require.NoError(t, lab.db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,status,schedulable) VALUES('cycle correction','openai','oauth','{}','active',true) RETURNING id`).Scan(&accountID))
	_, err = node.InitializeSubscription(ctx, node.Target(), carpool.NativeSetupInput{Capacity: 4, AccountID: accountID})
	require.NoError(t, err)
	start, end := time.Now().Add(-28*24*time.Hour).UTC().Truncate(time.Microsecond), time.Now().Add(30*24*time.Hour).UTC().Truncate(time.Microsecond)
	subject := uuid.NewString()
	_, err = node.Provision(ctx, node.Target(), subject)
	require.NoError(t, err)
	require.NoError(t, node.ApplyGrant(ctx, node.Target(), subject, carpool.ManagedGrant{NativeSubscription: true, Version: 1, Status: "active", ValidFrom: &start, ValidUntil: &end}))
	const week = 7 * 24 * time.Hour
	for _, weeks := range []int{0, 1, 3} {
		for _, drift := range []time.Duration{-2 * time.Minute, 2 * time.Minute} {
			for _, advanced := range []bool{false, true} {
				name := fmt.Sprintf("weeks=%d/drift=%s", weeks, drift)
				if advanced {
					name += "/native-reset"
				}
				t.Run(name, func(t *testing.T) {
					stamp := time.Now().UTC().Truncate(time.Microsecond)
					anchor := stamp.Add(-5 * time.Minute)
					reset := anchor.Add(week)
					oldReset := reset.Add(-time.Duration(weeks)*week - drift)
					window := oldReset.Add(time.Duration(weeks-1) * week)
					if !advanced && weeks > 0 {
						window = window.Add(-week)
					}
					_, err = lab.db.Exec(`UPDATE carpool_native_setup SET cycle_reset_at=$1,observed_at=$2,last_read_at=NULL;`, oldReset, oldReset.Add(-time.Minute))
					require.NoError(t, err)
					_, err = lab.db.Exec(`UPDATE user_subscriptions SET weekly_window_start=$1,weekly_usage_usd=17`, window)
					require.NoError(t, err)
					// The previous source observation is necessarily before the current capture.
					_, err = lab.db.Exec(`UPDATE carpool_native_setup SET observed_at=least(observed_at,$1)`, stamp.Add(-time.Minute))
					require.NoError(t, err)
					observation := carpool.QuotaObservation{CapturedAt: stamp, ObservedAt: stamp, ResetsAt: reset, Utilization: 0}
					_, err = node.ApplyQuotaObservation(ctx, accountID, observation, false)
					require.NoError(t, err)
					var used float64
					var actualAnchor time.Time
					require.NoError(t, lab.db.QueryRow(`SELECT weekly_usage_usd,weekly_window_start FROM user_subscriptions`).Scan(&used, &actualAnchor))
					if advanced || weeks == 0 {
						require.Equal(t, 17.0, used, "current-cycle charges must survive a clock correction")
					} else {
						require.Zero(t, used, "a window still in a previous cycle must reset")
					}
					require.True(t, anchor.Equal(actualAnchor), "all rows must align, including those already reset natively")
					_, err = lab.db.Exec(`UPDATE user_subscriptions SET weekly_usage_usd=22`)
					require.NoError(t, err)
					_, err = node.ApplyQuotaObservation(ctx, accountID, observation, true)
					require.NoError(t, err)
					require.NoError(t, lab.db.QueryRow(`SELECT weekly_usage_usd FROM user_subscriptions`).Scan(&used))
					require.Equal(t, 22.0, used, "a manual replay cannot reset the corrected cycle again")
				})
			}
		}
	}

	t.Run("same-cycle correction after native maintenance", func(t *testing.T) {
		stamp := time.Now().UTC().Truncate(time.Microsecond)
		oldReset, reset := stamp.Add(-time.Minute), stamp.Add(time.Minute)
		_, err = lab.db.Exec(`UPDATE carpool_native_setup SET cycle_reset_at=$1,observed_at=$2,last_read_at=NULL`, oldReset, stamp.Add(-2*time.Minute))
		require.NoError(t, err)
		_, err = lab.db.Exec(`UPDATE user_subscriptions SET weekly_window_start=$1,weekly_usage_usd=31`, oldReset)
		require.NoError(t, err)
		_, err = node.ApplyQuotaObservation(ctx, accountID, carpool.QuotaObservation{CapturedAt: stamp, ObservedAt: stamp, ResetsAt: reset}, false)
		require.NoError(t, err)
		var used float64
		var anchor time.Time
		require.NoError(t, lab.db.QueryRow(`SELECT weekly_usage_usd,weekly_window_start FROM user_subscriptions`).Scan(&used, &anchor))
		require.Equal(t, 31.0, used)
		require.True(t, anchor.Equal(reset), "a natively advanced row must not regress to the previous week")
	})
}
