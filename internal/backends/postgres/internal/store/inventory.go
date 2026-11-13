package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/catalog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	inventoryCountTableName    = "fhir_resource_inventory_count"
	inventoryBuildTableName    = "fhir_resource_inventory_build"
	MaxInventoryWriteBatch     = 1000
	defaultInventoryWriteBatch = 1000
)

var (
	ErrInvalidInventoryRow    = errors.New("postgres: invalid inventory row")
	ErrInvalidInventoryScope  = errors.New("postgres: invalid inventory scope")
	ErrInventoryCountConflict = errors.New("postgres: inventory count conflict")
	ErrInventoryPublishGuard  = errors.New("postgres: inventory publish guard failed")
)

type InventorySourceGroup struct {
	AuthResourcePath string
	DocumentCount    int64
}

type InventoryCountScope struct {
	Project                       string
	DatasetGeneration             string
	BuildID                       string
	ResourceTypes                 []string
	AuthResourcePathsUnrestricted *bool
	AuthResourcePaths             []string
}

type InventoryCountConflictError struct {
	BuildID           string
	Project           string
	DatasetGeneration string
	ResourceType      string
	AuthResourcePath  string
	Field             string
	Stored            string
	Requested         string
}

func (e *InventoryCountConflictError) Error() string {
	return fmt.Sprintf("postgres: inventory count %s/%s (type %s, auth path %q) already holds build %s with %s=%s; "+
		"reinsert requested %s=%s and never replaces the stored row",
		e.Project, e.DatasetGeneration, e.ResourceType, e.AuthResourcePath,
		e.BuildID, e.Field, e.Stored, e.Field, e.Requested)
}

func (e *InventoryCountConflictError) Unwrap() error { return ErrInventoryCountConflict }

type InventoryPublishGuardError struct {
	BuildID              string
	Project              string
	DatasetGeneration    string
	ClaimedRowCount      int
	CommittedRowCount    int
	MissingResourceTypes []string
}

func (e *InventoryPublishGuardError) Error() string {
	return fmt.Sprintf("postgres: cannot publish inventory marker for %s/%s build %s: "+
		"marker claims %d count rows, %d committed; missing resource types: %s; "+
		"commit every count row first — the marker is published last",
		e.Project, e.DatasetGeneration, e.BuildID,
		e.ClaimedRowCount, e.CommittedRowCount, strings.Join(e.MissingResourceTypes, ", "))
}

func (e *InventoryPublishGuardError) Unwrap() error { return ErrInventoryPublishGuard }

type InventoryRepository struct {
	src       ConnSource
	countFQN  string
	buildFQN  string
	sourceFQN string
}

func NewInventoryRepository(src ConnSource, schema string) (*InventoryRepository, error) {
	if src == nil {
		return nil, errors.New("postgres: nil inventory connection source")
	}
	if !schemaNamePattern.MatchString(schema) {
		return nil, fmt.Errorf("%w: %q must match [a-z_][a-z0-9_]{0,62}", ErrInvalidSchemaName, schema)
	}
	return &InventoryRepository{
		src:       src,
		countFQN:  pgx.Identifier{schema, inventoryCountTableName}.Sanitize(),
		buildFQN:  pgx.Identifier{schema, inventoryBuildTableName}.Sanitize(),
		sourceFQN: pgx.Identifier{schema, resourceTableName}.Sanitize(),
	}, nil
}

func (r *InventoryRepository) ScanSourceCounts(ctx context.Context, project, datasetGeneration, resourceType string) ([]InventorySourceGroup, error) {
	if strings.TrimSpace(project) == "" {
		return nil, fmt.Errorf("%w: project is blank", ErrInvalidInventoryScope)
	}
	if strings.TrimSpace(resourceType) == "" {
		return nil, fmt.Errorf("%w: resource type is blank", ErrInvalidInventoryScope)
	}
	sql := "SELECT auth_resource_path, COUNT(*)::int8 FROM " + r.sourceFQN +
		" WHERE project = $1 AND dataset_generation = $2 AND resource_type = $3" +
		" GROUP BY auth_resource_path ORDER BY auth_resource_path"

	conn, err := r.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, sql, project, datasetGeneration, resourceType)
	if err != nil {
		return nil, fmt.Errorf("postgres: scan inventory source %s/%s/%s: %w",
			project, datasetGeneration, resourceType, err)
	}
	defer rows.Close()

	var out []InventorySourceGroup
	for rows.Next() {
		var group InventorySourceGroup
		if err := rows.Scan(&group.AuthResourcePath, &group.DocumentCount); err != nil {
			return nil, fmt.Errorf("postgres: scan inventory source %s/%s/%s: %w",
				project, datasetGeneration, resourceType, err)
		}
		if group.DocumentCount < 0 {
			return nil, fmt.Errorf("postgres: scan inventory source %s/%s/%s: negative count for path %q",
				project, datasetGeneration, resourceType, group.AuthResourcePath)
		}
		out = append(out, group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: scan inventory source %s/%s/%s: %w",
			project, datasetGeneration, resourceType, err)
	}
	return out, nil
}

