//go:build integration

package carpool

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManualVehicleMode(t *testing.T) {
	db := mainTestDB(t)
	var cfg MainConfig
	require.NoError(t, json.Unmarshal([]byte(`{"Enabled":true,"ManualEnrollmentEnabled":true}`), &cfg))
	s := NewMainService(db, cfg)
	car := publishedCar(t, s, 2)
	raw, err := json.Marshal(car)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(raw, &fields))
	require.Equal(t, "manual", fields["enrollment_mode"])
	require.False(t, car.Reservable)
	_, err = s.Reserve(context.Background(), 2, car.ID, "manual-must-not-reserve")
	require.ErrorIs(t, err, ErrMainNotFound)
}

func manualInput(user int64, reference string) ManualEnrollmentInput {
	return ManualEnrollmentInput{UserID: user, AmountMinor: 9900, Channel: "alipay", ReceivedAt: time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond), Reference: reference, Note: "private-receipt-note", ConfirmReceived: true}
}

func TestManualEnrollmentLifecycle(t *testing.T) {
	db := mainTestDB(t)
	ctx := context.Background()
	s := NewMainService(db, MainConfig{Enabled: true, ManualEnrollmentEnabled: true})
	car := publishedCar(t, s, 2)
	require.Equal(t, ManualRefundPolicy, car.RefundPolicy)
	firstInput := manualInput(2, "receipt-1")
	first, err := s.EnrollManual(ctx, 1, car.ID, "enroll-1", firstInput)
	require.NoError(t, err)
	require.Equal(t, "manual", first.Mode)
	require.Equal(t, "confirmed", first.Status)
	require.Nil(t, first.ExpiresAt)
	require.Nil(t, first.Membership.ServiceStart)
	require.Equal(t, firstInput.Reference, first.Receipt.Reference)
	replayed, err := s.EnrollManual(ctx, 1, car.ID, "enroll-1", firstInput)
	require.NoError(t, err)
	require.Equal(t, first.ID, replayed.ID)
	changed := firstInput
	changed.Note = "different"
	_, err = s.EnrollManual(ctx, 1, car.ID, "enroll-1", changed)
	require.ErrorIs(t, err, ErrMainConflict)
	_, err = s.EnrollManual(ctx, 1, car.ID, "duplicate-user", firstInput)
	require.ErrorIs(t, err, ErrMainConflict)
	_, err = s.EnrollManual(ctx, 1, car.ID, "duplicate-reference", manualInput(3, "receipt-1"))
	require.ErrorIs(t, err, ErrMainConflict)
	second, err := s.EnrollManual(ctx, 1, car.ID, "enroll-2", manualInput(3, "receipt-2"))
	require.NoError(t, err)
	full, err := s.GetCar(ctx, car.ID, true)
	require.NoError(t, err)
	require.Equal(t, "awaiting_deployment", full.Status)
	require.Equal(t, 2, full.ConfirmedCount)
	_, err = s.EnrollManual(ctx, 1, car.ID, "over-capacity", manualInput(4, "receipt-3"))
	require.ErrorIs(t, err, ErrMainConflict)
	public, err := s.GetOrder(ctx, 2, first.ID)
	require.NoError(t, err)
	raw, err := json.Marshal(public)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "receipt-1")
	require.NotContains(t, string(raw), "private-receipt-note")
	require.NotNil(t, public.Payment)
	_, err = s.GetOrder(ctx, 3, first.ID)
	require.ErrorIs(t, err, ErrMainNotFound)
	_, err = s.CancelOrder(ctx, 2, first.ID)
	require.ErrorIs(t, err, ErrMainConflict)
	removed, err := s.WithdrawManual(ctx, 1, first.ID, "withdraw-1", ManualWithdrawalInput{Reason: "customer withdrew", Confirm: true})
	require.NoError(t, err)
	require.Equal(t, "cancelled", removed.Status)
	require.Equal(t, "pending", removed.Payment.Refund.Status)
	car, err = s.GetCar(ctx, car.ID, true)
	require.NoError(t, err)
	require.Equal(t, "needs_review", car.Status)
	require.Equal(t, full.FullAt, car.FullAt)
	require.Equal(t, full.DeliveryDue, car.DeliveryDue)
	replayed, err = s.EnrollManual(ctx, 1, car.ID, "enroll-1", firstInput)
	require.NoError(t, err)
	require.Equal(t, "cancelled", replayed.Status)
	_, err = s.EnrollManual(ctx, 1, car.ID, "cannot-refill", manualInput(4, "receipt-4"))
	require.ErrorIs(t, err, ErrMainConflict)
	_, err = s.CancelCar(ctx, 1, car.ID, "cannot deliver", true)
	require.NoError(t, err)
	second, err = s.GetManualOrder(ctx, second.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", second.Payment.Refund.Status)
	off := NewMainService(db, MainConfig{Enabled: true})
	refundInput := ManualRefundInput{AmountMinor: 9900, RefundedAt: time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond), Reference: "refund-1", ConfirmRefunded: true}
	refunded, err := off.RefundManual(ctx, 1, first.ID, "refund-1", refundInput)
	require.NoError(t, err)
	require.Equal(t, "refunded", refunded.Payment.Refund.Status)
	_, err = off.RefundManual(ctx, 1, first.ID, "refund-1", refundInput)
	require.NoError(t, err)
	refundInput.AmountMinor--
	_, err = off.RefundManual(ctx, 1, first.ID, "refund-1", refundInput)
	require.ErrorIs(t, err, ErrMainConflict)
	var receipts, refunds, grants int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_manual_receipts`).Scan(&receipts))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_manual_refunds`).Scan(&refunds))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_node_memberships`).Scan(&grants))
	require.Equal(t, 2, receipts)
	require.Equal(t, 2, refunds)
	require.Zero(t, grants)
	var balance int
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=2`).Scan(&balance))
	require.Equal(t, 10, balance)
}

