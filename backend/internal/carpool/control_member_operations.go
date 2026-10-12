package carpool

import (
	"context"
	"database/sql"
)

func requireNoKeyBarrier(ctx context.Context, tx *sql.Tx, snap controlMemberSnapshot, keyID int64, onlyDeleted bool) error {
	var blocked bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM carpool_main_key_barriers WHERE node_id=$1 AND subject_id=$2 AND remote_key_id=$3 AND (NOT $4 OR reason='deleted'))`, snap.target.NodeID, snap.subject, keyID, onlyDeleted).Scan(&blocked)
	if err != nil {
		return err
	}
	if blocked {
		return ErrManagedKeyGone
	}
	return nil
}

func (s *ControlStore) mutateMemberKey(ctx context.Context, actor int64, carID string, keyID int64, input *ManagedKeyUpdate) (ManagedKey, error) {
	if keyID <= 0 || actor <= 0 || (input != nil && input.Validate() != nil) {
		return ManagedKey{}, ErrMainInvalid
	}
	enabling := input != nil && input.Status != nil && *input.Status == "active"
	reason := ""
	if input == nil {
		reason = "deleted"
	} else if input.Status != nil && *input.Status == "disabled" {
		reason = "disabled"
	}
	var snap controlMemberSnapshot
	err := s.main.carTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx, car Car) error {
		var err error
		snap, err = s.memberSnapshot(ctx, tx, car, actor, enabling, false)
		if err != nil {
			return err
		}
		if enabling {
			if err := requireNoKeyBarrier(ctx, tx, snap, keyID, true); err != nil {
				return err
			}
		}
		if reason != "" {
			// Commit a metadata-only barrier before HTTP, so an older create cannot expose this key.
			_, err = tx.ExecContext(ctx, `INSERT INTO carpool_main_key_barriers(node_id,subject_id,remote_key_id,reason) VALUES($1,$2,$3,$4)
 ON CONFLICT(node_id,subject_id,remote_key_id) DO UPDATE SET reason=CASE WHEN carpool_main_key_barriers.reason='deleted' THEN 'deleted' ELSE EXCLUDED.reason END`, snap.target.NodeID, snap.subject, keyID, reason)
			if err != nil {
				return err
			}
			if reason == "deleted" {
				_, err = tx.ExecContext(ctx, `UPDATE carpool_main_key_commands SET status='gone',updated_at=clock_timestamp() WHERE node_id=$1 AND subject_id=$2 AND remote_key_id=$3`, snap.target.NodeID, snap.subject, keyID)
			}
		}
		return err
	})
	if err != nil {
		return ManagedKey{}, controlStoreError(err)
	}
	credential, err := s.memberCredential(snap)
	if err != nil {
		return ManagedKey{}, err
	}
	var key ManagedKey
	if input == nil {
		err = s.nodeClient.DeleteKey(ctx, snap.connection.origin, credential, snap.target, snap.subject, keyID)
	} else {
		key, err = s.nodeClient.UpdateKey(ctx, snap.connection.origin, credential, snap.target, snap.subject, keyID, *input)
		key.Key = ""
		if err == nil && key.ID != keyID {
			err = ErrControlProtocol
		}
	}
	if err != nil {
		return ManagedKey{}, controlStoreError(err)
	}
	err = s.main.carTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx, car Car) error {
		current, err := s.memberSnapshot(ctx, tx, car, actor, enabling, false)
		if err != nil {
			return err
		}
		if !sameControlMember(snap, current) {
			return ErrMainConflict
		}
		if enabling {
			if key.Status != "active" || key.ExpiresAt == nil || key.ExpiresAt.After(*current.serviceEnd) || !key.ExpiresAt.After(current.checkedAt) {
				return ErrControlProtocol
			}
			if err := requireNoKeyBarrier(ctx, tx, current, keyID, true); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `DELETE FROM carpool_main_key_barriers WHERE node_id=$1 AND subject_id=$2 AND remote_key_id=$3 AND reason='disabled'`, snap.target.NodeID, snap.subject, keyID)
		}
		return err
	})
	if err != nil {
		return ManagedKey{}, controlStoreError(err)
	}
	return key, nil
}

func (s *ControlStore) UpdateMemberKey(ctx context.Context, actor int64, carID string, keyID int64, input ManagedKeyUpdate) (ManagedKey, error) {
	return s.mutateMemberKey(ctx, actor, carID, keyID, &input)
}
func (s *ControlStore) DeleteMemberKey(ctx context.Context, actor int64, carID string, keyID int64) error {
	_, err := s.mutateMemberKey(ctx, actor, carID, keyID, nil)
	return err
}

func controlMemberRead[T any](ctx context.Context, s *ControlStore, actor int64, carID string, read func(controlMemberSnapshot, string) (T, error)) (T, error) {
	var zero T
	var snap controlMemberSnapshot
	err := s.main.carTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx, car Car) error {
		var err error
		snap, err = s.memberSnapshot(ctx, tx, car, actor, false, false)
		return err
	})
	if err != nil {
		return zero, controlStoreError(err)
	}
	credential, err := s.memberCredential(snap)
	if err != nil {
		return zero, err
	}
	result, err := read(snap, credential)
	if err != nil {
		return zero, controlStoreError(err)
	}
	err = s.main.carTransaction(ctx, carID, func(ctx context.Context, tx *sql.Tx, car Car) error {
		current, err := s.memberSnapshot(ctx, tx, car, actor, false, false)
		if err != nil {
			return err
		}
		if !sameControlMember(snap, current) {
			return ErrMainConflict
		}
		return nil
	})
	if err != nil {
		return zero, controlStoreError(err)
	}
	return result, nil
}

func (s *ControlStore) ListMemberKeys(ctx context.Context, actor int64, carID string, q ControlPageQuery) (ControlPage[ManagedKey], error) {
	if q.Validate() != nil {
		return ControlPage[ManagedKey]{}, ErrMainInvalid
	}
	result, err := controlMemberRead(ctx, s, actor, carID, func(snap controlMemberSnapshot, credential string) (ControlPage[ManagedKey], error) {
		return s.nodeClient.ListMemberKeys(ctx, snap.connection.origin, credential, snap.target, snap.remoteUserID, snap.remoteGroupID, q)
	})
	for i := range result.Items {
		result.Items[i].Key = ""
	}
	return result, err
}

func (s *ControlStore) MemberUsage(ctx context.Context, actor int64, carID string, q ControlUsageQuery) (ControlPage[ManagedUsage], error) {
	if _, _, err := q.Range(); err != nil || q.Validate() != nil {
		return ControlPage[ManagedUsage]{}, ErrMainInvalid
	}
	return controlMemberRead(ctx, s, actor, carID, func(snap controlMemberSnapshot, credential string) (ControlPage[ManagedUsage], error) {
		return s.nodeClient.MemberUsage(ctx, snap.connection.origin, credential, snap.target, snap.remoteUserID, snap.remoteGroupID, q)
	})
}

func (s *ControlStore) MemberUsageStats(ctx context.Context, actor int64, carID string, q ControlUsageQuery) (ManagedUsageStats, error) {
	if _, _, err := q.Range(); err != nil || q.Validate() != nil {
		return ManagedUsageStats{}, ErrMainInvalid
	}
	return controlMemberRead(ctx, s, actor, carID, func(snap controlMemberSnapshot, credential string) (ManagedUsageStats, error) {
		return s.nodeClient.MemberUsageStats(ctx, snap.connection.origin, credential, snap.target, snap.remoteUserID, snap.remoteGroupID, q)
	})
}
