package postgres

// Immutable Explorer revisions on PostgreSQL 18.
//
// The frozen boundary is the exact field set the Arango adapter compares
// (internal/explorer/arango/revisions.go immutableRevision): definition,
// recipe, digests, source generation, frozen materialization mappings,
// emitted columns, dataset metadata, quality reports, and the authoring
// payloads, decided equal the same way the adapter decides it: quality
// reports are normalized through publication.CloneQualityReports (nil and
// empty are one content), and the omitempty-tagged raw authoring payloads
// collapse missing-vs-empty while an explicit JSON null stays distinct.
// InsertRevision is insert-once; same ID + same frozen content (same
// content digest) replays idempotently, and same ID + different frozen
// content is the typed explorer.ErrImmutableRevision refusal. The
// DB enforces the boundary independently of Go: migration
// 000006_explorer_revision.sql freezes every frozen column, refuses
// DELETE outright (insert-once against direct SQL, where a DELETE plus
// re-INSERT would fire no guard on a fresh row), and admits only the
// monotone lifecycle transitions READY->ACTIVE, READY->FAILED,
// ACTIVE->SUPERSEDED and ACTIVE->FAILED, each in one statement, so a double
// transition (FAILED->FAILED), a resurrection (FAILED->READY), or an
// un-activation (ACTIVE->READY) is refused by PostgreSQL with 23514.
//
// Lifecycle mutations are compare-and-swap operations fenced by an
// expected-status token: the UPDATE carries the expected status in its
// WHERE clause, so a stale token updates zero rows and is refused as
// ErrStaleRevisionTransition without writing anything -- including the
// second, losing side of a real two-connection race, where PostgreSQL
// re-evaluates the guard against the committed row.
//
// There is no fallback to any other backend: every failure is returned
// typed for the caller to surface.

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

// RevisionsTable is the Explorer revision table created by migration
// 000006_explorer_revision.sql inside the store's schema.
const RevisionsTable = "explorer_revisions"

const revisionInsertMaxAttempts = 4

var (
	// ErrStaleRevisionTransition reports that a lifecycle CAS carried an
	// expected-status token that no longer matches the stored row (or the
	// row is already terminal, so no further transition exists). Nothing
	// was written.
	ErrStaleRevisionTransition = errors.New("postgres explorer store: stale explorer revision lifecycle token")
	// ErrRevisionClaimLost reports that every insert attempt lost the claim
	// race to a winner whose transaction aborted.
	ErrRevisionClaimLost = errors.New("postgres explorer store: revision insert lost every claim race to an aborted winner")
	// ErrInvalidRevision reports a revision the store refuses to accept at
	// all (a missing identity, or a lifecycle CAS expected-status token
	// outside the status domain).
	ErrInvalidRevision = errors.New("postgres explorer store: invalid explorer revision")
)

// revisionColumns is the single source of truth for column order: the INSERT
// column list, the RETURNING/SELECT column list, the argument order, and the
// scan order all derive from it, so ordered SQL parameters ($1..$N) match
// the Go argument slice by construction.
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

// revisionStatements are the backend-tagged statements of the revision
// store. The two fail statements differ only in the lifecycle token: failAny
// admits the two failure-eligible source states; failToken demands one
// exact expected status.
type revisionStatements struct {
	insert    string // INSERT ... ON CONFLICT (id) DO NOTHING RETURNING
	claim     string // SELECT ... WHERE id = $1 FOR UPDATE
	get       string // SELECT ... WHERE id = $1
	getScoped string // SELECT ... WHERE project = $1 AND explorer_id = $2 AND id = $3
	failAny   string // CAS UPDATE ... WHERE id = $1 AND status IN ('READY','ACTIVE') RETURNING
	failToken string // CAS UPDATE ... WHERE id = $1 AND status = $2 RETURNING
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

// frozenRevision is the content identity of one revision. Its field set is
// the exact immutable boundary the Arango adapter's sameRevisionContent
// compares; lifecycle columns (status, diagnostics, publication,
// activation/failure timestamps) are excluded, so a replay that differs
// only in those still counts as the same content.
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

// digestableRaw maps one omitempty-tagged json.RawMessage payload
// (Config, AuthoringBundle, PublicOutputContract) onto the equivalence
// classes the Arango adapter's document round-trip produces: a nil and an
// empty RawMessage are both dropped by omitempty and decode back as
// missing, so they digest together; the four bytes null survive the
// round-trip and remain distinct from missing (reflect.DeepEqual on the
// adapter side); every other document keeps raw-byte identity (compacted,
// so semantically identical whitespace still digests together but key
// order, like the adapter's raw byte-for-byte comparison, is content).
// The class tag can never collide with a document because document bytes
// sit under the value key.
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
		// CloneQualityReports is the adapter's own normalization, applied
		// before it compares: it collapses a nil slice into an empty one,
		// so a nil-vs-empty replay is the same content here exactly as it
		// is in Arango (idempotent), while a populated report still moves
		// the digest.
		QualityReports: publication.CloneQualityReports(revision.QualityReports),
	}, nil
}

