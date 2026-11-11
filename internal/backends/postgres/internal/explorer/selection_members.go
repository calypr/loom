package postgres

// Staged selection membership on PostgreSQL (pg-state-selection-members-v1).
//
// This file is the membership half of the relational counterpart of
// internal/backends/arango/internal/explorer/selections.go: the header/owner half (Begin,
// Get, owner-conditional Abort, bounded staging Cleanup) lives in
// selections.go, while Append, Digest, Complete, and Visit -- the methods
// the Arango adapter's member lifecycle exposes -- live here method for
// method so TestSelectionStorageAgainstPostgres and the member suite can
// hold the same guarantees against real PG18:
//
//   - Append stages a bounded batch of members in ONE transaction through
//     insert-once rows: a retry creates no duplicates, a batch carrying a
//     foreign reference is refused before any row of it exists, and a
//     member whose stored reference disagrees with its derived key is
//     refused by the database arbiter and classified as a conflict.
//   - Complete finalizes the digest and count under the current writer
//     fence: the completing transaction locks the header, verifies the
//     calling writer token and STAGING state, re-streams the staged
//     membership itself, and only flips the row when the claimed digest
//     and count agree with what is actually staged. A digest/count
//     mismatch never completes: the revision stays STAGING, which every
//     reader path (Visit, frozen-fact Digest) refuses as unusable.
//   - Visit streams at most one bounded page of COMPLETE membership per
//     call over a keyset walk, honors cancellation between members, and
//     never materializes the full member list.
//
// Database-enforced invariants (DDL in
// 000008_selection_revisions.sql, now part of this package's embedded
// Explorer control-plane history; this file contains no DDL):
//   - selection_members PRIMARY KEY (selection_id, member_key) plus UNIQUE
//     (selection_id, project, generation, resource_type, id) make member
//     staging insert-once and retries idempotent, exactly like Arango's
//     immutable member keys.
//   - The member guard trigger refuses ANY UPDATE of a staged member with
//     SQLSTATE 45501 before the write runs; rows are retired by whole-row
//     DELETE only (abort and cleanup), never rewritten.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/calypr/loom/internal/explorer"
)

