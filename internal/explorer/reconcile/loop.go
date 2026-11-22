package reconcile

import (
	"context"
	"log/slog"
	"time"
)

const sweepTimeout = 10 * time.Second

func Loop(ctx context.Context, sweep func(context.Context) error, ticks <-chan time.Time, logger *slog.Logger, label string) {
	if ctx == nil || sweep == nil {
		return
	}
	run := func() {
		sweepCtx, cancel := context.WithTimeout(ctx, sweepTimeout)
		defer cancel()
		if err := sweep(sweepCtx); err != nil && ctx.Err() == nil && logger != nil {
			logger.Error(label+" reconciliation failed", "error", err)
		}
	}
	for {
		if ctx.Err() != nil {
			return
		}
		run()
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
