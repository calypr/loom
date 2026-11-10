// Scoped relationship-edge persistence for the PostgreSQL 18 source store
// (pg-store-05).
//
// EdgeRepository stores one relationship occurrence per row in the
// fhir_edge_occurrence table created by migration 000004_edge_occurrence:
// endpoints are typed identities (resource type + logical id) inside the
// same project/dataset_generation scope as fhir_resource, and the occurrence
// identity includes the source path and ordinal, so repeated identical
// references from one source document survive instead of collapsing into one
// endpoint-keyed edge the way the Arango model does. This mirrors the Arango
// edge model (internal/ingest/documents.go EdgeDocument) with the one
// deliberate deviation the architecture contract names: explicit occurrence
// identity and ordinal, not endpoint deduplication.
//
// Guarantees:
//   - Insert is insert-only. The natural occurrence identity
//     (project, dataset_generation, source_resource_type, source_logical_id,
//     label, target_resource_type, target_logical_id, source_path,
//     source_ordinal) is a UNIQUE constraint, so the fully identical
//     occurrence returns a typed *EdgeConflictError and is never stored
//     twice — while two identical target/label references from different
//     source path/ordinal occurrences are two rows.
//   - The source must exist in the same (project, dataset_generation) scope
//     at insert time; a missing source returns a typed
//     *EdgeSourceMissingError and no row is written. The source precondition
//     is checked atomically with the insert (one INSERT ... SELECT ... WHERE
//     EXISTS statement), so a concurrent generation change cannot let an
//     edge land without its source.
//   - The target may be unresolved: its resource type and logical id are
//     stored regardless, and traversal reads report target resolution as a
//     read-time fact (a target resolves only when a fhir_resource row with
//     that identity exists in the edge's own project/dataset_generation),
//     never a stored flag that can go stale.
//   - Reads bind scope. Restricted traversals always bind project, dataset
//     generation, the endpoint identity, and auth path; the unrestricted
//     mode is explicit (AuthResourcePathsUnrestricted must be set
//     deliberately). Source- and target-directed lookups cannot resolve
//     across project/generation. Traversals page on the immutable storage
//     identity (edge_seq), so paging is stable.
//   - logical_edge_key keeps nil (SQL NULL, no key supplied) distinct from
//     the empty string (a present key whose value is empty).
//
// There is no cross-database atomicity and no backend fallback: every
// failure is a named error the caller must surface. Atomic CAS/history,
// fenced leases, bulk-write batches, and the generation manifest are later
// tickets; this file provides no update or delete path.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// edgeTableName is the table created by migration
	// 000004_edge_occurrence.sql.
	edgeTableName = "fhir_edge_occurrence"
	// MaxEdgeTraversalLimit bounds one traversal page so a caller cannot ask
	// for an unbounded scan through the pool.
	MaxEdgeTraversalLimit = 1000
)

// edgeColumns is the fixed column list for every edge read. The order
// matches scanEdgeRow exactly.
const edgeColumns = "edge_seq, project, dataset_generation, auth_resource_path, label, source_resource_type, source_logical_id, target_resource_type, target_logical_id, source_path, source_ordinal, logical_edge_key"

// Sentinel errors for classifying edge persistence failures without string
// matching.
var (
	// ErrEdgeConflict reports an insert whose full occurrence identity
	// already holds a row. The stored row is untouched.
	ErrEdgeConflict = errors.New("postgres: edge conflict")
	// ErrEdgeSourceMissing reports an insert whose source resource does not
	// exist in the edge's own (project, dataset_generation) scope. No row is
	// written.
	ErrEdgeSourceMissing = errors.New("postgres: edge source missing")
	// ErrInvalidEdge reports a malformed edge record: blank identity or
	// label fields, a caller-supplied storage identity, or a negative
	// source ordinal.
	ErrInvalidEdge = errors.New("postgres: invalid edge")
	// ErrInvalidEdgeScope reports a scope or traversal argument that cannot
	// be honored: a blank project, a missing unrestricted flag, an
	// unrestricted scope carrying paths, a blank endpoint identity, a
	// negative cursor, or a page size outside [1, MaxEdgeTraversalLimit].
	ErrInvalidEdgeScope = errors.New("postgres: invalid edge scope")
)

