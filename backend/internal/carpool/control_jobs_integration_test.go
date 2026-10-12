//go:build integration

package carpool

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type syncRemoteStub struct {
	*memberRemoteStub
	grantMu        sync.Mutex
	grants         map[string]ManagedGrant
	versions       []int64
	err            error
	afterGrant     func()
	provisionErr   error
	afterProvision func()
}

func (r *syncRemoteStub) Provision(ctx context.Context, origin, credential string, target ManagedTarget, subject string) (ManagedMember, error) {
	m, err := r.memberRemoteStub.Provision(ctx, origin, credential, target, subject)
	if err != nil {
		return m, err
	}
	if r.afterProvision != nil {
		r.afterProvision()
	}
	return m, r.provisionErr
}

func (r *syncRemoteStub) ApplyGrant(_ context.Context, _, _ string, _ ManagedTarget, subject string, g ManagedGrant) error {
	r.grantMu.Lock()
	r.versions = append(r.versions, g.Version)
	old := r.grants[subject]
	if g.Version < old.Version {
		r.grantMu.Unlock()
		return ErrVersionConflict
	}
	r.grants[subject] = g
	err, after := r.err, r.afterGrant
	r.grantMu.Unlock()
	if after != nil {
		after()
	}
	return err
}

func syncFixture(t *testing.T) (*ControlStore, *syncRemoteStub, Car, Node) {
	t.Helper()
	s, remote, car, node := mainControlFixture(t)
	r := &syncRemoteStub{memberRemoteStub: remote, grants: map[string]ManagedGrant{}}
	s.nodeClient = r
	return s, r, car, node
}

func TestControlSyncPublishesPreparedBeforeSending(t *testing.T) {
	s, remote, car, _ := syncFixture(t)
	ctx := context.Background()
	var count int
	require.NoError(t, s.main.db.QueryRow(`SELECT count(*) FROM carpool_control_grants WHERE status='prepared' AND delivery='pending'`).Scan(&count))
	require.Equal(t, 2, count, "connection verification publishes work atomically")
	require.Empty(t, remote.members, "HTTP must not run in the connection transaction")
	for i := 0; i < 2; i++ {
		worked, err := s.SyncNext(ctx)
		require.NoError(t, err)
		require.True(t, worked)
	}
	require.Len(t, remote.members, 2)
	for _, g := range remote.grants {
		require.Equal(t, "prepared", g.Status)
		require.Nil(t, g.ValidFrom)
		require.Nil(t, g.ValidUntil)
		require.Nil(t, g.LeaseUntil)
	}
	require.NoError(t, s.main.db.QueryRow(`SELECT count(*) FROM carpool_memberships WHERE car_id=$1 AND service_start IS NOT NULL`, car.ID).Scan(&count))
	require.Zero(t, count)
	worked, err := s.SyncNext(ctx)
	require.NoError(t, err)
	require.False(t, worked)
}

func TestControlSyncWithdrawalAndQueueAreAtomic(t *testing.T) {
	s, _, car, _ := syncFixture(t)
	ctx := context.Background()
	var order string
	require.NoError(t, s.main.db.QueryRow(`SELECT order_id FROM carpool_memberships WHERE car_id=$1 AND user_id=2`, car.ID).Scan(&order))
	_, err := s.main.db.Exec(`CREATE FUNCTION reject_control_sync() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic sync unavailable'; END $$;
 CREATE TRIGGER reject_control_sync BEFORE UPDATE ON carpool_control_grants FOR EACH ROW EXECUTE FUNCTION reject_control_sync()`)
	require.NoError(t, err)
	_, err = s.main.WithdrawManual(ctx, 1, order, "withdraw-atomic", ManualWithdrawalInput{Reason: "synthetic withdrawal", Confirm: true})
	require.Error(t, err)
	var status string
	require.NoError(t, s.main.db.QueryRow(`SELECT status FROM carpool_orders WHERE id=$1`, order).Scan(&status))
	require.Equal(t, "confirmed", status)
	_, err = s.main.db.Exec(`DROP TRIGGER reject_control_sync ON carpool_control_grants`)
	require.NoError(t, err)
	_, err = s.main.WithdrawManual(ctx, 1, order, "withdraw-atomic", ManualWithdrawalInput{Reason: "synthetic withdrawal", Confirm: true})
	require.NoError(t, err)
	require.NoError(t, s.main.db.QueryRow(`SELECT g.status FROM carpool_control_grants g JOIN carpool_main_subjects s USING(subject_id) WHERE s.user_id=2`).Scan(&status))
	require.Equal(t, "revoked", status)
}

