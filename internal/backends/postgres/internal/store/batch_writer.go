package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/source"
	"github.com/jackc/pgx/v5"
)

const (
	batchLedgerTableName = "batch_write_ledger"
	MaxBatchResources    = 1000
	MaxBatchEdges        = 1000
	MaxBatchIDLength     = 255
)

const (
	stagingResourceTable = "stg_batch_resource"
	stagingEdgeTable     = "stg_batch_edge"
)

var (
	ErrInvalidBatch          = errors.New("postgres: invalid batch")
	ErrBatchIdentityConflict = errors.New("postgres: batch identity conflict")
	ErrBatchRowConflict      = errors.New("postgres: batch row conflict")
	ErrBatchAccounting       = errors.New("postgres: batch commit accounting divergence")
	ErrBatchWrite            = errors.New("postgres: batch write failed")
)

type BatchIdentityConflictError struct {
	BatchID        string
	LedgerDigest   string
	ProvidedDigest string
}

func (e *BatchIdentityConflictError) Error() string {
	return fmt.Sprintf("postgres: batch %q already committed with digest %s; "+
		"reusing the identity with digest %s is refused and writes nothing",
		e.BatchID, e.LedgerDigest, e.ProvidedDigest)
}

func (e *BatchIdentityConflictError) Unwrap() error { return ErrBatchIdentityConflict }

type BatchResourceConflictError struct {
	Project           string
	DatasetGeneration string
	ResourceType      string
	LogicalID         string
	InBatch           bool
}

func (e *BatchResourceConflictError) Error() string {
	where := "an already-stored row"
	if e.InBatch {
		where = "another row in this batch"
	}
	return fmt.Sprintf("postgres: batch refused: resource %s/%s/%s/%s duplicates %s with different content; "+
		"the batch wrote nothing and the stored payload is preserved",
		e.Project, e.DatasetGeneration, e.ResourceType, e.LogicalID, where)
}

func (e *BatchResourceConflictError) Unwrap() error { return ErrBatchRowConflict }

type BatchEdgeConflictError struct {
	Project            string
	DatasetGeneration  string
	SourceResourceType string
	SourceLogicalID    string
	Label              string
	TargetResourceType string
	TargetLogicalID    string
	SourcePath         string
	SourceOrdinal      int64
	InBatch            bool
}

func (e *BatchEdgeConflictError) Error() string {
	where := "an already-stored occurrence"
	if e.InBatch {
		where = "another occurrence in this batch"
	}
	return fmt.Sprintf("postgres: batch refused: edge occurrence %s/%s/%s(%s) -%s-> %s/%s at path %q ordinal %d duplicates %s with different content; "+
		"the batch wrote nothing and the stored occurrence is preserved",
		e.Project, e.DatasetGeneration, e.SourceResourceType, e.SourceLogicalID, e.Label,
		e.TargetResourceType, e.TargetLogicalID, e.SourcePath, e.SourceOrdinal, where)
}

func (e *BatchEdgeConflictError) Unwrap() error { return ErrBatchRowConflict }

type BatchAccountingError struct {
	BatchID   string
	Side      string
	Expected  int64
	Committed int64
}

func (e *BatchAccountingError) Error() string {
	return fmt.Sprintf("postgres: batch %q %s accounting diverged: expected %d inserted rows, publish returned %d; "+
		"a concurrent writer changed the scope mid-batch and the batch rolled back",
		e.BatchID, e.Side, e.Expected, e.Committed)
}

func (e *BatchAccountingError) Unwrap() error { return ErrBatchAccounting }

type BatchWriteError struct {
	Op      string
	BatchID string
	Err     error
}

func (e *BatchWriteError) Error() string {
	return fmt.Sprintf("postgres: batch %q failed during %s: %v", e.BatchID, e.Op, e.Err)
}

func (e *BatchWriteError) Unwrap() []error { return []error{ErrBatchWrite, e.Err} }

type Batch struct {
	ID                string
	Project           string
	DatasetGeneration string
	Resources         []source.Resource
	Edges             []EdgeOccurrenceRow
}

type BatchResult struct {
	BatchID                   string
	Digest                    string
	CommittedResources        int64
	CommittedEdges            int64
	SkippedIdenticalResources int64
	SkippedIdenticalEdges     int64
	Replay                    bool
}

type BatchWriter struct {
	src              ConnSource
	schema           string
	resourceTableFQN string
	edgeTableFQN     string
	ledgerFQN        string
	failHook         func(step string) error
	inTxProbe        func(step, batchID string, tx pgx.Tx)
}