// EdgeConflictError reports an insert that was refused because the full
// occurrence identity already holds a row. The insert is a no-op: the
// earlier occurrence — with its recorded path/ordinal — stays stored. It
// matches errors.Is(err, ErrEdgeConflict).
type EdgeConflictError struct {
	Project            string
	DatasetGeneration  string
	AuthResourcePath   string
	Label              string
	SourceResourceType string
	SourceLogicalID    string
	TargetResourceType string
	TargetLogicalID    string
	SourcePath         string
	SourceOrdinal      int64
}

func (e *EdgeConflictError) Error() string {
	return fmt.Sprintf("postgres: edge occurrence %s/%s/%s(%s) -%s-> %s/%s at path %q ordinal %d (auth path %q) already exists; "+
		"insert never replaces the stored occurrence",
		e.Project, e.DatasetGeneration, e.SourceResourceType, e.SourceLogicalID, e.Label,
		e.TargetResourceType, e.TargetLogicalID, e.SourcePath, e.SourceOrdinal, e.AuthResourcePath)
}

func (e *EdgeConflictError) Unwrap() error { return ErrEdgeConflict }

// EdgeSourceMissingError reports an insert that was refused because the
// source resource is absent from the edge's own (project, dataset_generation)
// scope. No row was written. It matches errors.Is(err, ErrEdgeSourceMissing).
type EdgeSourceMissingError struct {
	Project            string
	DatasetGeneration  string
	SourceResourceType string
	SourceLogicalID    string
}

func (e *EdgeSourceMissingError) Error() string {
	return fmt.Sprintf("postgres: edge source %s/%s/%s/%s does not exist in this project/dataset_generation scope; "+
		"the source must be stored in the same scope before any of its edges",
		e.Project, e.DatasetGeneration, e.SourceResourceType, e.SourceLogicalID)
}

func (e *EdgeSourceMissingError) Unwrap() error { return ErrEdgeSourceMissing }

// EdgeScope binds one PostgreSQL edge traversal to exactly one storage scope
// and its authorization. It follows the explicit-mode convention of
// source.ResourceScope: there is no implicit-unrestricted mode, so
// AuthResourcePathsUnrestricted must be set deliberately.
type EdgeScope struct {
	// Project is required and matched exactly.
	Project string
	// DatasetGeneration is matched exactly. The empty string is the legacy
	// dataset namespace, never "any generation".
	DatasetGeneration string
	// AuthResourcePathsUnrestricted must be non-nil: true selects the
	// explicit unrestricted mode (then AuthResourcePaths must be empty),
	// false selects restricted mode. A nil value is an error, not the
	// legacy "empty means unrestricted" convention.
	AuthResourcePathsUnrestricted *bool
	// AuthResourcePaths are the authorized auth_resource_path values in
	// restricted mode. An empty list in restricted mode is a legitimate
	// authorization state that reads nothing.
	AuthResourcePaths []string
}

// EdgeOccurrenceRow is one persisted relationship occurrence. The scope and
// endpoint columns carry the occurrence identity; LogicalEdgeKey is a
// diagnostic only; TargetResolved is computed by traversal reads and is the
// zero value on insert.
type EdgeOccurrenceRow struct {
	// EdgeSeq is the server-assigned, immutable storage identity used for
	// traversal ordering. It must be zero on insert; the database assigns
	// it.
	EdgeSeq int64
	// Project is the owning project. Required, non-blank.
	Project string
	// DatasetGeneration is the owning immutable dataset generation. The
	// empty string is the legacy dataset namespace.
	DatasetGeneration string
	// AuthResourcePath is the typed, non-null authorization scope value.
	// The empty string is the legacy (unscoped) namespace.
	AuthResourcePath string
	// Label is the edge label. Required, non-blank.
	Label string
	// SourceResourceType is the canonical FHIR type of the owning source.
	// Required, non-blank.
	SourceResourceType string
	// SourceLogicalID is the logical FHIR id of the owning source.
	// Required, non-blank. The source must exist in the same
	// (project, dataset_generation) scope at insert time.
	SourceLogicalID string
	// TargetResourceType is the canonical FHIR type of the target.
	// Required, non-blank. It is preserved even when the target does not
	// exist (an unresolved reference).
	TargetResourceType string
	// TargetLogicalID is the logical FHIR id of the target. Required,
	// non-blank. It is preserved even when the target does not exist.
	TargetLogicalID string
	// SourcePath locates the reference inside the owning source document
	// (the repeated-owner correlation). The empty string is the unqualified
	// (legacy) namespace, never "any path".
	SourcePath string
	// SourceOrdinal distinguishes identical references at one source path.
	// Required, non-negative.
	SourceOrdinal int64
	// LogicalEdgeKey preserves the caller's logical edge key (the
	// Arango-style endpoint-derived key) as a diagnostic. A nil value is
	// SQL NULL (no key supplied) and is distinct from the empty string.
	// Rows sharing a logical edge key are exactly the multiplicity an
	// endpoint-keyed store would collapse.
	LogicalEdgeKey *string
	// TargetResolved reports, on traversal reads, whether a fhir_resource
	// row with the edge's target identity exists in the edge's own
	// (project, dataset_generation) scope. It is computed at read time and
	// is the zero value on insert.
	TargetResolved bool
}

