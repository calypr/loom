package postgres

// Read retention pins and cleanup claims (pg-publication-read-pins-reconcile-v1).
//
// ReadPins is the PostgreSQL control-plane arbiter between readers who pin an
// exact execution's physical tables during exact export / row streaming and
// the reconciler who claims an execution for physical cleanup. It implements
// publication.ExecutionReadPinCatalog with the same decision table the Arango
// bundle registry runs, arbitrated by real row locks instead of AQL
// single-document transactions:
//
//   - Every pin and cleanup transition for one execution opens one
//     transaction, takes the single publication_execution_retention anchor
//     row FOR UPDATE first, and only then reads or writes pin and claim rows.
//     The anchor is the one lock point both sides take, so "cleanup claim and
//     pin acquire must serialize" is enforced by PostgreSQL: concurrent
//     transitions on one execution queue on the anchor, and an execution can
//     never hold an unexpired pin and an unexpired cleanup claim at once --
//     each side's guard is evaluated under that lock against the newest
//     committed state.
//   - Decision table (now = call instant, all comparisons inclusive):
//     Acquire: refused while an unexpired cleanup claim exists; otherwise the
//     caller's pin row is created or refreshed and every already-expired pin
//     of the execution is garbage-collected, so a pin list never accumulates
//     dead owners (Arango APPEND(retained, ...) parity).
//     Renew pin: refused while a cleanup claim is unexpired or the caller has
//     no unexpired pin of its own; other expired pins are GC'd, the caller's
//     row is pushed out.
//     Release pin: deletes the caller's pin row whether or not it expired;
//     other owners' rows are untouched.
//     Claim cleanup: refused while any pin is unexpired; refused while an
//     unexpired claim names a different owner; same-owner re-claim refreshes;
//     on success every already-expired pin is removed (Arango pins: [] parity).
//     Renew cleanup: owner-conditional on an unexpired claim AND no unexpired
//     pin; like Arango it does not touch pin rows at all.
//     Release cleanup: clears the claim only for its owner.
//   - Repeated-owner correlation: pins are keyed by (execution, owner), so a
//     same-owner re-acquire refreshes one row instead of duplicating a
//     reader, and the claim row stores its owning reconciler so every claim
//     episode is attributable. Absent versus present is expressed only by row
//     absence (never a NULL stand-in): a missing claim row is "no cleanup"
//     and is distinct from an expired one, which is itself distinct from the
//     Arango literal cleanup: null only in storage shape, never in meaning.
//   - Arango's conflict-retry loop has no analogue here: row locks make the
//     guard evaluation atomic instead of optimistic. The Arango baseline's
//     trim-asymmetry (only Acquire trimmed identity) is hardened: every
//     operation trims and an empty execution id or owner is refused with the
//     named errors before any statement reaches the server, so a caller bug
//     never lands as a stored row -- the ErrInvalidLeaseKey discipline.
//
// Like the Registry and the lease keeper, ReadPins persists control facts
// only: ClickHouse remains the publication row store, there is no
// cross-database atomicity and no silent backend fallback -- every failure is
// returned for the caller to surface. All statements use ordered $n
// parameters and schema-qualified table names; nothing user-supplied is
// interpolated. The schema is created by 000009_publication_read_pins.sql in
// this package's embedded history (see migrate.go), applied through the
// pg-store-02 runner; this file contains no DDL.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/calypr/loom/internal/dataframe/publication"
)

const (
	// ExecutionRetentionTable is the per-execution lock anchor. Its row is
	// the single serialization point every pin and cleanup transition takes
	// before touching pin or claim rows. Created on demand by Acquire and
	// Claim (matching the Arango state document being created by the first
	// write) and never deleted.
	ExecutionRetentionTable = "publication_execution_retention"
	// ExecutionReadPinsTable stores one pin row per (execution, owner).
	ExecutionReadPinsTable = "publication_execution_read_pins"
	// ExecutionCleanupClaimsTable stores at most one cleanup claim per
	// execution; row absence is the only encoding of "no cleanup".
	ExecutionCleanupClaimsTable = "publication_execution_cleanup_claims"

	// executionCleanupTTL is the lifetime granted to a fresh cleanup claim,
	// matching the Arango registry's executionCleanupTTL.
	executionCleanupTTL = 2 * time.Minute
)

