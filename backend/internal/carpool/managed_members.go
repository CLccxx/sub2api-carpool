package carpool

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

type ManagedTarget struct {
	MainID     string `json:"main_instance_id"`
	NodeID     string `json:"node_id"`
	CarID      string `json:"car_id"`
	InstanceID string `json:"node_instance_id"`
}

type ManagedMember struct {
	Subject    string     `json:"subject_id"`
	UserID     int64      `json:"user_id"`
	GroupID    int64      `json:"group_id"`
	Version    int64      `json:"version"`
	Status     string     `json:"status"`
	ValidFrom  *time.Time `json:"valid_from"`
	ValidUntil *time.Time `json:"valid_until"`
	LeaseUntil *time.Time `json:"lease_until"`
}

func (n *NodeControl) Target() ManagedTarget {
	return ManagedTarget{MainID: n.cfg.MainInstanceID, NodeID: n.cfg.NodeID, CarID: n.cfg.CarID, InstanceID: n.instanceID}
}

func (n *NodeControl) validateMemberTarget(target ManagedTarget, subject string) error {
	if n == nil || n.db == nil || !n.cfg.Managed() {
		return ErrUnavailable
	}
	if target != n.Target() {
		return ErrControlTarget
	}
	id, err := uuid.Parse(subject)
	if err != nil || id == uuid.Nil || id.String() != subject {
		return ErrMainInvalid
	}
	return nil
}

func (n *NodeControl) bindTarget(ctx context.Context) error {
	var mainID, nodeID, carID, instanceID string
	var groupID int64
	if n.cfg.GroupID == 0 {
		err := n.db.QueryRowContext(ctx, `SELECT main_instance_id::text,node_id,car_id,node_instance_id::text FROM carpool_managed_node_binding WHERE singleton`).Scan(&mainID, &nodeID, &carID, &instanceID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return controlStoreError(err)
		}
		if mainID != n.cfg.MainInstanceID || nodeID != n.cfg.NodeID || carID != n.cfg.CarID || instanceID != n.instanceID {
			return ErrMainConflict
		}
		return nil
	}
	err := n.db.QueryRowContext(ctx, `INSERT INTO carpool_managed_node_binding(singleton,main_instance_id,node_id,car_id,group_id,node_instance_id)
 VALUES(TRUE,$1,$2,$3,$4,$5) ON CONFLICT(singleton) DO UPDATE SET singleton=TRUE
 RETURNING main_instance_id::text,node_id,car_id,group_id,node_instance_id::text`, n.cfg.MainInstanceID, n.cfg.NodeID, n.cfg.CarID, n.cfg.GroupID, n.instanceID).Scan(&mainID, &nodeID, &carID, &groupID, &instanceID)
	if err != nil {
		return controlStoreError(err)
	}
	if mainID != n.cfg.MainInstanceID || nodeID != n.cfg.NodeID || carID != n.cfg.CarID || groupID != n.cfg.GroupID || instanceID != n.instanceID {
		return ErrMainConflict
	}
	return nil
}

func (n *NodeControl) Provision(ctx context.Context, target ManagedTarget, subject string) (ManagedMember, error) {
	if err := n.validateMemberTarget(target, subject); err != nil {
		return ManagedMember{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := n.db.BeginTx(ctx, nil)
	if err != nil {
		return ManagedMember{}, ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	// Serialize an absent mapping too; no user can commit without its mapping.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,714420260919))`, target.MainID+":"+subject); err != nil {
		return ManagedMember{}, controlStoreError(err)
	}
	member, err := n.readMember(ctx, tx, subject)
	if err == nil {
		return member, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ManagedMember{}, controlStoreError(err)
	}
	var groupID int64
	if err = tx.QueryRowContext(ctx, `SELECT g.id FROM groups g JOIN carpool_managed_node_binding b ON b.group_id=g.id WHERE b.singleton AND g.status='active' AND g.deleted_at IS NULL FOR SHARE OF g`).Scan(&groupID); err != nil {
		return ManagedMember{}, controlStoreError(err)
	}
	email := subject + "." + target.MainID + "@node-members.invalid"
	var userID int64
	err = tx.QueryRowContext(ctx, `INSERT INTO users(email,password_hash,role,balance,concurrency,status,restrict_public_groups,balance_notify_enabled)
 VALUES($1,$2,'user',0,1,'active',TRUE,FALSE) RETURNING id`, email, "!managed:"+uuid.NewString()).Scan(&userID)
	if err != nil {
		return ManagedMember{}, controlStoreError(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO user_allowed_groups(user_id,group_id) VALUES($1,$2)`, userID, groupID); err != nil {
		return ManagedMember{}, controlStoreError(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO carpool_managed_members(main_instance_id,subject_id,node_id,car_id,group_id,node_instance_id,user_id)
 VALUES($1,$2,$3,$4,$5,$6,$7)`, target.MainID, subject, target.NodeID, target.CarID, groupID, target.InstanceID, userID)
	if err != nil {
		return ManagedMember{}, controlStoreError(err)
	}
	if err = tx.Commit(); err != nil {
		return ManagedMember{}, controlStoreError(err)
	}
	return ManagedMember{Subject: subject, UserID: userID, GroupID: groupID, Status: "prepared"}, nil
}

func (n *NodeControl) readMember(ctx context.Context, tx *sql.Tx, subject string) (ManagedMember, error) {
	var m ManagedMember
	var usable bool
	err := tx.QueryRowContext(ctx, `SELECT m.subject_id::text,m.user_id,m.group_id,m.version,m.status,m.valid_from,m.valid_until,m.lease_until,
 (u.status='active' AND u.deleted_at IS NULL AND u.role='user' AND u.restrict_public_groups)
 FROM carpool_managed_members m JOIN users u ON u.id=m.user_id
 WHERE m.main_instance_id=$1 AND m.subject_id=$2 AND m.node_id=$3 AND m.car_id=$4 AND m.node_instance_id=$5 AND ($6=0 OR m.group_id=$6)
 FOR UPDATE OF m,u`, n.cfg.MainInstanceID, subject, n.cfg.NodeID, n.cfg.CarID, n.instanceID, n.cfg.GroupID).Scan(
		&m.Subject, &m.UserID, &m.GroupID, &m.Version, &m.Status, &m.ValidFrom, &m.ValidUntil, &m.LeaseUntil, &usable)
	if err != nil {
		return m, err
	}
	if !usable {
		return ManagedMember{}, ErrRequired
	}
	return m, nil
}
