package postgres

// Staged selection revisions on PostgreSQL (pg-state-selection-revision-owner-v1).
//
// This file is the relational counterpart of internal/explorer/arango/
// selections.go: BeginSelection canonicalizes and validates the revision
// identity, reclaims only expired staging leases, then binds the
// project-scoped idempotency key. A matching retry is idempotent; another
// identity or an active writer conflicts. Membership (append/digest/visit),
// completion CAS, owner-conditional abort, and bounded staging cleanup
// mirror the Arango adapter method for method so TestSelectionStorage-
// AgainstPostgres can hold the same guarantees against real PG18.
//
// Database-enforced invariants (DDL in
// tests/migrations-selections/000008_selection_revisions.sql; this file
// contains no DDL):
//   - selection_revisions PRIMARY KEY (id) mirrors Arango's global document
//     key, so one selection ID is exactly one revision.
//   - UNIQUE (project, idempotency_key) is the project-scoped idempotency
//     arbiter: competing same-key begins converge on the winner's row.
//   - selection_members PRIMARY KEY (selection_id, member_key) plus UNIQUE
//     (selection_id, project, generation, resource_type, id) make member
//     staging insert-once and retries idempotent, exactly like Arango's
//     immutable member keys.
//   - Guard triggers abort identity tampering and completed-fact tampering
//     on the header, and any UPDATE of a staged member, with SQLSTATE
//     45501 before the write runs.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	// selectionDigestPageSize pages membership streaming for
	// DigestSelectionMembers, matching Arango's 1000-row pages.
	selectionDigestPageSize = 1000
)

// selectionMemberKey derives the immutable member key for one reference
// under one selection. It reproduces the Arango adapter's
// selectionMemberKey byte for byte (selectionID + "_" + the shared key()
// hash over project, generation, resource type, and id), so a member carries
// the same identity on either backend.
func selectionMemberKey(selectionID string, ref explorer.ResourceRef) string {
	h := sha256.New()
	for _, part := range []string{ref.Project, ref.Generation, ref.ResourceType, ref.ID} {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(part))
	}
	return selectionID + "_" + hex.EncodeToString(h.Sum(nil))
}

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

