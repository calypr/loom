// Package arango contains the Arango persistence adapter for catalog facts.
package arango

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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
	client   client
	database string
}

func New(client client, database string) (*Store, error) {
	if client == nil {
		return nil, fmt.Errorf("catalog Arango client is required")
	}
	return &Store{client: client, database: database}, nil
}

func (s *Store) DiscoverFields(ctx context.Context, opts catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
	if opts.CursorBatch <= 0 {
		opts.CursorBatch = 1000
	}
	vars := map[string]any{"project": opts.Project, "dataset_generation": generation(opts.DatasetGeneration), "resource_type": nil, "pivot_only": opts.PivotOnly, "auth_resource_paths": opts.AuthResourcePaths, "auth_resource_paths_unrestricted": catalog.EffectiveAuthResourcePathsUnrestricted(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted)}
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
	vars := map[string]any{"project": opts.Project, "dataset_generation": generation(opts.DatasetGeneration), "auth_resource_paths": opts.AuthResourcePaths, "auth_resource_paths_unrestricted": catalog.EffectiveAuthResourcePathsUnrestricted(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted)}
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
	rows := []catalog.PopulatedReference{}
	err := s.client.QueryRows(ctx, query, opts.CursorBatch, vars, func(row map[string]any) error {
		rows = append(rows, catalog.PopulatedReference{DatasetGeneration: stringValue(row["dataset_generation"]), FromType: stringValue(row["from_type"]), Label: stringValue(row["label"]), ToType: stringValue(row["to_type"]), EdgeCount: int64Value(row["edge_count"])})
		return nil
	})
	return rows, err
}

func (s *Store) DiscoverDatasets(ctx context.Context, opts catalog.DatasetSummaryOptions) ([]catalog.DatasetSummary, error) {
	if opts.CursorBatch <= 0 {
		opts.CursorBatch = 1000
	}
	var result []catalog.DatasetSummary
	for _, project := range opts.ProjectAllowlist {
		generationValue := opts.DatasetGenerationByProject[project]
		scope := opts.AuthScopesByProject[project]
		vars := map[string]any{"project": project, "dataset_generation": generation(generationValue), "auth_resource_paths": scope.AuthResourcePaths, "auth_resource_paths_unrestricted": scope.Unrestricted}
		var rows []catalog.ResourceTypeSummary
		if err := s.client.QueryRows(ctx, datasetSummariesAQL, opts.CursorBatch, vars, func(row map[string]any) error {
			rows = append(rows, catalog.ResourceTypeSummary{ResourceType: stringValue(row["resource_type"]), DocumentCount: int64Value(row["document_count"]), PopulatedFieldCount: int(int64Value(row["populated_field_count"])), PivotCandidateCount: int(int64Value(row["pivot_candidate_count"]))})
			return nil
		}); err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			result = append(result, catalog.DatasetSummary{Project: project, DatasetGeneration: catalog.NormalizeDatasetGeneration(generationValue), State: opts.DatasetStateByProject[project], ResourceTypes: rows})
		}
	}
	return result, nil
}

