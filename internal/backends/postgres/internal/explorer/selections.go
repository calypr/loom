package postgres

// Staged selection revisions on PostgreSQL (pg-state-selection-revision-owner-v1).
//
// This file is the header/owner half of the relational counterpart of
// internal/backends/arango/internal/explorer/selections.go: BeginSelection canonicalizes and
// validates the revision identity, reclaims only expired staging leases,
// then binds the project-scoped idempotency key. A matching retry is
// idempotent; another identity or an active writer conflicts. GetSelection,
// owner-conditional abort, and bounded staging cleanup mirror the Arango
// adapter method for method so TestSelectionStorage-AgainPostgres can hold
// the same guarantees against real PG18. The membership half -- the
// insert-once batch Append, the paged Digest, the fenced finalize Complete,
// and the keyset Visit -- lives in selection_members.go.
//
// Database-enforced invariants (DDL in
// 000008_selection_revisions.sql, now part of this package's embedded
// Explorer control-plane history; this file contains no DDL):
//   - selection_revisions PRIMARY KEY (id) mirrors Arango's global document
//     key, so one selection ID is exactly one revision.
//   - UNIQUE (project, idempotency_key) is the project-scoped idempotency
//     arbiter: competing same-key/same-content begins converge on the
//     winner's row.
//   - Guard triggers abort identity tampering and completed-fact tampering
//     on the header, and any UPDATE of a staged member, with SQLSTATE
//     45501 before the write runs.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/calypr/loom/internal/explorer"
)

const (
	// SelectionRevisionsTable holds staged and complete selection revision
	// headers. It is the counterpart of Arango's SelectionsCollection.
	SelectionRevisionsTable = "selection_revisions"
	// SelectionMembersTable holds insert-once member rows staged under a
	// revision. It is the counterpart of Arango's SelectionMembersCollection.
	SelectionMembersTable = "selection_members"

	selectionStagingState  = "STAGING"
	selectionCompleteState = "COMPLETE"
	// selectionWriterLease is the writer-ownership duration, identical to
	// the Arango adapter's 10-minute lease. Begin sets it; Append renews it.
	selectionWriterLease = 10 * time.Minute
	// selectionCleanupLimit bounds one staging-cleanup batch, as Arango's
	// startup callers budget cleanup work rather than scanning everything.
	selectionCleanupLimit = 100
)

// selectionColumns is the single source of truth for the header read/write
// column order. Ordered SQL parameters ($1..$N) therefore match each call's
// argument slice by construction, and scanSelection decodes in the same
// order.
var selectionColumns = []string{
	"id",
	"project",
	"generation",
	"resource_type",
	"rule",
	"source",
	"exclusions",
	"scope_digest",
	"rule_digest",
	"membership_digest",
	"member_count",
	"member_bytes",
	"complete",
	"idempotency_key",
	"created_at",
	"completed_at",
}

// selectionWriteColumns is the insert column list: the caller-provided
// columns in selectionColumns order plus the adapter-owned staging fields.
// state is always STAGING at begin; writer_token/writer_expires_at carry the
// expiring writer owner.
var selectionWriteColumns = append([]string{"state", "writer_token", "writer_expires_at"}, selectionColumns...)

// selectionReadColumns is the read list: the decoded columns plus the two
// writer columns the lifecycle checks inspect (never surfaced to callers).
var selectionReadColumns = append(append([]string{}, selectionColumns...), "writer_token", "writer_expires_at")

// selectionSQL is the ordered, schema-qualified statement set the selection
// lifecycle executes. Statements are built once in New for the schema the
// Store targets and reuse the column-order sources above.
type selectionSQL struct {
	// beginInsert binds a new staged revision. Both arbiters (id primary
	// key and the project-scoped idempotency key unique) resolve to
	// DO NOTHING: the caller's post-insert reread classifies whether this
	// was an idempotent retry or a conflicting reuse.
	beginInsert string
	// headerByID rereads a revision by its global id with the writer
	// columns included.
	headerByID string
	// headerScoped is the project-scoped GetSelection lookup: a foreign
	// project reading its own id never sees another project's row.
	headerScoped string
	// keyOwner resolves which id currently holds (project,
	// idempotency_key) after an insert was skipped by the arbiter. An
	// empty key never matches: the unique arbiter exists only over
	// non-empty keys (mirroring Arango, where an omitted idempotencyKey
	// attribute matches nothing), so keyless selections coexist.
	keyOwner string
	// leaseClaim rebinds the writer of an UNEXPIRED row only after the
	// lease boundary: PostgreSQL itself evaluates
	// writer_expires_at < $now at write time, so an unexpired writer can
	// never be claimed no matter which caller races.
	leaseClaim string
	// headerForAppend locks the header for the append and fenced-complete
	// transactions so cleanup and abort cannot yank the row (or its
	// members) mid-batch. selection_members.go drives it from both.
	headerForAppend string
	// cleanupVictims selects expired STAGING headers bounded by limit,
	// locking them so abort/append/complete cannot interleave mid-batch.
	cleanupVictims string
	// cleanupMembers/cleanupHeaders remove exactly the locked victim set.
	cleanupMembers string
	cleanupHeaders string
	// abortMembers/abortHeader remove an owned staging revision's rows;
	// the caller has already verified ownership under the row lock.
	abortMembers string
	abortHeader  string
}

