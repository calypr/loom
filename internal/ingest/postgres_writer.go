// PostgreSQL resource-row and edge-occurrence writes for the bounded
// generation ingest pipeline (pg-ingest-01 and pg-ingest-02, ingest
// surfaces).
//
// PostgresResourceWriter is the PostgreSQL sibling of the Arango document
// writer used by loadFile (generation_workers.go): it accepts a bounded
// slice of already-built, already-validated rowBuildResult values — the
// rowBuildResult form RowBuilder produces (generated and generic) — and
// commits them as FHIR resource rows through the PostgreSQL source store's
// idempotent, ledgered batch writer. This file owns that conversion and
// commit only. It does not open files, scan lines, build rows, or run
// worker pools: the execution owner streams NDJSON and hands built batches
// here, exactly as the Arango pipeline hands built documents to the
// document inserter. Built edges are not written by the resource surface;
// they belong to PostgresEdgeWriter below, whose contract is the built
// edge occurrence.
//
// Guarantees (proven in postgres_writer_test.go against real PostgreSQL 18):
//   - Exact writes: each built vertex's payload sub-object is committed as
//     its content-preserving canonical JSON encoding (sorted keys, no
//     insignificant whitespace, numeric literals verbatim — required for
//     stable ledger identity because the digest hashes payload bytes and
//     builder map serialization varies per process; JSONB canonicalizes on
//     input per migration 000002) with typed project, dataset_generation,
//     resource_type, logical_id, and auth_resource_path columns taken from
//     the vertex document itself. The
//     logical FHIR id is the raw vertex "id", never the sanitized physical
//     _key, and the storage identity (resource_seq) is always
//     server-assigned — no vertex field can supply it. A vertex payload key
//     that is absent stores SQL NULL; an explicit JSON null payload stores
//     the JSON null document; the two stay distinct.
//   - Scope is verified, not trusted, before any row is written: the writer
//     refuses (with a named error and zero database round trips) an empty,
//     over-capacity, or unscoped batch, and any row whose vertex project,
//     project_id, dataset_generation stamp, resourceType, id, or
//     auth_resource_path disagrees with the batch scope. The batch either
//     offers what it claims or nothing happens.
//   - Duplicate natural identity with different content returns the store's
//     typed *postgres.BatchResourceConflictError and the whole batch rolls
//     back; an identical batch replay under the same deterministic batch
//     identity is a ledger no-op returning the original accounting.
//   - Reported persisted counts change only after database commit: the
//     result comes from the committing transaction, and a witness on a
//     second real connection sees the scope's row count move from the old
//     value to exactly the reported value only when the commit lands.
//
// PostgresEdgeWriter is the edge-occurrence sibling: it accepts built
// rowBuildResult values and converts each built edge document
// (EdgeDocument form, generation-qualified by namespaceEdgeDocument) into
// an EdgeOccurrenceRow committed through the store's edge-only ledgered
// batch surface. Because built edge documents carry no source path or
// repeated-item ordinal (they are endpoint-keyed, like the Arango model),
// the conversion recovers the repeated-owner correlation from the owning
// row's built vertex payload: reference positions are scanned in canonical
// order (object keys sorted by byte order, array order kept), each offered
// edge document is matched to the reference position it was built from by
// endpoint identity with a label-token correlation preference, and the
// k-th identical edge document of one identity group takes the k-th
// matching position's path and its per-path ordinal. Two identical
// references at distinct repeated-array positions therefore persist as two
// independently addressable occurrences with exact path/ordinal; the
// generated loader's dedup keeps its collapsed multiplicity (one
// occurrence, ordinal 0), matching what the Arango store holds.
//
// Guarantees for the edge surface (proven in postgres_writer_test.go
// against real PostgreSQL 18):
//   - Endpoint identity is taken verbatim from the edge document's _from,
//     _to, label, from_type, and to_type columns (parsed with
//     SplitCollectionID); the document must be internally consistent and
//     one endpoint must be the owning row's own resource collection — an
//     edge whose source endpoint resolves to another resource is persisted
//     with that resource as source (back-reference direction included).
//     The source precondition (the source must exist in the same
//     project/dataset_generation) and target resolution stay the store's
//     typed facts; a missing target persists as an unresolved typed
//     endpoint, never an error.
//   - Invalid source/type/reference rows are refused with the existing
//     typed failure categories before any round trip: every edge-surface
//     refusal is *PGEdgeRowIdentityError or *PGEdgeWriteScopeError
//     (errors.Is ErrInvalidPGEdgeWrite) carrying the builder's own
//     rowErrorEdge or rowErrorGeneration category, and store-side
//     refusals are the store's typed edge/batch errors; a refused batch
//     never increments persisted edge counts (witnessed on the live
//     stack).
//   - Edge scope is verified like resource scope: project, project_id,
//     dataset_generation stamp, resourceType, id, and auth path must agree
//     with the batch for every vertex, and every edge document's project,
//     auth path, and generation stamp must agree as well.
//
// There is no cross-database atomicity and no backend fallback: every
// failure is a named error the caller must surface, never a cue to write
// the same rows somewhere else.

package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/catalog"
	postgresstore "github.com/calypr/loom/internal/store/postgres"
)

// ErrInvalidPGResourceWrite reports a resource write refused before any
// round trip: a malformed batch scope or a built row whose vertex identity
// disagrees with the scope. Classify with errors.Is and inspect the typed
// error (*PGResourceWriteScopeError or *PGResourceRowIdentityError) for the
// exact field.
var ErrInvalidPGResourceWrite = errors.New("ingest: invalid postgres resource write")

// PGResourceWriteScopeError reports a malformed batch scope: a blank or
// over-long batch identity, a blank project, or an empty or over-capacity
// row slice. Field names the offending scope element. It matches
// errors.Is(err, ErrInvalidPGResourceWrite).
type PGResourceWriteScopeError struct {
	BatchID string
	Field   string // "batch id", "project", or "rows"
	Reason  string
}

func (e *PGResourceWriteScopeError) Error() string {
	return fmt.Sprintf("postgres resource write refused: batch %q has an invalid %s: %s; "+
		"nothing was written", e.BatchID, e.Field, e.Reason)
}

