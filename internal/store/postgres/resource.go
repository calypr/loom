// Scoped FHIR resource persistence for the PostgreSQL 18 source store
// (pg-store-04).
//
// ResourceRepository stores one FHIR resource per row in the fhir_resource
// table created by migration 000002_fhir_resource: the storage identity
// fields (project, dataset generation, resource type, logical FHIR id, auth
// path) are typed columns, and the original resource object is stored as
// JSONB. PostgreSQL canonicalizes JSONB (object key order, duplicate keys
// collapse to the last value, numeric literals normalize), so no byte-exact
// fidelity is claimed; the tested guarantee is the semantic one below. This
// mirrors the Arango vertex model without emulating
// collection-per-resource, so the same logical identity reads identically
// from either backend.
//
// Guarantees:
//   - Insert is insert-only. The natural identity
//     (project, dataset_generation, resource_type, logical_id) is a UNIQUE
//     constraint, so a duplicate returns a typed *ResourceConflictError and
//     the earlier payload is never replaced — different or identical.
//   - Reads bind scope. Restricted reads always bind project, dataset
//     generation, and auth path; the unrestricted mode is explicit
//     (AuthResourcePathsUnrestricted must be set deliberately). A read never
//     returns another project's, generation's, or unauthorized path's rows.
//   - Keyset reads page on the immutable storage identity (resource_seq), so
//     paging is stable: concurrent inserts cannot shift a page boundary, and
//     every row present at the start of a walk is visited exactly once.
//   - Payload fidelity: nil payload is SQL NULL, the bytes "null" are an
//     explicit JSON null document, and inside a document a missing key, an
//     explicit null, an empty array, and an empty object each survive the
//     round trip as themselves.
//
// There is no cross-database atomicity and no backend fallback: every
// failure is a named error the caller must surface. Atomic CAS/history and
// fenced leases are later tickets; this file provides no update path.
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
	// resourceTableName is the table created by migration
	// 000002_fhir_resource.sql.
	resourceTableName = "fhir_resource"
	// MaxResourceKeysetLimit bounds one keyset page so a caller cannot ask
	// for an unbounded scan through the pool.
	MaxResourceKeysetLimit = 1000
)

// resourceColumns is the fixed column list for every resource read. The
// order matches scanResourceRow exactly.
const resourceColumns = "resource_seq, project, dataset_generation, resource_type, logical_id, auth_resource_path, payload"

// ResourceRow is one physical FHIR resource row of the PostgreSQL source
// store: the logical source contract plus the store-owned physical layout.
// The logical identity fields (project, dataset generation, resource type,
// logical id, auth path, payload) are defined once in internal/source and
// are what writers offer; ResourceSeq is the PostgreSQL storage identity —
// server-assigned on insert and the immutable keyset order key on reads.
// Reads answer with this physical row; writes accept only the logical
// source.Resource, so a caller can never supply or collide with a storage
// sequence.
type ResourceRow struct {
	// source.Resource carries the logical identity and document; its
	// fields are promoted, so readers see the same logical names writers
	// offered.
	source.Resource
	// ResourceSeq is the server-assigned, immutable storage identity used
	// for keyset ordering. It must be zero on insert; the database assigns
	// it.
	ResourceSeq int64
}

// Sentinel errors for classifying resource persistence failures without
// string matching.
var (
	// ErrResourceConflict reports an insert whose natural identity
	// (project, dataset_generation, resource_type, logical_id) already
	// holds a row. The stored row is untouched.
	ErrResourceConflict = errors.New("postgres: resource conflict")
	// ErrResourceNotFound reports an exact read or scoped lookup with no
	// row inside the caller's scope.
	ErrResourceNotFound = errors.New("postgres: resource not found")
	// ErrInvalidResource reports a malformed resource record: blank
	// identity fields, a caller-supplied storage identity, or a payload
	// that is not valid JSON.
	ErrInvalidResource = errors.New("postgres: invalid resource")
	// ErrInvalidResourceScope reports a scope or keyset page argument that
	// cannot be honored: a blank project, a missing unrestricted flag, an
	// unrestricted scope carrying paths, a negative cursor, or a page size
	// outside [1, MaxResourceKeysetLimit].
	ErrInvalidResourceScope = errors.New("postgres: invalid resource scope")
)

// ResourceConflictError reports an insert that was refused because the
// natural identity already holds a resource row. The insert is a no-op: the
// earlier payload — different or identical — stays stored. It matches
// errors.Is(err, ErrResourceConflict).
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