// ErrInvalidCleanupIdentity reports a cleanup operation with an empty
// execution id or owner. Unlike a refused claim (claimed=false), it marks a
// caller bug that must never reach the shared claim table as a stored row --
// the ErrInvalidLeaseKey discipline applied to cleanup identity.
var ErrInvalidCleanupIdentity = errors.New("publication postgres: cleanup execution id and owner are required")

// readPinAttempt is the in-transaction witness handed to the read-pin gate
// after every statement of one transition has applied inside the transaction
// and immediately before COMMIT (claimed=true) or the deliberate rollback of
// a refused transition (claimed=false).
type readPinAttempt struct {
	// Op names the transition: acquire-pin, renew-pin, release-pin,
	// claim-cleanup, renew-cleanup, or release-cleanup.
	Op          string
	ExecutionID string
	OwnerID     string
	// BackendPID is the PostgreSQL backend carrying the transaction
	// (pg_backend_pid), so a witness connection can correlate
	// pg_stat_activity rows with this exact attempt.
	BackendPID uint32
	// Statements are the SQL texts the transaction executed, in order.
	Statements []string
	// Claimed is the decision computed under the anchor lock: true commits,
	// false rolls the whole transition back with no durable change.
	Claimed bool
}

// readPinGate is a test-only seam, nil in production. A test stores a
// function here to rendezvous inside an open transition: it can observe the
// witness from a second real connection, then return nil to let COMMIT (or
// the refused rollback) proceed, or an error to force the total rollback that
// models a crash after every write applied. Production code never assigns it,
// and the zero cost when nil is one atomic load.
var readPinGate atomic.Pointer[func(readPinAttempt) error]

// ReadPins arbitrates read retention pins against cleanup claims for one
// schema of the PostgreSQL control plane.
type ReadPins struct {
	db        DB
	retention string
	pins      string
	cleanup   string

	// claimTTL is the lifetime a fresh ClaimExecutionCleanup grants.
	// Production keeps the baseline executionCleanupTTL; tests inside this
	// package may lower it directly (the migrate.go panicHook precedent) to
	// exercise abandoned-claim expiry without baseline-scale waits.
	claimTTL time.Duration
}

var _ publication.ExecutionReadPinCatalog = (*ReadPins)(nil)

// BundleCatalog is the catalog value for one schema of the PostgreSQL
// control plane: the bundle Registry and the ReadPins arbiter bound to the
// same database and schema. Production consumers discover pin arbitration by
// type-asserting the catalog value they hold -- internal/dataframe/published
// (export and read-pin seams) and the ClickHouse bundle store each assert
// publication.ExecutionReadPinCatalog on it -- so in the PostgreSQL shape
// this type is what those assertions must find; a bare Registry would make
// the reader seams hard-fail and the ClickHouse reconciler silently skip
// pin arbitration.
type BundleCatalog struct {
	*Registry
	pins *ReadPins
}

var (
	_ publication.BundleCatalog           = (*BundleCatalog)(nil)
	_ publication.ExactExecutionCatalog   = (*BundleCatalog)(nil)
	_ publication.PagedBundleCatalog      = (*BundleCatalog)(nil)
	_ publication.ExecutionReadPinCatalog = (*BundleCatalog)(nil)
)

// NewBundleCatalog binds both halves to the same pool and schema, so pin
// arbitration and bundle visibility can never straddle databases or schemas.
func NewBundleCatalog(pool DB, schema string) (*BundleCatalog, error) {
	registry, err := New(pool, schema)
	if err != nil {
		return nil, err
	}
	pins, err := NewReadPins(pool, schema)
	if err != nil {
		return nil, err
	}
	return &BundleCatalog{Registry: registry, pins: pins}, nil
}

