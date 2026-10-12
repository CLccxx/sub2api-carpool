//go:build integration

package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestTestOrderRealPostgres(t *testing.T) {
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23", tcpostgres.WithDatabase("carpool_test"), tcpostgres.WithUsername("carpool"), tcpostgres.WithPassword("local-test-only"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.Terminate(ctx)) })
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE users(id BIGINT PRIMARY KEY,status TEXT,deleted_at TIMESTAMPTZ); INSERT INTO users VALUES(1,'active',NULL),(2,'active',NULL),(3,'active',NULL)`)
	require.NoError(t, err)
	require.NoError(t, carpool.Migrate(ctx, db))
	s := carpool.NewMainService(db, carpool.MainConfig{Enabled: true, TestOrdersEnabled: true})
	car, err := s.CreateCar(ctx, 1, carpool.CarInput{Name: "CLI fixture", Capacity: 2, PriceMinor: 9900, Currency: "CNY", ServiceDays: 30, RecruitUntil: time.Now().Add(time.Hour), DeliveryHours: 24, RefundPolicy: "Test only"})
	require.NoError(t, err)
	_, err = s.PublishCar(ctx, 1, car.ID)
	require.NoError(t, err)
	o, err := s.Reserve(ctx, 2, car.ID, "cli-integration")
	require.NoError(t, err)
	env := map[string]string{"CARPOOL_MAIN_ENABLED": "true", "CARPOOL_MAIN_TEST_ORDERS_ENABLED": "true", "CARPOOL_DATABASE_DSN": dsn}
	getenv := func(k string) string { return env[k] }
	for i := 0; i < 2; i++ {
		var output bytes.Buffer
		err = runTestOrder(ctx, nil, getenv, strings.NewReader(fmt.Sprintf(`{"order_id":%q}`, o.ID)), &output, connectTestOrder)
		require.NoError(t, err)
		require.JSONEq(t, `{"status":"confirmed"}`, output.String())
	}
	var members, grants int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_memberships WHERE order_id=$1`, o.ID).Scan(&members))
	require.Equal(t, 1, members)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM carpool_node_memberships`).Scan(&grants))
	require.Zero(t, grants)
	late, err := s.Reserve(ctx, 3, car.ID, "cli-late")
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE carpool_orders SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, late.ID)
	require.NoError(t, err)
	var output bytes.Buffer
	err = runTestOrder(ctx, nil, getenv, strings.NewReader(fmt.Sprintf(`{"order_id":%q}`, late.ID)), &output, connectTestOrder)
	require.ErrorContains(t, err, "test order confirmation failed")
	require.Empty(t, output.String())
	require.NotContains(t, err.Error(), dsn)
}
