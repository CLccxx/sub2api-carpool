package carpool

import (
	"context"
	"database/sql"
	"errors"
	"math/rand/v2"
	"time"

	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

type controlSyncClaim struct {
	node, subject, car, operation, token string
	generation, version                  int64
}

func controlRetryDelay(attempt int, sample float64) time.Duration {
	steps := [...]time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second}
	index := min(max(attempt-1, 0), len(steps)-1)
	return time.Duration(float64(steps[index]) * (0.8 + 0.4*min(max(sample, 0), 1)))
}

func controlSyncTerminal(err error) bool {
	for _, terminal := range []error{ErrControlAuth, ErrControlCompliance, ErrControlTarget, ErrControlProtocol, ErrControlCredential, ErrVersionConflict, ErrMainInvalid, ErrMainNotFound, ErrMainConflict, ErrRequired, ErrManagedKeyGone, ErrControlGrantWindow} {
		if errors.Is(err, terminal) {
			return true
		}
	}
	return false
}

func (s *ControlStore) claimControlSync(ctx context.Context) (*controlSyncClaim, error) {
	var c *controlSyncClaim
	err := s.main.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		claim := controlSyncClaim{token: uuid.NewString()}
		err := tx.QueryRowContext(ctx, `SELECT g.node_id,g.subject_id::text,n.car_id,g.operation_id::text,g.generation,g.version
 FROM carpool_control_grants g JOIN carpool_nodes n ON n.id=g.node_id JOIN carpool_node_connections c ON c.node_id=n.id
 WHERE c.status='ready' AND c.identity_verified AND c.generation=g.generation AND g.available_at<=clock_timestamp()
 AND (g.delivery='pending' OR (g.delivery='leased' AND g.claim_until<=clock_timestamp()))
 AND NOT EXISTS(SELECT 1 FROM carpool_control_grants busy WHERE busy.node_id=g.node_id AND busy.subject_id<>g.subject_id AND busy.delivery='leased' AND busy.claim_until>clock_timestamp())
 ORDER BY CASE g.status WHEN 'revoked' THEN 0 WHEN 'paused' THEN 0 WHEN 'prepared' THEN 1 ELSE 2 END,g.available_at,g.node_id,g.subject_id
 LIMIT 1 FOR UPDATE OF c,g SKIP LOCKED`).Scan(&claim.node, &claim.subject, &claim.car, &claim.operation, &claim.generation, &claim.version)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE carpool_control_grants SET delivery='leased',claim_id=$3,claim_until=clock_timestamp()+INTERVAL '45 seconds',updated_at=clock_timestamp()
 WHERE node_id=$1 AND subject_id=$2`, claim.node, claim.subject, claim.token)
		if err == nil {
			c = &claim
		}
		return err
	})
	return c, controlStoreError(err)
}

type controlSyncSnapshot struct {
	target          ManagedTarget
	connection      controlStoredConnection
	grant           ManagedGrant
	userID, groupID int64
}

// Re-read authoritative state both before provision and before grant delivery.
// Completion of an old claim must never persist a mapping or ACK for a new command.
func (s *ControlStore) controlSyncSnapshot(ctx context.Context, c controlSyncClaim, member *ManagedMember) (*controlSyncSnapshot, error) {
	var snapshot *controlSyncSnapshot
	err := s.controlCarTransaction(ctx, c.car, func(ctx context.Context, tx *sql.Tx) error {
		if err := s.publishControlCar(ctx, tx, c.car); err != nil {
			return err
		}
		conn, err := readControlConnection(ctx, tx, c.node)
		if err != nil {
			return err
		}
		if conn.generation != c.generation || conn.status != "ready" || !conn.pinned {
			return nil
		}
		var snap controlSyncSnapshot
		var digest string
		err = tx.QueryRowContext(ctx, `SELECT version,status,valid_from,valid_until,lease_until,payload_hash,native_subscription FROM carpool_control_grants
 WHERE node_id=$1 AND subject_id=$2 AND operation_id=$3 AND claim_id=$4 AND generation=$5 AND delivery='leased' AND claim_until>clock_timestamp() FOR UPDATE`, c.node, c.subject, c.operation, c.token, c.generation).Scan(
			&snap.grant.Version, &snap.grant.Status, &snap.grant.ValidFrom, &snap.grant.ValidUntil, &snap.grant.LeaseUntil, &digest, &snap.grant.NativeSubscription)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if snap.grant.Version != c.version || controlGrantHash(snap.grant) != digest {
			return ErrControlProtocol
		}
		snap.connection = conn
		snap.target = ManagedTarget{MainID: s.mainID, NodeID: c.node, CarID: c.car, InstanceID: conn.instance}
		var instance string
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(remote_user_id,0),COALESCE(remote_group_id,0),COALESCE(provisioned_instance_id::text,'') FROM carpool_main_node_members
 WHERE node_id=$1 AND subject_id=$2 FOR UPDATE`, c.node, c.subject).Scan(&snap.userID, &snap.groupID, &instance)
		if err != nil {
			return err
		}
		if snap.userID != 0 && (instance != conn.instance || snap.groupID <= 0) {
			return ErrControlTarget
		}
		if member != nil {
			if member.Subject != c.subject || member.UserID <= 0 || member.GroupID <= 0 {
				return ErrControlProtocol
			}
			if snap.userID != 0 && (snap.userID != member.UserID || snap.groupID != member.GroupID) {
				return ErrControlTarget
			}
			_, err = tx.ExecContext(ctx, `UPDATE carpool_main_node_members SET remote_user_id=$3,remote_group_id=$4,provisioned_instance_id=$5,updated_at=clock_timestamp() WHERE node_id=$1 AND subject_id=$2`, c.node, c.subject, member.UserID, member.GroupID, conn.instance)
			if err != nil {
				return err
			}
			snap.userID, snap.groupID = member.UserID, member.GroupID
		}
		snapshot = &snap
		return nil
	})
	return snapshot, controlStoreError(err)
}

