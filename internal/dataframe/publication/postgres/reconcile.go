package postgres

// Page stale execution reconciliation (pg-publication-read-pins-reconcile-v1).
//
// Reconciler is the PostgreSQL control-plane half of the stale-execution
// reconciliation sweep: it drives the durable decisions -- which abandoned
// execution may be physically cleaned -- entirely through PostgreSQL, while
// ClickHouse remains the publication row store. The shape mirrors the
// ClickHouse bundle store's Reconcile (the generic sweep that already drives
// any PagedBundleCatalog + ExecutionReadPinCatalog) with the arbitration
// hardened for the PostgreSQL control plane:
//
//   - Stable bounded keyset scan: every sweep walks each swept state through
//     Registry.VisitExecutionPages -- the same (updated_at, id) keyset
//     continuation as the Arango baseline -- in fixed pages, never an
//     unbounded list. Swept states are RUNNING, VALIDATING, and FAILED;
//     the PUBLISHED janitor arm is enumerated ONLY when an ActiveRelease
//     reader is wired, because a janitor that cannot re-check bindings is
//     the m-po-janitor-blind shape that recreates bound-data loss.
//   - Active release/pointer bindings before ClickHouse deletion: a
//     PUBLISHED execution its pointer still names, or that the active
//     release binds, is live and skipped before any claim. After the bundle
//     lease is taken, the pointer is checked again, the cleanup claim is
//     then granted inside ONE transaction that also locks the pointer row
//     and re-vetoes a pointer that moved to this execution -- so the
//     "claim the right to delete" and the "am I now bound" facts commit
//     together under the retention anchor lock, strictly stronger than a
//     read-then-claim sequence. Immediately after that commit and before
//     the drop hook runs, the pointer and the active release are re-read one
//     final time (the post-claim destruction gate); any read failure other
//     than "no pointer" / "no active release" vetoes fail-closed.
//   - Pin arbitration: the claim runs through ReadPins, so a live reader
//     pin refuses the sweep with the exact named
//     publication.ErrExecutionReadPinActive and cleanup claim and pin
//     acquire serialize on the anchor row. The claim is heartbeated during
//     the drop phase (owner-conditional RenewExecutionCleanup on a ticker);
//     a lost heartbeat cancels the drop context so physical deletion stops
//     the moment the sweep no longer holds the right to delete.
//   - The physical deletion itself is a caller-owned, values-only hook
//     (PhysicalDropTable) invoked strictly OUTSIDE every PostgreSQL
//     transaction: this package never reads, writes, or issues DDL against
//     ClickHouse, there is no cross-database atomicity, and no backend
//     fallback. A failed drop keeps that table listed in the reconciled
//     record's remaining outputs, exactly as the ClickHouse store does, and
//     the terminal record (FAILED, retryable, "stale execution reconciled")
//     converges on retry: the drop hook and the SaveExecution write are
//     both idempotent under a re-sweep.
//   - Lease discipline: every SweepStale call mints a FRESH sweep identity
//     ("reconciler-"+uuid, ClickHouse baseline parity at
//     bundle_store.go:1082); the only way to pin one identity across calls
//     is the test-only fixIdentity seam, which production never calls.
//     This is load-bearing: an aborted sweep leaves BOTH its bundle lease
//     and its cleanup claim behind, each at the two-minute default, and
//     every later sweep is blocked until they expire -- including the next
//     sweep of the same Reconciler instance, which is skipped-leased while
//     the lease holds and, in the window after the lease lapses but before
//     the claim does, aborts the whole sweep with the named refusal under
//     first-error-wins. Recovery therefore depends on expiry, NOT on
//     identity; do not restore a once-per-instance identity as a "repair",
//     it removes the mutual exclusion between concurrent sweeps of one
//     instance (TestReconcileSweepsAreMutuallyExclusive,
//     TestReconcileMintsFreshIdentityPerSweep). The lease is never renewed
//     behind a lost fence; the terminal SaveExecution and the cleanup-claim
//     release run detached from the pre-claim deadline (each bounded,
//     baseline parity), so a slow drop phase cannot strand a live claim --
//     TestReconcileTerminalReleaseSurvivesSlowDrop guards that guarantee.
//     Lock order inside the claim transaction is pointer row, then
//     retention anchor, then pin and claim rows -- an order no other path
//     in this package cycles against (publish takes lease, execution,
//     pointer; pin transitions take only the anchor), so racing sweeps,
//     publishers, and readers serialize instead of deadlocking.
//
// First-error-wins propagation is preserved from the Arango/ClickHouse
// baseline: one refused execution aborts the sweep rather than silently
// skipping it, so an operator sees the pin that stranded the janitor.

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

// PhysicalDropTable removes one physical publication table. It is the
// caller-owned ClickHouse seam: values in, error out, executed outside every
// PostgreSQL transaction. Returning an error keeps the table in the
// reconciled record's remaining outputs so a retry re-attempts it.
type PhysicalDropTable func(ctx context.Context, physicalTable string) error

