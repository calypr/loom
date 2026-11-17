package postgres

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataset"
)

type PhysicalDropTable func(ctx context.Context, physicalTable string) error

const (
	defaultReconcilePageSize    = 32
	defaultReconcileLeaseTTL    = 2 * time.Minute
	defaultCleanupHeartbeatTTL  = 2 * time.Minute
	defaultCleanupRenewal       = 30 * time.Second
	defaultReconcileCallTimeout = 10 * time.Second
)

type ReconcileOutcome string

const (
	ReconcileReconciled           ReconcileOutcome = "reconciled"
	ReconcileReconciledSuperseded ReconcileOutcome = "reconciled-superseded"
	ReconcileSkippedLeased        ReconcileOutcome = "skipped-leased"
	ReconcileSkippedPointer       ReconcileOutcome = "skipped-pointer"
	ReconcileSkippedBound         ReconcileOutcome = "skipped-bound"
	ReconcileRefusedPinActive     ReconcileOutcome = "refused-pin-active"
	ReconcileRefusedCleanupOwned  ReconcileOutcome = "refused-cleanup-owned"

	reconcileClaimGranted ReconcileOutcome = "claim-granted"
)

type ReconcileDecision struct {
	ExecutionID string
	Outcome     ReconcileOutcome
	Err         error
}

type reconcileClaimAttempt struct {
	ExecutionID  string
	ReconcilerID string
	BackendPID   uint32
	Statements   []string
	Outcome      ReconcileOutcome
}

type reconcileFinalizeAttempt struct {
	ExecutionID   string
	ReconcilerID  string
	DroppedTables []string
	Remaining     []string
}

var (
	reconcileClaimGate    atomic.Pointer[func(reconcileClaimAttempt) error]
	reconcileFinalizeGate atomic.Pointer[func(reconcileFinalizeAttempt) error]

	reconcileBeforeClaim atomic.Pointer[func(executionID string)]
)

type Reconciler struct {
	registry *Registry
	pins     *ReadPins
	releases dataset.ActiveReleaseResolver

	reconcilerID  string
	identityFixed bool
	pageSize      int
	leaseTTL      time.Duration
	heartbeatTTL  time.Duration
	renewalEvery  time.Duration
	callTimeout   time.Duration

	OnDecision func(ReconcileDecision)
}

func NewReconciler(registry *Registry, pins *ReadPins, releases dataset.ActiveReleaseResolver) (*Reconciler, error) {
	if registry == nil || pins == nil {
		return nil, errors.New("publication postgres: reconciler requires a registry and a pins arbiter")
	}
	registryType, pinsType := reflect.TypeOf(registry.db), reflect.TypeOf(pins.db)
	if registryType == nil || pinsType == nil {
		return nil, errors.New("publication postgres: reconciler registry and pins require a database handle")
	}
	if registryType != pinsType {
		return nil, errors.New("publication postgres: reconciler registry and pins bind different database handles")
	}
	if !registryType.Comparable() {
		return nil, fmt.Errorf("publication postgres: reconciler database handle type %s is not comparable; bind a comparable handle (for example *pgxpool.Pool)", registryType)
	}
	if registry.db != pins.db {
		return nil, errors.New("publication postgres: reconciler registry and pins must bind the same database")
	}
	return &Reconciler{
		registry:     registry,
		pins:         pins,
		releases:     releases,
		pageSize:     defaultReconcilePageSize,
		leaseTTL:     defaultReconcileLeaseTTL,
		heartbeatTTL: defaultCleanupHeartbeatTTL,
		renewalEvery: defaultCleanupRenewal,
		callTimeout:  defaultReconcileCallTimeout,
	}, nil
}

func (rc *Reconciler) fixIdentity(identity string) {
	rc.reconcilerID = identity
	rc.identityFixed = true
}