func newSelectionSQL(schema string) selectionSQL {
	headers := pgx.Identifier{schema, SelectionRevisionsTable}.Sanitize()
	members := pgx.Identifier{schema, SelectionMembersTable}.Sanitize()
	readCols := strings.Join(selectionReadColumns, ", ")
	writePlaceholders := make([]string, len(selectionWriteColumns))
	for i := range selectionWriteColumns {
		writePlaceholders[i] = fmt.Sprintf("$%d", i+1)
	}
	return selectionSQL{
		beginInsert: "INSERT INTO " + headers + " (" + strings.Join(selectionWriteColumns, ", ") + ") VALUES (" +
			strings.Join(writePlaceholders, ", ") + ") ON CONFLICT DO NOTHING",
		headerByID:   "SELECT " + readCols + " FROM " + headers + " WHERE id = $1",
		headerScoped: "SELECT " + readCols + " FROM " + headers + " WHERE id = $1 AND project = $2",
		keyOwner: "SELECT id FROM " + headers +
			" WHERE project = $1 AND idempotency_key = $2 AND $2 <> ''",
		leaseClaim: "UPDATE " + headers + " SET writer_token = $2, writer_expires_at = $3" +
			" WHERE id = $1 AND state = '" + selectionStagingState + "'" +
			" AND writer_expires_at < $4" +
			" RETURNING " + readCols,
		headerForAppend: "SELECT writer_token, state, complete, project, generation, resource_type" +
			" FROM " + headers + " WHERE id = $1 FOR UPDATE",
		cleanupVictims: "SELECT id FROM " + headers +
			" WHERE state = '" + selectionStagingState + "' AND created_at < $1 AND writer_expires_at < $2" +
			" ORDER BY created_at, id LIMIT $3 FOR UPDATE",
		cleanupMembers: "DELETE FROM " + members + " WHERE selection_id = ANY($1)",
		cleanupHeaders: "DELETE FROM " + headers + " WHERE id = ANY($1)",
		abortMembers:   "DELETE FROM " + members + " WHERE selection_id = $1",
		abortHeader:    "DELETE FROM " + headers + " WHERE id = $1",
	}
}

// selectionRow is the full stored header: the domain revision plus the two
// writer columns the lifecycle inspects. writerExpires is zero when the
// column is SQL NULL (an owner that has already lost its lease record).
type selectionRow struct {
	revision      explorer.SelectionRevision
	writerToken   string
	writerExpires time.Time
}

// scanSelection decodes one selectionReadColumns row. The jsonb columns
// cross as json.RawMessage: SQL NULL yields a nil (missing) payload, the
// jsonb 'null' stays distinct, and completed_at decodes through a pointer
// so missing and a concrete instant never collapse.
func scanSelection(row rowScanner) (selectionRow, error) {
	var out selectionRow
	var ruleRaw, sourceRaw, exclusionsRaw json.RawMessage
	var completedAt *time.Time
	targets := []any{
		&out.revision.ID,
		&out.revision.Project,
		&out.revision.Generation,
		&out.revision.ResourceType,
		&ruleRaw,
		&sourceRaw,
		&exclusionsRaw,
		&out.revision.ScopeDigest,
		&out.revision.RuleDigest,
		&out.revision.MembershipDigest,
		&out.revision.MemberCount,
		&out.revision.MemberBytes,
		&out.revision.Complete,
		&out.revision.IdempotencyKey,
		&out.revision.CreatedAt,
		&completedAt,
		&out.writerToken,
		&out.writerExpires,
	}
	if err := row.Scan(targets...); err != nil {
		return selectionRow{}, err
	}
	out.revision.CompletedAt = completedAt
	if err := json.Unmarshal(ruleRaw, &out.revision.Rule); err != nil {
		return selectionRow{}, fmt.Errorf("postgres explorer store: decode selection rule: %w", err)
	}
	if err := json.Unmarshal(sourceRaw, &out.revision.Source); err != nil {
		return selectionRow{}, fmt.Errorf("postgres explorer store: decode selection source: %w", err)
	}
	if len(exclusionsRaw) > 0 {
		if err := json.Unmarshal(exclusionsRaw, &out.revision.Exclusions); err != nil {
			return selectionRow{}, fmt.Errorf("postgres explorer store: decode selection exclusions: %w", err)
		}
	}
	out.revision = out.revision.Canonical()
	return out, nil
}

