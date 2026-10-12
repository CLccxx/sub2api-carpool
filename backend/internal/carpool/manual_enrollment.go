package carpool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

func (s *MainService) EnrollManual(ctx context.Context, actor int64, carID, key string, input ManualEnrollmentInput) (ManualOrder, error) {
	if !s.ManualEnrollmentEnabled() {
		return ManualOrder{}, ErrMainNotFound
	}
	input.Channel = strings.TrimSpace(input.Channel)
	input.Reference = strings.TrimSpace(input.Reference)
	input.Note = strings.TrimSpace(input.Note)
	input.ReceivedAt = manualTimestamp(input.ReceivedAt)
	if input.UserID <= 0 || input.AmountMinor <= 0 || input.AmountMinor > 1_000_000_000_000 ||
		(input.Channel != "alipay" && input.Channel != "wechat") || !input.ConfirmReceived ||
		input.ReceivedAt.IsZero() || !validManualReference(input.Reference) || len(input.Note) > 1000 {
		return ManualOrder{}, ErrMainInvalid
	}
	return s.manualCommand(ctx, actor, carID, "enroll", carID, key, input, func(ctx context.Context, tx *sql.Tx, car Car) (string, error) {
		if err := activeMainUser(ctx, tx, input.UserID); err != nil {
			return "", err
		}
		car, err := refreshLockedMainCar(ctx, tx, carID)
		if err != nil {
			return "", err
		}
		if car.Status != "recruiting" || !car.RecruitUntil.After(car.ServerTime) {
			return "", ErrMainConflict
		}
		if input.AmountMinor != car.PriceMinor || input.ReceivedAt.After(car.ServerTime) {
			return "", ErrMainInvalid
		}
		var blocked bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM carpool_seats WHERE car_id=$1 AND user_id=$2 AND status<>'available')
		 OR EXISTS(SELECT 1 FROM carpool_manual_refunds r JOIN carpool_orders o ON o.id=r.order_id WHERE o.car_id=$1 AND o.user_id=$2 AND r.status='pending')`, carID, input.UserID).Scan(&blocked)
		if err != nil {
			return "", err
		}
		if blocked {
			return "", ErrMainConflict
		}
		var seat int
		err = tx.QueryRowContext(ctx, `SELECT seat_no FROM carpool_seats WHERE car_id=$1 AND status='available' ORDER BY seat_no LIMIT 1 FOR UPDATE`, carID).Scan(&seat)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrMainConflict
		}
		if err != nil {
			return "", err
		}
		snapshot, err := json.Marshal(car.CarInput)
		if err != nil {
			return "", err
		}
		digest, err := manualDigest(carID, input)
		if err != nil {
			return "", err
		}
		id := uuid.NewString()
		_, err = tx.ExecContext(ctx, `INSERT INTO carpool_orders(id,car_id,user_id,seat_no,mode,status,snapshot,idempotency_key,request_hash,confirmed_at)
		 VALUES($1,$2,$3,$4,'manual','confirmed',$5,$6,$7,$8)`, id, carID, input.UserID, seat, snapshot, "manual:"+id, digest, car.ServerTime)
		if err != nil {
			return "", err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO carpool_manual_receipts(order_id,amount_minor,currency,channel,received_at,recorded_by,reference,note)
		 VALUES($1,$2,'CNY',$3,$4,$5,NULLIF($6,''),$7)`, id, input.AmountMinor, input.Channel, input.ReceivedAt, actor, input.Reference, input.Note)
		if err != nil {
			return "", err
		}
		_, err = tx.ExecContext(ctx, `UPDATE carpool_seats SET status='confirmed',user_id=$3,order_id=$4,held_until=NULL WHERE car_id=$1 AND seat_no=$2`, carID, seat, input.UserID, id)
		if err != nil {
			return "", err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO carpool_memberships(id,car_id,user_id,seat_no,order_id,status) VALUES($1,$2,$3,$4,$5,'waiting_deployment')`, uuid.NewString(), carID, input.UserID, seat, id)
		if err != nil {
			return "", err
		}
		if err = appendMainEvent(ctx, tx, carID, "manual_enrolled", actor, id); err != nil {
			return "", err
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM carpool_seats WHERE car_id=$1 AND status='confirmed'`, carID).Scan(&count); err != nil {
			return "", err
		}
		if count == car.Capacity {
			_, err = tx.ExecContext(ctx, `UPDATE carpool_cars SET status='awaiting_deployment',full_at=$2,delivery_due=$2::timestamptz+delivery_hours*interval '1 hour' WHERE id=$1`, carID, car.ServerTime)
			if err != nil {
				return "", err
			}
			if err = appendMainEvent(ctx, tx, carID, "car_full", actor, ""); err != nil {
				return "", err
			}
		}
		return id, nil
	})
}
