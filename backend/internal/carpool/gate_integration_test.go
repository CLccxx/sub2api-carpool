//go:build integration

package carpool

import (
	"context"
	"database/sql"
	"io/fs"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestNodeMembershipPostgres(t *testing.T) {
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23", tcpostgres.WithDatabase("carpool_test"), tcpostgres.WithUsername("carpool"), tcpostgres.WithPassword("local-test-only"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, container.Terminate(ctx)) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.ExecContext(ctx, `CREATE TABLE users(id BIGINT PRIMARY KEY,status TEXT,deleted_at TIMESTAMPTZ);
 CREATE TABLE auth_identities(user_id BIGINT,provider_type TEXT,provider_key TEXT,provider_subject TEXT,verified_at TIMESTAMPTZ);
 INSERT INTO users VALUES(7,'active',NULL);
 INSERT INTO auth_identities VALUES(7,'oidc','issuer','alice',NULL);`)
	require.NoError(t, err)
	require.NoError(t, Migrate(ctx, db))
	require.NoError(t, Migrate(ctx, db))
	var migrationsCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM carpool_schema_migrations`).Scan(&migrationsCount))
	names, err := fs.Glob(migrations, "migrations/*.sql")
	require.NoError(t, err)
	require.Equal(t, len(names), migrationsCount)
	gate := NewGate(db, Config{Enabled: true, NodeID: "b", CarID: "1", Issuer: "issuer"})
	require.ErrorIs(t, gate.CheckUser(ctx, 7), ErrRequired)
	now := time.Now().UTC()
	g := Grant{NodeID: "b", CarID: "1", Issuer: "issuer", Subject: "alice", Version: 1, Status: "active", ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour), LeaseUntil: now.Add(time.Minute)}
	require.NoError(t, gate.Apply(ctx, g))
	require.NoError(t, gate.Apply(ctx, g))
	require.NoError(t, gate.CheckUser(ctx, 7))
	require.NoError(t, gate.CheckIdentity(ctx, "issuer", "alice"))
	require.ErrorIs(t, gate.CheckUser(ctx, 8), ErrRequired)
	require.ErrorIs(t, gate.CheckIdentity(ctx, "other", "alice"), ErrRequired)
	conflict := g
	conflict.Status = "revoked"
	require.ErrorIs(t, gate.Apply(ctx, conflict), ErrVersionConflict)
	revoked := g
	revoked.Version = 2
	revoked.Status = "revoked"
	require.NoError(t, gate.Apply(ctx, revoked))
	require.ErrorIs(t, gate.CheckUser(ctx, 7), ErrRequired)
	require.ErrorIs(t, gate.Apply(ctx, g), ErrVersionConflict)
	g.Version = 3
	g.LeaseUntil = now.Add(-time.Second)
	require.NoError(t, gate.Apply(ctx, g))
	require.ErrorIs(t, gate.CheckUser(ctx, 7), ErrRequired)
	g.Version = 4
	g.ValidUntil = now.Add(-time.Second)
	g.LeaseUntil = now.Add(-2 * time.Second)
	require.NoError(t, gate.Apply(ctx, g))
	require.ErrorIs(t, gate.CheckUser(ctx, 7), ErrRequired)
	var wg sync.WaitGroup
	applyErrors := make(chan error, 10)
	for version := int64(5); version < 15; version++ {
		wg.Add(1)
		go func(v int64) {
			defer wg.Done()
			m := revoked
			m.Version = v
			applyErrors <- gate.Apply(ctx, m)
		}(version)
	}
	wg.Wait()
	close(applyErrors)
	for applyErr := range applyErrors {
		if applyErr != nil {
			require.ErrorIs(t, applyErr, ErrVersionConflict)
		}
	}
	var latest int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT version FROM carpool_node_memberships`).Scan(&latest))
	require.Equal(t, int64(14), latest)
	require.ErrorIs(t, gate.CheckUser(ctx, 7), ErrRequired)
	_, err = db.ExecContext(ctx, `UPDATE carpool_schema_migrations SET checksum='tampered'`)
	require.NoError(t, err)
	require.ErrorContains(t, Migrate(ctx, db), "checksum mismatch")
	require.NoError(t, db.Close())
	require.ErrorIs(t, gate.CheckUser(ctx, 7), ErrUnavailable)
}