// Pins returns the bound arbiter for wiring that needs it directly, such as
// NewReconciler.
func (c *BundleCatalog) Pins() *ReadPins { return c.pins }

// ExecutionReadPinCatalog delegation to the bound arbiter. All six methods
// forward verbatim; BundleCatalog adds no policy of its own.

func (c *BundleCatalog) AcquireExecutionReadPin(ctx context.Context, executionID, ownerID string, expiresAt time.Time) (bool, error) {
	return c.pins.AcquireExecutionReadPin(ctx, executionID, ownerID, expiresAt)
}

func (c *BundleCatalog) RenewExecutionReadPin(ctx context.Context, executionID, ownerID string, expiresAt time.Time) (bool, error) {
	return c.pins.RenewExecutionReadPin(ctx, executionID, ownerID, expiresAt)
}

func (c *BundleCatalog) ReleaseExecutionReadPin(ctx context.Context, executionID, ownerID string) error {
	return c.pins.ReleaseExecutionReadPin(ctx, executionID, ownerID)
}

func (c *BundleCatalog) ClaimExecutionCleanup(ctx context.Context, executionID, ownerID string) (bool, error) {
	return c.pins.ClaimExecutionCleanup(ctx, executionID, ownerID)
}

func (c *BundleCatalog) RenewExecutionCleanup(ctx context.Context, executionID, ownerID string, expiresAt time.Time) (bool, error) {
	return c.pins.RenewExecutionCleanup(ctx, executionID, ownerID, expiresAt)
}

func (c *BundleCatalog) ReleaseExecutionCleanup(ctx context.Context, executionID, ownerID string) error {
	return c.pins.ReleaseExecutionCleanup(ctx, executionID, ownerID)
}

// NewReadPins binds an arbiter to one schema of a PostgreSQL 18 database
// served by pool. The schema must already contain the retention tables
// (000009_publication_read_pins.sql in this package's embedded history,
// applied via MigrateSchema); NewReadPins never runs DDL and never falls
// back when any is missing -- the first statement surfaces the failure.
func NewReadPins(pool DB, schema string) (*ReadPins, error) {
	if pool == nil {
		return nil, errors.New("publication postgres: nil pool")
	}
	if !schemaNamePattern.MatchString(schema) {
		return nil, fmt.Errorf("%w: %q must match [a-z_][a-z0-9_]{0,62}", ErrInvalidSchema, schema)
	}
	return &ReadPins{
		db:        pool,
		retention: pgx.Identifier{schema, ExecutionRetentionTable}.Sanitize(),
		pins:      pgx.Identifier{schema, ExecutionReadPinsTable}.Sanitize(),
		cleanup:   pgx.Identifier{schema, ExecutionCleanupClaimsTable}.Sanitize(),
		claimTTL:  executionCleanupTTL,
	}, nil
}

// readPinTx is one arbitrated transition: an open transaction plus the
// statement witness. Every statement is recorded verbatim as it is handed to
// the driver, so the gate witness reports exactly what ran.
type readPinTx struct {
	p        *ReadPins
	tx       pgx.Tx
	attempt  readPinAttempt
	recorded []string
}

func (p *ReadPins) beginTransition(ctx context.Context, op, executionID, owner string) (*readPinTx, error) {
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("publication postgres: begin %s: %w", op, err)
	}
	t := &readPinTx{p: p, tx: tx, attempt: readPinAttempt{Op: op, ExecutionID: executionID, OwnerID: owner}}
	if conn := tx.Conn(); conn != nil {
		t.attempt.BackendPID = conn.PgConn().PID()
	}
	return t, nil
}

func (t *readPinTx) record(sql string) {
	t.recorded = append(t.recorded, sql)
	t.attempt.Statements = t.recorded
}

func (t *readPinTx) exec(ctx context.Context, sql string, args ...any) (int64, error) {
	t.record(sql)
	tag, err := t.tx.Exec(ctx, sql, args...)
	if err != nil {
		return 0, fmt.Errorf("publication postgres: %s: %w", t.attempt.Op, err)
	}
	return tag.RowsAffected(), nil
}