func TestManualValidationExpiryAndIsolation(t *testing.T) {
	db := mainTestDB(t)
	ctx := context.Background()
	s := NewMainService(db, MainConfig{Enabled: true, ManualEnrollmentEnabled: true})
	car := publishedCar(t, s, 3)
	for index, change := range []func(*ManualEnrollmentInput){
		func(v *ManualEnrollmentInput) { v.AmountMinor-- },
		func(v *ManualEnrollmentInput) { v.ConfirmReceived = false },
		func(v *ManualEnrollmentInput) { v.Channel = "stripe" },
		func(v *ManualEnrollmentInput) { v.ReceivedAt = time.Now().Add(time.Hour) },
		func(v *ManualEnrollmentInput) { v.UserID = 999 },
	} {
		input := manualInput(2, fmt.Sprintf("invalid-%d", index))
		change(&input)
		_, err := s.EnrollManual(ctx, 1, car.ID, fmt.Sprintf("invalid-%d", index), input)
		require.Error(t, err)
	}
	first, err := s.EnrollManual(ctx, 1, car.ID, "valid", manualInput(2, "valid"))
	require.NoError(t, err)
	_, err = s.WithdrawManual(ctx, 1, first.ID, "withdraw", ManualWithdrawalInput{Reason: "withdraw", Confirm: true})
	require.NoError(t, err)
	_, err = s.EnrollManual(ctx, 1, car.ID, "pending-refund-blocks", manualInput(2, "new-receipt"))
	require.ErrorIs(t, err, ErrMainConflict)
	second, err := s.EnrollManual(ctx, 1, car.ID, "second", manualInput(3, "second"))
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE carpool_cars SET recruit_until=clock_timestamp()-interval '1 second' WHERE id=$1`, car.ID)
	require.NoError(t, err)
	before, err := s.GetOrder(ctx, 3, second.ID)
	require.NoError(t, err)
	require.Equal(t, "cancelled", before.Status)
	require.Equal(t, "pending", before.Payment.Refund.Status)
	for range 2 {
		_, err = s.Sweep(ctx)
		require.NoError(t, err)
	}
	var refunds int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_manual_refunds`).Scan(&refunds))
	require.Equal(t, 2, refunds)
	testService := NewMainService(db, MainConfig{Enabled: true, TestOrdersEnabled: true})
	testCar := publishedCar(t, testService, 2)
	_, err = s.EnrollManual(ctx, 1, testCar.ID, "cannot-enroll-test", manualInput(4, "test-car-receipt"))
	require.ErrorIs(t, err, ErrMainConflict)
	testOrder, err := testService.Reserve(ctx, 2, testCar.ID, "test-only")
	require.NoError(t, err)
	_, err = s.GetCar(ctx, testCar.ID, false)
	require.ErrorIs(t, err, ErrMainNotFound)
	_, err = s.GetOrder(ctx, 2, testOrder.ID)
	require.ErrorIs(t, err, ErrMainNotFound)
	_, err = testService.Reserve(ctx, 4, car.ID, "cannot-reserve-manual")
	require.ErrorIs(t, err, ErrMainConflict)
	_, err = testService.ConfirmTestOrder(ctx, first.ID)
	require.ErrorIs(t, err, ErrMainConflict)
}

