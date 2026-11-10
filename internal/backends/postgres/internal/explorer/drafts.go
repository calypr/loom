package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/calypr/loom/internal/explorer"
)

// DraftRevisionsTable holds the immutable exact-preimage history rows
// created by SaveDraft, one per accepted (project, explorer_id,
// draft_version). It is created by
// migrations/000003_explorer_draft_revision.sql through the shared
// pg-store-02 runner; this file contains no DDL. Rows are append-only: the
// CAS flow never updates or deletes them, and the primary key makes each
// exact preimage exist at most once, which is the serialization point that
// gives competing same-version writers a single winner.
const DraftRevisionsTable = "explorers_draft_revisions"

// duplicateKeySQLState is PostgreSQL's unique_violation class. A history
// insert that hits explorers_draft_revisions_identity_pk means another
// writer already persisted this exact preimage: this writer lost the race.
const duplicateKeySQLState = "23505"

// historyIdentityConstraint names the primary key whose violation
// classifies a lost CAS race. It is checked together with the SQLSTATE so a
// duplicate on any other constraint (for example a derived-id collision,
// which the schema makes impossible) can never masquerade as the
// single-winner outcome.
const historyIdentityConstraint = "explorers_draft_revisions_identity_pk"

// draftSQL is the ordered, schema-qualified statement set the draft CAS and
// history reads execute. Column order follows ownerColumns where a row is an
// owner row, so scanOwner decodes them, and the $n placeholders match each
// call's argument slice by construction. Statements are built once in New.
type draftSQL struct {
	// preimageRead selects the current owner row only while it still
	// carries the expected CAS version and (when supplied) digest.
	preimageRead string
	// historyInsert appends one exact preimage row.
	historyInsert string
	// ownerCAS applies the winning draft: it writes the candidate's draft
	// fields, bumps draft_version exactly once, and links
	// previous_draft_revision_id -- all only while the row still carries
	// the expected version/digest. It never touches active_revision_id or
	// management_mode, so the published head survives every draft save.
	ownerCAS string
	// revisionGet resolves an opaque revision id inside exactly one
	// project/Explorer pair through the scoped unique index.
	revisionGet string
}

func newDraftSQL(schema string) draftSQL {
	ownerTable := pgx.Identifier{schema, OwnerTable}.Sanitize()
	historyTable := pgx.Identifier{schema, DraftRevisionsTable}.Sanitize()
	cols := strings.Join(ownerColumns, ", ")
	return draftSQL{
		preimageRead: "SELECT " + cols + " FROM " + ownerTable +
			" WHERE project = $1 AND explorer_id = $2 AND draft_version = $3" +
			" AND ($4 = '' OR draft_digest = $4)",
		historyInsert: "INSERT INTO " + historyTable +
			" (project, explorer_id, draft_version, id, title, draft_digest, draft_config," +
			" snapshot_token, source_generation, authorization_scope_digest, updated_by, updated_at)" +
			" VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)",
		ownerCAS: "UPDATE " + ownerTable + " SET" +
			" title = $4, draft_config = $5, draft_digest = $6, draft_snapshot_token = $7," +
			" draft_source_generation = $8, draft_authorization_scope_digest = $9," +
			" last_authoring_command_id = $10, last_authoring_command_digest = $11," +
			" last_authoring_command_results = $12, updated_by = $13, updated_at = $14," +
			" draft_version = $15, previous_draft_revision_id = $16" +
			" WHERE project = $1 AND explorer_id = $2 AND draft_version = $3" +
			" AND ($17 = '' OR draft_digest = $17)" +
			" RETURNING " + cols,
		revisionGet: "SELECT id, project, explorer_id, draft_version, title, draft_digest," +
			" draft_config, snapshot_token, source_generation, authorization_scope_digest," +
			" updated_by, updated_at FROM " + historyTable +
			" WHERE project = $1 AND explorer_id = $2 AND id = $3",
	}
}

// draftRevisionKey derives the opaque id of the exact preimage stored for
// one draft version. It reproduces the Arango adapter's draftRevisionKey
// byte for byte ("draft_revision_" + the shared key() hash over project,
// explorerID, and the decimal version), so the restore command correlates
// the same revision identity on either backend.
func draftRevisionKey(project, explorerID string, version int64) string {
	h := sha256.New()
	for _, part := range []string{project, explorerID, strconv.FormatInt(version, 10)} {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(part))
	}
	return "draft_revision_" + hex.EncodeToString(h.Sum(nil))
}