func (e *PGResourceWriteScopeError) Unwrap() error { return ErrInvalidPGResourceWrite }

// PGResourceRowIdentityError reports the first offered row (0-based Row)
// refused because its built vertex disagrees with the batch scope or lacks
// a required identity column. Column names the exact vertex field at fault
// ("vertex" when the document itself is undecodable). It matches
// errors.Is(err, ErrInvalidPGResourceWrite).
type PGResourceRowIdentityError struct {
	BatchID string
	Row     int
	Column  string
	Reason  string
}

func (e *PGResourceRowIdentityError) Error() string {
	return fmt.Sprintf("postgres resource write refused: batch %q row %d has an invalid %s: %s; "+
		"nothing was written", e.BatchID, e.Row, e.Column, e.Reason)
}

func (e *PGResourceRowIdentityError) Unwrap() error { return ErrInvalidPGResourceWrite }

// PGResourceBatchScope is the explicit scope one bounded batch commits
// under. Every offered row must carry exactly this project, this
// dataset_generation stamp (the empty string is the legacy namespace and
// refuses stamped rows), and this auth path (VertexDocument omits the field
// for the legacy empty namespace, so an absent key is accepted only for the
// empty scope value). BatchID is the caller's deterministic batch identity,
// stable across retries of one write unit — derive it from stable
// provenance (project, generation, source, batch ordinal), never a
// per-attempt nonce, or replays lose their idempotency.
type PGResourceBatchScope struct {
	BatchID           string
	Project           string
	DatasetGeneration string
	AuthResourcePath  string
}

// PGResourceWriteResult is the committed outcome of one WriteResourceBatch
// call. The counts are exact-commit accounting from the committing
// transaction (on a ledger replay, the original commit's numbers); Offered
// is what the caller passed in, so Committed+SkippedIdentical accounts for
// every offered row.
type PGResourceWriteResult struct {
	BatchID          string
	Digest           string
	Offered          int
	Committed        int64
	SkippedIdentical int64
	Replay           bool
}

// resourceBatchCommitter is the store surface this writer commits through.
// Production always uses *postgresstore.ResourceWriter; the indirection
// exists so tests can prove refusals never reach the store at all (the
// "no round trip" half of the pre-write guarantee — live write claims still
// run against real PostgreSQL only).
type resourceBatchCommitter interface {
	WriteResourceBatch(ctx context.Context, batch postgresstore.ResourceBatch) (postgresstore.ResourceBatchResult, error)
}

// PostgresResourceWriter converts built, validated row batches into FHIR
// resource rows and commits them through the PostgreSQL source store's
// idempotent batch writer. Create one with NewPostgresResourceWriter. It
// holds no state beyond the store surface: independent writers share no
// state, and one batch is one store transaction. The caller owns the
// connection source and the schema lifecycle (MigrateSchema must have
// applied migrations 000002 and 000008 before use).
type PostgresResourceWriter struct {
	committer resourceBatchCommitter
}

// NewPostgresResourceWriter validates the connection source and schema name
// and binds the writer to the resource batch surface of <schema>. The
// connection source must be live only when WriteResourceBatch runs, not
// here.
func NewPostgresResourceWriter(src postgresstore.ConnSource, schema string) (*PostgresResourceWriter, error) {
	writer, err := postgresstore.NewResourceWriter(src, schema)
	if err != nil {
		return nil, err
	}
	return &PostgresResourceWriter{committer: writer}, nil
}

// WriteResourceBatch decodes every built row under the explicit scope,
// refuses the whole batch with a named error before any round trip if the
// scope or any row identity is invalid, and otherwise commits the whole
// batch atomically through the idempotent PG batch writer. The returned
// persisted counts describe committed rows and are produced only after a
// successful commit. Duplicate natural identities with different content
// are the store's typed conflicts and roll the whole batch back; an
// identical batch replay is a ledger no-op.
func (w *PostgresResourceWriter) WriteResourceBatch(
	ctx context.Context,
	scope PGResourceBatchScope,
	rows []rowBuildResult,
) (PGResourceWriteResult, error) {
	if strings.TrimSpace(scope.BatchID) == "" {
		return PGResourceWriteResult{}, &PGResourceWriteScopeError{
			BatchID: scope.BatchID, Field: "batch id", Reason: "it is blank",
		}
	}
	if len(scope.BatchID) > postgresstore.MaxBatchIDLength {
		return PGResourceWriteResult{}, &PGResourceWriteScopeError{
			BatchID: scope.BatchID, Field: "batch id",
			Reason: fmt.Sprintf("it is %d bytes, want at most %d", len(scope.BatchID), postgresstore.MaxBatchIDLength),
		}
	}
	if strings.TrimSpace(scope.Project) == "" {
		return PGResourceWriteResult{}, &PGResourceWriteScopeError{
			BatchID: scope.BatchID, Field: "project", Reason: "it is blank",
		}
	}
	if len(rows) == 0 {
		return PGResourceWriteResult{}, &PGResourceWriteScopeError{
			BatchID: scope.BatchID, Field: "rows", Reason: "the batch carries no rows",
		}
	}
	if len(rows) > postgresstore.MaxBatchResources {
		return PGResourceWriteResult{}, &PGResourceWriteScopeError{
			BatchID: scope.BatchID, Field: "rows",
			Reason: fmt.Sprintf("the batch offers %d rows, want at most %d",
				len(rows), postgresstore.MaxBatchResources),
		}
	}

	decoded := make([]catalog.PGResourceRow, 0, len(rows))
	for index, built := range rows {
		record, err := decodeBuiltResourceVertex(built.vertex, scope, index)
		if err != nil {
			return PGResourceWriteResult{}, err
		}
		decoded = append(decoded, record)
	}

	result, err := w.committer.WriteResourceBatch(ctx, postgresstore.ResourceBatch{
		ID:                scope.BatchID,
		Project:           scope.Project,
		DatasetGeneration: scope.DatasetGeneration,
		Rows:              decoded,
	})
	if err != nil {
		return PGResourceWriteResult{}, err
	}
	return PGResourceWriteResult{
		BatchID:          result.BatchID,
		Digest:           result.Digest,
		Offered:          len(rows),
		Committed:        result.Committed,
		SkippedIdentical: result.SkippedIdentical,
		Replay:           result.Replay,
	}, nil
}