// selectionArgs maps the canonical revision plus its staging fields to
// selectionWriteColumns order. The writer lease is computed by the caller so
// begin and its reread share one instant.
func selectionArgs(s explorer.SelectionRevision, ruleJSON, sourceJSON, exclusionsJSON []byte, writerToken string, expiresAt time.Time) []any {
	return []any{
		selectionStagingState,
		writerToken,
		expiresAt,
		s.ID,
		s.Project,
		s.Generation,
		s.ResourceType,
		ruleJSON,
		sourceJSON,
		exclusionsJSON,
		s.ScopeDigest,
		s.RuleDigest,
		s.MembershipDigest,
		s.MemberCount,
		s.MemberBytes,
		s.Complete,
		s.IdempotencyKey,
		s.CreatedAt,
		nil, // completed_at: a revision always begins uncompleted
	}
}

// BeginSelection is the staged-revision gate, exact counterpart of the
// Arango adapter's BeginSelection. It canonicalizes and validates the
// identity, reclaims only expired staging leases (CleanupSelectionStaging
// never touches an unexpired writer), then binds the project-scoped
// idempotency key:
//
//  1. INSERT ... ON CONFLICT DO NOTHING: both arbiters (id primary key and
//     UNIQUE (project, idempotency_key)) skip a competing concurrent
//     insert instead of failing, so same-key/same-content racers converge
//     on the single winner's row.
//  2. REREAD the stored row (acceptance: identity is reread after
//     conflict). A row under the requested id whose stored identity differs
//     in any of project, generation, resource type, rule digest, scope
//     digest, or idempotency key is explorer.ErrSelectionConflict -- the
//     idempotency key never rebinds a different intent. If no row carries
//     the id, the key-owner lookup resolves who holds (project,
//     idempotency_key): any answer is a foreign id and conflicts.
//  3. A COMPLETE stored row returns as-is (matching retry after completion
//     is idempotent). Otherwise the stored writer token must equal the
//     caller's token; if not, the lease may be re-bound only by the
//     database-evaluated boundary UPDATE writer_expires_at < now, which
//     makes an unexpired writer unclaimable and expired recovery follow the
//     current lease boundary. Zero claimed rows is ErrSelectionConflict.
//
// The stored row -- what PostgreSQL persisted -- is decoded and returned on
// every success path.
func (s *Store) BeginSelection(ctx context.Context, selection explorer.SelectionRevision, writerToken string) (*explorer.SelectionRevision, error) {
	selection = selection.Canonical()
	writerToken = strings.TrimSpace(writerToken)
	if writerToken == "" {
		return nil, explorer.ErrSelectionConflict
	}
	if err := selection.Validate(); err != nil {
		return nil, err
	}
	if selection.Complete {
		return nil, fmt.Errorf("selection must begin incomplete")
	}
	// A crashed creator leaves a staging header behind. Reap only expired
	// leases, in a bounded batch, before idempotency lookup so a retry with
	// the same key can claim a clean immutable revision. Active writers are
	// protected by writer_expires_at and are never touched here.
	if err := s.CleanupSelectionStaging(ctx, time.Now().UTC(), selectionCleanupLimit); err != nil {
		return nil, err
	}
	ruleJSON, err := json.Marshal(selection.Rule)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: encode selection rule: %w", err)
	}
	sourceJSON, err := json.Marshal(selection.Source)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: encode selection source: %w", err)
	}
	var exclusionsJSON []byte
	if selection.Exclusions != nil {
		exclusionsJSON, err = json.Marshal(selection.Exclusions)
		if err != nil {
			return nil, fmt.Errorf("postgres explorer store: encode selection exclusions: %w", err)
		}
	}
	now := time.Now().UTC()

	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire begin-selection connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, s.selections.beginInsert,
		selectionArgs(selection, ruleJSON, sourceJSON, exclusionsJSON, writerToken, now.Add(selectionWriterLease))...); err != nil {
		return nil, fmt.Errorf("postgres explorer store: bind selection revision %q/%q: %w",
			selection.Project, selection.ID, err)
	}

	stored, err := s.readSelectionRow(ctx, conn, s.selections.headerByID, selection.ID)
	if errors.Is(err, explorer.ErrSelectionNotFound) {
		// The insert was skipped by an arbiter and no row carries this id:
		// someone else already holds (project, idempotency_key) under a
		// different id. Resolve the holder so the conflict is a decision,
		// not a guess -- and an id that exists under another key still
		// conflicts above via the identity comparison.
		var holder string
		holderErr := conn.QueryRow(ctx, s.selections.keyOwner, selection.Project, selection.IdempotencyKey).Scan(&holder)
		if errors.Is(holderErr, pgx.ErrNoRows) {
			// The row vanished between insert and reread (concurrent abort
			// or cleanup): the retry boundary the Arango adapter shares.
			return nil, explorer.ErrSelectionNotFound
		}
		if holderErr != nil {
			return nil, fmt.Errorf("postgres explorer store: resolve selection key owner %q/%q: %w",
				selection.Project, selection.ID, holderErr)
		}
		return nil, explorer.ErrSelectionConflict
	}
	if err != nil {
		return nil, err
	}

	// Reread-after-conflict identity check, mirroring the Arango adapter:
	// any disagreement in project, generation, resource type, rule digest,
	// scope digest, or idempotency key between the request and what is
	// actually stored is a conflict. The key never authorizes different
	// intent.
	if stored.revision.Project != selection.Project ||
		stored.revision.Generation != selection.Generation ||
		stored.revision.ResourceType != selection.ResourceType ||
		stored.revision.RuleDigest != selection.RuleDigest ||
		stored.revision.ScopeDigest != selection.ScopeDigest ||
		stored.revision.IdempotencyKey != selection.IdempotencyKey {
		return nil, explorer.ErrSelectionConflict
	}
	if stored.revision.Complete {
		if err := stored.revision.Validate(); err != nil {
			return nil, err
		}
		revision := stored.revision
		return &revision, nil
	}
	if strings.TrimSpace(stored.writerToken) == writerToken {
		revision := stored.revision
		return &revision, nil
	}

	// A foreign token may take over only after the lease boundary, and the
	// boundary is evaluated by PostgreSQL at write time, not by this
	// adapter's read: an unexpired writer can never be claimed, and a
	// racing reclaim that expires mid-flight is decided by the row itself.
	claimed, err := s.readSelectionRowTx(ctx, conn, s.selections.leaseClaim,
		selection.ID, writerToken, now.Add(selectionWriterLease), now)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, explorer.ErrSelectionConflict
	}
	if err != nil {
		return nil, err
	}
	revision := claimed.revision
	return &revision, nil
}