func (rc *Reconciler) SweepStale(ctx context.Context, olderThan time.Time, drop PhysicalDropTable) error {
	if drop == nil {
		return errors.New("publication postgres: reconciler requires a physical drop hook")
	}
	sweepID := rc.reconcilerID
	if !rc.identityFixed {
		sweepID = "reconciler-" + uuid.NewString()
	}
	states := []publication.BundleState{
		publication.BundleRunning,
		publication.BundleValidating,
		publication.BundleFailed,
	}
	if rc.releases != nil {
		states = append(states, publication.BundlePublished)
	}
	for _, state := range states {
		process := func(executions []publication.BundleExecution) error {
			for _, execution := range executions {
				if err := rc.reconcileOne(ctx, execution, sweepID, drop); err != nil {
					return err
				}
			}
			return nil
		}
		if err := rc.registry.VisitExecutionPages(ctx, state, olderThan, rc.pageSize, publication.BundleExecutionPageFunc(process)); err != nil {
			return err
		}
	}
	return nil
}

func (rc *Reconciler) decide(execution publication.BundleExecution, outcome ReconcileOutcome, err error) {
	if rc.OnDecision != nil {
		rc.OnDecision(ReconcileDecision{ExecutionID: execution.ID, Outcome: outcome, Err: err})
	}
}

