package carpool

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
)

type controlAuthority struct {
	membership, subject, memberStatus, orderStatus, userStatus string
	userID                                                     int64
	deleted                                                    bool
	start, end                                                 *time.Time
}

func (a controlAuthority) grant(carStatus string, carStart, carEnd *time.Time, now time.Time) ManagedGrant {
	g := ManagedGrant{Status: "revoked"}
	if a.userStatus != "active" || a.deleted || a.orderStatus != "confirmed" {
		return g
	}
	if a.memberStatus == "waiting_deployment" && a.start == nil && a.end == nil && carStart == nil && carEnd == nil &&
		(carStatus == "recruiting" || carStatus == "awaiting_deployment" || carStatus == "needs_review") {
		g.Status = "prepared"
		return g
	}
	if a.start == nil || a.end == nil || carStart == nil || carEnd == nil || !a.end.After(now) || !carEnd.After(now) ||
		a.start.Before(*carStart) || a.end.After(*carEnd) || a.start.After(now) || carStart.After(now) {
		return g
	}
	if (a.memberStatus == "paused" || a.memberStatus == "active") && (carStatus == "paused" || carStatus == "active") {
		g.Status = "paused"
		if a.memberStatus == "active" && carStatus == "active" {
			lease := now.Add(15 * time.Minute)
			if lease.After(*a.end) {
				lease = *a.end
			}
			g.Status, g.ValidFrom, g.ValidUntil, g.LeaseUntil = "active", a.start, a.end, &lease
		}
	}
	return g.canonical()
}

func controlGrantHash(g ManagedGrant) string {
	encoded, _ := json.Marshal(g.canonical())
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func sameControlTime(a, b *time.Time) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && a.Equal(*b))
}

// Called with the car locked, before committing business state. No network I/O.
func (s *ControlStore) publishControlCar(ctx context.Context, tx *sql.Tx, carID string) error {
	var node string
	err := tx.QueryRowContext(ctx, `SELECT id FROM carpool_nodes WHERE car_id=$1`, carID).Scan(&node)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	conn, err := readControlConnection(ctx, tx, node)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !conn.pinned {
		return nil
	}
	var carStatus string
	var native bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM carpool_native_preparations WHERE node_id=$1 AND group_id IS NOT NULL)`, node).Scan(&native); err != nil {
		return err
	}
	var carStart, carEnd *time.Time
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT status,service_start,service_end,clock_timestamp() FROM carpool_cars WHERE id=$1`, carID).Scan(&carStatus, &carStart, &carEnd, &now); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT ON(m.user_id) m.id,m.user_id,m.status,m.service_start,m.service_end,o.status,u.status,u.deleted_at IS NOT NULL,COALESCE(s.subject_id::text,'')
 FROM carpool_memberships m JOIN carpool_orders o ON o.id=m.order_id JOIN users u ON u.id=m.user_id
 LEFT JOIN carpool_main_subjects s ON s.user_id=u.id WHERE m.car_id=$1 ORDER BY m.user_id,m.created_at DESC,m.id DESC`, carID)
	if err != nil {
		return err
	}
	authorities := []controlAuthority{}
	for rows.Next() {
		var a controlAuthority
		if err = rows.Scan(&a.membership, &a.userID, &a.memberStatus, &a.start, &a.end, &a.orderStatus, &a.userStatus, &a.deleted, &a.subject); err != nil {
			break
		}
		authorities = append(authorities, a)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, a := range authorities {
		grant := a.grant(carStatus, carStart, carEnd, now)
		grant.NativeSubscription = native
		grant = grant.canonical()
		if a.subject == "" {
			if grant.Status == "revoked" {
				continue
			}
			a.subject, err = ensureMainSubject(ctx, tx, a.userID)
			if err != nil {
				return err
			}
		}
		if grant.Status == "revoked" {
			var exists bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM carpool_main_node_members WHERE node_id=$1 AND subject_id=$2)`, node, a.subject).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				continue
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO carpool_main_node_members(node_id,subject_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, node, a.subject); err != nil {
			return err
		}
		if err = publishControlGrant(ctx, tx, node, conn.generation, a.membership, a.subject, grant, now); err != nil {
			return err
		}
	}
	return nil
}

