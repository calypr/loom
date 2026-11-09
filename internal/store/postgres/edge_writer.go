// Bounded, idempotent edge-occurrence batch writes for the PostgreSQL 18
// source store (pg-ingest-02, store surface).
//
// EdgeWriter is the edge-only surface of the ledgered COPY batch writer
// (BatchWriter, pg-store-06): it accepts one bounded slice of validated
// EdgeOccurrenceRow values under an explicit project/dataset-generation
// scope and commits the whole batch — COPY into transaction-local staging,
// the occurrence-identity and source-precondition guards, the publish into
// fhir_edge_occurrence, and the batch_write_ledger marker (migration
// 000008) — in one transaction. It reuses BatchWriter wholesale rather than
// restaging or republishing anything: the guarantees below are
// BatchWriter's, restated for the surface that cannot even carry a resource
// row.
//
// Guarantees (proven in edge_writer_test.go against real PostgreSQL 18):
//   - Exact column writes: every offered occurrence lands with its exact
//     label, typed endpoint columns, auth_resource_path, source_path,
//     source_ordinal, and logical_edge_key (SQL NULL distinct from the
//     empty string); the storage identity (edge_seq) is always
//     server-assigned — a row offering one is refused before any round trip.
//   - Repeated-reference multiplicity: two identical endpoint references
//     that differ only in source_path or source_ordinal are two
//     independently addressable rows (the nine-column occurrence identity
//     is unique, so only the fully identical occurrence collapses).
//   - Insert-only occurrence identity: a duplicate occurrence identity
//     whose content (auth path or logical edge key) differs — within the
//     batch or against a stored row — returns a typed
//     *BatchEdgeConflictError and the whole batch rolls back, preserving
//     the prior occurrence.
//   - Source precondition: an occurrence whose source resource is absent
//     from fhir_resource in the same (project, dataset_generation) scope
//     returns the typed *EdgeSourceMissingError and the batch writes
//     nothing; a missing target is not an error — the target identity is
//     stored and reported unresolved at read time.
//   - Idempotent replay: re-offering an identical committed batch under the
//     same batch identity reads the ledger marker and is a no-op returning
//     the original commit's exact accounting; reusing the identity with
//     different content is a typed *BatchIdentityConflictError and writes
//     nothing.
//   - Exact commit accounting: the reported counts are the rows the publish
//     statement actually inserted, and they are only ever returned after
//     the transaction commits — a batch that fails anywhere (including an
//     injected break at any durability seam) returns an error and leaves
//     neither rows nor a ledger marker behind.
//   - Pre-round-trip refusal: a malformed batch (blank or over-long batch
//     identity, blank project, no rows, over-capacity, a row scoped outside
//     the batch, or a row failing the single-row insert contract) is
//     refused with ErrInvalidBatch before a connection is acquired.
//
// There is no cross-database atomicity and no backend fallback: every
// failure is a named error the caller must surface. This file owns bulk
// edge WRITE only; single-row writes and traversals live in edge.go,
// resources belong to resource_writer.go/ResourceWriter, and conversion
// from built FHIR edge documents belongs to the ingest writer
// (internal/ingest/postgres_writer.go).
package postgres

import (
	"context"
	"fmt"
)

// EdgeBatch is one bounded, replayable, edge-only write unit. The contract
// is Batch's with the resource side removed: ID is the caller's
// deterministic batch identity (stable across retries of one write unit,
// never a per-attempt nonce), every row must carry exactly the batch's
// Project and DatasetGeneration, the batch is non-empty, and the row count
// is bounded by MaxBatchEdges. Edge sources are not written by this surface:
// the source resource must already exist in fhir_resource in the batch's
// own (project, dataset_generation) scope, exactly as
// EdgeRepository.InsertEdge requires.
type EdgeBatch struct {
	ID                string
	Project           string
	DatasetGeneration string
	Rows              []EdgeOccurrenceRow
}