const (
	defaultReconcilePageSize    = 32
	defaultReconcileLeaseTTL    = 2 * time.Minute
	defaultCleanupHeartbeatTTL  = 2 * time.Minute
	defaultCleanupRenewal       = 30 * time.Second
	defaultReconcileCallTimeout = 10 * time.Second
)

// ReconcileOutcome names one durable decision made for one execution.
type ReconcileOutcome string

const (
	ReconcileReconciled           ReconcileOutcome = "reconciled"
	ReconcileReconciledSuperseded ReconcileOutcome = "reconciled-superseded"
	ReconcileSkippedLeased        ReconcileOutcome = "skipped-leased"
	ReconcileSkippedPointer       ReconcileOutcome = "skipped-pointer"
	ReconcileSkippedBound         ReconcileOutcome = "skipped-bound"
	ReconcileRefusedPinActive     ReconcileOutcome = "refused-pin-active"
	ReconcileRefusedCleanupOwned  ReconcileOutcome = "refused-cleanup-owned"

	// reconcileClaimGranted is the internal witness value for a committed
	// claim; it never surfaces through ReconcileDecision.
	reconcileClaimGranted ReconcileOutcome = "claim-granted"
)

// ReconcileDecision records the decision path for one execution; the
// optional OnDecision hook on Reconciler receives every decision, including
// the ones that end the sweep with an error.
type ReconcileDecision struct {
	ExecutionID string
	Outcome     ReconcileOutcome
	Err         error
}

// reconcileClaimAttempt is the in-transaction witness for the claim
// transaction, and reconcileFinalizeAttempt the seam witness between the
// drop phase and the terminal write. Both gates are test-only seams, nil in
// production; an injected error models a crash at that durability point.
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

	// reconcileBeforeClaim is a test-only seam invoked after the bundle
	// lease is taken and the pre-claim pointer check passes, immediately
	// before the claim transaction opens -- the window in which a publisher
	// can still move the pointer under the scan's nose. Production never
	// assigns it.
	reconcileBeforeClaim atomic.Pointer[func(executionID string)]
)

// Reconciler drives the stale-execution sweep through PostgreSQL.
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

	// OnDecision, when non-nil, receives every per-execution decision. It
	// is a value-only observer hook, called from the sweep goroutine.
	OnDecision func(ReconcileDecision)
}

// NewReconciler binds a sweep to a registry (scan, pointers, lease, terminal
// write), a ReadPins arbiter (pin/cleanup arbitration), and optionally an
// active-release reader. With releases nil the PUBLISHED janitor arm is never
// enumerated and binding checks are not consulted -- the same wiring rule
// the ClickHouse store enforces. The registry and the arbiter must bind the
// same database handle: the comparison is done through reflect because DB
// is an exported injection seam whose dynamic type a caller chooses, and
// interface inequality on a non-comparable dynamic type would panic here
// rather than return an error. Sweep identity is minted per SweepStale call
// (ClickHouse baseline parity, bundle_store.go:1082), which keeps the bundle
// lease and cleanup claim as the mutual-exclusion point between concurrent
// sweeps of one instance; fixIdentity pins it for tests.
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

// fixIdentity pins the sweep identity for every SweepStale call on this
// instance. It is the test seam for same-sweeper claim refresh and
// foreign-owner scenarios; production never calls it, so each sweep mints a
// fresh "reconciler-"+uuid identity and two concurrent sweeps of one
// instance exclude each other through the bundle lease and cleanup claim
// exactly as the ClickHouse baseline does by minting a per-Reconcile
// identity.
func (rc *Reconciler) fixIdentity(identity string) {
	rc.reconcilerID = identity
	rc.identityFixed = true
}