func (rc *Reconciler) reconcileOne(ctx context.Context, execution publication.BundleExecution, sweepID string, drop PhysicalDropTable) error {
	superseded := execution.State.Canonical() == publication.BundlePublished
	if superseded {
		pointer, pointerErr := rc.registry.GetPointer(ctx, execution.PointerName())
		if pointerErr == nil {
			if pointer.ExecutionID == execution.ID {
				rc.decide(execution, ReconcileSkippedPointer, nil)
				return nil
			}
		} else if !errors.Is(pointerErr, publication.ErrBundleNotFound) {
			rc.decide(execution, "", pointerErr)
			return pointerErr
		}
		active, activeErr := rc.releases.ReadActiveRelease(ctx, execution.Project)
		if activeErr == nil {
			if releaseBindsExecution(active, execution.ID) {
				rc.decide(execution, ReconcileSkippedBound, nil)
				return nil
			}
		} else if !errors.Is(activeErr, dataset.ErrNoActiveRelease) {
			rc.decide(execution, "", activeErr)
			return activeErr
		}
	}

	claimed, err := rc.registry.AcquireBundleLease(ctx, execution.Key, sweepID, time.Now().UTC().Add(rc.leaseTTL))
	if err != nil {
		rc.decide(execution, "", err)
		return err
	}
	if !claimed {
		rc.decide(execution, ReconcileSkippedLeased, nil)
		return nil
	}
	releaseLease := func() error {
		releaseCtx, cancelRelease := context.WithTimeout(context.WithoutCancel(ctx), rc.callTimeout)
		defer cancelRelease()
		return rc.registry.ReleaseBundleLease(releaseCtx, execution.Key, sweepID)
	}

	cleanupCtx, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), rc.callTimeout)
	defer cancelCleanup()
	pointer, pointerErr := rc.registry.GetPointer(cleanupCtx, execution.PointerName())
	if pointerErr != nil && !errors.Is(pointerErr, publication.ErrBundleNotFound) {
		return errors.Join(pointerErr, releaseLease())
	}
	if pointerErr == nil && pointer.ExecutionID == execution.ID {
		rc.decide(execution, ReconcileSkippedPointer, nil)
		return releaseLease()
	}

	if hook := reconcileBeforeClaim.Load(); hook != nil {
		(*hook)(execution.ID)
	}

	outcome, claimErr := rc.claimCleanup(ctx, execution, sweepID)
	if claimErr != nil {
		return errors.Join(claimErr, releaseLease())
	}
	switch outcome {
	case ReconcileRefusedPinActive:
		err := fmt.Errorf("publication postgres: reconcile %s: %w", execution.ID, publication.ErrExecutionReadPinActive)
		rc.decide(execution, outcome, err)
		return errors.Join(err, releaseLease())
	case ReconcileRefusedCleanupOwned:
		err := fmt.Errorf("publication postgres: reconcile %s: %w", execution.ID, publication.ErrExecutionReadPinActive)
		rc.decide(execution, outcome, err)
		return errors.Join(err, releaseLease())
	case ReconcileSkippedPointer:
		rc.decide(execution, outcome, nil)
		return releaseLease()
	}

	releaseClaimAndLease := func() error {
		claimReleaseCtx, cancelClaimRelease := context.WithTimeout(context.WithoutCancel(ctx), rc.callTimeout)
		defer cancelClaimRelease()
		return errors.Join(
			rc.pins.ReleaseExecutionCleanup(claimReleaseCtx, execution.ID, sweepID),
			releaseLease(),
		)
	}

	pointerNow, pointerNowErr := rc.registry.GetPointer(cleanupCtx, execution.PointerName())
	if pointerNowErr != nil {
		if !errors.Is(pointerNowErr, publication.ErrBundleNotFound) {
			return errors.Join(pointerNowErr, releaseClaimAndLease())
		}
	} else if pointerNow.ExecutionID == execution.ID {
		rc.decide(execution, ReconcileSkippedPointer, nil)
		return releaseClaimAndLease()
	}
	if rc.releases != nil {
		active, activeErr := rc.releases.ReadActiveRelease(cleanupCtx, execution.Project)
		if activeErr != nil {
			if !errors.Is(activeErr, dataset.ErrNoActiveRelease) {
				return errors.Join(activeErr, releaseClaimAndLease())
			}
		} else if releaseBindsExecution(active, execution.ID) {
			rc.decide(execution, ReconcileSkippedBound, nil)
			return releaseClaimAndLease()
		}
	}

	dropCtx, cancelDrop := context.WithCancel(cleanupCtx)
	heartbeat := &cleanupHeartbeat{
		pins:        rc.pins,
		ctx:         dropCtx,
		cancel:      cancelDrop,
		done:        make(chan struct{}),
		executionID: execution.ID,
		owner:       sweepID,
		ttl:         rc.heartbeatTTL,
		every:       rc.renewalEvery,
	}
	go heartbeat.run()

	var first error
	dropped := make([]string, 0, len(execution.Outputs))
	remaining := make([]publication.BundleOutputRecord, 0, len(execution.Outputs))
	for _, output := range execution.Outputs {
		if err := drop(dropCtx, output.PhysicalTable); err != nil {
			first = errors.Join(first, err)
			remaining = append(remaining, output)
			continue
		}
		dropped = append(dropped, output.PhysicalTable)
	}
	heartbeat.stop()
	first = errors.Join(first, heartbeat.renewalErr())

	if gate := reconcileFinalizeGate.Load(); gate != nil {
		attempt := reconcileFinalizeAttempt{
			ExecutionID:   execution.ID,
			ReconcilerID:  sweepID,
			DroppedTables: dropped,
			Remaining:     tableNames(remaining),
		}
		if err := (*gate)(attempt); err != nil {
			rc.decide(execution, "", err)
			return errors.Join(err, first)
		}
	}

	updated := execution
	updated.State = publication.BundleFailed
	updated.Error = "stale execution reconciled"
	if superseded {
		updated.Error = "superseded execution reconciled (no pointer or active-release reference)"
	}
	updated.FailureCode = string(dataframeerrors.CodePublicationLeaseLost)
	updated.FailureRetryable = true
	updated.UpdatedAt = time.Now().UTC()
	updated.Outputs = remaining
	updated.OwnerID = ""
	updated.LeaseExpiresAt = nil
	terminalCtx, cancelTerminal := context.WithTimeout(context.WithoutCancel(ctx), rc.callTimeout)
	defer cancelTerminal()
	first = errors.Join(first, rc.registry.SaveExecution(terminalCtx, updated, sweepID))
	first = errors.Join(first, rc.pins.ReleaseExecutionCleanup(terminalCtx, execution.ID, sweepID))
	first = errors.Join(first, releaseLease())
	if first != nil {
		rc.decide(execution, "", first)
	} else if superseded {
		rc.decide(execution, ReconcileReconciledSuperseded, nil)
	} else {
		rc.decide(execution, ReconcileReconciled, nil)
	}
	return first
}

