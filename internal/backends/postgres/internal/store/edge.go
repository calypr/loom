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
	edgeTableName         = "fhir_edge_occurrence"
	MaxEdgeTraversalLimit = 1000
)

const edgeColumns = "edge_seq, project, dataset_generation, auth_resource_path, label, source_resource_type, source_logical_id, target_resource_type, target_logical_id, source_path, source_ordinal, logical_edge_key"

var (
	ErrEdgeConflict      = errors.New("postgres: edge conflict")
	ErrEdgeSourceMissing = errors.New("postgres: edge source missing")
	ErrInvalidEdge       = errors.New("postgres: invalid edge")
	ErrInvalidEdgeScope  = errors.New("postgres: invalid edge scope")
)

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

type EdgeScope struct {
	Project                       string
	DatasetGeneration             string
	AuthResourcePathsUnrestricted *bool
	AuthResourcePaths             []string
}

type EdgeOccurrenceRow struct {
	EdgeSeq            int64
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
	LogicalEdgeKey     *string
	TargetResolved     bool
}

type EdgeRepository struct {
	src              ConnSource
	tableFQN         string
	resourceTableFQN string
}

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

func (r *EdgeRepository) InsertEdge(ctx context.Context, rec EdgeOccurrenceRow) (int64, error) {
	if err := validateEdgeForInsert(rec); err != nil {
		return 0, err
	}
	conn, err := r.acquire(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()

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

func (r *EdgeRepository) ReadEdgesFromSource(ctx context.Context, scope EdgeScope,
	sourceResourceType, sourceLogicalID string, afterSeq int64, limit int) ([]EdgeOccurrenceRow, bool, error) {
	where, args, binding, err := r.bindTraversal("source_resource_type", "source_logical_id",
		scope, sourceResourceType, sourceLogicalID, afterSeq, limit)
	if err != nil {
		return nil, false, err
	}
	if binding.restricted && len(binding.paths) == 0 {
		return nil, false, nil
	}
	return r.queryEdges(ctx, where, args, limit)
}

func (r *EdgeRepository) ReadEdgesToTarget(ctx context.Context, scope EdgeScope,
	targetResourceType, targetLogicalID string, afterSeq int64, limit int) ([]EdgeOccurrenceRow, bool, error) {
	where, args, binding, err := r.bindTraversal("target_resource_type", "target_logical_id",
		scope, targetResourceType, targetLogicalID, afterSeq, limit)
	if err != nil {
		return nil, false, err
	}
	if binding.restricted && len(binding.paths) == 0 {
		return nil, false, nil
	}
	return r.queryEdges(ctx, where, args, limit)
}

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

func (r *EdgeRepository) acquire(ctx context.Context) (*pgxpool.Conn, error) {
	conn, err := r.src.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: acquire edge connection: %w", err)
	}
	return conn, nil
}

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

type edgeScopeBinding struct {
	restricted bool
	paths      []string
}

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
