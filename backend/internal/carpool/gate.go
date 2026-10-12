// Package carpool implements the opt-in, node-local membership boundary.
package carpool

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	apperrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

var (
	ErrRequired        = apperrors.Forbidden("NODE_MEMBERSHIP_REQUIRED", "active node membership required")
	ErrUnavailable     = apperrors.ServiceUnavailable("NODE_MEMBERSHIP_UNAVAILABLE", "node membership verification unavailable")
	ErrVersionConflict = apperrors.Conflict("NODE_MEMBERSHIP_VERSION_CONFLICT", "stale or conflicting membership version")
)

type Config struct {
	Enabled           bool   `mapstructure:"enabled"`
	NodeID            string `mapstructure:"node_id"`
	CarID             string `mapstructure:"car_id"`
	Issuer            string `mapstructure:"issuer"`
	AuthorizationMode string `mapstructure:"authorization_mode"`
	MainInstanceID    string `mapstructure:"main_instance_id"`
	GroupID           int64  `mapstructure:"group_id"`
}

func (c Config) Managed() bool { return c.Enabled && c.AuthorizationMode == "managed" }

func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.AuthorizationMode != "" && c.AuthorizationMode != "oidc" && c.AuthorizationMode != "managed" {
		return fmt.Errorf("carpool_node authorization_mode must be oidc or managed")
	}
	if c.Managed() {
		for _, value := range []string{c.MainInstanceID, c.NodeID, c.CarID} {
			id, err := uuid.Parse(value)
			if err != nil || id == uuid.Nil || id.String() != value {
				return fmt.Errorf("managed carpool_node requires canonical main_instance_id, node_id and car_id UUIDs")
			}
		}
		if c.Issuer != "" || c.GroupID < 0 {
			return fmt.Errorf("managed carpool_node requires a nonnegative group_id and no OIDC issuer")
		}
		return nil
	}
	if c.MainInstanceID != "" || c.GroupID != 0 {
		return fmt.Errorf("OIDC carpool_node cannot use managed identity or group configuration")
	}
	if strings.TrimSpace(c.NodeID) == "" || strings.TrimSpace(c.CarID) == "" || strings.TrimSpace(c.Issuer) == "" {
		return fmt.Errorf("carpool_node requires node_id, car_id and issuer")
	}
	return nil
}

type Grant struct {
	NodeID     string    `json:"node_id"`
	CarID      string    `json:"car_id"`
	Issuer     string    `json:"issuer"`
	Subject    string    `json:"subject"`
	Version    int64     `json:"version"`
	Status     string    `json:"status"`
	ValidFrom  time.Time `json:"valid_from"`
	ValidUntil time.Time `json:"valid_until"`
	LeaseUntil time.Time `json:"lease_until"`
}

func (g Grant) Allows(c Config, now time.Time) bool {
	return g.NodeID == c.NodeID && g.CarID == c.CarID && g.Issuer == c.Issuer && g.Subject != "" && g.Version > 0 && g.Status == "active" && !now.Before(g.ValidFrom) && now.Before(g.ValidUntil) && now.Before(g.LeaseUntil)
}

type Gate struct {
	db  *sql.DB
	cfg Config
}

func NewGate(db *sql.DB, cfg Config) *Gate { return &Gate{db: db, cfg: cfg} }

