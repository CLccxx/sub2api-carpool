package service

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
)

type carpoolQuotaStore interface {
	NativeQuotaAccount(context.Context) (int64, error)
	FlushQuotaCaches(context.Context) error
	RecordQuotaReadFailure(context.Context, time.Time) error
	ApplyQuotaObservation(context.Context, int64, carpool.QuotaObservation, bool) (carpool.NativeQuotaStatus, error)
}

type CarpoolQuotaWorker struct {
	store             carpoolQuotaStore
	read              func(context.Context, int64) (carpool.QuotaObservation, error)
	interval, timeout time.Duration
	ctx               context.Context
	cancel            context.CancelFunc
	start             sync.Once
	done              chan struct{}
	lane              chan struct{}
}

func newCarpoolQuotaWorker(store carpoolQuotaStore, read func(context.Context, int64) (carpool.QuotaObservation, error), interval, timeout time.Duration) *CarpoolQuotaWorker {
	ctx, cancel := context.WithCancel(context.Background())
	return &CarpoolQuotaWorker{store: store, read: read, interval: interval, timeout: timeout, ctx: ctx, cancel: cancel, done: make(chan struct{}), lane: make(chan struct{}, 1)}
}

// The managed-key dependency installs native cache invalidation before the worker starts.
func ProvideCarpoolQuotaWorker(node *carpool.NodeControl, usage *AccountUsageService, keys *CarpoolManagedKeys) *CarpoolQuotaWorker {
	if node == nil || keys == nil {
		return nil
	}
	w := newCarpoolQuotaWorker(node, usage.readCarpoolQuota, time.Minute, 30*time.Second)
	w.Start()
	return w
}

func (w *CarpoolQuotaWorker) Reconcile(ctx context.Context, force bool) (carpool.NativeQuotaStatus, error) {
	if w == nil {
		return carpool.NativeQuotaStatus{}, carpool.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	select {
	case w.lane <- struct{}{}:
		defer func() { <-w.lane }()
	case <-ctx.Done():
		return carpool.NativeQuotaStatus{}, carpool.ErrUnavailable
	}
	id, err := w.store.NativeQuotaAccount(ctx)
	if err != nil {
		return carpool.NativeQuotaStatus{}, err
	}
	if err = w.store.FlushQuotaCaches(ctx); err != nil {
		return carpool.NativeQuotaStatus{}, err
	}
	captured := time.Now()
	sample, err := w.read(ctx, id)
	if err != nil {
		_ = w.store.RecordQuotaReadFailure(ctx, captured)
		return carpool.NativeQuotaStatus{}, carpool.ErrUnavailable
	}
	status, err := w.store.ApplyQuotaObservation(ctx, id, sample, force)
	if errors.Is(err, carpool.ErrMainInvalid) || errors.Is(err, carpool.ErrMainConflict) {
		_ = w.store.RecordQuotaReadFailure(ctx, captured)
	}
	return status, err
}

func (w *CarpoolQuotaWorker) Start() {
	if w == nil {
		return
	}
	w.start.Do(func() { go w.run() })
}

func (w *CarpoolQuotaWorker) run() {
	defer close(w.done)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for w.ctx.Err() == nil {
		_, err := w.Reconcile(w.ctx, false)
		if err != nil && !errors.Is(err, carpool.ErrMainNotFound) && w.ctx.Err() == nil {
			log.Print("[CarpoolQuota] native quota reconciliation incomplete; retrying on next interval")
		}
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *CarpoolQuotaWorker) Stop() {
	if w == nil {
		return
	}
	w.cancel()
	w.Start()
	<-w.done
}
