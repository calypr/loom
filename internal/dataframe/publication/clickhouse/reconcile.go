package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"time"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataset"
	"github.com/google/uuid"
)

func (s *ClickHouseBundleStore) Reconcile(ctx context.Context, olderThan time.Time) error {
	reconcilerID := "reconciler-" + uuid.NewString()
	process := func(executions []publication.BundleExecution) error {
		for _, execution := range executions {
			if err := s.reconcileExecution(ctx, execution, reconcilerID); err != nil {
				return err
			}
		}
		return nil
	}
	states := []publication.BundleState{publication.BundleRunning, publication.BundleValidating, publication.BundleFailed}

	if s.activeReleases != nil {
		states = append(states, publication.BundlePublished)
	}
	paged, ok := s.catalog.(publication.PagedBundleCatalog)
	if !ok {
		return fmt.Errorf("bundle catalog does not support paged reconciliation")
	}
	for _, state := range states {
		if err := paged.VisitExecutionPages(ctx, state, olderThan, s.reconcilePageSize, publication.BundleExecutionPageFunc(process)); err != nil {
			return err
		}
	}
	return nil
}

func (s *ClickHouseBundleStore) reconcileExecution(ctx context.Context, execution publication.BundleExecution, reconcilerID string) error {
	superseded := execution.State.Canonical() == publication.BundlePublished
	if superseded {

		if s.activeReleases == nil {
			return nil
		}
		pointer, pointerErr := s.catalog.GetPointer(ctx, execution.PointerName())
		if pointerErr == nil {
			if pointer.ExecutionID == execution.ID {
				return nil
			}
		} else if !errors.Is(pointerErr, publication.ErrBundleNotFound) {
			return pointerErr
		}
		active, activeErr := s.activeReleases.ReadActiveRelease(ctx, execution.Project)
		if activeErr == nil {
			for _, binding := range active.Release.Publications {
				if binding.ExecutionID == execution.ID {
					return nil
				}
			}
		} else if !errors.Is(activeErr, dataset.ErrNoActiveRelease) {
			return activeErr
		}
	}
	expires := time.Now().UTC().Add(s.leaseTTL)
	claimed, err := s.catalog.AcquireBundleLease(ctx, execution.Key, reconcilerID, expires)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	execution.OwnerID = reconcilerID
	cleanupCtx, cancel := boundedBundleCleanupContext(ctx)
	defer cancel()
	pointer, pointerErr := s.catalog.GetPointer(cleanupCtx, execution.PointerName())
	if pointerErr != nil && !errors.Is(pointerErr, publication.ErrBundleNotFound) {
		releaseErr := s.catalog.ReleaseBundleLease(cleanupCtx, execution.Key, reconcilerID)
		return errors.Join(pointerErr, releaseErr)
	}
	if pointerErr == nil && pointer.ExecutionID == execution.ID {
		return s.catalog.ReleaseBundleLease(cleanupCtx, execution.Key, reconcilerID)
	}
	leaseCtx, releaseCleanup, claimErr := s.claimExecutionCleanup(cleanupCtx, execution.ID, reconcilerID)
	if claimErr != nil {
		releaseErr := s.catalog.ReleaseBundleLease(cleanupCtx, execution.Key, reconcilerID)
		if errors.Is(claimErr, publication.ErrExecutionReadPinActive) {
			return releaseErr
		}
		return errors.Join(claimErr, releaseErr)
	}
	defer func() { _ = releaseCleanup() }()

	if pointerNow, pointerNowErr := s.catalog.GetPointer(cleanupCtx, execution.PointerName()); pointerNowErr != nil {
		if !errors.Is(pointerNowErr, publication.ErrBundleNotFound) {
			return errors.Join(pointerNowErr, s.catalog.ReleaseBundleLease(cleanupCtx, execution.Key, reconcilerID))
		}
	} else if pointerNow.ExecutionID == execution.ID {
		return s.catalog.ReleaseBundleLease(cleanupCtx, execution.Key, reconcilerID)
	}
	if s.activeReleases != nil {
		active, activeErr := s.activeReleases.ReadActiveRelease(cleanupCtx, execution.Project)
		if activeErr != nil {
			if !errors.Is(activeErr, dataset.ErrNoActiveRelease) {
				return errors.Join(activeErr, s.catalog.ReleaseBundleLease(cleanupCtx, execution.Key, reconcilerID))
			}
		} else {
			for _, binding := range active.Release.Publications {
				if binding.ExecutionID == execution.ID {
					return s.catalog.ReleaseBundleLease(cleanupCtx, execution.Key, reconcilerID)
				}
			}
		}
	}
	var first error
	remaining := make([]publication.BundleOutputRecord, 0, len(execution.Outputs))
	for _, output := range execution.Outputs {
		if err := s.clickHouse.DropTable(leaseCtx, output.PhysicalTable); err != nil {
			first = errors.Join(first, err)
			remaining = append(remaining, output)
		}
	}
	execution.State = publication.BundleFailed
	execution.Error = "stale execution reconciled"
	if superseded {
		execution.Error = "superseded execution reconciled (no pointer or active-release reference)"
	}
	execution.FailureCode = string(dataframeerrors.CodePublicationLeaseLost)
	execution.FailureRetryable = true
	execution.UpdatedAt = time.Now().UTC()
	execution.Outputs = remaining
	execution.OwnerID = ""
	execution.LeaseExpiresAt = nil
	if err := s.catalog.SaveExecution(leaseCtx, execution, reconcilerID); err != nil {
		first = errors.Join(first, err)
	}
	first = errors.Join(first, s.catalog.ReleaseBundleLease(cleanupCtx, execution.Key, reconcilerID))
	return first
}
