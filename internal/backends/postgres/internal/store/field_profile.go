package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/catalog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	fieldProfileTableName         = "fhir_field_profile"
	fieldProfileCoverageTableName = "fhir_field_profile_coverage"
	MaxFieldProfileWriteBatch     = 500
	defaultFieldProfileWriteBatch = 50
	fieldProfileColumnCount       = 26
)

var (
	ErrInvalidFieldProfileRow   = errors.New("postgres: invalid field profile row")
	ErrInvalidFieldProfileScope = errors.New("postgres: invalid field profile scope")
)

type FieldProfileScope = catalog.FieldProfileScope

type FieldProfileFilter struct {
	Project                       string
	DatasetGeneration             string
	ResourceTypes                 []string
	AllResourceTypes              bool
	AuthResourcePathsUnrestricted *bool
	AuthResourcePaths             []string
}

type FieldProfileCoverage struct {
	FieldProfileScope
	EnrichmentComplete  *bool
	EnrichmentTruncated bool
}

var fieldProfileColumns = []string{
	"project", "dataset_generation", "auth_resource_path", "resource_type", "path",
	"profile_key", "record_type", "kind", "doc_count", "max_items", "sample_count",
	"distinct_values", "distinct_truncated", "enrichment_complete", "enrichment_truncated",
	"pivot_candidate", "pivot_kind", "pivot_columns", "pivot_family", "pivot_column_selector",
	"pivot_value_selector", "pivot_item_source", "pivot_item_resource_type", "pivot_value_selectors",
	"extension_values", "semantic_observations",
}

var fieldProfileUpdateSet = func() string {
	parts := make([]string, 0, len(fieldProfileColumns)-len(scopeColumnNames))
	for _, column := range fieldProfileColumns {
		switch column {
		case "project", "dataset_generation", "auth_resource_path", "resource_type", "path":
			continue
		}
		parts = append(parts, column+" = EXCLUDED."+column)
	}
	return strings.Join(parts, ", ")
}()

var scopeColumnNames = []string{"project", "dataset_generation", "auth_resource_path", "resource_type", "path"}

type FieldProfileRepository struct {
	src         ConnSource
	profileFQN  string
	coverageFQN string
}

func NewFieldProfileRepository(src ConnSource, schema string) (*FieldProfileRepository, error) {
	if src == nil {
		return nil, errors.New("postgres: nil field profile connection source")
	}
	if !ValidSchemaName(schema) {
		return nil, fmt.Errorf("%w: %q must match [a-z_][a-z0-9_]{0,62}", ErrInvalidSchemaName, schema)
	}
	return &FieldProfileRepository{
		src:         src,
		profileFQN:  pgx.Identifier{schema, fieldProfileTableName}.Sanitize(),
		coverageFQN: pgx.Identifier{schema, fieldProfileCoverageTableName}.Sanitize(),
	}, nil
}

