package clickhouse

import (
	"context"
	"errors"

	"time"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
)

func (s *ClickHouseBundleStore) claimExecutionCleanup(ctx context.Context, executionID, owner string) (context.Context, func() error, error) {
	pins, ok := s.catalog.(publication.ExecutionReadPinCatalog)
	if !ok {
		return ctx, func() error { return nil }, nil
	}
	claimed, err := pins.ClaimExecutionCleanup(ctx, executionID, owner)
	if err != nil {
		return nil, nil, err
	}
	if !claimed {
		return nil, nil, publication.ErrExecutionReadPinActive
	}
	leaseCtx, cancel := context.WithCancel(ctx)
	lease := &executionCleanupLease{ctx: leaseCtx, cancel: cancel, done: make(chan struct{}), pins: pins, executionID: executionID, owner: owner}
	go lease.run()
	return leaseCtx, lease.release, nil
}

func (t *clickHouseBundleTx) startLeaseRenewal(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	t.leaseCancel, t.leaseDone = cancel, make(chan struct{})
	go func() {
		defer close(t.leaseDone)
		interval := t.store.leaseRenewInterval
		if interval <= 0 {
			interval = 30 * time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				until := time.Now().UTC().Add(t.store.leaseTTL)
				acquired, err := t.store.catalog.RenewBundleLease(ctx, t.execution.Key, t.execution.OwnerID, until)
				t.leaseMu.Lock()
				if err != nil || !acquired {
					t.leaseLost = true
					if err != nil {
						t.leaseErr = err
					} else {
						t.leaseErr = ErrBundleLeaseLost
					}
					t.leaseMu.Unlock()
					return
				}
				t.execution.LeaseExpiresAt = &until
				t.leaseMu.Unlock()
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (t *clickHouseBundleTx) ensureLease() error {
	t.leaseMu.Lock()
	defer t.leaseMu.Unlock()
	if t.leaseLost {
		if t.leaseErr != nil {
			return dataframeerrors.Wrap(errors.Join(ErrBundleLeaseLost, t.leaseErr), dataframeerrors.CodePublicationLeaseLost, "", dataframeerrors.WithRetryable(true))
		}
		return dataframeerrors.NewError(dataframeerrors.CodePublicationLeaseLost, "", dataframeerrors.WithRetryable(true), dataframeerrors.WithCause(ErrBundleLeaseLost))
	}
	if t.execution.OwnerID == "" {
		return nil
	}
	if expires := t.execution.LeaseExpiresAt; expires != nil && !time.Now().UTC().Before(*expires) {
		t.leaseLost = true
		return dataframeerrors.NewError(dataframeerrors.CodePublicationLeaseLost, "", dataframeerrors.WithRetryable(true), dataframeerrors.WithCause(ErrBundleLeaseLost))
	}
	return nil
}

func (t *clickHouseBundleTx) stopLease() error {
	t.leaseStopOnce.Do(func() {
		if t.leaseCancel != nil {
			t.leaseCancel()
			timer := time.NewTimer(bundleCleanupTimeout)
			select {
			case <-t.leaseDone:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			case <-timer.C:
				t.leaseStopErr = context.DeadlineExceeded
				return
			}
		}
		if t.execution.OwnerID != "" {
			cleanupCtx, cancel := boundedBundleCleanupContext(context.Background())
			defer cancel()
			t.leaseStopErr = t.store.releaseLease(cleanupCtx, t.execution.Key, t.execution.OwnerID)
		}
	})
	return t.leaseStopErr
}
