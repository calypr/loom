package postgres

// Atomic Explorer publish-authoring commit on PostgreSQL 18
// (pg-state-publish-authoring-atomic-v1).
//
// PublishAuthoring is the single visibility commit of Explorer authoring: it
// commits the verified compilation receipt, the immutable Explorer revision,
// the active Explorer owner pointer, AND the project release activation in
// ONE PostgreSQL transaction, after the caller has already materialized and
// verified the ClickHouse publication rows. The Arango baseline
// (internal/backends/arango/internal/explorer/publication.go PublishAuthoring) runs the same
// five legs inside one Arango transaction -- receipt upsert + readback
// compare, revision upsert, CompareAndSwapPublishRelease, and the owner +
// candidate activation -- and this adapter reproduces that contract on the
// relational lanes without duplicating any lane's schema:
//
//   - receipt/revision/owner rows live in this package's control-plane
//     tables (migration history here, receipts.go/revisions.go/explorers.go);
//   - the release candidate and the active release pointer are the release
//     lane's own tables (project_release, active_project_release -- the
//     pg-publication-project-release-cas-v1 schema);
//   - the generation manifest and the active-generation pointer are the
//     generation lane's own tables (dataset_generation_manifest,
//     dataset_active_generation -- the pg-store-03 active-pointer schema).
//     The generation pointer is another lane and joins this transaction as a
//     participant: the publish-path activation holds its row FOR SHARE and
//     refuses while the pointer names a generation other than the release's
//     -- the PostgreSQL spelling of the baseline's requireCurrentGeneration
//     guard (CompareAndSwapPublishRelease: "The publication is only current
//     when the active generation pointer still identifies its source
//     manifest"). This adapter never adds columns to, or writes, another
//     lane's schema.
//
// Properties this file commits to, each pinned against a real PostgreSQL 18
// by the TestPublishAuthoring* suite:
//
//  1. Commit-boundary visibility. Before COMMIT every outside reader still
//     sees the old receipt/revision/owner/pointer state; after COMMIT all
//     four legs are visible in one consistent snapshot. No intermediate
//     state (receipt-without-revision, revision-ACTIVE-without-owner,
//     pointer-moved-without-activation) is ever externally observable.
//  2. Failure atomicity. Any failure inside the transaction -- including an
//     injection after every statement already applied (crashAt seam) --
//     rolls the whole thing back, and a clean retry converges to the state a
//     single uninterrupted run would have produced.
//  3. Verified receipt, exactly as receipts.go verifies one: the insert is
//     ON CONFLICT DO NOTHING; a lost claim rereads FOR UPDATE and the
//     stored row must decode, validate, and match the caller's canonical
//     ReceiptID digest, or the publish is refused explorer.ErrCorruptReceipt
//     with the stored row untouched.
//  4. Immutable revision replay. The revision insert is ON CONFLICT (id) DO
//     NOTHING; a lost claim rereads FOR UPDATE and the stored content_digest
//     must equal the caller's canonical digest: equal is the idempotent
//     replay (the Arango UPSERT UPDATE {} no-op), different is the typed
//     explorer.ErrImmutableRevision refusal (the exact-payload replay guard).
//  5. Release activation CAS (the publish path). The candidate must already
//     be persisted in the release lane and still match byte-for-byte
//     (createdAt excluded); the generation manifest must exist in a staged
//     state; the active-generation pointer must exist and name the release's
//     generation; the active release pointer then moves under a
//     revision-conditional CAS. An already-active candidate is the zero-write
//     no-op (the F-B15 rule) and takes precedence over the expected
//     revision. A lost CAS is dataset.ErrReleaseActivationConflict and
//     NOTHING this transaction wrote commits -- not the receipt, not the
//     revision, not the activation. Backend faults are never classified as
//     conflicts.
//  6. Activation guard parity (activationReadAQL): the owner must exist in
//     scope with the expected management mode (REPOSITORY for "default",
//     INTERACTIVE otherwise) and the candidate revision must be READY, or
//     ACTIVE with the owner already naming it; every refusal is the same
//     indistinguishable explorer.ErrDraftConflict, so a foreign owner, a
//     mode mismatch, and a missing owner never leak which one failed.
//
// PostgreSQL-native divergences from the baseline, deliberate and pinned:
//
//   - Zero-write replay. The revision state machine (migration 000006)
//     refuses ACTIVE->ACTIVE even for mutable-column churn, so when the
//     candidate is already ACTIVE and owner.active_revision_id already names
//     it, this adapter performs ZERO writes to the revision and owner rows
//     (the Arango baseline re-patches activated_at/publication.updatedAt).
//     The release CAS still runs its normal rules, so a replay whose pointer
//     has since moved is still a real activation.
//   - Fail-closed owner pointer. If owner.active_revision_id is non-empty,
//     it must resolve to a stored revision in the same project/Explorer
//     scope whose status is ACTIVE; the baseline's activation silently
//     skipped a missing prior. A dangling or mismatched pointer is the typed
//     explorer.ErrDraftConflict and nothing moves.
//   - Deadlock contention. The fixed lock order below makes racing
//     publishers serialize, but the candidate-before-prior interleave can
//     still deadlock under adversarial timing; PostgreSQL's deadlock victim
//     is retried on a fresh transaction a bounded number of times (the same
//     bounded-conflict-retry budget receipts.go uses), never mislabeled as
//     an application conflict. Retryability is classified by the error's
//     sentinel (isTypedPublicationRefusal), so a victim parked at any
//     statement of the transaction -- including the FOR UPDATE lock reads
//     (receipt/revision claim rereads, owner guard, prior lock) -- is
//     retried just like one from a data-write statement.
//
// Lock order is stable across concurrent publishes -- receipt row, revision
// candidate, release candidate, generation manifest, generation pointer,
// release pointer, owner row, prior revision row -- so racing publishers
// serialize instead of deadlocking, and the two release-lane statements
// keep the same relative order the release store's own
// CompareAndSwapActivateRelease uses for concurrent outside activations.
//
// There is no cross-database atomicity with ClickHouse (its rows are
// verified by the caller before this method is reached), no fallback to
// Arango or any other backend, and no callback surface that could smuggle
// an outside store into the transaction window: every failure is returned,
// typed where the contract types it, for the caller to surface.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/calypr/loom/internal/explorer"

	datasetpostgres "github.com/calypr/loom/internal/backends/postgres/internal/dataset"

	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/dataset"
)

