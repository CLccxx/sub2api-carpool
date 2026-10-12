package carpool

import (
	"context"
	"database/sql"
	"time"
)

type ControlConsole struct {
	CarID              string     `json:"car_id"`
	MemberStatus       string     `json:"member_status"`
	ConnectionStatus   string     `json:"connection_status"`
	SyncStatus         string     `json:"sync_status"`
	AccessStatus       string     `json:"access_status"`
	LastErrorCode      string     `json:"last_error_code"`
	APIBaseURL         string     `json:"api_base_url"`
	CanReadNode        bool       `json:"can_read_node"`
	CanCreateKey       bool       `json:"can_create_key"`
	ServiceStart       *time.Time `json:"service_start"`
	ServiceEnd         *time.Time `json:"service_end"`
	SyncedAt           *time.Time `json:"synced_at"`
	ServerTime         time.Time  `json:"server_time"`
	NativeSubscription bool       `json:"native_subscription"`
}

func (s *ControlStore) ConsoleForMember(ctx context.Context, actor int64, carID string) (ControlConsole, error) {
	result := ControlConsole{CarID: carID}
	err := s.controlCarTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx) error {
		if err := activeMainUser(ctx, tx, actor); err != nil {
			return err
		}
		var a controlAuthority
		var carStatus, mode string
		var carStart, carEnd *time.Time
		err := tx.QueryRowContext(ctx, `SELECT m.id,m.status,m.service_start,m.service_end,o.status,c.status,c.enrollment_mode,c.service_start,c.service_end,clock_timestamp()
 FROM carpool_memberships m JOIN carpool_orders o ON o.id=m.order_id JOIN carpool_cars c ON c.id=m.car_id
 WHERE m.car_id=$1 AND m.user_id=$2 ORDER BY m.created_at DESC,m.id DESC LIMIT 1`, carID, actor).Scan(&a.membership, &a.memberStatus, &a.start, &a.end, &a.orderStatus, &carStatus, &mode, &carStart, &carEnd, &result.ServerTime)
		if err != nil {
			return err
		}
		if s.main.cfg.ManualEnrollmentEnabled && mode != "manual" {
			return ErrMainNotFound
		}
		if err = s.publishControlCar(ctx, tx, carID); err != nil {
			return err
		}
		var origin string
		var mapped, synced bool
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(c.status,'unconfigured'),COALESCE(c.origin,''),COALESCE(g.delivery,'pending'),COALESCE(NULLIF(g.last_error_code,''),c.last_error_code,''),g.synced_at,
 COALESCE(nm.remote_user_id>0 AND nm.remote_group_id>0 AND nm.provisioned_instance_id=c.expected_instance_id,FALSE),
 COALESCE(g.status='active' AND g.delivery='synced' AND g.generation=c.generation AND g.ack_generation=c.generation AND g.version=g.ack_version AND g.lease_until>clock_timestamp(),FALSE),COALESCE(g.native_subscription,FALSE)
 FROM carpool_cars car LEFT JOIN carpool_nodes n ON n.car_id=car.id LEFT JOIN carpool_node_connections c ON c.node_id=n.id
 LEFT JOIN carpool_main_subjects s ON s.user_id=$2 LEFT JOIN carpool_main_node_members nm ON nm.node_id=n.id AND nm.subject_id=s.subject_id
 LEFT JOIN carpool_control_grants g ON g.node_id=n.id AND g.subject_id=s.subject_id WHERE car.id=$1`, carID, actor).Scan(&result.ConnectionStatus, &origin, &result.SyncStatus, &result.LastErrorCode, &result.SyncedAt, &mapped, &synced, &result.NativeSubscription)
		if err != nil {
			return err
		}
		result.MemberStatus, result.ServiceStart, result.ServiceEnd = a.memberStatus, a.start, a.end
		result.CanReadNode = result.ConnectionStatus == "ready" && mapped
		if result.ConnectionStatus == "ready" && !result.NativeSubscription {
			result.APIBaseURL = origin + "/v1"
		}
		a.userStatus = "active"
		g := a.grant(carStatus, carStart, carEnd, result.ServerTime)
		switch {
		case g.Status == "prepared":
			result.AccessStatus = "unactivated"
		case a.end != nil && !a.end.After(result.ServerTime):
			result.AccessStatus = "expired"
		case g.Status == "paused":
			result.AccessStatus = "paused"
		case g.Status == "revoked":
			result.AccessStatus = "revoked"
		case !result.CanReadNode:
			result.AccessStatus = "unavailable"
		case !synced:
			result.AccessStatus = "synchronizing"
		default:
			result.AccessStatus = "active"
			result.CanCreateKey = true
		}
		return nil
	})
	if err != nil {
		return ControlConsole{}, controlStoreError(err)
	}
	return result, nil
}

type ControlSyncMember struct {
	MembershipID  string     `json:"membership_id"`
	UserID        int64      `json:"user_id"`
	Status        string     `json:"status"`
	Delivery      string     `json:"delivery"`
	Version       int64      `json:"version"`
	AckVersion    int64      `json:"ack_version"`
	Attempts      int        `json:"attempts"`
	LastErrorCode string     `json:"last_error_code"`
	AvailableAt   time.Time  `json:"available_at"`
	SyncedAt      *time.Time `json:"synced_at"`
	LeaseUntil    *time.Time `json:"lease_until"`
}

type ControlSyncPage struct {
	PageMeta
	Items []ControlSyncMember `json:"items"`
}

func (s *ControlStore) ControlSyncStatus(ctx context.Context, carID string, query PageQuery) (ControlSyncPage, error) {
	q, err := query.normalized()
	if err != nil {
		return ControlSyncPage{}, err
	}
	result := ControlSyncPage{PageMeta: PageMeta{Page: q.Page, PageSize: q.PageSize}, Items: []ControlSyncMember{}}
	err = s.controlCarTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx) error {
		if err := s.publishControlCar(ctx, tx, carID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*),clock_timestamp() FROM carpool_control_grants g JOIN carpool_nodes n ON n.id=g.node_id WHERE n.car_id=$1`, carID).Scan(&result.Total, &result.ServerTime); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT g.membership_id,s.user_id,g.status,g.delivery,g.version,g.ack_version,g.attempts,g.last_error_code,g.available_at,g.synced_at,g.lease_until
 FROM carpool_control_grants g JOIN carpool_nodes n ON n.id=g.node_id JOIN carpool_main_subjects s ON s.subject_id=g.subject_id
 WHERE n.car_id=$1 ORDER BY s.user_id LIMIT $2 OFFSET $3`, carID, q.PageSize, (q.Page-1)*q.PageSize)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var item ControlSyncMember
			if err = rows.Scan(&item.MembershipID, &item.UserID, &item.Status, &item.Delivery, &item.Version, &item.AckVersion, &item.Attempts, &item.LastErrorCode, &item.AvailableAt, &item.SyncedAt, &item.LeaseUntil); err != nil {
				return err
			}
			result.Items = append(result.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return ControlSyncPage{}, controlStoreError(err)
	}
	return result, nil
}
