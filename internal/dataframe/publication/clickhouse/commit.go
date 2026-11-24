package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/store/clickhouse"
)

func (t *clickHouseBundleTx) Commit(ctx context.Context) ([]publication.PublishedOutput, error) {
	if t.idempotent {
		t.closed = true
		return t.ExistingPublishedOutputs(), nil
	}
	if t.closed {
		return nil, fmt.Errorf("bundle transaction is closed")
	}
	if t.checkpointUncertain {
		return nil, ErrBundleCheckpointUncertain
	}
	if err := t.ensureLease(); err != nil {
		return nil, err
	}
	if len(t.execution.Outputs) == 0 {
		return nil, t.fail(ctx, fmt.Errorf("bundle has no outputs"))
	}
	if err := publication.ValidateQualityReports(t.execution.BundleIdentity, t.execution.Outputs, t.execution.QualityReports); err != nil {
		return nil, t.failPhase(ctx, "QUALITY", "", err)
	}
	t.execution.State = publication.BundleValidating
	for i := range t.execution.Outputs {
		t.execution.Outputs[i].State = publication.BundleValidating
	}
	if err := t.save(ctx); err != nil {
		return nil, err
	}
	for i := range t.execution.Outputs {
		output := &t.execution.Outputs[i]
		if output.RowCount < 0 {
			return nil, t.failPhase(ctx, "VERIFY_OUTPUT", output.Name, fmt.Errorf("output %q has invalid row count", output.Name))
		}
		columns := make([]clickhouse.Column, len(output.Columns))
		for columnIndex, column := range output.Columns {
			columns[columnIndex] = clickhouse.Column{Name: column.Name, Type: column.ClickHouse}
		}
		if err := t.store.clickHouse.VerifyOutput(ctx, output.PhysicalTable, columns, output.RowCount); err != nil {
			return nil, t.failPhase(ctx, "VERIFY_OUTPUT", output.Name, dataframeerrors.Wrap(err, dataframeerrors.CodePublicationFailed, "", dataframeerrors.WithRetryable(true)))
		}
		verified := time.Now().UTC()
		output.VerifiedAt = &verified
	}
	now := time.Now().UTC()
	t.execution.State, t.execution.PublishedAt, t.execution.UpdatedAt = publication.BundlePublished, &now, now
	for i := range t.execution.Outputs {
		t.execution.Outputs[i].State = publication.BundlePublished
	}
	if err := t.store.catalog.PublishExecution(ctx, t.execution.PointerName(), t.expectedPointer, t.execution); err != nil {
		if errors.Is(err, publication.ErrBundlePointerConflict) {
			err = dataframeerrors.Wrap(err, dataframeerrors.CodePublicationConflict, "", dataframeerrors.WithRetryable(true))
			return nil, t.failPhase(ctx, "COMMIT_POINTER", "", fmt.Errorf("publish bundle pointer: %w", err))
		}
		committed, conclusive, confirmErr := t.confirmCommit(ctx)
		if committed {
			t.closed = true
			return t.publishedOutputs(), t.stopLease()
		}
		if !conclusive {
			t.closed = true
			uncertain := dataframeerrors.Wrap(errors.Join(ErrBundleCommitUncertain, err, confirmErr), dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
			return nil, publication.WithPhase(errors.Join(uncertain, t.stopLease()), "COMMIT_POINTER", "")
		}
		err = dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
		return nil, t.failPhase(ctx, "COMMIT_POINTER", "", fmt.Errorf("publish bundle pointer: %w", err))
	}
	t.closed = true
	return t.publishedOutputs(), t.stopLease()
}

func (t *clickHouseBundleTx) publishedOutputs() []publication.PublishedOutput {
	result := make([]publication.PublishedOutput, 0, len(t.execution.Outputs))
	for _, output := range t.execution.Outputs {
		result = append(result, publication.PublishedOutput{Name: output.Name, PhysicalName: output.PhysicalTable, RowCount: output.RowCount, ByteCount: output.ByteCount})
	}
	return result
}

func (t *clickHouseBundleTx) confirmCommit(ctx context.Context) (committed, conclusive bool, err error) {
	execution, executionErr := t.store.catalog.GetExecution(ctx, t.execution.ID)
	pointer, pointerErr := t.store.catalog.GetPointer(ctx, t.execution.PointerName())
	if errors.Is(pointerErr, publication.ErrBundleNotFound) {
		pointerErr = nil
		pointer = publication.BundlePointer{Name: t.execution.PointerName()}
	}
	if executionErr != nil || pointerErr != nil {
		return false, false, errors.Join(executionErr, pointerErr)
	}
	if execution.State.Successful() && pointer.ExecutionID == execution.ID && allOutputsQueryable(execution.Outputs) {
		t.execution = execution
		return true, true, nil
	}
	if !execution.State.Successful() && pointer.ExecutionID != execution.ID {
		return false, true, nil
	}
	return false, false, fmt.Errorf("publication metadata is inconsistent: execution state %q, pointer %q", execution.State, pointer.ExecutionID)
}

func (t *clickHouseBundleTx) Abort(ctx context.Context, cause error) error {
	if t.idempotent || t.closed {
		return nil
	}
	if err := t.ensureLease(); err != nil {
		t.closed = true
		return errors.Join(err, t.stopLease())
	}
	cleanupCtx, cancel := boundedBundleCleanupContext(ctx)
	defer cancel()
	leaseCtx, releaseCleanup, claimErr := t.store.claimExecutionCleanup(cleanupCtx, t.execution.ID, t.execution.OwnerID)
	if claimErr != nil {
		t.closed = true
		return errors.Join(claimErr, t.stopLease())
	}
	defer func() { _ = releaseCleanup() }()

	if pointerNow, pointerNowErr := t.store.catalog.GetPointer(cleanupCtx, t.execution.PointerName()); pointerNowErr != nil {
		if !errors.Is(pointerNowErr, publication.ErrBundleNotFound) {
			t.closed = true
			return errors.Join(pointerNowErr, releaseCleanup(), t.stopLease())
		}
	} else if pointerNow.ExecutionID == t.execution.ID {
		return t.stopLease()
	}
	if t.store.activeReleases != nil {
		active, activeErr := t.store.activeReleases.ReadActiveRelease(cleanupCtx, t.execution.Project)
		if activeErr != nil {
			if !errors.Is(activeErr, dataset.ErrNoActiveRelease) {
				t.closed = true
				return errors.Join(activeErr, releaseCleanup(), t.stopLease())
			}
		} else {
			for _, binding := range active.Release.Publications {
				if binding.ExecutionID == t.execution.ID {
					return t.stopLease()
				}
			}
		}
	}
	var cleanup error
	for _, output := range t.execution.Outputs {
		if err := t.store.clickHouse.DropTable(leaseCtx, output.PhysicalTable); err != nil {
			cleanup = errors.Join(cleanup, err)
		}
	}
	if t.checkpointUncertain {
		for index := range t.execution.Outputs {
			t.execution.Outputs[index].RowCount = 0
			t.execution.Outputs[index].ByteCount = 0
			t.execution.Outputs[index].VerifiedAt = nil
		}
	}
	if t.execution.State != publication.BundleFailed {
		if err := t.fail(leaseCtx, cause); err != nil {
			cleanup = errors.Join(cleanup, err)
		}
	}
	t.closed = true
	return errors.Join(cleanup, t.stopLease())
}

func (t *clickHouseBundleTx) outputIndex(name string) int {
	for i := range t.execution.Outputs {
		if t.execution.Outputs[i].Name == name {
			return i
		}
	}
	return -1
}

func (t *clickHouseBundleTx) tableName(output string) string {
	return t.store.prefix + "_" + strings.ReplaceAll(t.execution.ID, "-", "") + "_" + output
}

func (t *clickHouseBundleTx) save(ctx context.Context) error {
	if err := t.ensureLease(); err != nil {
		return err
	}
	t.leaseMu.Lock()
	t.execution.UpdatedAt = time.Now().UTC()
	snapshot := t.execution
	if t.execution.LeaseExpiresAt != nil {
		expires := *t.execution.LeaseExpiresAt
		snapshot.LeaseExpiresAt = &expires
	}
	t.leaseMu.Unlock()
	if err := t.store.catalog.SaveExecution(ctx, snapshot, snapshot.OwnerID); err != nil {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	return nil
}

func (t *clickHouseBundleTx) fail(ctx context.Context, err error) error {
	return t.failPhase(ctx, "PUBLICATION", "", err)
}

func (t *clickHouseBundleTx) failPhase(ctx context.Context, phase, output string, err error) error {
	slog.Error("dataframe bundle publication failed", "phase", phase, "output", output, "error", err)
	normalized := dataframeerrors.Normalize(err)
	t.execution.State, t.execution.Error = publication.BundleFailed, err.Error()
	t.execution.PublishedAt = nil
	t.execution.FailureCode, t.execution.FailureRetryable = normalized.Code(), normalized.Retryable()
	t.execution.FailurePhase, t.execution.FailureOutput, t.execution.FailureDetails = phase, output, err.Error()
	for i := range t.execution.Outputs {
		t.execution.Outputs[i].State = publication.BundleFailed
		t.execution.Outputs[i].FailureCode = normalized.Code()
		t.execution.Outputs[i].FailureRetryable = normalized.Retryable()
		t.execution.Outputs[i].FailurePhase = phase
		t.execution.Outputs[i].FailureDetails = err.Error()
	}
	if persistenceErr := t.save(ctx); persistenceErr != nil {
		return errors.Join(err, persistenceErr)
	}
	return err
}
