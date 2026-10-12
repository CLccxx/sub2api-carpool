package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type nodeAccessStub struct {
	err   error
	calls int
}

func (s *nodeAccessStub) CheckUser(context.Context, int64) error { s.calls++; return s.err }
func (s *nodeAccessStub) CheckIdentity(context.Context, string, string) error {
	s.calls++
	return s.err
}

func TestNodeAccessFailClosedAndNoSignupGrants(t *testing.T) {
	cfg := &config.Config{CarpoolNode: carpool.Config{Enabled: true}}
	auth := &AuthService{cfg: cfg}
	user := &User{ID: 7, Role: RoleUser}
	require.ErrorIs(t, auth.CheckNodeUser(context.Background(), user), carpool.ErrUnavailable)
	denied := &nodeAccessStub{err: carpool.ErrRequired}
	auth.nodeAccess = denied
	_, err := auth.GenerateToken(context.Background(), user)
	require.ErrorIs(t, err, carpool.ErrRequired)
	_, err = auth.GenerateTokenPair(context.Background(), user, "")
	require.ErrorIs(t, err, carpool.ErrRequired)
	require.Equal(t, 2, denied.calls)
	cfg.Default.UserBalance = 999
	require.Zero(t, auth.resolveSignupGrantPlan(context.Background(), "oidc").Balance)
	require.NoError(t, auth.CheckNodeUser(context.Background(), &User{ID: 1, Role: RoleAdmin}))
	keys := &APIKeyService{cfg: cfg, nodeAccess: denied}
	_, err = keys.Create(context.Background(), 7, CreateAPIKeyRequest{Name: "denied"})
	require.ErrorIs(t, err, carpool.ErrRequired)
	require.Equal(t, 3, denied.calls)
	cfg.CarpoolNode.Enabled = false
	require.NoError(t, auth.CheckNodeUser(context.Background(), user))
	require.NoError(t, keys.CheckNodeUser(context.Background(), 7))
}

func TestManagedNodeMemberNeverGetsWebSession(t *testing.T) {
	cfg := &config.Config{CarpoolNode: carpool.Config{Enabled: true, AuthorizationMode: "managed"}}
	allowed := &nodeAccessStub{}
	auth := &AuthService{cfg: cfg, nodeAccess: allowed}
	ctx := context.Background()
	member := &User{ID: 7, Role: RoleUser}
	require.ErrorIs(t, auth.CheckNodeUser(ctx, member), carpool.ErrRequired)
	_, err := auth.GenerateToken(ctx, member)
	require.ErrorIs(t, err, carpool.ErrRequired)
	_, err = auth.GenerateTokenPair(ctx, member, "")
	require.ErrorIs(t, err, carpool.ErrRequired)
	require.ErrorIs(t, auth.CheckNodeIdentity(ctx, "oidc", "issuer", "member"), carpool.ErrRequired)
	require.Zero(t, allowed.calls, "model authorization must not be used for node web sessions")
	require.NoError(t, auth.CheckNodeUser(ctx, &User{ID: 1, Role: RoleAdmin}))
	keys := &APIKeyService{cfg: cfg, nodeAccess: allowed}
	require.NoError(t, keys.CheckNodeUser(ctx, member.ID))
	require.Equal(t, 1, allowed.calls, "model key authorization still uses the local grant gate")
}
