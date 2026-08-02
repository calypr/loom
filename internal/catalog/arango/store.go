// Package arango contains the Arango persistence adapter for catalog facts.
package arango

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/calypr/loom/internal/catalog"
	store "github.com/calypr/loom/internal/store/arango"
)

type client interface {
	QueryRows(context.Context, string, int, map[string]any, store.RowVisitor) error
	InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error
	ExecuteAQL(context.Context, string, map[string]any) error
	Bootstrap(context.Context, store.BootstrapSpec) error
}

type Store struct {
	client client
}

func New(client client) (*Store, error) {
	if client == nil {
		return nil, fmt.Errorf("catalog Arango client is required")
	}
	return &Store{client: client}, nil
}

func (s *Store) DiscoverFields(ctx context.Context, opts catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
	if opts.CursorBatch <= 0 {
		opts.CursorBatch = 1000
	}
	vars := map[string]any{"project": opts.Project, "dataset_generation": generation(opts.DatasetGeneration), "resource_type": nil, "pivot_only": opts.PivotOnly, "auth_resource_paths": append([]string(nil), opts.AuthResourcePaths...), "auth_resource_paths_unrestricted": catalog.EffectiveAuthResourcePathsUnrestricted(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted)}
	if opts.ResourceType != "" {
		vars["resource_type"] = opts.ResourceType
	}
	rows := []catalog.PopulatedField{}
	err := s.client.QueryRows(ctx, populatedFieldsAQL, opts.CursorBatch, vars, func(row map[string]any) error {
		var out catalog.PopulatedField
		if err := decode(row, &out); err != nil {
			return err
		}
		rows = append(rows, out)
		return nil
	})
	return rows, err
}

func (s *Store) DiscoverReferences(ctx context.Context, opts catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
	if opts.CursorBatch <= 0 {
		opts.CursorBatch = 1000
	}
	mode := opts.Mode
	if mode == "" {
		mode = catalog.TraversalModeStorage
	}
	vars := map[string]any{"project": opts.Project, "dataset_generation": generation(opts.DatasetGeneration), "auth_resource_paths": append([]string(nil), opts.AuthResourcePaths...), "auth_resource_paths_unrestricted": catalog.EffectiveAuthResourcePathsUnrestricted(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted)}
	query := relationshipCatalogStorageAQL
	if mode == catalog.TraversalModeBuilder {
		query = relationshipCatalogBuilderAQL
		vars["node_type"] = nil
		if opts.NodeType != "" {
			vars["node_type"] = opts.NodeType
		}
	} else {
		vars["from_type"] = nil
		if opts.FromType != "" {
			vars["from_type"] = opts.FromType
		}
	}
	rows := make([]catalog.PopulatedReference, 0, 64)
	err := s.client.QueryRows(ctx, query, opts.CursorBatch, vars, func(row map[string]any) error {
		count, err := decodeInt64(row["edge_count"])
		if err != nil {
			return fmt.Errorf("decode edge_count for %s/%s/%s: %w", stringValue(row["from_type"]), stringValue(row["label"]), stringValue(row["to_type"]), err)
		}
		rows = append(rows, catalog.PopulatedReference{DatasetGeneration: stringValue(row["dataset_generation"]), FromType: stringValue(row["from_type"]), Label: stringValue(row["label"]), ToType: stringValue(row["to_type"]), EdgeCount: count})
		return nil
	})
	return rows, err
}

