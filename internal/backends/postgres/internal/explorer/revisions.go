package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
)

const RevisionsTable = "explorer_revisions"

const revisionInsertMaxAttempts = 4

var (
	ErrStaleRevisionTransition = errors.New("postgres explorer store: stale explorer revision lifecycle token")
	ErrRevisionClaimLost       = errors.New("postgres explorer store: revision insert lost every claim race to an aborted winner")
	ErrInvalidRevision         = errors.New("postgres explorer store: invalid explorer revision")
)

var revisionColumns = []string{
	"id",
	"project",
	"explorer_id",
	"config",
	"authoring_bundle",
	"intent_digest",
	"compilation_receipt_id",
	"public_output_contract",
	"recipe",
	"recipe_digest",
	"resolved_schema_digest",
	"source_generation",
	"materializations",
	"emitted_columns",
	"dataset",
	"quality_reports",
	"content_digest",
	"status",
	"diagnostics",
	"publication",
	"created_by",
	"created_at",
	"ready_at",
	"activated_at",
	"failed_at",
}

type revisionStatements struct {
	insert    string
	claim     string
	get       string
	getScoped string
	failAny   string
	failToken string
}

func newRevisionStatements(schema string) revisionStatements {
	table := sanitizeTableRef(schema, RevisionsTable)
	cols := strings.Join(revisionColumns, ", ")
	placeholders := orderedPlaceholders(len(revisionColumns))
	return revisionStatements{
		insert: "INSERT INTO " + table + " (" + cols + ") VALUES (" + placeholders + ") " +
			"ON CONFLICT (id) DO NOTHING RETURNING " + cols,
		claim:     "SELECT " + cols + " FROM " + table + " WHERE id = $1 FOR UPDATE",
		get:       "SELECT " + cols + " FROM " + table + " WHERE id = $1",
		getScoped: "SELECT " + cols + " FROM " + table + " WHERE project = $1 AND explorer_id = $2 AND id = $3",
		failAny: "UPDATE " + table + " SET status = 'FAILED', diagnostics = $1, failed_at = $2 " +
			"WHERE id = $3 AND status IN ('READY', 'ACTIVE') RETURNING " + cols,
		failToken: "UPDATE " + table + " SET status = 'FAILED', diagnostics = $1, failed_at = $2 " +
			"WHERE id = $3 AND status = $4 AND status IN ('READY', 'ACTIVE') RETURNING " + cols,
	}
}

type frozenRevision struct {
	ID                   string
	Project              string
	ExplorerID           string
	Config               json.RawMessage
	AuthoringBundle      json.RawMessage
	IntentDigest         string
	CompilationReceiptID string
	PublicOutputContract json.RawMessage
	Recipe               recipe.Bundle
	RecipeDigest         string
	ResolvedSchemaDigest string
	SourceGeneration     string
	Materializations     []explorer.Materialization
	EmittedColumns       []explorer.EmittedColumn
	Dataset              explorer.DatasetMetadata
	QualityReports       []publication.QualityReport
}