const (
	// publishTxMaxAttempts and the retry delay mirror the bounded
	// conflict-retry budget of receipts.go/revision inserts: only
	// PostgreSQL deadlock victims (SQLSTATE 40P01) re-run on a fresh
	// transaction; every other failure returns typed as it is.
	publishTxMaxAttempts  = 4
	publishDeadlockRetry  = 5 * time.Millisecond
	publishDeadlockState  = "40P01"
	publishDiagnosticsSet = "[]" // the baseline's activation clears diagnostics to []
)

// ErrInvalidPublicationInput reports an input the publish lane refuses
// before opening any transaction: a blank revision identity, a candidate
// outside the READY lifecycle state (publish authoring commits a READY
// revision; an ACTIVE/FAILED/SUPERSEDED candidate can only enter through
// its own lane), or an undecodable/inconsistent release candidate document.
var ErrInvalidPublicationInput = errors.New("postgres explorer store: invalid publish authoring input")

// ErrPublishContention reports that every publish attempt on this call lost
// a PostgreSQL deadlock to a concurrent publisher and re-run was exhausted.
// It is a genuine contention outcome wrapping the last backend error, never
// an application conflict and never a silent success.
var ErrPublishContention = errors.New("postgres explorer store: publish authoring lost every deadlock race")

// PublishStore commits Explorer authoring visibility across the Explorer
// control-plane schema and the release/generation lane schema of ONE
// PostgreSQL 18 database behind the shared pg-store-01 pool. It is the
// publication slice of this package's control-plane stores: receipt,
// revision, and owner persistence itself stays owned by
// receipts.go/revisions.go/explorers.go; PublishStore adds only the
// cross-lane transaction and the release-activation CAS. The caller owns
// schema creation and migration: the Explorer tables must come from this
// package's history and the release/generation tables from their owning
// lanes' histories in the same database; PublishStore never creates, drops,
// or migrates anything, and a missing table surfaces as SQLSTATE 42P01,
// never a fallback.
type PublishStore struct {
	client         *pgstore.Client
	explorerSchema string
	datasetSchema  string
	stmts          publishStatements
	// crashAt is the test-only fault/interleaving seam: when non-nil it is
	// invoked inside the open publish transaction after every write leg
	// (steps below) and once more immediately before COMMIT ("pre-commit").
	// Returning an error aborts the whole transaction -- the crash-window
	// injection that proves all-or-nothing commit-boundary visibility --
	// and receiving the live pgx.Tx lets a test coordinate a concurrent
	// observer. Production never assigns it; the zero cost when nil is one
	// nil check per leg.
	crashAt func(context.Context, pgx.Tx, string) error
}

// NewPublishStore builds the publish store for one Explorer control-plane
// schema and one release/generation lane schema of the database behind
// client. Both schema names must be safe lowercase identifiers (the same
// shape the pg-store-02 runner accepts); the two lanes may share a schema or
// live side by side in one database -- one PostgreSQL transaction spans
// schemas of its own database -- but never two databases, because publish
// authoring is one ACID unit and cross-database atomicity does not exist
// here. It opens no connection and runs no DDL.
func NewPublishStore(client *pgstore.Client, explorerSchema, datasetSchema string) (*PublishStore, error) {
	if client == nil {
		return nil, errors.New("postgres explorer store: publish client is required")
	}
	for _, schema := range []string{explorerSchema, datasetSchema} {
		if !schemaNamePattern.MatchString(schema) {
			return nil, fmt.Errorf("postgres explorer store: invalid publish schema %q: must match [a-z_][a-z0-9_]{0,62}", schema)
		}
	}
	return &PublishStore{
		client:         client,
		explorerSchema: explorerSchema,
		datasetSchema:  datasetSchema,
		stmts:          newPublishStatements(explorerSchema, datasetSchema),
	}, nil
}

// publishStatements are the backend-tagged statements of the publish
// transaction, in execution (lock) order. Ordered placeholders ($1..$N)
// match each argument slice by construction.
type publishStatements struct {
	// 1. Receipt claim (control-plane schema).
	receiptInsert string // INSERT ... ON CONFLICT (project, explorer_id, id) DO NOTHING RETURNING
	receiptClaim  string // tenant-scoped SELECT ... FOR UPDATE
	// 2. Revision candidate (control-plane schema).
	revisionInsert string // INSERT ... ON CONFLICT (id) DO NOTHING RETURNING
	revisionClaim  string // SELECT ... WHERE id = $1 FOR UPDATE
	// 3. Release lane: candidate verification and pointer CAS (lane schema).
	releaseCandidateLock string // SELECT payload, content-equality ... FOR SHARE
	manifestLock         string // SELECT state ... FOR SHARE
	generationPointer    string // SELECT generation ... FOR SHARE
	releasePointerLock   string // SELECT release_id, revision ... FOR UPDATE
	releasePointerRead   string // SELECT release_id, revision (unlocked reread)
	releasePointerCAS    string // UPDATE ... WHERE project AND revision RETURNING revision
	releasePointerInsert string // INSERT ... ON CONFLICT DO NOTHING (initial pointer)
	// 4. Activation guard + activation writes (control-plane schema).
	ownerGuard      string // SELECT owner FOR UPDATE, management mode in WHERE
	priorLock       string // SELECT revision FOR UPDATE by id
	priorSupersede  string // UPDATE status SUPERSEDED WHERE id AND status ACTIVE RETURNING
	candidateActive string // UPDATE status ACTIVE WHERE id AND status READY RETURNING
	ownerActivate   string // UPDATE active_revision_id WHERE identity RETURNING
}

