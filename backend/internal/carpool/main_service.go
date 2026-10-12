package carpool

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

type MainService struct {
	db      *sql.DB
	cfg     MainConfig
	control atomic.Pointer[ControlStore]
}

func NewMainService(db *sql.DB, cfg MainConfig) *MainService { return &MainService{db: db, cfg: cfg} }
func (s *MainService) Enabled() bool                         { return s != nil && s.cfg.Enabled }
func (s *MainService) TestOrdersEnabled() bool {
	return s.Enabled() && s.cfg.TestOrdersEnabled && !s.cfg.ManualEnrollmentEnabled
}
func (s *MainService) ManualEnrollmentEnabled() bool {
	return s.Enabled() && s.cfg.ManualEnrollmentEnabled && !s.cfg.TestOrdersEnabled
}

const ManualRefundPolicy = "未开通服务前退出、招募失败或车辆取消，全额退款，由管理员在站外办理并登记"

func (s *MainService) creationMode() string {
	if s.ManualEnrollmentEnabled() {
		return "manual"
	}
	return "test"
}

func (s *MainService) modeWritable(car Car) bool {
	return (car.EnrollmentMode == "manual" && s.ManualEnrollmentEnabled()) || (car.EnrollmentMode == "test" && !s.cfg.ManualEnrollmentEnabled)
}
func (s *MainService) ready() error {
	if !s.Enabled() {
		return ErrMainNotFound
	}
	if s.db == nil {
		return ErrMainUnavailable
	}
	return nil
}

func mainDBError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrMainNotFound
	}
	for _, known := range []error{ErrMainInvalid, ErrMainNotFound, ErrMainConflict, ErrMainUnavailable, ErrControlCredential, ErrControlTarget} {
		if errors.Is(err, known) {
			return err
		}
	}
	var pgErr *pq.Error
	if errors.As(err, &pgErr) {
		if pgErr.Code == "23505" {
			return ErrMainConflict
		}
		if pgErr.Code == "23503" || pgErr.Code == "23514" {
			return ErrMainInvalid
		}
	}
	return ErrMainUnavailable.WithCause(err)
}

func (s *MainService) transaction(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
	if err := s.ready(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return mainDBError(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err = fn(ctx, tx); err != nil {
		return mainDBError(err)
	}
	return mainDBError(tx.Commit())
}

func activeMainUser(ctx context.Context, tx *sql.Tx, id int64) error {
	var found int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 AND status='active' AND deleted_at IS NULL FOR SHARE`, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrMainNotFound
	}
	return err
}

func appendMainEvent(ctx context.Context, tx *sql.Tx, carID, eventType string, actorID int64, orderID string) error {
	var version int64
	if err := tx.QueryRowContext(ctx, `UPDATE carpool_cars SET version=version+1 WHERE id=$1 RETURNING version`, carID).Scan(&version); err != nil {
		return err
	}
	var actor, order any
	if actorID > 0 {
		actor = actorID
	}
	if orderID != "" {
		order = orderID
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO carpool_events(id,car_id,version,event_type,actor_id,order_id) VALUES($1,$2,$3,$4,$5,$6)`, uuid.NewString(), carID, version, eventType, actor, order)
	return err
}

// All mutations lock the car first. Business rejections may commit expiry cleanup,
// but any database failure rolls back the entire transaction.
func (s *MainService) carTransaction(ctx context.Context, id string, fn func(context.Context, *sql.Tx, Car) error) error {
	var rejected error
	err := s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var locked string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM carpool_cars WHERE id=$1 FOR UPDATE`, id).Scan(&locked); err != nil {
			return err
		}
		car, err := refreshLockedMainCar(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `SAVEPOINT carpool_command`); err != nil {
			return err
		}
		err = fn(ctx, tx, car)
		if errors.Is(err, ErrMainConflict) || errors.Is(err, ErrMainNotFound) || errors.Is(err, ErrMainInvalid) {
			if _, rollbackErr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT carpool_command`); rollbackErr != nil {
				return rollbackErr
			}
			rejected = err
			err = nil
		}
		if err == nil {
			if control := s.control.Load(); control != nil {
				return control.publishControlCar(ctx, tx, id)
			}
		}
		return err
	})
	if err != nil {
		return err
	}
	return rejected
}

func refreshLockedMainCar(ctx context.Context, tx *sql.Tx, id string) (Car, error) {
	car, err := readMainCar(ctx, tx, id, false)
	if err != nil {
		return Car{}, err
	}
	if err = expireMainCar(ctx, tx, car); err != nil {
		return Car{}, err
	}
	return readMainCar(ctx, tx, id, false)
}

