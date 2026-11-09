// Bounded, idempotent resource-row batch writes for the PostgreSQL 18
// source store (pg-ingest-01, store surface).
//
// ResourceWriter is the resource-only surface of the ledgered COPY batch
// writer (BatchWriter, pg-store-06): it accepts one bounded slice of
// validated catalog.PGResourceRow values under an explicit
// project/dataset-generation scope and commits the whole batch — COPY into
// transaction-local staging, the natural-identity guards, the publish into
// fhir_resource, and the batch_write_ledger marker (migration 000008) — in
// one transaction. It reuses BatchWriter wholesale rather than restaging or
// republishing anything: the guarantees below are BatchWriter's, restated
// for the surface that cannot even carry an edge row.
//
// Guarantees (proven in resource_writer_test.go against real PostgreSQL 18):
//   - Exact column writes: every offered row lands with its exact JSON
//     payload (canonicalized by JSONB, per migration 000002) and its typed
//     project, dataset_generation, resource_type, logical_id, and
//     auth_resource_path columns; the storage identity (resource_seq) is
//     always server-assigned — a row offering one is refused before any
//     round trip.
//   - Insert-only natural identity: a duplicate natural identity
//     (project, dataset_generation, resource_type, logical_id) whose content
//     differs — within the batch or against a stored row — returns a typed
//     *BatchResourceConflictError and the whole batch rolls back, so the
//     prior payload is preserved and no partial batch lands.
//   - Idempotent replay: re-offering an identical committed batch under the
//     same batch identity reads the ledger marker and is a no-op returning
//     the original commit's exact accounting; reusing the identity with
//     different content is a typed *BatchIdentityConflictError and writes
//     nothing. Content-identical rows across distinct batches collapse the
//     same way single-row inserts do.
//   - Exact commit accounting: the reported counts are the rows the publish
//     statement actually inserted, and they are only ever returned after the
//     transaction commits — a batch that fails anywhere, including a
//     deliberate break at the commit step, returns an error and leaves
//     neither rows nor a ledger marker behind.
//   - Pre-round-trip refusal: a malformed batch (blank or over-long batch
//     identity, blank project, no rows, over-capacity, a row scoped outside
//     the batch, or a row failing the single-row insert contract) is refused
//     with ErrInvalidBatch before a connection is acquired.
//
// There is no cross-database atomicity and no backend fallback: every
// failure is a named error the caller must surface. This file owns bulk
// resource WRITE only; reads live in resource.go, edges belong to
// BatchWriter/EdgeRepository, and row decoding from built FHIR vertices
// belongs to the ingest writer (internal/ingest/postgres_writer.go).
package postgres

import (
	"context"
	"fmt"

	"github.com/calypr/loom/internal/catalog"
)

// ResourceBatch is one bounded, replayable, resource-only write unit. The
// contract is Batch's with the edge side removed: ID is the caller's
// deterministic batch identity (stable across retries of one write unit,
// never a per-attempt nonce), every row must carry exactly the batch's
// Project and DatasetGeneration, the batch is non-empty, and the row count
// is bounded by MaxBatchResources.
type ResourceBatch struct {
	ID                string
	Project           string
	DatasetGeneration string
	Rows              []catalog.PGResourceRow
}

// ResourceBatchResult is the outcome of a WriteResourceBatch call, with the
// same exact-commit accounting as BatchResult minus the edge columns that
// this surface cannot carry. On a ledger replay the counts are the original
// commit's numbers read back from the ledger.
type ResourceBatchResult struct {
	BatchID string
	// Digest is the lowercase hex SHA-256 of the canonical batch content
	// (the same encoding BatchWriter fingerprints into the ledger).
	Digest string
	// Committed counts rows the publish statement actually inserted into
	// fhir_resource (a replay reports the original commit's number).
	Committed int64
	// SkippedIdentical counts offered rows that needed no write: fully
	// identical duplicates within the batch, or rows already stored
	// content-identically.
	SkippedIdentical int64
	// Replay is true when the ledger already held this batch identity with
	// an equal digest and the call wrote nothing.
	Replay bool
}

// ResourceWriter publishes bounded, replayable resource batches into one
// schema of the PostgreSQL source store. Create one with NewResourceWriter.
// Like BatchWriter it holds no mutex and no session state: one batch is one
// transaction on one pooled connection, and independent batches never share
// state. The caller owns the connection source and the schema lifecycle
// (MigrateSchema must have applied migrations 000002 and 000008 before use).
type ResourceWriter struct {
	// batch is the reused pg-store-06 engine. In-package tests may set its
	// failHook/inTxProbe seams directly, exactly as batch_writer_test.go
	// does; production construction always leaves them nil.
	batch *BatchWriter
}

// NewResourceWriter validates the connection source and schema name and
// binds the writer to <schema>.fhir_resource and <schema>.batch_write_ledger
// through the reused BatchWriter. The connection source must be live only
// when WriteResourceBatch runs, not here.
func NewResourceWriter(src ConnSource, schema string) (*ResourceWriter, error) {
	batch, err := NewBatchWriter(src, schema)
	if err != nil {
		return nil, err
	}
	return &ResourceWriter{batch: batch}, nil
}

// WriteResourceBatch commits one bounded resource batch atomically through
// the idempotent batch writer: claim the ledger marker, COPY the rows,
// refuse duplicate natural identities whose content differs (typed errors,
// whole batch rolled back), publish the distinct rows into fhir_resource,
// record the exact committed count in the marker, and commit. The returned
// counts describe committed rows and are produced only after a successful
// commit; any failure returns an error and persists nothing. An empty batch
// is refused with ErrInvalidBatch naming the resource batch, before the
// engine is asked to do anything.
func (w *ResourceWriter) WriteResourceBatch(ctx context.Context, batch ResourceBatch) (ResourceBatchResult, error) {
	if len(batch.Rows) == 0 {
		return ResourceBatchResult{}, fmt.Errorf("%w: resource batch %q carries no rows",
			ErrInvalidBatch, batch.ID)
	}
	result, err := w.batch.WriteBatch(ctx, Batch{
		ID:                batch.ID,
		Project:           batch.Project,
		DatasetGeneration: batch.DatasetGeneration,
		Resources:         batch.Rows,
	})
	if err != nil {
		return ResourceBatchResult{}, err
	}
	// This surface builds Batch with no Edges, so the engine cannot report
	// edge accounting; if that ever changes, fail loudly rather than
	// dropping the numbers from the result.
	if result.CommittedEdges != 0 || result.SkippedIdenticalEdges != 0 {
		return ResourceBatchResult{}, &BatchAccountingError{
			BatchID:   batch.ID,
			Side:      "edges",
			Expected:  0,
			Committed: result.CommittedEdges,
		}
	}
	return ResourceBatchResult{
		BatchID:          result.BatchID,
		Digest:           result.Digest,
		Committed:        result.CommittedResources,
		SkippedIdentical: result.SkippedIdenticalResources,
		Replay:           result.Replay,
	}, nil
}
