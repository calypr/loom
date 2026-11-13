package postgres

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
	SelectionRevisionsTable = "selection_revisions"
	SelectionMembersTable   = "selection_members"

	selectionStagingState  = "STAGING"
	selectionCompleteState = "COMPLETE"
	selectionWriterLease   = 10 * time.Minute
	selectionCleanupLimit  = 100
)

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

var selectionWriteColumns = append([]string{"state", "writer_token", "writer_expires_at"}, selectionColumns...)

var selectionReadColumns = append(append([]string{}, selectionColumns...), "writer_token", "writer_expires_at")

type selectionSQL struct {
	beginInsert     string
	headerByID      string
	headerScoped    string
	keyOwner        string
	leaseClaim      string
	headerForAppend string
	cleanupVictims  string
	cleanupMembers  string
	cleanupHeaders  string
	abortMembers    string
	abortHeader     string
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

type selectionRow struct {
	revision      explorer.SelectionRevision
	writerToken   string
	writerExpires time.Time
}

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
		nil,
	}
}

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
		var holder string
		holderErr := conn.QueryRow(ctx, s.selections.keyOwner, selection.Project, selection.IdempotencyKey).Scan(&holder)
		if errors.Is(holderErr, pgx.ErrNoRows) {
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
		return tx.Commit(ctx)
	}
	if err != nil {
		return fmt.Errorf("postgres explorer store: lock selection header %q for abort: %w", selectionID, err)
	}
	if complete || strings.TrimSpace(storedToken) != writerToken {
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

func (s *Store) readSelectionRowTx(ctx context.Context, conn selectionQuerier, query string, args ...any) (selectionRow, error) {
	stored, err := scanSelection(conn.QueryRow(ctx, query, args...))
	if err != nil {
		return selectionRow{}, fmt.Errorf("postgres explorer store: write-and-reread selection revision: %w", err)
	}
	return stored, nil
}

type selectionQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}
