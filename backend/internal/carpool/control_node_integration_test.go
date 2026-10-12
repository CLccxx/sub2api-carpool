//go:build integration

package carpool

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestNodeControlInfoPersistenceAndLegacyConflict(t *testing.T) {
	db := mainTestDB(t)
	ctx := context.Background()
	cfg := Config{Enabled: true, AuthorizationMode: "managed", MainInstanceID: uuid.NewString(), NodeID: uuid.NewString(), CarID: uuid.NewString(), GroupID: 7}
	node, err := NewNodeControl(ctx, db, cfg)
	require.NoError(t, err)
	info := node.Info()
	require.Equal(t, cfg.MainInstanceID, info.MainID)
	require.Equal(t, cfg.NodeID, info.NodeID)
	require.Equal(t, cfg.CarID, info.CarID)
	require.Equal(t, 1, info.Protocol)
	require.NotEmpty(t, info.InstanceID)
	again, err := NewNodeControl(ctx, db, cfg)
	require.NoError(t, err)
	require.Equal(t, info.InstanceID, again.Info().InstanceID)
	body, err := json.Marshal(info)
	require.NoError(t, err)
	require.NotContains(t, string(body), "admin_key")
	require.NotContains(t, string(body), "password")
	old := NewGate(db, Config{Enabled: true, NodeID: "old-node", CarID: "old-car", Issuer: "https://issuer.example"})
	now := time.Now().UTC()
	require.NoError(t, old.Apply(ctx, Grant{NodeID: "old-node", CarID: "old-car", Issuer: "https://issuer.example", Subject: "old-user", Version: 1, Status: "paused", ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour), LeaseUntil: now.Add(time.Minute)}))
	_, err = NewNodeControl(ctx, db, cfg)
	require.ErrorIs(t, err, ErrMainConflict, "managed startup must not silently reinterpret legacy grants")
}
