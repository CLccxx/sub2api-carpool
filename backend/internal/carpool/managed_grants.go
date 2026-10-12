package carpool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrControlGrantWindow = apperrors.BadRequest("CARPOOL_GRANT_WINDOW", "node rejected the service or lease window; verify database clocks and service dates")

type ManagedGrant struct {
	NativeSubscription bool       `json:"native_subscription,omitempty"`
	Version            int64      `json:"version"`
	Status             string     `json:"status"`
	ValidFrom          *time.Time `json:"valid_from"`
	ValidUntil         *time.Time `json:"valid_until"`
	LeaseUntil         *time.Time `json:"lease_until"`
}

func (g ManagedGrant) canonical() ManagedGrant {
	canonicalTime := func(value *time.Time) *time.Time {
		if value == nil {
			return nil
		}
		normalized := value.UTC().Truncate(time.Microsecond)
		return &normalized
	}
	g.ValidFrom, g.ValidUntil, g.LeaseUntil = canonicalTime(g.ValidFrom), canonicalTime(g.ValidUntil), canonicalTime(g.LeaseUntil)
	if g.NativeSubscription && g.Status == "active" {
		g.LeaseUntil = g.ValidUntil
	}
	return g
}

func (g ManagedGrant) validate(now time.Time) error {
	if g.Version <= 0 {
		return ErrMainInvalid
	}
	switch g.Status {
	case "prepared", "paused", "revoked":
		if g.ValidFrom != nil || g.ValidUntil != nil || g.LeaseUntil != nil {
			return ErrMainInvalid
		}
	case "active":
		if g.ValidFrom == nil || g.ValidUntil == nil || g.LeaseUntil == nil || g.ValidFrom.IsZero() || !g.ValidUntil.After(*g.ValidFrom) {
			return ErrMainInvalid
		}
		if !g.LeaseUntil.After(now) || g.LeaseUntil.After(*g.ValidUntil) || (!g.NativeSubscription && g.LeaseUntil.After(now.Add(15*time.Minute))) {
			return ErrControlGrantWindow
		}
	default:
		return ErrMainInvalid
	}
	return nil
}

func (n *NodeControl) ApplyGrant(ctx context.Context, target ManagedTarget, subject string, input ManagedGrant) error {
	if err := n.validateMemberTarget(target, subject); err != nil {
		return err
	}
	grant := input.canonical()
	encoded, err := json.Marshal(grant)
	if err != nil {
		return ErrMainInvalid
	}
	sum := sha256.Sum256(encoded)
	hash := hex.EncodeToString(sum[:])
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := n.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	var version int64
	var userID, groupID int64
	var storedHash string
	err = tx.QueryRowContext(ctx, `SELECT version,payload_hash,user_id,group_id FROM carpool_managed_members WHERE main_instance_id=$1 AND subject_id=$2
 AND node_id=$3 AND car_id=$4 AND node_instance_id=$5 AND ($6=0 OR group_id=$6) FOR UPDATE`, target.MainID, subject, target.NodeID, target.CarID, target.InstanceID, n.cfg.GroupID).Scan(&version, &storedHash, &userID, &groupID)
	if err != nil {
		return controlStoreError(err)
	}
	if grant.Version <= version {
		if grant.Version == version && storedHash == hash {
			_ = tx.Rollback()
			return n.invalidateNative(ctx, grant.NativeSubscription, userID, groupID)
		}
		return ErrVersionConflict
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return controlStoreError(err)
	}
	if err = grant.validate(now); err != nil {
		return err
	}
	if err = n.applyNativeSubscription(ctx, tx, userID, groupID, grant); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE carpool_managed_members SET version=$3,status=$4,valid_from=$5,valid_until=$6,lease_until=$7,payload_hash=$8,native_subscription=$9,updated_at=clock_timestamp()
 WHERE main_instance_id=$1 AND subject_id=$2`, target.MainID, subject, grant.Version, grant.Status, grant.ValidFrom, grant.ValidUntil, grant.LeaseUntil, hash, grant.NativeSubscription)
	if err != nil {
		return controlStoreError(err)
	}
	if err = tx.Commit(); err != nil {
		return controlStoreError(err)
	}
	return n.invalidateNative(ctx, grant.NativeSubscription, userID, groupID)
}

func (g *Gate) checkManagedUser(ctx context.Context, userID int64) error {
	var native bool
	if err := g.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM carpool_native_setup)`).Scan(&native); err != nil {
		return ErrUnavailable
	}
	if native {
		return g.checkNativeUser(ctx, userID)
	}
	var allowed bool
	err := g.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM carpool_managed_members m JOIN users u ON u.id=m.user_id
 JOIN carpool_control_instance i ON i.instance_id=m.node_instance_id AND i.kind='node'
 WHERE m.main_instance_id=$1 AND m.node_id=$2 AND m.car_id=$3 AND m.group_id=$4 AND m.user_id=$5
 AND u.status='active' AND u.deleted_at IS NULL AND u.role='user' AND u.restrict_public_groups
 AND m.status='active' AND m.valid_from<=clock_timestamp() AND m.valid_until>clock_timestamp() AND m.lease_until>clock_timestamp())`,
		g.cfg.MainInstanceID, g.cfg.NodeID, g.cfg.CarID, g.cfg.GroupID, userID).Scan(&allowed)
	if err != nil {
		return ErrUnavailable
	}
	if !allowed {
		return ErrRequired
	}
	return nil
}
