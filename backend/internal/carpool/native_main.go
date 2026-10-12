package carpool

import (
	"context"
	"database/sql"
	"time"
)

type NativeCarStatus struct {
	AccountID    int64      `json:"account_id"`
	GroupID      int64      `json:"group_id"`
	Capacity     int        `json:"capacity"`
	PreparedAt   *time.Time `json:"prepared_at"`
	ServiceStart *time.Time `json:"service_start"`
	ServiceEnd   *time.Time `json:"service_end"`
	CanActivate  bool       `json:"can_activate"`
	CanPrepare   bool       `json:"can_prepare"`
}

func (s *ControlStore) NativeCarStatus(ctx context.Context, carID string) (NativeCarStatus, error) {
	var result NativeCarStatus
	err := s.main.db.QueryRowContext(ctx, `SELECT COALESCE(p.account_id,0),COALESCE(p.group_id,0),c.capacity,p.prepared_at,c.service_start,c.service_end,
 c.status='awaiting_deployment' AND p.group_id IS NOT NULL AND cn.status='ready'
 AND (SELECT count(*) FROM carpool_control_grants g JOIN carpool_main_node_members nm ON nm.node_id=g.node_id AND nm.subject_id=g.subject_id
 JOIN carpool_memberships m ON m.id=g.membership_id JOIN users u ON u.id=m.user_id JOIN carpool_orders o ON o.id=m.order_id
 WHERE g.node_id=n.id AND g.native_subscription AND g.status='prepared' AND g.delivery='synced' AND g.generation=cn.generation
 AND nm.remote_group_id=p.group_id AND nm.remote_user_id>0 AND m.status='waiting_deployment' AND o.status='confirmed' AND u.status='active' AND u.deleted_at IS NULL)=c.capacity,
 COALESCE(c.status='awaiting_deployment' AND c.service_start IS NULL AND cn.status='ready'
 AND (SELECT count(*) FROM carpool_seats seats WHERE seats.car_id=c.id AND seats.status='confirmed')=c.capacity,FALSE)
 FROM carpool_cars c JOIN carpool_nodes n ON n.car_id=c.id LEFT JOIN carpool_node_connections cn ON cn.node_id=n.id LEFT JOIN carpool_native_preparations p ON p.node_id=n.id WHERE c.id=$1`, carID).Scan(&result.AccountID, &result.GroupID, &result.Capacity, &result.PreparedAt, &result.ServiceStart, &result.ServiceEnd, &result.CanActivate, &result.CanPrepare)
	return result, controlStoreError(err)
}

