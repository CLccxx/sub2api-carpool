// carpool-test-order confirms a local test order, never a payment.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	_ "github.com/lib/pq"
)

type testOrderConfirmer interface {
	ConfirmTestOrder(context.Context, string) (carpool.Order, error)
}
type testOrderConnect func(context.Context, string, carpool.MainConfig) (testOrderConfirmer, func(), error)

func runTestOrder(ctx context.Context, args []string, getenv func(string) string, input io.Reader, output io.Writer, connect testOrderConnect) error {
	if len(args) != 0 {
		return errors.New("no command-line arguments are accepted; provide order_id on stdin")
	}
	if getenv("CARPOOL_MAIN_ENABLED") != "true" || getenv("CARPOOL_MAIN_TEST_ORDERS_ENABLED") != "true" {
		return errors.New("CARPOOL_MAIN_ENABLED=true and CARPOOL_MAIN_TEST_ORDERS_ENABLED=true are required")
	}
	if node := getenv("CARPOOL_NODE_ENABLED"); node != "" {
		enabled, err := strconv.ParseBool(node)
		if err != nil || enabled {
			return errors.New("CARPOOL_NODE_ENABLED must be false or unset")
		}
	}
	if manual := getenv("CARPOOL_MAIN_MANUAL_ENROLLMENT_ENABLED"); manual != "" {
		enabled, err := strconv.ParseBool(manual)
		if err != nil || enabled {
			return errors.New("CARPOOL_MAIN_MANUAL_ENROLLMENT_ENABLED must be false or unset")
		}
	}
	dsn := strings.TrimSpace(getenv("CARPOOL_DATABASE_DSN"))
	if dsn == "" {
		return errors.New("CARPOOL_DATABASE_DSN is required")
	}
	data, err := io.ReadAll(io.LimitReader(input, 65537))
	if err != nil || len(data) > 65536 {
		return errors.New("invalid or oversized input")
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return errors.New("expected one JSON object")
	}
	var request struct {
		OrderID string `json:"order_id"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&request) != nil {
		return errors.New("invalid order input")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return errors.New("expected one JSON object")
	}
	if strings.TrimSpace(request.OrderID) == "" || len(request.OrderID) > 128 {
		return errors.New("order_id is required and must be at most 128 bytes")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	main, closeDB, err := connect(ctx, dsn, carpool.MainConfig{Enabled: true, TestOrdersEnabled: true})
	if err != nil {
		return errors.New("database connection failed")
	}
	defer closeDB()
	order, err := main.ConfirmTestOrder(ctx, request.OrderID)
	if err != nil {
		return errors.New("test order confirmation failed")
	}
	if err := json.NewEncoder(output).Encode(struct {
		Status string `json:"status"`
	}{Status: order.Status}); err != nil {
		return errors.New("could not write confirmation status")
	}
	return nil
}

func connectTestOrder(ctx context.Context, dsn string, cfg carpool.MainConfig) (testOrderConfirmer, func(), error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, nil, err
	}
	closeDB := func() { _ = db.Close() }
	if err := db.PingContext(ctx); err != nil {
		closeDB()
		return nil, nil, err
	}
	return carpool.NewMainService(db, cfg), closeDB, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runTestOrder(ctx, os.Args[1:], os.Getenv, os.Stdin, os.Stdout, connectTestOrder); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
