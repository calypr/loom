package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type blockingPrivateArtifactReconciler struct {
	calls      atomic.Int32
	active     atomic.Int32
	maxActive  atomic.Int32
	limits     chan int
	bounded    chan bool
	started    chan int32
	releaseOne chan struct{}
}

func (r *blockingPrivateArtifactReconciler) Reconcile(ctx context.Context, _ time.Time, limit int) error {
	call := r.calls.Add(1)
	active := r.active.Add(1)
	for previous := r.maxActive.Load(); active > previous && !r.maxActive.CompareAndSwap(previous, active); previous = r.maxActive.Load() {
	}
	_, hasDeadline := ctx.Deadline()
	r.limits <- limit
	r.bounded <- hasDeadline
	r.started <- call
	defer r.active.Add(-1)
	if call == 1 {
		select {
		case <-r.releaseOne:
			return errors.New("expired artifact cleanup failed")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestPrivateArtifactReconcilerIsImmediateBoundedSerialAndCancelable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time, 1)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	manager := &blockingPrivateArtifactReconciler{
		limits: make(chan int, 2), bounded: make(chan bool, 2), started: make(chan int32, 2), releaseOne: make(chan struct{}),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPrivateArtifactReconciler(ctx, manager, ticks, logger)
	}()

	select {
	case call := <-manager.started:
		if call != 1 {
			t.Fatalf("first reconcile call = %d, want immediate call 1", call)
		}
	case <-time.After(time.Second):
		t.Fatal("immediate reconciliation did not start")
	}
	if limit := <-manager.limits; limit != 1000 {
		t.Fatalf("reconcile candidate limit = %d, want 1000", limit)
	}
	if bounded := <-manager.bounded; !bounded {
		t.Fatal("immediate reconciliation did not receive a shutdown-bounded context")
	}

	ticks <- time.Now()
	if calls := manager.calls.Load(); calls != 1 {
		t.Fatalf("reconciliation overlapped a blocked sweep: calls=%d", calls)
	}
	close(manager.releaseOne)
	select {
	case call := <-manager.started:
		if call != 2 {
			t.Fatalf("second reconcile call = %d, want 2", call)
		}
	case <-time.After(time.Second):
		t.Fatal("queued tick did not start the next sweep")
	}
	if limit := <-manager.limits; limit != 1000 {
		t.Fatalf("second reconcile candidate limit = %d, want 1000", limit)
	}
	if bounded := <-manager.bounded; !bounded {
		t.Fatal("tick reconciliation did not receive a shutdown-bounded context")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reconciler did not stop after context cancellation")
	}
	if got := manager.maxActive.Load(); got != 1 {
		t.Fatalf("maximum concurrent reconciliation calls = %d, want 1", got)
	}
	if got := manager.calls.Load(); got != 2 {
		t.Fatalf("reconcile calls = %d, want immediate sweep plus one queued tick", got)
	}
	if !strings.Contains(logs.String(), "private ClickHouse artifact reconciliation failed") {
		t.Fatalf("reconcile failure was not logged: %q", logs.String())
	}
}