func (t *readPinTx) queryBool(ctx context.Context, sql string, args ...any) (bool, error) {
	t.record(sql)
	var value bool
	if err := t.tx.QueryRow(ctx, sql, args...).Scan(&value); err != nil {
		return false, fmt.Errorf("publication postgres: %s guard: %w", t.attempt.Op, err)
	}
	return value, nil
}

// finish hands the decided transition to the gate seam and then either
// commits (claimed) or rolls back the refused transition entirely, so a
// guard refusal leaves zero durable trace.
func (t *readPinTx) finish(ctx context.Context, claimed bool) (bool, error) {
	t.attempt.Claimed = claimed
	if gate := readPinGate.Load(); gate != nil {
		if err := (*gate)(t.attempt); err != nil {
			_ = t.tx.Rollback(ctx)
			return false, err
		}
	}
	if !claimed {
		if err := t.tx.Rollback(ctx); err != nil {
			return false, fmt.Errorf("publication postgres: %s rollback refused transition: %w", t.attempt.Op, err)
		}
		return false, nil
	}
	if err := t.tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("publication postgres: %s commit: %w", t.attempt.Op, err)
	}
	return true, nil
}

func (t *readPinTx) close() {
	_ = t.tx.Rollback(context.WithoutCancel(context.Background()))
}

// ensureAnchor creates the execution's anchor row when absent and locks it.
// Creation happens only on write paths that may mint state (Acquire, Claim),
// matching the Arango state document being UPSERTed by exactly those
// operations; the lock is the serialization point every transition shares.
func (t *readPinTx) ensureAnchor(ctx context.Context, now time.Time) error {
	if _, err := t.exec(ctx,
		"INSERT INTO "+t.p.retention+" (execution_id, updated_at) VALUES ($1, $2)"+
			" ON CONFLICT (execution_id) DO NOTHING",
		t.attempt.ExecutionID, now); err != nil {
		return err
	}
	held, err := t.lockAnchor(ctx)
	if err != nil {
		return err
	}
	if !held {
		return fmt.Errorf("publication postgres: %s: anchor row vanished for execution %s", t.attempt.Op, t.attempt.ExecutionID)
	}
	return nil
}

// lockAnchor locks an existing anchor row; ok=false means the execution has
// never been pinned or claimed (no state document in the Arango model).
func (t *readPinTx) lockAnchor(ctx context.Context) (bool, error) {
	t.record("SELECT true FROM " + t.p.retention + " WHERE execution_id = $1 FOR UPDATE")
	var held bool
	err := t.tx.QueryRow(ctx,
		"SELECT true FROM "+t.p.retention+" WHERE execution_id = $1 FOR UPDATE",
		t.attempt.ExecutionID).Scan(&held)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("publication postgres: %s lock anchor: %w", t.attempt.Op, err)
	}
	return held, nil
}

func (t *readPinTx) touchAnchor(ctx context.Context, now time.Time) error {
	_, err := t.exec(ctx,
		"UPDATE "+t.p.retention+" SET updated_at = $2 WHERE execution_id = $1",
		t.attempt.ExecutionID, now)
	return err
}

// guardCleanupFree reports true when no unexpired claim blocks readers.
func (t *readPinTx) guardCleanupFree(ctx context.Context, now time.Time) (bool, error) {
	return t.queryBool(ctx,
		"SELECT NOT EXISTS (SELECT 1 FROM "+t.p.cleanup+" WHERE execution_id = $1 AND expires_at >= $2)",
		t.attempt.ExecutionID, now)
}

// guardNoActivePins reports true when no reader currently holds the
// execution's physical tables.
func (t *readPinTx) guardNoActivePins(ctx context.Context, now time.Time) (bool, error) {
	return t.queryBool(ctx,
		"SELECT NOT EXISTS (SELECT 1 FROM "+t.p.pins+" WHERE execution_id = $1 AND expires_at >= $2)",
		t.attempt.ExecutionID, now)
}

