package service

import (
	"context"
	"database/sql"
	"errors"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/config"
)

func ProvideCarpoolManagedKeys(node *carpool.NodeControl, client *dbent.Client, api *APIKeyService, subscriptions *SubscriptionService) *CarpoolManagedKeys {
	if node == nil {
		return nil
	}
	subscriptions.SetWeeklyAnchorPolicy(node.UsesNativeWeeklyAnchor)
	node.SetSubscriptionInvalidator(func(ctx context.Context, userID, groupID int64) error {
		return errors.Join(api.invalidateAuthCacheByGroupID(ctx, groupID), subscriptions.invalidateSubscriptionCaches(userID, groupID))
	})
	return NewCarpoolManagedKeys(node, client, api)
}

func ProvideCarpoolControlStore(main *carpool.MainService, cfg *config.Config) (*carpool.ControlStore, error) {
	if !cfg.CarpoolControl.Enabled {
		return nil, nil
	}
	if err := cfg.CarpoolControl.Validate(cfg.CarpoolMain.Enabled, cfg.CarpoolNode.Enabled); err != nil {
		return nil, err
	}
	if err := cfg.CarpoolControl.ValidateSecrets(cfg.JWT.Secret, cfg.Totp.EncryptionKey); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return carpool.NewControlStore(ctx, main, cfg.CarpoolControl.EncryptionKey, cfg.CarpoolControl.AllowedOrigins)
}

func ProvideCarpoolNodeControl(db *sql.DB, cfg *config.Config) (*carpool.NodeControl, error) {
	if !cfg.CarpoolNode.Managed() {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := carpool.Migrate(ctx, db); err != nil {
		return nil, err
	}
	return carpool.NewNodeControl(ctx, db, cfg.CarpoolNode)
}
