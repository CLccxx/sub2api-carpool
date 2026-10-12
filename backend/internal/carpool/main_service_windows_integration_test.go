//go:build integration

package carpool

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Only tests may activate service in phase 3; there is no corresponding production command.
func activateMainFixture(t *testing.T, db *sql.DB, carID string) (time.Time, time.Time) {
	t.Helper()
	start, end := time.Now().UTC().Add(-30*time.Second).Truncate(time.Microsecond), time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`UPDATE carpool_cars SET status='active',service_start=$2,service_end=$3 WHERE id=$1`, carID, start, end)
	require.NoError(t, err)
	_, err = tx.Exec(`UPDATE carpool_memberships SET status='active',service_start=$2,service_end=$3 WHERE car_id=$1 AND status='waiting_deployment'`, carID, start, end)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	return start, end
}

func TestMainServiceWindowsAndPreServiceGuard(t *testing.T) {
	db := mainTestDB(t)
	ctx := context.Background()
	s := NewMainService(db, MainConfig{Enabled: true, ManualEnrollmentEnabled: true})
	car := publishedCar(t, s, 2)
	order, err := s.EnrollManual(ctx, 1, car.ID, "one", manualInput(2, "one"))
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE carpool_memberships SET status='active' WHERE id=$1`, order.Membership.ID)
	require.Error(t, err, "active members need a service window")
	start, end := activateMainFixture(t, db, car.ID)
	activeCars, err := s.ListCars(ctx, true, PageQuery{Status: "active"})
	require.NoError(t, err)
	require.Len(t, activeCars.Items, 1)
	activeMembers, err := s.ListMembers(ctx, car.ID, PageQuery{Status: "active"})
	require.NoError(t, err)
	require.Len(t, activeMembers.Items, 1)
	_, err = db.Exec(`UPDATE carpool_memberships SET service_end=NULL WHERE id=$1`, order.Membership.ID)
	require.Error(t, err)
	_, err = db.Exec(`UPDATE carpool_memberships SET service_end=service_start WHERE id=$1`, order.Membership.ID)
	require.Error(t, err)
	_, err = s.CancelCar(ctx, 1, car.ID, "old cancellation must not start a refund", true)
	require.ErrorIs(t, err, ErrMainConflict)
	_, err = s.WithdrawManual(ctx, 1, order.ID, "withdraw", ManualWithdrawalInput{Reason: "old withdrawal", Confirm: true})
	require.ErrorIs(t, err, ErrMainConflict)
	var status string
	var gotStart, gotEnd time.Time
	require.NoError(t, db.QueryRow(`SELECT status,service_start,service_end FROM carpool_memberships WHERE id=$1`, order.Membership.ID).Scan(&status, &gotStart, &gotEnd))
	require.Equal(t, "active", status)
	require.True(t, start.Equal(gotStart))
	require.True(t, end.Equal(gotEnd))
	var refunds int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_manual_refunds WHERE order_id=$1`, order.ID).Scan(&refunds))
	require.Zero(t, refunds)
	require.NoError(t, Migrate(ctx, db))
}

func TestManualRefundRejectsServedOrderButAllowsPreServiceHistory(t *testing.T) {
	db := mainTestDB(t)
	ctx := context.Background()
	s := NewMainService(db, MainConfig{Enabled: true, ManualEnrollmentEnabled: true})
	car := publishedCar(t, s, 2)
	history, err := s.EnrollManual(ctx, 1, car.ID, "history", manualInput(2, "history"))
	require.NoError(t, err)
	_, err = s.WithdrawManual(ctx, 1, history.ID, "history-withdraw", ManualWithdrawalInput{Reason: "before service", Confirm: true})
	require.NoError(t, err)
	served, err := s.EnrollManual(ctx, 1, car.ID, "served", manualInput(3, "served"))
	require.NoError(t, err)
	activateMainFixture(t, db, car.ID)
	// Simulate a future post-service cancellation and refund proposal, not a phase-3 API.
	_, err = db.Exec(`UPDATE carpool_orders SET status='cancelled' WHERE id=$1`, served.ID)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE carpool_memberships SET status='cancelled' WHERE order_id=$1`, served.ID)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO carpool_manual_refunds(order_id,amount_minor,reason,requested_by) VALUES($1,9900,'post-service proposal',1)`, served.ID)
	require.NoError(t, err)
	input := ManualRefundInput{AmountMinor: 9900, RefundedAt: time.Now().UTC().Add(-time.Second), ConfirmRefunded: true}
	_, err = s.RefundManual(ctx, 1, served.ID, "forbidden-refund", input)
	require.ErrorIs(t, err, ErrMainConflict)
	refunded, err := s.RefundManual(ctx, 1, history.ID, "valid-refund", input)
	require.NoError(t, err)
	require.Equal(t, "refunded", refunded.Payment.Refund.Status)
	var status string
	require.NoError(t, db.QueryRow(`SELECT status FROM carpool_manual_refunds WHERE order_id=$1`, served.ID).Scan(&status))
	require.Equal(t, "pending", status)
}
