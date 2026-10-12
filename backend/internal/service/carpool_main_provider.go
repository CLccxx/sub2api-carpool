package service

import (
	"context"
	"database/sql"
	"log"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/config"
)

func ProvideCarpoolMainService(db *sql.DB, cfg *config.Config) (*carpool.MainService, error) {
	if err := cfg.CarpoolMain.Validate(cfg.CarpoolNode.Enabled); err != nil {
		return nil, err
	}
	if !cfg.CarpoolMain.Enabled {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := carpool.Migrate(ctx, db); err != nil {
		return nil, err
	}
	return carpool.NewMainService(db, cfg.CarpoolMain), nil
}

type carpoolSweeper interface {
	Sweep(context.Context) (int, error)
}

type CarpoolExpiryWorker struct {
	sweeper           carpoolSweeper
	interval, timeout time.Duration
	ctx               context.Context
	cancel            context.CancelFunc
	start             sync.Once
	done              chan struct{}
}

func newCarpoolExpiryWorker(sweeper carpoolSweeper, interval, timeout time.Duration) *CarpoolExpiryWorker {
	ctx, cancel := context.WithCancel(context.Background())
	return &CarpoolExpiryWorker{sweeper: sweeper, interval: interval, timeout: timeout, ctx: ctx, cancel: cancel, done: make(chan struct{})}
}

func ProvideCarpoolExpiryWorker(main *carpool.MainService) *CarpoolExpiryWorker {
	if main == nil || !main.Enabled() {
		return nil
	}
	w := newCarpoolExpiryWorker(main, time.Minute, 30*time.Second)
	w.Start()
	return w
}

func (w *CarpoolExpiryWorker) Start() {
	if w == nil {
		return
	}
	w.start.Do(func() { go w.run() })
}

func (w *CarpoolExpiryWorker) run() {
	defer close(w.done)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		if w.ctx.Err() != nil {
			return
		}
		ctx, cancel := context.WithTimeout(w.ctx, w.timeout)
		_, err := w.sweeper.Sweep(ctx)
		cancel()
		if err != nil && w.ctx.Err() == nil {
			log.Print("[CarpoolExpiry] sweep failed; retrying on next interval")
		}
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Stop cancels in-flight SQL and joins the worker before infrastructure closes.
func (w *CarpoolExpiryWorker) Stop() {
	if w == nil {
		return
	}
	w.cancel()
	w.Start()
	<-w.done
}