// gcExpiredPins drops already-expired pin rows of the execution. It runs
// only inside transitions the Arango baseline rewrites the pins array in
// (acquire, renew pin, claim cleanup), never in renew-cleanup or release,
// preserving the baseline's exact garbage-collection points.
func (t *readPinTx) gcExpiredPins(ctx context.Context, now time.Time) error {
	_, err := t.exec(ctx,
		"DELETE FROM "+t.p.pins+" WHERE execution_id = $1 AND expires_at < $2",
		t.attempt.ExecutionID, now)
	return err
}

// AcquireExecutionReadPin pins the execution for owner until expires. It is
// refused (claimed=false, zero durable change) while an unexpired cleanup
// claim exists, so a reader can never pin a table sweep has promised to
// remove. A same-owner re-acquire refreshes the one pin row, and every
// already-expired pin is collected. An empty identity after trimming is a
// caller bug refused with publication.ErrExecutionReadPinLost, matching the
// Arango registry.
func (p *ReadPins) AcquireExecutionReadPin(ctx context.Context, executionID, owner string, expires time.Time) (bool, error) {
	executionID, owner = strings.TrimSpace(executionID), strings.TrimSpace(owner)
	if executionID == "" || owner == "" {
		return false, publication.ErrExecutionReadPinLost
	}
	now := time.Now().UTC()
	t, err := p.beginTransition(ctx, "acquire-pin", executionID, owner)
	if err != nil {
		return false, err
	}
	defer t.close()
	if err := t.ensureAnchor(ctx, now); err != nil {
		return false, err
	}
	free, err := t.guardCleanupFree(ctx, now)
	if err != nil {
		return false, err
	}
	if !free {
		return t.finish(ctx, false)
	}
	if err := t.gcExpiredPins(ctx, now); err != nil {
		return false, err
	}
	if _, err := t.exec(ctx,
		"INSERT INTO "+t.p.pins+" (execution_id, owner_id, expires_at, updated_at)"+
			" VALUES ($1, $2, $3, $4)"+
			" ON CONFLICT (execution_id, owner_id) DO UPDATE SET expires_at = $3, updated_at = $4",
		executionID, owner, expires, now); err != nil {
		return false, err
	}
	if err := t.touchAnchor(ctx, now); err != nil {
		return false, err
	}
	return t.finish(ctx, true)
}

// RenewExecutionReadPin extends the owner's pin only while the owner still
// holds an unexpired pin and no unexpired cleanup claim exists; any other
// answer is claimed=false with the state untouched. Expired pins of other
// owners are collected, mirroring the Arango APPEND(retained, ...) rewrite.
func (p *ReadPins) RenewExecutionReadPin(ctx context.Context, executionID, owner string, expires time.Time) (bool, error) {
	executionID, owner = strings.TrimSpace(executionID), strings.TrimSpace(owner)
	if executionID == "" || owner == "" {
		return false, publication.ErrExecutionReadPinLost
	}
	now := time.Now().UTC()
	t, err := p.beginTransition(ctx, "renew-pin", executionID, owner)
	if err != nil {
		return false, err
	}
	defer t.close()
	exists, err := t.lockAnchor(ctx)
	if err != nil {
		return false, err
	}
	if !exists {
		return t.finish(ctx, false)
	}
	free, err := t.guardCleanupFree(ctx, now)
	if err != nil {
		return false, err
	}
	if !free {
		return t.finish(ctx, false)
	}
	owned, err := t.queryBool(ctx,
		"SELECT EXISTS (SELECT 1 FROM "+t.p.pins+" WHERE execution_id = $1 AND owner_id = $2 AND expires_at >= $3)",
		executionID, owner, now)
	if err != nil {
		return false, err
	}
	if !owned {
		return t.finish(ctx, false)
	}
	if err := t.gcExpiredPins(ctx, now); err != nil {
		return false, err
	}
	rows, err := t.exec(ctx,
		"UPDATE "+t.p.pins+" SET expires_at = $3, updated_at = $4"+
			" WHERE execution_id = $1 AND owner_id = $2",
		executionID, owner, expires, now)
	if err != nil {
		return false, err
	}
	if rows != 1 {
		return false, fmt.Errorf("publication postgres: renew-pin lost pin row under lock: %s/%s", executionID, owner)
	}
	if err := t.touchAnchor(ctx, now); err != nil {
		return false, err
	}
	return t.finish(ctx, true)
}