func newPublishStatements(explorerSchema, datasetSchema string) publishStatements {
	ownerTable := sanitizeTableRef(explorerSchema, OwnerTable)
	ownerCols := strings.Join(ownerColumns, ", ")
	manifests := sanitizeTableRef(datasetSchema, "dataset_generation_manifest")
	generationPointer := sanitizeTableRef(datasetSchema, "dataset_active_generation")
	releases := sanitizeTableRef(datasetSchema, datasetpostgres.ProjectReleasesTable)
	pointers := sanitizeTableRef(datasetSchema, datasetpostgres.ActiveProjectReleasesTable)
	// The candidate content equality and the initial-pointer insert are the
	// release lane's own statement shapes, reused here so the publish path
	// compares and creates the pointer under exactly the release store's
	// rules (createdAt excluded from content, revision starts at 1).
	return publishStatements{
		receiptInsert:  newReceiptStatements(explorerSchema).insert,
		receiptClaim:   newReceiptStatements(explorerSchema).claim,
		revisionInsert: newRevisionStatements(explorerSchema).insert,
		revisionClaim:  newRevisionStatements(explorerSchema).claim,
		releaseCandidateLock: "SELECT payload::text, (payload - 'createdAt') = ($3::jsonb - 'createdAt') FROM " + releases +
			" WHERE project = $1 AND id = $2 FOR SHARE",
		manifestLock:       "SELECT state FROM " + manifests + " WHERE project = $1 AND generation = $2 FOR SHARE",
		generationPointer:  "SELECT generation FROM " + generationPointer + " WHERE project = $1 FOR SHARE",
		releasePointerLock: "SELECT release_id, revision FROM " + pointers + " WHERE project = $1 FOR UPDATE",
		releasePointerRead: "SELECT release_id, revision FROM " + pointers + " WHERE project = $1",
		releasePointerCAS: "UPDATE " + pointers + " SET release_id = $2, revision = $3, updated_at = now()" +
			" WHERE project = $1 AND revision = $4 RETURNING revision",
		releasePointerInsert: "INSERT INTO " + pointers + " (project, release_id, revision) VALUES ($1, $2, 1) ON CONFLICT DO NOTHING",
		ownerGuard: "SELECT " + ownerCols + " FROM " + ownerTable +
			" WHERE project = $1 AND explorer_id = $2 AND management_mode = $3 FOR UPDATE",
		priorLock:      "SELECT " + strings.Join(revisionColumns, ", ") + " FROM " + sanitizeTableRef(explorerSchema, RevisionsTable) + " WHERE id = $1 FOR UPDATE",
		priorSupersede: "UPDATE " + sanitizeTableRef(explorerSchema, RevisionsTable) + " SET status = 'SUPERSEDED' WHERE id = $1 AND status = 'ACTIVE' RETURNING " + strings.Join(revisionColumns, ", "),
		candidateActive: "UPDATE " + sanitizeTableRef(explorerSchema, RevisionsTable) +
			" SET status = 'ACTIVE', activated_at = $2, failed_at = NULL, diagnostics = $3, publication = $4" +
			" WHERE id = $1 AND status = 'READY' RETURNING " + strings.Join(revisionColumns, ", "),
		ownerActivate: "UPDATE " + ownerTable + " SET active_revision_id = $3, updated_at = $4" +
			" WHERE project = $1 AND explorer_id = $2 RETURNING " + ownerCols,
	}
}

// PublishAuthoring atomically commits the verified receipt, the immutable
// revision, the active Explorer owner pointer, and the project release
// activation in one PostgreSQL transaction, mirroring the Arango baseline
// (internal/backends/arango/internal/explorer/publication.go) leg for leg, and returns the
// revision as PostgreSQL stored it. The caller has already verified the
// ClickHouse rows: this method touches no outside data plane and accepts no
// callbacks. See the file contract for the full guarantee list; the typed
// refusals are:
//
//   - explorer.ErrCorruptReceipt  -- the claimed receipt row disagrees with
//     the caller's canonical receipt (or is internally corrupt);
//   - explorer.ErrNotFound        -- a claimed row vanished mid-claim;
//   - explorer.ErrImmutableRevision -- the revision id was reused with
//     different frozen content;
//   - dataset.ErrReleaseActivationConflict -- candidate not persisted or no
//     longer matching, manifest missing or not staged, no active generation
//     or the pointer names another generation, a lost pointer CAS, or a
//     stale expected revision;
//   - explorer.ErrDraftConflict   -- owner missing or foreign, management
//     mode mismatch, candidate out of scope or outside the admissible
//     status set, dangling owner active pointer, or a lost activation write;
//   - ErrInvalidPublicationInput  -- refused before the transaction.
//
// A replay of an already-durable publish (same canonical receipt, same
// revision content, owner already active on it, pointer already on the
// candidate) is the zero-write converged success.
func (p *PublishStore) PublishAuthoring(ctx context.Context, receipt explorer.CompilationReceipt, revision explorer.Revision, release dataset.ProjectRelease, expectedReleaseRevision int64) (*explorer.Revision, error) {
	if err := validateReceiptForStore(receipt); err != nil {
		return nil, fmt.Errorf("postgres explorer store: publish authoring: %w", err)
	}
	if strings.TrimSpace(revision.ID) == "" {
		return nil, fmt.Errorf("%w: publish authoring: revision ID is required", ErrInvalidPublicationInput)
	}
	if revision.Status != explorer.RevisionReady {
		return nil, fmt.Errorf("%w: publish authoring: revision %q carries status %q, only a READY revision may be published",
			ErrInvalidPublicationInput, revision.ID, string(revision.Status))
	}
	if expectedReleaseRevision < 0 {
		return nil, fmt.Errorf("%w: publish authoring: expected release revision %d is negative", ErrInvalidPublicationInput, expectedReleaseRevision)
	}
	contentDigest, err := revisionContentDigest(revision)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: publish authoring: content identity of revision %q: %w", revision.ID, err)
	}
	payloads, err := encodeRevisionPayloads(revision)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: publish authoring: encode revision %q: %w", revision.ID, err)
	}
	revisionDiag, err := marshalOrNil(revision.Diagnostics)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: publish authoring: encode revision %q diagnostics: %w", revision.ID, err)
	}
	receiptDoc, err := json.Marshal(receipt)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: publish authoring: encode compilation receipt %q: %w", receipt.ID, err)
	}
	releasePayload, err := marshalPublishReleasePayload(release)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: publish authoring: encode project release %q: %w", release.ID, err)
	}

	var lastErr error
	for attempt := 1; attempt <= publishTxMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("postgres explorer store: publish authoring: %w", err)
		}
		out, runErr := p.runPublishAuthoring(ctx, receipt, receiptDoc, revision, payloads, revisionDiag, contentDigest, release, releasePayload, expectedReleaseRevision)
		if runErr == nil {
			return out, nil
		}
		if isTypedPublicationRefusal(runErr) {
			// A typed application refusal: retrying cannot change the
			// verdict because it did not come from lock contention.
			// Classification is by sentinel -- never by the statement
			// that produced the error -- so no lock-statement backend
			// fault can hide behind a leg's position.
			return nil, runErr
		}
		lastErr = runErr
		if !isDeadlockError(runErr) || attempt == publishTxMaxAttempts {
			break
		}
		if waitErr := waitForStoreRetry(ctx, time.Duration(attempt)*publishDeadlockRetry); waitErr != nil {
			return nil, fmt.Errorf("postgres explorer store: publish authoring retry: %w", errors.Join(lastErr, waitErr))
		}
	}
	if lastErr == nil {
		lastErr = ErrPublishContention
	}
	if isDeadlockError(lastErr) {
		// The retry budget was exhausted on genuine PostgreSQL deadlock
		// victims: a contention outcome, named as one, wrapping the last
		// backend error. Never presented as an application conflict.
		return nil, fmt.Errorf("postgres explorer store: publish authoring: %w: %w", ErrPublishContention, lastErr)
	}
	return nil, fmt.Errorf("postgres explorer store: publish authoring: %w", lastErr)
}

