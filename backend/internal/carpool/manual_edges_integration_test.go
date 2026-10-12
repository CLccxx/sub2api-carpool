//go:build integration

package carpool

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManualRecordingTime(t *testing.T) {
	db := mainTestDB(t)
	ctx := context.Background()
	s := NewMainService(db, MainConfig{Enabled: true, ManualEnrollmentEnabled: true})
	car := publishedCar(t, s, 2)
	order, err := s.EnrollManual(ctx, 1, car.ID, "recording-time", manualInput(2, "recording-time"))
	require.NoError(t, err)
	require.NotNil(t, order.ConfirmedAt)
	require.False(t, order.Payment.RecordedAt.Before(*order.ConfirmedAt), "receipt recording time must follow admission, not transaction start")
	var beforeQueue time.Time
	err = s.carTransaction(ctx, car.ID, func(ctx context.Context, tx *sql.Tx, car Car) error {
		if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&beforeQueue); err != nil {
			return err
		}
		return queueManualRefunds(ctx, tx, car.ID, order.ID, "clock test", 1)
	})
	require.NoError(t, err)
	var requested time.Time
	require.NoError(t, db.QueryRow(`SELECT requested_at FROM carpool_manual_refunds WHERE order_id=$1`, order.ID).Scan(&requested))
	require.False(t, requested.Before(beforeQueue), "refund request time must reflect insertion")
}

func TestManualCapacitiesAndReferenceChannels(t *testing.T) {
	db := mainTestDB(t)
	ctx := context.Background()
	s := NewMainService(db, MainConfig{Enabled: true, ManualEnrollmentEnabled: true})
	for _, capacity := range []int{2, 3, 4} {
		car := publishedCar(t, s, capacity)
		for seat := 1; seat <= capacity; seat++ {
			input := manualInput(int64(seat+1), "")
			if seat <= 2 {
				input.Reference = fmt.Sprintf("shared-reference-%d", capacity)
			}
			if seat == 2 {
				input.Channel = "wechat"
			}
			_, err := s.EnrollManual(ctx, 1, car.ID, fmt.Sprintf("capacity-%d-seat-%d", capacity, seat), input)
			require.NoError(t, err)
		}
		full, err := s.GetCar(ctx, car.ID, true)
		require.NoError(t, err)
		require.Equal(t, "awaiting_deployment", full.Status)
		require.Equal(t, capacity, full.ConfirmedCount)
		require.Zero(t, full.AvailableCount)
	}
}

func TestManualRefundValidationAndImmutableReplay(t *testing.T) {
	db := mainTestDB(t)
	ctx := context.Background()
	s := NewMainService(db, MainConfig{Enabled: true, ManualEnrollmentEnabled: true})
	car := publishedCar(t, s, 2)
	input := manualInput(2, "immutable-receipt")
	order, err := s.EnrollManual(ctx, 1, car.ID, "enroll", input)
	require.NoError(t, err)
	refund := ManualRefundInput{AmountMinor: 9900, RefundedAt: time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond), ConfirmRefunded: true}
	_, err = s.RefundManual(ctx, 1, order.ID, "too-early", refund)
	require.ErrorIs(t, err, ErrMainConflict)
	_, err = s.WithdrawManual(ctx, 1, order.ID, "withdraw", ManualWithdrawalInput{Reason: "withdraw", Confirm: true})
	require.NoError(t, err)
	for index, mutate := range []func(*ManualRefundInput){
		func(v *ManualRefundInput) { v.AmountMinor-- },
		func(v *ManualRefundInput) { v.RefundedAt = time.Time{} },
		func(v *ManualRefundInput) { v.RefundedAt = time.Now().Add(time.Hour) },
		func(v *ManualRefundInput) { v.RefundedAt = input.ReceivedAt.Add(-time.Second) },
		func(v *ManualRefundInput) { v.ConfirmRefunded = false },
	} {
		invalid := refund
		mutate(&invalid)
		_, err = s.RefundManual(ctx, 1, order.ID, fmt.Sprintf("invalid-%d", index), invalid)
		require.ErrorIs(t, err, ErrMainInvalid)
	}
	refunded, err := s.RefundManual(ctx, 1, order.ID, "refund", refund)
	require.NoError(t, err)
	replayed, err := s.RefundManual(ctx, 1, order.ID, "same-facts", refund)
	require.NoError(t, err)
	require.Equal(t, refunded.RefundRecord, replayed.RefundRecord)
	changed := refund
	changed.Reference = "cannot-overwrite"
	_, err = s.RefundManual(ctx, 1, order.ID, "changed-facts", changed)
	require.ErrorIs(t, err, ErrMainConflict)
	off := NewMainService(db, MainConfig{Enabled: true})
	public, err := off.GetOrder(ctx, 2, order.ID)
	require.NoError(t, err)
	require.Equal(t, "refunded", public.Payment.Refund.Status)
	require.Nil(t, public.Membership.Receipt)
	require.Nil(t, public.Membership.RefundRecord)
	require.Empty(t, public.Membership.UserEmail)
	current, err := off.GetManualOrder(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, order.Receipt, current.Receipt)
	require.Equal(t, order.Payment.RecordedAt, current.Payment.RecordedAt)
}

func TestManualEnrollmentDeadlineAfterUserLock(t *testing.T) {
	db := mainTestDB(t)
	ctx := context.Background()
	s := NewMainService(db, MainConfig{Enabled: true, ManualEnrollmentEnabled: true})
	car := publishedCar(t, s, 2)
	first, err := s.EnrollManual(ctx, 1, car.ID, "first", manualInput(2, "first"))
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `SELECT id FROM users WHERE id=3 FOR UPDATE`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE carpool_cars SET recruit_until=clock_timestamp()+interval '500 milliseconds' WHERE id=$1`, car.ID)
	require.NoError(t, err)
	out := make(chan error, 1)
	go func() {
		_, e := s.EnrollManual(ctx, 1, car.ID, "late", manualInput(3, "late"))
		out <- e
	}()
	require.Eventually(t, func() bool {
		var waiting bool
		e := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE 'SELECT id FROM users%')`).Scan(&waiting)
		return e == nil && waiting
	}, 2*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool {
		var expired bool
		e := db.QueryRow(`SELECT recruit_until<=clock_timestamp() FROM carpool_cars WHERE id=$1`, car.ID).Scan(&expired)
		return e == nil && expired
	}, 2*time.Second, 10*time.Millisecond)
	require.NoError(t, tx.Commit())
	require.ErrorIs(t, <-out, ErrMainConflict)
	projected, err := s.GetOrder(ctx, 2, first.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", projected.Payment.Refund.Status)
	// A fresh service simulates the expiry worker restarting after the request.
	restarted := NewMainService(db, MainConfig{Enabled: true})
	for range 2 {
		_, err = restarted.Sweep(ctx)
		require.NoError(t, err)
	}
	var receipts, refunds, members int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_manual_receipts`).Scan(&receipts))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_manual_refunds WHERE status='pending'`).Scan(&refunds))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_memberships WHERE status='waiting_deployment'`).Scan(&members))
	require.Equal(t, 1, receipts)
	require.Equal(t, 1, refunds)
	require.Zero(t, members)
}