func (s *Store) DiscoverDatasets(ctx context.Context, opts catalog.DatasetSummaryOptions) ([]catalog.DatasetSummary, error) {
	if opts.CursorBatch <= 0 {
		opts.CursorBatch = 1000
	}
	projects := normalizedProjects(opts.ProjectAllowlist)
	result := make([]catalog.DatasetSummary, 0, len(projects))
	for _, project := range projects {
		generationValue := opts.DatasetGenerationByProject[project]
		scope, hasScope := opts.AuthScopesByProject[project]
		if !hasScope {
			scope.Unrestricted = true
		}
		state := opts.DatasetStateByProject[project]
		if state == "" {
			state = "LEGACY"
			if generationValue != "" {
				state = "READY"
			}
		}
		vars := map[string]any{"project": project, "dataset_generation": generation(generationValue), "auth_resource_paths": append([]string(nil), scope.AuthResourcePaths...), "auth_resource_paths_unrestricted": scope.Unrestricted}
		rows := make([]catalog.ResourceTypeSummary, 0, 16)
		if err := s.client.QueryRows(ctx, datasetSummariesAQL, opts.CursorBatch, vars, func(row map[string]any) error {
			resourceType := stringValue(row["resource_type"])
			if resourceType == "" {
				return fmt.Errorf("dataset summary returned an empty resource type for project %q", project)
			}
			documentCount, err := decodeInt64(row["document_count"])
			if err != nil {
				return fmt.Errorf("decode document_count for %s/%s: %w", project, resourceType, err)
			}
			populatedFields, err := decodeInt64(row["populated_field_count"])
			if err != nil {
				return fmt.Errorf("decode populated_field_count for %s/%s: %w", project, resourceType, err)
			}
			pivotCandidates, err := decodeInt64(row["pivot_candidate_count"])
			if err != nil {
				return fmt.Errorf("decode pivot_candidate_count for %s/%s: %w", project, resourceType, err)
			}
			rows = append(rows, catalog.ResourceTypeSummary{ResourceType: resourceType, DocumentCount: documentCount, PopulatedFieldCount: int(populatedFields), PivotCandidateCount: int(pivotCandidates)})
			return nil
		}); err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			sort.Slice(rows, func(i, j int) bool { return rows[i].ResourceType < rows[j].ResourceType })
			result = append(result, catalog.DatasetSummary{Project: project, DatasetGeneration: catalog.NormalizeDatasetGeneration(generationValue), State: state, ResourceTypes: rows})
		}
	}
	return result, nil
}

func (s *Store) DiscoverExistingAuthResourcePaths(ctx context.Context, opts catalog.AuthResourcePathOptions) ([]string, error) {
	if opts.CursorBatch <= 0 {
		opts.CursorBatch = 1000
	}
	result := make([]string, 0, 16)
	err := s.client.QueryRows(ctx, existingAuthResourcePathsAQL, opts.CursorBatch, map[string]any{"project": opts.Project, "dataset_generation": generation(opts.DatasetGeneration)}, func(row map[string]any) error {
		if value := stringValue(row["auth_resource_path"]); value != "" {
			result = append(result, value)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(result)
	return result, nil
}

func (s *Store) WriteFieldCatalog(ctx context.Context, collection string, docs []catalog.FieldCatalogDocument, batchSize int, overwrite bool, writeAPI string, timings map[string]float64) error {
	return catalog.WriteFieldCatalog(ctx, s.client, collection, docs, batchSize, overwrite, writeAPI, timings)
}
func (s *Store) WriteRelationshipCatalog(ctx context.Context, docs []catalog.RelationshipCatalogDocument, batchSize int, overwrite bool, writeAPI string, timings map[string]float64) error {
	return catalog.WriteRelationshipCatalog(ctx, s.client, docs, batchSize, overwrite, writeAPI, timings)
}
func (s *Store) AccumulateRelationshipCatalog(ctx context.Context, docs []catalog.RelationshipCatalogDocument, timings map[string]float64) error {
	return catalog.AccumulateRelationshipCatalog(ctx, s.client, docs, timings)
}

func (s *Store) RebuildRelationshipCatalog(ctx context.Context, opts catalog.RelationshipRebuildOptions) (catalog.RelationshipRebuildSummary, error) {
	if opts.CursorBatch <= 0 {
		opts.CursorBatch = 1000
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 1000
	}
	if opts.WriteAPI == "" {
		opts.WriteAPI = "import"
	}
	started := time.Now()
	if err := s.client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{
		Name: catalog.RelationshipCatalogCollection,
		Indexes: [][]string{
			{"project", "dataset_generation", "to_type"},
			{"project", "dataset_generation", "auth_resource_path", "to_type"},
			{"project", "dataset_generation", "from_type"},
			{"project", "dataset_generation", "auth_resource_path", "from_type"},
		},
	}}}); err != nil {
		return catalog.RelationshipRebuildSummary{}, err
	}
	vars := map[string]any{"project": opts.Project, "dataset_generation": generation(opts.DatasetGeneration), "auth_resource_paths": opts.AuthResourcePaths, "auth_resource_paths_unrestricted": catalog.EffectiveAuthResourcePathsUnrestricted(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted)}
	if err := s.client.QueryRows(ctx, relationshipClearAQL, opts.CursorBatch, vars, func(map[string]any) error { return nil }); err != nil {
		return catalog.RelationshipRebuildSummary{}, err
	}
	counts := map[catalog.RelationshipKey]int64{}
	if err := s.client.QueryRows(ctx, relationshipRebuildAQL, opts.CursorBatch, vars, func(row map[string]any) error {
		count, err := decodeInt64(row["edge_count"])
		if err != nil {
			return err
		}
		key := catalog.RelationshipKey{Project: stringValue(row["project"]), DatasetGeneration: stringValue(row["dataset_generation"]), AuthResourcePath: stringValue(row["auth_resource_path"]), FromType: stringValue(row["from_type"]), Label: stringValue(row["label"]), ToType: stringValue(row["to_type"])}
		counts[key] = count
		return nil
	}); err != nil {
		return catalog.RelationshipRebuildSummary{}, err
	}
	docs := catalog.RelationshipCatalogDocuments(counts)
	if err := s.WriteRelationshipCatalog(ctx, docs, opts.BatchSize, true, opts.WriteAPI, nil); err != nil {
		return catalog.RelationshipRebuildSummary{}, err
	}
	var total int64
	for _, count := range counts {
		total += count
	}
	return catalog.RelationshipRebuildSummary{Project: opts.Project, DatasetGeneration: catalog.NormalizeDatasetGeneration(opts.DatasetGeneration), Rows: len(docs), EdgeCount: total, Seconds: time.Since(started).Seconds()}, nil
}