// runPublishAuthoring runs one full publish transaction on one connection.
// It does not decide retryability: the caller classifies every error by its
// sentinel (isTypedPublicationRefusal), so a backend fault surfacing from
// any statement -- including the FOR UPDATE lock reads -- stays visible to
// the bounded deadlock retry.
func (p *PublishStore) runPublishAuthoring(
	ctx context.Context,
	receipt explorer.CompilationReceipt, receiptDoc []byte,
	revision explorer.Revision, payloads revisionPayloads, revisionDiag []byte, contentDigest string,
	release dataset.ProjectRelease, releasePayload string,
	expectedReleaseRevision int64,
) (out *explorer.Revision, err error) {
	conn, err := p.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire publish connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin publish transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	crash := func(step string) error {
		if p.crashAt != nil {
			if err := p.crashAt(ctx, tx, step); err != nil {
				return fmt.Errorf("injected fault at %s: %w", step, err)
			}
		}
		return nil
	}

	// -- Leg 1: verified receipt (claim order: receipt row first). ----------
	if _, err := p.claimReceiptForPublish(ctx, tx, receipt, receiptDoc); err != nil {
		return nil, err
	}
	if err := crash("receipt-insert"); err != nil {
		return nil, err
	}
	// The receipt is stored and verified; the revision's compilation_receipt_id
	// binding is the caller's contract, exactly as in the baseline.

	// -- Leg 2: immutable revision candidate (claim order: candidate row). --
	candidateRow, err := p.claimRevisionForPublish(ctx, tx, revision, payloads, revisionDiag, contentDigest)
	if err != nil {
		return nil, err
	}
	storedRevision, err := decodeRevision(*candidateRow)
	if err != nil {
		return nil, err
	}
	if err := crash("revision-insert"); err != nil {
		return nil, err
	}

	// -- Leg 3: release activation CAS, release-lane lock order: candidate,
	//    manifest, generation pointer, release pointer. --------------------
	if _, err := p.activateReleaseInTx(ctx, tx, release, releasePayload, expectedReleaseRevision); err != nil {
		if isTypedReleaseConflict(err) {
			return nil, err
		}
		return nil, err
	}
	if err := crash("release-pointer"); err != nil {
		return nil, err
	}

	// -- Leg 4: activation guard + writes (owner row, then prior row). ------
	owner, err := p.readPublishOwner(ctx, tx, storedRevision)
	if err != nil {
		return nil, err
	}
	replay := storedRevision.Status == explorer.RevisionActive &&
		owner.ActiveRevisionID == storedRevision.ID
	if replay {
		// Zero-write converged replay: the state machine refuses any
		// ACTIVE->ACTIVE churn, and the row is already the requested end
		// state. The release CAS above still ran under its own rules.
		if err := crash("pre-commit"); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit publish transaction: %w", err)
		}
		committed = true
		return storedRevision, nil
	}

	if storedRevision.Status != explorer.RevisionReady {
		// Admissible statuses are READY, or ACTIVE only under the owner
		// (the replay branch above); anything else is the baseline's
		// guard-miss ErrDraftConflict, indistinguishable by cause.
		return nil, explorer.ErrDraftConflict
	}

	if owner.ActiveRevisionID != "" && owner.ActiveRevisionID != storedRevision.ID {
		prior, err := p.lockPriorRevision(ctx, tx, owner, storedRevision)
		if err != nil {
			return nil, err
		}
		var superseded storedRevisionRow
		if err := scanRevisionRow(tx.QueryRow(ctx, p.stmts.priorSupersede, prior.ID), &superseded); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// The prior row moved out of ACTIVE between the locked read
				// and this CAS -- unreachable while this transaction holds
				// the row lock; refusing keeps the guard honest.
				return nil, fmt.Errorf("%w: prior revision %q moved before it could be superseded", explorer.ErrDraftConflict, prior.ID)
			}
			return nil, fmt.Errorf("supersede prior revision %q: %w", prior.ID, err)
		}
		if err := crash("prior-supersede"); err != nil {
			return nil, err
		}
	}

	activatedPublication, err := activatedPublicationPayload(candidateRow)
	if err != nil {
		return nil, err
	}
	activatedAt := time.Now().UTC()
	var activated storedRevisionRow
	if err := scanRevisionRow(tx.QueryRow(ctx, p.stmts.candidateActive,
		storedRevision.ID, activatedAt, []byte(publishDiagnosticsSet), []byte(activatedPublication)), &activated); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: candidate revision %q moved before it could be activated", explorer.ErrDraftConflict, storedRevision.ID)
		}
		return nil, fmt.Errorf("activate candidate revision %q: %w", storedRevision.ID, err)
	}
	if err := crash("candidate-activate"); err != nil {
		return nil, err
	}

	var activatedOwner explorer.Explorer
	if err := scanOwner(tx.QueryRow(ctx, p.stmts.ownerActivate,
		storedRevision.Project, storedRevision.ExplorerID, storedRevision.ID, time.Now().UTC()), &activatedOwner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: owner vanished before activation", explorer.ErrDraftConflict)
		}
		return nil, fmt.Errorf("activate explorer owner %q/%q: %w", storedRevision.Project, storedRevision.ExplorerID, err)
	}
	if err := crash("owner-activate"); err != nil {
		return nil, err
	}

	// The activated row is the returned revision: what PostgreSQL
	// persisted, exactly as the baseline re-reads the stored document.
	out, err = decodeRevision(activated)
	if err != nil {
		return nil, err
	}
	if err := crash("pre-commit"); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit publish transaction: %w", err)
	}
	committed = true
	return out, nil
}

