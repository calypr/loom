// PostgreSQL resource-row writes for the bounded generation ingest pipeline
// (pg-ingest-01, ingest surface).
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
// document inserter. Built edges are deliberately not written here; the
// resource row is this surface's contract.
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