func publishControlGrant(ctx context.Context, tx *sql.Tx, node string, generation int64, membership, subject string, g ManagedGrant, now time.Time) error {
	var old ManagedGrant
	var oldGeneration int64
	var oldMember, delivery, errorCode string
	err := tx.QueryRowContext(ctx, `SELECT generation,membership_id,version,status,valid_from,valid_until,lease_until,delivery,last_error_code,native_subscription
 FROM carpool_control_grants WHERE node_id=$1 AND subject_id=$2 FOR UPDATE`, node, subject).Scan(&oldGeneration, &oldMember, &old.Version, &old.Status, &old.ValidFrom, &old.ValidUntil, &old.LeaseUntil, &delivery, &errorCode, &old.NativeSubscription)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	changed := old.NativeSubscription != g.NativeSubscription || old.Status != g.Status || oldMember != membership || oldGeneration != generation || !sameControlTime(old.ValidFrom, g.ValidFrom) || !sameControlTime(old.ValidUntil, g.ValidUntil)
	// Renew five minutes before expiry, without churning pending commands on every reconciliation.
	renew := !g.NativeSubscription && g.Status == "active" && old.LeaseUntil != nil && (delivery == "synced" && !old.LeaseUntil.After(now.Add(10*time.Minute)) || delivery != "error" && !old.LeaseUntil.After(now))
	if !changed && !renew {
		return nil
	}
	if old.Version == math.MaxInt64 {
		return ErrMainConflict
	}
	g.Version = old.Version + 1
	if err = g.validate(now); err != nil {
		return err
	}
	nextDelivery := "pending"
	if delivery == "error" && oldGeneration == generation {
		nextDelivery = "error"
	} else {
		errorCode = ""
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO carpool_control_grants(node_id,subject_id,membership_id,generation,version,operation_id,status,valid_from,valid_until,lease_until,payload_hash,delivery,last_error_code,available_at,updated_at,native_subscription)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$14,$15)
 ON CONFLICT(node_id,subject_id) DO UPDATE SET membership_id=EXCLUDED.membership_id,generation=EXCLUDED.generation,version=EXCLUDED.version,
 operation_id=EXCLUDED.operation_id,status=EXCLUDED.status,valid_from=EXCLUDED.valid_from,valid_until=EXCLUDED.valid_until,lease_until=EXCLUDED.lease_until,
 payload_hash=EXCLUDED.payload_hash,delivery=EXCLUDED.delivery,last_error_code=EXCLUDED.last_error_code,available_at=EXCLUDED.available_at,updated_at=EXCLUDED.updated_at,native_subscription=EXCLUDED.native_subscription,
 attempts=0,claim_id=NULL,claim_until=NULL`, node, subject, membership, generation, g.Version, uuid.NewString(), g.Status, g.ValidFrom, g.ValidUntil, g.LeaseUntil, controlGrantHash(g), nextDelivery, errorCode, now, g.NativeSubscription)
	return err
}

// ReconcileControl also discovers user disable/delete outside carpool transactions.
func (s *ControlStore) ReconcileControl(ctx context.Context) error {
	last := ""
	var result error
	for {
		rows, err := s.main.db.QueryContext(ctx, `SELECT n.car_id FROM carpool_nodes n JOIN carpool_node_connections c ON c.node_id=n.id WHERE n.car_id>$1 AND c.identity_verified ORDER BY n.car_id LIMIT 100`, last)
		if err != nil {
			return ErrMainUnavailable
		}
		cars := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				break
			}
			cars = append(cars, id)
		}
		if err == nil {
			err = rows.Err()
		}
		_ = rows.Close()
		if err != nil {
			return ErrMainUnavailable
		}
		for _, id := range cars {
			err = s.controlCarTransaction(ctx, id, func(ctx context.Context, tx *sql.Tx) error { return s.publishControlCar(ctx, tx, id) })
			if err != nil {
				result = controlStoreError(err)
			}
			if ctx.Err() != nil {
				return ErrMainUnavailable
			}
			last = id
		}
		if len(cars) < 100 {
			return result
		}
	}
}

func (s *ControlStore) controlCarTransaction(ctx context.Context, carID string, fn func(context.Context, *sql.Tx) error) error {
	return s.main.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var id string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM carpool_cars WHERE id=$1 FOR UPDATE`, carID).Scan(&id); err != nil {
			return err
		}
		return fn(ctx, tx)
	})
}

func (s *ControlStore) RetryControlSync(ctx context.Context, actor int64, carID string) error {
	return controlStoreError(s.controlCarTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx) error {
		if err := activeMainUser(ctx, tx, actor); err != nil {
			return err
		}
		if err := s.publishControlCar(ctx, tx, carID); err != nil {
			return err
		}
		updated, err := tx.ExecContext(ctx, `UPDATE carpool_control_grants g SET delivery='pending',attempts=0,last_error_code='',available_at=clock_timestamp(),updated_at=clock_timestamp()
 FROM carpool_nodes n JOIN carpool_node_connections c ON c.node_id=n.id WHERE g.node_id=n.id AND n.car_id=$1 AND c.status='ready' AND g.generation=c.generation AND g.delivery IN ('pending','error')`, carID)
		if err != nil {
			return err
		}
		count, err := updated.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			return ErrMainConflict
		}
		return appendMainEvent(ctx, tx, carID, "node_sync_retried", actor, "")
	}))
}
