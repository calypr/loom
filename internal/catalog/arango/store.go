// Package arango contains the Arango persistence adapter for catalog facts.
package arango

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/calypr/loom/internal/catalog"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
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

// DiscoverResourceInventory reads the concrete resource collections directly.
// Field profiler rows are deliberately not consulted: a populated field is
// not evidence that a resource collection is a valid row root.
func (s *Store) DiscoverResourceInventory(ctx context.Context, opts catalog.ResourceInventoryOptions) (catalog.ResourceInventoryResult, error) {
	if opts.CursorBatch <= 0 {
		opts.CursorBatch = 1000
	}
	resourceTypes, diagnostics := evidenceResourceTypes(opts.ResourceTypes)
	result := catalog.ResourceInventoryResult{Values: make([]catalog.ResourceInventoryObservation, 0, len(resourceTypes)), Available: true, Complete: true, Diagnostics: diagnostics}
	vars := map[string]any{
		"project":                          opts.Project,
		"dataset_generation":               generation(opts.DatasetGeneration),
		"auth_resource_paths":              append([]string(nil), opts.AuthResourcePaths...),
		"auth_resource_paths_unrestricted": catalog.EffectiveAuthResourcePathsUnrestricted(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted),
	}
	for _, resourceType := range resourceTypes {
		vars["resource_collection"] = resourceType
		vars["resource_type"] = resourceType
		var rowSeen bool
		err := s.client.QueryRows(ctx, resourceInventoryAQL, opts.CursorBatch, vars, func(row map[string]any) error {
			rowSeen = true
			count, err := decodeInt64(row["document_count"])
			if err != nil {
				return fmt.Errorf("decode inventory count for %s: %w", resourceType, err)
			}
			result.Values = append(result.Values, catalog.ResourceInventoryObservation{
				Project: stringValue(row["project"]), DatasetGeneration: stringValue(row["dataset_generation"]), AuthResourcePath: stringValue(row["auth_resource_path"]), ResourceType: resourceType, DocumentCount: count,
			})
			return nil
		})
		if err != nil {
			result.Available, result.Complete, result.Status = false, false, catalog.EvidenceUnavailable
			result.Diagnostics = append(result.Diagnostics, catalog.EvidenceDiagnostic{Code: "RESOURCE_INVENTORY_UNAVAILABLE", Message: err.Error(), ResourceType: resourceType})
			return result, err
		}
		// Arango's COUNT query always returns one row. A defensive fallback
		// preserves a valid zero count when a compatible client omits it.
		if !rowSeen {
			result.Values = append(result.Values, catalog.ResourceInventoryObservation{Project: opts.Project, DatasetGeneration: catalog.NormalizeDatasetGeneration(opts.DatasetGeneration), ResourceType: resourceType, DocumentCount: 0})
		}
	}
	sort.Slice(result.Values, func(i, j int) bool {
		return inventoryObservationLess(result.Values[i], result.Values[j])
	})
	result.Digest, _ = catalog.ResourceInventoryDigest(result.Values)
	result.Status = evidenceStatus(result.Available, result.Complete, result.Truncated, len(result.Values))
	return result, nil
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

// Read* aliases make the capability boundary explicit while retaining the
// Discover* naming used by the pre-existing catalog reader.
func (s *Store) ReadResourceInventory(ctx context.Context, opts catalog.ResourceInventoryOptions) (catalog.ResourceInventoryResult, error) {
	return s.DiscoverResourceInventory(ctx, opts)
}
func (s *Store) ReadRelationshipObservations(ctx context.Context, opts catalog.RelationshipObservationOptions) (catalog.RelationshipObservationResult, error) {
	return s.DiscoverRelationshipObservations(ctx, opts)
}
func (s *Store) ReadFieldEnrichment(ctx context.Context, opts catalog.FieldEnrichmentOptions) (catalog.FieldEnrichmentResult, error) {
	return s.DiscoverFieldEnrichment(ctx, opts)
}

type relationshipObservationKey struct {
	project, generation, auth                string
	storageFrom, label, storageTo            string
	builderFrom, builderTo, builderDirection string
}

func evidenceResourceTypes(requested []string) ([]string, []catalog.EvidenceDiagnostic) {
	if len(requested) == 0 {
		return fhirschema.ResourceTypes(), nil
	}
	seen := make(map[string]struct{}, len(requested))
	diagnostics := make([]catalog.EvidenceDiagnostic, 0)
	for _, requestedType := range requested {
		canonical, ok := fhirschema.ConcreteResourceType(requestedType)
		if !ok {
			diagnostics = append(diagnostics, catalog.EvidenceDiagnostic{Code: "INVALID_RESOURCE_TYPE", Message: "requested resource type is not a concrete generated FHIR resource", ResourceType: requestedType})
			continue
		}
		seen[canonical] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for resourceType := range seen {
		result = append(result, resourceType)
	}
	sort.Strings(result)
	return result, diagnostics
}

func inventoryObservationLess(a, b catalog.ResourceInventoryObservation) bool {
	for _, pair := range [][2]string{{a.Project, b.Project}, {a.DatasetGeneration, b.DatasetGeneration}, {a.AuthResourcePath, b.AuthResourcePath}, {a.ResourceType, b.ResourceType}} {
		if pair[0] != pair[1] {
			return pair[0] < pair[1]
		}
	}
	return a.DocumentCount < b.DocumentCount
}

func relationshipLess(a, b catalog.RelationshipObservation) bool {
	for _, pair := range [][2]string{{a.Project, b.Project}, {a.DatasetGeneration, b.DatasetGeneration}, {a.AuthResourcePath, b.AuthResourcePath}, {a.FromType, b.FromType}, {a.Label, b.Label}, {a.ToType, b.ToType}, {a.StorageFromType, b.StorageFromType}, {a.StorageToType, b.StorageToType}} {
		if pair[0] != pair[1] {
			return pair[0] < pair[1]
		}
	}
	return a.EdgeCount < b.EdgeCount
}

func fieldLess(a, b catalog.FieldEnrichmentObservation) bool {
	for _, pair := range [][2]string{{a.Project, b.Project}, {a.DatasetGeneration, b.DatasetGeneration}, {a.AuthResourcePath, b.AuthResourcePath}, {a.ResourceType, b.ResourceType}, {a.Path, b.Path}, {a.Kind, b.Kind}} {
		if pair[0] != pair[1] {
			return pair[0] < pair[1]
		}
	}
	return a.DocCount < b.DocCount
}

func relationshipObservation(project, datasetGeneration, auth, storageFrom, label, storageTo string, count int64) (catalog.RelationshipObservation, bool, catalog.EvidenceDiagnostic) {
	diagnostic := catalog.EvidenceDiagnostic{StorageFromType: storageFrom, Label: label, StorageToType: storageTo, Count: count}
	if count <= 0 {
		diagnostic.Code = "INVALID_RELATIONSHIP_COUNT"
		diagnostic.Message = "relationship evidence count must be positive"
		return catalog.RelationshipObservation{}, false, diagnostic
	}
	// Stored fhir_edge is always _from -> _to. Builder's normal projection is
	// the inverse generated traversal, which is physically INBOUND from the
	// stored target. Keep a direct generated traversal as a valid fallback for
	// labels whose generated schema only exposes the storage orientation.
	if _, found, err := fhirschema.ResolveCompilerTraversal(storageTo, label, storageFrom); found && err == nil {
		return catalog.RelationshipObservation{Project: project, DatasetGeneration: datasetGeneration, AuthResourcePath: auth, FromType: storageTo, Label: label, ToType: storageFrom, EdgeCount: count, StorageFromType: storageFrom, StorageToType: storageTo, StorageDirection: "OUTBOUND", BuilderFromType: storageTo, BuilderToType: storageFrom, BuilderDirection: "INBOUND"}, true, diagnostic
	}
	if _, found, err := fhirschema.ResolveCompilerTraversal(storageFrom, label, storageTo); found && err == nil {
		return catalog.RelationshipObservation{Project: project, DatasetGeneration: datasetGeneration, AuthResourcePath: auth, FromType: storageFrom, Label: label, ToType: storageTo, EdgeCount: count, StorageFromType: storageFrom, StorageToType: storageTo, StorageDirection: "OUTBOUND", BuilderFromType: storageFrom, BuilderToType: storageTo, BuilderDirection: "OUTBOUND"}, true, diagnostic
	}
	diagnostic.Code = "UNSUPPORTED_GENERATED_TRAVERSAL"
	diagnostic.Message = "relationship endpoints and label are not a supported generated traversal"
	return catalog.RelationshipObservation{}, false, diagnostic
}

func evidenceStatus(available, complete, truncated bool, count int) catalog.EvidenceStatus {
	if !available {
		return catalog.EvidenceUnavailable
	}
	if truncated {
		return catalog.EvidenceTruncated
	}
	if !complete {
		return catalog.EvidenceIncomplete
	}
	if count == 0 {
		return catalog.EvidenceEmpty
	}
	return catalog.EvidenceAvailable
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

func generation(value string) any {
	value = catalog.NormalizeDatasetGeneration(value)
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

func decodeExtensionValues(value any) ([]catalog.ExtensionValueObservation, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		if typed, ok := value.([]map[string]any); ok {
			items = make([]any, len(typed))
			for i := range typed {
				items[i] = typed[i]
			}
		} else {
			return nil, fmt.Errorf("unsupported extension observation slice type %T", value)
		}
	}
	result := make([]catalog.ExtensionValueObservation, 0, len(items))
	for i, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unsupported extension observation type %T at index %d", item, i)
		}
		urlPath, err := decodeStrings(row["url_path"])
		if err != nil {
			return nil, fmt.Errorf("extension observation URL path: %w", err)
		}
		result = append(result, catalog.ExtensionValueObservation{URL: stringValue(row["url"]), SourcePath: stringValue(row["source_path"]), ValuePath: stringValue(row["value_path"]), ValueType: stringValue(row["value_type"]), URLPath: urlPath})
	}
	return result, nil
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
	if out.ExtensionValues, err = decodeExtensionValues(row["extension_values"]); err != nil {
		return fmt.Errorf("decode field row %s/%s extension_values: %w", out.ResourceType, out.Path, err)
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

// resourceInventoryAQL is executed once per concrete generated resource
// collection because Arango collection bind parameters cannot be safely
// interpolated from a caller-supplied type. Every identity predicate remains
// a bind variable, including the explicit restricted-empty authorization mode.
const resourceInventoryAQL = `
FOR d IN @@resource_collection
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths
  COLLECT WITH COUNT INTO document_count
  RETURN {
    project: @project,
    dataset_generation: @dataset_generation,
    resource_type: @resource_type,
    document_count
  }`

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
