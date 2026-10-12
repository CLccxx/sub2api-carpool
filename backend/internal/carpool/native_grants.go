package carpool

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SetSubscriptionInvalidator is configured before serving requests, never at runtime.
func (n *NodeControl) SetSubscriptionInvalidator(fn func(context.Context, int64, int64) error) {
	n.invalidateSubscription = fn
}

func (n *NodeControl) invalidateNative(ctx context.Context, native bool, userID, groupID int64) error {
	if !native || n.invalidateSubscription == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := n.invalidateSubscription(ctx, userID, groupID); err != nil {
		return ErrUnavailable
	}
	return nil
}

func (n *NodeControl) applyNativeSubscription(ctx context.Context, tx *sql.Tx, userID, groupID int64, grant ManagedGrant) error {
	var cycleReset *time.Time
	setupErr := tx.QueryRowContext(ctx, `SELECT cycle_reset_at FROM carpool_native_setup WHERE group_id=$1 FOR SHARE`, groupID).Scan(&cycleReset)
	if setupErr != nil && !errors.Is(setupErr, sql.ErrNoRows) {
		return controlStoreError(setupErr)
	}
	native := setupErr == nil
	if native != grant.NativeSubscription {
		return ErrMainConflict
	}
	if !native {
		return nil
	}
	if grant.Status != "active" {
		_, err := tx.ExecContext(ctx, `UPDATE user_subscriptions SET status='suspended',updated_at=clock_timestamp() WHERE user_id=$1 AND group_id=$2 AND deleted_at IS NULL`, userID, groupID)
		return controlStoreError(err)
	}
	var id int64
	var start, end time.Time
	var deleted *time.Time
	err := tx.QueryRowContext(ctx, `SELECT id,starts_at,expires_at,deleted_at FROM user_subscriptions WHERE user_id=$1 AND group_id=$2 FOR UPDATE`, userID, groupID).Scan(&id, &start, &end, &deleted)
	if err == nil {
		if deleted != nil || !start.Equal(*grant.ValidFrom) || !end.Equal(*grant.ValidUntil) {
			return ErrMainConflict
		}
		_, err = tx.ExecContext(ctx, `UPDATE user_subscriptions SET status='active',updated_at=clock_timestamp() WHERE id=$1`, id)
	} else if errors.Is(err, sql.ErrNoRows) {
		weeklyStart := *grant.ValidFrom
		if cycleReset != nil {
			weeklyStart = cycleReset.Add(-nativeWeek)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO user_subscriptions(user_id,group_id,starts_at,expires_at,status,daily_window_start,weekly_window_start,monthly_window_start,notes) VALUES($1,$2,$3,$4,'active',$3,$5,$3,'carpool native subscription')`, userID, groupID, grant.ValidFrom, grant.ValidUntil, weeklyStart)
	}
	return controlStoreError(err)
}

func (g *Gate) checkNativeUser(ctx context.Context, userID int64) error {
	var allowed bool
	err := g.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM carpool_managed_members m
 JOIN carpool_native_setup ns ON ns.group_id=m.group_id
 JOIN users u ON u.id=m.user_id JOIN groups gr ON gr.id=m.group_id
 JOIN user_subscriptions s ON s.user_id=m.user_id AND s.group_id=m.group_id
 JOIN carpool_control_instance i ON i.instance_id=m.node_instance_id AND i.kind='node'
 WHERE m.main_instance_id=$1 AND m.node_id=$2 AND m.car_id=$3 AND ($4=0 OR m.group_id=$4) AND m.user_id=$5
 AND m.native_subscription AND m.status='active'
 AND u.role='user' AND u.status='active' AND u.deleted_at IS NULL AND u.restrict_public_groups
 AND gr.status='active' AND gr.deleted_at IS NULL AND gr.subscription_type='subscription'
 AND EXISTS(SELECT 1 FROM user_allowed_groups a WHERE a.user_id=m.user_id AND a.group_id=m.group_id)
 AND s.deleted_at IS NULL AND s.status='active' AND s.starts_at<=clock_timestamp() AND s.expires_at>clock_timestamp())`, g.cfg.MainInstanceID, g.cfg.NodeID, g.cfg.CarID, g.cfg.GroupID, userID).Scan(&allowed)
	if err != nil {
		return ErrUnavailable
	}
	if !allowed {
		return ErrRequired
	}
	return nil
}
