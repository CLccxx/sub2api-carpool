// carpool-membership applies a local operator fixture; it does not serve HTTP.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	_ "github.com/lib/pq"
)

func run() error {
	cfg := carpool.Config{Enabled: true, NodeID: os.Getenv("CARPOOL_NODE_ID"), CarID: os.Getenv("CARPOOL_CAR_ID"), Issuer: os.Getenv("CARPOOL_ISSUER")}
	if err := cfg.Validate(); err != nil {
		return err
	}
	dsn := os.Getenv("CARPOOL_DATABASE_DSN")
	if dsn == "" {
		return fmt.Errorf("CARPOOL_DATABASE_DSN is required")
	}
	dec := json.NewDecoder(io.LimitReader(os.Stdin, 65537))
	dec.DisallowUnknownFields()
	var grant carpool.Grant
	if err := dec.Decode(&grant); err != nil {
		return fmt.Errorf("invalid grant JSON: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected one grant JSON object")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err = carpool.Migrate(ctx, db); err != nil {
		return err
	}
	if err = carpool.NewGate(db, cfg).Apply(ctx, grant); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"applied": true, "version": grant.Version})
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