func generation(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func normalizedProjects(projects []string) []string {
	seen := make(map[string]struct{}, len(projects))
	result := make([]string, 0, len(projects))
	for _, project := range projects {
		project = strings.TrimSpace(project)
		if project == "" {
			continue
		}
		if _, ok := seen[project]; ok {
			continue
		}
		seen[project] = struct{}{}
		result = append(result, project)
	}
	sort.Strings(result)
	return result
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
func decodeInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case int:
		return int64(typed), nil
	case int32:
		return int64(typed), nil
	case int64:
		return typed, nil
	case float64:
		return int64(typed), nil
	case float32:
		return int64(typed), nil
	case json.Number:
		n, err := typed.Int64()
		return n, err
	case string:
		n, err := strconv.ParseInt(typed, 10, 64)
		return n, err
	case nil:
		return 0, nil
	}
	return 0, fmt.Errorf("unsupported numeric type %T", value)
}

func decodeBool(value any) (bool, error) {
	if value == nil {
		return false, nil
	}
	result, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("unsupported boolean type %T", value)
	}
	return result, nil
}

func decodeStrings(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...), nil
	case []any:
		result := make([]string, len(values))
		for i, value := range values {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("unsupported slice item type %T at index %d", value, i)
			}
			result[i] = text
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported string slice type %T", value)
	}
}

func decode(row map[string]any, out *catalog.PopulatedField) error {
	out.Project = stringValue(row["project"])
	out.DatasetGeneration = stringValue(row["dataset_generation"])
	out.AuthResourcePath = stringValue(row["auth_resource_path"])
	out.ResourceType = stringValue(row["resource_type"])
	out.Path = stringValue(row["path"])
	out.Kind = stringValue(row["kind"])
	var err error
	if out.DocCount, err = decodeInt64(row["doc_count"]); err != nil {
		return fmt.Errorf("decode field row %s/%s doc_count: %w", out.ResourceType, out.Path, err)
	}
	var sampleCount int64
	if sampleCount, err = decodeInt64(row["sample_count"]); err != nil {
		return fmt.Errorf("decode field row %s/%s sample_count: %w", out.ResourceType, out.Path, err)
	}
	out.SampleCount = int(sampleCount)
	if out.DistinctTruncated, err = decodeBool(row["distinct_truncated"]); err != nil {
		return fmt.Errorf("decode field row %s/%s distinct_truncated: %w", out.ResourceType, out.Path, err)
	}
	if out.PivotCandidate, err = decodeBool(row["pivot_candidate"]); err != nil {
		return fmt.Errorf("decode field row %s/%s pivot_candidate: %w", out.ResourceType, out.Path, err)
	}
	if out.DistinctValues, err = decodeStrings(row["distinct_values"]); err != nil {
		return fmt.Errorf("decode field row %s/%s distinct_values: %w", out.ResourceType, out.Path, err)
	}
	if out.PivotColumns, err = decodeStrings(row["pivot_columns"]); err != nil {
		return fmt.Errorf("decode field row %s/%s pivot_columns: %w", out.ResourceType, out.Path, err)
	}
	if out.PivotValueSelectors, err = decodeStrings(row["pivot_value_selectors"]); err != nil {
		return fmt.Errorf("decode field row %s/%s pivot_value_selectors: %w", out.ResourceType, out.Path, err)
	}
	out.PivotKind = stringValue(row["pivot_kind"])
	out.PivotFamily = stringValue(row["pivot_family"])
	out.PivotColumnSelect = stringValue(row["pivot_column_selector"])
	out.PivotValueSelect = stringValue(row["pivot_value_selector"])
	out.PivotItemSource = stringValue(row["pivot_item_source"])
	out.PivotItemResourceType = stringValue(row["pivot_item_resource_type"])
	return nil
}

