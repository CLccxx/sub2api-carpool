package carpool

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func validManualKey(key string) bool {
	if len(key) == 0 || len(key) > 128 {
		return false
	}
	for _, r := range key {
		if r < 33 || r > 126 {
			return false
		}
	}
	return true
}

func validManualReference(reference string) bool {
	return reference == "" || validManualKey(reference)
}

func manualTimestamp(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

func manualDigest(target string, payload any) (string, error) {
	data, err := json.Marshal(struct {
		Target  string
		Payload any
	}{target, payload})
	if err != nil {
		return "", ErrMainInvalid
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

func (s *MainService) manualCommand(ctx context.Context, actor int64, carID, action, target, key string, payload any, apply func(context.Context, *sql.Tx, Car) (string, error)) (ManualOrder, error) {
	if actor <= 0 || !validManualKey(key) {
		return ManualOrder{}, ErrMainInvalid
	}
	digest, err := manualDigest(target, payload)
	if err != nil {
		return ManualOrder{}, err
	}
	var result ManualOrder
	err = s.carTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx, car Car) error {
		if car.EnrollmentMode != "manual" {
			return ErrMainConflict
		}
		if err := activeMainUser(ctx, tx, actor); err != nil {
			return err
		}
		if action == "withdraw" || action == "refund" {
			if err := requirePreService(ctx, tx, carID, target); err != nil {
				return err
			}
		}
		var orderID, existingHash string
		err := tx.QueryRowContext(ctx, `SELECT order_id,request_hash FROM carpool_manual_commands WHERE actor_id=$1 AND action=$2 AND idempotency_key=$3`, actor, action, key).Scan(&orderID, &existingHash)
		if err == nil {
			if existingHash != digest {
				return ErrMainConflict
			}
			result, err = readManualOrder(ctx, tx, orderID)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		orderID, err = apply(ctx, tx, car)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO carpool_manual_commands(actor_id,action,idempotency_key,request_hash,order_id) VALUES($1,$2,$3,$4,$5)`, actor, action, key, digest, orderID); err != nil {
			return err
		}
		result, err = readManualOrder(ctx, tx, orderID)
		return err
	})
	return result, err
}

func queueManualRefunds(ctx context.Context, tx *sql.Tx, carID, orderID, reason string, actor int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO carpool_manual_refunds(order_id,amount_minor,reason,requested_by)
	 SELECT r.order_id,r.amount_minor,$3,NULLIF($4::bigint,0)
	 FROM carpool_manual_receipts r JOIN carpool_orders o ON o.id=r.order_id
	 WHERE o.car_id=$1 AND ($2='' OR o.id=$2)
	 AND NOT EXISTS(SELECT 1 FROM carpool_memberships m WHERE m.order_id=o.id AND m.service_start IS NOT NULL)
	 ON CONFLICT(order_id) DO NOTHING`, carID, orderID, strings.TrimSpace(reason), actor)
	return err
}
