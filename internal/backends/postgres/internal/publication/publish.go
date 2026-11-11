package postgres

// Atomic publish-commit path (pg-publication-atomic-visibility-v1).
//
// Registry.PublishExecution is the single visibility commit of a bundle: it
// moves the logical reader pointer AND records the PUBLISHED execution
// metadata in ONE PostgreSQL transaction, after the caller has already
// staged and verified the physical ClickHouse tables. The properties below
// are the ticket contract, and TestPublishExecutionAtomicity holds the
// implementation to each against a real PostgreSQL 18 server.
//
//  1. Commit-boundary visibility. Before COMMIT, every outside reader keeps
//     seeing the old pointer and the pre-publish execution state; after
//     COMMIT, readers see the pointer naming this execution and the full
//     exact execution in one consistent snapshot. No intermediate state --
//     PUBLISHED-without-pointer or pointer-without-PUBLISHED -- is ever
//     externally observable, the same guarantee the Arango registry's
//     single-transaction publish gives (F-B16 root repair).
//  2. Failure atomicity. Any failure inside the transaction -- including a
//     crash after every statement already applied -- rolls the whole thing
//     back: neither the new execution visibility nor the pointer move is
//     exposed, and a clean retry converges with byte-identical rows.
//  3. Typed conflict, idempotent retry. Republishing the SAME execution
//     whose pointer move and payload are already durable succeeds as a
//     no-op (converged). Any other losing publisher -- stale expected
//     value, foreign or expired fence, missing execution row -- is refused
//     with the named publication.ErrBundlePointerConflict and changes
//     nothing.
//  4. No ClickHouse inside the transaction. Inputs are values only: the
//     signature carries no callbacks, so no ClickHouse operation can be
//     smuggled into the transaction window; the statement set the
//     transaction executes is exactly the catalog statements below, with
//     two extra catalog reads on the converged no-op path. ClickHouse
//     staging and verification belong to the caller
//     (internal/dataframe/publication/clickhouse BundleStore.Commit) and
//     complete before this method is reached. There is no cross-database
//     atomicity and no silent backend fallback: every failure is returned
//     for the caller to surface.
//
// Lock order is stable across concurrent publishers -- lease row, execution
// row, pointer row -- so racing publishers serialize instead of deadlocking.

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/calypr/loom/internal/dataframe/publication"
)

// publishAttempt is the in-transaction witness handed to the publish gate
// after every publish statement has applied inside the transaction and
// immediately before COMMIT.
type publishAttempt struct {
	// PointerName/PointerKey identify the logical reader pointer being moved.
	PointerName string
	PointerKey  string
	// ExpectedExecution is the compare-and-swap expectation supplied by the
	// caller; ExecutionID/OwnerID identify the publishing execution and the
	// fence owner its lease is checked against.
	ExpectedExecution string
	ExecutionID       string
	OwnerID           string
	// BackendPID is the PostgreSQL backend actually carrying the publish
	// transaction (pg_backend_pid), so a witness connection can correlate
	// pg_stat_activity rows with this exact attempt.
	BackendPID uint32
	// Statements are the SQL texts the transaction executed, in order.
	Statements []string
	// Converged marks the idempotent retry no-op path: the pointer already
	// named this execution and the stored payload already matched, so no
	// row was rewritten.
	Converged bool
}

// publishGate is a test-only seam, nil in production. A test stores a
// function here to rendezvous inside an open publish transaction: it can
// observe the witness from a second real connection, then return nil to let
// COMMIT proceed or an error to force the total rollback that models a
// crash after all writes applied. Production code never assigns it, and the
// zero cost when nil is one atomic load.
var publishGate atomic.Pointer[func(publishAttempt) error]

// publishExecutionSet is the full-row SET list of the publish UPDATE: every
// scan column plus the payload, so the committed row is exactly the
// published execution -- the same column set SaveExecution writes.
const publishExecutionSet = "execution_key = $2, project = $3, dataset_generation = $4, recipe_name = $5, " +
	"translation_version = $6, state = $7, created_at = $8, updated_at = $9, " +
	"ready_at = $10, published_at = $11, payload = $12"