func (s *Store) DiscoverExistingAuthResourcePaths(ctx context.Context, opts catalog.AuthResourcePathOptions) ([]string, error) {
	if opts.CursorBatch <= 0 {
		opts.CursorBatch = 1000
	}
	result := []string{}
	err := s.client.QueryRows(ctx, existingAuthResourcePathsAQL, opts.CursorBatch, map[string]any{"project": opts.Project, "dataset_generation": generation(opts.DatasetGeneration)}, func(row map[string]any) error {
		if value := stringValue(row["auth_resource_path"]); value != "" {
			result = append(result, value)
		}
		return nil
	})
	return result, err
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
	started := time.Now()
	if err := s.client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: catalog.RelationshipCatalogCollection}}}); err != nil {
		return catalog.RelationshipRebuildSummary{}, err
	}
	vars := map[string]any{"project": opts.Project, "dataset_generation": generation(opts.DatasetGeneration), "auth_resource_paths": opts.AuthResourcePaths, "auth_resource_paths_unrestricted": catalog.EffectiveAuthResourcePathsUnrestricted(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted)}
	if err := s.client.QueryRows(ctx, relationshipClearAQL, opts.CursorBatch, vars, func(map[string]any) error { return nil }); err != nil {
		return catalog.RelationshipRebuildSummary{}, err
	}
	counts := map[catalog.RelationshipKey]int64{}
	if err := s.client.QueryRows(ctx, relationshipRebuildAQL, opts.CursorBatch, vars, func(row map[string]any) error {
		key := catalog.RelationshipKey{Project: stringValue(row["project"]), DatasetGeneration: stringValue(row["dataset_generation"]), AuthResourcePath: stringValue(row["auth_resource_path"]), FromType: stringValue(row["from_type"]), Label: stringValue(row["label"]), ToType: stringValue(row["to_type"])}
		counts[key] = int64Value(row["edge_count"])
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
func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
func int64Value(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		n, _ := typed.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(typed, 10, 64)
		return n
	}
	return 0
}
func decode(row map[string]any, out *catalog.PopulatedField) error {
	out.Project = stringValue(row["project"])
	out.DatasetGeneration = stringValue(row["dataset_generation"])
	out.AuthResourcePath = stringValue(row["auth_resource_path"])
	out.ResourceType = stringValue(row["resource_type"])
	out.Path = stringValue(row["path"])
	out.Kind = stringValue(row["kind"])
	out.DocCount = int64Value(row["doc_count"])
	out.SampleCount = int(int64Value(row["sample_count"]))
	out.DistinctTruncated = row["distinct_truncated"] == true
	out.PivotCandidate = row["pivot_candidate"] == true
	out.PivotKind = stringValue(row["pivot_kind"])
	out.PivotFamily = stringValue(row["pivot_family"])
	out.PivotColumnSelect = stringValue(row["pivot_column_selector"])
	out.PivotValueSelect = stringValue(row["pivot_value_selector"])
	return nil
}

const populatedFieldsAQL = `FOR d IN fhir_field_catalog FILTER d.project == @project FILTER d.dataset_generation == @dataset_generation FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths FILTER @resource_type == null OR d.resource_type == @resource_type FILTER @pivot_only == false OR d.pivot_candidate == true SORT d.resource_type, d.doc_count DESC, d.path RETURN d`
const relationshipCatalogStorageAQL = `FOR d IN fhir_relationship_catalog FILTER d.project == @project FILTER d.dataset_generation == @dataset_generation FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths FILTER @from_type == null OR d.from_type == @from_type COLLECT from_type = d.from_type, label = d.label, to_type = d.to_type AGGREGATE edge_count = SUM(d.edge_count) RETURN {dataset_generation:@dataset_generation,from_type,label,to_type,edge_count}`
const relationshipCatalogBuilderAQL = `FOR d IN fhir_relationship_catalog FILTER d.project == @project FILTER d.dataset_generation == @dataset_generation FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths FILTER @node_type == null OR d.to_type == @node_type COLLECT from_type = d.to_type, label = d.label, to_type = d.from_type AGGREGATE edge_count = SUM(d.edge_count) RETURN {dataset_generation:@dataset_generation,from_type,label,to_type,edge_count}`
const datasetSummariesAQL = `FOR d IN fhir_field_catalog FILTER d.project == @project FILTER d.dataset_generation == @dataset_generation FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths COLLECT resource_type = d.resource_type, path = d.path AGGREGATE document_count = MAX(d.doc_count), pivot_candidate = MAX(d.pivot_candidate ? 1 : 0) COLLECT resource_type = resource_type AGGREGATE document_count = MAX(document_count), populated_field_count = COUNT(), pivot_candidate_count = SUM(pivot_candidate) RETURN {resource_type,document_count,populated_field_count,pivot_candidate_count}`
const existingAuthResourcePathsAQL = `FOR d IN fhir_field_catalog FILTER d.project == @project FILTER d.dataset_generation == @dataset_generation FILTER d.auth_resource_path != null AND d.auth_resource_path != "" COLLECT auth_resource_path = d.auth_resource_path RETURN {auth_resource_path}`
const relationshipClearAQL = `FOR d IN fhir_relationship_catalog FILTER d.project == @project FILTER d.dataset_generation == @dataset_generation FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths REMOVE d IN fhir_relationship_catalog RETURN 1`
const relationshipRebuildAQL = `FOR e IN fhir_edge FILTER e.project == @project FILTER e.dataset_generation == @dataset_generation FILTER @auth_resource_paths_unrestricted == true OR e.auth_resource_path IN @auth_resource_paths COLLECT project=e.project,dataset_generation=e.dataset_generation,auth_resource_path=e.auth_resource_path,from_type=e.from_type,label=e.label,to_type=e.to_type WITH COUNT INTO edge_count RETURN {project,dataset_generation,auth_resource_path,from_type,label,to_type,edge_count}`