func (r *FieldProfileRepository) UpsertFieldProfileBatch(ctx context.Context, scope FieldProfileScope, docs []catalog.FieldCatalogDocument, coverage FieldProfileCoverage, batchSize int) error {
	if err := validateFieldProfileScope(scope); err != nil {
		return err
	}
	if coverage.Project != scope.Project || coverage.DatasetGeneration != scope.DatasetGeneration ||
		coverage.AuthResourcePath != scope.AuthResourcePath || coverage.ResourceType != scope.ResourceType {
		return fmt.Errorf("%w: coverage marker scope %+v diverges from batch scope %+v", ErrInvalidFieldProfileScope, coverage.FieldProfileScope, scope)
	}
	if batchSize <= 0 {
		batchSize = defaultFieldProfileWriteBatch
	}
	if batchSize > MaxFieldProfileWriteBatch {
		return fmt.Errorf("%w: batch size is %d, want 1..%d", ErrInvalidFieldProfileScope, batchSize, MaxFieldProfileWriteBatch)
	}
	args := make([][]any, 0, len(docs))
	for i := range docs {
		if err := validateFieldProfileRow(docs[i]); err != nil {
			return fmt.Errorf("postgres: field profile row %d: %w", i, err)
		}
		if docs[i].Project != scope.Project || docs[i].DatasetGeneration != scope.DatasetGeneration ||
			docs[i].AuthResourcePath != scope.AuthResourcePath || docs[i].ResourceType != scope.ResourceType {
			return fmt.Errorf("%w: row %d scope (%s/%s/%s/%s) diverges from batch scope (%s/%s/%s/%s)",
				ErrInvalidFieldProfileScope, i,
				docs[i].Project, docs[i].DatasetGeneration, docs[i].AuthResourcePath, docs[i].ResourceType,
				scope.Project, scope.DatasetGeneration, scope.AuthResourcePath, scope.ResourceType)
		}
		encoded, err := fieldProfileRowArgs(docs[i])
		if err != nil {
			return fmt.Errorf("postgres: field profile row %d: %w", i, err)
		}
		args = append(args, encoded)
	}

	conn, err := r.acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres: begin field profile batch: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	for start := 0; start < len(args); start += batchSize {
		end := min(start+batchSize, len(args))
		if err := upsertFieldProfileChunk(ctx, tx, r.profileFQN, args[start:end]); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx,
		"INSERT INTO "+r.coverageFQN+
			" (project, dataset_generation, auth_resource_path, resource_type, enrichment_complete, enrichment_truncated)"+
			" VALUES ($1, $2, $3, $4, $5, $6)"+
			" ON CONFLICT (project, dataset_generation, auth_resource_path, resource_type) DO UPDATE SET"+
			" enrichment_complete = EXCLUDED.enrichment_complete,"+
			" enrichment_truncated = EXCLUDED.enrichment_truncated,"+
			" updated_at = now()",
		coverage.Project, coverage.DatasetGeneration, coverage.AuthResourcePath, coverage.ResourceType,
		coverage.EnrichmentComplete, coverage.EnrichmentTruncated); err != nil {
		return fmt.Errorf("postgres: upsert field profile coverage %s/%s/%s/%s: %w",
			coverage.Project, coverage.DatasetGeneration, coverage.AuthResourcePath, coverage.ResourceType, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit field profile batch: %w", err)
	}
	committed = true
	return nil
}

func upsertFieldProfileChunk(ctx context.Context, tx pgx.Tx, profileFQN string, chunk [][]any) error {
	var sql strings.Builder
	sql.WriteString("INSERT INTO " + profileFQN + " (" + strings.Join(fieldProfileColumns, ", ") + ") VALUES ")
	args := make([]any, 0, len(chunk)*fieldProfileColumnCount)
	for i, row := range chunk {
		if i > 0 {
			sql.WriteString(", ")
		}
		sql.WriteString("(")
		for p := 1; p <= fieldProfileColumnCount; p++ {
			if p > 1 {
				sql.WriteString(", ")
			}
			sql.WriteString("$")
			sql.WriteString(strconv.Itoa(i*fieldProfileColumnCount + p))
		}
		sql.WriteString(")")
		args = append(args, row...)
	}
	sql.WriteString(" ON CONFLICT (project, dataset_generation, auth_resource_path, resource_type, path) DO UPDATE SET ")
	sql.WriteString(fieldProfileUpdateSet)
	if _, err := tx.Exec(ctx, sql.String(), args...); err != nil {
		return fmt.Errorf("postgres: upsert field profile chunk of %d rows: %w", len(chunk), err)
	}
	return nil
}

func (r *FieldProfileRepository) ReadFieldProfileRows(ctx context.Context, scope FieldProfileScope) ([]catalog.FieldCatalogDocument, error) {
	if err := validateFieldProfileScope(scope); err != nil {
		return nil, err
	}
	sql := "SELECT " + strings.Join(fieldProfileColumns, ", ") + " FROM " + r.profileFQN +
		" WHERE project = $1 AND dataset_generation = $2 AND auth_resource_path = $3 AND resource_type = $4" +
		" ORDER BY path"

	conn, err := r.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, sql, scope.Project, scope.DatasetGeneration, scope.AuthResourcePath, scope.ResourceType)
	if err != nil {
		return nil, fmt.Errorf("postgres: read field profiles %s/%s/%s/%s: %w",
			scope.Project, scope.DatasetGeneration, scope.AuthResourcePath, scope.ResourceType, err)
	}
	defer rows.Close()
	return scanFieldProfileRows(rows)
}

func (r *FieldProfileRepository) ReadFieldProfileRowsFiltered(ctx context.Context, filter FieldProfileFilter) ([]catalog.FieldCatalogDocument, error) {
	where, args, empty, err := fieldProfileFilterSQL(filter)
	if err != nil {
		return nil, err
	}
	if empty {
		return []catalog.FieldCatalogDocument{}, nil
	}
	sql := "SELECT " + strings.Join(fieldProfileColumns, ", ") + " FROM " + r.profileFQN +
		" WHERE " + where + " ORDER BY resource_type, path"

	conn, err := r.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: read field profiles %s/%s: %w", filter.Project, filter.DatasetGeneration, err)
	}
	defer rows.Close()
	return scanFieldProfileRows(rows)
}