// No allow-cache: a stale API-key snapshot cannot outlive membership revocation.
func (g *Gate) CheckUser(ctx context.Context, userID int64) error {
	if g == nil || g.db == nil || !g.cfg.Enabled {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if g.cfg.Managed() {
		return g.checkManagedUser(ctx, userID)
	}
	var allowed bool
	err := g.db.QueryRowContext(ctx, `SELECT EXISTS (
 SELECT 1 FROM carpool_node_memberships m
 JOIN auth_identities a ON a.provider_type='oidc' AND a.provider_key=m.issuer AND a.provider_subject=m.subject
 JOIN users u ON u.id=a.user_id
 WHERE m.node_id=$1 AND m.car_id=$2 AND m.issuer=$3 AND a.user_id=$4
 AND u.status='active' AND u.deleted_at IS NULL
 AND m.status='active' AND m.valid_from<=CURRENT_TIMESTAMP
 AND m.valid_until>CURRENT_TIMESTAMP AND m.lease_until>CURRENT_TIMESTAMP
 )`, g.cfg.NodeID, g.cfg.CarID, g.cfg.Issuer, userID).Scan(&allowed)
	if err != nil {
		return ErrUnavailable.WithCause(err)
	}
	if !allowed {
		return ErrRequired
	}
	return nil
}

// CheckIdentity runs after OIDC verification, before local registration or grants.
func (g *Gate) CheckIdentity(ctx context.Context, issuer, subject string) error {
	if g == nil || g.db == nil || !g.cfg.Enabled {
		return ErrUnavailable
	}
	if g.cfg.Managed() || issuer != g.cfg.Issuer || strings.TrimSpace(subject) == "" {
		return ErrRequired
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var allowed bool
	err := g.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM carpool_node_memberships
 WHERE node_id=$1 AND car_id=$2 AND issuer=$3 AND subject=$4 AND status='active'
 AND valid_from<=CURRENT_TIMESTAMP AND valid_until>CURRENT_TIMESTAMP AND lease_until>CURRENT_TIMESTAMP)`, g.cfg.NodeID, g.cfg.CarID, issuer, subject).Scan(&allowed)
	if err != nil {
		return ErrUnavailable.WithCause(err)
	}
	if !allowed {
		return ErrRequired
	}
	return nil
}

// Apply is an operator/test fixture, not a remotely exposed synchronization API.
func (g *Gate) Apply(ctx context.Context, m Grant) error {
	if g == nil || g.db == nil || !g.cfg.Enabled {
		return ErrUnavailable
	}
	if err := g.cfg.Validate(); err != nil {
		return err
	}
	if g.cfg.Managed() {
		return ErrRequired
	}
	if m.NodeID != g.cfg.NodeID || m.CarID != g.cfg.CarID || m.Issuer != g.cfg.Issuer || strings.TrimSpace(m.Subject) == "" || m.Version <= 0 {
		return fmt.Errorf("invalid membership target or version")
	}
	if m.Status != "active" && m.Status != "revoked" && m.Status != "paused" {
		return fmt.Errorf("invalid membership status")
	}
	if m.ValidFrom.IsZero() || !m.ValidUntil.After(m.ValidFrom) || m.LeaseUntil.IsZero() || m.LeaseUntil.After(m.ValidUntil) || m.LeaseUntil.After(time.Now().Add(15*time.Minute)) {
		return fmt.Errorf("invalid membership interval or lease exceeds 15 minutes")
	}
	m.ValidFrom = m.ValidFrom.UTC()
	m.ValidUntil = m.ValidUntil.UTC()
	m.LeaseUntil = m.LeaseUntil.UTC()
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(payload)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := g.db.ExecContext(ctx, `INSERT INTO carpool_node_memberships
 (node_id,car_id,issuer,subject,version,status,valid_from,valid_until,lease_until,payload_hash)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
 ON CONFLICT (node_id,car_id,issuer,subject) DO UPDATE SET
 version=EXCLUDED.version,status=EXCLUDED.status,valid_from=EXCLUDED.valid_from,
 valid_until=EXCLUDED.valid_until,lease_until=EXCLUDED.lease_until,payload_hash=EXCLUDED.payload_hash,updated_at=CURRENT_TIMESTAMP
 WHERE carpool_node_memberships.version<EXCLUDED.version OR
 (carpool_node_memberships.version=EXCLUDED.version AND carpool_node_memberships.payload_hash=EXCLUDED.payload_hash)`, m.NodeID, m.CarID, m.Issuer, m.Subject, m.Version, m.Status, m.ValidFrom, m.ValidUntil, m.LeaseUntil, hex.EncodeToString(sum[:]))
	if err != nil {
		return ErrUnavailable.WithCause(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrVersionConflict
	}
	return nil
}