func (rc *Reconciler) claimCleanup(ctx context.Context, execution publication.BundleExecution, sweepID string) (ReconcileOutcome, error) {
	now := time.Now().UTC()
	tx, err := rc.registry.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("publication postgres: begin reconcile claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	attempt := reconcileClaimAttempt{ExecutionID: execution.ID, ReconcilerID: sweepID}
	if conn := tx.Conn(); conn != nil {
		attempt.BackendPID = conn.PgConn().PID()
	}
	var statements []string
	record := func(sql string) {
		statements = append(statements, sql)
		attempt.Statements = statements
	}

	lockPointerSQL := "SELECT execution_id FROM " + rc.registry.pointers + " WHERE pointer_key = $1 FOR UPDATE"
	record(lockPointerSQL)
	var pointerHolder string
	pointerErr := tx.QueryRow(ctx, lockPointerSQL, pointerKey(execution.PointerName())).Scan(&pointerHolder)
	if pointerErr != nil && !errors.Is(pointerErr, pgx.ErrNoRows) {
		return "", fmt.Errorf("publication postgres: reconcile claim lock pointer: %w", pointerErr)
	}

	ensureAnchorSQL := "INSERT INTO " + rc.pins.retention + " (execution_id, updated_at) VALUES ($1, $2)" +
		" ON CONFLICT (execution_id) DO NOTHING"
	record(ensureAnchorSQL)
	if _, err := tx.Exec(ctx, ensureAnchorSQL, execution.ID, now); err != nil {
		return "", fmt.Errorf("publication postgres: reconcile claim ensure anchor: %w", err)
	}
	lockAnchorSQL := "SELECT true FROM " + rc.pins.retention + " WHERE execution_id = $1 FOR UPDATE"
	record(lockAnchorSQL)
	var held bool
	if err := tx.QueryRow(ctx, lockAnchorSQL, execution.ID).Scan(&held); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("publication postgres: reconcile claim anchor vanished for %s", execution.ID)
		}
		return "", fmt.Errorf("publication postgres: reconcile claim lock anchor: %w", err)
	}

	if pointerErr == nil && pointerHolder == execution.ID {
		attempt.Outcome = ReconcileSkippedPointer
		return rc.finishClaim(ctx, tx, attempt)
	}

	activePinSQL := "SELECT EXISTS (SELECT 1 FROM " + rc.pins.pins + " WHERE execution_id = $1 AND expires_at >= $2)"
	record(activePinSQL)
	var pinned bool
	if err := tx.QueryRow(ctx, activePinSQL, execution.ID, now).Scan(&pinned); err != nil {
		return "", fmt.Errorf("publication postgres: reconcile claim pin guard: %w", err)
	}
	if pinned {
		attempt.Outcome = ReconcileRefusedPinActive
		return rc.finishClaim(ctx, tx, attempt)
	}

	readClaimSQL := "SELECT owner_id, expires_at FROM " + rc.pins.cleanup + " WHERE execution_id = $1"
	record(readClaimSQL)
	var (
		claimOwner   string
		claimExpires time.Time
		claimFound   bool
	)
	err = tx.QueryRow(ctx, readClaimSQL, execution.ID).Scan(&claimOwner, &claimExpires)
	if err == nil {
		claimFound = true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("publication postgres: reconcile claim read claim: %w", err)
	}
	if claimFound && !claimExpires.Before(now) && claimOwner != sweepID {
		attempt.Outcome = ReconcileRefusedCleanupOwned
		return rc.finishClaim(ctx, tx, attempt)
	}

	gcSQL := "DELETE FROM " + rc.pins.pins + " WHERE execution_id = $1 AND expires_at < $2"
	record(gcSQL)
	if _, err := tx.Exec(ctx, gcSQL, execution.ID, now); err != nil {
		return "", fmt.Errorf("publication postgres: reconcile claim gc pins: %w", err)
	}
	grantSQL := "INSERT INTO " + rc.pins.cleanup + " (execution_id, owner_id, expires_at, updated_at)" +
		" VALUES ($1, $2, $3, $4)" +
		" ON CONFLICT (execution_id) DO UPDATE SET owner_id = $2, expires_at = $3, updated_at = $4"
	record(grantSQL)
	if _, err := tx.Exec(ctx, grantSQL, execution.ID, sweepID, now.Add(rc.heartbeatTTL), now); err != nil {
		return "", fmt.Errorf("publication postgres: reconcile claim grant: %w", err)
	}
	touchSQL := "UPDATE " + rc.pins.retention + " SET updated_at = $2 WHERE execution_id = $1"
	record(touchSQL)
	if _, err := tx.Exec(ctx, touchSQL, execution.ID, now); err != nil {
		return "", fmt.Errorf("publication postgres: reconcile claim touch anchor: %w", err)
	}
	attempt.Outcome = reconcileClaimGranted
	return rc.finishClaim(ctx, tx, attempt)
}

