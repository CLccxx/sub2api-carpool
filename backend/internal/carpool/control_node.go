package carpool

import (
	"context"
	"database/sql"
	"time"
)

type NodeControl struct {
	db                     *sql.DB
	cfg                    Config
	instanceID             string
	invalidateSubscription func(context.Context, int64, int64) error
}

func NewNodeControl(ctx context.Context, db *sql.DB, cfg Config) (*NodeControl, error) {
	if db == nil || !cfg.Managed() {
		return nil, ErrMainInvalid
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var legacy bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM carpool_node_memberships)`).Scan(&legacy); err != nil {
		return nil, ErrMainUnavailable
	}
	if legacy {
		return nil, ErrMainConflict
	}
	id, err := EnsureControlIdentity(ctx, db, "node")
	if err != nil {
		return nil, err
	}
	node := &NodeControl{db: db, cfg: cfg, instanceID: id}
	if err := node.bindTarget(ctx); err != nil {
		return nil, err
	}
	return node, nil
}

func (n *NodeControl) Info() ControlInfo {
	return ControlInfo{Protocol: 1, MainID: n.cfg.MainInstanceID, NodeID: n.cfg.NodeID, CarID: n.cfg.CarID, InstanceID: n.instanceID, Capabilities: []string{"connection-check", "managed-members", "versioned-grants", "delegated-keys", "bound-admin-reads", "native-subscriptions"}}
}