// AbortSelection removes an owned staging revision with its members, exact
// counterpart of the Arango adapter's owner-conditional query: ownership
// means the row exists, is not COMPLETE, and carries the calling writer's
// token. A foreign token -- or an abort racing a live writer's append --
// is a no-op that leaves every row of the live writer intact, witnessed
// round after round. The header is locked FOR UPDATE before any deletion,
// so members and header vanish as one atomic unit or not at all.
func (s *Store) AbortSelection(ctx context.Context, selectionID, writerToken string) error {
	selectionID = strings.TrimSpace(selectionID)
	writerToken = strings.TrimSpace(writerToken)
	if selectionID == "" {
		return nil
	}
	if writerToken == "" {
		return explorer.ErrSelectionConflict
	}

	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres explorer store: acquire abort-selection connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres explorer store: begin abort selection %q: %w", selectionID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var storedToken string
	var complete bool
	err = tx.QueryRow(ctx, s.selections.headerForAppend, selectionID).Scan(&storedToken, new(string), &complete, new(string), new(string), new(string))
	if errors.Is(err, pgx.ErrNoRows) {
		// Missing header: the Arango adapter's removed=false, a no-op.
		return tx.Commit(ctx)
	}
	if err != nil {
		return fmt.Errorf("postgres explorer store: lock selection header %q for abort: %w", selectionID, err)
	}
	if complete || strings.TrimSpace(storedToken) != writerToken {
		// Not the owner (or already complete): nothing is removed, and no
		// error is raised -- stale retries must never disturb a live
		// writer's staged state.
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, s.selections.abortMembers, selectionID); err != nil {
		return fmt.Errorf("postgres explorer store: remove staged members of selection %q: %w", selectionID, err)
	}
	if _, err := tx.Exec(ctx, s.selections.abortHeader, selectionID); err != nil {
		return fmt.Errorf("postgres explorer store: remove selection header %q: %w", selectionID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres explorer store: commit abort selection %q: %w", selectionID, err)
	}
	return nil
}

// GetSelection returns one revision through the project-scoped lookup: a
// foreign project asking for an id that exists elsewhere sees the same
// explorer.ErrSelectionNotFound as a missing id. A stored COMPLETE row is
// fully validated before it is surfaced; a STAGING row round-trips as the
// incomplete revision it is.
func (s *Store) GetSelection(ctx context.Context, project, selectionID string) (*explorer.SelectionRevision, error) {
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire get-selection connection: %w", err)
	}
	defer conn.Release()

	stored, err := s.readSelectionRow(ctx, conn, s.selections.headerScoped, strings.TrimSpace(selectionID), strings.TrimSpace(project))
	if err != nil {
		return nil, err
	}
	if stored.revision.Complete {
		if err := stored.revision.Validate(); err != nil {
			return nil, err
		}
	}
	revision := stored.revision
	return &revision, nil
}