func TestControlSyncRetryLeaseAndLateResponse(t *testing.T) {
	s, remote, car, node := syncFixture(t)
	ctx := context.Background()
	remote.err = ErrControlTransport
	worked, err := s.SyncNext(ctx)
	require.True(t, worked)
	require.NoError(t, err, "remote failure is durably recorded")
	var subject, operation string
	var version int64
	var delay float64
	require.NoError(t, s.main.db.QueryRow(`SELECT subject_id::text,operation_id::text,version,EXTRACT(EPOCH FROM (available_at-updated_at)) FROM carpool_control_grants WHERE attempts=1`).Scan(&subject, &operation, &version, &delay))
	require.InDelta(t, 2, delay, 0.401)
	_, err = s.main.db.Exec(`UPDATE carpool_control_grants SET available_at=clock_timestamp()-INTERVAL '1 second' WHERE subject_id=$1`, subject)
	require.NoError(t, err)
	remote.err = nil
	// A new store object represents process restart; claim and operation metadata live in PostgreSQL.
	restarted := *s
	_, err = restarted.SyncNext(ctx)
	require.NoError(t, err)
	var ack int64
	require.NoError(t, s.main.db.QueryRow(`SELECT ack_version FROM carpool_control_grants WHERE subject_id=$1`, subject).Scan(&ack))
	require.Equal(t, version, ack)
	require.Equal(t, version, remote.grants[subject].Version)
	_, err = restarted.SyncNext(ctx)
	require.NoError(t, err)
	activateMainFixture(t, s.main.db, car.ID)
	require.NoError(t, s.ReconcileControl(ctx))
	remote.afterGrant = func() {
		_, e := s.main.db.Exec(`UPDATE users SET status='disabled' WHERE id=2 OR id=3`)
		require.NoError(t, e)
		require.NoError(t, s.ReconcileControl(ctx))
	}
	_, err = s.SyncNext(ctx)
	require.NoError(t, err)
	var syncedActive int
	require.NoError(t, s.main.db.QueryRow(`SELECT count(*) FROM carpool_control_grants WHERE node_id=$1 AND status='active' AND delivery='synced'`, node.ID).Scan(&syncedActive))
	require.Zero(t, syncedActive, "late active ACK cannot replace revoked work")
	remote.afterGrant = nil
	for i := 0; i < 2; i++ {
		_, err = s.SyncNext(ctx)
		require.NoError(t, err)
	}
	for _, g := range remote.grants {
		require.Equal(t, "revoked", g.Status)
	}
}

