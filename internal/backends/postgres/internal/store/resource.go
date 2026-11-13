package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/source"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	resourceTableName      = "fhir_resource"
	MaxResourceKeysetLimit = 1000
)

const resourceColumns = "resource_seq, project, dataset_generation, resource_type, logical_id, auth_resource_path, payload"

type ResourceRow struct {
	source.Resource
	ResourceSeq int64
}

var (
	ErrResourceConflict     = errors.New("postgres: resource conflict")
	ErrResourceNotFound     = errors.New("postgres: resource not found")
	ErrInvalidResource      = errors.New("postgres: invalid resource")
	ErrInvalidResourceScope = errors.New("postgres: invalid resource scope")
)

type ResourceConflictError struct {
	Project           string
	DatasetGeneration string
	ResourceType      string
	LogicalID         string
	AuthResourcePath  string
}

func (e *ResourceConflictError) Error() string {
	return fmt.Sprintf("postgres: resource %s/%s/%s/%s (auth path %q) already exists; "+
		"insert never replaces the stored payload",
		e.Project, e.DatasetGeneration, e.ResourceType, e.LogicalID, e.AuthResourcePath)
}

func (e *ResourceConflictError) Unwrap() error { return ErrResourceConflict }

type ResourceNotFoundError struct {
	Project           string
	DatasetGeneration string
	ResourceType      string
	LogicalID         string
	Restricted        bool
}

func (e *ResourceNotFoundError) Error() string {
	return fmt.Sprintf("postgres: no resource %s/%s/%s/%s in scope (restricted=%t)",
		e.Project, e.DatasetGeneration, e.ResourceType, e.LogicalID, e.Restricted)
}

func (e *ResourceNotFoundError) Unwrap() error { return ErrResourceNotFound }

type ResourceRepository struct {
	src      ConnSource
	tableFQN string
}

func NewResourceRepository(src ConnSource, schema string) (*ResourceRepository, error) {
	if src == nil {
		return nil, errors.New("postgres: nil resource connection source")
	}
	if !schemaNamePattern.MatchString(schema) {
		return nil, fmt.Errorf("%w: %q must match [a-z_][a-z0-9_]{0,62}", ErrInvalidSchemaName, schema)
	}
	return &ResourceRepository{
		src:      src,
		tableFQN: pgx.Identifier{schema, resourceTableName}.Sanitize(),
	}, nil
}

func (r *ResourceRepository) InsertResource(ctx context.Context, rec source.Resource) (int64, error) {
	if err := validateResourceForInsert(ResourceRow{Resource: rec}); err != nil {
		return 0, err
	}
	var payloadValue any
	if rec.Payload != nil {
		payloadValue = []byte(rec.Payload)
	}
	conn, err := r.acquire(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()

	sql := "INSERT INTO " + r.tableFQN + " (project, dataset_generation, resource_type, logical_id, auth_resource_path, payload) " +
		"VALUES ($1, $2, $3, $4, $5, $6::jsonb) " +
		"ON CONFLICT (project, dataset_generation, resource_type, logical_id) DO NOTHING " +
		"RETURNING resource_seq"
	var seq int64
	err = conn.QueryRow(ctx, sql,
		rec.Project, rec.DatasetGeneration, rec.ResourceType, rec.LogicalID, rec.AuthResourcePath,
		payloadValue).Scan(&seq)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, &ResourceConflictError{
			Project:           rec.Project,
			DatasetGeneration: rec.DatasetGeneration,
			ResourceType:      rec.ResourceType,
			LogicalID:         rec.LogicalID,
			AuthResourcePath:  rec.AuthResourcePath,
		}
	}
	if err != nil {
		return 0, fmt.Errorf("postgres: insert resource %s/%s/%s/%s: %w",
			rec.Project, rec.DatasetGeneration, rec.ResourceType, rec.LogicalID, err)
	}
	return seq, nil
}

