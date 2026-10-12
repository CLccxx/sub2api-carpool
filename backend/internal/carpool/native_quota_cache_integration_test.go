//go:build integration

package carpool_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type failingQuotaKeyRepo struct {
	service.APIKeyRepository
	err error
}

func (r *failingQuotaKeyRepo) ListKeysByGroupID(ctx context.Context, id int64) ([]string, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.APIKeyRepository.ListKeysByGroupID(ctx, id)
}

type failingQuotaAuthCache struct {
	service.APIKeyCache
	deleteErr, publishErr error
	deletes, publishes    int
}

func (c *failingQuotaAuthCache) DeleteAuthCache(context.Context, string) error {
	c.deletes++
	return c.deleteErr
}
func (c *failingQuotaAuthCache) PublishAuthCacheInvalidation(context.Context, string) error {
	c.publishes++
	return c.publishErr
}

func TestNativeQuotaAuthCacheFailureRemainsDurableUntilRetry(t *testing.T) {
	ctx := context.Background()
	lab := newNativeControlLab(t)
	cfg := carpool.Config{Enabled: true, AuthorizationMode: "managed", MainInstanceID: uuid.NewString(), NodeID: uuid.NewString(), CarID: uuid.NewString()}
	node, err := carpool.NewNodeControl(ctx, lab.db, cfg)
	require.NoError(t, err)
	var account int64
	require.NoError(t, lab.db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,status,schedulable) VALUES('cache recovery','openai','oauth','{}','active',true) RETURNING id`).Scan(&account))
	setup, err := node.InitializeSubscription(ctx, node.Target(), carpool.NativeSetupInput{Capacity: 4, AccountID: account})
	require.NoError(t, err)
	subject := uuid.NewString()
	member, err := node.Provision(ctx, node.Target(), subject)
	require.NoError(t, err)
	start, end := time.Now().Add(-time.Minute).UTC().Truncate(time.Microsecond), time.Now().Add(30*24*time.Hour).UTC().Truncate(time.Microsecond)
	require.NoError(t, node.ApplyGrant(ctx, node.Target(), subject, carpool.ManagedGrant{NativeSubscription: true, Version: 1, Status: "active", ValidFrom: &start, ValidUntil: &end}))
	repo := &failingQuotaKeyRepo{APIKeyRepository: repository.NewAPIKeyRepository(lab.ent, lab.db)}
	require.NoError(t, repo.Create(ctx, &service.APIKey{UserID: member.UserID, GroupID: &setup.GroupID, Key: "synthetic-" + uuid.NewString(), Name: "cache recovery", Status: service.StatusActive}))
	_, err = lab.db.Exec(`UPDATE user_subscriptions SET weekly_usage_usd=17`)
	require.NoError(t, err)
	for _, failure := range []string{"lookup", "delete", "publish"} {
		t.Run(failure, func(t *testing.T) {
			cache := &failingQuotaAuthCache{}
			failureErr := errors.New("synthetic unavailable")
			switch failure {
			case "lookup":
				repo.err = failureErr
			case "delete":
				cache.deleteErr = failureErr
			case "publish":
				cache.publishErr = failureErr
			}
			api := service.NewAPIKeyService(repo, nil, nil, nil, nil, cache, &config.Config{})
			service.ProvideCarpoolManagedKeys(node, lab.ent, api, &service.SubscriptionService{})
			stamp, estimate := time.Now().UTC(), 2000.0
			_, err = node.ApplyQuotaObservation(ctx, account, carpool.QuotaObservation{CapturedAt: stamp, ObservedAt: stamp, ResetsAt: stamp.Add(24 * time.Hour), Utilization: 25, EstimatedTotal: &estimate}, true)
			require.ErrorIs(t, err, carpool.ErrUnavailable)
			status, err := node.NativeQuotaStatus(ctx)
			require.NoError(t, err)
			require.True(t, status.CachePending, "the real database must retain the unacknowledged revision")
			require.Equal(t, 475.0, status.WeeklyLimitUSD, "the native transaction has already committed")
			repo.err, cache.deleteErr, cache.publishErr = nil, nil, nil
			node, err = carpool.NewNodeControl(ctx, lab.db, cfg)
			require.NoError(t, err)
			service.ProvideCarpoolManagedKeys(node, lab.ent, api, &service.SubscriptionService{})
			require.NoError(t, node.FlushQuotaCaches(ctx))
			status, err = node.NativeQuotaStatus(ctx)
			require.NoError(t, err)
			require.False(t, status.CachePending)
			require.Positive(t, cache.deletes)
			require.Positive(t, cache.publishes)
			var used float64
			require.NoError(t, lab.db.QueryRow(`SELECT weekly_usage_usd FROM user_subscriptions`).Scan(&used))
			require.Equal(t, 17.0, used, "retry only invalidates caches; it must not replay billing or resets")
		})
	}
}