// decodeBuiltResourceVertex converts one built vertex document into the
// resource row it commits under, refusing any identity that disagrees with
// the batch scope. Values are taken verbatim (like requireProject in
// generation_identity.go): the logical id is the raw vertex "id", never the
// sanitized _key, and the payload is the vertex payload sub-object itself,
// canonicalized for stable digest identity by canonicalPayload — absent
// stores SQL NULL, the bytes "null" store the JSON null document. The
// storage identity is never read from the vertex; the store assigns it.
func decodeBuiltResourceVertex(
	vertex json.RawMessage,
	scope PGResourceBatchScope,
	index int,
) (catalog.PGResourceRow, error) {
	refuse := func(column string, format string, args ...any) (catalog.PGResourceRow, error) {
		return catalog.PGResourceRow{}, &PGResourceRowIdentityError{
			BatchID: scope.BatchID, Row: index, Column: column,
			Reason: fmt.Sprintf(format, args...),
		}
	}
	invalid := func(column string, err error) (catalog.PGResourceRow, error) {
		return refuse(column, "%v", err)
	}

	document, err := decodeTopLevelDocument(vertex)
	if err != nil {
		return refuse("vertex", "the built document is undecodable: %v", err)
	}

	record := catalog.PGResourceRow{
		Project:           scope.Project,
		DatasetGeneration: scope.DatasetGeneration,
		AuthResourcePath:  scope.AuthResourcePath,
	}

	project, err := requiredDocumentString(document, "project")
	if err != nil {
		return invalid("project", err)
	}
	if project != scope.Project {
		return refuse("project", "vertex project %q does not match the batch project %q", project, scope.Project)
	}
	projectID, err := requiredDocumentString(document, "project_id")
	if err != nil {
		return invalid("project_id", err)
	}
	if projectID != scope.Project {
		return refuse("project_id", "vertex project_id %q does not match the batch project %q", projectID, scope.Project)
	}

	generation, stamped, err := optionalDocumentString(document, generationIdentityField)
	if err != nil {
		return invalid(generationIdentityField, err)
	}
	if scope.DatasetGeneration != "" {
		if !stamped {
			return refuse(generationIdentityField,
				"the row carries no dataset_generation stamp but the batch commits into generation %q",
				scope.DatasetGeneration)
		}
		if generation != scope.DatasetGeneration {
			return refuse(generationIdentityField, "vertex generation %q does not match the batch generation %q",
				generation, scope.DatasetGeneration)
		}
	} else if stamped {
		return refuse(generationIdentityField,
			"the row is stamped for generation %q but the batch is the legacy (unversioned) namespace", generation)
	}

	resourceType, err := requiredDocumentString(document, "resourceType")
	if err != nil {
		return invalid("resourceType", err)
	}
	record.ResourceType = resourceType

	logicalID, err := requiredDocumentString(document, "id")
	if err != nil {
		return invalid("id", err)
	}
	record.LogicalID = logicalID

	authPath, authPresent, err := optionalDocumentString(document, "auth_resource_path")
	if err != nil {
		return invalid("auth_resource_path", err)
	}
	if !authPresent {
		if scope.AuthResourcePath != "" {
			return refuse("auth_resource_path",
				"the row carries no auth_resource_path but the batch is scoped to %q", scope.AuthResourcePath)
		}
	} else if authPath != scope.AuthResourcePath {
		return refuse("auth_resource_path", "vertex auth_resource_path %q does not match the batch auth path %q",
			authPath, scope.AuthResourcePath)
	}

	// The payload keeps the vertex's own document: absent stays SQL NULL and
	// the JSON null document stays a present null document. The bytes are
	// canonicalized (sorted keys, no insignificant whitespace, numeric
	// literals verbatim) because the ledger digest hashes the payload bytes
	// exactly and the row builders serialize payloads out of Go maps, whose
	// key order varies per process — verbatim bytes would make a retry of
	// identical content reuse the batch identity under a different digest
	// and be refused as a conflict. Canonicalization is content-preserving:
	// the same JSON document in, the same bytes out, and PostgreSQL JSONB
	// normalizes storage the same way on input regardless.
	if rawPayload, present := document["payload"]; present {
		canonical, err := canonicalPayload(rawPayload)
		if err != nil {
			return invalid("payload", fmt.Errorf("the built payload document is undecodable: %w", err))
		}
		record.Payload = canonical
	}
	return record, nil
}

// canonicalPayload returns the deterministic JSON encoding of a payload
// document: object keys sorted by byte order, insignificant whitespace
// removed, numeric literals kept exactly as written (no float round trip),
// and duplicate object keys resolved last-wins. It refuses malformed JSON,
// including a valid document followed by trailing bytes.
func canonicalPayload(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected content after the JSON document: %v", err)
	}
	// encoding/json encodes map keys in sorted byte order and writes a
	// json.Number verbatim, so this is stable across processes and runs.
	return json.Marshal(document)
}

// optionalDocumentString reads a top-level string field that a built
// document may omit (VertexDocument emits auth_resource_path only when
// non-empty; the dataset_generation stamp only exists on generation rows).
// It reports whether the key was present and refuses a present non-string.
func optionalDocumentString(document map[string]json.RawMessage, field string) (string, bool, error) {
	raw, present := document[field]
	if !present {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false, fmt.Errorf("%s must be a string", field)
	}
	return value, true, nil
}

// ---------------------------------------------------------------------------
// Edge-occurrence writes (pg-ingest-02, ingest surface).
// ---------------------------------------------------------------------------

// ErrInvalidPGEdgeWrite reports an edge write refused before any round
// trip: a malformed batch scope or a built row whose vertex or edge
// document disagrees with the scope or carries an unsupportable endpoint.
// Classify with errors.Is and inspect the typed error
// (*PGEdgeWriteScopeError or *PGEdgeRowIdentityError) for the exact field.
var ErrInvalidPGEdgeWrite = errors.New("ingest: invalid postgres edge write")

