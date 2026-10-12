package carpool

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type mainQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}
type mainScanner interface{ Scan(...any) error }

const mainCarSelect = `SELECT c.id,c.name,c.capacity,c.price_minor,c.currency,c.service_days,c.recruit_until,c.delivery_hours,c.refund_policy,c.status,c.version,c.created_at,c.published_at,c.full_at,c.delivery_due,c.enrollment_mode,c.total_price_minor,
 (SELECT count(*) FROM carpool_seats s WHERE s.car_id=c.id AND s.status='confirmed'),
 (SELECT count(*) FROM carpool_seats s WHERE s.car_id=c.id AND s.status='held' AND s.held_until>statement_timestamp()),clock_timestamp() FROM carpool_cars c`

func scanMainCar(row mainScanner, effective bool) (Car, error) {
	var c Car
	err := row.Scan(&c.ID, &c.Name, &c.Capacity, &c.PriceMinor, &c.Currency, &c.ServiceDays, &c.RecruitUntil, &c.DeliveryHours, &c.RefundPolicy, &c.Status, &c.Version, &c.CreatedAt, &c.PublishedAt, &c.FullAt, &c.DeliveryDue, &c.EnrollmentMode, &c.TotalPriceMinor, &c.ConfirmedCount, &c.HeldCount, &c.ServerTime)
	if err != nil {
		return c, err
	}
	if effective && c.Status == "recruiting" && !c.RecruitUntil.After(c.ServerTime) {
		c.Status = "closed_unfilled"
		c.ConfirmedCount = 0
		c.HeldCount = 0
	}
	c.AvailableCount = c.Capacity - c.ConfirmedCount - c.HeldCount
	c.Reservable = c.Status == "recruiting" && c.RecruitUntil.After(c.ServerTime) && c.AvailableCount > 0
	return c, nil
}

func readMainCar(ctx context.Context, q mainQueryer, id string, effective bool) (Car, error) {
	return scanMainCar(q.QueryRowContext(ctx, mainCarSelect+` WHERE c.id=$1`, id), effective)
}