// SaveDraft is the optimistic compare-and-swap of the PostgreSQL adapter,
// exact counterpart of the Arango adapter's SaveDraft. Everything it writes
// happens inside ONE transaction on one pooled connection:
//
//  1. Read the current owner row under the CAS guard (expected version and,
//     when supplied, expected digest). No match is a stale writer:
//     explorer.ErrDraftConflict, and nothing was written.
//  2. Insert that read row as the immutable exact preimage into
//     explorers_draft_revisions. Its primary key (project, explorer_id,
//     draft_version) is the single-winner arbiter: a competing same-version
//     writer blocks on the unique index and, once the winner commits,
//     receives 23505, which is mapped to explorer.ErrDraftConflict with the
//     *pgconn.PgError cause retained.
//  3. Update the owner row with the candidate's draft fields, set
//     draft_version to expected+1 and previous_draft_revision_id to the
//     preimage's derived id -- under the same CAS guard, re-evaluated at
//     write time. Zero matched rows (the head moved after step 1, e.g. a
//     takeover) is explorer.ErrDraftConflict, and the deferred rollback
//     removes the step-2 history row: CAS and history are one atomic unit,
//     never half-applied.
//
// The adapter owns the monotonic version, updated_at, and the
// previous-revision link: the candidate's DraftVersion, UpdatedAt, and
// PreviousDraftRevisionID are ignored. active_revision_id and
// management_mode are outside the draft patch and are never rewritten. A
// nil candidate DraftConfig clears the draft packet to SQL NULL (missing);
// the JSON-null literal stays distinct. There is no retry, no fallback to
// another backend, and no silently absorbed error: every failure other than
// the two CAS-miss classes is returned wrapped with its statement tag.
func (s *Store) SaveDraft(ctx context.Context, e explorer.Explorer, expected int64, expectedDigest ...string) (*explorer.Explorer, error) {
	digest := ""
	if len(expectedDigest) > 0 {
		digest = strings.TrimSpace(expectedDigest[0])
	}
	e.UpdatedAt = time.Now().UTC()

	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire save-draft connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: begin save draft %q/%q: %w", e.Project, e.ExplorerID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Read the exact preimage under the CAS guard.
	var previous explorer.Explorer
	err = scanOwner(tx.QueryRow(ctx, s.drafts.preimageRead, e.Project, e.ExplorerID, expected, digest), &previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, explorer.ErrDraftConflict
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: read draft preimage %q/%q: %w", e.Project, e.ExplorerID, err)
	}

	// 2. Persist the exact preimage. The identity primary key is the
	// single-winner arbiter for same version/digest races.
	revisionID := draftRevisionKey(previous.Project, previous.ExplorerID, previous.DraftVersion)
	_, err = tx.Exec(ctx, s.drafts.historyInsert,
		previous.Project, previous.ExplorerID, previous.DraftVersion, revisionID,
		previous.Title, previous.DraftDigest, jsonParam(previous.DraftConfig),
		previous.DraftSnapshotToken, previous.DraftSourceGeneration,
		previous.DraftAuthorizationScopeDigest, previous.UpdatedBy, previous.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == duplicateKeySQLState && pgErr.ConstraintName == historyIdentityConstraint {
			// Another writer already stored this version's preimage: this
			// writer lost the race. The domain conflict carries the real
			// PostgreSQL cause for the caller to inspect.
			return nil, errors.Join(explorer.ErrDraftConflict, err)
		}
		return nil, fmt.Errorf("postgres explorer store: persist draft preimage %q/%q version %d: %w",
			e.Project, e.ExplorerID, previous.DraftVersion, err)
	}

	// Test-only seam: exercises faults and interleavings between the real
	// history insert and the real owner CAS inside this same live
	// transaction. Always nil in production (unexported, never assigned
	// outside drafts_test.go); an error here aborts the transaction, so the
	// preimage row and the head update both vanish.
	if s.afterHistoryInsert != nil {
		if err := s.afterHistoryInsert(ctx, tx); err != nil {
			return nil, fmt.Errorf("postgres explorer store: save draft %q/%q after history insert: %w",
				e.Project, e.ExplorerID, err)
		}
	}

	// 3. Apply the winner under the CAS guard. draft_version advances
	// exactly once; the published head is untouched.
	var updated explorer.Explorer
	err = scanOwner(tx.QueryRow(ctx, s.drafts.ownerCAS,
		e.Project, e.ExplorerID, expected,
		e.Title, jsonParam(e.DraftConfig), e.DraftDigest, e.DraftSnapshotToken,
		e.DraftSourceGeneration, e.DraftAuthorizationScopeDigest,
		e.LastAuthoringCommandID, e.LastAuthoringCommandDigest, jsonParam(e.LastAuthoringCommandResults),
		e.UpdatedBy, e.UpdatedAt,
		expected+1, revisionID,
		digest), &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		// The head moved after step 1: the CAS lost and the deferred
		// rollback removes the history row -- half-applied state is
		// impossible.
		return nil, explorer.ErrDraftConflict
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: apply draft CAS %q/%q: %w", e.Project, e.ExplorerID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres explorer store: commit save draft %q/%q: %w", e.Project, e.ExplorerID, err)
	}
	return &updated, nil
}

// GetDraftRevision resolves one immutable preimage for the restore command.
// The opaque revision id grants nothing on its own: lookup is scoped to
// exactly one project and Explorer, so a revision belonging to another
// project, or to another Explorer in the same project, returns the same
// concealed explorer.ErrNotFound as a missing id -- no signal whether the
// id exists elsewhere. Blank parts are never a wildcard: they find nothing.
func (s *Store) GetDraftRevision(ctx context.Context, project, explorerID, revisionID string) (*explorer.DraftRevision, error) {
	project = strings.TrimSpace(project)
	explorerID = strings.TrimSpace(explorerID)
	revisionID = strings.TrimSpace(revisionID)
	if project == "" || explorerID == "" || revisionID == "" {
		return nil, explorer.ErrNotFound
	}

	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire get-draft-revision connection: %w", err)
	}
	defer conn.Release()

	var out explorer.DraftRevision
	err = conn.QueryRow(ctx, s.drafts.revisionGet, project, explorerID, revisionID).Scan(
		&out.ID, &out.Project, &out.ExplorerID, &out.DraftVersion, &out.Title,
		&out.DraftDigest, &out.DraftConfig, &out.SnapshotToken,
		&out.SourceGeneration, &out.AuthorizationScopeDigest, &out.UpdatedBy, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, explorer.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: read draft revision %q/%q/%q: %w",
			project, explorerID, revisionID, err)
	}
	return &out, nil
}

// The PostgreSQL adapter satisfies the narrow history reader the server-side
// restore command uses, alongside the owner methods built in explorers.go.
var _ explorer.DraftRevisionReader = (*Store)(nil)