// PGEdgeWriteScopeError reports a malformed batch scope: a blank or
// over-long batch identity, a blank project, or an empty or over-capacity
// batch. Field names the offending scope element. It matches
// errors.Is(err, ErrInvalidPGEdgeWrite).
type PGEdgeWriteScopeError struct {
	BatchID string
	Field   string // "batch id", "project", or "rows"
	Reason  string
}

func (e *PGEdgeWriteScopeError) Error() string {
	return fmt.Sprintf("postgres edge write refused: batch %q has an invalid %s: %s; "+
		"nothing was written", e.BatchID, e.Field, e.Reason)
}

func (e *PGEdgeWriteScopeError) Unwrap() error { return ErrInvalidPGEdgeWrite }

// PGEdgeRowIdentityError reports the first offered row (0-based Row)
// refused because its built vertex or one of its built edge documents is
// invalid for occurrence persistence. Column names the exact document
// field at fault ("vertex" when the vertex itself is undecodable,
// "edge <index>" when an edge document is). Category carries the builder's
// own failure classification — rowErrorEdge for edge-document defects and
// rowErrorGeneration for vertex identity/generation defects — so edge
// refusals land in the existing typed edge/generation failure categories
// the Arango loader uses. It matches errors.Is(err, ErrInvalidPGEdgeWrite).
type PGEdgeRowIdentityError struct {
	BatchID  string
	Row      int
	Category rowErrorType
	Column   string
	Reason   string
}

func (e *PGEdgeRowIdentityError) Error() string {
	return fmt.Sprintf("postgres edge write refused: batch %q row %d (%s failure) has an invalid %s: %s; "+
		"nothing was written", e.BatchID, e.Row, e.Category, e.Column, e.Reason)
}

func (e *PGEdgeRowIdentityError) Unwrap() error { return ErrInvalidPGEdgeWrite }

// PGEdgeBatchScope is the explicit scope one bounded edge batch commits
// under. It is PGResourceBatchScope's contract for the edge side: every
// built row must carry exactly this project, this dataset_generation stamp,
// and this auth path, and every built edge document must carry matching
// project, dataset_generation stamp, and auth_resource_path values. BatchID
// is the caller's deterministic batch identity, stable across retries of
// one write unit — derive it from stable provenance (project, generation,
// source, batch ordinal), never a per-attempt nonce, or replays lose their
// idempotency. Edge sources are not written by this surface: each
// occurrence's source resource must already exist in fhir_resource in this
// exact project/dataset_generation (the store enforces it as a typed
// precondition and rolls the whole batch back when it fails).
type PGEdgeBatchScope struct {
	BatchID           string
	Project           string
	DatasetGeneration string
	AuthResourcePath  string
}

// PGEdgeWriteResult is the committed outcome of one WriteEdgeBatch call.
// Rows is the number of built rows offered and Offered the number of edge
// occurrences converted from them; Committed+SkippedIdentical accounts for
// every offered occurrence. The counts are exact-commit accounting from
// the committing transaction (on a ledger replay, the original commit's
// numbers).
type PGEdgeWriteResult struct {
	BatchID          string
	Digest           string
	Rows             int
	Offered          int
	Committed        int64
	SkippedIdentical int64
	Replay           bool
}

// edgeBatchCommitter is the store surface this writer commits through.
// Production always uses *postgresstore.EdgeWriter; the indirection exists
// so tests can prove refusals never reach the store at all (the "no round
// trip" half of the pre-write guarantee — live write claims still run
// against real PostgreSQL only).
type edgeBatchCommitter interface {
	WriteEdgeBatch(ctx context.Context, batch postgresstore.EdgeBatch) (postgresstore.EdgeBatchResult, error)
}

// PostgresEdgeWriter converts built, validated row batches into FHIR edge
// occurrence rows and commits them through the PostgreSQL source store's
// idempotent edge-only batch writer. Create one with NewPostgresEdgeWriter.
// It holds no state beyond the store surface: independent writers share no
// state, and one batch is one store transaction. The caller owns the
// connection source and the schema lifecycle (MigrateSchema must have
// applied migrations 000002, 000004, and 000008 before use).
type PostgresEdgeWriter struct {
	committer edgeBatchCommitter
}

// NewPostgresEdgeWriter validates the connection source and schema name and
// binds the writer to the edge batch surface of <schema>. The connection
// source must be live only when WriteEdgeBatch runs, not here.
func NewPostgresEdgeWriter(src postgresstore.ConnSource, schema string) (*PostgresEdgeWriter, error) {
	writer, err := postgresstore.NewEdgeWriter(src, schema)
	if err != nil {
		return nil, err
	}
	return &PostgresEdgeWriter{committer: writer}, nil
}