// EdgeRepository stores and reads relationship occurrence rows in one schema
// of the PostgreSQL source store. Create one with NewEdgeRepository. It
// holds no state of its own beyond the qualified table names; the caller
// owns the connection source and the schema lifecycle (both tables must be
// created by MigrateSchema before use).
type EdgeRepository struct {
	src              ConnSource
	tableFQN         string
	resourceTableFQN string
}

// NewEdgeRepository validates the connection source and schema name and
// binds the repository to <schema>.fhir_edge_occurrence, reading
// <schema>.fhir_resource for source preconditions and target resolution. The
// connection source must be live only when a method runs, not here.
func NewEdgeRepository(src ConnSource, schema string) (*EdgeRepository, error) {
	if src == nil {
		return nil, errors.New("postgres: nil edge connection source")
	}
	if !schemaNamePattern.MatchString(schema) {
		return nil, fmt.Errorf("%w: %q must match [a-z_][a-z0-9_]{0,62}", ErrInvalidSchemaName, schema)
	}
	return &EdgeRepository{
		src:              src,
		tableFQN:         pgx.Identifier{schema, edgeTableName}.Sanitize(),
		resourceTableFQN: pgx.Identifier{schema, resourceTableName}.Sanitize(),
	}, nil
}

// InsertEdge stores one relationship occurrence and returns the
// server-assigned storage identity. The record's EdgeSeq must be zero: the
// database assigns it. The source resource must exist in the same
// (project, dataset_generation) scope — checked atomically with the insert —
// or a *EdgeSourceMissingError matching ErrEdgeSourceMissing is returned and
// nothing is written. The target may be unresolved: its identity is stored
// either way. A fully identical occurrence (all nine identity columns equal)
// returns a *EdgeConflictError matching ErrEdgeConflict and is never stored
// twice; occurrences that differ in source path or ordinal are separate rows.
func (r *EdgeRepository) InsertEdge(ctx context.Context, rec EdgeOccurrenceRow) (int64, error) {
	if err := validateEdgeForInsert(rec); err != nil {
		return 0, err
	}
	conn, err := r.acquire(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()

	// One statement carries the whole write decision: the WHERE EXISTS
	// makes the source precondition atomic with the insert, and ON CONFLICT
	// against the named occurrence-identity constraint makes duplicates a
	// no-op instead of an error path with a partial write. Parameters are
	// ordered: $1 project, $2 generation, $3 auth path, $4 label,
	// $5 source type, $6 source id, $7 target type, $8 target id,
	// $9 source path, $10 source ordinal, $11 logical key. The SELECT list
	// casts every parameter so PostgreSQL never has to infer a type it
	// cannot see.
	sql := "INSERT INTO " + r.tableFQN +
		" (project, dataset_generation, auth_resource_path, label," +
		" source_resource_type, source_logical_id, target_resource_type, target_logical_id," +
		" source_path, source_ordinal, logical_edge_key) " +
		"SELECT $1::text, $2::text, $3::text, $4::text, $5::text, $6::text, $7::text, $8::text, $9::text, $10::bigint, $11::text " +
		"WHERE EXISTS (SELECT 1 FROM " + r.resourceTableFQN + " s" +
		" WHERE s.project = $1 AND s.dataset_generation = $2" +
		" AND s.resource_type = $5 AND s.logical_id = $6) " +
		"ON CONFLICT ON CONSTRAINT " + edgeTableName + "_identity_ux DO NOTHING " +
		"RETURNING edge_seq"
	var seq int64
	err = conn.QueryRow(ctx, sql,
		rec.Project, rec.DatasetGeneration, rec.AuthResourcePath, rec.Label,
		rec.SourceResourceType, rec.SourceLogicalID, rec.TargetResourceType, rec.TargetLogicalID,
		rec.SourcePath, rec.SourceOrdinal, rec.LogicalEdgeKey).Scan(&seq)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, r.classifyRefusedInsert(ctx, conn, rec)
	}
	if err != nil {
		return 0, fmt.Errorf("postgres: insert edge %s/%s/%s/%s -%s-> %s/%s at path %q ordinal %d: %w",
			rec.Project, rec.DatasetGeneration, rec.SourceResourceType, rec.SourceLogicalID,
			rec.Label, rec.TargetResourceType, rec.TargetLogicalID, rec.SourcePath, rec.SourceOrdinal, err)
	}
	return seq, nil
}

