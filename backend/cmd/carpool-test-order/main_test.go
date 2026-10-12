package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/stretchr/testify/require"
)

func TestTestOrderRejectsBeforeConnect(t *testing.T) {
	for _, tt := range []struct {
		name, main, tests, node, manual, dsn, body string
		args                                       []string
	}{
		{name: "off", tests: "true", dsn: "secret", body: `{"order_id":"o"}`},
		{name: "tests off", main: "true", dsn: "secret", body: `{"order_id":"o"}`},
		{name: "explicit true", main: "1", tests: "true", dsn: "secret", body: `{"order_id":"o"}`},
		{name: "node", main: "true", tests: "true", node: "true", dsn: "secret", body: `{"order_id":"o"}`},
		{name: "invalid node", main: "true", tests: "true", node: "invalid", dsn: "secret", body: `{"order_id":"o"}`},
		{name: "manual", main: "true", tests: "true", manual: "true", dsn: "secret", body: `{"order_id":"o"}`},
		{name: "invalid manual", main: "true", tests: "true", manual: "invalid", dsn: "secret", body: `{"order_id":"o"}`},
		{name: "missing dsn", main: "true", tests: "true", body: `{"order_id":"o"}`},
		{name: "flags", main: "true", tests: "true", dsn: "secret", body: `{"order_id":"o"}`, args: []string{"--confirm"}},
		{name: "unknown", main: "true", tests: "true", dsn: "secret", body: `{"order_id":"o","user_id":2}`},
		{name: "multiple", main: "true", tests: "true", dsn: "secret", body: `{"order_id":"o"}{}`},
		{name: "null", main: "true", tests: "true", dsn: "secret", body: `null`},
		{name: "empty id", main: "true", tests: "true", dsn: "secret", body: `{"order_id":" "}`},
		{name: "oversized", main: "true", tests: "true", dsn: "secret", body: `{"order_id":"o"}` + strings.Repeat(" ", 65537)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := map[string]string{"CARPOOL_MAIN_ENABLED": tt.main, "CARPOOL_MAIN_TEST_ORDERS_ENABLED": tt.tests, "CARPOOL_NODE_ENABLED": tt.node, "CARPOOL_MAIN_MANUAL_ENROLLMENT_ENABLED": tt.manual, "CARPOOL_DATABASE_DSN": tt.dsn}
			connect := func(context.Context, string, carpool.MainConfig) (testOrderConfirmer, func(), error) {
				t.Fatal("connected before validation")
				return nil, nil, nil
			}
			err := runTestOrder(context.Background(), tt.args, func(k string) string { return env[k] }, strings.NewReader(tt.body), &bytes.Buffer{}, connect)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret")
		})
	}
}

type testOrderFake struct {
	id  string
	err error
}

func (f *testOrderFake) ConfirmTestOrder(_ context.Context, id string) (carpool.Order, error) {
	f.id = id
	return carpool.Order{ID: id, Status: "paid"}, f.err
}

func TestTestOrderSanitizedResult(t *testing.T) {
	for _, fail := range []bool{false, true} {
		f := &testOrderFake{}
		if fail {
			f.err = errors.New("password=secret SQL statement")
		}
		closed := false
		connect := func(_ context.Context, dsn string, cfg carpool.MainConfig) (testOrderConfirmer, func(), error) {
			require.Equal(t, "secret", dsn)
			require.True(t, cfg.Enabled && cfg.TestOrdersEnabled)
			return f, func() { closed = true }, nil
		}
		env := func(k string) string {
			if k == "CARPOOL_DATABASE_DSN" {
				return "secret"
			}
			if k == "CARPOOL_NODE_ENABLED" || k == "CARPOOL_MAIN_MANUAL_ENROLLMENT_ENABLED" {
				return "false"
			}
			return "true"
		}
		var out bytes.Buffer
		err := runTestOrder(context.Background(), nil, env, strings.NewReader(`{"order_id":"o"}`), &out, connect)
		require.Equal(t, "o", f.id)
		require.True(t, closed)
		if fail {
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret")
			require.Empty(t, out.String())
		} else {
			require.NoError(t, err)
			require.JSONEq(t, `{"status":"paid"}`, out.String())
		}
	}
}