func TestControlSyncClaimsAndTerminalErrors(t *testing.T) {
	s, remote, car, _ := syncFixture(t)
	ctx := context.Background()
	claims := make(chan controlSyncClaim, 6)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := s.claimControlSync(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			if c != nil {
				claims <- *c
			}
		}()
	}
	wg.Wait()
	close(claims)
	require.Len(t, claims, 1)
	var first controlSyncClaim
	for c := range claims {
		first = c
	}
	_, err := s.main.db.Exec(`UPDATE carpool_control_grants SET claim_until=clock_timestamp()-INTERVAL '1 second' WHERE subject_id=$1`, first.subject)
	require.NoError(t, err)
	next, err := s.claimControlSync(ctx)
	require.NoError(t, err)
	require.NotNil(t, next)
	require.NotEqual(t, first.token, next.token)
	require.Equal(t, first.operation, next.operation)
	require.NoError(t, s.finishControlSync(ctx, first, nil))
	var currentDelivery string
	require.NoError(t, s.main.db.QueryRow(`SELECT delivery FROM carpool_control_grants WHERE subject_id=$1`, first.subject).Scan(&currentDelivery))
	require.Equal(t, "leased", currentDelivery, "an expired owner cannot acknowledge the replacement claim")
	_, err = s.main.db.Exec(`UPDATE carpool_control_grants SET claim_until=clock_timestamp()-INTERVAL '1 second' WHERE delivery='leased'`)
	require.NoError(t, err)
	remote.err = ErrControlAuth
	for i := 0; i < 2; i++ {
		_, err = s.SyncNext(ctx)
		require.NoError(t, err)
	}
	require.NoError(t, s.ReconcileControl(ctx))
	worked, err := s.SyncNext(ctx)
	require.NoError(t, err)
	require.False(t, worked, "auth failures require operator retry")
	require.NoError(t, s.RetryControlSync(ctx, 1, car.ID))
	remote.err = nil
	worked, err = s.SyncNext(ctx)
	require.NoError(t, err)
	require.True(t, worked)
}

func TestControlSyncRechecksAuthorityBeforeSend(t *testing.T) {
	s, remote, car, _ := syncFixture(t)
	ctx := context.Background()
	activateMainFixture(t, s.main.db, car.ID)
	require.NoError(t, s.ReconcileControl(ctx))
	c, err := s.claimControlSync(ctx)
	require.NoError(t, err)
	require.NotNil(t, c)
	_, err = s.main.db.Exec(`UPDATE carpool_memberships SET status='paused' WHERE car_id=$1`, car.ID)
	require.NoError(t, err)
	require.NoError(t, s.deliverControlSync(ctx, *c))
	require.Empty(t, remote.grants, "an old queued active command must not be sent")
	for i := 0; i < 2; i++ {
		_, err = s.SyncNext(ctx)
		require.NoError(t, err)
	}
	for _, g := range remote.grants {
		require.Equal(t, "paused", g.Status)
	}
}

func TestControlSyncGenerationChangeInvalidatesClaim(t *testing.T) {
	s, remote, car, node := syncFixture(t)
	ctx := context.Background()
	c, err := s.claimControlSync(ctx)
	require.NoError(t, err)
	require.NotNil(t, c)
	conn, err := s.ConnectionForCar(ctx, car.ID)
	require.NoError(t, err)
	_, err = s.SaveConnection(ctx, 1, node.ID, ControlConnectionInput{Origin: conn.Origin, InstanceID: conn.InstanceID, ExpectedGeneration: conn.Generation, Disabled: true})
	require.NoError(t, err)
	require.NoError(t, s.deliverControlSync(ctx, *c))
	require.Empty(t, remote.grants)
	var claimed sql.NullTime
	require.NoError(t, s.main.db.QueryRow(`SELECT claim_until FROM carpool_control_grants WHERE subject_id=$1`, c.subject).Scan(&claimed))
	require.False(t, claimed.Valid)
}

func TestControlSyncOneClaimPerNode(t *testing.T) {
	s, _, _, _ := syncFixture(t)
	ctx := context.Background()
	first, err := s.claimControlSync(ctx)
	require.NoError(t, err)
	require.NotNil(t, first)
	second, err := s.claimControlSync(ctx)
	require.NoError(t, err)
	require.Nil(t, second, "one slow node must not consume both delivery lanes")
}