// SweepStale walks every swept state's stale executions (updated strictly
// before olderThan) in stable keyset pages and reconciles each abandoned
// execution, calling drop once per physical table. It returns the first
// failure it chooses to propagate (first-error-wins, baseline parity).
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
		// Pre-claim janitor filter: a PUBLISHED execution its pointer still
		// names, or that the active release binds, is live -- skip without
		// claiming so healthy published rows see no lease churn. This filter
		// is an optimization only; the post-claim checks below remain the
		// destruction gate. Read failures other than "no pointer"/"no active
		// release" veto fail-closed.
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
		// Baseline parity with executionCleanupLease.release: detached from
		// cancellation but bounded, so a hung release cannot outlive the
		// sweep.
		releaseCtx, cancelRelease := context.WithTimeout(context.WithoutCancel(ctx), rc.callTimeout)
		defer cancelRelease()
		return rc.registry.ReleaseBundleLease(releaseCtx, execution.Key, sweepID)
	}

	// Pre-claim pointer check: an execution its pointer already names is
	// live, and the claim transaction's pointer veto is the authority --
	// this check just avoids minting a claim for a scan-time-stale row.
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
		// Baseline parity: a pinned execution aborts the sweep rather than
		// being silently skipped, with the exact named refusal.
		err := fmt.Errorf("publication postgres: reconcile %s: %w", execution.ID, publication.ErrExecutionReadPinActive)
		rc.decide(execution, outcome, err)
		return errors.Join(err, releaseLease())
	case ReconcileRefusedCleanupOwned:
		err := fmt.Errorf("publication postgres: reconcile %s: %w", execution.ID, publication.ErrExecutionReadPinActive)
		rc.decide(execution, outcome, err)
		return errors.Join(err, releaseLease())
	case ReconcileSkippedPointer:
		// The claim transaction's in-transaction veto caught a pointer that
		// moved to this execution after the scan.
		rc.decide(execution, outcome, nil)
		return releaseLease()
	}

	releaseClaimAndLease := func() error {
		// Baseline parity with executionCleanupLease.release
		// (bundle_store.go:310): detached from cancellation AND bounded.
		// Without the bound a stalled database -- the release touches rows
		// under the retention anchor a pin acquire or foreign claim holds
		// FOR UPDATE -- wedges the janitor forever, and because errors.Join
		// evaluates eagerly the unbounded call would stop the bounded
		// releaseLease from ever running.
		claimReleaseCtx, cancelClaimRelease := context.WithTimeout(context.WithoutCancel(ctx), rc.callTimeout)
		defer cancelClaimRelease()
		return errors.Join(
			rc.pins.ReleaseExecutionCleanup(claimReleaseCtx, execution.ID, sweepID),
			releaseLease(),
		)
	}

	// Post-claim destruction gate (F-B12 drop-path): the pointer and the
	// active release are historical facts by the claim's commit instant, so
	// both are re-read immediately before any physical deletion. Any read
	// failure other than "no pointer"/"no active release" vetoes
	// fail-closed.
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

	// Drop phase under a claim heartbeated context: the heartbeat cancels
	// the drop context the instant the owner-conditional renewal says the
	// claim is no longer ours, so physical deletion stops when the right to
	// delete is lost. Failed drops keep their tables in remaining.
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
			// The injected failure models a process death between the drop
			// phase and the terminal write: no release runs, so the claim
			// stays active until its expiry and the execution keeps its
			// pre-sweep state -- exactly what a re-sweep must converge from.
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
	// The terminal record and the claim release are detached from the
	// pre-claim deadline (baseline parity with executionCleanupLease.release
	// and the boundedBundleCleanupContext releases): a drop phase that
	// consumes the pre-claim budget must not strand a live claim owned by
	// this identity, which would refuse every foreign sweep until expiry.
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

// claimCleanup grants the cleanup claim for this execution inside ONE
// transaction: the pointer row is locked first, then the retention anchor,
// and the pointer is re-vetoed under both locks -- so the claim and the
// "pointer still does not name me" fact commit together, and the guard runs
// against the newest committed pin state (pin acquire is blocked on the
// same anchor for the whole window). Lock order is documented at the top of
// this file.
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

	// 1. Lock the pointer row so a concurrent publisher's compare-and-swap
	// queues behind this decision instead of slipping a new binding under it.
	lockPointerSQL := "SELECT execution_id FROM " + rc.registry.pointers + " WHERE pointer_key = $1 FOR UPDATE"
	record(lockPointerSQL)
	var pointerHolder string
	pointerErr := tx.QueryRow(ctx, lockPointerSQL, pointerKey(execution.PointerName())).Scan(&pointerHolder)
	if pointerErr != nil && !errors.Is(pointerErr, pgx.ErrNoRows) {
		return "", fmt.Errorf("publication postgres: reconcile claim lock pointer: %w", pointerErr)
	}

	// 2. Lock the retention anchor -- the one lock point pin acquire and
	// cleanup claim share.
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

	// 3. In-transaction pointer veto: the pointer already names this
	// execution, so it is live regardless of what the scan said.
	if pointerErr == nil && pointerHolder == execution.ID {
		attempt.Outcome = ReconcileSkippedPointer
		return rc.finishClaim(ctx, tx, attempt)
	}

	// 4. Reader-pin guard: any unexpired pin refuses the sweep.
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

	// 5. Foreign-claim guard: an unexpired claim owned by someone else
	// refuses. Only a sweep carrying the identical identity refreshes it --
	// in production that never happens across calls (fresh identity per
	// SweepStale), so a refresh means the fixIdentity test seam.
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

	// 6. Grant: collect expired pins (baseline pins: [] rewrite) and (re-)
	// grant the claim under the sweeper's identity.
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

// cleanupHeartbeat owner-conditionally renews the cleanup claim on a ticker
// for the duration of the drop phase; a refused or failed renewal records
// the loss (joining ErrExecutionReadPinLost like the ClickHouse store's
// cleanup lease) and cancels the drop context so physical deletion stops
// with the lost right.
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
