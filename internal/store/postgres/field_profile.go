// Bounded field-profile record persistence for the PostgreSQL 18 source
// store (pg-catalog-01).
//
// FieldProfileRepository is the persistence half of the field-profile slice
// built by internal/catalog/postgres. It stores the two tables created by
// migration 000007_field_profile:
//
//   - fhir_field_profile: one row per field-profile identity
//     (project, dataset_generation, auth_resource_path, resource_type,
//     path), plus the scope-level profile_status marker (path = ”).
//   - fhir_field_profile_coverage: one coverage marker per profiler scope
//     (project, dataset_generation, auth_resource_path, resource_type), so
//     a persisted known-empty profile stays distinguishable from a scope
//     that was never profiled.
//
// Guarantees:
//   - Upsert replaces exactly one identity: the whole non-identity column
//     set of the conflicting row is replaced; no other row is touched. The
//     primary key is the full identity tuple -- never the derived
//     profile_key -- because the legacy (empty-generation) key derivation is
//     many-to-one, so distinct scopes can never be merged by key collision.
//   - Lossless bounded summaries: every catalog.FieldCatalogDocument public
//     field round-trips unchanged, including false booleans and zero
//     counts. enrichment_complete is a nullable boolean (missing *bool is
//     SQL NULL, never coerced). Text arrays and the jsonb subrecord columns
//     keep SQL NULL (missing) distinct from a populated value, and the
//     correlated extension/semantic observations live inside jsonb as one
//     object per observation so values never cross-combine independently.
//   - Reads bind scope: filtered reads always bind project, dataset
//     generation, and resource types, plus (in restricted mode) the
//     authorized auth paths. The unrestricted mode must be requested
//     explicitly; restricted with no authorized paths reads nothing.
//
// This file adds no cross-database atomicity and no backend fallback: every
// failure is a named error the caller must surface.
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
	// fieldProfileTableName and fieldProfileCoverageTableName are the tables
	// created by migration 000007_field_profile.sql.
	fieldProfileTableName         = "fhir_field_profile"
	fieldProfileCoverageTableName = "fhir_field_profile_coverage"
	// MaxFieldProfileWriteBatch bounds one upsert chunk's row count so a
	// caller cannot build an oversized statement through the pool (26
	// parameters per row stays well under PostgreSQL's 65535-parameter cap).
	MaxFieldProfileWriteBatch = 500
	// defaultFieldProfileWriteBatch is used when the caller passes no batch
	// size.
	defaultFieldProfileWriteBatch = 50
	// fieldProfileColumnCount is len(fieldProfileColumns); the upsert builds
	// this many ordered parameters per row.
	fieldProfileColumnCount = 26
)

// Sentinel errors for classifying field-profile failures without string
// matching.
var (
	// ErrInvalidFieldProfileRow reports a document whose fields the database
	// contract cannot honor: blank identity or key, unknown record type, a
	// profile_status row carrying a path, a field row without a kind, or a
	// negative count.
	ErrInvalidFieldProfileRow = errors.New("postgres: invalid field profile row")
	// ErrInvalidFieldProfileScope reports a scope or filter that cannot be
	// honored: a blank project or resource type, a missing unrestricted
	// flag, or an unrestricted filter carrying paths.
	ErrInvalidFieldProfileScope = errors.New("postgres: invalid field profile scope")
)

// FieldProfileScope is the exact storage identity of one write-side profile:
// the identity tuple the catalog.Profiler was constructed with. Empty
// DatasetGeneration is the legacy dataset namespace, never "any generation";
// empty AuthResourcePath is the legacy (unscoped) namespace, never "any
// path".
type FieldProfileScope struct {
	Project           string
	DatasetGeneration string
	AuthResourcePath  string
	ResourceType      string
}

// FieldProfileFilter binds one discovery read to exactly one
// project/generation plus a declared resource-type list and authorization
// scope. It follows the explicit-mode convention of InventoryCountScope: the
// unrestricted mode must be requested deliberately and carries no paths.
type FieldProfileFilter struct {
	Project           string
	DatasetGeneration string
	// ResourceTypes bounds the read to the declared types; an empty list
	// reads nothing (there is no implicit "all types" here).
	ResourceTypes []string
	// AuthResourcePathsUnrestricted must be non-nil: true selects the
	// explicit unrestricted mode (then AuthResourcePaths must be empty),
	// false selects restricted mode.
	AuthResourcePathsUnrestricted *bool
	// AuthResourcePaths are the authorized auth_resource_path values in
	// restricted mode. An empty list in restricted mode is a legitimate
	// authorization state that reads nothing.
	AuthResourcePaths []string
}