// claimReceiptForPublish runs the leg-1 claim: insert-once with the
// tenant-scoped FOR UPDATE reread, verified the same way
// InsertCompilationReceipt verifies a stored receipt. Unlike the standalone
// insert, a winner-abort inside this transaction retries the insert a
// bounded number of times before surfacing; a digest mismatch is the
// corrupt-ID reuse refusal and a vanished row is explorer.ErrNotFound.
func (p *PublishStore) claimReceiptForPublish(ctx context.Context, tx pgx.Tx, receipt explorer.CompilationReceipt, doc []byte) (*explorer.CompilationReceipt, error) {
	wantKey, err := canonicalReceiptKey(receipt)
	if err != nil {
		return nil, fmt.Errorf("content identity of compilation receipt %q: %w", receipt.ID, err)
	}
	for attempt := 1; attempt <= receiptInsertMaxAttempts; attempt++ {
		var row storedReceipt
		err := scanReceipt(tx.QueryRow(ctx, p.stmts.receiptInsert, compilationReceiptArgs(receipt, doc)...), &row)
		if err == nil {
			stored, decodeErr := decodeStoredReceipt(row)
			if decodeErr != nil {
				return nil, decodeErr
			}
			storedKey, keyErr := canonicalReceiptKey(*stored)
			if keyErr != nil || storedKey != wantKey {
				return nil, fmt.Errorf("%w: stored receipt %q content identity disagrees with the claim", explorer.ErrCorruptReceipt, row.ID)
			}
			return stored, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("claim compilation receipt %q: %w", receipt.ID, err)
		}
		existing, err := p.readReceiptRowInTx(ctx, tx, receipt.Project, receipt.ExplorerID, receipt.ID)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			stored, decodeErr := decodeStoredReceipt(*existing)
			if decodeErr != nil {
				return nil, decodeErr
			}
			storedKey, keyErr := canonicalReceiptKey(*stored)
			if keyErr != nil || storedKey != wantKey {
				return nil, fmt.Errorf("%w: receipt id %q was reused with different content", explorer.ErrCorruptReceipt, receipt.ID)
			}
			return stored, nil
		}
		// The winner aborted before this reread: retry the insert (the row
		// is gone, not conflicting).
	}
	return nil, fmt.Errorf("%w: receipt claim %q/%q/%q vanished before it became visible", explorer.ErrNotFound, receipt.Project, receipt.ExplorerID, receipt.ID)
}

// claimRevisionForPublish runs the leg-2 claim: insert-once, FOR UPDATE
// reread on conflict, and the exact-content replay guard -- same
// content_digest is the baseline's UPSERT UPDATE {} no-op, a different
// digest is the typed explorer.ErrImmutableRevision refusal with the stored
// row untouched. The physically stored row is returned so the activation
// legs work from what PostgreSQL persisted, never from the caller's struct.
func (p *PublishStore) claimRevisionForPublish(ctx context.Context, tx pgx.Tx, revision explorer.Revision, payloads revisionPayloads, revisionDiag []byte, contentDigest string) (*storedRevisionRow, error) {
	args := revisionArgs(revision, payloads, contentDigest, revisionDiag)
	for attempt := 1; attempt <= revisionInsertMaxAttempts; attempt++ {
		var row storedRevisionRow
		err := scanRevisionRow(tx.QueryRow(ctx, p.stmts.revisionInsert, args...), &row)
		if err == nil {
			return &row, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("claim revision %q: %w", revision.ID, err)
		}
		var existing storedRevisionRow
		err = scanRevisionRow(tx.QueryRow(ctx, p.stmts.revisionClaim, revision.ID), &existing)
		if errors.Is(err, pgx.ErrNoRows) {
			continue // winner aborted: retry the insert
		}
		if err != nil {
			return nil, fmt.Errorf("claim revision %q: %w", revision.ID, err)
		}
		if existing.ContentDigest != contentDigest {
			return nil, fmt.Errorf("%w: revision id %q was reused with different content (stored %s, requested %s)",
				explorer.ErrImmutableRevision, revision.ID, existing.ContentDigest, contentDigest)
		}
		return &existing, nil
	}
	return nil, fmt.Errorf("%w: revision claim %q vanished before it became visible", explorer.ErrNotFound, revision.ID)
}

