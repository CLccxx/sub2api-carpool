package carpool

import (
	"context"
	"database/sql"
	"strings"
)

func (s *MainService) WithdrawManual(ctx context.Context, actor int64, orderID, key string, input ManualWithdrawalInput) (ManualOrder, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" || len(input.Reason) > 2000 || !input.Confirm {
		return ManualOrder{}, ErrMainInvalid
	}
	carID, err := s.orderCarID(ctx, orderID, 0)
	if err != nil {
		return ManualOrder{}, err
	}
	return s.manualCommand(ctx, actor, carID, "withdraw", orderID, key, input, func(ctx context.Context, tx *sql.Tx, car Car) (string, error) {
		o, err := readManualOrder(ctx, tx, orderID)
		if err != nil {
			return "", err
		}
		if o.Status == "cancelled" {
			return orderID, nil
		}
		if o.Status != "confirmed" || (car.Status != "recruiting" && car.Status != "awaiting_deployment" && car.Status != "needs_review") {
			return "", ErrMainConflict
		}
		if err = queueManualRefunds(ctx, tx, carID, orderID, input.Reason, actor); err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE carpool_orders SET status='cancelled' WHERE id=$1`, orderID); err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE carpool_memberships SET status='cancelled' WHERE order_id=$1`, orderID); err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE carpool_seats SET status='available',user_id=NULL,order_id=NULL,held_until=NULL WHERE order_id=$1`, orderID); err != nil {
			return "", err
		}
		if car.Status == "awaiting_deployment" {
			if _, err = tx.ExecContext(ctx, `UPDATE carpool_cars SET status='needs_review' WHERE id=$1`, carID); err != nil {
				return "", err
			}
		}
		return orderID, appendMainEvent(ctx, tx, carID, "manual_withdrawn", actor, orderID)
	})
}

func (s *MainService) RefundManual(ctx context.Context, actor int64, orderID, key string, input ManualRefundInput) (ManualOrder, error) {
	input.Reference = strings.TrimSpace(input.Reference)
	input.RefundedAt = manualTimestamp(input.RefundedAt)
	if !input.ConfirmRefunded || input.RefundedAt.IsZero() || input.AmountMinor <= 0 || !validManualReference(input.Reference) {
		return ManualOrder{}, ErrMainInvalid
	}
	carID, err := s.orderCarID(ctx, orderID, 0)
	if err != nil {
		return ManualOrder{}, err
	}
	return s.manualCommand(ctx, actor, carID, "refund", orderID, key, input, func(ctx context.Context, tx *sql.Tx, car Car) (string, error) {
		o, err := readManualOrder(ctx, tx, orderID)
		if err != nil {
			return "", err
		}
		if o.Payment == nil || o.Status != "cancelled" || o.Payment.Refund == nil {
			return "", ErrMainConflict
		}
		car, err = readMainCar(ctx, tx, carID, false)
		if err != nil {
			return "", err
		}
		if input.AmountMinor != o.Payment.AmountMinor || input.RefundedAt.After(car.ServerTime) || input.RefundedAt.Before(o.Payment.ReceivedAt) {
			return "", ErrMainInvalid
		}
		if o.Payment.Refund.Status == "refunded" {
			if o.Payment.Refund.RefundedAt == nil || !o.Payment.Refund.RefundedAt.Equal(input.RefundedAt) || o.RefundRecord == nil || o.RefundRecord.Reference != input.Reference {
				return "", ErrMainConflict
			}
			return orderID, nil
		}
		res, err := tx.ExecContext(ctx, `UPDATE carpool_manual_refunds SET status='refunded',refunded_at=$2,recorded_at=clock_timestamp(),recorded_by=$3,reference=NULLIF($4,'') WHERE order_id=$1 AND status='pending'`, orderID, input.RefundedAt, actor, input.Reference)
		if err != nil {
			return "", err
		}
		count, err := res.RowsAffected()
		if err != nil {
			return "", err
		}
		if count != 1 {
			return "", ErrMainConflict
		}
		return orderID, appendMainEvent(ctx, tx, carID, "manual_refunded", actor, orderID)
	})
}
