package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCarpoolWeeklyAnchorDoesNotApplyLegacyMidnightCorrection(t *testing.T) {
	anchor := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	sub := &UserSubscription{StartsAt: anchor.Add(12 * time.Hour), ExpiresAt: anchor.Add(60 * 24 * time.Hour), WeeklyWindowStart: &anchor, MonthlyWindowStart: &anchor, AccountAlignedWeekly: true}
	require.Equal(t, anchor.Add(7*24*time.Hour), *sub.WeeklyResetTime())
	start, ok := sub.automaticWindowStartAt(sub.WeeklyWindowStart, 7*24*time.Hour, anchor.Add(7*24*time.Hour))
	require.True(t, ok)
	require.Equal(t, anchor.Add(7*24*time.Hour), start)
	require.Equal(t, sub.StartsAt.Add(30*24*time.Hour), *sub.MonthlyResetTime(), "weekly policy must not change monthly legacy behavior")
	sub.AccountAlignedWeekly = false
	require.Equal(t, sub.StartsAt.Add(7*24*time.Hour), *sub.WeeklyResetTime())
	_, ok = sub.automaticWindowStartAt(sub.WeeklyWindowStart, 7*24*time.Hour, anchor.Add(7*24*time.Hour))
	require.False(t, ok, "ordinary subscriptions retain the existing legacy behavior")
}

type carpoolAnchorRepo struct {
	UserSubscriptionRepository
	sub    UserSubscription
	resets int
}

func (r *carpoolAnchorRepo) GetActiveByUserIDAndGroupID(context.Context, int64, int64) (*UserSubscription, error) {
	cp := r.sub
	return &cp, nil
}
func (r *carpoolAnchorRepo) GetByID(context.Context, int64) (*UserSubscription, error) {
	cp := r.sub
	return &cp, nil
}
func (r *carpoolAnchorRepo) ListByUserID(context.Context, int64) ([]UserSubscription, error) {
	return []UserSubscription{r.sub}, nil
}
func (r *carpoolAnchorRepo) ResetWeeklyUsage(_ context.Context, _ int64, _ *time.Time, start time.Time) error {
	r.resets++
	r.sub.WeeklyWindowStart = &start
	r.sub.WeeklyUsageUSD = 17
	return nil
}

func TestCarpoolSubscriptionPolicyAppliedToLoadsAndMaintenance(t *testing.T) {
	anchor := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	repo := &carpoolAnchorRepo{sub: UserSubscription{ID: 1, UserID: 2, GroupID: 3, StartsAt: anchor.Add(time.Hour), ExpiresAt: time.Now().Add(60 * 24 * time.Hour), WeeklyWindowStart: &anchor, WeeklyUsageUSD: 40}}
	svc := NewSubscriptionService(nil, repo, nil, nil, nil)
	svc.SetWeeklyAnchorPolicy(func(_ context.Context, groupID int64) (bool, error) {
		require.EqualValues(t, 3, groupID)
		return true, nil
	})
	sub, err := svc.GetActiveSubscription(context.Background(), 2, 3)
	require.NoError(t, err)
	require.True(t, sub.AccountAlignedWeekly)
	sub, err = svc.GetByID(context.Background(), 1)
	require.NoError(t, err)
	require.True(t, sub.AccountAlignedWeekly)
	listed, err := svc.ListUserSubscriptions(context.Background(), 2)
	require.NoError(t, err)
	require.True(t, listed[0].AccountAlignedWeekly)
	svc.now = func() time.Time { return anchor.Add(7 * 24 * time.Hour) }
	// Maintenance must also decorate an uncached repository result.
	sub.AccountAlignedWeekly = false
	fresh, err := svc.EnsureWindowMaintenance(context.Background(), sub)
	require.NoError(t, err)
	require.Equal(t, 1, repo.resets)
	require.True(t, fresh.AccountAlignedWeekly)
	require.Equal(t, 17.0, fresh.WeeklyUsageUSD, "fresh read wins over optimistic in-memory zero")
	require.Equal(t, anchor.Add(14*24*time.Hour), *fresh.WeeklyResetTime())
}