// classifyRefusedInsert decides which precondition an ON CONFLICT DO NOTHING
// / WHERE EXISTS insert skipped, reporting the source precondition first
// (matching the order the insert itself enforces). Between the insert
// statement and these probes a concurrent writer may change the picture;
// the classification is diagnostic — no data was written either way.
func (r *EdgeRepository) classifyRefusedInsert(ctx context.Context, conn *pgxpool.Conn, rec EdgeOccurrenceRow) error {
	var sourceExists bool
	if err := conn.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM "+r.resourceTableFQN+
			" WHERE project = $1 AND dataset_generation = $2 AND resource_type = $3 AND logical_id = $4)",
		rec.Project, rec.DatasetGeneration, rec.SourceResourceType, rec.SourceLogicalID,
	).Scan(&sourceExists); err != nil {
		return fmt.Errorf("postgres: classify refused edge insert: %w", err)
	}
	if !sourceExists {
		return &EdgeSourceMissingError{
			Project:            rec.Project,
			DatasetGeneration:  rec.DatasetGeneration,
			SourceResourceType: rec.SourceResourceType,
			SourceLogicalID:    rec.SourceLogicalID,
		}
	}
	return &EdgeConflictError{
		Project:            rec.Project,
		DatasetGeneration:  rec.DatasetGeneration,
		AuthResourcePath:   rec.AuthResourcePath,
		Label:              rec.Label,
		SourceResourceType: rec.SourceResourceType,
		SourceLogicalID:    rec.SourceLogicalID,
		TargetResourceType: rec.TargetResourceType,
		TargetLogicalID:    rec.TargetLogicalID,
		SourcePath:         rec.SourcePath,
		SourceOrdinal:      rec.SourceOrdinal,
	}
}

// ReadEdgesFromSource performs a source-directed traversal: edge
// occurrences owned by (sourceResourceType, sourceLogicalID) inside one
// scope, ordered by the immutable storage identity (edge_seq ascending),
// rows with EdgeSeq strictly greater than afterSeq, at most limit rows. It
// returns the page and whether more rows remain in scope after it. Each row
// reports TargetResolved against the edge's own project/dataset_generation,
// so the traversal cannot resolve across project/generation. A restricted
// scope with no authorized paths reads nothing (empty page, no error); the
// unrestricted mode must be requested explicitly.
func (r *EdgeRepository) ReadEdgesFromSource(ctx context.Context, scope EdgeScope,
	sourceResourceType, sourceLogicalID string, afterSeq int64, limit int) ([]EdgeOccurrenceRow, bool, error) {
	where, args, binding, err := r.bindTraversal("source_resource_type", "source_logical_id",
		scope, sourceResourceType, sourceLogicalID, afterSeq, limit)
	if err != nil {
		return nil, false, err
	}
	if binding.restricted && len(binding.paths) == 0 {
		// Restricted with no authorized path reads nothing; never widen.
		return nil, false, nil
	}
	return r.queryEdges(ctx, where, args, limit)
}