func TestManualLastSeatAndRollback(t *testing.T) {
	db := mainTestDB(t)
	ctx := context.Background()
	s := NewMainService(db, MainConfig{Enabled: true, ManualEnrollmentEnabled: true})
	car := publishedCar(t, s, 2)
	_, err := s.EnrollManual(ctx, 1, car.ID, "first", manualInput(2, "first"))
	require.NoError(t, err)
	var wg sync.WaitGroup
	out := make(chan error, 20)
	for user := int64(10); user < 30; user++ {
		wg.Add(1)
		go func(user int64) {
			defer wg.Done()
			_, e := s.EnrollManual(ctx, 1, car.ID, fmt.Sprintf("race-%d", user), manualInput(user, fmt.Sprintf("race-%d", user)))
			out <- e
		}(user)
	}
	wg.Wait()
	close(out)
	success := 0
	for e := range out {
		if e == nil {
			success++
		} else {
			require.ErrorIs(t, e, ErrMainConflict)
		}
	}
	require.Equal(t, 1, success)
	var fullEvents int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_events WHERE car_id=$1 AND event_type='car_full'`, car.ID).Scan(&fullEvents))
	require.Equal(t, 1, fullEvents)
	other := publishedCar(t, s, 2)
	_, err = db.Exec(`CREATE FUNCTION reject_manual_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_type='manual_enrolled' THEN RAISE EXCEPTION 'injected failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_manual_event BEFORE INSERT ON carpool_events FOR EACH ROW EXECUTE FUNCTION reject_manual_event();`)
	require.NoError(t, err)
	_, err = s.EnrollManual(ctx, 1, other.ID, "rollback", manualInput(4, "rollback"))
	require.Error(t, err)
	var remaining int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_orders WHERE car_id=$1`, other.ID).Scan(&remaining))
	require.Zero(t, remaining)
	_, err = db.Exec(`DROP TRIGGER reject_manual_event ON carpool_events`)
	require.NoError(t, err)
	_, err = s.EnrollManual(ctx, 1, other.ID, "rollback", manualInput(4, "rollback"))
	require.NoError(t, err)
}

func TestManualBusinessRejectionRollsBackCommand(t *testing.T) {
	db := mainTestDB(t)
	s := NewMainService(db, MainConfig{Enabled: true, ManualEnrollmentEnabled: true})
	car := publishedCar(t, s, 2)
	err := s.carTransaction(context.Background(), car.ID, func(ctx context.Context, tx *sql.Tx, car Car) error {
		_, err := tx.ExecContext(ctx, `UPDATE carpool_cars SET name='must rollback' WHERE id=$1`, car.ID)
		if err != nil {
			return err
		}
		return ErrMainConflict
	})
	require.ErrorIs(t, err, ErrMainConflict)
	current, err := s.GetCar(context.Background(), car.ID, true)
	require.NoError(t, err)
	require.Equal(t, car.Name, current.Name)
}

func TestManualSameKeyRaceAndPermanentReference(t *testing.T) {
	db := mainTestDB(t)
	ctx := context.Background()
	s := NewMainService(db, MainConfig{Enabled: true, ManualEnrollmentEnabled: true})
	car := publishedCar(t, s, 4)
	input := manualInput(2, " stable-reference ")
	var wg sync.WaitGroup
	results := make(chan ManualOrder, 8)
	errors := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			order, err := s.EnrollManual(ctx, 1, car.ID, "same-key", input)
			results <- order
			errors <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	id := ""
	for order := range results {
		if id == "" {
			id = order.ID
		}
		require.Equal(t, id, order.ID)
	}
	_, err := s.WithdrawManual(ctx, 1, id, "withdraw", ManualWithdrawalInput{Reason: "withdraw", Confirm: true})
	require.NoError(t, err)
	_, err = s.RefundManual(ctx, 1, id, "refund", ManualRefundInput{AmountMinor: 9900, RefundedAt: time.Now().UTC().Add(-time.Second), ConfirmRefunded: true})
	require.NoError(t, err)
	_, err = s.EnrollManual(ctx, 1, car.ID, "old-reference", manualInput(2, "stable-reference"))
	require.ErrorIs(t, err, ErrMainConflict)
	_, err = s.EnrollManual(ctx, 1, car.ID, "new-reference", manualInput(2, "new-reference"))
	require.NoError(t, err)
	page, err := s.ListManualOrders(ctx, ManualOrderQuery{CarID: car.ID, RefundStatus: "refunded"})
	require.NoError(t, err)
	require.Equal(t, 1, page.Total)
	require.Len(t, page.Items, 1)
	page, err = s.ListManualOrders(ctx, ManualOrderQuery{CarID: car.ID, RefundStatus: "none"})
	require.NoError(t, err)
	require.Equal(t, 1, page.Total)
	_, err = db.Exec(`UPDATE users SET status='disabled' WHERE id=3`)
	require.NoError(t, err)
	_, err = s.EnrollManual(ctx, 1, car.ID, "disabled-user", manualInput(3, "disabled"))
	require.ErrorIs(t, err, ErrMainNotFound)
	off := NewMainService(db, MainConfig{Enabled: true})
	_, err = off.EnrollManual(ctx, 1, car.ID, "off", manualInput(4, "off"))
	require.ErrorIs(t, err, ErrMainNotFound)
	_, err = off.GetManualOrder(ctx, id)
	require.NoError(t, err)
}

func TestManualMigrationPreservesTestOrders(t *testing.T) {
	db := mainTestDB(t, true)
	body, err := migrations.ReadFile("migrations/002_main_carpool.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(body))
	require.NoError(t, err)
	sum := sha256.Sum256(body)
	_, err = db.Exec(`INSERT INTO carpool_schema_migrations(filename,checksum) VALUES('migrations/002_main_carpool.sql',$1)`, hex.EncodeToString(sum[:]))
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO carpool_cars(id,name,capacity,price_minor,currency,service_days,recruit_until,delivery_hours,refund_policy,created_by)
	 VALUES('old','Old car',2,100,'CNY',30,clock_timestamp()+interval '1 hour',24,'test',1);
	 INSERT INTO carpool_seats(car_id,seat_no,capacity) VALUES('old',1,2),('old',2,2);
	 INSERT INTO carpool_orders(id,car_id,user_id,seat_no,mode,status,snapshot,idempotency_key,request_hash,expires_at)
	 VALUES('old-order','old',2,1,'test','pending','{}','old-key',repeat('a',64),clock_timestamp()+interval '10 minutes');`)
	require.NoError(t, err)
	var before, after, mode string
	require.NoError(t, db.QueryRow(`SELECT row_to_json(o)::text FROM carpool_orders o WHERE id='old-order'`).Scan(&before))
	require.NoError(t, Migrate(context.Background(), db))
	require.NoError(t, Migrate(context.Background(), db))
	require.NoError(t, db.QueryRow(`SELECT row_to_json(o)::text FROM carpool_orders o WHERE id='old-order'`).Scan(&after))
	require.Equal(t, before, after)
	require.NoError(t, db.QueryRow(`SELECT enrollment_mode FROM carpool_cars WHERE id='old'`).Scan(&mode))
	require.Equal(t, "test", mode)
	var price int64
	var total sql.NullInt64
	require.NoError(t, db.QueryRow(`SELECT price_minor,total_price_minor FROM carpool_cars WHERE id='old'`).Scan(&price, &total))
	require.EqualValues(t, 100, price)
	require.False(t, total.Valid, "migration must not infer a new total for a historical car")
}

func TestManualAdministratorMemberDetails(t *testing.T) {
	db := mainTestDB(t)
	_, err := db.Exec(`ALTER TABLE users ADD COLUMN email TEXT; UPDATE users SET email='member@example.test' WHERE id=2`)
	require.NoError(t, err)
	s := NewMainService(db, MainConfig{Enabled: true, ManualEnrollmentEnabled: true})
	car := publishedCar(t, s, 2)
	_, err = s.EnrollManual(context.Background(), 1, car.ID, "member-details", manualInput(2, "member-details-reference"))
	require.NoError(t, err)
	page, err := s.ListMembers(context.Background(), car.ID, PageQuery{})
	require.NoError(t, err)
	raw, err := json.Marshal(page)
	require.NoError(t, err)
	require.Contains(t, string(raw), "member@example.test")
	require.Contains(t, string(raw), "member-details-reference")
}
