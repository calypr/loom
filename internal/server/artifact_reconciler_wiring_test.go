package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"
)

type countingFSReconciler struct {
	calls chan struct{}
	err   error
}

func (c *countingFSReconciler) Reconcile(context.Context, time.Time, int) (int, error) {
	select {
	case c.calls <- struct{}{}:
	default:
	}
	return 0, c.err
}

func TestRunPrivateArtifactReconcilerUntypedNilReturnsCleanly(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("untyped-nil manager must be a clean no-op, got panic: %v", r)
		}
	}()
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()
	runPrivateArtifactReconciler(context.Background(), nil, ticks, slog.New(slog.DiscardHandler))
}

func TestStartArtifactFSReconcilerUntypedNilReturnsClosedChannel(t *testing.T) {
	done := startArtifactFSReconciler(context.Background(), nil, slog.New(slog.DiscardHandler))
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("nil filesystem store target must yield an immediately completed reconciler")
	}
}

func TestStartArtifactFSReconcilerSweepsImmediatelyAndStopsOnCancel(t *testing.T) {
	target := &countingFSReconciler{calls: make(chan struct{}, 4)}
	ctx, cancel := context.WithCancel(context.Background())
	done := startArtifactFSReconciler(ctx, target, slog.New(slog.DiscardHandler))
	select {
	case <-target.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("fs artifact reconciler did not perform the immediate sweep")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("fs artifact reconciler did not stop after cancel")
	}
}

// Drives runReconcilerLoop synchronously (manual ticks, no goroutine) so the
// logger buffer is only ever touched by this goroutine: the fs sweep must be
// labeled distinctly from the ClickHouse private artifact sweep.
func TestFilesystemReconcilerLoopUsesFilesystemArtifactLabel(t *testing.T) {
	buffer := &bytes.Buffer{}
	sweeps := 0
	sweep := func(context.Context) error {
		sweeps++
		return context.DeadlineExceeded
	}
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-time.After(50 * time.Millisecond)
		cancel()
	}()
	runReconcilerLoop(ctx, sweep, ticks, slog.New(slog.NewJSONHandler(buffer, nil)), "filesystem artifact")
	<-done
	if sweeps == 0 {
		t.Fatal("sweep never ran")
	}
	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(bytes.Split(buffer.Bytes(), []byte("\n"))[0]), &record); err != nil {
		t.Fatalf("no structured error entry logged; buffer=%q", buffer.String())
	}
	if record["msg"] != "filesystem artifact reconciliation failed" {
		t.Fatalf("label = %v, want the filesystem artifact sweep label distinct from the ClickHouse one", record["msg"])
	}
}