// ReadEdgesToTarget performs a target-directed traversal: edge
// occurrences whose target is (targetResourceType, targetLogicalID) inside
// one scope. Ordering, paging, resolution, and authorization rules are
// exactly as in ReadEdgesFromSource.
func (r *EdgeRepository) ReadEdgesToTarget(ctx context.Context, scope EdgeScope,
	targetResourceType, targetLogicalID string, afterSeq int64, limit int) ([]EdgeOccurrenceRow, bool, error) {
	where, args, binding, err := r.bindTraversal("target_resource_type", "target_logical_id",
		scope, targetResourceType, targetLogicalID, afterSeq, limit)
	if err != nil {
		return nil, false, err
	}
	if binding.restricted && len(binding.paths) == 0 {
		// Restricted with no authorized path reads nothing; never widen.
		return nil, false, nil
	}
	return r.queryEdges(ctx, where, args, limit)
}

// bindTraversal validates the shared traversal arguments and builds the
// WHERE clause. Ordered parameters: $1 project, $2 generation, $3 endpoint
// type, $4 endpoint id, $5 cursor, $6 auth paths (restricted only),
// $last page size (+1 to detect more).
func (r *EdgeRepository) bindTraversal(endpointTypeColumn, endpointIDColumn string,
	scope EdgeScope, endpointType, endpointID string, afterSeq int64, limit int) (string, []any, edgeScopeBinding, error) {
	binding, err := normalizeEdgeScope(scope)
	if err != nil {
		return "", nil, binding, err
	}
	if strings.TrimSpace(endpointType) == "" {
		return "", nil, binding, fmt.Errorf("%w: traversal endpoint resource type is blank", ErrInvalidEdge)
	}
	if strings.TrimSpace(endpointID) == "" {
		return "", nil, binding, fmt.Errorf("%w: traversal endpoint logical id is blank", ErrInvalidEdge)
	}
	if afterSeq < 0 {
		return "", nil, binding, fmt.Errorf("%w: traversal cursor is %d, want >= 0", ErrInvalidEdgeScope, afterSeq)
	}
	if limit < 1 || limit > MaxEdgeTraversalLimit {
		return "", nil, binding, fmt.Errorf("%w: traversal limit is %d, want 1..%d",
			ErrInvalidEdgeScope, limit, MaxEdgeTraversalLimit)
	}

	where := " WHERE project = $1 AND dataset_generation = $2" +
		" AND " + endpointTypeColumn + " = $3 AND " + endpointIDColumn + " = $4 AND edge_seq > $5"
	args := []any{scope.Project, scope.DatasetGeneration, endpointType, endpointID, afterSeq}
	if binding.restricted {
		where += " AND auth_resource_path = ANY($6::text[])"
		args = append(args, binding.paths)
	}
	where += fmt.Sprintf(" ORDER BY edge_seq LIMIT $%d", len(args)+1)
	args = append(args, limit+1)
	return where, args, binding, nil
}