// activateReleaseInTx is the publish-path release activation CAS -- the
// CompareAndSwapPublishRelease sibling of the release lane's
// CompareAndSwapActivateRelease (internal/backends/arango/internal/dataset/release_store.go):
// same rules, plus the current-generation guard that makes "the active
// generation pointer still identifies this release's source manifest" a
// precondition. It runs entirely on the caller's pgx.Tx against the release
// and generation lanes' own tables -- no new schema, no write to the
// generation lane (whose pointer table carries no release columns by
// design, per the release lane's OnReleaseActivated zero-write ruling).
func (p *PublishStore) activateReleaseInTx(ctx context.Context, tx pgx.Tx, release dataset.ProjectRelease, releasePayload string, expectedRevision int64) (dataset.ActiveRelease, error) {
	// (a) The candidate must be a persisted, still-matching immutable
	//     document. FOR SHARE holds it against concurrent modification.
	var (
		storedPayload string
		matches       bool
	)
	err := tx.QueryRow(ctx, p.stmts.releaseCandidateLock,
		release.Project, release.ID, releasePayload).Scan(&storedPayload, &matches)
	if errors.Is(err, pgx.ErrNoRows) {
		return dataset.ActiveRelease{}, fmt.Errorf("%w: candidate %s/%s is not a persisted project release",
			dataset.ErrReleaseActivationConflict, release.Project, release.ID)
	}
	if err != nil {
		return dataset.ActiveRelease{}, fmt.Errorf("lock release candidate %s/%s: %w", release.Project, release.ID, err)
	}
	if !matches {
		return dataset.ActiveRelease{}, fmt.Errorf("%w: candidate %s/%s no longer matches the persisted document",
			dataset.ErrReleaseActivationConflict, release.Project, release.ID)
	}
	candidate, err := publishReleaseFromRow(release.Project, release.ID, release.Generation, release.GitCommit, storedPayload)
	if err != nil {
		return dataset.ActiveRelease{}, err
	}

	// (b) The generation manifest must exist in a publicly staged state
	//     (STAGED, or the retained legacy READY spelling), locked FOR SHARE.
	var state string
	err = tx.QueryRow(ctx, p.stmts.manifestLock, release.Project, release.Generation).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return dataset.ActiveRelease{}, fmt.Errorf("%w: generation %s/%s has no persisted manifest",
			dataset.ErrReleaseActivationConflict, release.Project, release.Generation)
	}
	if err != nil {
		return dataset.ActiveRelease{}, fmt.Errorf("lock generation manifest %s/%s: %w", release.Project, release.Generation, err)
	}
	if state != string(dataset.StateStaged) && state != string(dataset.StateReady) {
		return dataset.ActiveRelease{}, fmt.Errorf("%w: generation %s/%s manifest is %s, not %s or %s",
			dataset.ErrReleaseActivationConflict, release.Project, release.Generation, state,
			string(dataset.StateStaged), string(dataset.StateReady))
	}

	// (c) The publish-path current-generation guard: the active-generation
	//     pointer must exist and name this generation, locked FOR SHARE so a
	//     concurrent generation transition cannot slip between check and
	//     commit.
	var activeGeneration string
	err = tx.QueryRow(ctx, p.stmts.generationPointer, release.Project).Scan(&activeGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return dataset.ActiveRelease{}, fmt.Errorf("%w: project %s has no active generation",
			dataset.ErrReleaseActivationConflict, release.Project)
	}
	if err != nil {
		return dataset.ActiveRelease{}, fmt.Errorf("read active generation for %s: %w", release.Project, err)
	}
	if activeGeneration != release.Generation {
		return dataset.ActiveRelease{}, fmt.Errorf("%w: active generation of %s is %s, not the candidate generation %s",
			dataset.ErrReleaseActivationConflict, release.Project, activeGeneration, release.Generation)
	}

	// (d) The release pointer CAS, locked first, F-B15 already-active
	//     short-circuit taking precedence over the expected revision.
	var (
		pointerReleaseID string
		currentRevision  int64
	)
	lockErr := tx.QueryRow(ctx, p.stmts.releasePointerLock, release.Project).Scan(&pointerReleaseID, &currentRevision)
	if lockErr != nil && !errors.Is(lockErr, pgx.ErrNoRows) {
		return dataset.ActiveRelease{}, fmt.Errorf("lock active release pointer for %s: %w", release.Project, lockErr)
	}
	if lockErr == nil {
		if pointerReleaseID == release.ID {
			return dataset.ActiveRelease{Release: candidate, Revision: currentRevision}, nil
		}
		if currentRevision != expectedRevision {
			return dataset.ActiveRelease{}, fmt.Errorf("%w: candidate %s/%s expected active revision %d, found %d",
				dataset.ErrReleaseActivationConflict, release.Project, release.ID, expectedRevision, currentRevision)
		}
		var committedRevision int64
		casErr := tx.QueryRow(ctx, p.stmts.releasePointerCAS,
			release.Project, release.ID, expectedRevision+1, expectedRevision).Scan(&committedRevision)
		if errors.Is(casErr, pgx.ErrNoRows) {
			return dataset.ActiveRelease{}, fmt.Errorf("%w: active release pointer moved during activation", dataset.ErrReleaseActivationConflict)
		}
		if casErr != nil {
			return dataset.ActiveRelease{}, fmt.Errorf("move active release pointer for %s: %w", release.Project, casErr)
		}
		if committedRevision != expectedRevision+1 {
			return dataset.ActiveRelease{}, fmt.Errorf("%w: active release revision committed as %d, want %d",
				datasetpostgres.ErrUnexpectedStoreResult, committedRevision, expectedRevision+1)
		}
		return dataset.ActiveRelease{Release: candidate, Revision: committedRevision}, nil
	}

	// No pointer row: only an expected-revision-0 caller may create one.
	if expectedRevision != 0 {
		return dataset.ActiveRelease{}, fmt.Errorf("%w: candidate %s/%s expected active revision %d, found no active release",
			dataset.ErrReleaseActivationConflict, release.Project, release.ID, expectedRevision)
	}
	tag, err := tx.Exec(ctx, p.stmts.releasePointerInsert, release.Project, release.ID)
	if err != nil {
		return dataset.ActiveRelease{}, fmt.Errorf("create active release pointer for %s: %w", release.Project, err)
	}
	if tag.RowsAffected() == 1 {
		return dataset.ActiveRelease{Release: candidate, Revision: 1}, nil
	}
	// A concurrent activation created the pointer first; the reread sees it
	// once the ON CONFLICT wait resolves. Naming this very candidate is the
	// accepted zero-write end state; anything else is the lost race.
	var concurrentReleaseID string
	var concurrentRevision int64
	rereadErr := tx.QueryRow(ctx, p.stmts.releasePointerRead, release.Project).Scan(&concurrentReleaseID, &concurrentRevision)
	if errors.Is(rereadErr, pgx.ErrNoRows) {
		return dataset.ActiveRelease{}, fmt.Errorf("%w: active release pointer vanished during activation", datasetpostgres.ErrUnexpectedStoreResult)
	}
	if rereadErr != nil {
		return dataset.ActiveRelease{}, fmt.Errorf("reread active release pointer for %s: %w", release.Project, rereadErr)
	}
	if concurrentReleaseID != release.ID {
		return dataset.ActiveRelease{}, fmt.Errorf("%w: candidate %s/%s expected no active release, found %s at revision %d (concurrent activation)",
			dataset.ErrReleaseActivationConflict, release.Project, release.ID, concurrentReleaseID, concurrentRevision)
	}
	return dataset.ActiveRelease{Release: candidate, Revision: concurrentRevision}, nil
}