func NewBatchWriter(src ConnSource, schema string) (*BatchWriter, error) {
	if src == nil {
		return nil, errors.New("postgres: nil batch connection source")
	}
	if !schemaNamePattern.MatchString(schema) {
		return nil, fmt.Errorf("%w: %q must match [a-z_][a-z0-9_]{0,62}", ErrInvalidSchemaName, schema)
	}
	return &BatchWriter{
		src:              src,
		schema:           schema,
		resourceTableFQN: pgx.Identifier{schema, resourceTableName}.Sanitize(),
		edgeTableFQN:     pgx.Identifier{schema, edgeTableName}.Sanitize(),
		ledgerFQN:        pgx.Identifier{schema, batchLedgerTableName}.Sanitize(),
	}, nil
}

const (
	stepBegin          = "begin"
	stepReadLedger     = "read-ledger"
	stepClaimLedger    = "claim-ledger"
	stepStageResources = "stage-resources"
	stepCheckResources = "check-resources"
	stepPublishResourc = "publish-resources"
	stepStageEdges     = "stage-edges"
	stepCheckEdges     = "check-edges"
	stepPublishEdges   = "publish-edges"
	stepFinalizeLedger = "finalize-ledger"
	stepCommit         = "commit"
)

func (w *BatchWriter) WriteBatch(ctx context.Context, batch Batch) (BatchResult, error) {
	if err := validateBatch(batch); err != nil {
		return BatchResult{}, err
	}
	digest := batchDigest(batch)

	conn, err := w.src.Acquire(ctx)
	if err != nil {
		return BatchResult{}, &BatchWriteError{Op: "acquire", BatchID: batch.ID, Err: err}
	}
	defer conn.Release()

	if err := w.hookFail(stepBegin); err != nil {
		return BatchResult{}, &BatchWriteError{Op: "begin", BatchID: batch.ID, Err: err}
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return BatchResult{}, &BatchWriteError{Op: "begin", BatchID: batch.ID, Err: err}
	}
	abandoned := true
	defer func() {
		if abandoned {
			_ = tx.Rollback(ctx)
		}
	}()

	setSearchPath := "SET LOCAL search_path TO " + pgx.Identifier{w.schema}.Sanitize()
	if _, err := tx.Exec(ctx, setSearchPath); err != nil {
		return BatchResult{}, &BatchWriteError{Op: "search_path", BatchID: batch.ID, Err: err}
	}
	w.probe(stepBegin, batch.ID, tx)

	if err := w.hookFail(stepReadLedger); err != nil {
		return BatchResult{}, &BatchWriteError{Op: "ledger", BatchID: batch.ID, Err: err}
	}
	if res, done, err := w.checkLedger(ctx, tx, batch, digest); err != nil || done {
		return res, err
	}
	w.probe(stepReadLedger, batch.ID, tx)

	if err := w.hookFail(stepClaimLedger); err != nil {
		return BatchResult{}, &BatchWriteError{Op: "ledger", BatchID: batch.ID, Err: err}
	}
	claimed, err := w.claimLedger(ctx, tx, batch, digest)
	if err != nil {
		return BatchResult{}, err
	}
	if !claimed {
		res, _, err := w.readLedgerRow(ctx, tx, batch, digest)
		return res, err
	}
	w.probe(stepClaimLedger, batch.ID, tx)

	var committedResources, committedEdges int64
	if len(batch.Resources) > 0 {
		if err := w.hookFail(stepStageResources); err != nil {
			return BatchResult{}, &BatchWriteError{Op: "create-staging", BatchID: batch.ID, Err: err}
		}
		if err := w.stageResources(ctx, tx, batch); err != nil {
			return BatchResult{}, err
		}
		w.probe(stepStageResources, batch.ID, tx)

		if err := w.hookFail(stepCheckResources); err != nil {
			return BatchResult{}, &BatchWriteError{Op: "check", BatchID: batch.ID, Err: err}
		}
		expectedResources, err := w.guardResources(ctx, tx, batch)
		if err != nil {
			return BatchResult{}, err
		}
		w.probe(stepCheckResources, batch.ID, tx)

		if err := w.hookFail(stepPublishResourc); err != nil {
			return BatchResult{}, &BatchWriteError{Op: "publish", BatchID: batch.ID, Err: err}
		}
		committedResources, err = w.publishResources(ctx, tx, batch.ID)
		if err != nil {
			return BatchResult{}, err
		}
		if committedResources != expectedResources {
			return BatchResult{}, &BatchAccountingError{
				BatchID: batch.ID, Side: "resources",
				Expected: expectedResources, Committed: committedResources,
			}
		}
		w.probe(stepPublishResourc, batch.ID, tx)
	}

	if len(batch.Edges) > 0 {
		if err := w.hookFail(stepStageEdges); err != nil {
			return BatchResult{}, &BatchWriteError{Op: "create-staging", BatchID: batch.ID, Err: err}
		}
		if err := w.stageEdges(ctx, tx, batch); err != nil {
			return BatchResult{}, err
		}
		w.probe(stepStageEdges, batch.ID, tx)

		if err := w.hookFail(stepCheckEdges); err != nil {
			return BatchResult{}, &BatchWriteError{Op: "check", BatchID: batch.ID, Err: err}
		}
		expectedEdges, err := w.guardEdges(ctx, tx, batch)
		if err != nil {
			return BatchResult{}, err
		}
		w.probe(stepCheckEdges, batch.ID, tx)

		if err := w.hookFail(stepPublishEdges); err != nil {
			return BatchResult{}, &BatchWriteError{Op: "publish", BatchID: batch.ID, Err: err}
		}
		committedEdges, err = w.publishEdges(ctx, tx, batch.ID)
		if err != nil {
			return BatchResult{}, err
		}
		if committedEdges != expectedEdges {
			return BatchResult{}, &BatchAccountingError{
				BatchID: batch.ID, Side: "edges",
				Expected: expectedEdges, Committed: committedEdges,
			}
		}
		w.probe(stepPublishEdges, batch.ID, tx)
	}

	if err := w.hookFail(stepFinalizeLedger); err != nil {
		return BatchResult{}, &BatchWriteError{Op: "ledger", BatchID: batch.ID, Err: err}
	}
	if _, err := tx.Exec(ctx,
		"UPDATE "+w.ledgerFQN+" SET resource_count = $2, edge_count = $3, committed_at = clock_timestamp() WHERE batch_id = $1",
		batch.ID, committedResources, committedEdges); err != nil {
		return BatchResult{}, &BatchWriteError{Op: "ledger", BatchID: batch.ID, Err: err}
	}
	w.probe(stepFinalizeLedger, batch.ID, tx)

	if err := w.hookFail(stepCommit); err != nil {
		return BatchResult{}, &BatchWriteError{Op: "commit", BatchID: batch.ID, Err: err}
	}
	if err := tx.Commit(ctx); err != nil {
		return BatchResult{}, &BatchWriteError{Op: "commit", BatchID: batch.ID, Err: err}
	}
	abandoned = false

	return BatchResult{
		BatchID:                   batch.ID,
		Digest:                    digest,
		CommittedResources:        committedResources,
		CommittedEdges:            committedEdges,
		SkippedIdenticalResources: int64(len(batch.Resources)) - committedResources,
		SkippedIdenticalEdges:     int64(len(batch.Edges)) - committedEdges,
	}, nil
}

