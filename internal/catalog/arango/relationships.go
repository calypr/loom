package arango

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/calypr/loom/internal/catalog"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	store "github.com/calypr/loom/internal/store/arango"
)

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
		fromType, fromOK := fhirschema.ConcreteResourceType(stringValue(row["from_type"]))
		toType, toOK := fhirschema.ConcreteResourceType(stringValue(row["to_type"]))
		if !fromOK || !toOK {
			return nil
		}
		if _, ok := fhirschema.LookupTraversal(fromType, stringValue(row["label"]), toType); !ok {
			return nil
		}
		count, err := decodeInt64(row["edge_count"])
		if err != nil {
			return fmt.Errorf("decode edge_count for %s/%s/%s: %w", fromType, stringValue(row["label"]), toType, err)
		}
		rows = append(rows, catalog.PopulatedReference{DatasetGeneration: stringValue(row["dataset_generation"]), FromType: fromType, Label: stringValue(row["label"]), ToType: toType, EdgeCount: count})
		return nil
	})
	return rows, err
}

func (s *Store) AuditRelationshipEdges(ctx context.Context, opts catalog.RelationshipAuditOptions) (catalog.RelationshipAuditSummary, error) {
	if opts.CursorBatch <= 0 {
		opts.CursorBatch = 1000
	}
	vars := map[string]any{
		"project":                          opts.Project,
		"dataset_generation":               generation(opts.DatasetGeneration),
		"auth_resource_paths":              append([]string(nil), opts.AuthResourcePaths...),
		"auth_resource_paths_unrestricted": catalog.EffectiveAuthResourcePathsUnrestricted(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted),
		"resource_types":                   fhirschema.ResourceTypes(),
	}
	summary := catalog.RelationshipAuditSummary{Project: opts.Project, DatasetGeneration: catalog.NormalizeDatasetGeneration(opts.DatasetGeneration)}
	err := s.client.QueryRows(ctx, relationshipAuditAQL, opts.CursorBatch, vars, func(row map[string]any) error {
		count, err := decodeInt64(row["edge_count"])
		if err != nil {
			return err
		}
		summary.InvalidEdgeCount += count
		summary.InvalidRelations = append(summary.InvalidRelations, catalog.InvalidRelationship{
			FromType:  stringValue(row["from_type"]),
			Label:     stringValue(row["label"]),
			ToType:    stringValue(row["to_type"]),
			EdgeCount: count,
		})
		return nil
	})
	if err != nil {
		return catalog.RelationshipAuditSummary{}, err
	}
	return summary, nil
}

