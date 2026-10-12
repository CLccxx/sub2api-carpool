package carpool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type NativeSetupInput struct {
	Capacity  int   `json:"capacity"`
	AccountID int64 `json:"account_id"`
}

type NativeSetup struct {
	NativeSetupInput
	GroupID        int64   `json:"group_id"`
	WeeklyLimitUSD float64 `json:"weekly_limit_usd"`
}

func (n *NodeControl) InitializeSubscription(ctx context.Context, target ManagedTarget, input NativeSetupInput) (NativeSetup, error) {
	if n == nil || target != n.Target() {
		return NativeSetup{}, ErrControlTarget
	}
	if input.Capacity < 2 || input.Capacity > 4 || input.AccountID <= 0 {
		return NativeSetup{}, ErrMainInvalid
	}
	tx, err := n.db.BeginTx(ctx, nil)
	if err != nil {
		return NativeSetup{}, ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(714420260923)`); err != nil {
		return NativeSetup{}, controlStoreError(err)
	}
	var saved NativeSetup
	err = tx.QueryRowContext(ctx, `SELECT s.capacity,s.account_id,s.group_id,g.weekly_limit_usd FROM carpool_native_setup s JOIN groups g ON g.id=s.group_id WHERE s.singleton AND g.deleted_at IS NULL AND g.status='active' AND g.subscription_type='subscription'`).Scan(&saved.Capacity, &saved.AccountID, &saved.GroupID, &saved.WeeklyLimitUSD)
	if err == nil {
		if saved.NativeSetupInput != input {
			return NativeSetup{}, ErrMainConflict
		}
		return saved, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return NativeSetup{}, controlStoreError(err)
	}
	var platform string
	if err = tx.QueryRowContext(ctx, `SELECT platform FROM accounts WHERE id=$1 AND type='oauth' AND status='active' AND schedulable AND deleted_at IS NULL FOR SHARE`, input.AccountID).Scan(&platform); err != nil {
		return NativeSetup{}, controlStoreError(err)
	}
	if platform != "openai" && platform != "anthropic" {
		return NativeSetup{}, ErrMainInvalid
	}
	var binding int64
	err = tx.QueryRowContext(ctx, `SELECT group_id FROM carpool_managed_node_binding WHERE singleton FOR UPDATE`).Scan(&binding)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return NativeSetup{}, controlStoreError(err)
	}
	// Existing phase-3 groups are accepted only if still empty of delivered keys/subscriptions.
	if binding > 0 {
		var occupied bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM api_keys WHERE group_id=$1 AND deleted_at IS NULL) OR EXISTS(SELECT 1 FROM user_subscriptions WHERE group_id=$1) OR EXISTS(SELECT 1 FROM account_groups WHERE group_id=$1 AND account_id<>$2)`, binding, input.AccountID).Scan(&occupied); err != nil {
			return NativeSetup{}, controlStoreError(err)
		}
		if occupied {
			return NativeSetup{}, ErrMainConflict
		}
		_, err = tx.ExecContext(ctx, `UPDATE groups SET name=$2,platform=$3,subscription_type='subscription',is_exclusive=true,rate_multiplier=1,daily_limit_usd=NULL,monthly_limit_usd=NULL,weekly_limit_usd=$4,updated_at=clock_timestamp() WHERE id=$1 AND status='active' AND deleted_at IS NULL`, binding, fmt.Sprintf("%d 人车分组", input.Capacity), platform, 1800.0/float64(input.Capacity))
	} else {
		err = tx.QueryRowContext(ctx, `INSERT INTO groups(name,platform,status,subscription_type,is_exclusive,rate_multiplier,weekly_limit_usd) VALUES($1,$2,'active','subscription',true,1,$3) RETURNING id`, fmt.Sprintf("%d 人车分组", input.Capacity), platform, 1800.0/float64(input.Capacity)).Scan(&binding)
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO carpool_managed_node_binding(singleton,main_instance_id,node_id,car_id,group_id,node_instance_id) VALUES(true,$1,$2,$3,$4,$5)`, target.MainID, target.NodeID, target.CarID, binding, target.InstanceID)
		}
	}
	if err != nil {
		return NativeSetup{}, controlStoreError(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id,priority) VALUES($1,$2,1) ON CONFLICT(account_id,group_id) DO NOTHING`, input.AccountID, binding); err != nil {
		return NativeSetup{}, controlStoreError(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO scheduler_outbox(event_type,account_id) VALUES('account_changed',$1)`, input.AccountID); err != nil {
		return NativeSetup{}, controlStoreError(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO carpool_native_setup(capacity,account_id,group_id) VALUES($1,$2,$3)`, input.Capacity, input.AccountID, binding); err != nil {
		return NativeSetup{}, controlStoreError(err)
	}
	if err = tx.Commit(); err != nil {
		return NativeSetup{}, controlStoreError(err)
	}
	return NativeSetup{NativeSetupInput: input, GroupID: binding, WeeklyLimitUSD: 1800.0 / float64(input.Capacity)}, nil
}