func (s *MainService) CreateCar(ctx context.Context, actor int64, input CarInput) (Car, error) {
	if s.ManualEnrollmentEnabled() {
		input.RefundPolicy = ManualRefundPolicy
	}
	id := uuid.NewString()
	err := s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if err := activeMainUser(ctx, tx, actor); err != nil {
			return err
		}
		var now time.Time
		if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return err
		}
		if err := input.normalizePrice(); err != nil {
			return err
		}
		if err := input.Validate(now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO carpool_cars(id,name,capacity,price_minor,currency,service_days,recruit_until,delivery_hours,refund_policy,created_by,enrollment_mode,total_price_minor) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, id, strings.TrimSpace(input.Name), input.Capacity, input.PriceMinor, input.Currency, input.ServiceDays, input.RecruitUntil, input.DeliveryHours, strings.TrimSpace(input.RefundPolicy), actor, s.creationMode(), input.TotalPriceMinor)
		if err != nil {
			return err
		}
		return appendMainEvent(ctx, tx, id, "car_created", actor, "")
	})
	if err != nil {
		return Car{}, err
	}
	return s.GetCar(ctx, id, true)
}

func (s *MainService) UpdateCar(ctx context.Context, actor int64, id string, input CarInput) (Car, error) {
	err := s.carTransaction(ctx, id, func(ctx context.Context, tx *sql.Tx, car Car) error {
		if car.Status != "draft" || !s.modeWritable(car) {
			return ErrMainConflict
		}
		if car.EnrollmentMode == "manual" {
			input.RefundPolicy = ManualRefundPolicy
		}
		if err := input.normalizePrice(); err != nil {
			return err
		}
		if err := input.Validate(car.ServerTime); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE carpool_cars SET name=$2,capacity=$3,price_minor=$4,currency=$5,service_days=$6,recruit_until=$7,delivery_hours=$8,refund_policy=$9,total_price_minor=$10 WHERE id=$1`, id, strings.TrimSpace(input.Name), input.Capacity, input.PriceMinor, input.Currency, input.ServiceDays, input.RecruitUntil, input.DeliveryHours, strings.TrimSpace(input.RefundPolicy), input.TotalPriceMinor)
		if err != nil {
			return err
		}
		return appendMainEvent(ctx, tx, id, "car_updated", actor, "")
	})
	if err != nil {
		return Car{}, err
	}
	return s.GetCar(ctx, id, true)
}

func (s *MainService) PublishCar(ctx context.Context, actor int64, id string) (Car, error) {
	err := s.carTransaction(ctx, id, func(ctx context.Context, tx *sql.Tx, car Car) error {
		if car.Status != "draft" || !s.modeWritable(car) {
			return ErrMainConflict
		}
		if err := car.Validate(car.ServerTime); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO carpool_seats(car_id,seat_no,capacity) SELECT $1,generate_series(1,$2::integer),$2`, id, car.Capacity); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE carpool_cars SET status='recruiting',published_at=$2 WHERE id=$1`, id, car.ServerTime); err != nil {
			return err
		}
		return appendMainEvent(ctx, tx, id, "car_published", actor, "")
	})
	if err != nil {
		return Car{}, err
	}
	return s.GetCar(ctx, id, true)
}

func (s *MainService) CancelCar(ctx context.Context, actor int64, id, reason string, confirm bool) (Car, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 2000 {
		return Car{}, ErrMainInvalid
	}
	err := s.carTransaction(ctx, id, func(ctx context.Context, tx *sql.Tx, car Car) error {
		if car.Status == "cancelled" {
			return nil
		}
		if car.Status == "closed_unfilled" {
			return ErrMainConflict
		}
		if car.ConfirmedCount > 0 && !confirm {
			return ErrMainConflict
		}
		return closeMainCar(ctx, tx, car.ID, "cancelled", reason, actor)
	})
	if err != nil {
		return Car{}, err
	}
	return s.GetCar(ctx, id, true)
}

func (s *MainService) RegisterNode(ctx context.Context, actor int64, input NodeInput) (Node, error) {
	input.Hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(input.Hostname), "."))
	if input.CarID == "" || len(input.Hostname) > 253 || len(input.Label) > 160 {
		return Node{}, ErrMainInvalid
	}
	for _, label := range strings.Split(input.Hostname, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return Node{}, ErrMainInvalid
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return Node{}, ErrMainInvalid
			}
		}
	}
	node := Node{ID: uuid.NewString(), CarID: input.CarID, Hostname: input.Hostname, Label: strings.TrimSpace(input.Label), Status: "registered"}
	err := s.carTransaction(ctx, input.CarID, func(ctx context.Context, tx *sql.Tx, car Car) error {
		if car.Status != "recruiting" && car.Status != "awaiting_deployment" {
			return ErrMainConflict
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO carpool_nodes(id,car_id,hostname,label,status) VALUES($1,$2,$3,$4,'registered') RETURNING created_at`, node.ID, node.CarID, node.Hostname, node.Label).Scan(&node.CreatedAt); err != nil {
			return err
		}
		return appendMainEvent(ctx, tx, car.ID, "node_registered", actor, "")
	})
	return node, err
}
