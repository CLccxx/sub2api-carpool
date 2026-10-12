package carpool

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

var ErrControlPending = apperrors.Conflict("CARPOOL_MEMBER_SYNC_PENDING", "node member synchronization pending")

type controlNodeClient interface {
	Provision(context.Context, string, string, ManagedTarget, string) (ManagedMember, error)
	ApplyGrant(context.Context, string, string, ManagedTarget, string, ManagedGrant) error
	CreateKey(context.Context, string, string, ManagedTarget, string, string, string) (ManagedKey, error)
	UpdateKey(context.Context, string, string, ManagedTarget, string, int64, ManagedKeyUpdate) (ManagedKey, error)
	DeleteKey(context.Context, string, string, ManagedTarget, string, int64) error
	ListMemberKeys(context.Context, string, string, ManagedTarget, int64, int64, ControlPageQuery) (ControlPage[ManagedKey], error)
	MemberUsage(context.Context, string, string, ManagedTarget, int64, int64, ControlUsageQuery) (ControlPage[ManagedUsage], error)
	MemberUsageStats(context.Context, string, string, ManagedTarget, int64, int64, ControlUsageQuery) (ManagedUsageStats, error)
}

type controlMemberSnapshot struct {
	target                        ManagedTarget
	connection                    controlStoredConnection
	membershipID, subject, status string
	remoteUserID, remoteGroupID   int64
	serviceEnd                    *time.Time
	checkedAt                     time.Time
}