// CleanupSelectionStaging removes bounded abandoned staging revisions --
// STAGING rows older than the caller's bound whose writer lease has
// expired -- together with their member rows in ONE transaction, so an
// abandoned revision never leaves orphan membership behind and a live
// writer's row (or partial batch) is never touched. The victim set is
// locked FOR UPDATE before either delete, which serializes cleanup against
// append/abort/complete racing the same rows; limit <= 0 falls back to the
// bounded default exactly like the Arango adapter.
func (s *Store) CleanupSelectionStaging(ctx context.Context, before time.Time, limit int) error {
	if limit <= 0 {
		limit = selectionCleanupLimit
	}
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres explorer store: acquire cleanup connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres explorer store: begin cleanup staging: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, s.selections.cleanupVictims, before.UTC(), time.Now().UTC(), limit)
	if err != nil {
		return fmt.Errorf("postgres explorer store: select cleanup victims: %w", err)
	}
	var victims []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("postgres explorer store: scan cleanup victims: %w", err)
		}
		victims = append(victims, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("postgres explorer store: iterate cleanup victims: %w", err)
	}
	rows.Close()

	if len(victims) > 0 {
		if _, err := tx.Exec(ctx, s.selections.cleanupMembers, victims); err != nil {
			return fmt.Errorf("postgres explorer store: remove abandoned members: %w", err)
		}
		if _, err := tx.Exec(ctx, s.selections.cleanupHeaders, victims); err != nil {
			return fmt.Errorf("postgres explorer store: remove abandoned headers: %w", err)
		}
	}

	// Test-only seam: exercises a crash between the member/header delete
	// pair and the commit of one cleanup batch. Always nil in production;
	// an error here aborts the transaction, so the abandoned revision and
	// its members survive TOGETHER -- cleanup converges as one atomic
	// unit, never half-reaped.
	if s.beforeCleanupCommit != nil {
		if err := s.beforeCleanupCommit(ctx, tx); err != nil {
			return fmt.Errorf("postgres explorer store: cleanup staging before commit: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres explorer store: commit cleanup staging: %w", err)
	}
	return nil
}

// readSelectionRow runs one header read and decodes it, mapping no-rows to
// explorer.ErrSelectionNotFound (the concealed-missing classification the
// Arango adapter's readSelection shares).
func (s *Store) readSelectionRow(ctx context.Context, conn selectionQuerier, query string, args ...any) (selectionRow, error) {
	stored, err := scanSelection(conn.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return selectionRow{}, explorer.ErrSelectionNotFound
	}
	if err != nil {
		return selectionRow{}, fmt.Errorf("postgres explorer store: read selection revision: %w", err)
	}
	return stored, nil
}

// readSelectionRowTx is readSelectionRow for statements that must observe
// what PostgreSQL actually wrote: it returns the raw pgx.ErrNoRows so
// callers can distinguish "the guard refused the write" from "the row is
// missing" instead of collapsing both into not-found.
func (s *Store) readSelectionRowTx(ctx context.Context, conn selectionQuerier, query string, args ...any) (selectionRow, error) {
	stored, err := scanSelection(conn.QueryRow(ctx, query, args...))
	if err != nil {
		return selectionRow{}, fmt.Errorf("postgres explorer store: write-and-reread selection revision: %w", err)
	}
	return stored, nil
}

// selectionQuerier is the query surface shared by *pgxpool.Conn and pgx.Tx,
// so the header reads run unchanged on a pooled connection or inside the
// append/abort transactions.
type selectionQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// The PostgreSQL adapter now covers the selection half of
// explorer.Store: staged identity, project-scoped idempotency, the expiring
// writer owner, insert-once membership, the completion CAS, owner-conditional
// abort, and bounded staging cleanup.
