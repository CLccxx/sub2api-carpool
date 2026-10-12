//go:build integration

package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyExternalTransactionLifecycle(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@key-transaction.invalid"})
	repo := NewAPIKeyRepository(client, integrationDB)
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	key := &service.APIKey{UserID: user.ID, Key: "sk-" + uuid.NewString(), Name: "pending", Status: service.StatusActive}
	require.NoError(t, repo.Create(txCtx, key))
	_, err := repo.GetByID(ctx, key.ID)
	require.ErrorIs(t, err, service.ErrAPIKeyNotFound, "uncommitted key must not escape its outer transaction")
	inside, err := repo.GetByID(txCtx, key.ID)
	require.NoError(t, err)
	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	inside.ExpiresAt = &expires
	inside.Name = "bounded"
	require.NoError(t, repo.Update(txCtx, inside, service.APIKeyUpdateFields{Name: true, ExpiresAt: true}))
	inside, err = repo.GetByID(txCtx, key.ID)
	require.NoError(t, err)
	require.Equal(t, "bounded", inside.Name)
	require.True(t, expires.Equal(*inside.ExpiresAt))
	require.NoError(t, tx.Rollback())
	_, err = repo.GetByID(ctx, key.ID)
	require.ErrorIs(t, err, service.ErrAPIKeyNotFound, "command rollback must roll back key creation and expiry")
}

func nativeManagedNode(t *testing.T) (*carpool.NodeControl, carpool.Config) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, carpool.Migrate(ctx, integrationDB))
	cfg := carpool.Config{Enabled: true, AuthorizationMode: "managed"}
	err := integrationDB.QueryRow(`SELECT main_instance_id::text,node_id,car_id,group_id FROM carpool_managed_node_binding`).Scan(&cfg.MainInstanceID, &cfg.NodeID, &cfg.CarID, &cfg.GroupID)
	if errors.Is(err, sql.ErrNoRows) {
		group := mustCreateGroup(t, testEntClient(t), &service.Group{Name: "managed-" + uuid.NewString(), RateMultiplier: 1})
		cfg.MainInstanceID, cfg.NodeID, cfg.CarID, cfg.GroupID = uuid.NewString(), uuid.NewString(), uuid.NewString(), group.ID
	} else {
		require.NoError(t, err)
	}
	node, err := carpool.NewNodeControl(ctx, integrationDB, cfg)
	require.NoError(t, err)
	return node, cfg
}

func TestManagedProvisionOnNativeSchema(t *testing.T) {
	ctx := context.Background()
	node, cfg := nativeManagedNode(t)
	subject := uuid.NewString()
	member, err := node.Provision(ctx, node.Target(), subject)
	require.NoError(t, err)
	u, err := NewUserRepository(testEntClient(t), integrationDB).GetByID(ctx, member.UserID)
	require.NoError(t, err)
	require.Equal(t, service.RoleUser, u.Role)
	require.Zero(t, u.Balance)
	require.True(t, u.RestrictPublicGroups)
	require.Equal(t, []int64{cfg.GroupID}, u.AllowedGroups)
	var identities, subscriptions int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM auth_identities WHERE user_id=$1`, member.UserID).Scan(&identities))
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM user_subscriptions WHERE user_id=$1`, member.UserID).Scan(&subscriptions))
	require.Zero(t, identities)
	require.Zero(t, subscriptions)
	_, err = node.Provision(ctx, node.Target(), subject)
	require.NoError(t, err)
	require.ErrorIs(t, carpool.NewGate(integrationDB, cfg).CheckUser(ctx, member.UserID), carpool.ErrRequired)
}