// WriteEdgeBatch decodes every built row under the explicit scope, converts
// each built edge document into an occurrence row (recovering the
// repeated-owner path/ordinal from the owning vertex payload), refuses the
// whole batch with a named typed error before any round trip if the scope,
// any vertex identity, or any edge document is invalid, and otherwise
// commits the whole batch atomically through the idempotent PG edge batch
// writer. The returned persisted counts describe committed occurrences and
// are produced only after a successful commit. A missing edge source is the
// store's typed EdgeSourceMissingError and rolls the whole batch back,
// never incrementing persisted edge counts; a missing target is stored as
// an unresolved typed endpoint.
func (w *PostgresEdgeWriter) WriteEdgeBatch(
	ctx context.Context,
	scope PGEdgeBatchScope,
	rows []rowBuildResult,
) (PGEdgeWriteResult, error) {
	if strings.TrimSpace(scope.BatchID) == "" {
		return PGEdgeWriteResult{}, &PGEdgeWriteScopeError{
			BatchID: scope.BatchID, Field: "batch id", Reason: "it is blank",
		}
	}
	if len(scope.BatchID) > postgresstore.MaxBatchIDLength {
		return PGEdgeWriteResult{}, &PGEdgeWriteScopeError{
			BatchID: scope.BatchID, Field: "batch id",
			Reason: fmt.Sprintf("it is %d bytes, want at most %d", len(scope.BatchID), postgresstore.MaxBatchIDLength),
		}
	}
	if strings.TrimSpace(scope.Project) == "" {
		return PGEdgeWriteResult{}, &PGEdgeWriteScopeError{
			BatchID: scope.BatchID, Field: "project", Reason: "it is blank",
		}
	}
	if len(rows) == 0 {
		return PGEdgeWriteResult{}, &PGEdgeWriteScopeError{
			BatchID: scope.BatchID, Field: "rows", Reason: "the batch carries no rows",
		}
	}

	offered := make([]postgresstore.EdgeOccurrenceRow, 0, len(rows))
	for index, built := range rows {
		converted, err := convertBuiltEdgeOccurrences(built, scope, index)
		if err != nil {
			return PGEdgeWriteResult{}, err
		}
		offered = append(offered, converted...)
	}
	if len(offered) == 0 {
		return PGEdgeWriteResult{}, &PGEdgeWriteScopeError{
			BatchID: scope.BatchID, Field: "rows",
			Reason: "the batch's rows carry no built edge occurrences",
		}
	}
	if len(offered) > postgresstore.MaxBatchEdges {
		return PGEdgeWriteResult{}, &PGEdgeWriteScopeError{
			BatchID: scope.BatchID, Field: "rows",
			Reason: fmt.Sprintf("the batch offers %d edge occurrences, want at most %d",
				len(offered), postgresstore.MaxBatchEdges),
		}
	}

	result, err := w.committer.WriteEdgeBatch(ctx, postgresstore.EdgeBatch{
		ID:                scope.BatchID,
		Project:           scope.Project,
		DatasetGeneration: scope.DatasetGeneration,
		Rows:              offered,
	})
	if err != nil {
		return PGEdgeWriteResult{}, err
	}
	return PGEdgeWriteResult{
		BatchID:          result.BatchID,
		Digest:           result.Digest,
		Rows:             len(rows),
		Offered:          len(offered),
		Committed:        result.Committed,
		SkippedIdentical: result.SkippedIdentical,
		Replay:           result.Replay,
	}, nil
}

// pgEdgeDocument is one decoded built edge document, fields verified and
// endpoints parsed.
type pgEdgeDocument struct {
	fromType, fromID  string
	toType, toID      string
	label             string
	project           string
	authResourcePath  string
	authPresent       bool
	datasetGeneration string
	stamped           bool
	key               string
	logicalEdgeKey    *string
}