// ReleaseExecutionReadPin removes the owner's pin row whether or not it has
// expired; other owners' pins are never touched. With no retention state at
// all it is a no-op that mints nothing, matching the Arango no-document
// case.
func (p *ReadPins) ReleaseExecutionReadPin(ctx context.Context, executionID, owner string) error {
	executionID, owner = strings.TrimSpace(executionID), strings.TrimSpace(owner)
	if executionID == "" || owner == "" {
		return publication.ErrExecutionReadPinLost
	}
	now := time.Now().UTC()
	t, err := p.beginTransition(ctx, "release-pin", executionID, owner)
	if err != nil {
		return err
	}
	defer t.close()
	exists, err := t.lockAnchor(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return t.commitTransition(ctx)
	}
	if _, err := t.exec(ctx,
		"DELETE FROM "+t.p.pins+" WHERE execution_id = $1 AND owner_id = $2",
		executionID, owner); err != nil {
		return err
	}
	// The Arango release rewrites the state document (updatedAt bump)
	// whenever it exists, even when the owner held no pin row.
	if err := t.touchAnchor(ctx, now); err != nil {
		return err
	}
	return t.commitTransition(ctx)
}

// ClaimExecutionCleanup grants owner the exclusive right to remove the
// execution's physical tables for the claim TTL. It is refused while any pin
// is unexpired -- the half of the serialization the reader side relies on --
// and while an unexpired claim names a different owner; a same-owner re-claim
// refreshes the expiry. On success every already-expired pin is removed, the
// Arango pins: [] rewrite.
func (p *ReadPins) ClaimExecutionCleanup(ctx context.Context, executionID, owner string) (bool, error) {
	executionID, owner = strings.TrimSpace(executionID), strings.TrimSpace(owner)
	if executionID == "" || owner == "" {
		return false, ErrInvalidCleanupIdentity
	}
	now := time.Now().UTC()
	t, err := p.beginTransition(ctx, "claim-cleanup", executionID, owner)
	if err != nil {
		return false, err
	}
	defer t.close()
	if err := t.ensureAnchor(ctx, now); err != nil {
		return false, err
	}
	noPins, err := t.guardNoActivePins(ctx, now)
	if err != nil {
		return false, err
	}
	if !noPins {
		return t.finish(ctx, false)
	}
	var (
		claimOwner   string
		claimExpires time.Time
		claimExists  bool
	)
	t.record("SELECT owner_id, expires_at FROM " + t.p.cleanup + " WHERE execution_id = $1")
	err = t.tx.QueryRow(ctx,
		"SELECT owner_id, expires_at FROM "+t.p.cleanup+" WHERE execution_id = $1",
		executionID).Scan(&claimOwner, &claimExpires)
	if err == nil {
		claimExists = true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("publication postgres: claim-cleanup read claim: %w", err)
	}
	if claimExists && !claimExpires.Before(now) && claimOwner != owner {
		return t.finish(ctx, false)
	}
	if err := t.gcExpiredPins(ctx, now); err != nil {
		return false, err
	}
	if _, err := t.exec(ctx,
		"INSERT INTO "+t.p.cleanup+" (execution_id, owner_id, expires_at, updated_at)"+
			" VALUES ($1, $2, $3, $4)"+
			" ON CONFLICT (execution_id) DO UPDATE SET owner_id = $2, expires_at = $3, updated_at = $4",
		executionID, owner, now.Add(p.claimTTL), now); err != nil {
		return false, err
	}
	if err := t.touchAnchor(ctx, now); err != nil {
		return false, err
	}
	return t.finish(ctx, true)
}