func (w *BatchWriter) hookFail(step string) error {
	if w.failHook == nil {
		return nil
	}
	return w.failHook(step)
}

func (w *BatchWriter) probe(step, batchID string, tx pgx.Tx) {
	if w.inTxProbe == nil {
		return
	}
	w.inTxProbe(step, batchID, tx)
}

func (w *BatchWriter) checkLedger(ctx context.Context, tx pgx.Tx, batch Batch, digest string) (BatchResult, bool, error) {
	var ledgerDigest string
	var ledgerResources, ledgerEdges int64
	err := tx.QueryRow(ctx,
		"SELECT digest, resource_count, edge_count FROM "+w.ledgerFQN+" WHERE batch_id = $1",
		batch.ID).Scan(&ledgerDigest, &ledgerResources, &ledgerEdges)
	if errors.Is(err, pgx.ErrNoRows) {
		return BatchResult{}, false, nil
	}
	if err != nil {
		return BatchResult{}, false, &BatchWriteError{Op: "ledger", BatchID: batch.ID, Err: err}
	}
	res, err := classifyLedgerRow(batch, digest, ledgerDigest, ledgerResources, ledgerEdges)
	return res, true, err
}

func (w *BatchWriter) claimLedger(ctx context.Context, tx pgx.Tx, batch Batch, digest string) (bool, error) {
	var claimedID string
	err := tx.QueryRow(ctx,
		"INSERT INTO "+w.ledgerFQN+
			" (batch_id, project, dataset_generation, digest, resource_count, edge_count) "+
			"VALUES ($1, $2, $3, $4::text, 0, 0) ON CONFLICT (batch_id) DO NOTHING RETURNING batch_id",
		batch.ID, batch.Project, batch.DatasetGeneration, digest).Scan(&claimedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, &BatchWriteError{Op: "ledger", BatchID: batch.ID, Err: err}
	}
	return true, nil
}