// EdgeBatchResult is the outcome of a WriteEdgeBatch call, with the same
// exact-commit accounting as BatchResult minus the resource columns this
// surface cannot carry. On a ledger replay the counts are the original
// commit's numbers read back from the ledger.
type EdgeBatchResult struct {
	BatchID string
	// Digest is the lowercase hex SHA-256 of the canonical batch content
	// (the same encoding BatchWriter fingerprints into the ledger).
	Digest string
	// Committed counts rows the publish statement actually inserted into
	// fhir_edge_occurrence (a replay reports the original commit's number).
	Committed int64
	// SkippedIdentical counts offered rows that needed no write: fully
	// identical duplicates within the batch, or rows already stored
	// content-identically.
	SkippedIdentical int64
	// Replay is true when the ledger already held this batch identity with
	// an equal digest and the call wrote nothing.
	Replay bool
}

// EdgeWriter publishes bounded, replayable edge-occurrence batches into one
// schema of the PostgreSQL source store. Create one with NewEdgeWriter.
// Like BatchWriter it holds no mutex and no session state: one batch is one
// transaction on one pooled connection, and independent batches never share
// state. The caller owns the connection source and the schema lifecycle
// (MigrateSchema must have applied migrations 000002, 000004, and 000008
// before use).
type EdgeWriter struct {
	// batch is the reused pg-store-06 engine. In-package tests may set its
	// failHook/inTxProbe seams directly, exactly as batch_writer_test.go
	// does; production construction always leaves them nil.
	batch *BatchWriter
}

// NewEdgeWriter validates the connection source and schema name and binds
// the writer to <schema>.fhir_edge_occurrence, <schema>.fhir_resource (the
// source precondition), and <schema>.batch_write_ledger through the reused
// BatchWriter. The connection source must be live only when WriteEdgeBatch
// runs, not here.
func NewEdgeWriter(src ConnSource, schema string) (*EdgeWriter, error) {
	batch, err := NewBatchWriter(src, schema)
	if err != nil {
		return nil, err
	}
	return &EdgeWriter{batch: batch}, nil
}

// WriteEdgeBatch commits one bounded edge-occurrence batch atomically
// through the idempotent batch writer: claim the ledger marker, COPY the
// rows, refuse duplicate occurrence identities whose content differs and
// absent source resources (typed errors, whole batch rolled back), publish
// the distinct occurrences into fhir_edge_occurrence (each still guarded by
// a source EXISTS), record the exact committed count in the marker, and
// commit. The returned counts describe committed rows and are produced only
// after a successful commit; any failure returns an error and persists
// nothing. An empty batch is refused with ErrInvalidBatch naming the edge
// batch, before the engine is asked to do anything.
func (w *EdgeWriter) WriteEdgeBatch(ctx context.Context, batch EdgeBatch) (EdgeBatchResult, error) {
	if len(batch.Rows) == 0 {
		return EdgeBatchResult{}, fmt.Errorf("%w: edge batch %q carries no rows",
			ErrInvalidBatch, batch.ID)
	}
	result, err := w.batch.WriteBatch(ctx, Batch{
		ID:                batch.ID,
		Project:           batch.Project,
		DatasetGeneration: batch.DatasetGeneration,
		Edges:             batch.Rows,
	})
	if err != nil {
		return EdgeBatchResult{}, err
	}
	// This surface builds Batch with no Resources, so the engine cannot
	// report resource accounting; if that ever changes, fail loudly rather
	// than dropping the numbers from the result.
	if result.CommittedResources != 0 || result.SkippedIdenticalResources != 0 {
		return EdgeBatchResult{}, &BatchAccountingError{
			BatchID:   batch.ID,
			Side:      "resources",
			Expected:  0,
			Committed: result.CommittedResources,
		}
	}
	return EdgeBatchResult{
		BatchID:          result.BatchID,
		Digest:           result.Digest,
		Committed:        result.CommittedEdges,
		SkippedIdentical: result.SkippedIdenticalEdges,
		Replay:           result.Replay,
	}, nil
}