func (s *ControlStore) finishControlSync(ctx context.Context, c controlSyncClaim, remoteErr error) error {
	return controlStoreError(s.controlCarTransaction(ctx, c.car, func(ctx context.Context, tx *sql.Tx) error {
		if err := s.publishControlCar(ctx, tx, c.car); err != nil {
			return err
		}
		var attempt int
		err := tx.QueryRowContext(ctx, `SELECT attempts FROM carpool_control_grants
 WHERE node_id=$1 AND subject_id=$2 AND operation_id=$3 AND claim_id=$4 AND generation=$5 AND delivery='leased' AND claim_until>clock_timestamp() FOR UPDATE`, c.node, c.subject, c.operation, c.token, c.generation).Scan(&attempt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if remoteErr == nil {
			_, err = tx.ExecContext(ctx, `UPDATE carpool_control_grants g SET delivery='synced',ack_version=version,ack_generation=generation,synced_at=clock_timestamp(),last_error_code='',claim_id=NULL,claim_until=NULL,updated_at=clock_timestamp()
 WHERE node_id=$1 AND subject_id=$2 AND EXISTS(SELECT 1 FROM carpool_node_connections c WHERE c.node_id=g.node_id AND c.generation=g.generation AND c.status='ready')`, c.node, c.subject)
			return err
		}
		remoteErr = controlStoreError(remoteErr)
		attempt = min(attempt+1, 1000000)
		delivery := "pending"
		if controlSyncTerminal(remoteErr) || attempt >= 6 {
			delivery = "error"
		}
		delay := controlRetryDelay(attempt, rand.Float64())
		_, err = tx.ExecContext(ctx, `UPDATE carpool_control_grants SET delivery=$3,attempts=$4,last_error_code=$5,claim_id=NULL,claim_until=NULL,
 available_at=clock_timestamp()+$6*INTERVAL '1 microsecond',updated_at=clock_timestamp() WHERE node_id=$1 AND subject_id=$2`, c.node, c.subject, delivery, attempt, apperrors.Reason(remoteErr), delay.Microseconds())
		return err
	}))
}

func (s *ControlStore) deliverControlSync(ctx context.Context, c controlSyncClaim) error {
	var remoteErr error
	snap, err := s.controlSyncSnapshot(ctx, c, nil)
	if err != nil {
		remoteErr = err
	} else if snap == nil {
		return nil
	}
	if remoteErr == nil {
		var credential string
		credential, remoteErr = s.cipher.Open(CredentialBinding{MainID: s.mainID, NodeID: c.node, Generation: c.generation}, snap.connection.sealed)
		if remoteErr == nil && snap.userID == 0 {
			var member ManagedMember
			member, remoteErr = s.nodeClient.Provision(ctx, snap.connection.origin, credential, snap.target, c.subject)
			if remoteErr == nil {
				snap, remoteErr = s.controlSyncSnapshot(ctx, c, &member)
			}
		}
		if remoteErr == nil {
			if snap == nil {
				return nil
			}
			remoteErr = s.nodeClient.ApplyGrant(ctx, snap.connection.origin, credential, snap.target, c.subject, snap.grant)
		}
	}
	// A lost/cancelled HTTP response still needs a durable retry, without keeping shutdown unbounded.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.finishControlSync(finishCtx, c, remoteErr)
}

func (s *ControlStore) SyncNext(ctx context.Context) (bool, error) {
	c, err := s.claimControlSync(ctx)
	if err != nil || c == nil {
		return false, err
	}
	return true, s.deliverControlSync(ctx, *c)
}
