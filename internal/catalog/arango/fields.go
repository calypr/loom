package arango

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/bytedance/sonic"
	"github.com/calypr/loom/internal/catalog"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

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
		resourceType, ok := fhirschema.ConcreteResourceType(out.ResourceType)
		if !ok {
			return nil
		}
		out.ResourceType = resourceType
		rows = append(rows, out)
		return nil
	})
	return rows, err
}

// DiscoverFieldEnrichment reads profiler rows as optional, fail-closed
// enrichment. A successful empty result is valid evidence; a query failure or
// bounded distinct-value set is explicitly unavailable/incomplete.
func (s *Store) DiscoverFieldEnrichment(ctx context.Context, opts catalog.FieldEnrichmentOptions) (catalog.FieldEnrichmentResult, error) {
	if opts.CursorBatch <= 0 {
		opts.CursorBatch = 1000
	}
	resourceTypes, diagnostics := evidenceResourceTypes(opts.ResourceTypes)
	result := catalog.FieldEnrichmentResult{Values: []catalog.FieldEnrichmentObservation{}, Available: true, Complete: true, Diagnostics: diagnostics}
	vars := map[string]any{
		"project":                          opts.Project,
		"dataset_generation":               generation(opts.DatasetGeneration),
		"auth_resource_paths":              append([]string(nil), opts.AuthResourcePaths...),
		"auth_resource_paths_unrestricted": catalog.EffectiveAuthResourcePathsUnrestricted(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted),
	}
	if len(opts.ResourceTypes) > 0 {
		vars["resource_types"] = resourceTypes
	} else {
		vars["resource_types"] = nil
	}
	err := s.client.QueryRows(ctx, fieldEnrichmentAQL, opts.CursorBatch, vars, func(row map[string]any) error {
		var field catalog.PopulatedField
		if err := decode(row, &field); err != nil {
			return err
		}
		resourceType, ok := fhirschema.ConcreteResourceType(field.ResourceType)
		if !ok {
			result.Diagnostics = append(result.Diagnostics, catalog.EvidenceDiagnostic{Code: "INVALID_FIELD_RESOURCE_TYPE", Message: "field enrichment resource type is not concrete/generated", ResourceType: field.ResourceType})
			return nil
		}
		field.ResourceType = resourceType
		result.Values = append(result.Values, catalog.FieldEnrichmentObservation{Project: field.Project, DatasetGeneration: field.DatasetGeneration, AuthResourcePath: field.AuthResourcePath, ResourceType: field.ResourceType, Path: field.Path, Kind: field.Kind, DocCount: field.DocCount, SampleCount: field.SampleCount, DistinctValues: append([]string(nil), field.DistinctValues...), DistinctTruncated: field.DistinctTruncated, ExtensionValues: append([]catalog.ExtensionValueObservation(nil), field.ExtensionValues...), PivotCandidate: field.PivotCandidate, PivotKind: field.PivotKind, PivotColumns: append([]string(nil), field.PivotColumns...), PivotFamily: field.PivotFamily, PivotColumnSelect: field.PivotColumnSelect, PivotValueSelect: field.PivotValueSelect, PivotItemSource: field.PivotItemSource, PivotItemResourceType: field.PivotItemResourceType, PivotValueSelectors: append([]string(nil), field.PivotValueSelectors...)})
		// DistinctTruncated is per-field suggestion metadata. It is retained
		// on the observation but does not make the complete enrichment artifact
		// unusable; only an artifact-level marker can fail the snapshot.
		if truncated, ok := row["enrichment_truncated"].(bool); ok && truncated {
			result.Truncated = true
		}
		if complete, ok := row["enrichment_complete"].(bool); ok && !complete {
			result.Complete = false
		}
		return nil
	})
	if err != nil {
		result.Available, result.Complete, result.Status = false, false, catalog.EvidenceUnavailable
		result.Diagnostics = append(result.Diagnostics, catalog.EvidenceDiagnostic{Code: "FIELD_ENRICHMENT_UNAVAILABLE", Message: err.Error()})
		return result, err
	}
	if result.Truncated {
		result.Complete = false
	}
	sort.Slice(result.Values, func(i, j int) bool { return fieldLess(result.Values[i], result.Values[j]) })
	result.Digest, _ = catalog.FieldEnrichmentDigest(result.Values)
	result.Status = evidenceStatus(result.Available, result.Complete, result.Truncated, len(result.Values))
	return result, nil
}

func (s *Store) WriteFieldCatalog(ctx context.Context, collection string, docs []catalog.FieldCatalogDocument, batchSize int, overwrite bool, writeAPI string, timings map[string]float64) error {
	if len(docs) == 0 {
		return nil
	}
	if batchSize <= 0 {
		batchSize = 1000
	}
	started := time.Now()
	raw := make([]json.RawMessage, 0, len(docs))
	for _, doc := range docs {
		encoded, err := sonic.ConfigFastest.Marshal(&doc)
		if err != nil {
			return err
		}
		raw = append(raw, encoded)
	}
	if timings != nil {
		timings["field_catalog_marshal"] += time.Since(started).Seconds()
	}
	for i := 0; i < len(raw); i += batchSize {
		end := min(i+batchSize, len(raw))
		inserted := time.Now()
		if err := s.client.InsertBatchRaw(ctx, collection, raw[i:end], overwrite, writeAPI); err != nil {
			return err
		}
		if timings != nil {
			timings["field_catalog_insert"] += time.Since(inserted).Seconds()
		}
	}
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
    extension_values: d.extension_values,
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

const fieldEnrichmentAQL = `
FOR d IN fhir_field_catalog
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths
  FILTER @resource_types == null OR d.resource_type IN @resource_types
  SORT d.resource_type, d.path, d.auth_resource_path, d.doc_count
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
    extension_values: d.extension_values,
    pivot_candidate: d.pivot_candidate,
    pivot_kind: d.pivot_kind,
    pivot_columns: d.pivot_columns,
    pivot_family: d.pivot_family,
    pivot_column_selector: d.pivot_column_selector,
    pivot_value_selector: d.pivot_value_selector,
    pivot_item_source: d.pivot_item_source,
    pivot_item_resource_type: d.pivot_item_resource_type,
    pivot_value_selectors: d.pivot_value_selectors,
    enrichment_complete: d.enrichment_complete,
    enrichment_truncated: d.enrichment_truncated
  }`
