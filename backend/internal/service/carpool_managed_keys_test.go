//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type managedInvalidationCache struct {
	APIKeyCache
	contexts []context.Context
	errors   []error
}

func (c *managedInvalidationCache) DeleteAuthCache(ctx context.Context, _ string) error {
	c.contexts = append(c.contexts, ctx)
	c.errors = append(c.errors, ctx.Err())
	return ctx.Err()
}
func (c *managedInvalidationCache) PublishAuthCacheInvalidation(ctx context.Context, _ string) error {
	c.contexts = append(c.contexts, ctx)
	c.errors = append(c.errors, ctx.Err())
	return ctx.Err()
}

func TestManagedCommittedInvalidationSurvivesRequestCancellation(t *testing.T) {
	cache := &managedInvalidationCache{}
	keys := &CarpoolManagedKeys{api: &APIKeyService{cache: cache}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	keys.invalidateCommitted(ctx, "synthetic-key")
	require.Len(t, cache.contexts, 2)
	require.Equal(t, []error{nil, nil}, cache.errors)
	for _, invalidation := range cache.contexts {
		deadline, ok := invalidation.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), 3*time.Second)
	}
}