func (rc *Reconciler) finishClaim(ctx context.Context, tx pgx.Tx, attempt reconcileClaimAttempt) (ReconcileOutcome, error) {
	if gate := reconcileClaimGate.Load(); gate != nil {
		if err := (*gate)(attempt); err != nil {
			return "", err
		}
	}
	if attempt.Outcome != reconcileClaimGranted {
		if err := tx.Rollback(ctx); err != nil {
			return "", fmt.Errorf("publication postgres: reconcile claim rollback refused decision: %w", err)
		}
		return attempt.Outcome, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("publication postgres: reconcile claim commit: %w", err)
	}
	return attempt.Outcome, nil
}

func releaseBindsExecution(active dataset.ActiveRelease, executionID string) bool {
	for _, binding := range active.Release.Publications {
		if binding.ExecutionID == executionID {
			return true
		}
	}
	return false
}

func tableNames(records []publication.BundleOutputRecord) []string {
	out := make([]string, 0, len(records))
	for _, record := range records {
		out = append(out, record.PhysicalTable)
	}
	return out
}

type cleanupHeartbeat struct {
	pins        *ReadPins
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	executionID string
	owner       string
	ttl         time.Duration
	every       time.Duration

	lost atomic.Bool
	err  atomic.Pointer[error]
}

func (h *cleanupHeartbeat) run() {
	defer close(h.done)
	if h.every <= 0 {
		return
	}
	ticker := time.NewTicker(h.every)
	defer ticker.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			renewCtx, cancel := context.WithTimeout(context.WithoutCancel(h.ctx), defaultReconcileCallTimeout)
			owned, err := h.pins.RenewExecutionCleanup(renewCtx, h.executionID, h.owner, time.Now().UTC().Add(h.ttl))
			cancel()
			if err != nil || !owned {
				if err == nil {
					err = publication.ErrExecutionReadPinLost
				}
				h.err.Store(&err)
				h.lost.Store(true)
				h.cancel()
				return
			}
		}
	}
}

func (h *cleanupHeartbeat) stop() {
	if h.every <= 0 {
		<-h.done
		return
	}
	h.cancel()
	<-h.done
}

func (h *cleanupHeartbeat) renewalErr() error {
	if p := h.err.Load(); p != nil {
		return errors.Join(publication.ErrExecutionReadPinLost, *p)
	}
	return nil
}
