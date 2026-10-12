package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/google/uuid"
)

var ErrManagedKeyGone = carpool.ErrManagedKeyGone

type ManagedKey = carpool.ManagedKey
type ManagedKeyUpdate = carpool.ManagedKeyUpdate

type CarpoolManagedKeys struct {
	node   *carpool.NodeControl
	client *dbent.Client
	api    *APIKeyService
}

func NewCarpoolManagedKeys(node *carpool.NodeControl, client *dbent.Client, api *APIKeyService) *CarpoolManagedKeys {
	return &CarpoolManagedKeys{node: node, client: client, api: api}
}

func managedKeyDTO(key *APIKey, secret bool) ManagedKey {
	result := ManagedKey{ID: key.ID, Name: key.Name, Status: key.Status, ExpiresAt: key.ExpiresAt}
	if secret {
		result.Key = key.Key
	}
	return result
}

// The member lock serializes commands, including the first request for an operation ID.
func (s *CarpoolManagedKeys) Create(ctx context.Context, target carpool.ManagedTarget, subject, operation, name string) (ManagedKey, error) {
	id, err := uuid.Parse(operation)
	if err != nil || id == uuid.Nil || id.String() != operation || !carpool.ValidManagedKeyName(name) {
		return ManagedKey{}, carpool.ErrMainInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return ManagedKey{}, carpool.ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	member, err := s.node.LockManagedMember(ctx, tx.Client(), target, subject, true)
	if err != nil {
		return ManagedKey{}, err
	}
	sum := sha256.Sum256([]byte(name))
	hash := hex.EncodeToString(sum[:])
	rows, err := tx.Client().QueryContext(ctx, `SELECT request_hash,key_id FROM carpool_node_key_commands WHERE main_instance_id=$1 AND subject_id=$2 AND operation_id=$3`, target.MainID, subject, operation)
	if err != nil {
		return ManagedKey{}, carpool.ErrUnavailable
	}
	var storedHash string
	var keyID int64
	found := rows.Next()
	if found {
		err = rows.Scan(&storedHash, &keyID)
	} else {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return ManagedKey{}, carpool.ErrUnavailable
	}
	if found && storedHash != hash {
		return ManagedKey{}, carpool.ErrMainConflict
	}
	txCtx := dbent.NewTxContext(ctx, tx)
	var key *APIKey
	if found {
		deleted, lockErr := lockManagedKey(ctx, tx.Client(), member, keyID)
		if lockErr != nil {
			return ManagedKey{}, lockErr
		}
		if deleted {
			return ManagedKey{}, ErrManagedKeyGone
		}
		key, err = s.api.GetByID(txCtx, keyID)
		if err != nil {
			return ManagedKey{}, managedKeyError(err)
		}
		if key.Status != StatusActive || key.ExpiresAt == nil || key.ExpiresAt.After(*member.ValidUntil) {
			return ManagedKey{}, ErrManagedKeyGone
		}
	} else {
		key, err = s.api.Create(txCtx, member.UserID, CreateAPIKeyRequest{Name: name, GroupID: &member.GroupID})
		if err != nil {
			return ManagedKey{}, managedKeyError(err)
		}
		key, err = s.api.Update(txCtx, key.ID, member.UserID, UpdateAPIKeyRequest{ExpiresAt: member.ValidUntil})
		if err != nil {
			return ManagedKey{}, managedKeyError(err)
		}
		_, err = tx.Client().ExecContext(ctx, `INSERT INTO carpool_node_key_commands(main_instance_id,subject_id,operation_id,request_hash,key_id) VALUES($1,$2,$3,$4,$5)`, target.MainID, subject, operation, hash, key.ID)
		if err != nil {
			return ManagedKey{}, carpool.ErrUnavailable
		}
	}
	if _, err = s.node.LockManagedMember(ctx, tx.Client(), target, subject, true); err != nil {
		return ManagedKey{}, err
	}
	if err = checkManagedKeyLive(ctx, tx.Client(), member, key.ID); err != nil {
		return ManagedKey{}, err
	}
	if err = tx.Commit(); err != nil {
		return ManagedKey{}, carpool.ErrUnavailable
	}
	s.invalidateCommitted(ctx, key.Key)
	return managedKeyDTO(key, true), nil
}

func (s *CarpoolManagedKeys) Update(ctx context.Context, target carpool.ManagedTarget, subject string, keyID int64, input ManagedKeyUpdate) (ManagedKey, error) {
	if keyID <= 0 || input.Validate() != nil {
		return ManagedKey{}, carpool.ErrMainInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return ManagedKey{}, carpool.ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	enabling := input.Status != nil && *input.Status == StatusActive
	member, err := s.node.LockManagedMember(ctx, tx.Client(), target, subject, enabling)
	if err != nil {
		return ManagedKey{}, err
	}
	deleted, err := lockManagedKey(ctx, tx.Client(), member, keyID)
	if err != nil {
		return ManagedKey{}, err
	}
	if deleted {
		return ManagedKey{}, ErrManagedKeyGone
	}
	txCtx := dbent.NewTxContext(ctx, tx)
	key, err := s.api.GetByID(txCtx, keyID)
	if err != nil {
		return ManagedKey{}, managedKeyError(err)
	}
	if enabling && (key.ExpiresAt == nil || key.ExpiresAt.After(*member.ValidUntil)) {
		return ManagedKey{}, ErrManagedKeyGone
	}
	key, err = s.api.Update(txCtx, keyID, member.UserID, UpdateAPIKeyRequest{Name: input.Name, Status: input.Status})
	if err != nil {
		return ManagedKey{}, managedKeyError(err)
	}
	if enabling {
		if _, err = s.node.LockManagedMember(ctx, tx.Client(), target, subject, true); err != nil {
			return ManagedKey{}, err
		}
		if err = checkManagedKeyLive(ctx, tx.Client(), member, key.ID); err != nil {
			return ManagedKey{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return ManagedKey{}, carpool.ErrUnavailable
	}
	s.invalidateCommitted(ctx, key.Key)
	return managedKeyDTO(key, false), nil
}

func (s *CarpoolManagedKeys) Delete(ctx context.Context, target carpool.ManagedTarget, subject string, keyID int64) error {
	if keyID <= 0 {
		return carpool.ErrMainInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return carpool.ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	member, err := s.node.LockManagedMember(ctx, tx.Client(), target, subject, false)
	if err != nil {
		return err
	}
	deleted, err := lockManagedKey(ctx, tx.Client(), member, keyID)
	if err != nil {
		return err
	}
	if deleted {
		return nil
	}
	txCtx := dbent.NewTxContext(ctx, tx)
	key, err := s.api.GetByID(txCtx, keyID)
	if err != nil {
		return managedKeyError(err)
	}
	if err = s.api.Delete(txCtx, keyID, member.UserID); err != nil {
		return managedKeyError(err)
	}
	if err = tx.Commit(); err != nil {
		return carpool.ErrUnavailable
	}
	s.invalidateCommitted(ctx, key.Key)
	return nil
}

func lockManagedKey(ctx context.Context, tx carpool.ManagedQuery, member carpool.ManagedMember, id int64) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT deleted_at FROM api_keys WHERE id=$1 AND user_id=$2 AND group_id=$3 FOR UPDATE`, id, member.UserID, member.GroupID)
	if err != nil {
		return false, carpool.ErrUnavailable
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if rows.Err() != nil {
			return false, carpool.ErrUnavailable
		}
		return false, carpool.ErrMainNotFound
	}
	var deleted sql.NullTime
	if err = rows.Scan(&deleted); err != nil {
		return false, carpool.ErrUnavailable
	}
	return deleted.Valid, nil
}

func checkManagedKeyLive(ctx context.Context, tx carpool.ManagedQuery, member carpool.ManagedMember, id int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT EXISTS(SELECT 1 FROM api_keys WHERE id=$1 AND user_id=$2 AND group_id=$3 AND deleted_at IS NULL
 AND status='active' AND expires_at>clock_timestamp() AND expires_at<=$4)`, id, member.UserID, member.GroupID, member.ValidUntil)
	if err != nil {
		return carpool.ErrUnavailable
	}
	defer func() { _ = rows.Close() }()
	var live bool
	if !rows.Next() || rows.Scan(&live) != nil {
		return carpool.ErrUnavailable
	}
	if !live {
		return ErrManagedKeyGone
	}
	return nil
}

func managedKeyError(err error) error {
	switch {
	case errors.Is(err, ErrAPIKeyNotFound):
		return ErrManagedKeyGone
	case errors.Is(err, carpool.ErrRequired), errors.Is(err, ErrGroupNotAllowed):
		return carpool.ErrRequired
	default:
		return carpool.ErrUnavailable
	}
}

func (s *CarpoolManagedKeys) invalidateCommitted(ctx context.Context, key string) {
	// The DB commit survives a disconnected caller; its invalidation must too.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	s.api.InvalidateAuthCacheByKey(ctx, key)
}