func (r *FieldProfileRepository) ReadProfileCoverage(ctx context.Context, filter FieldProfileFilter) ([]FieldProfileCoverage, error) {
	where, args, empty, err := fieldProfileFilterSQL(filter)
	if err != nil {
		return nil, err
	}
	if empty {
		return []FieldProfileCoverage{}, nil
	}
	sql := "SELECT project, dataset_generation, auth_resource_path, resource_type, enrichment_complete, enrichment_truncated" +
		" FROM " + r.coverageFQN + " WHERE " + where + " ORDER BY auth_resource_path, resource_type"

	conn, err := r.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: read field profile coverage %s/%s: %w", filter.Project, filter.DatasetGeneration, err)
	}
	defer rows.Close()

	out := []FieldProfileCoverage{}
	for rows.Next() {
		var coverage FieldProfileCoverage
		if err := rows.Scan(&coverage.Project, &coverage.DatasetGeneration, &coverage.AuthResourcePath,
			&coverage.ResourceType, &coverage.EnrichmentComplete, &coverage.EnrichmentTruncated); err != nil {
			return nil, fmt.Errorf("postgres: read field profile coverage %s/%s: %w", filter.Project, filter.DatasetGeneration, err)
		}
		out = append(out, coverage)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: read field profile coverage %s/%s: %w", filter.Project, filter.DatasetGeneration, err)
	}
	return out, nil
}

func (r *FieldProfileRepository) ListDistinctAuthResourcePaths(ctx context.Context, project string) ([]string, error) {
	sql, args, err := distinctAuthResourcePathsSQL(r.profileFQN, project)
	if err != nil {
		return nil, err
	}
	conn, err := r.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: list distinct auth resource paths %s: %w", project, err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("postgres: scan distinct auth resource path %s: %w", project, err)
		}
		out = append(out, path)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list distinct auth resource paths %s: %w", project, err)
	}
	return out, nil
}

func distinctAuthResourcePathsSQL(profileFQN, project string) (string, []any, error) {
	if strings.TrimSpace(project) == "" {
		return "", nil, fmt.Errorf("%w: project is blank", ErrInvalidFieldProfileScope)
	}
	sql := `SELECT DISTINCT auth_resource_path COLLATE "C" FROM ` + profileFQN +
		" WHERE project = $1 AND auth_resource_path <> ''" +
		" ORDER BY auth_resource_path"
	return sql, []any{project}, nil
}

func fieldProfileFilterSQL(filter FieldProfileFilter) (string, []any, bool, error) {
	if strings.TrimSpace(filter.Project) == "" {
		return "", nil, false, fmt.Errorf("%w: project is blank", ErrInvalidFieldProfileScope)
	}
	if filter.AuthResourcePathsUnrestricted == nil {
		return "", nil, false, fmt.Errorf(
			"%w: AuthResourcePathsUnrestricted must be set explicitly; there is no implicit unrestricted mode",
			ErrInvalidFieldProfileScope)
	}
	if *filter.AuthResourcePathsUnrestricted {
		if len(filter.AuthResourcePaths) > 0 {
			return "", nil, false, fmt.Errorf(
				"%w: unrestricted mode must not carry auth resource paths (%d given)",
				ErrInvalidFieldProfileScope, len(filter.AuthResourcePaths))
		}
	} else if len(filter.AuthResourcePaths) == 0 {
		return "", nil, true, nil
	}
	if filter.AllResourceTypes {
		if len(filter.ResourceTypes) > 0 {
			return "", nil, false, fmt.Errorf(
				"%w: AllResourceTypes must not accompany a declared resource types list (%d given)",
				ErrInvalidFieldProfileScope, len(filter.ResourceTypes))
		}
		where := "project = $1 AND dataset_generation = $2"
		args := []any{filter.Project, filter.DatasetGeneration}
		if !*filter.AuthResourcePathsUnrestricted {
			where += " AND auth_resource_path = ANY($3::text[])"
			args = append(args, append([]string(nil), filter.AuthResourcePaths...))
		}
		return where, args, false, nil
	}
	if len(filter.ResourceTypes) == 0 {
		return "", nil, true, nil
	}
	where := "project = $1 AND dataset_generation = $2 AND resource_type = ANY($3::text[])"
	args := []any{filter.Project, filter.DatasetGeneration, append([]string(nil), filter.ResourceTypes...)}
	if !*filter.AuthResourcePathsUnrestricted {
		where += " AND auth_resource_path = ANY($4::text[])"
		args = append(args, append([]string(nil), filter.AuthResourcePaths...))
	}
	return where, args, false, nil
}