// convertBuiltEdgeOccurrences converts one built row's edge documents into
// occurrence rows under the batch scope. The vertex identity is verified
// exactly as the resource surface verifies it (same field rules, same
// reasons), then each edge document is validated and matched to the
// reference position in the vertex payload that produced it. Edge-free
// built rows convert to no occurrences (their vertex still must be
// in-scope).
func convertBuiltEdgeOccurrences(
	built rowBuildResult,
	scope PGEdgeBatchScope,
	index int,
) ([]postgresstore.EdgeOccurrenceRow, error) {
	refuse := func(category rowErrorType, column string, format string, args ...any) ([]postgresstore.EdgeOccurrenceRow, error) {
		return nil, &PGEdgeRowIdentityError{
			BatchID: scope.BatchID, Row: index, Category: category, Column: column,
			Reason: fmt.Sprintf(format, args...),
		}
	}

	// Vertex identity: the same verification the resource surface applies,
	// reused wholesale so both surfaces refuse the same defective vertices
	// with the same field reasons.
	if _, err := decodeBuiltResourceVertex(built.vertex, PGResourceBatchScope{
		BatchID:           scope.BatchID,
		Project:           scope.Project,
		DatasetGeneration: scope.DatasetGeneration,
		AuthResourcePath:  scope.AuthResourcePath,
	}, index); err != nil {
		var rowErr *PGResourceRowIdentityError
		if errors.As(err, &rowErr) {
			category := rowErrorGeneration
			return refuse(category, rowErr.Column, "%s", rowErr.Reason)
		}
		return refuse(rowErrorGeneration, "vertex", "%v", err)
	}

	document, err := decodeTopLevelDocument(built.vertex)
	if err != nil {
		return refuse(rowErrorGeneration, "vertex", "the built document is undecodable: %v", err)
	}
	resourceType, err := requiredDocumentString(document, "resourceType")
	if err != nil {
		return refuse(rowErrorGeneration, "resourceType", "%v", err)
	}
	logicalID, err := requiredDocumentString(document, "id")
	if err != nil {
		return refuse(rowErrorGeneration, "id", "%v", err)
	}
	// The vertex's own physical key is the authority for "the owning row's
	// endpoint": the builders put SanitizeKey(id) there, and
	// namespaceVertexDocument rewrites it to the generation-qualified
	// g_<hash> — exactly what the edge documents' _from/_to carry for the
	// owning side. Deriving ownership from the vertex itself makes the
	// forward/back-reference classification work under both the raw and
	// the generation-qualified identity, and refuses (rather than
	// silently mis-owns) any row whose builder wrote edge endpoints under
	// a different object id than the vertex's own id.
	vertexKey, err := requiredDocumentString(document, "_key")
	if err != nil {
		return refuse(rowErrorGeneration, "_key", "%v", err)
	}
	if len(built.edges) == 0 {
		return nil, nil
	}

	// Canonical payload order: object keys sorted by byte order, array
	// order kept. Scanning the canonical encoding (not the builder's bytes)
	// is what makes path/ordinal assignment reproducible across processes:
	// builder map serialization order varies per run, and a retry must
	// assign the same ordinal to the same occurrence or the ledger digest
	// of identical content would drift.
	var positions []pgReferencePosition
	if rawPayload, present := document["payload"]; present {
		canonical, err := canonicalPayload(rawPayload)
		if err != nil {
			return refuse(rowErrorGeneration, "payload", "the built payload document is undecodable: %v", err)
		}
		positions, err = scanPayloadReferencePositions(canonical)
		if err != nil {
			return refuse(rowErrorGeneration, "payload", "%v", err)
		}
	}

	// Own endpoint: the physical collection id the builders give this row's
	// own side of every incident edge — SanitizeKey(type)/vertex _key, raw
	// or generation-qualified exactly as namespaceVertexDocument left it.
	ownEndpoint := SanitizeKey(resourceType) + "/" + vertexKey

	// Group edge documents by full identity in offer order. Identical edge
	// documents from distinct repeated-array positions are one group; the
	// k-th member of a group pairs with the k-th matching payload reference
	// position, which carries the exact path and per-path ordinal.
	groupOrder := make([]pgEdgeIdentity, 0, len(built.edges))
	groups := make(map[pgEdgeIdentity][]*pgEdgeDocument, len(built.edges))
	for edgeIndex, rawEdge := range built.edges {
		doc, err := decodeBuiltEdgeDocument(rawEdge, scope, index, edgeIndex)
		if err != nil {
			var rowErr *PGEdgeRowIdentityError
			if errors.As(err, &rowErr) {
				return nil, rowErr
			}
			return refuse(rowErrorEdge, fmt.Sprintf("edge %d", edgeIndex), "%v", err)
		}
		identity, err := classifyEdgeOwnership(doc, ownEndpoint, index, edgeIndex, scope)
		if err != nil {
			return nil, err
		}
		if _, seen := groups[identity]; !seen {
			groupOrder = append(groupOrder, identity)
		}
		groups[identity] = append(groups[identity], doc)
	}

	// Pair each group's offered documents with payload positions. The peer
	// endpoint (the end that is not the owning row) names the reference
	// target the position must hold — by physical key, so the same check
	// works raw and generation-qualified — and the matched position is
	// what supplies the stored logical id, the exact path, and the
	// per-path ordinal. The typed-identity storage contract (migration
	// 000004: endpoints are resource type + logical FHIR id, never
	// physical document ids) is why the g_<hash> key a qualified document
	// carries can only be understood through the raw reference the payload
	// still holds.
	out := make([]postgresstore.EdgeOccurrenceRow, 0, len(built.edges))
	for _, identity := range groupOrder {
		members := groups[identity]
		peerType, peerKey := identity.toType, identity.toID
		ownIsSource := !identity.directionBackref
		direction := "forward"
		if !ownIsSource {
			peerType, peerKey = identity.fromType, identity.fromID
			direction = "back-reference"
		}
		qualify := sanitizeKeyPart
		if scope.DatasetGeneration != "" {
			project, generation := scope.Project, scope.DatasetGeneration
			qualify = func(logicalID string) string {
				return generationDocumentKey(project, generation, peerType, SanitizeKey(logicalID))
			}
		}
		candidates := selectReferencePositions(positions, peerType, peerKey, identity.label, qualify)
		if len(candidates) == 0 {
			column := fmt.Sprintf("edge _to (first of group %s)", identity.key)
			if !ownIsSource {
				column = fmt.Sprintf("edge _from (first of group %s)", identity.key)
			}
			return refuse(rowErrorEdge, column,
				"the %s edge document %s/%s -%s-> %s/%s matches no reference position in the vertex payload",
				direction, identity.fromType, identity.fromID, identity.label, identity.toType, identity.toID)
		}
		if len(members) > len(candidates) {
			return refuse(rowErrorEdge, fmt.Sprintf("edge _key (group %s)", identity.key),
				"the row offers %d identical %s edge documents for %s/%s -%s-> %s/%s but the payload holds only %d matching reference positions",
				len(members), direction, identity.fromType, identity.fromID, identity.label,
				identity.toType, identity.toID, len(candidates))
		}
		ordinals := make(map[string]int64, len(candidates))
		for i := range members {
			position := candidates[i]
			ordinal := ordinals[position.path]
			ordinals[position.path] = ordinal + 1
			sourceType, sourceID, targetType, targetID := identity.fromType, identity.fromID, identity.toType, identity.toID
			if ownIsSource {
				sourceID = logicalID
				targetID = position.logicalID
			} else {
				sourceID = position.logicalID
				targetID = logicalID
			}
			out = append(out, postgresstore.EdgeOccurrenceRow{
				Project:            scope.Project,
				DatasetGeneration:  scope.DatasetGeneration,
				AuthResourcePath:   scope.AuthResourcePath,
				Label:              identity.label,
				SourceResourceType: sourceType,
				SourceLogicalID:    sourceID,
				TargetResourceType: targetType,
				TargetLogicalID:    targetID,
				SourcePath:         position.path,
				SourceOrdinal:      ordinal,
				LogicalEdgeKey:     members[i].logicalEdgeKey,
			})
		}
	}
	return out, nil
}

// sanitizeKeyPart is the raw-batch key projection: the collection id a
// builder puts after the slash for one logical id.
func sanitizeKeyPart(logicalID string) string {
	return SanitizeKey(logicalID)
}

