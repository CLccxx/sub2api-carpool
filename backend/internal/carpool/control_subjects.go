package carpool

import (
	"context"
	"database/sql"

	"github.com/google/uuid"
)

func ensureMainSubject(ctx context.Context, tx *sql.Tx, userID int64) (string, error) {
	if err := activeMainUser(ctx, tx, userID); err != nil {
		return "", err
	}
	var subject string
	err := tx.QueryRowContext(ctx, `INSERT INTO carpool_main_subjects(subject_id,user_id) VALUES($1,$2)
 ON CONFLICT(user_id) DO UPDATE SET user_id=EXCLUDED.user_id RETURNING subject_id::text`, uuid.NewString(), userID).Scan(&subject)
	return subject, err
}

func (s *MainService) EnsureSubject(ctx context.Context, userID int64) (string, error) {
	var subject string
	err := s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		subject, err = ensureMainSubject(ctx, tx, userID)
		return err
	})
	return subject, err
}
