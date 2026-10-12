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

	"github.com/google/uuid"
)

func closeMainCar(ctx context.Context, tx *sql.Tx, id, status, reason string, actor int64) error {
	if err := requirePreService(ctx, tx, id, ""); err != nil {
		return err
	}
	if err := queueManualRefunds(ctx, tx, id, "", reason, actor); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE carpool_orders SET status=CASE WHEN $2='closed_unfilled' AND status='pending' THEN 'expired' ELSE 'cancelled' END WHERE car_id=$1 AND status IN ('pending','confirmed')`, id, status); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE carpool_memberships SET status='cancelled' WHERE car_id=$1 AND status='waiting_deployment'`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE carpool_seats SET status='available',user_id=NULL,order_id=NULL,held_until=NULL WHERE car_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE carpool_cars SET status=$2,cancel_reason=$3 WHERE id=$1`, id, status, reason); err != nil {
		return err
	}
	kind := "car_cancelled"
	if status == "closed_unfilled" {
		kind = "recruitment_closed"
	}
	return appendMainEvent(ctx, tx, id, kind, actor, "")
}

func expireMainCar(ctx context.Context, tx *sql.Tx, car Car) error {
	if car.Status != "recruiting" {
		return nil
	}
	if !car.RecruitUntil.After(car.ServerTime) {
		return closeMainCar(ctx, tx, car.ID, "closed_unfilled", "recruitment deadline reached", 0)
	}
	rows, err := tx.QueryContext(ctx, `UPDATE carpool_orders o SET status='expired' WHERE o.car_id=$1 AND o.status='pending' AND (o.expires_at<=$2 OR EXISTS(SELECT 1 FROM carpool_seats s WHERE s.order_id=o.id AND s.held_until<=$2)) RETURNING o.id`, car.ID, car.ServerTime)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, `UPDATE carpool_seats SET status='available',user_id=NULL,order_id=NULL,held_until=NULL WHERE order_id=$1 AND status='held'`, id); err != nil {
			return err
		}
		if err = appendMainEvent(ctx, tx, car.ID, "hold_expired", 0, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *MainService) Reserve(ctx context.Context, userID int64, carID, key string) (Order, error) {
	if !s.TestOrdersEnabled() {
		return Order{}, ErrMainNotFound
	}
	if userID <= 0 || strings.TrimSpace(key) == "" || len(key) > 128 || key != strings.TrimSpace(key) {
		return Order{}, ErrMainInvalid
	}
	for _, r := range key {
		if r < 33 || r > 126 {
			return Order{}, ErrMainInvalid
		}
	}
	hash := sha256.Sum256([]byte("reserve:" + carID))
	digest := hex.EncodeToString(hash[:])
	var result Order
	err := s.carTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx, car Car) error {
		if car.EnrollmentMode != "test" {
			return ErrMainConflict
		}
		if err := activeMainUser(ctx, tx, userID); err != nil {
			return err
		}
		// User-row locks can wait too; never reuse the pre-wait admission time.
		car, err := refreshLockedMainCar(ctx, tx, carID)
		if err != nil {
			return err
		}
		var existingID, existingHash string
		err = tx.QueryRowContext(ctx, `SELECT id,request_hash FROM carpool_orders WHERE user_id=$1 AND idempotency_key=$2`, userID, key).Scan(&existingID, &existingHash)
		if err == nil {
			if existingHash != digest {
				return ErrMainConflict
			}
			result, err = readMainOrder(ctx, tx, existingID)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if car.Status != "recruiting" || !car.RecruitUntil.After(car.ServerTime) {
			return ErrMainConflict
		}
		var occupied bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM carpool_seats WHERE car_id=$1 AND user_id=$2 AND status<>'available')`, carID, userID).Scan(&occupied); err != nil {
			return err
		}
		if occupied {
			return ErrMainConflict
		}
		var seat int
		if err = tx.QueryRowContext(ctx, `SELECT seat_no FROM carpool_seats WHERE car_id=$1 AND status='available' ORDER BY seat_no LIMIT 1 FOR UPDATE`, carID).Scan(&seat); errors.Is(err, sql.ErrNoRows) {
			return ErrMainConflict
		} else if err != nil {
			return err
		}
		expires := car.ServerTime.Add(10 * time.Minute)
		if expires.After(car.RecruitUntil) {
			expires = car.RecruitUntil
		}
		payload, err := json.Marshal(car.CarInput)
		if err != nil {
			return err
		}
		id := uuid.NewString()
		if _, err = tx.ExecContext(ctx, `INSERT INTO carpool_orders(id,car_id,user_id,seat_no,mode,status,snapshot,idempotency_key,request_hash,expires_at) VALUES($1,$2,$3,$4,'test','pending',$5,$6,$7,$8)`, id, carID, userID, seat, payload, key, digest, expires); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE carpool_seats SET status='held',user_id=$3,order_id=$4,held_until=$5 WHERE car_id=$1 AND seat_no=$2`, carID, seat, userID, id, expires); err != nil {
			return err
		}
		if err = appendMainEvent(ctx, tx, carID, "seat_reserved", userID, id); err != nil {
			return err
		}
		result, err = readMainOrder(ctx, tx, id)
		return err
	})
	return result, err
}

func (s *MainService) orderCarID(ctx context.Context, id string, userID int64) (string, error) {
	if err := s.ready(); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var carID string
	err := s.db.QueryRowContext(ctx, `SELECT car_id FROM carpool_orders WHERE id=$1 AND ($2::bigint=0 OR user_id=$2)`, id, userID).Scan(&carID)
	return carID, mainDBError(err)
}

func (s *MainService) CancelOrder(ctx context.Context, userID int64, id string) (Order, error) {
	if userID <= 0 {
		return Order{}, ErrMainNotFound
	}
	carID, err := s.orderCarID(ctx, id, userID)
	if err != nil {
		return Order{}, err
	}
	var result Order
	err = s.carTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx, car Car) error {
		o, err := readMainOrder(ctx, tx, id)
		if err != nil {
			return err
		}
		if o.UserID != userID {
			return ErrMainNotFound
		}
		if o.Mode != "test" || s.cfg.ManualEnrollmentEnabled {
			return ErrMainConflict
		}
		if o.Status == "cancelled" {
			result = o
			return nil
		}
		if o.Status != "pending" {
			return ErrMainConflict
		}
		if _, err = tx.ExecContext(ctx, `UPDATE carpool_orders SET status='cancelled' WHERE id=$1`, id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE carpool_seats SET status='available',user_id=NULL,order_id=NULL,held_until=NULL WHERE order_id=$1 AND status='held'`, id); err != nil {
			return err
		}
		if err = appendMainEvent(ctx, tx, car.ID, "order_cancelled", userID, id); err != nil {
			return err
		}
		result, err = readMainOrder(ctx, tx, id)
		return err
	})
	return result, err
}