// revisionContentDigest is the sha256 content identity of the frozen column
// set. encoding/json emits struct fields in declaration order and sorts map
// keys, and the raw authoring payloads enter through digestableRaw, so two
// revisions digest identically exactly when the Arango adapter would
// consider their immutable content equal: documents compare byte-exactly
// (compact-normalized, key order significant), missing and empty collapse,
// an explicit JSON null is its own class, and quality reports pass through
// publication.CloneQualityReports. Canonical packets are the caller's
// contract, mirroring the Arango adapter, which DeepEquals the same
// normalized content.
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

// marshalOrNil encodes v for a jsonb parameter: a nil slice (or nil pointer)
// is "missing" (SQL NULL), an empty slice is "[]", and a populated value is
// its document. Together with jsonParam (for the json.RawMessage tri-state)
// this preserves missing versus empty versus JSON-null across a fresh
// connection.
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

// storedRevisionRow is the physically stored form of one revision row.
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

// scanRevisionRow decodes one row. Targets follow revisionColumns order.
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

// decodeRevision reconstructs the domain Revision from stored columns. The
// jsonb payload columns carry the missing/empty/JSON-null states losslessly:
// SQL NULL yields the Go zero value (a nil slice stays nil), "null" yields
// nil through json.Unmarshal, and "[]" yields an empty non-nil slice.
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

// unmarshalOrNil decodes a jsonb payload into out, treating SQL NULL
// (missing) as "leave the zero value alone".
func unmarshalOrNil(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// revisionPayloads holds every jsonb column value pre-encoded so argument
// assembly cannot fail midway through an attempt.
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

// revisionArgs maps a revision, its pre-encoded payloads, and its content
// digest to column values in revisionColumns order.
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

// InsertRevision stores an Explorer revision exactly once.
//
//   - A first insert stores the row and returns what PostgreSQL persisted.
//   - Re-inserting the same ID with the same frozen content (same content
//     digest; lifecycle differences never reach InsertRevision as an
//     overwrite -- an insert replay differs only in mutable fields) is
//     idempotent: nothing is written and the stored row is returned.
//   - Reusing the same ID with different frozen content is a typed
//     explorer.ErrImmutableRevision refusal; the stored row is never
//     overwritten. The digest is the canonical content check, never jsonb
//     object order.
//   - Concurrent identical writers all succeed via the claim reread, which
//     waits for an in-flight winner through FOR UPDATE and retries the
//     insert a bounded number of times when a winner aborts.
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

// claimedRevision performs one insert attempt on conn.
func (s *Store) claimedRevision(ctx context.Context, conn *pgxpool.Conn, id string, args []any, contentDigest string) (*explorer.Revision, error) {
	var row storedRevisionRow
	err := scanRevisionRow(conn.QueryRow(ctx, s.revision.insert, args...), &row)
	if err == nil {
		return decodeRevision(row)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("postgres explorer store: claim revision %q: %w", id, err)
	}

	// The identity already exists or is being claimed right now: reread
	// FOR UPDATE so the claim waits for an in-flight winner.
	existing, err := s.readRevisionRow(ctx, conn, s.revision.claim, id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, fmt.Errorf("%w: revision claim %q vanished before it became visible", explorer.ErrNotFound, id)
	}
	if existing.ContentDigest != contentDigest {
		// Same ID, different frozen content: an immutable revision was
		// reused, refused with the stored row left exactly as it is.
		return nil, fmt.Errorf("%w: revision id %q was reused with different content (stored %s, requested %s)",
			explorer.ErrImmutableRevision, id, existing.ContentDigest, contentDigest)
	}
	return decodeRevision(*existing)
}

// GetRevision returns a revision by identity through the Store contract.
// Callers that hold the tenant scope must prefer GetRevisionForExplorer,
// which is scoped by project AND Explorer.
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

// GetRevisionForExplorer returns the revision for one Explorer. The read is
// tenant-scoped: a revision that exists only under another project or
// another Explorer returns explorer.ErrNotFound with no information leak.
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

// readRevisionRow executes one id-addressed revision statement. A missing
// row yields (nil, nil).
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

// FailRevision records the failure transition for one revision: it is the
// compare-and-swap form of the Store contract, admitting exactly the two
// failure-eligible source states (READY, ACTIVE) in one statement. A
// revision that is already FAILED or SUPERSEDED is a stale token: the CAS
// updates zero rows, the call fails as ErrStaleRevisionTransition, and the
// stored row is not touched (the double-transition refusal). A missing
// revision is explorer.ErrNotFound.
func (s *Store) FailRevision(ctx context.Context, id string, diagnostics []explorer.Diagnostic) (*explorer.Revision, error) {
	return s.failRevision(ctx, id, diagnostics, false, "")
}

// FailRevisionFrom is the explicit-token CAS form of FailRevision: it
// transitions the revision to FAILED only while it carries the exact
// expected status, so a caller that observed the status earlier cannot
// clobber a transition committed in the meantime. Only the two
// failure-eligible states are ever CAS targets: a token that no longer
// matches the committed status, or names a terminal status (FAILED,
// SUPERSEDED -- there is no transition out of those), is refused as
// ErrStaleRevisionTransition without writing; a token outside the status
// domain is ErrInvalidRevision; a missing revision is explorer.ErrNotFound.
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
		// Either the revision does not exist, or its committed status no
		// longer satisfies the CAS guard -- including the losing side of a
		// concurrent race, where PostgreSQL re-evaluates the guard against
		// the row the winner just committed. Distinguish by reading the
		// row; neither branch writes anything.
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
