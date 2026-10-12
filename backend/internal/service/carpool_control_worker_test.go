package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type controlWorkStub struct {
	reconcile func(context.Context) error
	sync      func(context.Context) (bool, error)
}

func (f controlWorkStub) ReconcileControl(ctx context.Context) error { return f.reconcile(ctx) }
func (f controlWorkStub) SyncNext(ctx context.Context) (bool, error) { return f.sync(ctx) }

func TestCarpoolControlWorkerStartsReconcilesAndStops(t *testing.T) {
	var reconciles, sends atomic.Int64
	w := newCarpoolControlWorker(controlWorkStub{
		reconcile: func(ctx context.Context) error {
			_, bounded := ctx.Deadline()
			if !bounded {
				t.Error("missing deadline")
			}
			reconciles.Add(1)
			return errors.New("synthetic private failure")
		},
		sync: func(context.Context) (bool, error) { sends.Add(1); return false, nil },
	}, 5*time.Millisecond, 5*time.Millisecond, time.Second)
	w.Start()
	t.Cleanup(w.Stop)
	require.Eventually(t, func() bool { return reconciles.Load() >= 2 && sends.Load() >= 2 }, time.Second, time.Millisecond)
	w.Stop()
	before := sends.Load()
	w.Stop()
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, before, sends.Load())
	require.Nil(t, ProvideCarpoolControlWorker(nil))
}

func TestCarpoolControlWorkerShutdownCancelsBothLanes(t *testing.T) {
	started := make(chan struct{}, 2)
	var exited atomic.Int64
	w := newCarpoolControlWorker(controlWorkStub{
		reconcile: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
		sync: func(ctx context.Context) (bool, error) {
			started <- struct{}{}
			<-ctx.Done()
			exited.Add(1)
			return true, ctx.Err()
		},
	}, time.Hour, time.Hour, time.Hour)
	w.Start()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("worker lane not started")
		}
	}
	w.Stop()
	require.EqualValues(t, 2, exited.Load())
}

func TestCarpoolControlWorkerStopBeforeStart(t *testing.T) {
	w := newCarpoolControlWorker(controlWorkStub{
		reconcile: func(context.Context) error { t.Error("reconciled after stop"); return nil },
		sync:      func(context.Context) (bool, error) { t.Error("sent after stop"); return false, nil },
	}, time.Minute, time.Second, time.Second)
	w.Stop()
	w.Start()
	w.Stop()
}
