package carpool

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"
)

func (n *NodeControl) NativeQuotaAccount(ctx context.Context) (int64, error) {
	var id int64
	err := n.db.QueryRowContext(ctx, `SELECT account_id FROM carpool_native_setup WHERE singleton`).Scan(&id)
	return id, controlStoreError(err)
}

func (n *NodeControl) UsesNativeWeeklyAnchor(ctx context.Context, groupID int64) (bool, error) {
	var native bool
	err := n.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM carpool_native_setup WHERE group_id=$1 AND cycle_reset_at IS NOT NULL)`, groupID).Scan(&native)
	return native, controlStoreError(err)
}

func (n *NodeControl) NativeQuotaStatus(ctx context.Context) (NativeQuotaStatus, error) {
	return readNativeQuotaStatus(ctx, n.db)
}

func readNativeQuotaStatus(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (NativeQuotaStatus, error) {
	var status NativeQuotaStatus
	err := query.QueryRowContext(ctx, `SELECT g.weekly_limit_usd,s.utilization,s.estimated_total,s.observed_at,s.resets_at,s.last_read_at,s.quota_updated_at,s.cycle_synced_at,s.last_error_code,s.quota_revision>s.cache_ack_revision
 FROM carpool_native_setup s JOIN groups g ON g.id=s.group_id WHERE s.singleton AND g.deleted_at IS NULL AND g.subscription_type='subscription'`).Scan(&status.WeeklyLimitUSD, &status.Utilization, &status.EstimatedTotal, &status.ObservedAt, &status.ResetsAt, &status.LastCheckedAt, &status.QuotaUpdatedAt, &status.CycleSyncedAt, &status.LastErrorCode, &status.CachePending)
	if err != nil {
		return status, controlStoreError(err)
	}
	switch {
	case status.LastErrorCode != "":
		status.State = "error"
	case status.ObservedAt == nil || status.ResetsAt == nil:
		status.State = "unavailable"
	case time.Since(*status.ObservedAt) > nativeObservationMaxAge || !status.ResetsAt.After(time.Now()):
		status.State = "stale"
	case status.CachePending:
		status.State = "synchronizing"
	default:
		status.State = "ready"
	}
	return status, nil
}

func (n *NodeControl) RecordQuotaReadFailure(ctx context.Context, captured time.Time) error {
	captured = captured.UTC().Truncate(time.Microsecond)
	_, err := n.db.ExecContext(ctx, `UPDATE carpool_native_setup SET last_read_at=$1,last_error_code='source_unavailable' WHERE singleton AND (last_read_at IS NULL OR last_read_at<=$1)`, captured)
	return controlStoreError(err)
}

func (n *NodeControl) ApplyQuotaObservation(ctx context.Context, accountID int64, sample QuotaObservation, force bool) (NativeQuotaStatus, error) {
	// Compare at the same precision PostgreSQL stores, including on exact retries.
	sample = sample.canonical()
	if err := sample.validate(time.Now()); err != nil {
		return NativeQuotaStatus{}, err
	}
	tx, err := n.db.BeginTx(ctx, nil)
	if err != nil {
		return NativeQuotaStatus{}, ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	var groupID int64
	var capacity int
	var lastRead, oldObserved, cycleReset, lastQuota *time.Time
	var now time.Time
	err = tx.QueryRowContext(ctx, `SELECT group_id,capacity,last_read_at,observed_at,cycle_reset_at,quota_updated_at,clock_timestamp() FROM carpool_native_setup WHERE singleton AND account_id=$1 FOR UPDATE`, accountID).Scan(&groupID, &capacity, &lastRead, &oldObserved, &cycleReset, &lastQuota, &now)
	if err != nil {
		return NativeQuotaStatus{}, controlStoreError(err)
	}
	if err = sample.validate(now); err != nil {
		return NativeQuotaStatus{}, err
	}
	var lockedGroup int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM groups WHERE id=$1 AND deleted_at IS NULL AND subscription_type='subscription' FOR UPDATE`, groupID).Scan(&lockedGroup); err != nil {
		return NativeQuotaStatus{}, controlStoreError(err)
	}
	if lastRead != nil && sample.CapturedAt.Before(*lastRead) || oldObserved != nil && sample.ObservedAt.Before(*oldObserved) {
		return NativeQuotaStatus{}, ErrMainConflict
	}
	newCycle, err := nativeCycleChanged(cycleReset, oldObserved, sample)
	if err != nil {
		return NativeQuotaStatus{}, err
	}
	anchorChanged := cycleReset == nil || !sample.ResetsAt.Equal(*cycleReset)
	if anchorChanged {
		anchor := sample.ResetsAt.Add(-nativeWeek)
		if cycleReset == nil {
			// First alignment changes the clock, never the usage already recorded.
			_, err = tx.ExecContext(ctx, `UPDATE user_subscriptions SET weekly_window_start=$2,updated_at=clock_timestamp() WHERE group_id=$1 AND deleted_at IS NULL`, groupID, anchor)
		} else {
			weeks := time.Duration(math.Round(float64(sample.ResetsAt.Sub(*cycleReset)) / float64(nativeWeek)))
			cutoff := cycleReset.Add((weeks - 1) * nativeWeek)
			if anchor.Before(cutoff) {
				cutoff = anchor
			}
			// Classify the locked native window against both anchors, then align it.
			// A row already advanced by request-time maintenance retains its new charges.
			_, err = tx.ExecContext(ctx, `UPDATE user_subscriptions SET
 weekly_usage_usd=CASE WHEN $4 AND (weekly_window_start IS NULL OR weekly_window_start<$3::timestamptz) THEN 0 ELSE weekly_usage_usd END,
 weekly_window_start=$2::timestamptz+greatest(0,floor(extract(epoch FROM (weekly_window_start-$3::timestamptz))/604800))*interval '168 hours',
 updated_at=clock_timestamp() WHERE group_id=$1 AND deleted_at IS NULL`, groupID, anchor, cutoff, newCycle)
		}
		if err != nil {
			return NativeQuotaStatus{}, controlStoreError(err)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE carpool_native_setup SET cycle_reset_at=$1,cycle_synced_at=clock_timestamp() WHERE singleton`, sample.ResetsAt); err != nil {
			return NativeQuotaStatus{}, controlStoreError(err)
		}
	}
	updateQuota := (force || newCycle || lastQuota == nil || now.Sub(*lastQuota) >= time.Hour) && (sample.EstimatedTotal != nil || newCycle)
	if updateQuota {
		limit, e := nativeWeeklyLimit(capacity, sample.EstimatedTotal)
		if e != nil {
			return NativeQuotaStatus{}, e
		}
		result, updateErr := tx.ExecContext(ctx, `UPDATE groups SET weekly_limit_usd=$2,updated_at=clock_timestamp() WHERE id=$1 AND deleted_at IS NULL AND subscription_type='subscription'`, groupID, limit)
		if updateErr != nil {
			return NativeQuotaStatus{}, controlStoreError(updateErr)
		}
		if count, countErr := result.RowsAffected(); countErr != nil || count != 1 {
			return NativeQuotaStatus{}, ErrMainConflict
		}
		if _, err = tx.ExecContext(ctx, `UPDATE carpool_native_setup SET quota_updated_at=clock_timestamp() WHERE singleton`); err != nil {
			return NativeQuotaStatus{}, controlStoreError(err)
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE carpool_native_setup SET last_read_at=$1,observed_at=$2,resets_at=$3,utilization=$4,estimated_total=$5,last_error_code='',quota_revision=quota_revision+$6 WHERE singleton`, sample.CapturedAt, sample.ObservedAt, sample.ResetsAt, sample.Utilization, sample.EstimatedTotal, boolInt(anchorChanged || updateQuota))
	if err != nil {
		return NativeQuotaStatus{}, controlStoreError(err)
	}
	if err = tx.Commit(); err != nil {
		return NativeQuotaStatus{}, controlStoreError(err)
	}
	if err = n.FlushQuotaCaches(ctx); err != nil {
		return NativeQuotaStatus{}, err
	}
	return n.NativeQuotaStatus(ctx)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (n *NodeControl) FlushQuotaCaches(ctx context.Context) error {
	var groupID, revision, ack int64
	err := n.db.QueryRowContext(ctx, `SELECT group_id,quota_revision,cache_ack_revision FROM carpool_native_setup WHERE singleton`).Scan(&groupID, &revision, &ack)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return controlStoreError(err)
	}
	if revision == ack {
		return nil
	}
	rows, err := n.db.QueryContext(ctx, `SELECT user_id FROM user_subscriptions WHERE group_id=$1 ORDER BY user_id`, groupID)
	if err != nil {
		return controlStoreError(err)
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return controlStoreError(err)
	}
	for _, id := range ids {
		if err = n.invalidateNative(ctx, true, id, groupID); err != nil {
			return err
		}
	}
	_, err = n.db.ExecContext(ctx, `UPDATE carpool_native_setup SET cache_ack_revision=$1 WHERE singleton AND quota_revision=$1`, revision)
	return controlStoreError(err)
}
