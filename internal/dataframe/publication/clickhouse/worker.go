package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/google/uuid"
)

type ExecutionProcessor func(context.Context, publication.BundleExecution, publication.Target) error

type WorkerConfig struct {
	Lease       time.Duration
	MaxAttempts int
	RetryDelay  time.Duration
	WorkerID    string
}

// Worker leases durable QUEUED commands and retries only typed retryable
// failures. The execution is saved before the processor performs recipe or
// schema validation.
type Worker struct {
	store       *ClickHouseBundleStore
	processor   ExecutionProcessor
	maxAttempts int
	retryDelay  time.Duration
	workerID    string
}

// Run continuously drains durable work until ctx is canceled. Individual
// execution failures are reported and do not stop unrelated commands.
func (w *Worker) Run(ctx context.Context, pollInterval time.Duration, report func(error)) error {
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if err := w.RunOnce(ctx); err != nil && report != nil {
			report(err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func NewWorker(store *ClickHouseBundleStore, processor ExecutionProcessor, cfg WorkerConfig) (*Worker, error) {
	if store == nil || processor == nil {
		return nil, fmt.Errorf("publication worker requires store and processor")
	}
	if cfg.Lease > 0 {
		store.leaseTTL = cfg.Lease
		store.leaseRenewInterval = cfg.Lease / 3
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = time.Second
	}
	if strings.TrimSpace(cfg.WorkerID) == "" {
		cfg.WorkerID = "publication-worker-" + uuid.NewString()
	}
	return &Worker{store: store, processor: processor, maxAttempts: cfg.MaxAttempts, retryDelay: cfg.RetryDelay, workerID: cfg.WorkerID}, nil
}

// Enqueue is idempotent by the complete BundleIdentity, which includes the
// exact recipe version and generation. It performs no recipe/schema checks.
func (w *Worker) Enqueue(ctx context.Context, identity publication.BundleIdentity) (publication.BundleExecution, error) {
	if strings.TrimSpace(identity.Name) == "" || strings.TrimSpace(identity.TranslationVersion) == "" {
		return publication.BundleExecution{}, fmt.Errorf("recipe name and translation version are required")
	}
	key := identity.Key()
	if existing, err := w.store.catalog.FindExecutionByKey(ctx, key); err == nil {
		return existing.CanonicalizeLegacy(), nil
	} else if !errors.Is(err, publication.ErrBundleNotFound) {
		return publication.BundleExecution{}, err
	}
	owner := "enqueue-" + uuid.NewString()
	expires := time.Now().UTC().Add(w.store.leaseTTL)
	claimed, err := w.store.catalog.AcquireBundleLease(ctx, key, owner, expires)
	if err != nil {
		return publication.BundleExecution{}, err
	}
	if !claimed {
		return publication.BundleExecution{}, dataframeerrors.NewError(dataframeerrors.CodePublicationInProgress, "", dataframeerrors.WithRetryable(true))
	}
	defer func() { _ = w.store.catalog.ReleaseBundleLease(context.WithoutCancel(ctx), key, owner) }()
	if existing, err := w.store.catalog.FindExecutionByKey(ctx, key); err == nil {
		return existing.CanonicalizeLegacy(), nil
	} else if !errors.Is(err, publication.ErrBundleNotFound) {
		return publication.BundleExecution{}, err
	}
	now := time.Now().UTC()
	idValue, err := uuid.NewV7()
	if err != nil {
		return publication.BundleExecution{}, fmt.Errorf("create publication UUIDv7: %w", err)
	}
	execution := publication.BundleExecution{
		ID: idValue.String(), Key: key, BundleIdentity: identity, State: publication.BundleQueued,
		CreatedAt: now, UpdatedAt: now, MaxAttempts: w.maxAttempts,
	}
	if err := w.store.catalog.SaveExecution(ctx, execution); err != nil {
		return publication.BundleExecution{}, err
	}
	return execution, nil
}

// RunOnce recovers expired work and drains commands that are currently due.
func (w *Worker) RunOnce(ctx context.Context) error {
	now := time.Now().UTC()
	if err := w.recoverExpired(ctx, now); err != nil {
		return err
	}
	queued, err := w.store.catalog.ListExecutions(ctx, publication.BundleQueued, now.Add(time.Nanosecond))
	if err != nil {
		return err
	}
	var joined error
	for _, execution := range queued {
		if execution.NextAttemptAt != nil && execution.NextAttemptAt.After(now) {
			continue
		}
		if err := w.runExecution(ctx, execution); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func (w *Worker) runExecution(ctx context.Context, execution publication.BundleExecution) error {
	owner := w.workerID + "-" + uuid.NewString()
	expires := time.Now().UTC().Add(w.store.leaseTTL)
	claimed, err := w.store.catalog.AcquireBundleLease(ctx, execution.Key, owner, expires)
	if err != nil || !claimed {
		return err
	}
	execution.OwnerID, execution.LeaseExpiresAt = owner, &expires
	execution.State = publication.BundleRunning
	execution.Attempt++
	execution.UpdatedAt = time.Now().UTC()
	execution.NextAttemptAt = nil
	if len(execution.Outputs) != 0 {
		remaining, cleanupErr := w.dropOutputs(ctx, execution.Outputs)
		execution.Outputs = remaining
		if cleanupErr != nil {
			if saveErr := w.store.catalog.SaveExecution(ctx, execution); saveErr != nil {
				cleanupErr = errors.Join(cleanupErr, saveErr)
			}
			return w.recordFailure(ctx, execution.ID, owner, publication.WithPhase(cleanupErr, "CLEANUP", ""))
		}
	}
	if err := w.store.catalog.SaveExecution(ctx, execution); err != nil {
		_ = w.store.catalog.ReleaseBundleLease(context.WithoutCancel(ctx), execution.Key, owner)
		return err
	}
	target, err := NewClaimed(w.store, execution.ID, owner)
	if err == nil {
		err = w.processor(ctx, execution, target)
	}
	if err == nil {
		published, loadErr := w.store.catalog.GetExecution(ctx, execution.ID)
		if loadErr != nil {
			err = loadErr
		} else if !published.State.Successful() || !allOutputsQueryable(published.Outputs) {
			err = dataframeerrors.NewError(dataframeerrors.CodePublicationFailed, "", dataframeerrors.WithRetryable(true))
		}
	}
	if err != nil {
		if errors.Is(err, ErrBundleCommitUncertain) {
			releaseErr := w.store.catalog.ReleaseBundleLease(context.WithoutCancel(ctx), execution.Key, owner)
			return errors.Join(err, releaseErr)
		}
		return w.recordFailure(ctx, execution.ID, owner, err)
	}
	return w.store.catalog.ReleaseBundleLease(context.WithoutCancel(ctx), execution.Key, owner)
}

func allOutputsQueryable(outputs []publication.BundleOutputRecord) bool {
	if len(outputs) == 0 {
		return false
	}
	for _, output := range outputs {
		if !output.Queryable() {
			return false
		}
	}
	return true
}

func (w *Worker) recordFailure(ctx context.Context, executionID, owner string, cause error) error {
	cleanupCtx, cancel := boundedBundleCleanupContext(ctx)
	defer cancel()
	execution, loadErr := w.store.catalog.GetExecution(cleanupCtx, executionID)
	if loadErr != nil {
		return errors.Join(cause, loadErr)
	}
	remaining, cleanupErr := w.dropOutputs(cleanupCtx, execution.Outputs)
	if cleanupErr != nil {
		cleanupErr = dataframeerrors.Wrap(cleanupErr, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
		cause = publication.WithPhase(errors.Join(cleanupErr, cause), "CLEANUP", "")
	}
	normalized := dataframeerrors.Normalize(cause)
	execution.State = publication.BundleFailed
	execution.FailureCode = normalized.Code()
	execution.FailureRetryable = normalized.Retryable()
	execution.Error = cause.Error()
	execution.FailureDetails = cause.Error()
	var phase *publication.PhaseError
	if errors.As(cause, &phase) {
		execution.FailurePhase, execution.FailureOutput = phase.Phase, phase.Output
	}
	execution.Outputs = remaining
	execution.OwnerID, execution.LeaseExpiresAt = "", nil
	execution.UpdatedAt = time.Now().UTC()
	if normalized.Retryable() && execution.Attempt < execution.MaxAttempts {
		execution.State = publication.BundleQueued
		next := execution.UpdatedAt.Add(w.retryDelay * time.Duration(execution.Attempt))
		execution.NextAttemptAt = &next
	}
	saveErr := w.store.catalog.SaveExecution(cleanupCtx, execution)
	releaseErr := w.store.catalog.ReleaseBundleLease(cleanupCtx, execution.Key, owner)
	return errors.Join(cause, saveErr, releaseErr)
}

func (w *Worker) recoverExpired(ctx context.Context, now time.Time) error {
	for _, state := range []publication.BundleState{publication.BundleRunning, publication.BundleValidating} {
		executions, err := w.store.catalog.ListExecutions(ctx, state, now)
		if err != nil {
			return err
		}
		for _, execution := range executions {
			if execution.LeaseExpiresAt != nil && execution.LeaseExpiresAt.After(now) {
				continue
			}
			owner := w.workerID + "-recovery-" + uuid.NewString()
			expires := now.Add(w.store.leaseTTL)
			claimed, err := w.store.catalog.AcquireBundleLease(ctx, execution.Key, owner, expires)
			if err != nil {
				return err
			}
			if !claimed {
				continue
			}
			execution.OwnerID = owner
			if err := w.store.catalog.SaveExecution(ctx, execution); err != nil {
				return err
			}
			recovery := dataframeerrors.NewError(dataframeerrors.CodePublicationLeaseLost, "", dataframeerrors.WithRetryable(true))
			if err := w.recordFailure(ctx, execution.ID, owner, publication.WithPhase(recovery, "RECOVER_LEASE", "")); err != nil && execution.Attempt >= execution.MaxAttempts {
				return err
			}
		}
	}
	return nil
}

func (w *Worker) dropOutputs(ctx context.Context, outputs []publication.BundleOutputRecord) ([]publication.BundleOutputRecord, error) {
	remaining := make([]publication.BundleOutputRecord, 0, len(outputs))
	var joined error
	for _, output := range outputs {
		if strings.TrimSpace(output.PhysicalTable) == "" {
			continue
		}
		if err := w.store.clickHouse.DropTable(ctx, output.PhysicalTable); err != nil {
			remaining = append(remaining, output)
			joined = errors.Join(joined, fmt.Errorf("drop staging table %q: %w", output.PhysicalTable, err))
		}
	}
	return remaining, joined
}
