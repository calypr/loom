package arango

import (
	"context"
	"fmt"
	"sort"

	"github.com/calypr/loom/internal/catalog"
)

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
		collectionExists, err := s.client.CollectionExists(ctx, resourceType)
		if err != nil {
			err = fmt.Errorf("check resource collection %s: %w", resourceType, err)
			result.Available, result.Complete, result.Status = false, false, catalog.EvidenceUnavailable
			result.Diagnostics = append(result.Diagnostics, catalog.EvidenceDiagnostic{Code: "RESOURCE_INVENTORY_UNAVAILABLE", Message: err.Error(), ResourceType: resourceType})
			return result, err
		}
		if !collectionExists {
			result.Values = append(result.Values, catalog.ResourceInventoryObservation{
				Project: opts.Project, DatasetGeneration: catalog.NormalizeDatasetGeneration(opts.DatasetGeneration), ResourceType: resourceType, DocumentCount: 0,
			})
			continue
		}
		// Arango collection bind parameters use @@name in AQL and require the
		// corresponding bind-var map key to retain one leading @.
		vars["@resource_collection"] = resourceType
		vars["resource_type"] = resourceType
		var rowSeen bool
		err = s.client.QueryRows(ctx, resourceInventoryAQL, opts.CursorBatch, vars, func(row map[string]any) error {
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

const existingAuthResourcePathsAQL = `
FOR d IN fhir_field_catalog
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER d.auth_resource_path != null AND d.auth_resource_path != ""
  COLLECT auth_resource_path = d.auth_resource_path
  SORT auth_resource_path
  RETURN { auth_resource_path: auth_resource_path }`
