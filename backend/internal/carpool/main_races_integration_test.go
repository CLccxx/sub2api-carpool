//go:build integration

package carpool

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMainCarpoolAtomicity(t *testing.T) {
	db := mainTestDB(t)
	ctx := context.Background()
	s := NewMainService(db, MainConfig{Enabled: true, TestOrdersEnabled: true})
	t.Run("different keys still give same user one seat", func(t *testing.T) {
		car := publishedCar(t, s, 4)
		out := make(chan error, 10)
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, e := s.Reserve(ctx, 12, car.ID, fmt.Sprintf("different-concurrent-%d", i))
				out <- e
			}(i)
		}
		wg.Wait()
		close(out)
		successes := 0
		for e := range out {
			if e == nil {
				successes++
			} else {
				require.ErrorIs(t, e, ErrMainConflict)
			}
		}
		require.Equal(t, 1, successes)
	})
	t.Run("all held is not full and cancellation restores capacity", func(t *testing.T) {
		car := publishedCar(t, s, 2)
		first, err := s.Reserve(ctx, 13, car.ID, "held-first")
		require.NoError(t, err)
		_, err = s.Reserve(ctx, 14, car.ID, "held-second")
		require.NoError(t, err)
		car, err = s.GetCar(ctx, car.ID, false)
		require.NoError(t, err)
		require.Equal(t, "recruiting", car.Status)
		require.Equal(t, 2, car.HeldCount)
		require.Zero(t, car.ConfirmedCount)
		require.Zero(t, car.AvailableCount)
		_, err = s.CancelOrder(ctx, 13, first.ID)
		require.NoError(t, err)
		car, err = s.GetCar(ctx, car.ID, false)
		require.NoError(t, err)
		require.Equal(t, 1, car.HeldCount)
		require.Equal(t, 1, car.AvailableCount)
	})
	t.Run("new service sweeps outstanding expired work", func(t *testing.T) {
		car := publishedCar(t, s, 2)
		o, err := s.Reserve(ctx, 15, car.ID, "restart-expiry")
		require.NoError(t, err)
		_, err = db.Exec(`UPDATE carpool_orders SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, o.ID)
		require.NoError(t, err)
		restarted := NewMainService(db, MainConfig{Enabled: true, TestOrdersEnabled: true})
		_, err = restarted.Sweep(ctx)
		require.NoError(t, err)
		var status string
		require.NoError(t, db.QueryRow(`SELECT status FROM carpool_orders WHERE id=$1`, o.ID).Scan(&status))
		require.Equal(t, "expired", status)
		_, err = restarted.Sweep(ctx)
		require.NoError(t, err)
	})
	t.Run("concurrent retries produce one order and member", func(t *testing.T) {
		car := publishedCar(t, s, 2)
		type outcome struct {
			o   Order
			err error
		}
		out := make(chan outcome, 10)
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); o, e := s.Reserve(ctx, 2, car.ID, "concurrent-retry"); out <- outcome{o, e} }()
		}
		wg.Wait()
		close(out)
		id := ""
		for got := range out {
			require.NoError(t, got.err)
			if id == "" {
				id = got.o.ID
			}
			require.Equal(t, id, got.o.ID)
		}
		errors := make(chan error, 10)
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); _, e := s.ConfirmTestOrder(ctx, id); errors <- e }()
		}
		wg.Wait()
		close(errors)
		for e := range errors {
			require.NoError(t, e)
		}
		var orders, members, events int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_orders WHERE car_id=$1`, car.ID).Scan(&orders))
		require.Equal(t, 1, orders)
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_memberships WHERE car_id=$1`, car.ID).Scan(&members))
		require.Equal(t, 1, members)
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_events WHERE car_id=$1 AND event_type='order_confirmed'`, car.ID).Scan(&events))
		require.Equal(t, 1, events)
	})
	t.Run("database constraints and privacy", func(t *testing.T) {
		car := publishedCar(t, s, 2)
		_, err := db.Exec(`INSERT INTO carpool_seats(car_id,seat_no,capacity) VALUES($1,3,2)`, car.ID)
		require.Error(t, err)
		o, err := s.Reserve(ctx, 3, car.ID, "constraint")
		require.NoError(t, err)
		_, err = db.Exec(`UPDATE carpool_seats SET user_id=4 WHERE order_id=$1`, o.ID)
		require.Error(t, err)
		_, err = db.Exec(`UPDATE carpool_orders SET mode='live' WHERE id=$1`, o.ID)
		require.Error(t, err)
		_, err = db.Exec(`UPDATE carpool_cars SET status='active' WHERE id=$1`, car.ID)
		require.Error(t, err)
		_, err = db.Exec(`DELETE FROM users WHERE id=3`)
		require.Error(t, err)
		cars, err := s.ListCars(ctx, false, PageQuery{})
		require.NoError(t, err)
		body, err := json.Marshal(cars)
		require.NoError(t, err)
		for _, private := range []string{"user_id", "created_by", "subject", "password", "order_id", "hostname"} {
			require.NotContains(t, string(body), private)
		}
		_, err = s.ListCars(ctx, false, PageQuery{PageSize: 101})
		require.ErrorIs(t, err, ErrMainInvalid)
		_, err = s.Mine(ctx, 3, PageQuery{Status: "active"})
		require.ErrorIs(t, err, ErrMainInvalid)
	})
	t.Run("late database failure rolls back seat order member and event", func(t *testing.T) {
		car := publishedCar(t, s, 2)
		_, err := db.Exec(`CREATE FUNCTION carpool_test_fail_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_type IN ('seat_reserved','order_confirmed') THEN RAISE EXCEPTION 'injected failure'; END IF; RETURN NEW; END $$;
CREATE TRIGGER carpool_test_fail BEFORE INSERT ON carpool_events FOR EACH ROW EXECUTE FUNCTION carpool_test_fail_event();`)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, e := db.Exec(`DROP TRIGGER IF EXISTS carpool_test_fail ON carpool_events; DROP FUNCTION IF EXISTS carpool_test_fail_event()`)
			require.NoError(t, e)
		})
		_, err = s.Reserve(ctx, 5, car.ID, "rollback")
		require.ErrorIs(t, err, ErrMainUnavailable)
		var count int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_orders WHERE car_id=$1`, car.ID).Scan(&count))
		require.Zero(t, count)
		current, err := s.GetCar(ctx, car.ID, true)
		require.NoError(t, err)
		require.Equal(t, 2, current.AvailableCount)
		require.Equal(t, car.Version, current.Version)
		_, err = db.Exec(`ALTER TABLE carpool_events DISABLE TRIGGER carpool_test_fail`)
		require.NoError(t, err)
		o, err := s.Reserve(ctx, 5, car.ID, "rollback")
		require.NoError(t, err)
		_, err = db.Exec(`ALTER TABLE carpool_events ENABLE TRIGGER carpool_test_fail`)
		require.NoError(t, err)
		_, err = s.ConfirmTestOrder(ctx, o.ID)
		require.ErrorIs(t, err, ErrMainUnavailable)
		o, err = s.GetOrder(ctx, 5, o.ID)
		require.NoError(t, err)
		require.Equal(t, "pending", o.Status)
		require.Nil(t, o.Membership)
		current, err = s.GetCar(ctx, car.ID, true)
		require.NoError(t, err)
		require.Equal(t, 1, current.HeldCount)
		require.Zero(t, current.ConfirmedCount)
	})
	t.Run("cancel and confirmation are serialized", func(t *testing.T) {
		for i := 0; i < 5; i++ {
			car := publishedCar(t, s, 2)
			first, err := s.Reserve(ctx, 9, car.ID, fmt.Sprintf("cancel-race-first-%d", i))
			require.NoError(t, err)
			_, err = s.ConfirmTestOrder(ctx, first.ID)
			require.NoError(t, err)
			o, err := s.Reserve(ctx, 6, car.ID, fmt.Sprintf("cancel-race-%d", i))
			require.NoError(t, err)
			var confirmErr, cancelErr error
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); _, confirmErr = s.ConfirmTestOrder(ctx, o.ID) }()
			go func() { defer wg.Done(); _, cancelErr = s.CancelCar(ctx, 1, car.ID, "cancel race", true) }()
			wg.Wait()
			require.NoError(t, cancelErr)
			if confirmErr != nil {
				require.ErrorIs(t, confirmErr, ErrMainConflict)
			}
			current, err := s.GetCar(ctx, car.ID, true)
			require.NoError(t, err)
			require.Equal(t, "cancelled", current.Status)
			require.Zero(t, current.HeldCount)
			require.Zero(t, current.ConfirmedCount)
			stored, err := s.GetOrder(ctx, 6, o.ID)
			require.NoError(t, err)
			require.Equal(t, "cancelled", stored.Status)
			if stored.Membership != nil {
				require.Equal(t, "cancelled", stored.Membership.Status)
			}
			var laterFull int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_events f JOIN carpool_events x ON x.car_id=f.car_id AND x.event_type='car_cancelled' WHERE f.car_id=$1 AND f.event_type='car_full' AND f.version>x.version`, car.ID).Scan(&laterFull))
			require.Zero(t, laterFull)
		}
	})
	t.Run("full event failure rolls back final confirmation", func(t *testing.T) {
		car := publishedCar(t, s, 2)
		first, err := s.Reserve(ctx, 16, car.ID, "full-first")
		require.NoError(t, err)
		_, err = s.ConfirmTestOrder(ctx, first.ID)
		require.NoError(t, err)
		last, err := s.Reserve(ctx, 17, car.ID, "full-last")
		require.NoError(t, err)
		_, err = db.Exec(`CREATE FUNCTION carpool_test_fail_full() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_type='car_full' THEN RAISE EXCEPTION 'injected full-event failure'; END IF; RETURN NEW; END $$;
CREATE TRIGGER carpool_test_fail_full BEFORE INSERT ON carpool_events FOR EACH ROW EXECUTE FUNCTION carpool_test_fail_full();`)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, e := db.Exec(`DROP TRIGGER carpool_test_fail_full ON carpool_events; DROP FUNCTION carpool_test_fail_full()`)
			require.NoError(t, e)
		})
		_, err = s.ConfirmTestOrder(ctx, last.ID)
		require.ErrorIs(t, err, ErrMainUnavailable)
		car, err = s.GetCar(ctx, car.ID, false)
		require.NoError(t, err)
		require.Equal(t, "recruiting", car.Status)
		require.Nil(t, car.FullAt)
		require.Equal(t, 1, car.ConfirmedCount)
		require.Equal(t, 1, car.HeldCount)
		members, err := s.ListMembers(ctx, car.ID, PageQuery{})
		require.NoError(t, err)
		require.Len(t, members.Items, 1)
		stored, err := s.GetOrder(ctx, 17, last.ID)
		require.NoError(t, err)
		require.Equal(t, "pending", stored.Status)
		require.Nil(t, stored.ConfirmedAt)
		var leakedEvents int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_events WHERE order_id=$1 AND event_type='order_confirmed'`, last.ID).Scan(&leakedEvents))
		require.Zero(t, leakedEvents)
	})
	t.Run("create deadline validation follows administrator row lock", func(t *testing.T) {
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()
		_, err = tx.Exec(`SELECT id FROM users WHERE id=1 FOR UPDATE`)
		require.NoError(t, err)
		input := testCarInput(2)
		input.RecruitUntil = time.Now().Add(500 * time.Millisecond)
		out := make(chan error, 1)
		go func() { _, e := s.CreateCar(ctx, 1, input); out <- e }()
		require.Eventually(t, func() bool {
			var waiting bool
			e := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE 'SELECT id FROM users%')`).Scan(&waiting)
			return e == nil && waiting
		}, time.Second, 10*time.Millisecond)
		require.Eventually(t, func() bool { return time.Now().After(input.RecruitUntil) }, time.Second, 10*time.Millisecond)
		require.NoError(t, tx.Commit())
		require.ErrorIs(t, <-out, ErrMainInvalid)
	})
	t.Run("sweep and late confirmation cannot revive expired seat", func(t *testing.T) {
		car := publishedCar(t, s, 2)
		o, err := s.Reserve(ctx, 7, car.ID, "expiry-race")
		require.NoError(t, err)
		_, err = db.Exec(`UPDATE carpool_orders SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, o.ID)
		require.NoError(t, err)
		var sweepErr, confirmErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, sweepErr = s.Sweep(ctx) }()
		go func() { defer wg.Done(); _, confirmErr = s.ConfirmTestOrder(ctx, o.ID) }()
		wg.Wait()
		require.NoError(t, sweepErr)
		require.ErrorIs(t, confirmErr, ErrMainConflict)
		stored, err := s.GetOrder(ctx, 7, o.ID)
		require.NoError(t, err)
		require.Equal(t, "expired", stored.Status)
	})
	for _, confirm := range []bool{false, true} {
		t.Run(fmt.Sprintf("deadline refreshed after user row lock wait confirm=%t", confirm), func(t *testing.T) {
			car := publishedCar(t, s, 2)
			var order Order
			var err error
			if confirm {
				order, err = s.Reserve(ctx, 8, car.ID, "confirm-user-lock-deadline")
				require.NoError(t, err)
			}
			tx, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()
			_, err = tx.ExecContext(ctx, `SELECT id FROM users WHERE id=8 FOR UPDATE`)
			require.NoError(t, err)
			// Short, bounded wall-clock wait exercises a different lock after the car lock.
			_, err = db.Exec(`UPDATE carpool_cars SET recruit_until=clock_timestamp()+interval '500 milliseconds' WHERE id=$1`, car.ID)
			require.NoError(t, err)
			out := make(chan error, 1)
			go func() {
				var e error
				if confirm {
					_, e = s.ConfirmTestOrder(ctx, order.ID)
				} else {
					_, e = s.Reserve(ctx, 8, car.ID, "user-lock-deadline")
				}
				out <- e
			}()
			require.Eventually(t, func() bool {
				var waiting bool
				e := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE 'SELECT id FROM users%')`).Scan(&waiting)
				return e == nil && waiting
			}, time.Second, 10*time.Millisecond)
			require.Eventually(t, func() bool {
				var expired bool
				e := db.QueryRow(`SELECT recruit_until<=clock_timestamp() FROM carpool_cars WHERE id=$1`, car.ID).Scan(&expired)
				return e == nil && expired
			}, time.Second, 10*time.Millisecond)
			require.NoError(t, tx.Commit())
			require.ErrorIs(t, <-out, ErrMainConflict)
		})
	}
	require.NoError(t, db.Close())
	_, err := s.ListCars(ctx, false, PageQuery{})
	require.ErrorIs(t, err, ErrMainUnavailable)
}

// Compile-time guard that both SQL connection types retain the query contract.
var _ mainQueryer = (*sql.DB)(nil)
