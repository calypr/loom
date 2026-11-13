package server

import (
	"context"
	"log/slog"
	"time"
)

const (
	privateArtifactReconcileInterval = 30 * time.Second
	privateArtifactReconcileLimit    = 1000
	artifactFSReconcileInterval      = 60 * time.Second
	artifactFSReconcileLimit         = 1000
)

type privateArtifactReconcileTarget interface {
	Reconcile(context.Context, time.Time, int) error
}

type artifactFSReconcileTarget interface {
	Reconcile(context.Context, time.Time, int) (int, error)
}

func startPrivateArtifactReconciler(ctx context.Context, manager privateArtifactReconcileTarget, logger *slog.Logger) <-chan struct{} {
	sweep := func(sweepCtx context.Context) error {
		return manager.Reconcile(sweepCtx, time.Now().UTC(), privateArtifactReconcileLimit)
	}
	return startTickerReconciler(ctx, sweep, logger, privateArtifactReconcileInterval, "private ClickHouse artifact")
}

func startArtifactFSReconciler(ctx context.Context, store artifactFSReconcileTarget, logger *slog.Logger) <-chan struct{} {
	if store == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	sweep := func(sweepCtx context.Context) error {
		_, err := store.Reconcile(sweepCtx, time.Now().UTC(), artifactFSReconcileLimit)
		return err
	}
	return startTickerReconciler(ctx, sweep, logger, artifactFSReconcileInterval, "filesystem artifact")
}

func startTickerReconciler(ctx context.Context, sweep func(context.Context) error, logger *slog.Logger, interval time.Duration, label string) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		runReconcilerLoop(ctx, sweep, ticker.C, logger, label)
	}()
	return done
}

func runPrivateArtifactReconciler(ctx context.Context, manager privateArtifactReconcileTarget, ticks <-chan time.Time, logger *slog.Logger) {
	if manager == nil {
		return
	}
	sweep := func(sweepCtx context.Context) error {
		return manager.Reconcile(sweepCtx, time.Now().UTC(), privateArtifactReconcileLimit)
	}
	runReconcilerLoop(ctx, sweep, ticks, logger, "private ClickHouse artifact")
}

func runReconcilerLoop(ctx context.Context, sweep func(context.Context) error, ticks <-chan time.Time, logger *slog.Logger, label string) {
	if ctx == nil || sweep == nil {
		return
	}
	reconcile := func() {
		sweepCtx, cancel := context.WithTimeout(ctx, cleanupTimeout)
		defer cancel()
		if err := sweep(sweepCtx); err != nil && ctx.Err() == nil && logger != nil {
			logger.Error(label+" reconciliation failed", "error", err)
		}
	}
	for {
		if ctx.Err() != nil {
			return
		}
		reconcile()
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ticks:
			if !ok {
				return
			}
		}
	}
}