func TestControlSyncProvisionLossAndDisabledUserRecovery(t *testing.T) {
	s, remote, car, _ := syncFixture(t)
	ctx := context.Background()
	remote.provisionErr = ErrControlTransport
	_, err := s.SyncNext(ctx)
	require.NoError(t, err)
	require.Len(t, remote.members, 1)
	var subject string
	var id int64
	for key, member := range remote.members {
		subject = key
		id = member.UserID
	}
	var persisted bool
	require.NoError(t, s.main.db.QueryRow(`SELECT remote_user_id IS NOT NULL FROM carpool_main_node_members WHERE subject_id=$1`, subject).Scan(&persisted))
	require.False(t, persisted, "response was lost, so mapping remains uncertain")
	_, err = s.main.db.Exec(`UPDATE users SET status='disabled' WHERE id=(SELECT user_id FROM carpool_main_subjects WHERE subject_id=$1)`, subject)
	require.NoError(t, err)
	require.NoError(t, s.ReconcileControl(ctx))
	remote.provisionErr = nil
	_, err = s.SyncNext(ctx)
	require.NoError(t, err)
	require.Equal(t, "revoked", remote.grants[subject].Status, "revocation wins over the other pending prepared member")
	require.Len(t, remote.members, 1, "uncertain provision is replayed, not duplicated")
	var savedID int64
	require.NoError(t, s.main.db.QueryRow(`SELECT remote_user_id FROM carpool_main_node_members WHERE subject_id=$1`, subject).Scan(&savedID))
	require.Equal(t, id, savedID)
	status, err := s.ControlSyncStatus(ctx, car.ID, PageQuery{})
	require.NoError(t, err)
	require.Equal(t, 2, status.Total)
}

func TestControlSyncRevalidatesBetweenProvisionAndGrant(t *testing.T) {
	s, remote, car, _ := syncFixture(t)
	ctx := context.Background()
	activateMainFixture(t, s.main.db, car.ID)
	require.NoError(t, s.ReconcileControl(ctx))
	remote.afterProvision = func() {
		_, err := s.main.db.Exec(`UPDATE carpool_memberships SET status='paused' WHERE car_id=$1`, car.ID)
		require.NoError(t, err)
	}
	_, err := s.SyncNext(ctx)
	require.NoError(t, err)
	require.Empty(t, remote.grants, "a provision response cannot authorize a now-paused member")
	remote.afterProvision = nil
	for i := 0; i < 2; i++ {
		_, err = s.SyncNext(ctx)
		require.NoError(t, err)
	}
	for _, g := range remote.grants {
		require.Equal(t, "paused", g.Status)
	}
}

func TestControlSyncCredentialRotationDuringGrant(t *testing.T) {
	s, remote, car, node := syncFixture(t)
	ctx := context.Background()
	conn, err := s.ConnectionForCar(ctx, car.ID)
	require.NoError(t, err)
	remote.afterGrant = func() {
		_, e := s.SaveConnection(ctx, 1, node.ID, ControlConnectionInput{Origin: conn.Origin, InstanceID: conn.InstanceID, ExpectedGeneration: conn.Generation, AdminKey: "replacement-control-key"})
		require.NoError(t, e)
	}
	_, err = s.SyncNext(ctx)
	require.NoError(t, err)
	var synced int
	require.NoError(t, s.main.db.QueryRow(`SELECT count(*) FROM carpool_control_grants WHERE delivery='synced'`).Scan(&synced))
	require.Zero(t, synced)
	worked, err := s.SyncNext(ctx)
	require.NoError(t, err)
	require.False(t, worked, "replacement must be reverified before sending")
}

