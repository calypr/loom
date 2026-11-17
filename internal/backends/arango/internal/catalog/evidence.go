package arango

import (
	"context"
	"sort"

	"github.com/calypr/loom/internal/catalog"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func (s *Store) ReadResourceInventory(ctx context.Context, opts catalog.ResourceInventoryOptions) (catalog.ResourceInventoryResult, error) {
	return s.DiscoverResourceInventory(ctx, opts)
}
func (s *Store) ReadRelationshipObservations(ctx context.Context, opts catalog.RelationshipObservationOptions) (catalog.RelationshipObservationResult, error) {
	return s.DiscoverRelationshipObservations(ctx, opts)
}
func (s *Store) ReadFieldEnrichment(ctx context.Context, opts catalog.FieldEnrichmentOptions) (catalog.FieldEnrichmentResult, error) {
	return s.DiscoverFieldEnrichment(ctx, opts)
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

func fieldLess(a, b catalog.FieldEnrichmentObservation) bool {
	for _, pair := range [][2]string{{a.Project, b.Project}, {a.DatasetGeneration, b.DatasetGeneration}, {a.AuthResourcePath, b.AuthResourcePath}, {a.ResourceType, b.ResourceType}, {a.Path, b.Path}, {a.Kind, b.Kind}} {
		if pair[0] != pair[1] {
			return pair[0] < pair[1]
		}
	}
	return a.DocCount < b.DocCount
}