// decodeBuiltEdgeDocument decodes and validates one built edge document
// against the batch scope. Field presence and identity rules mirror the
// vertex rules: required strings are non-blank, project must match the
// batch project, the auth_resource_path key appears exactly when the
// builder injects it, and the dataset_generation stamp appears exactly
// when the row is generation-qualified. from_type/to_type must agree with
// the parsed _from/_to collections; the storage identity is never read —
// edge_seq is server-assigned, and an advertised "edge_seq" field is
// ignored by contract and refused as a defect so a caller cannot
// accidentally believe it was honored.
func decodeBuiltEdgeDocument(
	raw json.RawMessage,
	scope PGEdgeBatchScope,
	rowIndex int,
	edgeIndex int,
) (*pgEdgeDocument, error) {
	column := func(name string) string { return fmt.Sprintf("edge %d %s", edgeIndex, name) }
	refuse := func(column string, format string, args ...any) (*pgEdgeDocument, error) {
		return nil, &PGEdgeRowIdentityError{
			BatchID: scope.BatchID, Row: rowIndex, Category: rowErrorEdge, Column: column,
			Reason: fmt.Sprintf(format, args...),
		}
	}

	document, err := decodeTopLevelDocument(raw)
	if err != nil {
		return refuse(column("document"), "the built edge document is undecodable: %v", err)
	}
	if _, advertised := document["edge_seq"]; advertised {
		return refuse(column("edge_seq"),
			"the storage identity is server-assigned; an edge document must not advertise one")
	}

	doc := &pgEdgeDocument{}
	if doc.project, err = requiredDocumentString(document, "project"); err != nil {
		return refuse(column("project"), "%v", err)
	}
	if doc.project != scope.Project {
		return refuse(column("project"), "edge project %q does not match the batch project %q", doc.project, scope.Project)
	}
	if doc.label, err = requiredDocumentString(document, "label"); err != nil {
		return refuse(column("label"), "%v", err)
	}
	if doc.key, err = requiredDocumentString(document, "_key"); err != nil {
		return refuse(column("_key"), "%v", err)
	}
	if logicalKey, present := document[logicalKeyField]; present {
		var value string
		if err := json.Unmarshal(logicalKey, &value); err != nil {
			return refuse(column(logicalKeyField), "%s must be a string", logicalKeyField)
		}
		doc.logicalEdgeKey = &value
	} else {
		key := doc.key
		doc.logicalEdgeKey = &key
	}
	from, err := requiredDocumentString(document, "_from")
	if err != nil {
		return refuse(column("_from"), "%v", err)
	}
	to, err := requiredDocumentString(document, "_to")
	if err != nil {
		return refuse(column("_to"), "%v", err)
	}
	declaredFromType, err := requiredDocumentString(document, "from_type")
	if err != nil {
		return refuse(column("from_type"), "%v", err)
	}
	declaredToType, err := requiredDocumentString(document, "to_type")
	if err != nil {
		return refuse(column("to_type"), "%v", err)
	}
	fromType, fromID, parseErr := parseEdgeEndpoint(column("from_type"), declaredFromType, from)
	if parseErr != nil {
		parseErr.BatchID = scope.BatchID
		parseErr.Row = rowIndex
		return nil, parseErr
	}
	toType, toID, parseErr := parseEdgeEndpoint(column("to_type"), declaredToType, to)
	if parseErr != nil {
		parseErr.BatchID = scope.BatchID
		parseErr.Row = rowIndex
		return nil, parseErr
	}
	doc.fromType, doc.fromID = fromType, fromID
	doc.toType, doc.toID = toType, toID

	if doc.authResourcePath, doc.authPresent, err = optionalDocumentString(document, "auth_resource_path"); err != nil {
		return refuse(column("auth_resource_path"), "%v", err)
	}
	if !doc.authPresent {
		if scope.AuthResourcePath != "" {
			return refuse(column("auth_resource_path"),
				"the edge carries no auth_resource_path but the batch is scoped to %q", scope.AuthResourcePath)
		}
	} else if doc.authResourcePath != scope.AuthResourcePath {
		return refuse(column("auth_resource_path"), "edge auth_resource_path %q does not match the batch auth path %q",
			doc.authResourcePath, scope.AuthResourcePath)
	}

	if doc.datasetGeneration, doc.stamped, err = optionalDocumentString(document, generationIdentityField); err != nil {
		return refuse(column(generationIdentityField), "%v", err)
	}
	if scope.DatasetGeneration != "" {
		if !doc.stamped {
			return refuse(column(generationIdentityField),
				"the edge carries no dataset_generation stamp but the batch commits into generation %q",
				scope.DatasetGeneration)
		}
		if doc.datasetGeneration != scope.DatasetGeneration {
			return refuse(column(generationIdentityField),
				"edge generation %q does not match the batch generation %q", doc.datasetGeneration, scope.DatasetGeneration)
		}
	} else if doc.stamped {
		return refuse(column(generationIdentityField),
			"the edge is stamped for generation %q but the batch is the legacy (unversioned) namespace", doc.datasetGeneration)
	}
	return doc, nil
}

// parseEdgeEndpoint parses one edge endpoint into its typed identity,
// requiring the explicit column type to agree with the endpoint collection
// (both are builder-sanitized canonical types, so agreement is exact).
// A disagreement is an invalid type row: the endpoint would persist under a
// different typed identity than the edge claims.
func parseEdgeEndpoint(column, declaredType, endpoint string) (string, string, *PGEdgeRowIdentityError) {
	resourceType, logicalID, ok := SplitCollectionID(endpoint)
	if !ok {
		return "", "", &PGEdgeRowIdentityError{
			Category: rowErrorEdge, Column: column,
			Reason: fmt.Sprintf("endpoint %q is not a well-formed Type/id collection identity", endpoint),
		}
	}
	if resourceType != declaredType {
		return "", "", &PGEdgeRowIdentityError{
			Category: rowErrorEdge, Column: column,
			Reason: fmt.Sprintf("endpoint %q has collection type %q but the document declares %q",
				endpoint, resourceType, declaredType),
		}
	}
	return resourceType, logicalID, nil
}

// pgEdgeIdentity is the full endpoint identity of one edge document group:
// the five stored endpoint columns plus ownership direction. Two documents
// share a group only when byte-identical on every endpoint field and label;
// they then differ, if at all, only in which repeated-array position built
// them.
type pgEdgeIdentity struct {
	fromType         string
	fromID           string
	label            string
	toType           string
	toID             string
	directionBackref bool
	key              string
}

// classifyEdgeOwnership requires one endpoint to be the owning row itself:
// _from equal to the row's own endpoint makes the row the source (forward
// direction), _to equal makes it the target (back-reference direction,
// whose stored source endpoint is the other resource, so that resource
// must exist in scope). A self-reference classifies forward, matching the
// store's insert contract where the row itself is the source. An edge with
// neither endpoint is an invalid source/reference row.
func classifyEdgeOwnership(
	doc *pgEdgeDocument,
	ownEndpoint string,
	rowIndex int,
	edgeIndex int,
	scope PGEdgeBatchScope,
) (pgEdgeIdentity, error) {
	identity := pgEdgeIdentity{
		fromType: doc.fromType,
		fromID:   doc.fromID,
		label:    doc.label,
		toType:   doc.toType,
		toID:     doc.toID,
		key:      doc.key,
	}
	fromEndpoint := SanitizeKey(doc.fromType) + "/" + doc.fromID
	toEndpoint := SanitizeKey(doc.toType) + "/" + doc.toID
	switch ownEndpoint {
	case fromEndpoint:
		return identity, nil
	case toEndpoint:
		identity.directionBackref = true
		return identity, nil
	default:
		return pgEdgeIdentity{}, &PGEdgeRowIdentityError{
			BatchID: scope.BatchID, Row: rowIndex, Category: rowErrorEdge,
			Column: fmt.Sprintf("edge %d _from", edgeIndex),
			Reason: fmt.Sprintf("neither _from %q nor _to %q is the owning row's endpoint %q; "+
				"an edge document must be owned by (or back-reference) its row",
				fromEndpoint, toEndpoint, ownEndpoint),
		}
	}
}