// FieldProfileCoverage is the scope-level persistence marker written with
// every profile batch: it records that one profiler scope was persisted and
// how complete its enrichment was, so a zero-row profile read is "known
// empty" rather than "unavailable". EnrichmentComplete is the missing/
// true/false tri-state (*bool); SQL NULL is missing.
type FieldProfileCoverage struct {
	FieldProfileScope
	EnrichmentComplete  *bool
	EnrichmentTruncated bool
}

// fieldProfileColumns is the single source of truth for column order: the
// INSERT column list, the SELECT column list, the upsert argument order, and
// the scan order all derive from it, so ordered SQL parameters ($1..$N)
// match the Go argument slice by construction.
var fieldProfileColumns = []string{
	"project", "dataset_generation", "auth_resource_path", "resource_type", "path",
	"profile_key", "record_type", "kind", "doc_count", "max_items", "sample_count",
	"distinct_values", "distinct_truncated", "enrichment_complete", "enrichment_truncated",
	"pivot_candidate", "pivot_kind", "pivot_columns", "pivot_family", "pivot_column_selector",
	"pivot_value_selector", "pivot_item_source", "pivot_item_resource_type", "pivot_value_selectors",
	"extension_values", "semantic_observations",
}

// fieldProfileUpdateSet renders the DO UPDATE assignment for every
// non-identity column: an upsert replaces exactly the conflicting identity's
// payload and never a neighboring row.
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

// scopeColumnNames exists only to document the identity prefix of
// fieldProfileColumns; the switch above is the enforcement.
var scopeColumnNames = []string{"project", "dataset_generation", "auth_resource_path", "resource_type", "path"}

// FieldProfileRepository stores and reads bounded field-profile records in
// one schema of the PostgreSQL source store. Create one with
// NewFieldProfileRepository. The tables must be created by MigrateSchema
// before use; the caller owns the connection source and the schema
// lifecycle.
type FieldProfileRepository struct {
	src         ConnSource
	profileFQN  string
	coverageFQN string
}

// NewFieldProfileRepository validates the connection source and schema name
// and binds the repository to the field-profile and coverage tables inside
// <schema>. The connection source must be live only when a method runs, not
// here.
func NewFieldProfileRepository(src ConnSource, schema string) (*FieldProfileRepository, error) {
	if src == nil {
		return nil, errors.New("postgres: nil field profile connection source")
	}
	if !schemaNamePattern.MatchString(schema) {
		return nil, fmt.Errorf("%w: %q must match [a-z_][a-z0-9_]{0,62}", ErrInvalidSchemaName, schema)
	}
	return &FieldProfileRepository{
		src:         src,
		profileFQN:  pgx.Identifier{schema, fieldProfileTableName}.Sanitize(),
		coverageFQN: pgx.Identifier{schema, fieldProfileCoverageTableName}.Sanitize(),
	}, nil
}

// UpsertFieldProfileBatch replaces exactly the identities named by docs and
// refreshes the scope's coverage marker in one transaction: either every
// document and the marker are durable together or nothing from this batch
// is. Each conflicting identity's full non-identity column set is replaced;
// rows outside the batch are never touched. An empty docs list is legal and
// writes only the coverage marker -- that is how a persisted known-empty
// profile is recorded. Docs must already carry the scope's identity
// components; any divergence is a typed error and writes nothing.
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

	// The scope's coverage marker commits with the batch: a persisted
	// profile is never readable as "known empty" without its marker, and a
	// marker never claims a batch that did not commit.
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

// upsertFieldProfileChunk executes one multi-row upsert inside the caller's
// transaction. Ordered parameters, fieldProfileColumnCount per row, in
// fieldProfileColumns order.
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