// readPublishOwner is the activation guard's owner read: one scoped
// statement with the expected management mode inside the WHERE, so owner
// missing, owner in another tenant, and mode mismatch all return zero rows
// and the same indistinguishable explorer.ErrDraftConflict -- the baseline
// activationReadAQL's FILTER block rendered as a locked relational read.
func (p *PublishStore) readPublishOwner(ctx context.Context, tx pgx.Tx, candidate *explorer.Revision) (explorer.Explorer, error) {
	management := explorer.ManagementInteractive
	if candidate.ExplorerID == "default" {
		management = explorer.ManagementRepository
	}
	var owner explorer.Explorer
	err := scanOwner(tx.QueryRow(ctx, p.stmts.ownerGuard,
		candidate.Project, candidate.ExplorerID, string(management)), &owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return explorer.Explorer{}, explorer.ErrDraftConflict
	}
	if err != nil {
		return explorer.Explorer{}, fmt.Errorf("read owner guard for %q/%q: %w", candidate.Project, candidate.ExplorerID, err)
	}
	// The candidate must be the owner's tenant's own candidate: a revision
	// row whose scope disagrees with the guard is the same guard miss.
	if owner.Project != candidate.Project || owner.ExplorerID != candidate.ExplorerID {
		return explorer.Explorer{}, explorer.ErrDraftConflict
	}
	return owner, nil
}

// lockPriorRevision locks the owner's current active revision FOR UPDATE and
// verifies the fail-closed preconditions: it must exist, sit in the same
// project/Explorer scope as the owner, and be ACTIVE -- the baseline
// silently skipped a missing prior; the relational store refuses to activate
// over a dangling pointer instead.
func (p *PublishStore) lockPriorRevision(ctx context.Context, tx pgx.Tx, owner explorer.Explorer, candidate *explorer.Revision) (storedRevisionRow, error) {
	var prior storedRevisionRow
	err := scanRevisionRow(tx.QueryRow(ctx, p.stmts.priorLock, owner.ActiveRevisionID), &prior)
	if errors.Is(err, pgx.ErrNoRows) {
		return storedRevisionRow{}, fmt.Errorf("%w: owner active revision %q is not a stored revision",
			explorer.ErrDraftConflict, owner.ActiveRevisionID)
	}
	if err != nil {
		return storedRevisionRow{}, fmt.Errorf("lock prior revision %q: %w", owner.ActiveRevisionID, err)
	}
	if prior.Project != owner.Project || prior.ExplorerID != owner.ExplorerID ||
		prior.Project != candidate.Project || prior.ExplorerID != candidate.ExplorerID {
		return storedRevisionRow{}, fmt.Errorf("%w: owner active revision %q is foreign to %q/%q",
			explorer.ErrDraftConflict, owner.ActiveRevisionID, owner.Project, owner.ExplorerID)
	}
	if prior.Status != string(explorer.RevisionActive) {
		return storedRevisionRow{}, fmt.Errorf("%w: owner active revision %q is %s, not ACTIVE",
			explorer.ErrDraftConflict, owner.ActiveRevisionID, prior.Status)
	}
	return prior, nil
}

// activatedPublicationPayload renders the activation publication document
// from the STORED candidate's raw publication jsonb, mirroring the
// baseline's activatedPublication exactly: every stored key is carried
// over, then state/revisionId/updatedAt are overwritten, and generation is
// defaulted from source_generation only when the stored document carries no
// generation key at all (an explicit null survives, as it does in Arango's
// ok-map check). The document is computed in Go from the locked row -- the
// single source of truth for the shape; SQL only stores the bound
// parameter, never re-derives it.
func activatedPublicationPayload(candidate *storedRevisionRow) (string, error) {
	// The stored publication is CHECKed to be a jsonb object, so a missing
	// or non-object document is a store fault, never a silent repair.
	var stored map[string]json.RawMessage
	if len(candidate.Publication) == 0 {
		return "", fmt.Errorf("%w: candidate revision %q has no stored publication document",
			datasetpostgres.ErrUnexpectedStoreResult, candidate.ID)
	}
	if err := json.Unmarshal(candidate.Publication, &stored); err != nil || stored == nil {
		return "", fmt.Errorf("%w: candidate revision %q publication document is undecodable: %v",
			datasetpostgres.ErrUnexpectedStoreResult, candidate.ID, err)
	}
	updated := make(map[string]json.RawMessage, len(stored)+3)
	for key, value := range stored {
		updated[key] = value
	}
	now := time.Now().UTC()
	stateValue, err := json.Marshal(string(explorer.RevisionActive))
	if err != nil {
		return "", err
	}
	revisionValue, err := json.Marshal(candidate.ID)
	if err != nil {
		return "", err
	}
	updatedValue, err := json.Marshal(now)
	if err != nil {
		return "", err
	}
	updated["state"] = stateValue
	updated["revisionId"] = revisionValue
	updated["updatedAt"] = updatedValue
	if _, present := stored["generation"]; !present && candidate.SourceGeneration != "" {
		generationValue, err := json.Marshal(candidate.SourceGeneration)
		if err != nil {
			return "", err
		}
		updated["generation"] = generationValue
	}
	encoded, err := json.Marshal(updated)
	if err != nil {
		return "", fmt.Errorf("encode activation publication for revision %q: %w", candidate.ID, err)
	}
	return string(encoded), nil
}