func scanFieldProfileRows(rows pgx.Rows) ([]catalog.FieldCatalogDocument, error) {
	out := []catalog.FieldCatalogDocument{}
	for rows.Next() {
		var doc catalog.FieldCatalogDocument
		var extensionRaw, semanticRaw json.RawMessage
		targets := []any{
			&doc.Project, &doc.DatasetGeneration, &doc.AuthResourcePath, &doc.ResourceType, &doc.Path,
			&doc.Key, &doc.RecordType, &doc.Kind, &doc.DocCount, &doc.MaxItems, &doc.SampleCount,
			&doc.DistinctValues, &doc.DistinctTruncated, &doc.EnrichmentComplete, &doc.EnrichmentTruncated,
			&doc.PivotCandidate, &doc.PivotKind, &doc.PivotColumns, &doc.PivotFamily, &doc.PivotColumnSelect,
			&doc.PivotValueSelect, &doc.PivotItemSource, &doc.PivotItemResourceType, &doc.PivotValueSelectors,
			&extensionRaw, &semanticRaw,
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, fmt.Errorf("postgres: scan field profile row %s/%s/%s: %w",
				doc.Project, doc.ResourceType, doc.Path, err)
		}
		if err := decodeFieldProfileJSON(&doc, extensionRaw, semanticRaw); err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: read field profile rows: %w", err)
	}
	return out, nil
}

func decodeFieldProfileJSON(doc *catalog.FieldCatalogDocument, extensionRaw, semanticRaw json.RawMessage) error {
	if len(extensionRaw) > 0 {
		if err := json.Unmarshal(extensionRaw, &doc.ExtensionValues); err != nil {
			return fmt.Errorf("postgres: decode extension observations for field profile %s/%s/%s: %w",
				doc.Project, doc.ResourceType, doc.Path, err)
		}
	}
	if len(semanticRaw) > 0 {
		if err := json.Unmarshal(semanticRaw, &doc.SemanticObservations); err != nil {
			return fmt.Errorf("postgres: decode semantic observations for field profile %s/%s/%s: %w",
				doc.Project, doc.ResourceType, doc.Path, err)
		}
	}
	if len(doc.DistinctValues) == 0 {
		doc.DistinctValues = nil
	}
	if len(doc.PivotColumns) == 0 {
		doc.PivotColumns = nil
	}
	if len(doc.PivotValueSelectors) == 0 {
		doc.PivotValueSelectors = nil
	}
	return nil
}

func fieldProfileRowArgs(doc catalog.FieldCatalogDocument) ([]any, error) {
	extensionValues, err := extensionValuesParam(doc.ExtensionValues)
	if err != nil {
		return nil, fmt.Errorf("encode extension values: %w", err)
	}
	semanticObservations, err := semanticObservationsParam(doc.SemanticObservations)
	if err != nil {
		return nil, fmt.Errorf("encode semantic observations: %w", err)
	}
	return []any{
		doc.Project, doc.DatasetGeneration, doc.AuthResourcePath, doc.ResourceType, doc.Path,
		doc.Key, doc.RecordType, doc.Kind, doc.DocCount, int64(doc.MaxItems), int64(doc.SampleCount),
		textArrayParam(doc.DistinctValues), doc.DistinctTruncated, doc.EnrichmentComplete, doc.EnrichmentTruncated,
		doc.PivotCandidate, doc.PivotKind, textArrayParam(doc.PivotColumns), doc.PivotFamily, doc.PivotColumnSelect,
		doc.PivotValueSelect, doc.PivotItemSource, doc.PivotItemResourceType, textArrayParam(doc.PivotValueSelectors),
		extensionValues, semanticObservations,
	}, nil
}

func textArrayParam(values []string) any {
	if len(values) == 0 {
		return nil
	}
	return append([]string(nil), values...)
}

