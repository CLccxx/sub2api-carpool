package service

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/config"
)

func ProvideNodeMembershipGate(db *sql.DB, cfg *config.Config) (*carpool.Gate, error) {
	if !cfg.CarpoolNode.Enabled {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := carpool.Migrate(ctx, db); err != nil {
		return nil, err
	}
	return carpool.NewGate(db, cfg.CarpoolNode), nil
}
