package catalog

import (
	"fmt"
	"sort"
)

const (
	ResourceInventoryCountRecordType = "count"
	ResourceInventoryBuildRecordType = "build"
)

type resourceInventoryCountIdentity struct {
	Project           string `json:"project"`
	DatasetGeneration string `json:"dataset_generation"`
	AuthResourcePath  string `json:"auth_resource_path,omitempty"`
	ResourceType      string `json:"resource_type"`
	DocumentCount     int64  `json:"document_count"`
}

func ResourceInventoryBuildKey(project, datasetGeneration string) string {
	return "ribm_" + catalogIdentityDigest("resource-inventory-marker/v1", project, NormalizeDatasetGeneration(datasetGeneration))
}

func ResourceInventoryCoverageDigest(resourceTypes []string) (string, error) {
	types := append([]string(nil), resourceTypes...)
	sort.Strings(types)
	for i, resourceType := range types {
		if resourceType == "" || (i > 0 && types[i-1] == resourceType) {
			return "", fmt.Errorf("resource inventory coverage has an empty or duplicate resource type")
		}
	}
	return canonicalDigest(types)
}

// NewResourceInventoryBuild validates and canonicalizes one complete count
// projection, then assigns its immutable BuildID and document keys.
func NewResourceInventoryBuild(project, datasetGeneration string, resourceTypes []string, counts []ResourceInventoryCount) (ResourceInventoryBuild, []ResourceInventoryCount, error) {
	datasetGeneration = NormalizeDatasetGeneration(datasetGeneration)
	if project == "" || datasetGeneration == "" || len(resourceTypes) == 0 {
		return ResourceInventoryBuild{}, nil, fmt.Errorf("resource inventory build requires project, generation, and resource types")
	}
	types := append([]string(nil), resourceTypes...)
	sort.Strings(types)
	for i, resourceType := range types {
		if resourceType == "" || (i > 0 && types[i-1] == resourceType) {
			return ResourceInventoryBuild{}, nil, fmt.Errorf("resource inventory build has an empty or duplicate resource type")
		}
	}
	typeSet := make(map[string]struct{}, len(types))
	for _, resourceType := range types {
		typeSet[resourceType] = struct{}{}
	}
	canonicalCounts := append([]ResourceInventoryCount(nil), counts...)
	sort.Slice(canonicalCounts, func(i, j int) bool {
		left, right := canonicalCounts[i], canonicalCounts[j]
		if left.ResourceType != right.ResourceType {
			return left.ResourceType < right.ResourceType
		}
		return left.AuthResourcePath < right.AuthResourcePath
	})
	coveredTypes := make(map[string]struct{}, len(types))
	identities := make([]resourceInventoryCountIdentity, 0, len(canonicalCounts))
	for i, count := range canonicalCounts {
		if count.Project != project || NormalizeDatasetGeneration(count.DatasetGeneration) != datasetGeneration || count.ResourceType == "" || count.DocumentCount < 0 {
			return ResourceInventoryBuild{}, nil, fmt.Errorf("resource inventory count %d has invalid identity or document count", i)
		}
		if _, ok := typeSet[count.ResourceType]; !ok {
			return ResourceInventoryBuild{}, nil, fmt.Errorf("resource inventory count has uncovered resource type %q", count.ResourceType)
		}
		if i > 0 && canonicalCounts[i-1].ResourceType == count.ResourceType && canonicalCounts[i-1].AuthResourcePath == count.AuthResourcePath {
			return ResourceInventoryBuild{}, nil, fmt.Errorf("resource inventory has duplicate count scope for %q and auth path %q", count.ResourceType, count.AuthResourcePath)
		}
		coveredTypes[count.ResourceType] = struct{}{}
		identities = append(identities, resourceInventoryCountIdentity{
			Project: project, DatasetGeneration: datasetGeneration, AuthResourcePath: count.AuthResourcePath,
			ResourceType: count.ResourceType, DocumentCount: count.DocumentCount,
		})
	}
	for _, resourceType := range types {
		if _, ok := coveredTypes[resourceType]; !ok {
			return ResourceInventoryBuild{}, nil, fmt.Errorf("resource inventory has no count row for resource type %q", resourceType)
		}
	}
	coverageDigest, err := ResourceInventoryCoverageDigest(types)
	if err != nil {
		return ResourceInventoryBuild{}, nil, err
	}
	buildID, err := canonicalDigest(struct {
		SchemaVersion  string                           `json:"schema_version"`
		Project        string                           `json:"project"`
		Generation     string                           `json:"dataset_generation"`
		CoverageDigest string                           `json:"coverage_digest"`
		Counts         []resourceInventoryCountIdentity `json:"counts"`
	}{
		SchemaVersion: "resource-inventory-build/v1", Project: project, Generation: datasetGeneration,
		CoverageDigest: coverageDigest, Counts: identities,
	})
	if err != nil {
		return ResourceInventoryBuild{}, nil, err
	}
	for i := range canonicalCounts {
		count := &canonicalCounts[i]
		count.Key = "ric_" + catalogIdentityDigest("resource-inventory-count/v1", project, datasetGeneration, buildID, count.ResourceType, count.AuthResourcePath)
		count.RecordType = ResourceInventoryCountRecordType
		count.Project = project
		count.DatasetGeneration = datasetGeneration
		count.BuildID = buildID
	}
	build := ResourceInventoryBuild{
		Key: ResourceInventoryBuildKey(project, datasetGeneration), RecordType: ResourceInventoryBuildRecordType,
		Project: project, DatasetGeneration: datasetGeneration, BuildID: buildID,
		CoverageDigest: coverageDigest, ResourceTypes: types, ResourceTypeCount: len(types),
		CountRowCount: len(canonicalCounts), Complete: true,
	}
	return build, canonicalCounts, nil
}