// ResourceNotFoundError reports that no row matched an exact read inside the
// caller's scope. In restricted mode it does not disclose whether the row
// exists under an unauthorized auth path. It matches
// errors.Is(err, ErrResourceNotFound).
type ResourceNotFoundError struct {
	Project           string
	DatasetGeneration string
	ResourceType      string
	LogicalID         string
	// Restricted reports that the read was path-bound (the unrestricted
	// mode is explicit; see source.ResourceScope).
	Restricted bool
}

func (e *ResourceNotFoundError) Error() string {
	return fmt.Sprintf("postgres: no resource %s/%s/%s/%s in scope (restricted=%t)",
		e.Project, e.DatasetGeneration, e.ResourceType, e.LogicalID, e.Restricted)
}

func (e *ResourceNotFoundError) Unwrap() error { return ErrResourceNotFound }

// ResourceRepository stores and reads FHIR resource rows in one schema of
// the PostgreSQL source store. Create one with NewResourceRepository. It
// holds no state of its own beyond the qualified table name; the caller owns
// the connection source and the schema lifecycle (the table must be created
// by MigrateSchema before use).
type ResourceRepository struct {
	src      ConnSource
	tableFQN string
}

// NewResourceRepository validates the connection source and schema name and
// binds the repository to <schema>.fhir_resource. The connection source must
// be live only when a method runs, not here.
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

// InsertResource stores one logical FHIR resource and returns the
// server-assigned storage identity. The logical contract carries no storage
// sequence, so an insert can never offer or collide with one: the database
// assigns it. A duplicate natural identity returns a *ResourceConflictError
// matching ErrResourceConflict and leaves the stored row untouched. A nil
// Payload stores SQL NULL; the bytes "null" store an explicit JSON null
// document.
func (r *ResourceRepository) InsertResource(ctx context.Context, rec source.Resource) (int64, error) {
	// The physical row below carries the zero storage sequence; the shared
	// write guard still refuses any offered one at the single boundary all
	// writes cross.
	if err := validateResourceForInsert(ResourceRow{Resource: rec}); err != nil {
		return 0, err
	}
	// A nil payloadValue reaches PostgreSQL as NULL; a non-nil one is sent
	// as the payload's JSON bytes and cast to jsonb by the statement,
	// where PostgreSQL canonicalizes it on input.
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

// GetResource reads exactly one resource by the natural identity
// (project, dataset_generation, resource_type, logical_id) within scope. A
// restricted read also requires the row's auth path to be one of the bound
// paths, and reports a not-found that does not disclose rows outside the
// authorization. The unrestricted mode must be requested explicitly.
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
		// Restricted with no authorized path reads nothing; never widen.
		return ResourceRow{}, notFound
	}

	// Ordered parameters: $1 project, $2 generation, $3 type, $4 logical
	// id, $5 auth paths (restricted only).
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

// ReadResources pages resources inside one scope ordered by the immutable
// storage identity: rows with ResourceSeq strictly greater than afterSeq,
// ascending, at most limit rows. It returns the page and whether more rows
// remain in scope after it. Paging is stable: because the order key never
// changes, concurrent inserts cannot duplicate or skip a row that existed
// before the walk began. A restricted scope with no authorized paths reads
// nothing (empty page, no error); the unrestricted mode must be requested
// explicitly.
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
		// Restricted with no authorized path reads nothing; never widen.
		return nil, false, nil
	}

	// Ordered parameters: $1 project, $2 generation, $3 cursor,
	// $4 auth paths (restricted only), $last page size (+1 to detect more).
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

// acquire takes one pooled connection for a single-statement operation.
func (r *ResourceRepository) acquire(ctx context.Context) (*pgxpool.Conn, error) {
	conn, err := r.src.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: acquire resource connection: %w", err)
	}
	return conn, nil
}

// scanResourceRow scans one row of resourceColumns. A SQL NULL payload
// scans as a nil json.RawMessage, distinct from the bytes "null".
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

// validateResourceForInsert enforces the column contract before a round
// trip: the storage identity is server-assigned, the identity fields are
// present, and a payload is either absent (SQL NULL) or exact valid JSON.
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

// resourceScopeBinding is the validated form of source.ResourceScope for
// one query.
type resourceScopeBinding struct {
	// restricted is true unless the explicit unrestricted mode was chosen.
	restricted bool
	// paths are the bound auth paths in restricted mode (nil in
	// unrestricted mode). An empty non-nil list is a legitimate
	// authorization state that reads nothing.
	paths []string
}

// normalizeResourceScope validates a scope and projects it into query
// bindings. Unrestricted mode must be set explicitly and carries no paths;
// restricted mode binds exactly the authorized paths, including the empty
// string when the legacy namespace is authorized. Values are bound verbatim:
// the caller stores and reads with the same identity discipline.
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