func (s *MainService) GetCar(ctx context.Context, id string, admin bool) (Car, error) {
	if err := s.ready(); err != nil {
		return Car{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	car, err := readMainCar(ctx, s.db, id, true)
	if err != nil {
		return Car{}, mainDBError(err)
	}
	if !admin && (car.PublishedAt == nil || (s.cfg.ManualEnrollmentEnabled && car.EnrollmentMode != "manual")) {
		return Car{}, ErrMainNotFound
	}
	car.Reservable = car.Reservable && s.TestOrdersEnabled() && car.EnrollmentMode == "test"
	return car, nil
}

const effectiveCarStatus = `CASE WHEN c.status='recruiting' AND c.recruit_until<=statement_timestamp() THEN 'closed_unfilled' ELSE c.status END`

func (s *MainService) ListCars(ctx context.Context, admin bool, query PageQuery) (CarPage, error) {
	page := CarPage{Items: []Car{}}
	if err := s.ready(); err != nil {
		return page, err
	}
	query, err := query.normalized("draft", "recruiting", "awaiting_deployment", "needs_review", "closed_unfilled", "cancelled", "active", "paused", "completed")
	if err != nil {
		return page, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	page.Page = query.Page
	page.PageSize = query.PageSize
	where := ` WHERE ($1 OR c.published_at IS NOT NULL) AND ($2='' OR ` + effectiveCarStatus + `=$2)`
	if !admin && s.cfg.ManualEnrollmentEnabled {
		where += ` AND c.enrollment_mode='manual'`
	}
	if err = s.db.QueryRowContext(ctx, `SELECT count(*),statement_timestamp() FROM carpool_cars c`+where, admin, query.Status).Scan(&page.Total, &page.ServerTime); err != nil {
		return page, mainDBError(err)
	}
	rows, err := s.db.QueryContext(ctx, mainCarSelect+where+` ORDER BY c.created_at DESC,c.id LIMIT $3 OFFSET $4`, admin, query.Status, query.PageSize, (query.Page-1)*query.PageSize)
	if err != nil {
		return page, mainDBError(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		car, e := scanMainCar(rows, true)
		if e != nil {
			return page, mainDBError(e)
		}
		car.Reservable = car.Reservable && s.TestOrdersEnabled() && car.EnrollmentMode == "test"
		page.Items = append(page.Items, car)
	}
	return page, mainDBError(rows.Err())
}

const effectiveOrderStatus = `CASE WHEN o.status IN ('expired','cancelled') THEN o.status
 WHEN c.status='cancelled' THEN 'cancelled'
 WHEN c.status='closed_unfilled' OR (c.status='recruiting' AND c.recruit_until<=statement_timestamp()) THEN CASE WHEN o.status='pending' THEN 'expired' ELSE 'cancelled' END
 WHEN o.status='pending' AND o.expires_at<=statement_timestamp() THEN 'expired' ELSE o.status END`
const effectiveMemberStatus = `CASE WHEN c.status IN ('cancelled','closed_unfilled') OR (c.status='recruiting' AND c.recruit_until<=statement_timestamp()) THEN 'cancelled' ELSE m.status END`
const mainOrderSelect = `SELECT o.id,o.car_id,o.user_id,o.seat_no,o.mode,` + effectiveOrderStatus + `,o.snapshot,o.expires_at,o.confirmed_at,o.created_at,
 CASE WHEN m.id IS NULL THEN NULL ELSE to_jsonb(m)||jsonb_build_object('status',` + effectiveMemberStatus + `) END,` + manualPaymentJSON + `
 FROM carpool_orders o JOIN carpool_cars c ON c.id=o.car_id LEFT JOIN carpool_memberships m ON m.order_id=o.id`

func scanMainOrder(row mainScanner) (Order, error) {
	var o Order
	var snapshot, member, payment []byte
	err := row.Scan(&o.ID, &o.CarID, &o.UserID, &o.SeatNo, &o.Mode, &o.Status, &snapshot, &o.ExpiresAt, &o.ConfirmedAt, &o.CreatedAt, &member, &payment)
	if err != nil {
		return o, err
	}
	if err = json.Unmarshal(snapshot, &o.Snapshot); err != nil {
		return o, err
	}
	if len(member) > 0 {
		err = json.Unmarshal(member, &o.Membership)
	}
	if err == nil && len(payment) > 0 {
		err = json.Unmarshal(payment, &o.Payment)
	}
	return o, err
}

func readMainOrder(ctx context.Context, q mainQueryer, id string) (Order, error) {
	return scanMainOrder(q.QueryRowContext(ctx, mainOrderSelect+` WHERE o.id=$1`, id))
}

func (s *MainService) GetOrder(ctx context.Context, userID int64, id string) (Order, error) {
	if err := s.ready(); err != nil {
		return Order{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	o, err := scanMainOrder(s.db.QueryRowContext(ctx, mainOrderSelect+` WHERE o.id=$1 AND o.user_id=$2`, id, userID))
	if err == nil && s.cfg.ManualEnrollmentEnabled && o.Mode != "manual" {
		return Order{}, ErrMainNotFound
	}
	return o, mainDBError(err)
}

func (s *MainService) Mine(ctx context.Context, userID int64, query PageQuery) (OrderPage, error) {
	page := OrderPage{Items: []Order{}}
	if err := s.ready(); err != nil {
		return page, err
	}
	query, err := query.normalized("pending", "confirmed", "expired", "cancelled")
	if err != nil {
		return page, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	page.Page = query.Page
	page.PageSize = query.PageSize
	where := ` WHERE o.user_id=$1 AND ($2='' OR ` + effectiveOrderStatus + `=$2)`
	if s.cfg.ManualEnrollmentEnabled {
		where += ` AND o.mode='manual'`
	}
	if err = s.db.QueryRowContext(ctx, `SELECT count(*),statement_timestamp() FROM carpool_orders o JOIN carpool_cars c ON c.id=o.car_id`+where, userID, query.Status).Scan(&page.Total, &page.ServerTime); err != nil {
		return page, mainDBError(err)
	}
	rows, err := s.db.QueryContext(ctx, mainOrderSelect+where+` ORDER BY o.created_at DESC,o.id LIMIT $3 OFFSET $4`, userID, query.Status, query.PageSize, (query.Page-1)*query.PageSize)
	if err != nil {
		return page, mainDBError(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		o, e := scanMainOrder(rows)
		if e != nil {
			return page, mainDBError(e)
		}
		page.Items = append(page.Items, o)
	}
	return page, mainDBError(rows.Err())
}

func (s *MainService) ListMembers(ctx context.Context, id string, query PageQuery) (MemberPage, error) {
	page := MemberPage{Items: []Membership{}}
	if err := s.ready(); err != nil {
		return page, err
	}
	query, err := query.normalized("waiting_deployment", "cancelled", "active", "paused", "revoked", "expired")
	if err != nil {
		return page, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err = readMainCar(ctx, s.db, id, false); err != nil {
		return page, mainDBError(err)
	}
	page.Page = query.Page
	page.PageSize = query.PageSize
	from := ` FROM carpool_memberships m JOIN carpool_cars c ON c.id=m.car_id JOIN carpool_orders o ON o.id=m.order_id
	 JOIN users u ON u.id=m.user_id LEFT JOIN carpool_manual_receipts r ON r.order_id=o.id LEFT JOIN carpool_manual_refunds f ON f.order_id=o.id
	 WHERE m.car_id=$1 AND ($2='' OR ` + effectiveMemberStatus + `=$2)`
	if err = s.db.QueryRowContext(ctx, `SELECT count(*),statement_timestamp()`+from, id, query.Status).Scan(&page.Total, &page.ServerTime); err != nil {
		return page, mainDBError(err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT m.id,m.car_id,m.user_id,m.seat_no,m.order_id,`+effectiveMemberStatus+`,m.created_at,m.service_start,m.service_end,
	 COALESCE(to_jsonb(u)->>'email',''),`+manualPaymentJSON+`,
	 CASE WHEN r.order_id IS NULL THEN NULL ELSE jsonb_build_object('recorded_by',r.recorded_by,'reference',COALESCE(r.reference,''),'note',r.note) END,
	 CASE WHEN f.order_id IS NULL THEN NULL ELSE to_jsonb(f) END`+from+` ORDER BY m.created_at DESC,m.id LIMIT $3 OFFSET $4`, id, query.Status, query.PageSize, (query.Page-1)*query.PageSize)
	if err != nil {
		return page, mainDBError(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var m Membership
		var payment, receipt, refund []byte
		if err = rows.Scan(&m.ID, &m.CarID, &m.UserID, &m.SeatNo, &m.OrderID, &m.Status, &m.CreatedAt, &m.ServiceStart, &m.ServiceEnd, &m.UserEmail, &payment, &receipt, &refund); err != nil {
			return page, mainDBError(err)
		}
		if len(payment) > 0 {
			if err = json.Unmarshal(payment, &m.Payment); err != nil {
				return page, mainDBError(err)
			}
		}
		if len(receipt) > 0 {
			if err = json.Unmarshal(receipt, &m.Receipt); err != nil {
				return page, mainDBError(err)
			}
		}
		if len(refund) > 0 {
			if err = json.Unmarshal(refund, &m.RefundRecord); err != nil {
				return page, mainDBError(err)
			}
		}
		page.Items = append(page.Items, m)
	}
	return page, mainDBError(rows.Err())
}
