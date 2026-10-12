//go:build integration

package carpool

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestControlMainSubjectsStableAndIndependent(t *testing.T) {
	db := mainTestDB(t)
	ctx := context.Background()
	main := NewMainService(db, MainConfig{Enabled: true})
	var wg sync.WaitGroup
	type result struct {
		subject string
		err     error
	}
	results := make(chan result, 12)
	for range 12 {
		wg.Add(1)
		go func() { defer wg.Done(); subject, err := main.EnsureSubject(ctx, 2); results <- result{subject, err} }()
	}
	wg.Wait()
	close(results)
	var first string
	for r := range results {
		require.NoError(t, r.err)
		if first == "" {
			first = r.subject
		} else {
			require.Equal(t, first, r.subject)
		}
	}
	_, err := uuid.Parse(first)
	require.NoError(t, err)
	require.NotEqual(t, "2", first)
	restarted := NewMainService(db, MainConfig{Enabled: true})
	again, err := restarted.EnsureSubject(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, first, again)
	other, err := main.EnsureSubject(ctx, 3)
	require.NoError(t, err)
	require.NotEqual(t, first, other)
	_, err = db.Exec(`UPDATE users SET status='disabled',deleted_at=clock_timestamp() WHERE id=2`)
	require.NoError(t, err)
	_, err = main.EnsureSubject(ctx, 2)
	require.ErrorIs(t, err, ErrMainNotFound)
	var stored string
	require.NoError(t, db.QueryRow(`SELECT subject_id::text FROM carpool_main_subjects WHERE user_id=2`).Scan(&stored))
	require.Equal(t, first, stored)
}