func digestableRaw(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{"loomRaw":"missing"}`), nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, fmt.Errorf("invalid raw payload %q: %w", string(raw), err)
	}
	if compact.String() == "null" {
		return json.RawMessage(`{"loomRaw":"null"}`), nil
	}
	return json.RawMessage(`{"loomRaw":"document","value":` + compact.String() + `}`), nil
}

func frozenRevisionContent(revision explorer.Revision) (frozenRevision, error) {
	config, err := digestableRaw(revision.Config)
	if err != nil {
		return frozenRevision{}, fmt.Errorf("config: %w", err)
	}
	authoring, err := digestableRaw(revision.AuthoringBundle)
	if err != nil {
		return frozenRevision{}, fmt.Errorf("authoringBundle: %w", err)
	}
	publicContract, err := digestableRaw(revision.PublicOutputContract)
	if err != nil {
		return frozenRevision{}, fmt.Errorf("publicOutputContract: %w", err)
	}
	return frozenRevision{
		ID: revision.ID, Project: revision.Project, ExplorerID: revision.ExplorerID,
		Config:               config,
		AuthoringBundle:      authoring,
		IntentDigest:         revision.IntentDigest,
		CompilationReceiptID: revision.CompilationReceiptID,
		PublicOutputContract: publicContract,
		Recipe:               revision.Recipe,
		RecipeDigest:         revision.RecipeDigest,
		ResolvedSchemaDigest: revision.ResolvedSchemaDigest,
		SourceGeneration:     revision.SourceGeneration,
		Materializations:     append([]explorer.Materialization(nil), revision.Materializations...),
		EmittedColumns:       append([]explorer.EmittedColumn(nil), revision.EmittedColumns...),
		Dataset:              revision.Dataset,
		QualityReports:       publication.CloneQualityReports(revision.QualityReports),
	}, nil
}

func revisionContentDigest(revision explorer.Revision) (string, error) {
	frozen, err := frozenRevisionContent(revision)
	if err != nil {
		return "", fmt.Errorf("encode frozen revision content: %w", err)
	}
	raw, err := json.Marshal(frozen)
	if err != nil {
		return "", fmt.Errorf("encode frozen revision content: %w", err)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func marshalOrNil(v any) ([]byte, error) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Ptr, reflect.Map:
		if rv.IsNil() {
			return nil, nil
		}
	}
	return json.Marshal(v)
}

type storedRevisionRow struct {
	ID                   string
	Project              string
	ExplorerID           string
	Config               json.RawMessage
	AuthoringBundle      json.RawMessage
	IntentDigest         string
	CompilationReceiptID string
	PublicOutputContract json.RawMessage
	Recipe               json.RawMessage
	RecipeDigest         string
	ResolvedSchemaDigest string
	SourceGeneration     string
	Materializations     json.RawMessage
	EmittedColumns       json.RawMessage
	Dataset              json.RawMessage
	QualityReports       json.RawMessage
	ContentDigest        string
	Status               string
	Diagnostics          json.RawMessage
	Publication          json.RawMessage
	CreatedBy            string
	CreatedAt            time.Time
	ReadyAt              *time.Time
	ActivatedAt          *time.Time
	FailedAt             *time.Time
}

func scanRevisionRow(row rowScanner, out *storedRevisionRow) error {
	return row.Scan(
		&out.ID,
		&out.Project,
		&out.ExplorerID,
		&out.Config,
		&out.AuthoringBundle,
		&out.IntentDigest,
		&out.CompilationReceiptID,
		&out.PublicOutputContract,
		&out.Recipe,
		&out.RecipeDigest,
		&out.ResolvedSchemaDigest,
		&out.SourceGeneration,
		&out.Materializations,
		&out.EmittedColumns,
		&out.Dataset,
		&out.QualityReports,
		&out.ContentDigest,
		&out.Status,
		&out.Diagnostics,
		&out.Publication,
		&out.CreatedBy,
		&out.CreatedAt,
		&out.ReadyAt,
		&out.ActivatedAt,
		&out.FailedAt,
	)
}

func decodeRevision(row storedRevisionRow) (*explorer.Revision, error) {
	revision := &explorer.Revision{
		ID:                   row.ID,
		Project:              row.Project,
		ExplorerID:           row.ExplorerID,
		Config:               row.Config,
		AuthoringBundle:      row.AuthoringBundle,
		IntentDigest:         row.IntentDigest,
		CompilationReceiptID: row.CompilationReceiptID,
		PublicOutputContract: row.PublicOutputContract,
		RecipeDigest:         row.RecipeDigest,
		ResolvedSchemaDigest: row.ResolvedSchemaDigest,
		SourceGeneration:     row.SourceGeneration,
		Status:               explorer.RevisionStatus(row.Status),
		CreatedBy:            row.CreatedBy,
		CreatedAt:            row.CreatedAt,
		ReadyAt:              row.ReadyAt,
		ActivatedAt:          row.ActivatedAt,
		FailedAt:             row.FailedAt,
	}
	if err := json.Unmarshal(row.Recipe, &revision.Recipe); err != nil {
		return nil, fmt.Errorf("postgres explorer store: stored revision %q canonical recipe is undecodable: %w", row.ID, err)
	}
	if err := json.Unmarshal(row.Dataset, &revision.Dataset); err != nil {
		return nil, fmt.Errorf("postgres explorer store: stored revision %q dataset metadata is undecodable: %w", row.ID, err)
	}
	if err := json.Unmarshal(row.Publication, &revision.Publication); err != nil {
		return nil, fmt.Errorf("postgres explorer store: stored revision %q publication metadata is undecodable: %w", row.ID, err)
	}
	if err := unmarshalOrNil(row.Materializations, &revision.Materializations); err != nil {
		return nil, fmt.Errorf("postgres explorer store: stored revision %q materializations are undecodable: %w", row.ID, err)
	}
	if err := unmarshalOrNil(row.EmittedColumns, &revision.EmittedColumns); err != nil {
		return nil, fmt.Errorf("postgres explorer store: stored revision %q emitted columns are undecodable: %w", row.ID, err)
	}
	if err := unmarshalOrNil(row.QualityReports, &revision.QualityReports); err != nil {
		return nil, fmt.Errorf("postgres explorer store: stored revision %q quality reports are undecodable: %w", row.ID, err)
	}
	if err := unmarshalOrNil(row.Diagnostics, &revision.Diagnostics); err != nil {
		return nil, fmt.Errorf("postgres explorer store: stored revision %q diagnostics are undecodable: %w", row.ID, err)
	}
	return revision, nil
}

func unmarshalOrNil(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

type revisionPayloads struct {
	config               []byte
	authoringBundle      []byte
	publicOutputContract []byte
	recipe               []byte
	dataset              []byte
	qualityReports       []byte
	publication          []byte
	materializations     []byte
	emittedColumns       []byte
	diagnostics          []byte
}

func encodeRevisionPayloads(revision explorer.Revision) (revisionPayloads, error) {
	var payloads revisionPayloads
	payloads.config = jsonParam(revision.Config)
	payloads.authoringBundle = jsonParam(revision.AuthoringBundle)
	payloads.publicOutputContract = jsonParam(revision.PublicOutputContract)
	var err error
	if payloads.recipe, err = json.Marshal(revision.Recipe); err != nil {
		return payloads, fmt.Errorf("encode canonical recipe: %w", err)
	}
	if payloads.dataset, err = json.Marshal(revision.Dataset); err != nil {
		return payloads, fmt.Errorf("encode dataset metadata: %w", err)
	}
	if payloads.publication, err = json.Marshal(revision.Publication); err != nil {
		return payloads, fmt.Errorf("encode publication metadata: %w", err)
	}
	if payloads.materializations, err = marshalOrNil(revision.Materializations); err != nil {
		return payloads, fmt.Errorf("encode materializations: %w", err)
	}
	if payloads.emittedColumns, err = marshalOrNil(revision.EmittedColumns); err != nil {
		return payloads, fmt.Errorf("encode emitted columns: %w", err)
	}
	if payloads.qualityReports, err = marshalOrNil(revision.QualityReports); err != nil {
		return payloads, fmt.Errorf("encode quality reports: %w", err)
	}
	return payloads, nil
}

func revisionArgs(revision explorer.Revision, payloads revisionPayloads, contentDigest string, diagnostics []byte) []any {
	return []any{
		revision.ID,
		revision.Project,
		revision.ExplorerID,
		payloads.config,
		payloads.authoringBundle,
		revision.IntentDigest,
		revision.CompilationReceiptID,
		payloads.publicOutputContract,
		payloads.recipe,
		revision.RecipeDigest,
		revision.ResolvedSchemaDigest,
		revision.SourceGeneration,
		payloads.materializations,
		payloads.emittedColumns,
		payloads.dataset,
		payloads.qualityReports,
		contentDigest,
		string(revision.Status),
		diagnostics,
		payloads.publication,
		revision.CreatedBy,
		revision.CreatedAt,
		revision.ReadyAt,
		revision.ActivatedAt,
		revision.FailedAt,
	}
}

func (s *Store) InsertRevision(ctx context.Context, revision explorer.Revision) (*explorer.Revision, error) {
	if strings.TrimSpace(revision.ID) == "" {
		return nil, fmt.Errorf("%w: revision ID is required", ErrInvalidRevision)
	}
	contentDigest, err := revisionContentDigest(revision)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: content identity of revision %q: %w", revision.ID, err)
	}
	payloads, err := encodeRevisionPayloads(revision)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: encode revision %q: %w", revision.ID, err)
	}
	diagnostics, err := marshalOrNil(revision.Diagnostics)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: encode revision %q diagnostics: %w", revision.ID, err)
	}
	args := revisionArgs(revision, payloads, contentDigest, diagnostics)

	var lastErr error
	for attempt := 1; attempt <= revisionInsertMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("postgres explorer store: insert revision %q: %w", revision.ID, err)
		}
		conn, err := s.client.Acquire(ctx)
		if err != nil {
			return nil, fmt.Errorf("postgres explorer store: acquire revision insert connection: %w", err)
		}
		stored, claimErr := s.claimedRevision(ctx, conn, revision.ID, args, contentDigest)
		conn.Release()
		if stored != nil {
			return stored, nil
		}
		lastErr = claimErr
		if attempt == revisionInsertMaxAttempts {
			break
		}
		if waitErr := waitForStoreRetry(ctx, time.Duration(attempt)*receiptConflictRetryDelay); waitErr != nil {
			return nil, fmt.Errorf("postgres explorer store: retry revision insert %q: %w",
				revision.ID, errors.Join(lastErr, waitErr))
		}
	}
	if lastErr == nil {
		lastErr = ErrRevisionClaimLost
	}
	return nil, fmt.Errorf("postgres explorer store: insert revision %q: %w", revision.ID, lastErr)
}

func (s *Store) claimedRevision(ctx context.Context, conn *pgxpool.Conn, id string, args []any, contentDigest string) (*explorer.Revision, error) {
	var row storedRevisionRow
	err := scanRevisionRow(conn.QueryRow(ctx, s.revision.insert, args...), &row)
	if err == nil {
		return decodeRevision(row)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("postgres explorer store: claim revision %q: %w", id, err)
	}

	existing, err := s.readRevisionRow(ctx, conn, s.revision.claim, id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, fmt.Errorf("%w: revision claim %q vanished before it became visible", explorer.ErrNotFound, id)
	}
	if existing.ContentDigest != contentDigest {
		return nil, fmt.Errorf("%w: revision id %q was reused with different content (stored %s, requested %s)",
			explorer.ErrImmutableRevision, id, existing.ContentDigest, contentDigest)
	}
	return decodeRevision(*existing)
}

func (s *Store) GetRevision(ctx context.Context, id string) (*explorer.Revision, error) {
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire revision read connection: %w", err)
	}
	defer conn.Release()

	row, err := s.readRevisionRow(ctx, conn, s.revision.get, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, explorer.ErrNotFound
	}
	return decodeRevision(*row)
}

func (s *Store) GetRevisionForExplorer(ctx context.Context, project, explorerID, id string) (*explorer.Revision, error) {
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire scoped revision read connection: %w", err)
	}
	defer conn.Release()

	var row storedRevisionRow
	err = scanRevisionRow(conn.QueryRow(ctx, s.revision.getScoped, project, explorerID, id), &row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, explorer.ErrNotFound
		}
		return nil, fmt.Errorf("postgres explorer store: read scoped revision %q/%q/%q: %w", project, explorerID, id, err)
	}
	return decodeRevision(row)
}

func (s *Store) readRevisionRow(ctx context.Context, conn *pgxpool.Conn, statement, id string) (*storedRevisionRow, error) {
	var row storedRevisionRow
	err := scanRevisionRow(conn.QueryRow(ctx, statement, id), &row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres explorer store: read revision %q: %w", id, err)
	}
	return &row, nil
}

func (s *Store) FailRevision(ctx context.Context, id string, diagnostics []explorer.Diagnostic) (*explorer.Revision, error) {
	return s.failRevision(ctx, id, diagnostics, false, "")
}

func (s *Store) FailRevisionFrom(ctx context.Context, id string, expected explorer.RevisionStatus, diagnostics []explorer.Diagnostic) (*explorer.Revision, error) {
	switch expected {
	case explorer.RevisionReady, explorer.RevisionActive, explorer.RevisionSuperseded, explorer.RevisionFailed:
	default:
		return nil, fmt.Errorf("%w: unknown expected status %q", ErrInvalidRevision, string(expected))
	}
	return s.failRevision(ctx, id, diagnostics, true, string(expected))
}

func (s *Store) failRevision(ctx context.Context, id string, diagnostics []explorer.Diagnostic, useToken bool, expected string) (*explorer.Revision, error) {
	diagBytes, err := marshalOrNil(diagnostics)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: encode failure diagnostics for revision %q: %w", id, err)
	}
	failedAt := time.Now().UTC()

	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire revision fail connection: %w", err)
	}
	defer conn.Release()

	statement := s.revision.failAny
	args := []any{diagBytes, failedAt, id}
	if useToken {
		statement = s.revision.failToken
		args = append(args, expected)
	}

	var row storedRevisionRow
	err = scanRevisionRow(conn.QueryRow(ctx, statement, args...), &row)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, readErr := s.readRevisionRow(ctx, conn, s.revision.get, id)
		if readErr != nil {
			return nil, errors.Join(err, readErr)
		}
		if existing == nil {
			return nil, explorer.ErrNotFound
		}
		return nil, fmt.Errorf("%w: revision %q is %s", ErrStaleRevisionTransition, id, existing.Status)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: fail revision %q: %w", id, err)
	}
	return decodeRevision(row)
}
