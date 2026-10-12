package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type carpoolSweepFunc func(context.Context) (int, error)

func (f carpoolSweepFunc) Sweep(ctx context.Context) (int, error) { return f(ctx) }

func TestCarpoolExpiryStartupPeriodicAndStop(t *testing.T) {
	var count atomic.Int64
	w := newCarpoolExpiryWorker(carpoolSweepFunc(func(ctx context.Context) (int, error) {
		_, ok := ctx.Deadline()
		if !ok {
			return 0, errors.New("missing deadline")
		}
		count.Add(1)
		return 0, errors.New("retry next sweep")
	}), 5*time.Millisecond, time.Second)
	w.Start()
	t.Cleanup(w.Stop)
	require.Eventually(t, func() bool { return count.Load() >= 2 }, time.Second, time.Millisecond)
	w.Stop()
	n := count.Load()
	w.Stop()
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, n, count.Load())
}

func TestCarpoolExpiryStopCancelsSweep(t *testing.T) {
	started, exited := make(chan struct{}), make(chan struct{})
	w := newCarpoolExpiryWorker(carpoolSweepFunc(func(ctx context.Context) (int, error) {
		close(started)
		<-ctx.Done()
		close(exited)
		return 0, ctx.Err()
	}), time.Hour, time.Hour)
	w.Start()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("no startup sweep")
	}
	w.Stop()
	select {
	case <-exited:
	default:
		t.Fatal("Stop did not wait for cancellation")
	}
}

func TestCarpoolExpirySweepTimeout(t *testing.T) {
	finished := make(chan struct{}, 10)
	w := newCarpoolExpiryWorker(carpoolSweepFunc(func(ctx context.Context) (int, error) {
		<-ctx.Done()
		finished <- struct{}{}
		return 0, ctx.Err()
	}), time.Hour, 5*time.Millisecond)
	w.Start()
	defer w.Stop()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("sweep not bounded")
	}
}

func TestCarpoolMainProviderDisabledDoesNotMigrate(t *testing.T) {
	main, err := ProvideCarpoolMainService(nil, &config.Config{})
	require.NoError(t, err)
	require.Nil(t, main)
	require.Nil(t, ProvideCarpoolExpiryWorker(main))
	_, err = ProvideCarpoolMainService(nil, &config.Config{CarpoolMain: carpool.MainConfig{Enabled: true}, CarpoolNode: carpool.Config{Enabled: true}})
	require.Error(t, err)
	_, err = ProvideCarpoolMainService(nil, &config.Config{CarpoolMain: carpool.MainConfig{TestOrdersEnabled: true}})
	require.Error(t, err)
}

func TestCarpoolMainProviderMigrationFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	migrationErr := errors.New("migration unavailable")
	mock.ExpectBegin().WillReturnError(migrationErr)
	main, err := ProvideCarpoolMainService(db, &config.Config{CarpoolMain: carpool.MainConfig{Enabled: true}})
	require.ErrorIs(t, err, migrationErr)
	require.Nil(t, main)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCarpoolExpiryStopBeforeStart(t *testing.T) {
	w := newCarpoolExpiryWorker(carpoolSweepFunc(func(context.Context) (int, error) { t.Error("swept after stop"); return 0, nil }), time.Minute, time.Second)
	w.Stop()
	w.Start()
	w.Stop()
}
