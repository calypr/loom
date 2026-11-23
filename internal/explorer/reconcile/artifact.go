package reconcile

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

func StartPrivateArtifactReconciler(ctx context.Context, manager privateArtifactReconcileTarget, logger *slog.Logger) <-chan struct{} {
	sweep := func(sweepCtx context.Context) error {
		return manager.Reconcile(sweepCtx, time.Now().UTC(), privateArtifactReconcileLimit)
	}
	return startTickerReconciler(ctx, sweep, logger, privateArtifactReconcileInterval, "private ClickHouse artifact")
}

func StartArtifactFSReconciler(ctx context.Context, store artifactFSReconcileTarget, logger *slog.Logger) <-chan struct{} {
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
		Loop(ctx, sweep, ticker.C, logger, label)
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
	Loop(ctx, sweep, ticks, logger, "private ClickHouse artifact")
}