func (s *ControlStore) nativeConnection(ctx context.Context, actor int64, carID string) (controlMemberSnapshot, error) {
	var snap controlMemberSnapshot
	err := s.main.carTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx, _ Car) error {
		if err := activeMainUser(ctx, tx, actor); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT id FROM carpool_nodes WHERE car_id=$1`, carID).Scan(&snap.target.NodeID); err != nil {
			return err
		}
		var err error
		snap.connection, err = readControlConnection(ctx, tx, snap.target.NodeID)
		if err != nil {
			return err
		}
		if snap.connection.status != "ready" || !snap.connection.pinned {
			return ErrControlTransport
		}
		snap.target.MainID, snap.target.CarID, snap.target.InstanceID = s.mainID, carID, snap.connection.instance
		return nil
	})
	return snap, controlStoreError(err)
}

func (s *ControlStore) NativeAccounts(ctx context.Context, actor int64, carID string) ([]NativeAccount, error) {
	snap, err := s.nativeConnection(ctx, actor, carID)
	if err != nil {
		return nil, err
	}
	key, err := s.memberCredential(snap)
	if err != nil {
		return nil, err
	}
	return s.client.SubscriptionAccounts(ctx, snap.connection.origin, key, snap.target)
}

func (s *ControlStore) PrepareNativeCar(ctx context.Context, actor int64, carID string, accountID int64) (NativeCarStatus, error) {
	if accountID <= 0 {
		return NativeCarStatus{}, ErrMainInvalid
	}
	snap, err := s.nativeConnection(ctx, actor, carID)
	if err != nil {
		return NativeCarStatus{}, err
	}
	input := NativeSetupInput{AccountID: accountID}
	err = s.main.carTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx, car Car) error {
		if err := activeMainUser(ctx, tx, actor); err != nil {
			return err
		}
		if car.Status != "awaiting_deployment" || car.ConfirmedCount != car.Capacity {
			return ErrMainConflict
		}
		if err := requirePreService(ctx, tx, carID, ""); err != nil {
			return err
		}
		input.Capacity = car.Capacity
		_, err := tx.ExecContext(ctx, `INSERT INTO carpool_native_preparations(node_id,account_id,capacity) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, snap.target.NodeID, input.AccountID, input.Capacity)
		if err != nil {
			return err
		}
		var saved NativeSetupInput
		if err = tx.QueryRowContext(ctx, `SELECT account_id,capacity FROM carpool_native_preparations WHERE node_id=$1 FOR UPDATE`, snap.target.NodeID).Scan(&saved.AccountID, &saved.Capacity); err != nil {
			return err
		}
		if saved != input {
			return ErrMainConflict
		}
		return nil
	})
	if err != nil {
		return NativeCarStatus{}, controlStoreError(err)
	}
	key, err := s.memberCredential(snap)
	if err != nil {
		return NativeCarStatus{}, err
	}
	setup, err := s.client.InitializeSubscription(ctx, snap.connection.origin, key, snap.target, input)
	if err != nil {
		return NativeCarStatus{}, err
	}
	err = s.main.carTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx, _ Car) error {
		current, err := readControlConnection(ctx, tx, snap.target.NodeID)
		if err != nil {
			return err
		}
		if current.generation != snap.connection.generation || current.status != "ready" || current.instance != snap.target.InstanceID {
			return ErrMainConflict
		}
		_, err = tx.ExecContext(ctx, `UPDATE carpool_native_preparations SET group_id=$2,prepared_at=COALESCE(prepared_at,clock_timestamp()) WHERE node_id=$1 AND (group_id IS NULL OR group_id=$2)`, snap.target.NodeID, setup.GroupID)
		if err != nil {
			return err
		}
		if err = s.publishControlCar(ctx, tx, carID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE carpool_control_grants SET delivery='pending',attempts=0,last_error_code='',available_at=clock_timestamp() WHERE node_id=$1 AND delivery='error'`, snap.target.NodeID)
		return err
	})
	if err != nil {
		return NativeCarStatus{}, controlStoreError(err)
	}
	return s.NativeCarStatus(ctx, carID)
}

func (s *ControlStore) ActivateNativeCar(ctx context.Context, actor int64, carID string, confirm bool) (NativeCarStatus, error) {
	if !confirm {
		return NativeCarStatus{}, ErrMainInvalid
	}
	err := s.main.carTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx, car Car) error {
		if err := activeMainUser(ctx, tx, actor); err != nil {
			return err
		}
		var start, end *time.Time
		if err := tx.QueryRowContext(ctx, `SELECT service_start,service_end FROM carpool_cars WHERE id=$1`, carID).Scan(&start, &end); err != nil {
			return err
		}
		if start != nil {
			return nil
		}
		if car.Status != "awaiting_deployment" || car.ConfirmedCount != car.Capacity {
			return ErrMainConflict
		}
		// Publish authority changes before checking prepared acknowledgements.
		if err := s.publishControlCar(ctx, tx, carID); err != nil {
			return err
		}
		var ready int
		err := tx.QueryRowContext(ctx, `SELECT count(*) FROM carpool_memberships m JOIN carpool_orders o ON o.id=m.order_id JOIN users u ON u.id=m.user_id
 JOIN carpool_main_subjects s ON s.user_id=m.user_id JOIN carpool_nodes n ON n.car_id=m.car_id JOIN carpool_node_connections c ON c.node_id=n.id
 JOIN carpool_native_preparations p ON p.node_id=n.id JOIN carpool_main_node_members nm ON nm.node_id=n.id AND nm.subject_id=s.subject_id
 JOIN carpool_control_grants g ON g.node_id=n.id AND g.subject_id=s.subject_id
 WHERE m.car_id=$1 AND m.status='waiting_deployment' AND o.status='confirmed' AND u.status='active' AND u.deleted_at IS NULL
 AND c.status='ready' AND p.group_id=nm.remote_group_id AND nm.remote_user_id>0 AND nm.provisioned_instance_id=c.expected_instance_id
 AND g.native_subscription AND g.membership_id=m.id AND g.status='prepared' AND g.delivery='synced' AND g.version=g.ack_version AND g.generation=c.generation AND g.ack_generation=c.generation`, carID).Scan(&ready)
		if err != nil {
			return err
		}
		if ready != car.Capacity {
			return ErrControlPending
		}
		if _, err = tx.ExecContext(ctx, `UPDATE carpool_cars SET status='active',service_start=statement_timestamp(),service_end=statement_timestamp()+service_days*interval '1 day',version=version+1 WHERE id=$1`, carID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE carpool_memberships m SET status='active',service_start=c.service_start,service_end=c.service_end FROM carpool_cars c WHERE m.car_id=c.id AND c.id=$1 AND m.status='waiting_deployment'`, carID); err != nil {
			return err
		}
		return appendMainEvent(ctx, tx, carID, "native_service_activated", actor, "")
	})
	if err != nil {
		return NativeCarStatus{}, controlStoreError(err)
	}
	return s.NativeCarStatus(ctx, carID)
}