// DiscoverRelationshipObservations returns both storage and Builder views of
// each aggregate. The catalog stores physical fhir_edge direction; Builder
// usually traverses its inverse, so dropping the storage provenance here is a
// correctness bug rather than a presentation detail.
func (s *Store) DiscoverRelationshipObservations(ctx context.Context, opts catalog.RelationshipObservationOptions) (catalog.RelationshipObservationResult, error) {
	if opts.CursorBatch <= 0 {
		opts.CursorBatch = 1000
	}
	resourceTypes, diagnostics := evidenceResourceTypes(opts.ResourceTypes)
	result := catalog.RelationshipObservationResult{Values: []catalog.RelationshipObservation{}, Available: true, Complete: true, Diagnostics: diagnostics}
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
	counts := make(map[relationshipObservationKey]int64)
	var queryErr error
	err := s.client.QueryRows(ctx, relationshipObservationsAQL, opts.CursorBatch, vars, func(row map[string]any) error {
		storageFrom := stringValue(row["storage_from_type"])
		label := stringValue(row["label"])
		storageTo := stringValue(row["storage_to_type"])
		count, err := decodeInt64(row["edge_count"])
		if err != nil {
			return fmt.Errorf("decode relationship count for %s/%s/%s: %w", storageFrom, label, storageTo, err)
		}
		from, fromOK := fhirschema.ConcreteResourceType(storageFrom)
		to, toOK := fhirschema.ConcreteResourceType(storageTo)
		if !fromOK || !toOK {
			result.Diagnostics = append(result.Diagnostics, catalog.EvidenceDiagnostic{Code: "INVALID_RELATIONSHIP_ENDPOINT", Message: "relationship endpoint is not a concrete generated FHIR resource", StorageFromType: storageFrom, Label: label, StorageToType: storageTo, Count: count})
			return nil
		}
		observation, ok, diagnostic := relationshipObservation(stringValue(row["project"]), stringValue(row["dataset_generation"]), stringValue(row["auth_resource_path"]), from, label, to, count)
		if !ok {
			result.Diagnostics = append(result.Diagnostics, diagnostic)
			return nil
		}
		key := relationshipObservationKey{project: observation.Project, generation: observation.DatasetGeneration, auth: observation.AuthResourcePath, storageFrom: observation.StorageFromType, label: observation.Label, storageTo: observation.StorageToType, builderFrom: observation.BuilderFromType, builderTo: observation.BuilderToType, builderDirection: observation.BuilderDirection}
		counts[key] += observation.EdgeCount
		return nil
	})
	queryErr = err
	if queryErr != nil {
		result.Available, result.Complete, result.Status = false, false, catalog.EvidenceUnavailable
		result.Diagnostics = append(result.Diagnostics, catalog.EvidenceDiagnostic{Code: "RELATIONSHIP_EVIDENCE_UNAVAILABLE", Message: queryErr.Error()})
		return result, queryErr
	}
	for key, count := range counts {
		result.Values = append(result.Values, catalog.RelationshipObservation{Project: key.project, DatasetGeneration: key.generation, AuthResourcePath: key.auth, FromType: key.builderFrom, Label: key.label, ToType: key.builderTo, EdgeCount: count, StorageFromType: key.storageFrom, StorageToType: key.storageTo, StorageDirection: "OUTBOUND", BuilderFromType: key.builderFrom, BuilderToType: key.builderTo, BuilderDirection: key.builderDirection})
	}
	sort.Slice(result.Values, func(i, j int) bool { return relationshipLess(result.Values[i], result.Values[j]) })
	result.Digest, _ = catalog.RelationshipObservationDigest(result.Values)
	result.Status = evidenceStatus(result.Available, result.Complete, result.Truncated, len(result.Values))
	return result, nil
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
	audit, err := s.AuditRelationshipEdges(ctx, catalog.RelationshipAuditOptions{Project: opts.Project, DatasetGeneration: opts.DatasetGeneration, AuthResourcePaths: opts.AuthResourcePaths, AuthResourcePathsUnrestricted: opts.AuthResourcePathsUnrestricted, CursorBatch: opts.CursorBatch})
	if err != nil {
		return catalog.RelationshipRebuildSummary{}, err
	}
	vars := map[string]any{"project": opts.Project, "dataset_generation": generation(opts.DatasetGeneration), "auth_resource_paths": opts.AuthResourcePaths, "auth_resource_paths_unrestricted": catalog.EffectiveAuthResourcePathsUnrestricted(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted), "resource_types": fhirschema.ResourceTypes()}
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
	return catalog.RelationshipRebuildSummary{Project: opts.Project, DatasetGeneration: catalog.NormalizeDatasetGeneration(opts.DatasetGeneration), Rows: len(docs), EdgeCount: total, InvalidEdgeCount: audit.InvalidEdgeCount, Seconds: time.Since(started).Seconds()}, nil
}

const relationshipObservationsAQL = `
FOR d IN fhir_relationship_catalog
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths
  FILTER @resource_types == null OR (d.from_type IN @resource_types AND d.to_type IN @resource_types)
  COLLECT project = d.project, dataset_generation = d.dataset_generation, auth_resource_path = d.auth_resource_path,
    storage_from_type = d.from_type, label = d.label, storage_to_type = d.to_type
    AGGREGATE edge_count = SUM(d.edge_count)
  SORT project, dataset_generation, auth_resource_path, storage_from_type, label, storage_to_type, edge_count
  RETURN { project, dataset_generation, auth_resource_path, storage_from_type, label, storage_to_type, edge_count }`

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
  FILTER e.from_type IN @resource_types AND e.to_type IN @resource_types
  COLLECT project = e.project, dataset_generation = e.dataset_generation, auth_resource_path = e.auth_resource_path,
    from_type = e.from_type, label = e.label, to_type = e.to_type WITH COUNT INTO edge_count
  RETURN { project, dataset_generation, auth_resource_path, from_type, label, to_type, edge_count }`
const relationshipAuditAQL = `
FOR e IN fhir_edge
  FILTER e.project == @project
  FILTER e.dataset_generation == @dataset_generation
  FILTER @auth_resource_paths_unrestricted == true OR e.auth_resource_path IN @auth_resource_paths
  FILTER e.from_type NOT IN @resource_types OR e.to_type NOT IN @resource_types
  COLLECT from_type = e.from_type, label = e.label, to_type = e.to_type WITH COUNT INTO edge_count
  SORT edge_count DESC, from_type, label, to_type
  RETURN { from_type, label, to_type, edge_count }`