// RenewExecutionCleanup extends the claim only for its current owner while
// the claim is unexpired AND no pin is unexpired: a reader that pinned after
// the claim lapsed (or a straggler pin that outlived a lost heartbeat)
// vetoes renewal so the sweeper stops, the Arango LENGTH(active) == 0
// guard. It never rewrites pin rows, baseline-parity.
func (p *ReadPins) RenewExecutionCleanup(ctx context.Context, executionID, owner string, expires time.Time) (bool, error) {
	executionID, owner = strings.TrimSpace(executionID), strings.TrimSpace(owner)
	if executionID == "" || owner == "" {
		return false, ErrInvalidCleanupIdentity
	}
	now := time.Now().UTC()
	t, err := p.beginTransition(ctx, "renew-cleanup", executionID, owner)
	if err != nil {
		return false, err
	}
	defer t.close()
	exists, err := t.lockAnchor(ctx)
	if err != nil {
		return false, err
	}
	if !exists {
		return t.finish(ctx, false)
	}
	var (
		claimOwner   string
		claimExpires time.Time
		claimFound   bool
	)
	t.record("SELECT owner_id, expires_at FROM " + t.p.cleanup + " WHERE execution_id = $1")
	err = t.tx.QueryRow(ctx,
		"SELECT owner_id, expires_at FROM "+t.p.cleanup+" WHERE execution_id = $1",
		executionID).Scan(&claimOwner, &claimExpires)
	if err == nil {
		claimFound = true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("publication postgres: renew-cleanup read claim: %w", err)
	}
	if !claimFound || claimOwner != owner || claimExpires.Before(now) {
		return t.finish(ctx, false)
	}
	noPins, err := t.guardNoActivePins(ctx, now)
	if err != nil {
		return false, err
	}
	if !noPins {
		return t.finish(ctx, false)
	}
	rows, err := t.exec(ctx,
		"UPDATE "+t.p.cleanup+" SET expires_at = $3, updated_at = $4"+
			" WHERE execution_id = $1 AND owner_id = $2",
		executionID, owner, expires, now)
	if err != nil {
		return false, err
	}
	if rows != 1 {
		return false, fmt.Errorf("publication postgres: renew-cleanup lost claim row under lock: %s/%s", executionID, owner)
	}
	if err := t.touchAnchor(ctx, now); err != nil {
		return false, err
	}
	return t.finish(ctx, true)
}

// ReleaseExecutionCleanup clears the claim only for its owner; a non-owner
// or ownerless release is a no-op that leaves the live claim intact.
func (p *ReadPins) ReleaseExecutionCleanup(ctx context.Context, executionID, owner string) error {
	executionID, owner = strings.TrimSpace(executionID), strings.TrimSpace(owner)
	if executionID == "" || owner == "" {
		return ErrInvalidCleanupIdentity
	}
	now := time.Now().UTC()
	t, err := p.beginTransition(ctx, "release-cleanup", executionID, owner)
	if err != nil {
		return err
	}
	defer t.close()
	exists, err := t.lockAnchor(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return t.commitTransition(ctx)
	}
	rows, err := t.exec(ctx,
		"DELETE FROM "+t.p.cleanup+" WHERE execution_id = $1 AND owner_id = $2",
		executionID, owner)
	if err != nil {
		return err
	}
	// The Arango release updates the state document only when the claim was
	// actually owned by the releaser, so updatedAt moves only on a hit.
	if rows == 1 {
		if err := t.touchAnchor(ctx, now); err != nil {
			return err
		}
	}
	return t.commitTransition(ctx)
}

// commitTransition hands an accepted release (the catalog contract gives
// releases no refused answer) -- including the lock-only no-op where the
// execution had no state to release -- to the gate seam and commits. The
// witness's statement list distinguishes a real release from the no-op.
func (t *readPinTx) commitTransition(ctx context.Context) error {
	t.attempt.Claimed = true
	if gate := readPinGate.Load(); gate != nil {
		if err := (*gate)(t.attempt); err != nil {
			_ = t.tx.Rollback(ctx)
			return err
		}
	}
	if err := t.tx.Commit(ctx); err != nil {
		return fmt.Errorf("publication postgres: %s commit: %w", t.attempt.Op, err)
	}
	return nil
}
