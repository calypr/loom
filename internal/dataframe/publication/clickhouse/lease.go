package clickhouse

import (
	"context"
	"errors"
	"github.com/calypr/loom/internal/dataframe/publication"
	"sync"
	"sync/atomic"
	"time"
)

const (
	cleanupLeaseTTL      = 2 * time.Minute
	cleanupLeaseRenewal  = 30 * time.Second
	cleanupLeaseCallTime = 5 * time.Second
)

type executionCleanupLease struct {
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	pins        publication.ExecutionReadPinCatalog
	executionID string
	owner       string
	lost        atomic.Bool
	errMu       sync.Mutex
	renewErr    error
	stopOnce    sync.Once
	stopErr     error
}

func (l *executionCleanupLease) run() {
	defer close(l.done)
	ticker := time.NewTicker(cleanupLeaseRenewal)
	defer ticker.Stop()
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-ticker.C:
			renewCtx, cancel := context.WithTimeout(context.WithoutCancel(l.ctx), cleanupLeaseCallTime)
			owned, err := l.pins.RenewExecutionCleanup(renewCtx, l.executionID, l.owner, time.Now().UTC().Add(cleanupLeaseTTL))
			cancel()
			if err != nil || !owned {
				l.errMu.Lock()
				if err != nil {
					l.renewErr = err
				} else {
					l.renewErr = publication.ErrExecutionReadPinLost
				}
				l.errMu.Unlock()
				l.lost.Store(true)
				l.cancel()
				return
			}
		}
	}
}

func (l *executionCleanupLease) release() error {
	l.stopOnce.Do(func() {
		l.cancel()
		<-l.done
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(l.ctx), bundleCleanupTimeout)
		defer cancel()
		l.stopErr = l.pins.ReleaseExecutionCleanup(releaseCtx, l.executionID, l.owner)
		if l.lost.Load() {
			l.errMu.Lock()
			renewErr := l.renewErr
			l.errMu.Unlock()
			l.stopErr = errors.Join(l.stopErr, publication.ErrExecutionReadPinLost, renewErr)
		}
	})
	return l.stopErr
}

const bundleCleanupTimeout = 10 * time.Second

func boundedBundleCleanupContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), bundleCleanupTimeout)
}
