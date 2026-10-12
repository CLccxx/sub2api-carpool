package service

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
)

type carpoolControlWork interface {
	ReconcileControl(context.Context) error
	SyncNext(context.Context) (bool, error)
}

type CarpoolControlWorker struct {
	store                                    carpoolControlWork
	reconcileInterval, pollInterval, timeout time.Duration
	ctx                                      context.Context
	cancel                                   context.CancelFunc
	start                                    sync.Once
	done                                     chan struct{}
}

func newCarpoolControlWorker(store carpoolControlWork, reconcile, poll, timeout time.Duration) *CarpoolControlWorker {
	ctx, cancel := context.WithCancel(context.Background())
	return &CarpoolControlWorker{store: store, reconcileInterval: reconcile, pollInterval: poll, timeout: timeout, ctx: ctx, cancel: cancel, done: make(chan struct{})}
}

func ProvideCarpoolControlWorker(store *carpool.ControlStore) *CarpoolControlWorker {
	if store == nil {
		return nil
	}
	w := newCarpoolControlWorker(store, time.Minute, time.Second, 30*time.Second)
	w.Start()
	return w
}

func (w *CarpoolControlWorker) Start() {
	if w == nil {
		return
	}
	w.start.Do(func() { go w.run() })
}

func (w *CarpoolControlWorker) run() {
	defer close(w.done)
	var lanes sync.WaitGroup
	lanes.Add(2)
	for i := 0; i < 2; i++ {
		go func() { defer lanes.Done(); w.deliver() }()
	}
	defer lanes.Wait()
	ticker := time.NewTicker(w.reconcileInterval)
	defer ticker.Stop()
	for w.ctx.Err() == nil {
		ctx, cancel := context.WithTimeout(w.ctx, w.timeout)
		err := w.store.ReconcileControl(ctx)
		cancel()
		if err != nil && w.ctx.Err() == nil {
			log.Print("[CarpoolControl] reconciliation incomplete; retrying on next interval")
		}
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *CarpoolControlWorker) deliver() {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	for w.ctx.Err() == nil {
		ctx, cancel := context.WithTimeout(w.ctx, w.timeout)
		worked, err := w.store.SyncNext(ctx)
		cancel()
		if err != nil && w.ctx.Err() == nil {
			log.Print("[CarpoolControl] sync persistence unavailable; retaining recoverable claim")
		}
		if worked && err == nil {
			continue
		}
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *CarpoolControlWorker) Stop() {
	if w == nil {
		return
	}
	w.cancel()
	w.Start()
	<-w.done
}
