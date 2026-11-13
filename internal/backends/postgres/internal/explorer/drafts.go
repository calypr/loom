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

const DraftRevisionsTable = "explorers_draft_revisions"

const duplicateKeySQLState = "23505"

const historyIdentityConstraint = "explorers_draft_revisions_identity_pk"

type draftSQL struct {
	preimageRead  string
	historyInsert string
	ownerCAS      string
	revisionGet   string
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

func draftRevisionKey(project, explorerID string, version int64) string {
	h := sha256.New()
	for _, part := range []string{project, explorerID, strconv.FormatInt(version, 10)} {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(part))
	}
	return "draft_revision_" + hex.EncodeToString(h.Sum(nil))
}

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

	var previous explorer.Explorer
	err = scanOwner(tx.QueryRow(ctx, s.drafts.preimageRead, e.Project, e.ExplorerID, expected, digest), &previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, explorer.ErrDraftConflict
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: read draft preimage %q/%q: %w", e.Project, e.ExplorerID, err)
	}

	revisionID := draftRevisionKey(previous.Project, previous.ExplorerID, previous.DraftVersion)
	_, err = tx.Exec(ctx, s.drafts.historyInsert,
		previous.Project, previous.ExplorerID, previous.DraftVersion, revisionID,
		previous.Title, previous.DraftDigest, jsonParam(previous.DraftConfig),
		previous.DraftSnapshotToken, previous.DraftSourceGeneration,
		previous.DraftAuthorizationScopeDigest, previous.UpdatedBy, previous.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == duplicateKeySQLState && pgErr.ConstraintName == historyIdentityConstraint {
			return nil, errors.Join(explorer.ErrDraftConflict, err)
		}
		return nil, fmt.Errorf("postgres explorer store: persist draft preimage %q/%q version %d: %w",
			e.Project, e.ExplorerID, previous.DraftVersion, err)
	}

	if s.afterHistoryInsert != nil {
		if err := s.afterHistoryInsert(ctx, tx); err != nil {
			return nil, fmt.Errorf("postgres explorer store: save draft %q/%q after history insert: %w",
				e.Project, e.ExplorerID, err)
		}
	}

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

var _ explorer.DraftRevisionReader = (*Store)(nil)
