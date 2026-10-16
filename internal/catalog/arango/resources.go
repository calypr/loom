package arango

import (
	"context"
	"fmt"
	"sort"
	"strings"

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

// DiscoverResourceInventory reads the publish-last inventory projection.
// Field profiler rows are deliberately not consulted: a populated field is
// not evidence that a resource collection is a valid row root.
func (s *Store) DiscoverResourceInventory(ctx context.Context, opts catalog.ResourceInventoryOptions) (catalog.ResourceInventoryResult, error) {
	if opts.CursorBatch <= 0 {
		opts.CursorBatch = 1000
	}
	resourceTypes, diagnostics := evidenceResourceTypes(opts.ResourceTypes)
	result := catalog.ResourceInventoryResult{Values: make([]catalog.ResourceInventoryObservation, 0, len(resourceTypes)), Available: true, Complete: true, Diagnostics: diagnostics}
	datasetGeneration := catalog.NormalizeDatasetGeneration(opts.DatasetGeneration)
	collectionExists, err := s.client.CollectionExists(ctx, catalog.ResourceInventoryCollection)
	if err != nil {
		result.Available, result.Complete, result.Status = false, false, catalog.EvidenceUnavailable
		result.Diagnostics = append(result.Diagnostics, catalog.EvidenceDiagnostic{Code: "RESOURCE_INVENTORY_UNAVAILABLE", Message: err.Error()})
		return result, fmt.Errorf("check resource inventory collection: %w", err)
	}
	if !collectionExists {
		return unavailableResourceInventory(result, "resource inventory collection is missing"), fmt.Errorf("%w: collection is missing", ErrResourceInventoryBuildUnavailable)
	}
	build, found, err := s.readResourceInventoryBuild(ctx, opts.Project, datasetGeneration)
	if err != nil {
		result.Available, result.Complete, result.Status = false, false, catalog.EvidenceUnavailable
		result.Diagnostics = append(result.Diagnostics, catalog.EvidenceDiagnostic{Code: "RESOURCE_INVENTORY_UNAVAILABLE", Message: err.Error()})
		return result, err
	}
	if !found {
		return unavailableResourceInventory(result, "completed resource inventory build marker is missing"), fmt.Errorf("%w: marker is missing for %s/%s", ErrResourceInventoryBuildUnavailable, opts.Project, datasetGeneration)
	}
	coveredTypes := make(map[string]struct{}, len(build.ResourceTypes))
	for _, resourceType := range build.ResourceTypes {
		coveredTypes[resourceType] = struct{}{}
	}
	canonicalBuildTypes, typeDiagnostics := evidenceResourceTypes(build.ResourceTypes)
	typesAreCanonical := len(typeDiagnostics) == 0 && len(canonicalBuildTypes) == len(build.ResourceTypes)
	if typesAreCanonical {
		for i := range canonicalBuildTypes {
			if canonicalBuildTypes[i] != build.ResourceTypes[i] {
				typesAreCanonical = false
				break
			}
		}
	}
	coverageDigest, coverageErr := catalog.ResourceInventoryCoverageDigest(build.ResourceTypes)
	if build.Key != catalog.ResourceInventoryBuildKey(opts.Project, datasetGeneration) ||
		build.RecordType != catalog.ResourceInventoryBuildRecordType || build.Project != opts.Project ||
		catalog.NormalizeDatasetGeneration(build.DatasetGeneration) != datasetGeneration ||
		!strings.HasPrefix(build.BuildID, "sha256:") || len(build.BuildID) != len("sha256:")+64 ||
		build.CoverageDigest == "" || build.CoverageDigest != coverageDigest ||
		coverageErr != nil || !typesAreCanonical || build.ResourceTypeCount != len(build.ResourceTypes) ||
		build.CountRowCount < build.ResourceTypeCount || !build.Complete {
		return unavailableResourceInventory(result, "resource inventory build marker is stale or incomplete"), fmt.Errorf("%w: stale or incomplete marker for %s/%s", ErrResourceInventoryBuildUnavailable, opts.Project, datasetGeneration)
	}
	for _, resourceType := range resourceTypes {
		if _, ok := coveredTypes[resourceType]; !ok {
			return unavailableResourceInventory(result, "resource inventory build does not cover requested resource type "+resourceType), fmt.Errorf("%w: build does not cover %s", ErrResourceInventoryBuildUnavailable, resourceType)
		}
	}
	authPathsUnrestricted := catalog.EffectiveAuthResourcePathsUnrestricted(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted)
	if !authPathsUnrestricted && len(opts.AuthResourcePaths) == 0 {
		result.Digest, _ = catalog.ResourceInventoryDigest(result.Values)
		result.Status = evidenceStatus(result.Available, result.Complete, result.Truncated, len(result.Values))
		return result, nil
	}
	counts := make(map[string]int64, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		counts[resourceType] = 0
	}
	err = s.client.QueryRows(ctx, readResourceInventoryCountsAQL, opts.CursorBatch, map[string]interface{}{
		"project": opts.Project, "dataset_generation": datasetGeneration, "build_id": build.BuildID,
		"resource_types":                   resourceTypes,
		"auth_resource_paths":              append([]string(nil), opts.AuthResourcePaths...),
		"auth_resource_paths_unrestricted": authPathsUnrestricted,
	}, func(row map[string]any) error {
		resourceType := stringValue(row["resource_type"])
		if _, ok := counts[resourceType]; !ok {
			return fmt.Errorf("resource inventory count returned uncovered type %q", resourceType)
		}
		count, err := decodeInt64(row["document_count"])
		if err != nil {
			return fmt.Errorf("decode resource inventory count for %s: %w", resourceType, err)
		}
		if count < 0 {
			return fmt.Errorf("decode resource inventory count for %s: negative count", resourceType)
		}
		counts[resourceType] = count
		return nil
	})
	if err != nil {
		result.Available, result.Complete, result.Status = false, false, catalog.EvidenceUnavailable
		result.Diagnostics = append(result.Diagnostics, catalog.EvidenceDiagnostic{Code: "RESOURCE_INVENTORY_UNAVAILABLE", Message: err.Error()})
		return result, err
	}
	for _, resourceType := range resourceTypes {
		result.Values = append(result.Values, catalog.ResourceInventoryObservation{
			Project: opts.Project, DatasetGeneration: datasetGeneration, ResourceType: resourceType, DocumentCount: counts[resourceType],
		})
	}
	sort.Slice(result.Values, func(i, j int) bool {
		return inventoryObservationLess(result.Values[i], result.Values[j])
	})
	result.Digest, _ = catalog.ResourceInventoryDigest(result.Values)
	result.Status = evidenceStatus(result.Available, result.Complete, result.Truncated, len(result.Values))
	return result, nil
}

func unavailableResourceInventory(result catalog.ResourceInventoryResult, message string) catalog.ResourceInventoryResult {
	result.Values = []catalog.ResourceInventoryObservation{}
	result.Available, result.Complete, result.Status = false, false, catalog.EvidenceUnavailable
	result.Diagnostics = append(result.Diagnostics, catalog.EvidenceDiagnostic{Code: "RESOURCE_INVENTORY_UNAVAILABLE", Message: message})
	return result
}

const existingAuthResourcePathsAQL = `
FOR d IN fhir_field_catalog
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER d.auth_resource_path != null AND d.auth_resource_path != ""
  COLLECT auth_resource_path = d.auth_resource_path
  SORT auth_resource_path
  RETURN { auth_resource_path: auth_resource_path }`