func (r *ResourceRepository) GetResource(ctx context.Context, scope source.ResourceScope, resourceType, logicalID string) (ResourceRow, error) {
	binding, err := normalizeResourceScope(scope)
	if err != nil {
		return ResourceRow{}, err
	}
	if strings.TrimSpace(resourceType) == "" {
		return ResourceRow{}, fmt.Errorf("%w: resource type is blank", ErrInvalidResource)
	}
	if strings.TrimSpace(logicalID) == "" {
		return ResourceRow{}, fmt.Errorf("%w: logical id is blank", ErrInvalidResource)
	}
	notFound := &ResourceNotFoundError{
		Project:           scope.Project,
		DatasetGeneration: scope.DatasetGeneration,
		ResourceType:      resourceType,
		LogicalID:         logicalID,
		Restricted:        binding.restricted,
	}
	if binding.restricted && len(binding.paths) == 0 {
		return ResourceRow{}, notFound
	}

	sql := "SELECT " + resourceColumns + " FROM " + r.tableFQN +
		" WHERE project = $1 AND dataset_generation = $2 AND resource_type = $3 AND logical_id = $4"
	args := []any{scope.Project, scope.DatasetGeneration, resourceType, logicalID}
	if binding.restricted {
		sql += " AND auth_resource_path = ANY($5::text[])"
		args = append(args, binding.paths)
	}

	conn, err := r.acquire(ctx)
	if err != nil {
		return ResourceRow{}, err
	}
	defer conn.Release()

	rec, err := scanResourceRow(conn.QueryRow(ctx, sql, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return ResourceRow{}, notFound
	}
	if err != nil {
		return ResourceRow{}, fmt.Errorf("postgres: read resource %s/%s/%s/%s: %w",
			scope.Project, scope.DatasetGeneration, resourceType, logicalID, err)
	}
	return rec, nil
}

func (r *ResourceRepository) ReadResources(ctx context.Context, scope source.ResourceScope, afterSeq int64, limit int) ([]ResourceRow, bool, error) {
	binding, err := normalizeResourceScope(scope)
	if err != nil {
		return nil, false, err
	}
	if afterSeq < 0 {
		return nil, false, fmt.Errorf("%w: keyset cursor is %d, want >= 0", ErrInvalidResourceScope, afterSeq)
	}
	if limit < 1 || limit > MaxResourceKeysetLimit {
		return nil, false, fmt.Errorf("%w: keyset limit is %d, want 1..%d",
			ErrInvalidResourceScope, limit, MaxResourceKeysetLimit)
	}
	if binding.restricted && len(binding.paths) == 0 {
		return nil, false, nil
	}

	sql := "SELECT " + resourceColumns + " FROM " + r.tableFQN +
		" WHERE project = $1 AND dataset_generation = $2 AND resource_seq > $3"
	args := []any{scope.Project, scope.DatasetGeneration, afterSeq}
	if binding.restricted {
		sql += " AND auth_resource_path = ANY($4::text[])"
		args = append(args, binding.paths)
	}
	sql += fmt.Sprintf(" ORDER BY resource_seq LIMIT $%d", len(args)+1)
	args = append(args, limit+1)

	conn, err := r.acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, false, fmt.Errorf("postgres: keyset read resources %s/%s: %w",
			scope.Project, scope.DatasetGeneration, err)
	}
	defer rows.Close()

	var out []ResourceRow
	for rows.Next() {
		rec, err := scanResourceRow(rows)
		if err != nil {
			return nil, false, fmt.Errorf("postgres: keyset read resources %s/%s: %w",
				scope.Project, scope.DatasetGeneration, err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("postgres: keyset read resources %s/%s: %w",
			scope.Project, scope.DatasetGeneration, err)
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

func (r *ResourceRepository) acquire(ctx context.Context) (*pgxpool.Conn, error) {
	conn, err := r.src.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: acquire resource connection: %w", err)
	}
	return conn, nil
}

func scanResourceRow(row pgx.Row) (ResourceRow, error) {
	var rec ResourceRow
	var payload []byte
	if err := row.Scan(
		&rec.ResourceSeq, &rec.Project, &rec.DatasetGeneration,
		&rec.ResourceType, &rec.LogicalID, &rec.AuthResourcePath, &payload,
	); err != nil {
		return ResourceRow{}, err
	}
	if payload != nil {
		rec.Payload = json.RawMessage(payload)
	}
	return rec, nil
}

func validateResourceForInsert(rec ResourceRow) error {
	if rec.ResourceSeq != 0 {
		return fmt.Errorf("%w: ResourceSeq is %d; the storage identity is server-assigned, insert requires 0",
			ErrInvalidResource, rec.ResourceSeq)
	}
	for _, f := range []struct {
		name  string
		value string
	}{
		{"project", rec.Project},
		{"resource type", rec.ResourceType},
		{"logical id", rec.LogicalID},
	} {
		if strings.TrimSpace(f.value) == "" {
			return fmt.Errorf("%w: %s is blank", ErrInvalidResource, f.name)
		}
	}
	if rec.Payload != nil && !json.Valid(rec.Payload) {
		return fmt.Errorf("%w: payload is not valid JSON", ErrInvalidResource)
	}
	return nil
}

type resourceScopeBinding struct {
	restricted bool
	paths      []string
}

func normalizeResourceScope(scope source.ResourceScope) (resourceScopeBinding, error) {
	if strings.TrimSpace(scope.Project) == "" {
		return resourceScopeBinding{}, fmt.Errorf("%w: project is blank", ErrInvalidResourceScope)
	}
	if scope.AuthResourcePathsUnrestricted == nil {
		return resourceScopeBinding{}, fmt.Errorf(
			"%w: AuthResourcePathsUnrestricted must be set explicitly; there is no implicit unrestricted mode",
			ErrInvalidResourceScope)
	}
	if *scope.AuthResourcePathsUnrestricted {
		if len(scope.AuthResourcePaths) > 0 {
			return resourceScopeBinding{}, fmt.Errorf(
				"%w: unrestricted mode must not carry auth resource paths (%d given)",
				ErrInvalidResourceScope, len(scope.AuthResourcePaths))
		}
		return resourceScopeBinding{restricted: false}, nil
	}
	return resourceScopeBinding{restricted: true, paths: scope.AuthResourcePaths}, nil
}