func (w *BatchWriter) readLedgerRow(ctx context.Context, tx pgx.Tx, batch Batch, digest string) (BatchResult, bool, error) {
	var ledgerDigest string
	var ledgerResources, ledgerEdges int64
	err := tx.QueryRow(ctx,
		"SELECT digest, resource_count, edge_count FROM "+w.ledgerFQN+" WHERE batch_id = $1",
		batch.ID).Scan(&ledgerDigest, &ledgerResources, &ledgerEdges)
	if errors.Is(err, pgx.ErrNoRows) {
		return BatchResult{}, false, &BatchWriteError{
			Op: "ledger", BatchID: batch.ID,
			Err: errors.New("concurrent batch claim vanished before it could be classified; retry the batch"),
		}
	}
	if err != nil {
		return BatchResult{}, false, &BatchWriteError{Op: "ledger", BatchID: batch.ID, Err: err}
	}
	res, err := classifyLedgerRow(batch, digest, ledgerDigest, ledgerResources, ledgerEdges)
	return res, true, err
}

func classifyLedgerRow(batch Batch, digest, ledgerDigest string, ledgerResources, ledgerEdges int64) (BatchResult, error) {
	if digest != ledgerDigest {
		return BatchResult{}, &BatchIdentityConflictError{
			BatchID:        batch.ID,
			LedgerDigest:   ledgerDigest,
			ProvidedDigest: digest,
		}
	}
	return BatchResult{
		BatchID:                   batch.ID,
		Digest:                    digest,
		CommittedResources:        ledgerResources,
		CommittedEdges:            ledgerEdges,
		SkippedIdenticalResources: int64(len(batch.Resources)) - ledgerResources,
		SkippedIdenticalEdges:     int64(len(batch.Edges)) - ledgerEdges,
		Replay:                    true,
	}, nil
}

func (w *BatchWriter) stagingResourceFQN() string {
	return pgx.Identifier{"pg_temp", stagingResourceTable}.Sanitize()
}

func (w *BatchWriter) stagingEdgeFQN() string {
	return pgx.Identifier{"pg_temp", stagingEdgeTable}.Sanitize()
}

func (w *BatchWriter) stageResources(ctx context.Context, tx pgx.Tx, batch Batch) error {
	const createSQL = "CREATE TEMP TABLE " + stagingResourceTable + " (" +
		"ordinal bigint NOT NULL, " +
		"project text NOT NULL, " +
		"dataset_generation text NOT NULL, " +
		"resource_type text NOT NULL, " +
		"logical_id text NOT NULL, " +
		"auth_resource_path text NOT NULL, " +
		"payload text" +
		") ON COMMIT DROP"
	if _, err := tx.Exec(ctx, createSQL); err != nil {
		return &BatchWriteError{Op: "create-staging", BatchID: batch.ID, Err: err}
	}
	columns := []string{"ordinal", "project", "dataset_generation", "resource_type",
		"logical_id", "auth_resource_path", "payload"}
	rows := make([][]any, 0, len(batch.Resources))
	for i, rec := range batch.Resources {
		var payload any
		if rec.Payload != nil {
			payload = string(rec.Payload)
		}
		rows = append(rows, []any{int64(i), rec.Project, rec.DatasetGeneration,
			rec.ResourceType, rec.LogicalID, rec.AuthResourcePath, payload})
	}
	n, err := tx.CopyFrom(ctx, pgx.Identifier{"pg_temp", stagingResourceTable}, columns,
		pgx.CopyFromRows(rows))
	if err != nil {
		return &BatchWriteError{Op: "copy", BatchID: batch.ID, Err: err}
	}
	if n != int64(len(batch.Resources)) {
		return &BatchAccountingError{BatchID: batch.ID, Side: "resources",
			Expected: int64(len(batch.Resources)), Committed: n}
	}
	return nil
}

