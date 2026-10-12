//go:build unit

package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type carpoolInvalidationCache struct {
	APIKeyCache
	deleteErr, publishErr error
	deleted, published    []string
}

func (c *carpoolInvalidationCache) DeleteAuthCache(_ context.Context, key string) error {
	c.deleted = append(c.deleted, key)
	return c.deleteErr
}

func (c *carpoolInvalidationCache) PublishAuthCacheInvalidation(_ context.Context, key string) error {
	c.published = append(c.published, key)
	return c.publishErr
}

func TestCarpoolNativeInvalidationPropagatesAuthFailuresAndRetries(t *testing.T) {
	for _, failure := range []string{"lookup", "delete", "publish"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			cfg := carpool.Config{Enabled: true, AuthorizationMode: "managed", MainInstanceID: uuid.NewString(), NodeID: uuid.NewString(), CarID: uuid.NewString()}
			mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM carpool_node_memberships\)`).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
			mock.ExpectQuery(`INSERT INTO carpool_control_instance`).WillReturnRows(sqlmock.NewRows([]string{"kind", "id"}).AddRow("node", uuid.NewString()))
			mock.ExpectQuery(`SELECT main_instance_id::text,node_id,car_id,node_instance_id::text FROM carpool_managed_node_binding`).WillReturnError(sql.ErrNoRows)
			node, err := carpool.NewNodeControl(ctx, db, cfg)
			require.NoError(t, err)
			cache := &carpoolInvalidationCache{}
			failureErr := errors.New("synthetic cache failure")
			var lookupErr error
			switch failure {
			case "lookup":
				lookupErr = failureErr
			case "delete":
				cache.deleteErr = failureErr
			case "publish":
				cache.publishErr = failureErr
			}
			api := &APIKeyService{cache: cache, apiKeyRepo: &authRepoStub{listKeysByGroupID: func(_ context.Context, groupID int64) ([]string, error) {
				require.EqualValues(t, 9, groupID)
				return []string{"synthetic-key"}, lookupErr
			}}}
			ProvideCarpoolManagedKeys(node, nil, api, &SubscriptionService{})
			grant := carpool.ManagedGrant{NativeSubscription: true, Version: 1, Status: "prepared"}
			encoded, err := json.Marshal(grant)
			require.NoError(t, err)
			sum := sha256.Sum256(encoded)
			subject := uuid.NewString()
			expectRetry := func() {
				mock.ExpectBegin()
				mock.ExpectQuery(`SELECT version,payload_hash,user_id,group_id FROM carpool_managed_members`).WillReturnRows(sqlmock.NewRows([]string{"version", "hash", "user_id", "group_id"}).AddRow(1, hex.EncodeToString(sum[:]), 7, 9))
				mock.ExpectRollback()
			}
			expectRetry()
			require.ErrorIs(t, node.ApplyGrant(ctx, node.Target(), subject, grant), carpool.ErrUnavailable, "a committed operation cannot acknowledge failed authentication-cache invalidation")
			require.NoError(t, mock.ExpectationsWereMet())
			lookupErr, cache.deleteErr, cache.publishErr = nil, nil, nil
			expectRetry()
			require.NoError(t, node.ApplyGrant(ctx, node.Target(), subject, grant))
			require.Contains(t, cache.deleted, api.authCacheKey("synthetic-key"))
			require.Contains(t, cache.published, api.authCacheKey("synthetic-key"))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