const (
	// selectionDigestPageSize pages membership streaming for
	// DigestSelectionMembers, the fenced Complete re-stream, and the
	// default Visit page, matching Arango's 1000-row pages.
	selectionDigestPageSize = 1000
	// selectionMembersRefConstraint names the database arbiter that keeps
	// one reference exactly one member per selection: UNIQUE
	// (selection_id, project, generation, resource_type, id) in
	// 000008_selection_revisions.sql. The
	// adapter classifies a violation of THIS constraint as a conflicting
	// member instead of surfacing a raw driver error.
	selectionMembersRefConstraint = "selection_members_ref_ux"
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

// memberColumns is the single source of truth for member read/write order.
var memberColumns = []string{
	"member_key",
	"project",
	"generation",
	"resource_type",
	"id",
	"ordinal",
}

// selectionMemberSQL is the ordered, schema-qualified statement set the
// membership lifecycle executes: insert-once staging, the keyset pages the
// digest stream and the visit walk run, the lease renewal Append performs,
// and the completion CAS the fenced finalize commits. Statements are built
// once in New for the schema the Store targets.
type selectionMemberSQL struct {
	// memberInsert stages one member insert-once; a returning row means
	// this call created it, no row means the key already existed. A
	// violation of selection_members_ref_ux is refused by the database
	// and classified by the adapter as a conflicting member.
	memberInsert string
	// memberByKey reads one stored member row by its primary key. The
	// append skip path uses it to verify that an already-present key
	// stores exactly the reference its derived key promises: a stored
	// row whose reference disagrees with the key is a conflicting
	// member, refused rather than silently treated as staged.
	memberByKey string
	// memberDigestPage streams one membership frame page in byte-order id
	// (COLLATE "C" columns, so this is Arango's byte-order SORT). The
	// equality prefix plus the id range/order is exactly the
	// selection_members_ref_ux index shape.
	memberDigestPage string
	// memberVisitPage pages members by byte-order member_key, mirroring
	// Arango's m._key > @after walk.
	memberVisitPage string
	// leaseRenew extends the writer lease only for the matching writer on
	// a still-staged row, mirroring Arango's writer-token filter.
	leaseRenew string
	// completeCAS is the completion CAS: it flips a row to COMPLETE with
	// its membership facts only while it is still STAGING under the
	// calling writer's token. The fenced finalize has already verified the
	// claimed facts against the staged membership under the header lock;
	// the token and state are re-evaluated by PostgreSQL at write time.
	completeCAS string
}

func newSelectionMemberSQL(schema string) selectionMemberSQL {
	members := pgx.Identifier{schema, SelectionMembersTable}.Sanitize()
	headers := pgx.Identifier{schema, SelectionRevisionsTable}.Sanitize()
	readCols := strings.Join(selectionReadColumns, ", ")
	return selectionMemberSQL{
		memberInsert: "INSERT INTO " + members +
			" (selection_id, " + strings.Join(memberColumns, ", ") + ")" +
			" VALUES (" + orderedPlaceholders(len(memberColumns)+1) + ")" +
			" ON CONFLICT (selection_id, member_key) DO NOTHING" +
			" RETURNING " + strings.Join(memberColumns, ", "),
		memberByKey: "SELECT " + strings.Join(memberColumns, ", ") + " FROM " + members +
			" WHERE selection_id = $1 AND member_key = $2",
		memberDigestPage: "SELECT project, generation, resource_type, id FROM " + members +
			" WHERE selection_id = $1 AND project = $2 AND generation = $3 AND resource_type = $4 AND id > $5" +
			" ORDER BY id LIMIT $6",
		memberVisitPage: "SELECT " + strings.Join(memberColumns, ", ") + " FROM " + members +
			" WHERE selection_id = $1 AND project = $2 AND member_key > $3" +
			" ORDER BY member_key LIMIT $4",
		leaseRenew: "UPDATE " + headers + " SET writer_expires_at = $2" +
			" WHERE id = $1 AND writer_token = $3 AND state = '" + selectionStagingState + "'",
		completeCAS: "UPDATE " + headers + " SET state = '" + selectionCompleteState + "', complete = true," +
			" membership_digest = $2, member_count = $3, member_bytes = $4, completed_at = $5" +
			" WHERE id = $1 AND state = '" + selectionStagingState + "' AND writer_token = $6" +
			" RETURNING " + readCols,
	}
}

// streamMemberFacts walks one revision's staged membership in byte-order id
// (the digest frame order), pages of at most selectionDigestPageSize rows,
// and returns the frozen-frame digest, the member count, and the same
// 8+len(value) byte total the Arango adapter reports. It is ONE
// implementation shared by DigestSelectionMembers and the fenced
// CompleteSelection, so the facts a caller may finalize are exactly the
// facts a reader may digest -- there is no second digest path to drift.
// The caller supplies the header's scoped identity; the walk never buffers
// more than one page. The test-only memberPageStreamed observer (always nil
// in production) sees every fetched page, which is how the suite proves the
// walk is bounded and keyset-driven rather than a full materialization.
func (s *Store) streamMemberFacts(ctx context.Context, q selectionQuerier, project, generation, resourceType, selectionID string) (string, int64, int64, error) {
	hash := sha256.New()
	var count, byteTotal int64
	afterID := ""
	for {
		pageCount := 0
		rows, err := q.Query(ctx, s.selectionMembers.memberDigestPage,
			selectionID, project, generation, resourceType, afterID, selectionDigestPageSize)
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
		if s.memberPageStreamed != nil {
			s.memberPageStreamed(pageCount)
		}
		if pageCount < selectionDigestPageSize {
			break
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), count, byteTotal, nil
}

// isMemberRefConflict reports whether an insert failed on the named
// database arbiter that keeps one reference exactly one member per
// selection: UNIQUE (selection_id, project, generation, resource_type, id).
// Reaching it means a stored member row carries this batch's reference
// under a different derived key -- a conflicting member, which the adapter
// refuses with explorer.ErrSelectionConflict rather than surfacing a raw
// driver error.
func isMemberRefConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == selectionMembersRefConstraint
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
//
// Batch retry is idempotent without duplicates: ON CONFLICT on the
// (selection_id, member_key) primary key means an already-staged member
// returns no row and is NOT reported as created by this call, and a
// reference repeated inside one batch is created exactly once. A member
// that conflicts -- a caller-supplied memberKey that disagrees with the
// derived key, a reference outside the header's scope, a stored row
// holding this reference under another key (the selection_members_ref_ux
// arbiter), or a stored row holding this key while its reference
// disagrees with what the key promises -- refuses with
// explorer.ErrSelectionConflict and zero rows of the batch surviving.
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
		err := tx.QueryRow(ctx, s.selectionMembers.memberInsert,
			selectionID, entry.memberKey, entry.member.Ref.Project, entry.member.Ref.Generation,
			entry.member.Ref.ResourceType, entry.member.Ref.ID, entry.member.Ordinal).
			Scan(&memberKey, &mProject, &mGeneration, &mResourceType, &mID, &ordinal)
		if errors.Is(err, pgx.ErrNoRows) {
			// The key already existed. An idempotent retry stores the
			// same reference under the same derived key, so verify that
			// the stored row agrees with the reference this key promises
			// before crediting it as staged: a stored row holding this
			// key with a DIFFERENT reference is a conflicting member,
			// refused exactly like the reference-arbiter shape.
			var skippedKey, sProject, sGeneration, sResourceType, sID string
			var sOrdinal int64
			scanErr := tx.QueryRow(ctx, s.selectionMembers.memberByKey, selectionID, entry.memberKey).
				Scan(&skippedKey, &sProject, &sGeneration, &sResourceType, &sID, &sOrdinal)
			if errors.Is(scanErr, pgx.ErrNoRows) {
				// Unreachable while this transaction holds the header
				// lock (abort and cleanup must take it before removing
				// members). If it ever fires, this call created nothing.
				continue
			}
			if scanErr != nil {
				return nil, fmt.Errorf("postgres explorer store: reread staged member %q of selection %q: %w",
					entry.memberKey, selectionID, scanErr)
			}
			if skippedKey != entry.memberKey ||
				sProject != entry.member.Ref.Project || sGeneration != entry.member.Ref.Generation ||
				sResourceType != entry.member.Ref.ResourceType || sID != entry.member.Ref.ID {
				return nil, explorer.ErrSelectionConflict
			}
			continue
		}
		if err != nil {
			if isMemberRefConflict(err) {
				// The database arbiter refused this reference under
				// another member key: a conflicting member, decided by
				// the schema rather than guessed by the adapter. The
				// deferred rollback vanishes every row this batch
				// already inserted.
				return nil, explorer.ErrSelectionConflict
			}
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
	tag, err := tx.Exec(ctx, s.selectionMembers.leaseRenew, selectionID, renewAt, writerToken)
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
	return s.streamMemberFacts(ctx, conn, project, header.revision.Generation, header.revision.ResourceType, selectionID)
}

// CompleteSelection is the completion CAS, exact counterpart of the Arango
// adapter's owner filter (STAGING under the calling writer's token) plus
// this ticket's finalize-under-the-fence guarantee. Everything runs in ONE
// transaction:
//
//  1. The header is locked FOR UPDATE and its writer token plus STAGING
//     state are verified: only the active writer may complete. A foreign
//     token, or a row that is already COMPLETE, never flips anything --
//     COMPLETE is classified from the stored row (identical claimed facts
//     are the idempotent retry, different facts are a conflict).
//  2. Under that lock the adapter RE-STREAMS the staged membership
//     through the same paged walk DigestSelectionMembers uses and compares
//     the caller's claimed digest and count against what is actually
//     staged. A mismatch is explorer.ErrSelectionIncomplete and NOTHING is
//     flipped: the revision stays STAGING, which every reader path refuses
//     as unusable (Visit rejects STAGING; the frozen facts a reader gets
//     from a COMPLETE row are only ever written by this CAS). Membership
//     cannot move underneath the comparison -- append and abort must take
//     the same header lock, and members are insert-once rows.
//  3. Only then does the CAS flip the row to COMPLETE with the claimed
//     facts; PostgreSQL re-evaluates the token and state at write time and
//     a zero-row CAS is a conflict, never a silent success.
//
// member_bytes is the caller's accounting hint, like the Arango adapter's
// @bytes bind: it is stored, not verified, and must be non-negative. The
// identity-bearing facts the fence verifies are the digest and the count.
// The guard trigger then makes the frozen facts irreversible at the
// database (completion is one-way).
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

	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: begin complete selection %q: %w", selectionID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The writer fence: hold the header lock across verify-and-flip.
	var storedToken, state string
	var complete bool
	var project, generation, resourceType string
	err = tx.QueryRow(ctx, s.selections.headerForAppend, selectionID).
		Scan(&storedToken, &state, &complete, &project, &generation, &resourceType)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, explorer.ErrSelectionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: lock selection header %q for complete: %w", selectionID, err)
	}
	if complete {
		// The CAS already had its chance to speak; classify from the
		// stored row exactly as the Arango adapter's post-CAS reread
		// does -- identical facts are idempotent, different facts are a
		// conflict.
		stored, err := s.readSelectionRow(ctx, tx, s.selections.headerByID, selectionID)
		if err != nil {
			return nil, err
		}
		if stored.revision.MembershipDigest != digest ||
			stored.revision.MemberCount != count || stored.revision.MemberBytes != bytes {
			return nil, explorer.ErrSelectionConflict
		}
		revision := stored.revision
		return &revision, nil
	}
	if strings.TrimSpace(storedToken) != writerToken {
		return nil, explorer.ErrSelectionConflict
	}
	if state != "" && state != selectionStagingState {
		return nil, explorer.ErrSelectionConflict
	}

	// Finalize under the fence: the claimed digest and count must describe
	// the membership that is actually staged, re-streamed here rather than
	// trusted from the caller. A mismatch leaves the staging unusable and
	// unfinalized.
	stagedDigest, stagedCount, _, err := s.streamMemberFacts(ctx, tx, project, generation, resourceType, selectionID)
	if err != nil {
		return nil, err
	}
	if stagedDigest != digest || stagedCount != count {
		return nil, explorer.ErrSelectionIncomplete
	}

	winner, err := s.readSelectionRowTx(ctx, tx, s.selectionMembers.completeCAS,
		selectionID, digest, count, bytes, completedAt.UTC(), writerToken)
	if errors.Is(err, pgx.ErrNoRows) {
		// Unreachable while the FOR UPDATE lock is held -- the row was
		// just verified STAGING under this token. If it ever fires, the
		// flip was not this writer's to make.
		return nil, explorer.ErrSelectionConflict
	}
	if err != nil {
		return nil, err
	}
	if err := winner.revision.Validate(); err != nil {
		return nil, err
	}

	// Test-only seam: exercises a crash between the completion CAS and the
	// commit of the finalize transaction. Always nil in production; an
	// error here aborts the transaction, so the row stays STAGING with its
	// members intact -- the freeze either commits whole or not at all, and
	// the writer can retry after the crash.
	if s.beforeCompleteCommit != nil {
		if err := s.beforeCompleteCommit(ctx, tx); err != nil {
			return nil, fmt.Errorf("postgres explorer store: complete selection %q before commit: %w",
				selectionID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres explorer store: commit complete selection %q: %w", selectionID, err)
	}
	revision := winner.revision
	return &revision, nil
}

// VisitSelectionMembers visits at most one page of COMPLETE membership per
// call -- exactly one page of at most pageSize members (never more than one
// bounded page, however large the requested pageSize is), mirroring
// arango.VisitSelectionMembers, whose callers (lifecycle readSelectionIntent
// and appendSelectionRevision) gate their own batching on that per-call
// bound: the caller drives the walk and re-invokes with the returned
// cursor; an empty page returns after unchanged. Members are paged by a
// keyset walk in byte-order member_key (the member key starts with the
// selection id, so the walk is Arango's m._key > @after over the same
// 64-hex suffix space); member rows are insert-once and never rewritten, so
// pages cannot shift under the walker, and only one page -- never the full
// member list -- is in memory at a time. The walk is cancellation-aware: a
// cancelled call stops between members and returns the cursor of the last
// member the visitor actually saw, so resuming continues with no gap and no
// duplicate. A STAGING revision is
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
	} else if pageSize > selectionDigestPageSize {
		// One call never materializes more than one bounded page, no
		// matter what page budget a caller asks for.
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
	rows, err := conn.Query(ctx, s.selectionMembers.memberVisitPage, selectionID, strings.TrimSpace(project), last, pageSize)
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
	// The caller's context is honored between members; the cursor names
	// the last member the visitor actually saw, so a cancelled walk
	// resumes from there with no gap and no duplicate.
	for i, member := range page {
		if err := ctx.Err(); err != nil {
			return last, err
		}
		if err := visit(member); err != nil {
			return last, err
		}
		last = keys[i]
	}
	return last, nil
}

// The PostgreSQL adapter's membership half is complete: insert-once
// idempotent batches, the fenced finalize that verifies digest and count
// against the staged membership under the writer fence, and the bounded,
// cancellation-aware keyset visit. The header/owner half lives in
// selections.go.