const populatedFieldsAQL = `
FOR d IN fhir_field_catalog
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths
  FILTER @resource_type == null OR d.resource_type == @resource_type
  FILTER @pivot_only == false OR d.pivot_candidate == true
  SORT d.resource_type, d.doc_count DESC, d.path
  RETURN {
    project: d.project,
    dataset_generation: d.dataset_generation,
    auth_resource_path: d.auth_resource_path,
    resource_type: d.resource_type,
    path: d.path,
    kind: d.kind,
    doc_count: d.doc_count,
    sample_count: d.sample_count,
    distinct_values: d.distinct_values,
    distinct_truncated: d.distinct_truncated,
    pivot_candidate: d.pivot_candidate,
    pivot_kind: d.pivot_kind,
    pivot_columns: d.pivot_columns,
    pivot_family: d.pivot_family,
    pivot_column_selector: d.pivot_column_selector,
    pivot_value_selector: d.pivot_value_selector,
    pivot_item_source: d.pivot_item_source,
    pivot_item_resource_type: d.pivot_item_resource_type,
    pivot_value_selectors: d.pivot_value_selectors
  }`
const relationshipCatalogStorageAQL = `
FOR d IN fhir_relationship_catalog
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths
  FILTER @from_type == null OR d.from_type == @from_type
  COLLECT from_type = d.from_type, label = d.label, to_type = d.to_type
    AGGREGATE edge_count = SUM(d.edge_count)
  SORT from_type, edge_count DESC, label, to_type
  RETURN { dataset_generation: @dataset_generation, from_type, label, to_type, edge_count }`
const relationshipCatalogBuilderAQL = `
FOR d IN fhir_relationship_catalog
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths
  FILTER @node_type == null OR d.to_type == @node_type
  COLLECT from_type = d.to_type, label = d.label, to_type = d.from_type
    AGGREGATE edge_count = SUM(d.edge_count)
  SORT from_type, edge_count DESC, label, to_type
  RETURN { dataset_generation: @dataset_generation, from_type, label, to_type, edge_count }`
const datasetSummariesAQL = `
FOR d IN fhir_field_catalog
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths
  COLLECT resource_type = d.resource_type, path = d.path
    AGGREGATE document_count = MAX(d.doc_count), pivot_candidate = MAX(d.pivot_candidate ? 1 : 0)
  COLLECT resource_type = resource_type
    AGGREGATE document_count = MAX(document_count), populated_field_count = COUNT(), pivot_candidate_count = SUM(pivot_candidate)
  SORT resource_type
  RETURN { resource_type, document_count, populated_field_count, pivot_candidate_count }`
const existingAuthResourcePathsAQL = `
FOR d IN fhir_field_catalog
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER d.auth_resource_path != null AND d.auth_resource_path != ""
  COLLECT auth_resource_path = d.auth_resource_path
  SORT auth_resource_path
  RETURN { auth_resource_path: auth_resource_path }`
const relationshipClearAQL = `
FOR d IN fhir_relationship_catalog
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths
  REMOVE d IN fhir_relationship_catalog
  RETURN 1`
const relationshipRebuildAQL = `
FOR e IN fhir_edge
  FILTER e.project == @project
  FILTER e.dataset_generation == @dataset_generation
  FILTER @auth_resource_paths_unrestricted == true OR e.auth_resource_path IN @auth_resource_paths
  COLLECT project = e.project, dataset_generation = e.dataset_generation, auth_resource_path = e.auth_resource_path,
    from_type = e.from_type, label = e.label, to_type = e.to_type WITH COUNT INTO edge_count
  RETURN { project, dataset_generation, auth_resource_path, from_type, label, to_type, edge_count }`