// PublishExecution atomically commits the PUBLISHED execution metadata and
// its visibility pointer in one PostgreSQL transaction under the lease
// fence. A missing execution, an unheld or expired lease, or a failed
// pointer compare-and-swap rolls the whole thing back and reports
// publication.ErrBundlePointerConflict, so no observer -- above all the
// reconcile sweep -- ever sees a PUBLISHED execution the pointer does not
// yet name.
//
// A retry of the same execution is idempotent: when the pointer already
// names the execution and the stored payload already equals the payload
// about to be written, the call is a converged no-op that succeeds without
// touching any row. Every other losing publisher is refused with the named
// conflict error and leaves both documents intact.
func (r *Registry) PublishExecution(ctx context.Context, name, expected string, execution publication.BundleExecution) error {
	params, err := executionParams(execution)
	if err != nil {
		return err
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("publication postgres: begin publish: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	attempt := publishAttempt{
		PointerName:       name,
		PointerKey:        pointerKey(name),
		ExpectedExecution: expected,
		ExecutionID:       execution.ID,
		OwnerID:           execution.OwnerID,
	}
	if conn := tx.Conn(); conn != nil {
		attempt.BackendPID = conn.PgConn().PID()
	}
	// Every statement this transaction executes is recorded verbatim as it
	// is handed to the driver, so the gate witness -- and with it the
	// "nothing but catalog statements inside the transaction" guarantee --
	// reports exactly what ran, not what a wrapper assumed ran.
	record := func(sql string) { attempt.Statements = append(attempt.Statements, sql) }

	// 1. Lease fence, locked first. Only the current unexpired owner of the
	// execution's lease key may publish; an empty owner never matches.
	fenceSQL := "SELECT true FROM " + r.leases +
		" WHERE key = $1 AND owner_id = $2 AND expires_at >= $3 FOR UPDATE"
	record(fenceSQL)
	var held bool
	if err := tx.QueryRow(ctx, fenceSQL, execution.Key, execution.OwnerID, time.Now().UTC()).Scan(&held); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return publication.ErrBundlePointerConflict
		}
		return fmt.Errorf("publication postgres: check publish lease: %w", err)
	}
	if !held {
		return publication.ErrBundlePointerConflict
	}

	// 2. The execution row must exist and is locked before the pointer moves
	// so a concurrent checkpoint cannot slip under the publish.
	lockSQL := "SELECT true FROM " + r.executions + " WHERE id = $1 FOR UPDATE"
	record(lockSQL)
	var exists bool
	if err := tx.QueryRow(ctx, lockSQL, execution.ID).Scan(&exists); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return publication.ErrBundlePointerConflict
		}
		return fmt.Errorf("publication postgres: check publish execution: %w", err)
	}
	if !exists {
		return publication.ErrBundlePointerConflict
	}

	// 3. Move the pointer under compare-and-swap. The ON CONFLICT guard is
	// re-evaluated against the newest committed row and locks it, so racing
	// publishers converge to exactly one winner.
	swapped, casSQL, err := compareAndSwapPointer(ctx, tx, r.pointers, name, expected, execution.ID, execution.UpdatedAt)
	if err != nil {
		return err
	}
	record(casSQL)
	if !swapped {
		// The CAS lost. This is either the idempotent retry of an
		// execution that is already fully published at this pointer, or a
		// genuine stale publisher. The pointer row is locked by the failed
		// DO UPDATE attempt until this transaction ends, so both reads
		// below are stable.
		var current string
		pointerReadSQL := "SELECT execution_id FROM " + r.pointers + " WHERE pointer_key = $1"
		record(pointerReadSQL)
		if err := tx.QueryRow(ctx, pointerReadSQL, attempt.PointerKey).Scan(&current); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return publication.ErrBundlePointerConflict
			}
			return fmt.Errorf("publication postgres: read contested pointer: %w", err)
		}
		if current != execution.ID {
			return publication.ErrBundlePointerConflict
		}
		// The pointer already names this execution. Converge only when the
		// stored payload already equals the payload this call would write;
		// a drifted retry is refused rather than silently rewritten.
		var matches bool
		payloadReadSQL := "SELECT payload = $2::jsonb FROM " + r.executions + " WHERE id = $1"
		record(payloadReadSQL)
		if err := tx.QueryRow(ctx, payloadReadSQL, execution.ID, params[len(params)-1]).Scan(&matches); err != nil {
			return fmt.Errorf("publication postgres: compare publish payload: %w", err)
		}
		if !matches {
			return publication.ErrBundlePointerConflict
		}
		attempt.Converged = true
	} else {
		// 4. Commit the PUBLISHED metadata in the SAME transaction as the
		// pointer move: one COMMIT makes both visible together or not at
		// all.
		updateSQL := "UPDATE " + r.executions + " SET " + publishExecutionSet + " WHERE id = $1"
		record(updateSQL)
		if _, err := tx.Exec(ctx, updateSQL, params...); err != nil {
			return fmt.Errorf("publication postgres: publish execution: %w", err)
		}
	}

	if gate := publishGate.Load(); gate != nil {
		if err := (*gate)(attempt); err != nil {
			// The injected failure lands after every visibility write has
			// applied and before COMMIT: the deferred rollback makes the
			// whole publish invisible, which is exactly the crash window
			// the pre-fix two-leg publish could not survive.
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("publication postgres: commit publish: %w", err)
	}
	return nil
}
