//go:build integration

package carpool

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func mainTestDB(t *testing.T, originalOnly ...bool) *sql.DB {
	t.Helper()
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23", tcpostgres.WithDatabase("carpool_test"), tcpostgres.WithUsername("carpool"), tcpostgres.WithPassword("local-test-only"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, container.Terminate(ctx)) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(30)
	_, err = db.Exec(`CREATE TABLE users(id BIGINT PRIMARY KEY,status TEXT NOT NULL DEFAULT 'active',deleted_at TIMESTAMPTZ,balance NUMERIC NOT NULL DEFAULT 10);
INSERT INTO users(id) SELECT generate_series(1,100);`)
	require.NoError(t, err)
	if len(originalOnly) > 0 && originalOnly[0] {
		body, readErr := migrations.ReadFile("migrations/001_node_memberships.sql")
		require.NoError(t, readErr)
		_, err = db.Exec(string(body))
		require.NoError(t, err)
		_, err = db.Exec(`CREATE TABLE carpool_schema_migrations(filename TEXT PRIMARY KEY,checksum TEXT NOT NULL,applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
		require.NoError(t, err)
		sum := sha256.Sum256(body)
		_, err = db.Exec(`INSERT INTO carpool_schema_migrations(filename,checksum) VALUES('migrations/001_node_memberships.sql',$1)`, hex.EncodeToString(sum[:]))
		require.NoError(t, err)
		return db
	}
	require.NoError(t, Migrate(ctx, db))
	require.NoError(t, Migrate(ctx, db))
	return db
}

func TestMainMigrationPreservesExistingNodeGrants(t *testing.T) {
	db := mainTestDB(t, true)
	ctx := context.Background()
	gate := NewGate(db, Config{Enabled: true, NodeID: "b", CarID: "existing-car", Issuer: "issuer"})
	now := time.Now().UTC()
	grant := Grant{NodeID: "b", CarID: "existing-car", Issuer: "issuer", Subject: "member", Version: 7, Status: "active", ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour), LeaseUntil: now.Add(time.Minute)}
	require.NoError(t, gate.Apply(ctx, grant))
	var before string
	require.NoError(t, db.QueryRow(`SELECT row_to_json(m)::text FROM carpool_node_memberships m`).Scan(&before))
	require.NoError(t, Migrate(ctx, db))
	require.NoError(t, Migrate(ctx, db))
	var after string
	var count int
	require.NoError(t, db.QueryRow(`SELECT row_to_json(m)::text FROM carpool_node_memberships m`).Scan(&after))
	require.Equal(t, before, after)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_schema_migrations`).Scan(&count))
	names, err := fs.Glob(migrations, "migrations/*.sql")
	require.NoError(t, err)
	require.Equal(t, len(names), count)
	require.NoError(t, gate.CheckIdentity(ctx, "issuer", "member"))
}

func testCarInput(capacity int) CarInput {
	return CarInput{Name: "Local test car", Capacity: capacity, PriceMinor: 9900, Currency: "CNY", ServiceDays: 30, RecruitUntil: time.Now().UTC().Add(time.Hour), DeliveryHours: 24, RefundPolicy: "Synthetic cancellation; no payment"}
}

func publishedCar(t *testing.T, s *MainService, capacity int) Car {
	t.Helper()
	car, err := s.CreateCar(context.Background(), 1, testCarInput(capacity))
	require.NoError(t, err)
	car, err = s.PublishCar(context.Background(), 1, car.ID)
	require.NoError(t, err)
	return car
}