func (r *InventoryRepository) InsertCounts(ctx context.Context, rows []catalog.ResourceInventoryCount, batchSize int) error {
	if len(rows) == 0 {
		return fmt.Errorf("%w: no count rows to insert", ErrInvalidInventoryRow)
	}
	if batchSize <= 0 {
		batchSize = defaultInventoryWriteBatch
	}
	if batchSize > MaxInventoryWriteBatch {
		return fmt.Errorf("%w: batch size is %d, want 1..%d", ErrInvalidInventoryScope, batchSize, MaxInventoryWriteBatch)
	}
	seen := make(map[string]struct{}, len(rows))
	for i, row := range rows {
		if err := validateInventoryCountRow(row); err != nil {
			return fmt.Errorf("postgres: inventory count row %d: %w", i, err)
		}
		identity := strings.Join([]string{row.BuildID, row.Project, row.DatasetGeneration, row.ResourceType, row.AuthResourcePath}, "\x00")
		if _, dup := seen[identity]; dup {
			return fmt.Errorf("%w: duplicate count scope %s/%s in one insert (type %q, auth path %q)",
				ErrInvalidInventoryRow, row.Project, row.DatasetGeneration, row.ResourceType, row.AuthResourcePath)
		}
		seen[identity] = struct{}{}
	}

	for start := 0; start < len(rows); start += batchSize {
		end := min(start+batchSize, len(rows))
		if err := r.insertCountChunk(ctx, rows[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (r *InventoryRepository) insertCountChunk(ctx context.Context, chunk []catalog.ResourceInventoryCount) error {
	conn, err := r.acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres: begin inventory count chunk: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	var sql strings.Builder
	sql.WriteString("INSERT INTO " + r.countFQN +
		" (count_key, build_id, project, dataset_generation, resource_type, auth_resource_path, document_count) VALUES ")
	args := make([]any, 0, len(chunk)*7)
	for i, row := range chunk {
		if i > 0 {
			sql.WriteString(", ")
		}
		base := i * 7
		sql.WriteString("(")
		for p := 1; p <= 7; p++ {
			if p > 1 {
				sql.WriteString(", ")
			}
			sql.WriteString("$")
			sql.WriteString(strconv.Itoa(base + p))
		}
		sql.WriteString(")")
		args = append(args, row.Key, row.BuildID, row.Project, row.DatasetGeneration,
			row.ResourceType, row.AuthResourcePath, row.DocumentCount)
	}
	sql.WriteString(" ON CONFLICT (build_id, project, dataset_generation, resource_type, auth_resource_path)" +
		" DO UPDATE SET document_count = EXCLUDED.document_count" +
		" WHERE " + inventoryCountTableName + ".document_count = EXCLUDED.document_count" +
		" AND " + inventoryCountTableName + ".count_key = EXCLUDED.count_key" +
		" RETURNING count_key")

	returningRows, err := tx.Query(ctx, sql.String(), args...)
	if err != nil {
		return inventoryWriteError("insert inventory counts", chunk, err)
	}
	returned := make(map[string]struct{}, len(chunk))
	var returnErr error
	for returningRows.Next() {
		var key string
		if err := returningRows.Scan(&key); err != nil {
			returningRows.Close()
			return fmt.Errorf("postgres: insert inventory counts: %w", err)
		}
		returned[key] = struct{}{}
	}
	returningRows.Close()
	returnErr = returningRows.Err()
	if returnErr != nil {
		return inventoryWriteError("insert inventory counts", chunk, returnErr)
	}

	if len(returned) != len(chunk) {
		for _, row := range chunk {
			if _, ok := returned[row.Key]; ok {
				continue
			}
			storedKey, storedCount, found, err := selectInventoryCountRow(ctx, tx, r.countFQN, row)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("postgres: insert inventory counts: row %s vanished before conflict inspection", row.Key)
			}
			if storedKey != row.Key {
				return &InventoryCountConflictError{
					BuildID: row.BuildID, Project: row.Project, DatasetGeneration: row.DatasetGeneration,
					ResourceType: row.ResourceType, AuthResourcePath: row.AuthResourcePath,
					Field: "count_key", Stored: storedKey, Requested: row.Key,
				}
			}
			if storedCount != row.DocumentCount {
				return &InventoryCountConflictError{
					BuildID: row.BuildID, Project: row.Project, DatasetGeneration: row.DatasetGeneration,
					ResourceType: row.ResourceType, AuthResourcePath: row.AuthResourcePath,
					Field: "document_count", Stored: strconv.FormatInt(storedCount, 10), Requested: strconv.FormatInt(row.DocumentCount, 10),
				}
			}
		}
		return fmt.Errorf("postgres: insert inventory counts: %d of %d rows returned without a detectable divergence",
			len(returned), len(chunk))
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit inventory count chunk: %w", err)
	}
	committed = true
	return nil
}

func (r *InventoryRepository) PublishBuild(ctx context.Context, build catalog.ResourceInventoryBuild) error {
	if err := validateInventoryBuildMarker(build); err != nil {
		return err
	}

	conn, err := r.acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres: begin inventory marker publish: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	var committedRows int
	err = tx.QueryRow(ctx,
		"SELECT COUNT(*) FROM "+r.countFQN+" WHERE build_id = $1 AND project = $2 AND dataset_generation = $3",
		build.BuildID, build.Project, build.DatasetGeneration).Scan(&committedRows)
	if err != nil {
		return fmt.Errorf("postgres: inventory publish guard count check: %w", err)
	}

	declared := append([]string(nil), build.ResourceTypes...)
	sort.Strings(declared)
	rows, err := tx.Query(ctx,
		"SELECT DISTINCT resource_type FROM "+r.countFQN+" WHERE build_id = $1 AND project = $2 AND dataset_generation = $3",
		build.BuildID, build.Project, build.DatasetGeneration)
	if err != nil {
		return fmt.Errorf("postgres: inventory publish guard coverage check: %w", err)
	}
	defer rows.Close()
	present := make(map[string]struct{}, len(declared))
	for rows.Next() {
		var resourceType string
		if err := rows.Scan(&resourceType); err != nil {
			rows.Close()
			return fmt.Errorf("postgres: inventory publish guard coverage check: %w", err)
		}
		present[resourceType] = struct{}{}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: inventory publish guard coverage check: %w", err)
	}
	var missing []string
	for _, resourceType := range declared {
		if _, ok := present[resourceType]; !ok {
			missing = append(missing, resourceType)
		}
	}
	if committedRows != build.CountRowCount || len(missing) > 0 {
		return &InventoryPublishGuardError{
			BuildID: build.BuildID, Project: build.Project, DatasetGeneration: build.DatasetGeneration,
			ClaimedRowCount: build.CountRowCount, CommittedRowCount: committedRows, MissingResourceTypes: missing,
		}
	}

	_, err = tx.Exec(ctx,
		"INSERT INTO "+r.buildFQN+
			" (marker_key, project, dataset_generation, build_id, coverage_digest, resource_types,"+
			" resource_type_count, count_row_count, complete)"+
			" VALUES ($1, $2, $3, $4, $5, $6::text[], $7, $8, $9)"+
			" ON CONFLICT (project, dataset_generation) DO UPDATE SET"+
			" marker_key = EXCLUDED.marker_key, build_id = EXCLUDED.build_id,"+
			" coverage_digest = EXCLUDED.coverage_digest, resource_types = EXCLUDED.resource_types,"+
			" resource_type_count = EXCLUDED.resource_type_count, count_row_count = EXCLUDED.count_row_count,"+
			" complete = EXCLUDED.complete, published_at = now()",
		build.Key, build.Project, build.DatasetGeneration, build.BuildID, build.CoverageDigest,
		declared, build.ResourceTypeCount, build.CountRowCount, build.Complete)
	if err != nil {
		if IsUniqueConstraintViolation(err, inventoryBuildTableName+"_key_ux") {
			return fmt.Errorf("postgres: inventory marker key %s collides across scopes (corrupted derivation): %w",
				build.Key, err)
		}
		return fmt.Errorf("postgres: publish inventory marker %s/%s: %w", build.Project, build.DatasetGeneration, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit inventory marker publish: %w", err)
	}
	committed = true
	return nil
}

func (r *InventoryRepository) ReadBuild(ctx context.Context, project, datasetGeneration string) (catalog.ResourceInventoryBuild, bool, error) {
	if strings.TrimSpace(project) == "" {
		return catalog.ResourceInventoryBuild{}, false, fmt.Errorf("%w: project is blank", ErrInvalidInventoryScope)
	}
	sql := "SELECT marker_key, build_id, coverage_digest, resource_types, resource_type_count, count_row_count, complete" +
		" FROM " + r.buildFQN + " WHERE project = $1 AND dataset_generation = $2"

	conn, err := r.acquire(ctx)
	if err != nil {
		return catalog.ResourceInventoryBuild{}, false, err
	}
	defer conn.Release()

	var build catalog.ResourceInventoryBuild
	err = conn.QueryRow(ctx, sql, project, datasetGeneration).Scan(
		&build.Key, &build.BuildID, &build.CoverageDigest, &build.ResourceTypes,
		&build.ResourceTypeCount, &build.CountRowCount, &build.Complete,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return catalog.ResourceInventoryBuild{}, false, nil
	}
	if err != nil {
		return catalog.ResourceInventoryBuild{}, false, fmt.Errorf("postgres: read inventory marker %s/%s: %w",
			project, datasetGeneration, err)
	}
	build.RecordType = catalog.ResourceInventoryBuildRecordType
	build.Project = project
	build.DatasetGeneration = datasetGeneration
	return build, true, nil
}

func (r *InventoryRepository) ReadCountSums(ctx context.Context, scope InventoryCountScope) (map[string]int64, error) {
	if strings.TrimSpace(scope.Project) == "" {
		return nil, fmt.Errorf("%w: project is blank", ErrInvalidInventoryScope)
	}
	if strings.TrimSpace(scope.BuildID) == "" {
		return nil, fmt.Errorf("%w: build id is blank", ErrInvalidInventoryScope)
	}
	if scope.AuthResourcePathsUnrestricted == nil {
		return nil, fmt.Errorf(
			"%w: AuthResourcePathsUnrestricted must be set explicitly; there is no implicit unrestricted mode",
			ErrInvalidInventoryScope)
	}
	sums := make(map[string]int64, len(scope.ResourceTypes))
	if *scope.AuthResourcePathsUnrestricted {
		if len(scope.AuthResourcePaths) > 0 {
			return nil, fmt.Errorf(
				"%w: unrestricted mode must not carry auth resource paths (%d given)",
				ErrInvalidInventoryScope, len(scope.AuthResourcePaths))
		}
	} else if len(scope.AuthResourcePaths) == 0 {
		return sums, nil
	}
	if len(scope.ResourceTypes) == 0 {
		return sums, nil
	}

	sql := "SELECT resource_type, SUM(document_count)::bigint FROM " + r.countFQN +
		" WHERE project = $1 AND dataset_generation = $2 AND build_id = $3 AND resource_type = ANY($4::text[])"
	args := []any{scope.Project, scope.DatasetGeneration, scope.BuildID, scope.ResourceTypes}
	if !*scope.AuthResourcePathsUnrestricted {
		sql += " AND auth_resource_path = ANY($5::text[])"
		args = append(args, scope.AuthResourcePaths)
	}
	sql += " GROUP BY resource_type"

	conn, err := r.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: read inventory counts %s/%s build %s: %w",
			scope.Project, scope.DatasetGeneration, scope.BuildID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var resourceType string
		var total int64
		if err := rows.Scan(&resourceType, &total); err != nil {
			return nil, fmt.Errorf("postgres: read inventory counts %s/%s build %s: %w",
				scope.Project, scope.DatasetGeneration, scope.BuildID, err)
		}
		if total < 0 {
			return nil, fmt.Errorf("postgres: read inventory counts %s/%s build %s: negative sum for type %q",
				scope.Project, scope.DatasetGeneration, scope.BuildID, resourceType)
		}
		sums[resourceType] = total
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: read inventory counts %s/%s build %s: %w",
			scope.Project, scope.DatasetGeneration, scope.BuildID, err)
	}
	return sums, nil
}

func selectInventoryCountRow(ctx context.Context, tx pgx.Tx, countFQN string, row catalog.ResourceInventoryCount) (string, int64, bool, error) {
	var storedKey string
	var storedCount int64
	err := tx.QueryRow(ctx,
		"SELECT count_key, document_count FROM "+countFQN+
			" WHERE build_id = $1 AND project = $2 AND dataset_generation = $3 AND resource_type = $4 AND auth_resource_path = $5",
		row.BuildID, row.Project, row.DatasetGeneration, row.ResourceType, row.AuthResourcePath,
	).Scan(&storedKey, &storedCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, fmt.Errorf("postgres: inspect conflicting inventory count %s: %w", row.Key, err)
	}
	return storedKey, storedCount, true, nil
}

func inventoryWriteError(op string, chunk []catalog.ResourceInventoryCount, err error) error {
	if pgErr, ok := pgErrorFrom(err); ok && pgErr.Code == sqlStateUniqueViolation {
		sample := chunk[0]
		if pgErr.ConstraintName == inventoryCountTableName+"_key_ux" {
			return &InventoryCountConflictError{
				BuildID: sample.BuildID, Project: sample.Project, DatasetGeneration: sample.DatasetGeneration,
				ResourceType: sample.ResourceType, AuthResourcePath: sample.AuthResourcePath,
				Field: "count_key", Stored: "<other scope holds this key>", Requested: sample.Key,
			}
		}
		return fmt.Errorf("%w: %s: identity unique violation (%s): %w",
			ErrInventoryCountConflict, op, pgErr.ConstraintName, err)
	}
	return fmt.Errorf("postgres: %s: %w", op, err)
}

func validateInventoryCountRow(row catalog.ResourceInventoryCount) error {
	if row.RecordType != catalog.ResourceInventoryCountRecordType {
		return fmt.Errorf("%w: record type is %q, want %q", ErrInvalidInventoryRow, row.RecordType, catalog.ResourceInventoryCountRecordType)
	}
	if strings.TrimSpace(row.Key) == "" {
		return fmt.Errorf("%w: count key is blank", ErrInvalidInventoryRow)
	}
	if strings.TrimSpace(row.BuildID) == "" {
		return fmt.Errorf("%w: build id is blank", ErrInvalidInventoryRow)
	}
	for _, f := range []struct {
		name  string
		value string
	}{
		{"project", row.Project},
		{"resource type", row.ResourceType},
	} {
		if strings.TrimSpace(f.value) == "" {
			return fmt.Errorf("%w: %s is blank", ErrInvalidInventoryRow, f.name)
		}
	}
	if row.DocumentCount < 0 {
		return fmt.Errorf("%w: document count is %d, want >= 0", ErrInvalidInventoryRow, row.DocumentCount)
	}
	return nil
}

func validateInventoryBuildMarker(build catalog.ResourceInventoryBuild) error {
	if build.RecordType != catalog.ResourceInventoryBuildRecordType {
		return fmt.Errorf("%w: record type is %q, want %q", ErrInvalidInventoryRow, build.RecordType, catalog.ResourceInventoryBuildRecordType)
	}
	if !build.Complete {
		return fmt.Errorf("%w: marker claims complete=false; the store publishes only complete markers", ErrInvalidInventoryRow)
	}
	for _, f := range []struct {
		name  string
		value string
	}{
		{"marker key", build.Key},
		{"project", build.Project},
		{"build id", build.BuildID},
		{"coverage digest", build.CoverageDigest},
	} {
		if strings.TrimSpace(f.value) == "" {
			return fmt.Errorf("%w: %s is blank", ErrInvalidInventoryRow, f.name)
		}
	}
	if len(build.ResourceTypes) == 0 {
		return fmt.Errorf("%w: marker declares no resource types", ErrInvalidInventoryRow)
	}
	if build.ResourceTypeCount != len(build.ResourceTypes) {
		return fmt.Errorf("%w: resource type count is %d, want %d", ErrInvalidInventoryRow, build.ResourceTypeCount, len(build.ResourceTypes))
	}
	if build.CountRowCount < build.ResourceTypeCount {
		return fmt.Errorf("%w: count row count %d is below the declared %d resource types",
			ErrInvalidInventoryRow, build.CountRowCount, build.ResourceTypeCount)
	}
	return nil
}

func (r *InventoryRepository) acquire(ctx context.Context) (*pgxpool.Conn, error) {
	conn, err := r.src.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: acquire inventory connection: %w", err)
	}
	return conn, nil
}