// memberColumns is the single source of truth for member read/write order.
var memberColumns = []string{
	"member_key",
	"project",
	"generation",
	"resource_type",
	"id",
	"ordinal",
}

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
	// headerForAppend locks the header for the append transaction so
	// cleanup and abort cannot yank the row (or its members) mid-batch.
	headerForAppend string
	// memberInsert stages one member insert-once; a returning row means
	// this call created it, no row means the key already existed.
	memberInsert string
	// leaseRenew extends the writer lease only for the matching writer on
	// a still-staged row, mirroring Arango's writer-token filter.
	leaseRenew string
	// completeCAS is the completion CAS: it flips a row to COMPLETE with
	// its membership facts only while it is still STAGING under the
	// calling writer's token.
	completeCAS string
	// memberDigestPage streams one membership frame page in byte-order id
	// (COLLATE "C" columns, so this is Arango's byte-order SORT).
	memberDigestPage string
	// memberVisitPage pages members by byte-order member_key, mirroring
	// Arango's m._key > @after walk.
	memberVisitPage string
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
	memberPlaceholders := make([]string, len(memberColumns)+2)
	for i := range memberPlaceholders {
		memberPlaceholders[i] = fmt.Sprintf("$%d", i+1)
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
		memberInsert: "INSERT INTO " + members +
			" (selection_id, member_key, project, generation, resource_type, id, ordinal)" +
			" VALUES (" + strings.Join(memberPlaceholders[:len(memberColumns)+1], ", ") + ")" +
			" ON CONFLICT (selection_id, member_key) DO NOTHING" +
			" RETURNING " + strings.Join(memberColumns, ", "),
		leaseRenew: "UPDATE " + headers + " SET writer_expires_at = $2" +
			" WHERE id = $1 AND writer_token = $3 AND state = '" + selectionStagingState + "'",
		completeCAS: "UPDATE " + headers + " SET state = '" + selectionCompleteState + "', complete = true," +
			" membership_digest = $2, member_count = $3, member_bytes = $4, completed_at = $5" +
			" WHERE id = $1 AND state = '" + selectionStagingState + "' AND writer_token = $6" +
			" RETURNING " + readCols,
		memberDigestPage: "SELECT project, generation, resource_type, id FROM " + members +
			" WHERE selection_id = $1 AND project = $2 AND generation = $3 AND resource_type = $4 AND id > $5" +
			" ORDER BY id LIMIT $6",
		memberVisitPage: "SELECT " + strings.Join(memberColumns, ", ") + " FROM " + members +
			" WHERE selection_id = $1 AND project = $2 AND member_key > $3" +
			" ORDER BY member_key LIMIT $4",
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

// AppendSelectionMembers stages a bounded member batch under the writer's
// lease, exact counterpart of the Arango adapter. Everything happens in ONE
// transaction: the header row is locked FOR UPDATE, its writer token and
// staging state are verified, every member's derived immutable key is
// checked, and only then are the insert-once member rows written and the
// lease renewed for the matching writer. A failed validation or any fault
// after the first member insert (see the afterMemberInsert seam) aborts the
// transaction, so a partially applied batch never leaks members and never
// renews a lease it did not earn (post-abort convergence, pinned against
// real PG18).
func (s *Store) AppendSelectionMembers(ctx context.Context, selectionID, writerToken string, members []explorer.SelectionMember) ([]explorer.SelectionMember, error) {
	selectionID = strings.TrimSpace(selectionID)
	writerToken = strings.TrimSpace(writerToken)
	if selectionID == "" {
		return nil, fmt.Errorf("selection id is required")
	}
	if writerToken == "" {
		return nil, explorer.ErrSelectionConflict
	}
	if len(members) == 0 {
		return nil, nil
	}

	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire append-members connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: begin append members %q: %w", selectionID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var storedToken, state string
	var complete bool
	var project, generation, resourceType string
	err = tx.QueryRow(ctx, s.selections.headerForAppend, selectionID).
		Scan(&storedToken, &state, &complete, &project, &generation, &resourceType)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, explorer.ErrSelectionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: lock selection header %q: %w", selectionID, err)
	}
	if strings.TrimSpace(storedToken) != writerToken {
		return nil, explorer.ErrSelectionConflict
	}
	if complete || (state != "" && state != selectionStagingState) {
		return nil, explorer.ErrSelectionConflict
	}

	// Build and validate the whole batch before the first insert, as the
	// Arango adapter does: a mixed-project member is refused before any
	// row of the batch can exist.
	type pendingMember struct {
		memberKey string
		member    explorer.SelectionMember
	}
	batch := make([]pendingMember, 0, len(members))
	for _, raw := range members {
		member := raw.Canonical()
		expectedMemberKey := selectionMemberKey(selectionID, member.Ref)
		if member.MemberKey != "" && member.MemberKey != expectedMemberKey {
			return nil, explorer.ErrSelectionConflict
		}
		member.MemberKey = expectedMemberKey
		if err := member.Ref.Validate(project, generation, resourceType); err != nil {
			return nil, err
		}
		batch = append(batch, pendingMember{memberKey: expectedMemberKey, member: member})
	}

	inserted := make([]explorer.SelectionMember, 0, len(batch))
	for _, entry := range batch {
		var (
			memberKey, mProject, mGeneration, mResourceType, mID string
			ordinal                                              int64
		)
		err := tx.QueryRow(ctx, s.selections.memberInsert,
			selectionID, entry.memberKey, entry.member.Ref.Project, entry.member.Ref.Generation,
			entry.member.Ref.ResourceType, entry.member.Ref.ID, entry.member.Ordinal).
			Scan(&memberKey, &mProject, &mGeneration, &mResourceType, &mID, &ordinal)
		if errors.Is(err, pgx.ErrNoRows) {
			// The key already existed: an idempotent retry, not a new
			// member. This call did not create it.
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("postgres explorer store: stage member %q of selection %q: %w",
				entry.memberKey, selectionID, err)
		}
		if memberKey != entry.memberKey || mProject != entry.member.Ref.Project ||
			mGeneration != entry.member.Ref.Generation || mResourceType != entry.member.Ref.ResourceType ||
			mID != entry.member.Ref.ID || ordinal != entry.member.Ordinal {
			// The stored row disagrees with what was written in the same
			// statement: an integrity failure of the storage itself.
			return nil, fmt.Errorf("postgres explorer store: staged member %q of selection %q reread mismatch",
				entry.memberKey, selectionID)
		}
		inserted = append(inserted, entry.member)
	}

	// Test-only seam: exercises faults and interleavings after the real
	// member inserts and before the lease renewal inside this same live
	// transaction. Always nil in production (unexported, never assigned
	// outside selections_test.go); an error here aborts the transaction,
	// so both the member rows and the renewal vanish together.
	if s.afterMemberInsert != nil {
		if err := s.afterMemberInsert(ctx, tx); err != nil {
			return nil, fmt.Errorf("postgres explorer store: append members %q after member insert: %w",
				selectionID, err)
		}
	}

	renewAt := time.Now().UTC().Add(selectionWriterLease)
	tag, err := tx.Exec(ctx, s.selections.leaseRenew, selectionID, renewAt, writerToken)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: renew selection writer lease %q: %w", selectionID, err)
	}
	if tag.RowsAffected() != 1 {
		// Unreachable while the FOR UPDATE lock is held; if it ever fires
		// the batch is not the writer's and must not commit.
		return nil, explorer.ErrSelectionConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres explorer store: commit append members %q: %w", selectionID, err)
	}
	return inserted, nil
}