func (s *MainService) ConfirmTestOrder(ctx context.Context, id string) (Order, error) {
	if !s.TestOrdersEnabled() {
		return Order{}, ErrMainNotFound
	}
	carID, err := s.orderCarID(ctx, id, 0)
	if err != nil {
		return Order{}, err
	}
	var result Order
	err = s.carTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx, car Car) error {
		o, err := readMainOrder(ctx, tx, id)
		if err != nil {
			return err
		}
		if o.Mode != "test" {
			return ErrMainConflict
		}
		if o.Status == "confirmed" {
			result = o
			return nil
		}
		if err = activeMainUser(ctx, tx, o.UserID); err != nil {
			return err
		}
		car, err = refreshLockedMainCar(ctx, tx, carID)
		if err != nil {
			return err
		}
		o, err = readMainOrder(ctx, tx, id)
		if err != nil {
			return err
		}
		if o.Status != "pending" || car.EnrollmentMode != "test" || car.Status != "recruiting" || o.ExpiresAt == nil || !o.ExpiresAt.After(car.ServerTime) {
			return ErrMainConflict
		}
		res, err := tx.ExecContext(ctx, `UPDATE carpool_seats SET status='confirmed',held_until=NULL WHERE car_id=$1 AND seat_no=$2 AND order_id=$3 AND user_id=$4 AND status='held' AND held_until>$5`, car.ID, o.SeatNo, id, o.UserID, car.ServerTime)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrMainConflict
		}
		if _, err = tx.ExecContext(ctx, `UPDATE carpool_orders SET status='confirmed',confirmed_at=$2 WHERE id=$1`, id, car.ServerTime); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO carpool_memberships(id,car_id,user_id,seat_no,order_id,status) VALUES($1,$2,$3,$4,$5,'waiting_deployment')`, uuid.NewString(), car.ID, o.UserID, o.SeatNo, id); err != nil {
			return err
		}
		if err = appendMainEvent(ctx, tx, car.ID, "order_confirmed", 0, id); err != nil {
			return err
		}
		var confirmed int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM carpool_seats WHERE car_id=$1 AND status='confirmed'`, car.ID).Scan(&confirmed); err != nil {
			return err
		}
		if confirmed == car.Capacity {
			if _, err = tx.ExecContext(ctx, `UPDATE carpool_cars SET status='awaiting_deployment',full_at=$2,delivery_due=$3 WHERE id=$1`, car.ID, car.ServerTime, car.ServerTime.Add(time.Duration(car.DeliveryHours)*time.Hour)); err != nil {
				return err
			}
			if err = appendMainEvent(ctx, tx, car.ID, "car_full", 0, ""); err != nil {
				return err
			}
		}
		result, err = readMainOrder(ctx, tx, id)
		return err
	})
	return result, err
}

// Sweep is bounded so a backlog cannot monopolize the process or shutdown.
// Multiple processes may sweep concurrently; car row locks make repeats harmless.
func (s *MainService) Sweep(ctx context.Context) (int, error) {
	if err := s.ready(); err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `SELECT c.id FROM carpool_cars c WHERE c.status='recruiting' AND (c.recruit_until<=statement_timestamp() OR EXISTS(SELECT 1 FROM carpool_orders o WHERE o.car_id=c.id AND o.status='pending' AND o.expires_at<=statement_timestamp())) ORDER BY c.recruit_until,c.id LIMIT 100`)
	if err != nil {
		return 0, mainDBError(err)
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, mainDBError(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return 0, mainDBError(err)
	}
	for index, id := range ids {
		if err = s.carTransaction(ctx, id, func(context.Context, *sql.Tx, Car) error { return nil }); err != nil {
			return index, err
		}
	}
	return len(ids), nil
}
