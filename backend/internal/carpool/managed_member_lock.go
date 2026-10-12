package carpool

import (
	"context"
	"database/sql"
)

// ManagedQuery allows native ent operations and membership checks to share a transaction.
type ManagedQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (n *NodeControl) LockManagedMember(ctx context.Context, tx ManagedQuery, target ManagedTarget, subject string, active bool) (ManagedMember, error) {
	if err := n.validateMemberTarget(target, subject); err != nil {
		return ManagedMember{}, err
	}
	// Only native mode needs the native subscription tables; legacy fixtures remain unchanged.
	var native bool
	check, err := tx.QueryContext(ctx, `SELECT EXISTS(SELECT 1 FROM carpool_native_setup)`)
	if err != nil {
		return ManagedMember{}, ErrUnavailable
	}
	if !check.Next() || check.Scan(&native) != nil {
		_ = check.Close()
		return ManagedMember{}, ErrUnavailable
	}
	_ = check.Close()
	access := `m.lease_until>clock_timestamp()`
	if native {
		access = `m.native_subscription AND EXISTS(SELECT 1 FROM user_subscriptions s WHERE s.user_id=m.user_id AND s.group_id=m.group_id AND s.deleted_at IS NULL AND s.status='active' AND s.starts_at<=clock_timestamp() AND s.expires_at>clock_timestamp())`
	}
	rows, err := tx.QueryContext(ctx, `SELECT m.subject_id::text,m.user_id,m.group_id,m.version,m.status,m.valid_from,m.valid_until,m.lease_until,
 (u.status='active' AND u.deleted_at IS NULL AND u.role='user' AND u.restrict_public_groups
 AND g.status='active' AND g.deleted_at IS NULL
 AND EXISTS(SELECT 1 FROM user_allowed_groups a WHERE a.user_id=m.user_id AND a.group_id=m.group_id)
 AND m.status='active' AND m.valid_from<=clock_timestamp() AND m.valid_until>clock_timestamp() AND `+access+`)
 FROM carpool_managed_members m JOIN users u ON u.id=m.user_id JOIN groups g ON g.id=m.group_id
 WHERE m.main_instance_id=$1 AND m.subject_id=$2 AND m.node_id=$3 AND m.car_id=$4 AND m.node_instance_id=$5 AND ($6=0 OR m.group_id=$6)
 FOR UPDATE OF m,u FOR SHARE OF g`, target.MainID, subject, target.NodeID, target.CarID, target.InstanceID, n.cfg.GroupID)
	if err != nil {
		return ManagedMember{}, ErrUnavailable
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if rows.Err() != nil {
			return ManagedMember{}, ErrUnavailable
		}
		return ManagedMember{}, ErrMainNotFound
	}
	var member ManagedMember
	var allowed bool
	if err := rows.Scan(&member.Subject, &member.UserID, &member.GroupID, &member.Version, &member.Status, &member.ValidFrom, &member.ValidUntil, &member.LeaseUntil, &allowed); err != nil {
		return ManagedMember{}, ErrUnavailable
	}
	if active && !allowed {
		return ManagedMember{}, ErrRequired
	}
	return member, nil
}