func TestControlSyncRenewsBoundedLeaseAndStopsAtServiceEnd(t *testing.T) {
	s, remote, car, _ := syncFixture(t)
	ctx := context.Background()
	activateMainFixture(t, s.main.db, car.ID)
	require.NoError(t, s.ReconcileControl(ctx))
	for i := 0; i < 2; i++ {
		_, err := s.SyncNext(ctx)
		require.NoError(t, err)
	}
	var bounded bool
	require.NoError(t, s.main.db.QueryRow(`SELECT bool_and(lease_until<=valid_until AND lease_until<=clock_timestamp()+INTERVAL '15 minutes' AND lease_until>clock_timestamp()) FROM carpool_control_grants`).Scan(&bounded))
	require.True(t, bounded)
	var version int64
	require.NoError(t, s.main.db.QueryRow(`SELECT max(version) FROM carpool_control_grants`).Scan(&version))
	// Move the acknowledged deadline into the renewal interval in the isolated fixture.
	_, err := s.main.db.Exec(`UPDATE carpool_control_grants SET lease_until=clock_timestamp()+INTERVAL '9 minutes'`)
	require.NoError(t, err)
	require.NoError(t, s.ReconcileControl(ctx))
	for i := 0; i < 2; i++ {
		_, err = s.SyncNext(ctx)
		require.NoError(t, err)
	}
	for _, g := range remote.grants {
		require.Greater(t, g.Version, version)
		require.Equal(t, "active", g.Status)
	}
	_, err = s.main.db.Exec(`WITH shortened AS (UPDATE carpool_cars SET service_end=clock_timestamp()+INTERVAL '5 minutes' WHERE id=$1 RETURNING id,service_end)
 UPDATE carpool_memberships m SET service_end=c.service_end FROM shortened c WHERE m.car_id=c.id`, car.ID)
	require.NoError(t, err)
	require.NoError(t, s.ReconcileControl(ctx))
	for i := 0; i < 2; i++ {
		_, err = s.SyncNext(ctx)
		require.NoError(t, err)
	}
	for _, g := range remote.grants {
		require.True(t, g.LeaseUntil.Equal(*g.ValidUntil))
	}
	_, err = s.main.db.Exec(`UPDATE carpool_memberships SET service_end=clock_timestamp()-INTERVAL '1 second' WHERE car_id=$1`, car.ID)
	require.NoError(t, err)
	require.NoError(t, s.ReconcileControl(ctx))
	for i := 0; i < 2; i++ {
		_, err = s.SyncNext(ctx)
		require.NoError(t, err)
	}
	for _, g := range remote.grants {
		require.Equal(t, "revoked", g.Status)
		require.Nil(t, g.LeaseUntil)
	}
}

func TestControlSyncPermanentFailuresDoNotLoop(t *testing.T) {
	for _, err := range []error{ErrControlAuth, ErrControlCompliance, ErrControlProtocol, ErrControlTarget, ErrControlCredential, ErrControlGrantWindow, ErrVersionConflict} {
		require.True(t, controlSyncTerminal(err))
	}
	for _, err := range []error{ErrControlTransport, ErrMainUnavailable, context.DeadlineExceeded} {
		require.False(t, controlSyncTerminal(err))
	}
}

func TestControlSyncTransientRetryBudgetRequiresOperator(t *testing.T) {
	s, remote, car, _ := syncFixture(t)
	ctx := context.Background()
	remote.err = ErrControlTransport
	for attempt := 1; attempt <= 6; attempt++ {
		_, err := s.main.db.Exec(`UPDATE carpool_control_grants SET available_at=clock_timestamp()-INTERVAL '1 second' WHERE delivery='pending'`)
		require.NoError(t, err)
		for i := 0; i < 2; i++ {
			worked, e := s.SyncNext(ctx)
			require.NoError(t, e)
			require.True(t, worked)
		}
	}
	var stopped int
	require.NoError(t, s.main.db.QueryRow(`SELECT count(*) FROM carpool_control_grants WHERE delivery='error' AND attempts=6 AND last_error_code='CARPOOL_CONTROL_TRANSPORT'`).Scan(&stopped))
	require.Equal(t, 2, stopped, "network failures must not retry forever")
	require.NoError(t, s.ReconcileControl(ctx))
	worked, err := s.SyncNext(ctx)
	require.NoError(t, err)
	require.False(t, worked)
	require.NoError(t, s.RetryControlSync(ctx, 1, car.ID))
	remote.err = nil
	worked, err = s.SyncNext(ctx)
	require.NoError(t, err)
	require.True(t, worked)
}

func TestControlSyncRetrySchedule(t *testing.T) {
	for attempt, base := range []time.Duration{2, 5, 15, 30, 60, 60, 60} {
		for _, sample := range []float64{0, 0.5, 1} {
			d := controlRetryDelay(attempt+1, sample)
			require.GreaterOrEqual(t, d, base*time.Second*8/10)
			require.LessOrEqual(t, d, base*time.Second*12/10)
		}
	}
}
