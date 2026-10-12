//go:build integration

package carpool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestControlConsoleStateDoesNotActivatePreparedMembers(t *testing.T) {
	s, _, car, _ := syncFixture(t)
	ctx := context.Background()
	state, err := s.ConsoleForMember(ctx, 2, car.ID)
	require.NoError(t, err)
	require.Equal(t, "unactivated", state.AccessStatus)
	require.False(t, state.CanCreateKey)
	require.False(t, state.CanReadNode)
	require.Nil(t, state.ServiceStart)
	require.Equal(t, "pending", state.SyncStatus)
	for i := 0; i < 2; i++ {
		_, err = s.SyncNext(ctx)
		require.NoError(t, err)
	}
	state, err = s.ConsoleForMember(ctx, 2, car.ID)
	require.NoError(t, err)
	require.True(t, state.CanReadNode)
	require.Equal(t, "synced", state.SyncStatus)
	require.False(t, state.CanCreateKey)
	require.Equal(t, "https://node.example/v1", state.APIBaseURL)
	_, err = s.ConsoleForMember(ctx, 4, car.ID)
	require.ErrorIs(t, err, ErrMainNotFound)
	_, err = s.ConsoleForMember(ctx, 1, car.ID)
	require.ErrorIs(t, err, ErrMainNotFound, "admin cannot inspect another user's console")
	encoded, err := json.Marshal(state)
	require.NoError(t, err)
	for _, secret := range []string{"subject_id", "remote_user_id", "admin_key", "synthetic-control-key", "credential_ciphertext"} {
		require.NotContains(t, string(encoded), secret)
	}
	activateMainFixture(t, s.main.db, car.ID)
	state, err = s.ConsoleForMember(ctx, 2, car.ID)
	require.NoError(t, err)
	require.Equal(t, "synchronizing", state.AccessStatus)
	require.False(t, state.CanCreateKey)
	for i := 0; i < 2; i++ {
		_, err = s.SyncNext(ctx)
		require.NoError(t, err)
	}
	state, err = s.ConsoleForMember(ctx, 2, car.ID)
	require.NoError(t, err)
	require.Equal(t, "active", state.AccessStatus)
	require.True(t, state.CanCreateKey)
	_, err = s.main.db.Exec(`UPDATE users SET status='disabled' WHERE id=2`)
	require.NoError(t, err)
	_, err = s.ConsoleForMember(ctx, 2, car.ID)
	require.ErrorIs(t, err, ErrMainNotFound)
}

func TestControlAdminSyncStatusIsPagedAndSanitized(t *testing.T) {
	s, remote, car, _ := syncFixture(t)
	ctx := context.Background()
	remote.err = ErrControlCompliance
	_, err := s.SyncNext(ctx)
	require.NoError(t, err)
	page, err := s.ControlSyncStatus(ctx, car.ID, PageQuery{Page: 1, PageSize: 1})
	require.NoError(t, err)
	require.EqualValues(t, 2, page.Total)
	require.Len(t, page.Items, 1)
	all, err := s.ControlSyncStatus(ctx, car.ID, PageQuery{Page: 1, PageSize: 20})
	require.NoError(t, err)
	var failure bool
	for _, item := range all.Items {
		if item.Delivery == "error" {
			failure = true
			require.Equal(t, "CARPOOL_CONTROL_COMPLIANCE", item.LastErrorCode)
			require.Equal(t, 1, item.Attempts)
		}
	}
	require.True(t, failure)
	_, err = s.ControlSyncStatus(ctx, car.ID, PageQuery{Page: 1, PageSize: 101})
	require.ErrorIs(t, err, ErrMainInvalid)
}

func TestControlRetryRejectsUnavailableConnection(t *testing.T) {
	s, _, car, node := syncFixture(t)
	ctx := context.Background()
	conn, err := s.ConnectionForCar(ctx, car.ID)
	require.NoError(t, err)
	_, err = s.SaveConnection(ctx, 1, node.ID, ControlConnectionInput{Origin: conn.Origin, InstanceID: conn.InstanceID, ExpectedGeneration: conn.Generation, Disabled: true})
	require.NoError(t, err)
	require.ErrorIs(t, s.RetryControlSync(ctx, 1, car.ID), ErrMainConflict)
	var events int
	require.NoError(t, s.main.db.QueryRow(`SELECT count(*) FROM carpool_events WHERE car_id=$1 AND event_type='node_sync_retried'`, car.ID).Scan(&events))
	require.Zero(t, events, "a failed retry cannot claim that work was queued")
}