// ReadFieldProfileRows returns every persisted document for one exact
// scope -- field records and any profile_status marker -- ordered by path
// in exact byte order. A scope with nothing persisted returns an empty
// slice: persistence questions belong to the coverage marker, which
// DiscoverFieldEnrichment consults.
func (r *FieldProfileRepository) ReadFieldProfileRows(ctx context.Context, scope FieldProfileScope) ([]catalog.FieldCatalogDocument, error) {
	if err := validateFieldProfileScope(scope); err != nil {
		return nil, err
	}
	// Ordered parameters: $1 project, $2 generation, $3 auth path, $4 type.
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

// ReadFieldProfileRowsFiltered returns every persisted document inside one
// filter, ordered by resource type and path in exact byte order. Restricted
// with no authorized paths reads nothing (empty slice, no round trip); the
// unrestricted mode must be requested explicitly.
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

// ReadProfileCoverage returns the coverage markers inside one filter,
// ordered by authorization path and resource type in exact byte order.
// Restricted with no authorized paths reads nothing (empty slice, no round
// trip).
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

// fieldProfileFilterSQL renders the shared WHERE clause for filtered reads.
// Ordered parameters: $1 project, $2 generation, $3 resource types, and
// $4 auth paths (restricted mode only). empty is true when the filter is a
// legitimate read-nothing state (restricted with no authorized paths, or no
// declared resource types).
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

// scanFieldProfileRows decodes rows whose column order is
// fieldProfileColumns. Nullable text arrays and jsonb columns scan into
// their Go zero values when SQL NULL, so a missing summary array stays nil
// and a missing subrecord list stays nil rather than an empty container;
// populated jsonb arrays decode back into one correlated struct per element.
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
	// SQL NULL (empty raw) leaves the slice nil: the missing state. A
	// stored '[]' decodes to an empty-but-non-nil slice, exactly what the
	// profiler emitted. No normalization here — nil versus empty is data.
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
	// Text arrays: the profiler's append(nil, ...) copies are nil-or-
	// populated; SQL NULL and a seeded '{}' both decode to the nil
	// summary slice.
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

// fieldProfileRowArgs maps a document to its column values in
// fieldProfileColumns order. Missing (nil) text arrays and missing
// subrecord slices cross the boundary as SQL NULL; populated and
// empty-but-non-nil jsonb subrecords are encoded as arrays so the Go
// nil-versus-empty distinction round-trips exactly, keeping each
// correlated observation one whole object that never cross-combines.
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

// textArrayParam passes nil (SQL NULL) for a missing slice and the slice
// itself when populated; PostgreSQL keeps NULL and '{}' distinct, and both
// decode back to the nil Go slice the profiler produces for an empty
// summary (the profiler's append(nil, ...) copies are nil-or-populated).
func textArrayParam(values []string) any {
	if len(values) == 0 {
		return nil
	}
	return append([]string(nil), values...)
}

// extensionValuesParam encodes correlated extension observations for the
// jsonb column: nil (missing) is SQL NULL; an empty-but-non-nil slice is
// the jsonb array '[]' so the distinction round-trips.
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

// semanticObservationsParam encodes correlated semantic observations for
// the jsonb column with the same nil/'[]'/populated contract, and with the
// bounded slice members (examples, observed units, extension URL path, and
// the value's fallback selectors) forced to their Go nil-versus-empty
// presence: the profiler emits empty-but-non-nil []string for a retained
// but valueless bounded list, encoding/json's omitempty would otherwise
// drop it, and the round trip would silently rewrite []string{} to nil.
// Inside a decoded struct member, JSON null and an absent key are the same
// Go nil, so forcing presence loses no further state.
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

// forceSlicePresence rewrites the named keys of an already-marshaled JSON
// object so each listed slice's Go presence is explicit: nil becomes JSON
// null and an empty-but-non-nil slice becomes [], overriding omitempty's
// omission. A populated slice keeps its plain-marshaled encoding.
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

// forceNestedSlicePresence applies forceSlicePresence to one member object.
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

// validateFieldProfileScope enforces the column contract for one exact
// identity. DatasetGeneration may be the empty legacy namespace; AuthResourcePath
// may be the empty legacy (unscoped) namespace; the other identity fields
// are required.
func validateFieldProfileScope(scope FieldProfileScope) error {
	if strings.TrimSpace(scope.Project) == "" {
		return fmt.Errorf("%w: project is blank", ErrInvalidFieldProfileScope)
	}
	if strings.TrimSpace(scope.ResourceType) == "" {
		return fmt.Errorf("%w: resource type is blank", ErrInvalidFieldProfileScope)
	}
	return nil
}

// validateFieldProfileRow enforces the column contract before a round trip:
// one row is either a field profile (path and kind present, record_type
// empty) or the scope-level profile_status marker (no path), never a mix.
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

// acquire takes one pooled connection for a single-statement or
// single-transaction field-profile operation.
func (r *FieldProfileRepository) acquire(ctx context.Context) (*pgxpool.Conn, error) {
	conn, err := r.src.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: acquire field profile connection: %w", err)
	}
	return conn, nil
}
