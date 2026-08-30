package arango

import (
	"context"
	"sort"

	"github.com/calypr/loom/internal/catalog"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

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
