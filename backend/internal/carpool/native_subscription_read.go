package carpool

import (
	"context"
	"database/sql"
	"math"
	"time"
)

type NativeMemberSubscription struct {
	GroupName          string            `json:"group_name"`
	Platform           string            `json:"platform"`
	Status             string            `json:"status"`
	StartsAt           time.Time         `json:"starts_at"`
	ExpiresAt          time.Time         `json:"expires_at"`
	WeeklyWindowStart  *time.Time        `json:"weekly_window_start"`
	WeeklyUsedUSD      float64           `json:"weekly_used_usd"`
	WeeklyLimitUSD     float64           `json:"weekly_limit_usd"`
	WeeklyRemainingUSD float64           `json:"weekly_remaining_usd"`
	Account            NativeQuotaStatus `json:"account"`
	APIBaseURL         string            `json:"api_base_url"`
	ServerTime         time.Time         `json:"server_time"`
}

func (n *NodeControl) MemberSubscription(ctx context.Context, target ManagedTarget, subject string) (NativeMemberSubscription, error) {
	var result NativeMemberSubscription
	if err := n.validateMemberTarget(target, subject); err != nil {
		return result, err
	}
	tx, err := n.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return result, ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	err = tx.QueryRowContext(ctx, `SELECT g.name,g.platform,s.status,s.starts_at,s.expires_at,s.weekly_window_start,s.weekly_usage_usd,g.weekly_limit_usd,clock_timestamp()
 FROM user_subscriptions s JOIN groups g ON g.id=s.group_id JOIN carpool_native_setup ns ON ns.group_id=g.id
 JOIN carpool_managed_members m ON m.user_id=s.user_id AND m.group_id=s.group_id
 WHERE m.main_instance_id=$1 AND m.subject_id=$2 AND m.node_id=$3 AND m.car_id=$4 AND m.node_instance_id=$5 AND ($6=0 OR m.group_id=$6)
 AND m.native_subscription AND s.deleted_at IS NULL AND g.deleted_at IS NULL AND g.subscription_type='subscription'`, target.MainID, subject, target.NodeID, target.CarID, target.InstanceID, n.cfg.GroupID).Scan(&result.GroupName, &result.Platform, &result.Status, &result.StartsAt, &result.ExpiresAt, &result.WeeklyWindowStart, &result.WeeklyUsedUSD, &result.WeeklyLimitUSD, &result.ServerTime)
	if err != nil {
		return result, controlStoreError(err)
	}
	if result.Status == "active" && !result.ExpiresAt.After(result.ServerTime) {
		result.Status = "expired"
	}
	result.WeeklyRemainingUSD = math.Max(0, result.WeeklyLimitUSD-result.WeeklyUsedUSD)
	result.Account, err = readNativeQuotaStatus(ctx, tx)
	if err != nil {
		return NativeMemberSubscription{}, err
	}
	return result, controlStoreError(tx.Commit())
}