// queryEdges runs one traversal (LIMIT was already set to limit+1 by
// bindTraversal) and scans its rows, reporting whether more rows remain. The
// target_resolved expression resolves only within the edge's own
// project/dataset_generation, so a decoy target stored under another scope
// cannot mark an edge resolved.
func (r *EdgeRepository) queryEdges(ctx context.Context, where string, args []any, limit int) ([]EdgeOccurrenceRow, bool, error) {
	sql := "SELECT " + edgeColumns + ", EXISTS (SELECT 1 FROM " + r.resourceTableFQN + " t" +
		" WHERE t.project = " + r.tableFQN + ".project" +
		" AND t.dataset_generation = " + r.tableFQN + ".dataset_generation" +
		" AND t.resource_type = " + r.tableFQN + ".target_resource_type" +
		" AND t.logical_id = " + r.tableFQN + ".target_logical_id) AS target_resolved" +
		" FROM " + r.tableFQN + where
	conn, err := r.acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, false, fmt.Errorf("postgres: traverse edges: %w", err)
	}
	defer rows.Close()

	var out []EdgeOccurrenceRow
	for rows.Next() {
		rec, err := scanEdgeRow(rows)
		if err != nil {
			return nil, false, fmt.Errorf("postgres: traverse edges: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("postgres: traverse edges: %w", err)
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

// acquire takes one pooled connection for a single-statement operation.
func (r *EdgeRepository) acquire(ctx context.Context) (*pgxpool.Conn, error) {
	conn, err := r.src.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: acquire edge connection: %w", err)
	}
	return conn, nil
}

// scanEdgeRow scans one row of edgeColumns plus the target_resolved
// expression. A SQL NULL logical_edge_key scans as a nil *string, distinct
// from a pointer to the empty string.
func scanEdgeRow(row pgx.Row) (EdgeOccurrenceRow, error) {
	var rec EdgeOccurrenceRow
	var logicalEdgeKey *string
	if err := row.Scan(
		&rec.EdgeSeq, &rec.Project, &rec.DatasetGeneration, &rec.AuthResourcePath,
		&rec.Label, &rec.SourceResourceType, &rec.SourceLogicalID,
		&rec.TargetResourceType, &rec.TargetLogicalID,
		&rec.SourcePath, &rec.SourceOrdinal, &logicalEdgeKey, &rec.TargetResolved,
	); err != nil {
		return EdgeOccurrenceRow{}, err
	}
	rec.LogicalEdgeKey = logicalEdgeKey
	return rec, nil
}

// validateEdgeForInsert enforces the column contract before a round trip:
// the storage identity is server-assigned, the identity and label fields are
// present, and the ordinal is non-negative.
func validateEdgeForInsert(rec EdgeOccurrenceRow) error {
	if rec.EdgeSeq != 0 {
		return fmt.Errorf("%w: EdgeSeq is %d; the storage identity is server-assigned, insert requires 0",
			ErrInvalidEdge, rec.EdgeSeq)
	}
	for _, f := range []struct {
		name  string
		value string
	}{
		{"project", rec.Project},
		{"label", rec.Label},
		{"source resource type", rec.SourceResourceType},
		{"source logical id", rec.SourceLogicalID},
		{"target resource type", rec.TargetResourceType},
		{"target logical id", rec.TargetLogicalID},
	} {
		if strings.TrimSpace(f.value) == "" {
			return fmt.Errorf("%w: %s is blank", ErrInvalidEdge, f.name)
		}
	}
	if rec.SourceOrdinal < 0 {
		return fmt.Errorf("%w: source ordinal is %d, want >= 0", ErrInvalidEdge, rec.SourceOrdinal)
	}
	return nil
}

// edgeScopeBinding is the validated form of EdgeScope for one query.
type edgeScopeBinding struct {
	// restricted is true unless the explicit unrestricted mode was chosen.
	restricted bool
	// paths are the bound auth paths in restricted mode (nil in
	// unrestricted mode). An empty non-nil list is a legitimate
	// authorization state that reads nothing.
	paths []string
}

// normalizeEdgeScope validates a scope and projects it into query bindings.
// Unrestricted mode must be set explicitly and carries no paths; restricted
// mode binds exactly the authorized paths, including the empty string when
// the legacy namespace is authorized. Values are bound verbatim: the caller
// stores and reads with the same identity discipline.
func normalizeEdgeScope(scope EdgeScope) (edgeScopeBinding, error) {
	if strings.TrimSpace(scope.Project) == "" {
		return edgeScopeBinding{}, fmt.Errorf("%w: project is blank", ErrInvalidEdgeScope)
	}
	if scope.AuthResourcePathsUnrestricted == nil {
		return edgeScopeBinding{}, fmt.Errorf(
			"%w: AuthResourcePathsUnrestricted must be set explicitly; there is no implicit unrestricted mode",
			ErrInvalidEdgeScope)
	}
	if *scope.AuthResourcePathsUnrestricted {
		if len(scope.AuthResourcePaths) > 0 {
			return edgeScopeBinding{}, fmt.Errorf(
				"%w: unrestricted mode must not carry auth resource paths (%d given)",
				ErrInvalidEdgeScope, len(scope.AuthResourcePaths))
		}
		return edgeScopeBinding{restricted: false}, nil
	}
	return edgeScopeBinding{restricted: true, paths: scope.AuthResourcePaths}, nil
}
