package server

import (
	"context"
	"log/slog"
	"time"
)

const (
	privateArtifactReconcileInterval = 30 * time.Second
	privateArtifactReconcileLimit    = 1000
)

type privateArtifactReconcileTarget interface {
	Reconcile(context.Context, time.Time, int) error
}

func startPrivateArtifactReconciler(ctx context.Context, manager privateArtifactReconcileTarget, logger *slog.Logger) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(privateArtifactReconcileInterval)
		defer ticker.Stop()
		runPrivateArtifactReconciler(ctx, manager, ticker.C, logger)
	}()
	return done
}

// runPrivateArtifactReconciler performs an immediate sweep, then one bounded
// sweep per tick. It processes ticks serially so a slow cleanup cannot overlap
// with the next pass.
func runPrivateArtifactReconciler(ctx context.Context, manager privateArtifactReconcileTarget, ticks <-chan time.Time, logger *slog.Logger) {
	if ctx == nil || manager == nil {
		return
	}
	reconcile := func() {
		sweepCtx, cancel := context.WithTimeout(ctx, cleanupTimeout)
		defer cancel()
		if err := manager.Reconcile(sweepCtx, time.Now().UTC(), privateArtifactReconcileLimit); err != nil && ctx.Err() == nil && logger != nil {
			logger.Error("private ClickHouse artifact reconciliation failed", "error", err)
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