func TestMainCarpoolPostgres(t *testing.T) {
	db := mainTestDB(t)
	ctx := context.Background()
	s := NewMainService(db, MainConfig{Enabled: true, TestOrdersEnabled: true})
	t.Run("draft visibility capacities and immutable terms", func(t *testing.T) {
		for _, cap := range []int{2, 3, 4} {
			car, err := s.CreateCar(ctx, 1, testCarInput(cap))
			require.NoError(t, err)
			_, err = s.GetCar(ctx, car.ID, false)
			require.ErrorIs(t, err, ErrMainNotFound)
			input := testCarInput(cap)
			input.Name = "Edited draft"
			_, err = s.UpdateCar(ctx, 1, car.ID, input)
			require.NoError(t, err)
			car, err = s.PublishCar(ctx, 1, car.ID)
			require.NoError(t, err)
			require.Equal(t, cap, car.AvailableCount)
			var seats int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_seats WHERE car_id=$1`, car.ID).Scan(&seats))
			require.Equal(t, cap, seats)
			_, err = s.UpdateCar(ctx, 1, car.ID, input)
			require.ErrorIs(t, err, ErrMainConflict)
		}
		invalid := testCarInput(1)
		_, err := s.CreateCar(ctx, 1, invalid)
		require.ErrorIs(t, err, ErrMainInvalid)
	})
	t.Run("idempotency ownership and user per car", func(t *testing.T) {
		car := publishedCar(t, s, 2)
		order, err := s.Reserve(ctx, 2, car.ID, "same-user-key")
		require.NoError(t, err)
		replay, err := s.Reserve(ctx, 2, car.ID, "same-user-key")
		require.NoError(t, err)
		require.Equal(t, order.ID, replay.ID)
		_, err = s.Reserve(ctx, 2, car.ID, "different-key")
		require.ErrorIs(t, err, ErrMainConflict)
		other := publishedCar(t, s, 2)
		_, err = s.Reserve(ctx, 2, other.ID, "same-user-key")
		require.ErrorIs(t, err, ErrMainConflict)
		_, err = s.Reserve(ctx, 2, other.ID, "other-car-key")
		require.NoError(t, err)
		_, err = s.GetOrder(ctx, 3, order.ID)
		require.ErrorIs(t, err, ErrMainNotFound)
		_, err = s.CancelOrder(ctx, 3, order.ID)
		require.ErrorIs(t, err, ErrMainNotFound)
		cancelled, err := s.CancelOrder(ctx, 2, order.ID)
		require.NoError(t, err)
		require.Equal(t, "cancelled", cancelled.Status)
		replay, err = s.Reserve(ctx, 2, car.ID, "same-user-key")
		require.NoError(t, err)
		require.Equal(t, "cancelled", replay.Status)
		mine, err := s.Mine(ctx, 2, PageQuery{})
		require.NoError(t, err)
		require.Len(t, mine.Items, 2)
	})
	t.Run("last seat race and exactly one full event", func(t *testing.T) {
		car := publishedCar(t, s, 2)
		first, err := s.Reserve(ctx, 30, car.ID, "first-last-seat")
		require.NoError(t, err)
		_, err = s.ConfirmTestOrder(ctx, first.ID)
		require.NoError(t, err)
		type outcome struct {
			order Order
			err   error
		}
		out := make(chan outcome, 20)
		var wg sync.WaitGroup
		for user := int64(31); user < 51; user++ {
			wg.Add(1)
			go func(id int64) {
				defer wg.Done()
				o, e := s.Reserve(ctx, id, car.ID, fmt.Sprintf("last-seat-%d", id))
				out <- outcome{o, e}
			}(user)
		}
		wg.Wait()
		close(out)
		var winner Order
		var won int
		for got := range out {
			if got.err == nil {
				won++
				winner = got.order
			} else {
				require.ErrorIs(t, got.err, ErrMainConflict)
			}
		}
		require.Equal(t, 1, won)
		current, err := s.GetCar(ctx, car.ID, false)
		require.NoError(t, err)
		require.Equal(t, "recruiting", current.Status)
		require.Equal(t, 1, current.ConfirmedCount)
		require.Equal(t, 1, current.HeldCount)
		require.Zero(t, current.AvailableCount)
		_, err = s.ConfirmTestOrder(ctx, winner.ID)
		require.NoError(t, err)
		_, err = s.ConfirmTestOrder(ctx, winner.ID)
		require.NoError(t, err)
		current, err = s.GetCar(ctx, car.ID, false)
		require.NoError(t, err)
		require.Equal(t, "awaiting_deployment", current.Status)
		members, err := s.ListMembers(ctx, car.ID, PageQuery{})
		require.NoError(t, err)
		require.Len(t, members.Items, 2)
		var full, grants, started int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_events WHERE car_id=$1 AND event_type='car_full'`, car.ID).Scan(&full))
		require.Equal(t, 1, full)
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_node_memberships`).Scan(&grants))
		require.Zero(t, grants)
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_memberships WHERE service_start IS NOT NULL OR service_end IS NOT NULL`).Scan(&started))
		require.Zero(t, started)
		var balance int
		require.NoError(t, db.QueryRow(`SELECT sum(balance) FROM users`).Scan(&balance))
		require.Equal(t, 1000, balance)
		_, err = s.CancelCar(ctx, 1, car.ID, "test cancellation", false)
		require.ErrorIs(t, err, ErrMainConflict)
		_, err = s.CancelCar(ctx, 1, car.ID, "", true)
		require.ErrorIs(t, err, ErrMainInvalid)
		_, err = s.CancelCar(ctx, 1, car.ID, "test cancellation", true)
		require.NoError(t, err)
		_, err = s.ConfirmTestOrder(ctx, winner.ID)
		require.ErrorIs(t, err, ErrMainConflict)
	})
	t.Run("expired holds cannot be reclaimed by late confirmation", func(t *testing.T) {
		car := publishedCar(t, s, 2)
		old, err := s.Reserve(ctx, 60, car.ID, "old-expiry")
		require.NoError(t, err)
		_, err = db.Exec(`UPDATE carpool_orders SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, old.ID)
		require.NoError(t, err)
		_, err = db.Exec(`UPDATE carpool_seats SET held_until=clock_timestamp()-interval '1 second' WHERE order_id=$1`, old.ID)
		require.NoError(t, err)
		_, err = s.ConfirmTestOrder(ctx, old.ID)
		require.ErrorIs(t, err, ErrMainConflict)
		fresh, err := s.Reserve(ctx, 61, car.ID, "fresh-expiry")
		require.NoError(t, err)
		require.Equal(t, old.SeatNo, fresh.SeatNo)
		_, err = s.ConfirmTestOrder(ctx, old.ID)
		require.ErrorIs(t, err, ErrMainConflict)
		_, err = s.Sweep(ctx)
		require.NoError(t, err)
		restarted := NewMainService(db, MainConfig{Enabled: true, TestOrdersEnabled: true})
		_, err = restarted.Sweep(ctx)
		require.NoError(t, err)
		stored, err := s.GetOrder(ctx, 60, old.ID)
		require.NoError(t, err)
		require.Equal(t, "expired", stored.Status)
	})
	t.Run("recruitment cutoff closes partial members and pending seats", func(t *testing.T) {
		car := publishedCar(t, s, 3)
		o, err := s.Reserve(ctx, 70, car.ID, "cutoff-paid")
		require.NoError(t, err)
		_, err = s.ConfirmTestOrder(ctx, o.ID)
		require.NoError(t, err)
		_, err = s.Reserve(ctx, 71, car.ID, "cutoff-pending")
		require.NoError(t, err)
		_, err = db.Exec(`UPDATE carpool_cars SET recruit_until=clock_timestamp()-interval '1 second' WHERE id=$1`, car.ID)
		require.NoError(t, err)
		_, err = s.Sweep(ctx)
		require.NoError(t, err)
		car, err = s.GetCar(ctx, car.ID, false)
		require.NoError(t, err)
		require.Equal(t, "closed_unfilled", car.Status)
		require.Zero(t, car.ConfirmedCount)
		require.Zero(t, car.HeldCount)
		members, err := s.ListMembers(ctx, car.ID, PageQuery{})
		require.NoError(t, err)
		require.Equal(t, "cancelled", members.Items[0].Status)
	})
	t.Run("off flags and node metadata", func(t *testing.T) {
		off := NewMainService(db, MainConfig{})
		_, err := off.ListCars(ctx, false, PageQuery{})
		require.ErrorIs(t, err, ErrMainNotFound)
		car := publishedCar(t, s, 2)
		noTests := NewMainService(db, MainConfig{Enabled: true})
		_, err = noTests.Reserve(ctx, 80, car.ID, "disabled")
		require.ErrorIs(t, err, ErrMainNotFound)
		_, err = noTests.ConfirmTestOrder(ctx, "any")
		require.ErrorIs(t, err, ErrMainNotFound)
		node, err := s.RegisterNode(ctx, 1, NodeInput{CarID: car.ID, Hostname: "b.example.test", Label: "No remote request"})
		require.NoError(t, err)
		require.Equal(t, "registered", node.Status)
		_, err = s.RegisterNode(ctx, 1, NodeInput{CarID: car.ID, Hostname: "other.example.test"})
		require.ErrorIs(t, err, ErrMainConflict)
		_, err = s.RegisterNode(ctx, 1, NodeInput{CarID: car.ID, Hostname: "https://user:secret@host/path"})
		require.ErrorIs(t, err, ErrMainInvalid)
	})
}