func (w *BatchWriter) guardResources(ctx context.Context, tx pgx.Tx, batch Batch) (int64, error) {
	stg := w.stagingResourceFQN()
	var resourceType, logicalID string
	inBatchSQL := "SELECT a.resource_type, a.logical_id FROM " + stg + " a JOIN " + stg + " b" +
		" ON a.project = b.project AND a.dataset_generation = b.dataset_generation" +
		" AND a.resource_type = b.resource_type AND a.logical_id = b.logical_id" +
		" AND (a.auth_resource_path IS DISTINCT FROM b.auth_resource_path" +
		"      OR (a.payload IS NULL) <> (b.payload IS NULL)" +
		"      OR a.payload::jsonb IS DISTINCT FROM b.payload::jsonb)" +
		" ORDER BY a.ordinal, b.ordinal LIMIT 1"
	if err := tx.QueryRow(ctx, inBatchSQL).Scan(&resourceType, &logicalID); err == nil {
		return 0, &BatchResourceConflictError{
			Project: batch.Project, DatasetGeneration: batch.DatasetGeneration,
			ResourceType: resourceType, LogicalID: logicalID, InBatch: true,
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return 0, &BatchWriteError{Op: "check", BatchID: batch.ID, Err: err}
	}

	storedSQL := "SELECT s.resource_type, s.logical_id FROM " + stg + " s JOIN " + w.resourceTableFQN + " r" +
		" ON r.project = s.project AND r.dataset_generation = s.dataset_generation" +
		" AND r.resource_type = s.resource_type AND r.logical_id = s.logical_id" +
		" WHERE s.auth_resource_path IS DISTINCT FROM r.auth_resource_path" +
		"    OR (s.payload IS NULL) <> (r.payload IS NULL)" +
		"    OR s.payload::jsonb IS DISTINCT FROM r.payload" +
		" ORDER BY s.ordinal LIMIT 1"
	if err := tx.QueryRow(ctx, storedSQL).Scan(&resourceType, &logicalID); err == nil {
		return 0, &BatchResourceConflictError{
			Project: batch.Project, DatasetGeneration: batch.DatasetGeneration,
			ResourceType: resourceType, LogicalID: logicalID, InBatch: false,
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return 0, &BatchWriteError{Op: "check", BatchID: batch.ID, Err: err}
	}

	expectedSQL := "SELECT count(*) FROM (" +
		" SELECT DISTINCT project, dataset_generation, resource_type, logical_id," +
		" auth_resource_path, payload::jsonb AS payload FROM " + stg +
		") d WHERE NOT EXISTS (SELECT 1 FROM " + w.resourceTableFQN + " r" +
		" WHERE r.project = d.project AND r.dataset_generation = d.dataset_generation" +
		" AND r.resource_type = d.resource_type AND r.logical_id = d.logical_id" +
		" AND r.auth_resource_path IS NOT DISTINCT FROM d.auth_resource_path" +
		" AND r.payload IS NOT DISTINCT FROM d.payload)"
	var expected int64
	if err := tx.QueryRow(ctx, expectedSQL).Scan(&expected); err != nil {
		return 0, &BatchWriteError{Op: "check", BatchID: batch.ID, Err: err}
	}
	return expected, nil
}

func (w *BatchWriter) publishResources(ctx context.Context, tx pgx.Tx, batchID string) (int64, error) {
	sql := "INSERT INTO " + w.resourceTableFQN +
		" (project, dataset_generation, resource_type, logical_id, auth_resource_path, payload)" +
		" SELECT DISTINCT project, dataset_generation, resource_type, logical_id," +
		" auth_resource_path, payload::jsonb FROM " + w.stagingResourceFQN() +
		" ON CONFLICT ON CONSTRAINT " + resourceTableName + "_natural_identity_ux DO NOTHING" +
		" RETURNING resource_seq"
	rows, err := tx.Query(ctx, sql)
	if err != nil {
		return 0, &BatchWriteError{Op: "publish", BatchID: batchID, Err: err}
	}
	defer rows.Close()
	var n int64
	for rows.Next() {
		var seq int64
		if err := rows.Scan(&seq); err != nil {
			return 0, &BatchWriteError{Op: "publish", BatchID: batchID, Err: err}
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return 0, &BatchWriteError{Op: "publish", BatchID: batchID, Err: err}
	}
	return n, nil
}

func (w *BatchWriter) stageEdges(ctx context.Context, tx pgx.Tx, batch Batch) error {
	const createSQL = "CREATE TEMP TABLE " + stagingEdgeTable + " (" +
		"ordinal bigint NOT NULL, " +
		"project text NOT NULL, " +
		"dataset_generation text NOT NULL, " +
		"auth_resource_path text NOT NULL, " +
		"label text NOT NULL, " +
		"source_resource_type text NOT NULL, " +
		"source_logical_id text NOT NULL, " +
		"target_resource_type text NOT NULL, " +
		"target_logical_id text NOT NULL, " +
		"source_path text NOT NULL, " +
		"source_ordinal bigint NOT NULL, " +
		"logical_edge_key text" +
		") ON COMMIT DROP"
	if _, err := tx.Exec(ctx, createSQL); err != nil {
		return &BatchWriteError{Op: "create-staging", BatchID: batch.ID, Err: err}
	}
	columns := []string{"ordinal", "project", "dataset_generation", "auth_resource_path",
		"label", "source_resource_type", "source_logical_id", "target_resource_type",
		"target_logical_id", "source_path", "source_ordinal", "logical_edge_key"}
	rows := make([][]any, 0, len(batch.Edges))
	for i, rec := range batch.Edges {
		var logicalEdgeKey any
		if rec.LogicalEdgeKey != nil {
			logicalEdgeKey = *rec.LogicalEdgeKey
		}
		rows = append(rows, []any{int64(i), rec.Project, rec.DatasetGeneration,
			rec.AuthResourcePath, rec.Label, rec.SourceResourceType, rec.SourceLogicalID,
			rec.TargetResourceType, rec.TargetLogicalID, rec.SourcePath, rec.SourceOrdinal,
			logicalEdgeKey})
	}
	n, err := tx.CopyFrom(ctx, pgx.Identifier{"pg_temp", stagingEdgeTable}, columns,
		pgx.CopyFromRows(rows))
	if err != nil {
		return &BatchWriteError{Op: "copy", BatchID: batch.ID, Err: err}
	}
	if n != int64(len(batch.Edges)) {
		return &BatchAccountingError{BatchID: batch.ID, Side: "edges",
			Expected: int64(len(batch.Edges)), Committed: n}
	}
	return nil
}

func (w *BatchWriter) edgeIdentityJoin(left, right string) string {
	return left + ".project = " + right + ".project" +
		" AND " + left + ".dataset_generation = " + right + ".dataset_generation" +
		" AND " + left + ".source_resource_type = " + right + ".source_resource_type" +
		" AND " + left + ".source_logical_id = " + right + ".source_logical_id" +
		" AND " + left + ".label = " + right + ".label" +
		" AND " + left + ".target_resource_type = " + right + ".target_resource_type" +
		" AND " + left + ".target_logical_id = " + right + ".target_logical_id" +
		" AND " + left + ".source_path = " + right + ".source_path" +
		" AND " + left + ".source_ordinal = " + right + ".source_ordinal"
}

func (w *BatchWriter) guardEdges(ctx context.Context, tx pgx.Tx, batch Batch) (int64, error) {
	stg := w.stagingEdgeFQN()

	var e EdgeOccurrenceRow
	inBatchSQL := "SELECT a.project, a.dataset_generation, a.source_resource_type, a.source_logical_id," +
		" a.label, a.target_resource_type, a.target_logical_id, a.source_path, a.source_ordinal" +
		" FROM " + stg + " a JOIN " + stg + " b" +
		" ON " + w.edgeIdentityJoin("a", "b") +
		" AND (a.auth_resource_path IS DISTINCT FROM b.auth_resource_path" +
		"      OR a.logical_edge_key IS DISTINCT FROM b.logical_edge_key)" +
		" ORDER BY a.ordinal, b.ordinal LIMIT 1"
	err := tx.QueryRow(ctx, inBatchSQL).Scan(&e.Project, &e.DatasetGeneration,
		&e.SourceResourceType, &e.SourceLogicalID, &e.Label,
		&e.TargetResourceType, &e.TargetLogicalID, &e.SourcePath, &e.SourceOrdinal)
	if err == nil {
		return 0, &BatchEdgeConflictError{
			Project: e.Project, DatasetGeneration: e.DatasetGeneration,
			SourceResourceType: e.SourceResourceType, SourceLogicalID: e.SourceLogicalID,
			Label: e.Label, TargetResourceType: e.TargetResourceType,
			TargetLogicalID: e.TargetLogicalID, SourcePath: e.SourcePath,
			SourceOrdinal: e.SourceOrdinal, InBatch: true,
		}
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, &BatchWriteError{Op: "check", BatchID: batch.ID, Err: err}
	}

	storedSQL := "SELECT s.project, s.dataset_generation, s.source_resource_type, s.source_logical_id," +
		" s.label, s.target_resource_type, s.target_logical_id, s.source_path, s.source_ordinal" +
		" FROM " + stg + " s JOIN " + w.edgeTableFQN + " e" +
		" ON " + w.edgeIdentityJoin("s", "e") +
		" WHERE s.auth_resource_path IS DISTINCT FROM e.auth_resource_path" +
		"    OR s.logical_edge_key IS DISTINCT FROM e.logical_edge_key" +
		" ORDER BY s.ordinal LIMIT 1"
	err = tx.QueryRow(ctx, storedSQL).Scan(&e.Project, &e.DatasetGeneration,
		&e.SourceResourceType, &e.SourceLogicalID, &e.Label,
		&e.TargetResourceType, &e.TargetLogicalID, &e.SourcePath, &e.SourceOrdinal)
	if err == nil {
		return 0, &BatchEdgeConflictError{
			Project: e.Project, DatasetGeneration: e.DatasetGeneration,
			SourceResourceType: e.SourceResourceType, SourceLogicalID: e.SourceLogicalID,
			Label: e.Label, TargetResourceType: e.TargetResourceType,
			TargetLogicalID: e.TargetLogicalID, SourcePath: e.SourcePath,
			SourceOrdinal: e.SourceOrdinal, InBatch: false,
		}
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, &BatchWriteError{Op: "check", BatchID: batch.ID, Err: err}
	}

	sourceSQL := "SELECT s.source_resource_type, s.source_logical_id FROM " + stg + " s" +
		" WHERE NOT EXISTS (SELECT 1 FROM " + w.resourceTableFQN + " src" +
		" WHERE src.project = s.project AND src.dataset_generation = s.dataset_generation" +
		" AND src.resource_type = s.source_resource_type AND src.logical_id = s.source_logical_id)" +
		" ORDER BY s.ordinal LIMIT 1"
	var sourceType, sourceID string
	err = tx.QueryRow(ctx, sourceSQL).Scan(&sourceType, &sourceID)
	if err == nil {
		return 0, &EdgeSourceMissingError{
			Project: batch.Project, DatasetGeneration: batch.DatasetGeneration,
			SourceResourceType: sourceType, SourceLogicalID: sourceID,
		}
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, &BatchWriteError{Op: "check", BatchID: batch.ID, Err: err}
	}

	expectedSQL := "SELECT count(*) FROM (" +
		" SELECT DISTINCT project, dataset_generation, auth_resource_path, label," +
		" source_resource_type, source_logical_id, target_resource_type, target_logical_id," +
		" source_path, source_ordinal, logical_edge_key FROM " + stg +
		") d WHERE NOT EXISTS (SELECT 1 FROM " + w.edgeTableFQN + " e" +
		" WHERE " + w.edgeIdentityJoin("d", "e") +
		" AND e.auth_resource_path IS NOT DISTINCT FROM d.auth_resource_path" +
		" AND e.logical_edge_key IS NOT DISTINCT FROM d.logical_edge_key)"
	var expected int64
	if err := tx.QueryRow(ctx, expectedSQL).Scan(&expected); err != nil {
		return 0, &BatchWriteError{Op: "check", BatchID: batch.ID, Err: err}
	}
	return expected, nil
}

func (w *BatchWriter) publishEdges(ctx context.Context, tx pgx.Tx, batchID string) (int64, error) {
	sql := "INSERT INTO " + w.edgeTableFQN +
		" (project, dataset_generation, auth_resource_path, label," +
		" source_resource_type, source_logical_id, target_resource_type, target_logical_id," +
		" source_path, source_ordinal, logical_edge_key)" +
		" SELECT DISTINCT project, dataset_generation, auth_resource_path, label," +
		" source_resource_type, source_logical_id, target_resource_type, target_logical_id," +
		" source_path, source_ordinal, logical_edge_key FROM " + w.stagingEdgeFQN() + " s" +
		" WHERE EXISTS (SELECT 1 FROM " + w.resourceTableFQN + " src" +
		" WHERE src.project = s.project AND src.dataset_generation = s.dataset_generation" +
		" AND src.resource_type = s.source_resource_type AND src.logical_id = s.source_logical_id)" +
		" ON CONFLICT ON CONSTRAINT " + edgeTableName + "_identity_ux DO NOTHING" +
		" RETURNING edge_seq"
	rows, err := tx.Query(ctx, sql)
	if err != nil {
		return 0, &BatchWriteError{Op: "publish", BatchID: batchID, Err: err}
	}
	defer rows.Close()
	var n int64
	for rows.Next() {
		var seq int64
		if err := rows.Scan(&seq); err != nil {
			return 0, &BatchWriteError{Op: "publish", BatchID: batchID, Err: err}
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return 0, &BatchWriteError{Op: "publish", BatchID: batchID, Err: err}
	}
	return n, nil
}

func validateBatch(batch Batch) error {
	if strings.TrimSpace(batch.ID) == "" {
		return fmt.Errorf("%w: batch id is blank", ErrInvalidBatch)
	}
	if len(batch.ID) > MaxBatchIDLength {
		return fmt.Errorf("%w: batch id is %d bytes, want at most %d",
			ErrInvalidBatch, len(batch.ID), MaxBatchIDLength)
	}
	if strings.TrimSpace(batch.Project) == "" {
		return fmt.Errorf("%w: project is blank", ErrInvalidBatch)
	}
	if len(batch.Resources) == 0 && len(batch.Edges) == 0 {
		return fmt.Errorf("%w: batch %q carries no rows", ErrInvalidBatch, batch.ID)
	}
	if len(batch.Resources) > MaxBatchResources {
		return fmt.Errorf("%w: batch %q offers %d resources, want at most %d",
			ErrInvalidBatch, batch.ID, len(batch.Resources), MaxBatchResources)
	}
	if len(batch.Edges) > MaxBatchEdges {
		return fmt.Errorf("%w: batch %q offers %d edges, want at most %d",
			ErrInvalidBatch, batch.ID, len(batch.Edges), MaxBatchEdges)
	}
	for i, rec := range batch.Resources {
		if rec.Project != batch.Project || rec.DatasetGeneration != batch.DatasetGeneration {
			return fmt.Errorf("%w: batch %q resource %d is scoped to %s/%s, want the batch scope %s/%s",
				ErrInvalidBatch, batch.ID, i, rec.Project, rec.DatasetGeneration,
				batch.Project, batch.DatasetGeneration)
		}
		if err := validateResourceForInsert(ResourceRow{Resource: rec}); err != nil {
			return fmt.Errorf("%w: batch %q resource %d: %w", ErrInvalidBatch, batch.ID, i, err)
		}
	}
	for i, rec := range batch.Edges {
		if rec.Project != batch.Project || rec.DatasetGeneration != batch.DatasetGeneration {
			return fmt.Errorf("%w: batch %q edge %d is scoped to %s/%s, want the batch scope %s/%s",
				ErrInvalidBatch, batch.ID, i, rec.Project, rec.DatasetGeneration,
				batch.Project, batch.DatasetGeneration)
		}
		if err := validateEdgeForInsert(rec); err != nil {
			return fmt.Errorf("%w: batch %q edge %d: %w", ErrInvalidBatch, batch.ID, i, err)
		}
	}
	return nil
}

func batchDigest(batch Batch) string {
	h := sha256.New()
	put := func(s string) {
		h.Write([]byte(strconv.Itoa(len(s))))
		h.Write([]byte{':'})
		h.Write([]byte(s))
	}
	putTagged := func(present bool, s string) {
		if present {
			h.Write([]byte{'V'})
			put(s)
			return
		}
		h.Write([]byte{'N'})
	}
	put("loom.batch.v1\n")
	put(batch.ID)
	put(batch.Project)
	put(batch.DatasetGeneration)
	put("R" + strconv.Itoa(len(batch.Resources)))
	for _, rec := range batch.Resources {
		put(rec.Project)
		put(rec.DatasetGeneration)
		put(rec.ResourceType)
		put(rec.LogicalID)
		put(rec.AuthResourcePath)
		putTagged(rec.Payload != nil, string(rec.Payload))
	}
	put("E" + strconv.Itoa(len(batch.Edges)))
	for _, rec := range batch.Edges {
		put(rec.Project)
		put(rec.DatasetGeneration)
		put(rec.AuthResourcePath)
		put(rec.Label)
		put(rec.SourceResourceType)
		put(rec.SourceLogicalID)
		put(rec.TargetResourceType)
		put(rec.TargetLogicalID)
		put(rec.SourcePath)
		put("o" + strconv.FormatInt(rec.SourceOrdinal, 10))
		putTagged(rec.LogicalEdgeKey != nil, derefString(rec.LogicalEdgeKey))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