func extensionValuesParam(values []catalog.ExtensionValueObservation) ([]byte, error) {
	if values == nil {
		return nil, nil
	}
	encoded := make([]json.RawMessage, 0, len(values))
	for i := range values {
		observation, err := json.Marshal(values[i])
		if err != nil {
			return nil, err
		}
		object, err := forceSlicePresence([]string{"url_path"}, map[string][]string{
			"url_path": values[i].URLPath,
		}, observation)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, object)
	}
	return json.Marshal(encoded)
}

func semanticObservationsParam(observations []catalog.SemanticObservation) ([]byte, error) {
	if observations == nil {
		return nil, nil
	}
	encoded := make([]json.RawMessage, 0, len(observations))
	for i := range observations {
		observation, err := json.Marshal(observations[i])
		if err != nil {
			return nil, err
		}
		object, err := forceSlicePresence([]string{"extension_url_path", "observed_units", "examples"}, map[string][]string{
			"extension_url_path": observations[i].ExtensionURLPath,
			"observed_units":     observations[i].ObservedUnits,
			"examples":           observations[i].Examples,
		}, observation)
		if err != nil {
			return nil, err
		}
		object, err = forceNestedSlicePresence(object, "value", "fallback_selectors", observations[i].Value.FallbackSelectors)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, object)
	}
	return json.Marshal(encoded)
}

func forceSlicePresence(keys []string, values map[string][]string, observation json.RawMessage) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(observation, &object); err != nil {
		return nil, err
	}
	for _, key := range keys {
		if values[key] == nil {
			object[key] = json.RawMessage("null")
		} else if len(values[key]) == 0 {
			object[key] = json.RawMessage("[]")
		}
	}
	return json.Marshal(object)
}

func forceNestedSlicePresence(observation json.RawMessage, member, key string, value []string) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(observation, &object); err != nil {
		return nil, err
	}
	nested, ok := object[member]
	if !ok {
		return observation, nil
	}
	forced, err := forceSlicePresence([]string{key}, map[string][]string{key: value}, nested)
	if err != nil {
		return nil, err
	}
	object[member] = forced
	return json.Marshal(object)
}

func validateFieldProfileScope(scope FieldProfileScope) error {
	if strings.TrimSpace(scope.Project) == "" {
		return fmt.Errorf("%w: project is blank", ErrInvalidFieldProfileScope)
	}
	if strings.TrimSpace(scope.ResourceType) == "" {
		return fmt.Errorf("%w: resource type is blank", ErrInvalidFieldProfileScope)
	}
	return nil
}

func validateFieldProfileRow(doc catalog.FieldCatalogDocument) error {
	if strings.TrimSpace(doc.Project) == "" {
		return fmt.Errorf("%w: project is blank", ErrInvalidFieldProfileRow)
	}
	if strings.TrimSpace(doc.ResourceType) == "" {
		return fmt.Errorf("%w: resource type is blank", ErrInvalidFieldProfileRow)
	}
	if strings.TrimSpace(doc.Key) == "" {
		return fmt.Errorf("%w: profile key is blank", ErrInvalidFieldProfileRow)
	}
	switch doc.RecordType {
	case "":
		if doc.Path == "" {
			return fmt.Errorf("%w: field profile has no path (record_type is empty)", ErrInvalidFieldProfileRow)
		}
		if strings.TrimSpace(doc.Kind) == "" {
			return fmt.Errorf("%w: field profile %q has no kind", ErrInvalidFieldProfileRow, doc.Path)
		}
	case catalog.FieldProfileStatusRecordType:
		if doc.Path != "" {
			return fmt.Errorf("%w: profile_status marker carries path %q", ErrInvalidFieldProfileRow, doc.Path)
		}
	default:
		return fmt.Errorf("%w: record type %q is neither a field profile nor %q",
			ErrInvalidFieldProfileRow, doc.RecordType, catalog.FieldProfileStatusRecordType)
	}
	if doc.DocCount < 0 || doc.MaxItems < 0 || doc.SampleCount < 0 {
		return fmt.Errorf("%w: counts are negative (doc_count=%d, max_items=%d, sample_count=%d)",
			ErrInvalidFieldProfileRow, doc.DocCount, doc.MaxItems, doc.SampleCount)
	}
	return nil
}

func (r *FieldProfileRepository) acquire(ctx context.Context) (*pgxpool.Conn, error) {
	conn, err := r.src.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: acquire field profile connection: %w", err)
	}
	return conn, nil
}