// pgReferencePosition is one FHIR reference position inside the canonical
// payload document: the element path locating the reference inside the
// owning source document (dot-joined object keys, array levels unnamed, the
// terminal "reference" key excluded), the parsed reference type (empty for
// a typeless reference), and the parsed logical id.
type pgReferencePosition struct {
	path      string
	refType   string
	logicalID string
}

// scanPayloadReferencePositions returns every FHIR reference position in
// the canonical payload document in deterministic canonical order: object
// keys visited sorted by byte order, array items in document order. The
// canonical input is required so the walk cannot depend on per-process map
// serialization order. A JSON null document holds no references; anything
// that is neither an object nor null is refused.
func scanPayloadReferencePositions(canonical []byte) ([]pgReferencePosition, error) {
	trimmed := strings.TrimSpace(string(canonical))
	if trimmed == "null" || trimmed == "" {
		return nil, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &object); err != nil {
		return nil, fmt.Errorf("the built payload document must be a JSON object: %w", err)
	}
	if object == nil {
		return nil, fmt.Errorf("the built payload document must be a JSON object")
	}
	var out []pgReferencePosition
	collectReferencePositions(object, "", &out)
	return out, nil
}

// collectReferencePositions walks one decoded payload object depth first
// with keys in byte order, recording every string under a "reference" key.
// The terminal "reference" key is excluded from the path so the path names
// the element holding the reference (FHIR element-path style); every other
// key, including a nested object also named "reference", stays in the path.
func collectReferencePositions(object map[string]json.RawMessage, prefix string, out *[]pgReferencePosition) {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		walkReferenceValue(object[key], joinPath(prefix, key), out)
	}
}

// walkReferenceValue descends one payload value. A string under a key
// named "reference" that parses as a reference records a position; strings
// under any other key are opaque data.
func walkReferenceValue(raw json.RawMessage, path string, out *[]pgReferencePosition) {
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case strings.HasPrefix(trimmed, "{"):
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil || object == nil {
			return
		}
		collectReferencePositions(object, path, out)
	case strings.HasPrefix(trimmed, "["):
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return
		}
		for _, item := range items {
			walkReferenceValue(item, path, out)
		}
	case path == "reference" || strings.HasSuffix(path, ".reference"):
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return
		}
		if refType, logicalID, ok := parseFHIRReferenceString(value); ok {
			*out = append(*out, pgReferencePosition{
				path:      strings.TrimSuffix(strings.TrimSuffix(path, "reference"), "."),
				refType:   refType,
				logicalID: logicalID,
			})
		}
	}
}

// parseFHIRReferenceString parses a FHIR reference value into its typed
// identity exactly as the builders split it: the segment before the first
// slash is the type, the segment(s) after it up to the next slash the
// logical id. A value with no usable slash parses to a typeless reference
// with the whole value as id (the generic path strips types from edge
// endpoints and derives them from labels, so a position must be able to
// back an untyped claim).
func parseFHIRReferenceString(value string) (refType, logicalID string, ok bool) {
	first := strings.IndexByte(value, '/')
	if first <= 0 || first == len(value)-1 {
		return "", value, true
	}
	refType = value[:first]
	logicalID = value[first+1:]
	if second := strings.IndexByte(logicalID, '/'); second >= 0 {
		logicalID = logicalID[:second]
	}
	if logicalID == "" {
		return "", "", false
	}
	return refType, logicalID, true
}

// joinPath appends one object key to a dot-joined path.
func joinPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

// selectReferencePositions returns the payload positions that can back an
// edge document with the given label whose peer endpoint carries the given
// typed collection and key part, in canonical order. A position must
// project (via qualify — raw SanitizeKey, or the generation-qualified
// g_<hash> the builder would have written) to the document's key part:
// under a qualified batch the document carries only the physical key, so
// this equivalence, not a raw string compare, is what proves the position
// produced the document and supplies the stored logical id. The peer type
// then decides among matches: exact-type positions win, else typeless
// positions win, else (a builder-derived column type that disagrees with
// the payload's own type prefix, as the generic back-reference path
// produces) all key-matching positions stay. Within the survivors,
// positions whose path shares a token with the edge label (underscore- and
// dot-separated) win when any do, which pins a target reachable through
// two different fields to the field its rule names; a label naming no
// candidate path keeps all candidates.
func selectReferencePositions(
	positions []pgReferencePosition,
	peerType, peerKey, label string,
	qualify func(logicalID string) string,
) []pgReferencePosition {
	var idMatches []pgReferencePosition
	for _, position := range positions {
		if qualify(position.logicalID) == peerKey {
			idMatches = append(idMatches, position)
		}
	}
	if len(idMatches) == 0 {
		return nil
	}
	pick := func(predicate func(pgReferencePosition) bool) ([]pgReferencePosition, bool) {
		var kept []pgReferencePosition
		for _, position := range idMatches {
			if predicate(position) {
				kept = append(kept, position)
			}
		}
		return kept, len(kept) > 0
	}
	if kept, ok := pick(func(p pgReferencePosition) bool { return p.refType == peerType }); ok {
		idMatches = kept
	} else if kept, ok := pick(func(p pgReferencePosition) bool { return p.refType == "" }); ok {
		idMatches = kept
	}
	if kept, ok := pick(func(p pgReferencePosition) bool { return labelNamesPathToken(label, p.path) }); ok {
		idMatches = kept
	}
	return idMatches
}

// labelNamesPathToken reports whether any underscore- or dot-separated
// token of the edge label equals any dot-separated segment of the position
// path. Graph schema labels name their source field
// ("subject_Patient", "actor_reference_BodyStructure", "link"), so label
// tokens are the strongest available signal of which payload field built
// an edge document.
func labelNamesPathToken(label, path string) bool {
	for _, pathSegment := range strings.Split(path, ".") {
		if pathSegment == "" {
			continue
		}
		for _, labelToken := range strings.FieldsFunc(label, func(r rune) bool {
			return r == '_' || r == '.'
		}) {
			if labelToken == pathSegment {
				return true
			}
		}
	}
	return false
}