// DigestSelectionMembers streams the membership digest, count, and byte
// total for a revision, mirroring the Arango adapter's indexed paging:
// byte-order id walk with 1000-row pages, the shared WriteMembershipFrame
// framing, and the same 8+len(value) byte accounting. A COMPLETE revision
// returns its frozen facts without re-streaming, so the digest the
// completion CAS stored is exactly what readers get back.
func (s *Store) DigestSelectionMembers(ctx context.Context, project, selectionID string) (string, int64, int64, error) {
	selectionID = strings.TrimSpace(selectionID)
	project = strings.TrimSpace(project)
	if selectionID == "" || project == "" {
		return "", 0, 0, explorer.ErrSelectionNotFound
	}
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return "", 0, 0, fmt.Errorf("postgres explorer store: acquire digest-members connection: %w", err)
	}
	defer conn.Release()

	header, err := s.readSelectionRow(ctx, conn, s.selections.headerScoped, selectionID, project)
	if err != nil {
		return "", 0, 0, err
	}
	if header.revision.Complete {
		return header.revision.MembershipDigest, header.revision.MemberCount, header.revision.MemberBytes, nil
	}

	hash := sha256.New()
	var count, byteTotal int64
	afterID := ""
	for {
		pageCount := 0
		rows, err := conn.Query(ctx, s.selections.memberDigestPage,
			selectionID, project, header.revision.Generation, header.revision.ResourceType,
			afterID, selectionDigestPageSize)
		if err != nil {
			return "", 0, 0, fmt.Errorf("postgres explorer store: stream selection members %q: %w", selectionID, err)
		}
		for rows.Next() {
			var ref explorer.ResourceRef
			if err := rows.Scan(&ref.Project, &ref.Generation, &ref.ResourceType, &ref.ID); err != nil {
				rows.Close()
				return "", 0, 0, fmt.Errorf("postgres explorer store: scan selection member %q: %w", selectionID, err)
			}
			ref = ref.Canonical()
			explorer.WriteMembershipFrame(hash, ref)
			for _, value := range []string{ref.Project, ref.Generation, ref.ResourceType, ref.ID} {
				byteTotal += int64(8 + len(value))
			}
			count++
			pageCount++
			afterID = ref.ID
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return "", 0, 0, fmt.Errorf("postgres explorer store: iterate selection members %q: %w", selectionID, err)
		}
		rows.Close()
		if pageCount < selectionDigestPageSize {
			break
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), count, byteTotal, nil
}