// marshalPublishReleasePayload encodes the caller's candidate for the
// release lane's content equality parameter. It mirrors the release lane's
// own storage encoding exactly (nil slices normalized to empty, then the
// plain domain JSON) so the SQL-side jsonb equality
// (payload - 'createdAt') = ($3 - 'createdAt') compares canonical content
// with the same rule the release store applied when the row was saved; the
// canonicalization itself lives in Go, never in SQL trim/lower
// approximations.
func marshalPublishReleasePayload(release dataset.ProjectRelease) (string, error) {
	normalized := release
	if normalized.Publications == nil {
		normalized.Publications = []dataset.ReleasePublication{}
	}
	if normalized.RequiredVerifications == nil {
		normalized.RequiredVerifications = []dataset.ContractVerification{}
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("encode project release payload: %w", err)
	}
	return string(encoded), nil
}

// publishReleaseFromRow rebuilds the verified candidate from the stored
// row, mirroring the release lane's readback contract (authoritative
// payload, strict decoding, scan-column agreement) without reaching into
// the release store's internals. Disagreement is a store fault surfaced
// for the caller, never coerced.
func publishReleaseFromRow(project, id, generation, gitCommit, storedPayload string) (dataset.ProjectRelease, error) {
	var release dataset.ProjectRelease
	decoder := json.NewDecoder(strings.NewReader(storedPayload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&release); err != nil {
		return dataset.ProjectRelease{}, fmt.Errorf("%w: stored release payload is malformed: %v", datasetpostgres.ErrUnexpectedStoreResult, err)
	}
	if release.ID == "" {
		return dataset.ProjectRelease{}, fmt.Errorf("%w: stored release candidate id is required", datasetpostgres.ErrUnexpectedStoreResult)
	}
	ref := dataset.Ref{Project: release.Project, Generation: release.Generation}
	if err := ref.Validate(); err != nil {
		return dataset.ProjectRelease{}, fmt.Errorf("%w: stored release is invalid: %v", datasetpostgres.ErrUnexpectedStoreResult, err)
	}
	if release.GitCommit == "" {
		return dataset.ProjectRelease{}, fmt.Errorf("%w: stored release gitCommit is required", datasetpostgres.ErrUnexpectedStoreResult)
	}
	for _, binding := range release.Publications {
		if err := binding.Selector.Validate(); err != nil {
			return dataset.ProjectRelease{}, fmt.Errorf("%w: stored release publication selector %v: %v",
				datasetpostgres.ErrUnexpectedStoreResult, binding.Selector, err)
		}
	}
	for _, verification := range release.RequiredVerifications {
		if err := verification.Selector.Validate(); err != nil {
			return dataset.ProjectRelease{}, fmt.Errorf("%w: stored release verification selector %v: %v",
				datasetpostgres.ErrUnexpectedStoreResult, verification.Selector, err)
		}
	}
	if release.Project != project || release.ID != id || release.Generation != generation || release.GitCommit != gitCommit {
		return dataset.ProjectRelease{}, fmt.Errorf("%w: stored release scan columns disagree with its payload for %s/%s",
			datasetpostgres.ErrUnexpectedStoreResult, project, id)
	}
	return release, nil
}

// readReceiptRowInTx runs the leg-1 reread on the publish transaction. A
// missing row yields (nil, nil): the caller chooses the missing-row meaning.
func (p *PublishStore) readReceiptRowInTx(ctx context.Context, tx pgx.Tx, project, explorerID, id string) (*storedReceipt, error) {
	var row storedReceipt
	err := scanReceipt(tx.QueryRow(ctx, p.stmts.receiptClaim, project, explorerID, id), &row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("read compilation receipt %q/%q/%q: %w", project, explorerID, id, err)
	}
	return &row, nil
}

// isTypedReleaseConflict reports a refusal carrying the domain activation
// conflict sentinel: those verdicts are final (done=true), while any other
// error from the release legs is a backend fault eligible only for the
// deadlock retry path.
func isTypedReleaseConflict(err error) bool {
	return errors.Is(err, dataset.ErrReleaseActivationConflict) ||
		errors.Is(err, datasetpostgres.ErrUnexpectedStoreResult)
}

// isTypedPublicationRefusal reports an error carrying one of the publish
// transaction's typed application refusals. Refusals are terminal (a retry
// re-runs the same verdict); anything else is a contention/backend fault and
// stays visible to the bounded deadlock retry, no matter which statement --
// including the FOR UPDATE lock reads of the documented lock order --
// produced it.
func isTypedPublicationRefusal(err error) bool {
	return errors.Is(err, explorer.ErrCorruptReceipt) ||
		errors.Is(err, explorer.ErrNotFound) ||
		errors.Is(err, explorer.ErrImmutableRevision) ||
		errors.Is(err, explorer.ErrDraftConflict) ||
		isTypedReleaseConflict(err)
}

// isDeadlockError reports a PostgreSQL deadlock_detected victim: the only
// fault class PublishAuthoring retries on a fresh transaction.
func isDeadlockError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == publishDeadlockState
}