func (s *ControlStore) memberSnapshot(ctx context.Context, tx *sql.Tx, car Car, actor int64, active, allowUnmapped bool) (controlMemberSnapshot, error) {
	var snap controlMemberSnapshot
	if err := activeMainUser(ctx, tx, actor); err != nil {
		return snap, err
	}
	if s.main.cfg.ManualEnrollmentEnabled && car.EnrollmentMode != "manual" {
		return snap, ErrMainNotFound
	}
	var start, end, carStart, carEnd *time.Time
	var orderStatus string
	err := tx.QueryRowContext(ctx, `SELECT m.id,m.status,m.service_start,m.service_end,o.status FROM carpool_memberships m JOIN carpool_orders o ON o.id=m.order_id
 WHERE m.car_id=$1 AND m.user_id=$2 ORDER BY m.created_at DESC,m.id DESC LIMIT 1 FOR UPDATE OF m,o`, car.ID, actor).Scan(&snap.membershipID, &snap.status, &start, &end, &orderStatus)
	if err != nil {
		return snap, err
	}
	var now time.Time
	err = tx.QueryRowContext(ctx, `SELECT service_start,service_end,clock_timestamp() FROM carpool_cars WHERE id=$1`, car.ID).Scan(&carStart, &carEnd, &now)
	if err != nil {
		return snap, err
	}
	if active && (car.Status != "active" || snap.status != "active" || orderStatus != "confirmed" || start == nil || end == nil || carStart == nil || carEnd == nil || start.After(now) || !end.After(now) || carStart.After(now) || !carEnd.After(now) || start.Before(*carStart) || end.After(*carEnd)) {
		return snap, ErrRequired
	}
	snap.checkedAt = now
	snap.serviceEnd = end
	err = tx.QueryRowContext(ctx, `SELECT id FROM carpool_nodes WHERE car_id=$1 FOR SHARE`, car.ID).Scan(&snap.target.NodeID)
	if err != nil {
		return snap, err
	}
	snap.connection, err = readControlConnection(ctx, tx, snap.target.NodeID)
	if err != nil {
		return snap, err
	}
	if snap.connection.status != "ready" || !snap.connection.pinned {
		return snap, ErrControlTransport
	}
	snap.target.MainID = s.mainID
	snap.target.CarID = car.ID
	snap.target.InstanceID = snap.connection.instance
	snap.subject, err = ensureMainSubject(ctx, tx, actor)
	if err != nil {
		return snap, err
	}
	var userID, groupID sql.NullInt64
	var instance sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT remote_user_id,remote_group_id,provisioned_instance_id::text FROM carpool_main_node_members WHERE node_id=$1 AND subject_id=$2 FOR UPDATE`, snap.target.NodeID, snap.subject).Scan(&userID, &groupID, &instance)
	if errors.Is(err, sql.ErrNoRows) && allowUnmapped {
		return snap, nil
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return snap, ErrControlPending
		}
		return snap, err
	}
	if !userID.Valid {
		if allowUnmapped {
			return snap, nil
		}
		return snap, ErrControlPending
	}
	if !groupID.Valid || !instance.Valid || instance.String != snap.target.InstanceID {
		return snap, ErrControlTarget
	}
	snap.remoteUserID = userID.Int64
	snap.remoteGroupID = groupID.Int64
	return snap, nil
}

func sameControlMember(a, b controlMemberSnapshot) bool {
	return a.target == b.target && a.connection.generation == b.connection.generation && a.connection.origin == b.connection.origin && a.membershipID == b.membershipID && a.subject == b.subject && a.remoteUserID == b.remoteUserID && a.remoteGroupID == b.remoteGroupID
}

func (s *ControlStore) memberCredential(snap controlMemberSnapshot) (string, error) {
	return s.cipher.Open(CredentialBinding{MainID: s.mainID, NodeID: snap.target.NodeID, Generation: snap.connection.generation}, snap.connection.sealed)
}

// PrepareMember creates only the internal mapping; grant delivery belongs to the durable worker.
func (s *ControlStore) PrepareMember(ctx context.Context, carID string, userID int64) error {
	var snap controlMemberSnapshot
	err := s.main.carTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx, car Car) error {
		var err error
		snap, err = s.memberSnapshot(ctx, tx, car, userID, false, true)
		if err != nil {
			return err
		}
		if snap.status != "waiting_deployment" && snap.status != "active" && snap.status != "paused" {
			return ErrMainConflict
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO carpool_main_node_members(node_id,subject_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, snap.target.NodeID, snap.subject)
		return err
	})
	if err != nil {
		return controlStoreError(err)
	}
	credential, err := s.memberCredential(snap)
	if err != nil {
		return err
	}
	member, err := s.nodeClient.Provision(ctx, snap.connection.origin, credential, snap.target, snap.subject)
	if err != nil {
		return controlStoreError(err)
	}
	if member.Subject != snap.subject || member.UserID <= 0 || member.GroupID <= 0 {
		return ErrControlProtocol
	}
	err = s.main.carTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx, car Car) error {
		current, err := s.memberSnapshot(ctx, tx, car, userID, false, true)
		if err != nil {
			return err
		}
		// Concurrent successful provision may have filled the same immutable mapping.
		if current.target != snap.target || current.connection.generation != snap.connection.generation || current.membershipID != snap.membershipID || current.subject != snap.subject {
			return ErrMainConflict
		}
		if current.remoteUserID != 0 && (current.remoteUserID != member.UserID || current.remoteGroupID != member.GroupID) {
			return ErrControlTarget
		}
		_, err = tx.ExecContext(ctx, `UPDATE carpool_main_node_members SET remote_user_id=$3,remote_group_id=$4,provisioned_instance_id=$5,updated_at=clock_timestamp() WHERE node_id=$1 AND subject_id=$2`, snap.target.NodeID, snap.subject, member.UserID, member.GroupID, snap.target.InstanceID)
		return err
	})
	return controlStoreError(err)
}

func (s *ControlStore) CreateMemberKey(ctx context.Context, actor int64, carID, operation, name string) (ManagedKey, error) {
	if actor <= 0 || !validManagedUUID(operation) || !ValidManagedKeyName(name) {
		return ManagedKey{}, ErrMainInvalid
	}
	sum := sha256.Sum256([]byte(name))
	hash := hex.EncodeToString(sum[:])
	var snap controlMemberSnapshot
	var remoteOperation string
	err := s.main.carTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx, car Car) error {
		var err error
		snap, err = s.memberSnapshot(ctx, tx, car, actor, true, false)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO carpool_main_key_commands(actor_id,operation_id,car_id,membership_id,node_id,subject_id,request_hash,remote_operation_id,generation)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(actor_id,operation_id) DO NOTHING`, actor, operation, carID, snap.membershipID, snap.target.NodeID, snap.subject, hash, uuid.NewString(), snap.connection.generation)
		if err != nil {
			return err
		}
		var storedCar, storedMember, storedSubject, storedNode, storedHash, status string
		err = tx.QueryRowContext(ctx, `SELECT car_id,membership_id,subject_id::text,node_id,request_hash,remote_operation_id::text,status FROM carpool_main_key_commands WHERE actor_id=$1 AND operation_id=$2 FOR UPDATE`, actor, operation).Scan(&storedCar, &storedMember, &storedSubject, &storedNode, &storedHash, &remoteOperation, &status)
		if err != nil {
			return err
		}
		if storedCar != carID || storedMember != snap.membershipID || storedSubject != snap.subject || storedNode != snap.target.NodeID || storedHash != hash {
			return ErrMainConflict
		}
		if status == "gone" {
			return ErrManagedKeyGone
		}
		_, err = tx.ExecContext(ctx, `UPDATE carpool_main_key_commands SET generation=$3,updated_at=clock_timestamp() WHERE actor_id=$1 AND operation_id=$2`, actor, operation, snap.connection.generation)
		return err
	})
	if err != nil {
		return ManagedKey{}, controlStoreError(err)
	}
	credential, err := s.memberCredential(snap)
	if err != nil {
		return ManagedKey{}, err
	}
	key, err := s.nodeClient.CreateKey(ctx, snap.connection.origin, credential, snap.target, snap.subject, remoteOperation, name)
	if err != nil {
		return ManagedKey{}, controlStoreError(err)
	}
	if key.ID <= 0 || key.Key == "" || key.Status != "active" || key.ExpiresAt == nil || key.ExpiresAt.After(*snap.serviceEnd) || !key.ExpiresAt.After(snap.checkedAt) {
		return ManagedKey{}, ErrControlProtocol
	}
	err = s.main.carTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx, car Car) error {
		current, err := s.memberSnapshot(ctx, tx, car, actor, true, false)
		if err != nil {
			return err
		}
		if !sameControlMember(snap, current) || key.ExpiresAt.After(*current.serviceEnd) {
			return ErrMainConflict
		}
		if !key.ExpiresAt.After(current.checkedAt) {
			return ErrControlProtocol
		}
		if err := requireNoKeyBarrier(ctx, tx, current, key.ID, false); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE carpool_main_key_commands SET remote_key_id=$4,status='succeeded',updated_at=clock_timestamp()
 WHERE actor_id=$1 AND operation_id=$2 AND generation=$3 AND status<>'gone' AND (remote_key_id IS NULL OR remote_key_id=$4)`, actor, operation, snap.connection.generation, key.ID)
		if err != nil {
			return err
		}
		count, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrManagedKeyGone
		}
		return nil
	})
	if err != nil {
		return ManagedKey{}, controlStoreError(err)
	}
	return key, nil
}