// CompleteSelection is the completion CAS, exact counterpart of the Arango
// adapter: a row flips to COMPLETE with its membership facts only while it
// is still STAGING under the calling writer's token. A zero-row CAS is not
// blindly a failure -- the stored row is reread and classified: already
// COMPLETE with identical facts is idempotent success; COMPLETE with
// different facts is a conflict; still STAGING under this writer is
// ErrSelectionIncomplete (the membership was never completed); anything
// else is a conflict. The guard trigger makes a completed row's facts and
// the STAGING/COMPLETE flip irreversible at the database.
func (s *Store) CompleteSelection(ctx context.Context, selectionID, writerToken, digest string, count, bytes int64, completedAt time.Time) (*explorer.SelectionRevision, error) {
	selectionID = strings.TrimSpace(selectionID)
	writerToken = strings.TrimSpace(writerToken)
	if selectionID == "" || strings.TrimSpace(digest) == "" || count < 0 || bytes < 0 {
		return nil, explorer.ErrSelectionIncomplete
	}
	if writerToken == "" {
		return nil, explorer.ErrSelectionConflict
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}

	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire complete-selection connection: %w", err)
	}
	defer conn.Release()

	winner, err := s.readSelectionRowTx(ctx, conn, s.selections.completeCAS,
		selectionID, digest, count, bytes, completedAt.UTC(), writerToken)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		if err := winner.revision.Validate(); err != nil {
			return nil, err
		}
		revision := winner.revision
		return &revision, nil
	}

	stored, err := s.readSelectionRow(ctx, conn, s.selections.headerByID, selectionID)
	if err != nil {
		// Missing row surfaces ErrSelectionNotFound exactly as the Arango
		// adapter's post-CAS reread does; nothing was completed.
		return nil, err
	}
	if stored.revision.Complete {
		if stored.revision.MembershipDigest != digest ||
			stored.revision.MemberCount != count || stored.revision.MemberBytes != bytes {
			return nil, explorer.ErrSelectionConflict
		}
		revision := stored.revision
		return &revision, nil
	}
	if strings.TrimSpace(stored.writerToken) != writerToken {
		return nil, explorer.ErrSelectionConflict
	}
	return nil, explorer.ErrSelectionIncomplete
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

// VisitSelectionMembers visits at most one page of COMPLETE membership per
// call -- exactly one page of at most pageSize members, mirroring
// arango.VisitSelectionMembers, whose callers (lifecycle readSelectionIntent
// and appendSelectionRevision) gate their own batching on that per-call
// bound: the caller drives the walk and re-invokes with the returned
// cursor; an empty page returns after unchanged. Members are paged in
// byte-order member_key (the member key starts with the selection id, so
// the walk is Arango's m._key > @after over the same 64-hex suffix space).
// A STAGING revision is
// ErrSelectionIncomplete: streaming membership that was never finalized
// into a digest would let callers publish facts no completion CAS ever
// froze.
func (s *Store) VisitSelectionMembers(ctx context.Context, project, selectionID, after string, pageSize int, visit func(explorer.SelectionMember) error) (string, error) {
	if visit == nil {
		return "", fmt.Errorf("selection member visitor is required")
	}
	header, err := s.GetSelection(ctx, project, selectionID)
	if err != nil {
		return "", err
	}
	if !header.Complete {
		return "", explorer.ErrSelectionIncomplete
	}
	if pageSize <= 0 {
		pageSize = selectionDigestPageSize
	}

	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return "", fmt.Errorf("postgres explorer store: acquire visit-members connection: %w", err)
	}
	defer conn.Release()

	last := strings.TrimSpace(after)
	page := make([]explorer.SelectionMember, 0, pageSize)
	keys := make([]string, 0, pageSize)
	rows, err := conn.Query(ctx, s.selections.memberVisitPage, selectionID, strings.TrimSpace(project), last, pageSize)
	if err != nil {
		return last, fmt.Errorf("postgres explorer store: page selection members %q: %w", selectionID, err)
	}
	for rows.Next() {
		var member explorer.SelectionMember
		if err := rows.Scan(&member.MemberKey, &member.Ref.Project, &member.Ref.Generation,
			&member.Ref.ResourceType, &member.Ref.ID, &member.Ordinal); err != nil {
			rows.Close()
			return last, fmt.Errorf("postgres explorer store: scan selection member page %q: %w", selectionID, err)
		}
		member = member.Canonical()
		page = append(page, member)
		keys = append(keys, member.MemberKey)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return last, fmt.Errorf("postgres explorer store: iterate selection member page %q: %w", selectionID, err)
	}
	rows.Close()
	// One page per call, like the Arango adapter: visiting stops at
	// pageSize members and the caller resumes from the returned cursor.
	for i, member := range page {
		if err := visit(member); err != nil {
			return last, err
		}
		last = keys[i]
	}
	return last, nil
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