func TestManagedKeysNativeAtomicLifecycle(t *testing.T) {
	ctx := context.Background()
	node, nodeCfg := nativeManagedNode(t)
	client := testEntClient(t)
	repo := NewAPIKeyRepository(client, integrationDB)
	api := service.ProvideAPIKeyService(carpool.NewGate(integrationDB, nodeCfg), repo, NewUserRepository(client, integrationDB), NewGroupRepository(client, integrationDB), NewUserSubscriptionRepository(client), NewUserGroupRateRepository(integrationDB), nil, &config.Config{CarpoolNode: nodeCfg}, nil, nil)
	keys := service.NewCarpoolManagedKeys(node, client, api)
	subject := uuid.NewString()
	member, err := node.Provision(ctx, node.Target(), subject)
	require.NoError(t, err)
	operation := uuid.NewString()
	_, err = keys.Create(ctx, node.Target(), subject, operation, "My key")
	require.ErrorIs(t, err, carpool.ErrRequired)
	now := time.Now().UTC().Truncate(time.Microsecond)
	start, end, lease := now.Add(-time.Hour), now.Add(time.Hour), now.Add(2*time.Minute)
	grant := carpool.ManagedGrant{Version: 1, Status: "active", ValidFrom: &start, ValidUntil: &end, LeaseUntil: &lease}
	require.NoError(t, node.ApplyGrant(ctx, node.Target(), subject, grant))
	type result struct {
		key service.ManagedKey
		err error
	}
	results := make(chan result, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key, err := keys.Create(ctx, node.Target(), subject, operation, "My key")
			results <- result{key, err}
		}()
	}
	wg.Wait()
	close(results)
	var first service.ManagedKey
	for r := range results {
		require.NoError(t, r.err)
		if first.ID == 0 {
			first = r.key
		} else {
			require.Equal(t, first.ID, r.key.ID)
			require.Equal(t, first.Key, r.key.Key)
		}
	}
	require.NotEmpty(t, first.Key)
	require.NotNil(t, first.ExpiresAt)
	require.True(t, end.Equal(*first.ExpiresAt))
	_, err = keys.Create(ctx, node.Target(), subject, operation, "different body")
	require.ErrorIs(t, err, carpool.ErrMainConflict)
	name := "Renamed"
	updated, err := keys.Update(ctx, node.Target(), subject, first.ID, service.ManagedKeyUpdate{Name: &name})
	require.NoError(t, err)
	require.Equal(t, name, updated.Name)
	require.Empty(t, updated.Key, "mutations must not re-expose the secret")
	otherSubject := uuid.NewString()
	_, err = node.Provision(ctx, node.Target(), otherSubject)
	require.NoError(t, err)
	require.NoError(t, node.ApplyGrant(ctx, node.Target(), otherSubject, grant))
	_, err = keys.Update(ctx, node.Target(), otherSubject, first.ID, service.ManagedKeyUpdate{Name: &name})
	require.ErrorIs(t, err, carpool.ErrMainNotFound)
	require.ErrorIs(t, keys.Delete(ctx, node.Target(), otherSubject, first.ID), carpool.ErrMainNotFound)
	disabled := "disabled"
	_, err = keys.Update(ctx, node.Target(), subject, first.ID, service.ManagedKeyUpdate{Status: &disabled})
	require.NoError(t, err)
	_, err = keys.Create(ctx, node.Target(), subject, operation, "My key")
	require.ErrorIs(t, err, service.ErrManagedKeyGone)
	require.NoError(t, node.ApplyGrant(ctx, node.Target(), subject, carpool.ManagedGrant{Version: 2, Status: "revoked"}))
	_, err = keys.Create(ctx, node.Target(), subject, operation, "My key")
	require.ErrorIs(t, err, carpool.ErrRequired)
	require.NoError(t, keys.Delete(ctx, node.Target(), subject, first.ID))
	require.NoError(t, keys.Delete(ctx, node.Target(), subject, first.ID))
	grant.Version = 3
	require.NoError(t, node.ApplyGrant(ctx, node.Target(), subject, grant))
	_, err = keys.Create(ctx, node.Target(), subject, operation, "My key")
	require.ErrorIs(t, err, service.ErrManagedKeyGone, "replay must not recreate a deleted key")
	var count int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM api_keys WHERE user_id=$1 AND deleted_at IS NULL`, member.UserID).Scan(&count))
	require.Zero(t, count)
	var metadata string
	require.NoError(t, integrationDB.QueryRow(`SELECT to_jsonb(c)::text FROM carpool_node_key_commands c WHERE subject_id=$1`, subject).Scan(&metadata))
	require.NotContains(t, metadata, first.Key)
	_, err = integrationDB.Exec(`CREATE FUNCTION fail_managed_key_command() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic command rollback'; END $$;
 CREATE TRIGGER fail_managed_key_command BEFORE INSERT ON carpool_node_key_commands FOR EACH ROW EXECUTE FUNCTION fail_managed_key_command()`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.Exec(`DROP TRIGGER IF EXISTS fail_managed_key_command ON carpool_node_key_commands`)
		require.NoError(t, err)
	})
	_, err = keys.Create(ctx, node.Target(), subject, uuid.NewString(), "must roll back")
	require.Error(t, err)
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM api_keys WHERE user_id=$1 AND deleted_at IS NULL`, member.UserID).Scan(&count))
	require.Zero(t, count, "failure to store command must roll back key and expiry")
}

func TestManagedKeysRejectExpiryDuringEnable(t *testing.T) {
	ctx := context.Background()
	node, nodeCfg := nativeManagedNode(t)
	client := testEntClient(t)
	repo := NewAPIKeyRepository(client, integrationDB)
	api := service.ProvideAPIKeyService(carpool.NewGate(integrationDB, nodeCfg), repo, NewUserRepository(client, integrationDB), NewGroupRepository(client, integrationDB), NewUserSubscriptionRepository(client), NewUserGroupRateRepository(integrationDB), nil, &config.Config{CarpoolNode: nodeCfg}, nil, nil)
	keys := service.NewCarpoolManagedKeys(node, client, api)
	subject := uuid.NewString()
	_, err := node.Provision(ctx, node.Target(), subject)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Microsecond)
	start, end, lease := now.Add(-time.Hour), now.Add(time.Hour), now.Add(time.Minute)
	require.NoError(t, node.ApplyGrant(ctx, node.Target(), subject, carpool.ManagedGrant{Version: 1, Status: "active", ValidFrom: &start, ValidUntil: &end, LeaseUntil: &lease}))
	key, err := keys.Create(ctx, node.Target(), subject, uuid.NewString(), "expiring")
	require.NoError(t, err)
	disabled := "disabled"
	_, err = keys.Update(ctx, node.Target(), subject, key.ID, service.ManagedKeyUpdate{Status: &disabled})
	require.NoError(t, err)
	_, err = integrationDB.Exec(fmt.Sprintf(`CREATE FUNCTION expire_managed_enable() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.id=%d AND NEW.status='active' THEN NEW.expires_at=clock_timestamp()-interval '1 second'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER expire_managed_enable BEFORE UPDATE OF status ON api_keys FOR EACH ROW EXECUTE FUNCTION expire_managed_enable()`, key.ID))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.Exec(`DROP TRIGGER IF EXISTS expire_managed_enable ON api_keys`)
		require.NoError(t, err)
	})
	active := "active"
	_, err = keys.Update(ctx, node.Target(), subject, key.ID, service.ManagedKeyUpdate{Status: &active})
	require.ErrorIs(t, err, service.ErrManagedKeyGone, "the committed key must still be within its database-time expiry")
	stored, err := repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, "disabled", stored.Status, "failed eligibility recheck must roll back the enable")
}
