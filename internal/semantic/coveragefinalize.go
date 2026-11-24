package semantic

import (
	"slices"
	"sort"
)

type ResourceFinalizationInput struct {
	ResourceType               string
	Found                      bool
	State                      SemanticCoverageBuildState
	SourceEnumerationComplete  bool
	SourceSetDigest            string
	ExpectedSourceCount        int64
	MaterializedSourceCount    int64
	SourcePartitionCount       int64
	MaterializedPartitionCount int64
	StoredPartitionCount       int64
	SummaryCount               int64
	Reason                     string
	IncompletePartitionReason  string
}

type BuildFinalization struct {
	State               SemanticCoverageBuildState
	Reason              string
	ExpectedSources     int64
	MaterializedSources int64
	SummaryCount        int64
}

func CoverageFinalizeBuild(resources []ResourceFinalizationInput) BuildFinalization {
	ordered := slices.Clone(resources)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].ResourceType < ordered[j].ResourceType
	})
	result := BuildFinalization{State: SemanticCoverageBuildComplete}
	for _, resource := range ordered {
		if !resource.Found {
			markPartial(&result, "RESOURCE_CERTIFICATES_INCOMPLETE")
			continue
		}
		if resource.State != SemanticCoverageBuildComplete || !resource.SourceEnumerationComplete ||
			resource.SourceSetDigest == "" || resource.ExpectedSourceCount != resource.MaterializedSourceCount ||
			resource.SourcePartitionCount != resource.MaterializedPartitionCount {
			reason := resource.Reason
			if reason == "" {
				reason = "RESOURCE_CERTIFICATES_INCOMPLETE"
			}
			markPartial(&result, reason)
			continue
		}
		result.ExpectedSources += resource.ExpectedSourceCount
		result.MaterializedSources += resource.MaterializedSourceCount
		result.SummaryCount += resource.SummaryCount
		if resource.StoredPartitionCount != resource.MaterializedPartitionCount ||
			resource.StoredPartitionCount != resource.SourcePartitionCount {
			markPartial(&result, "RESOURCE_PARTITION_SET_MISMATCH")
		}
		if resource.IncompletePartitionReason != "" {
			markPartial(&result, resource.IncompletePartitionReason)
		}
	}
	return result
}

func markPartial(result *BuildFinalization, reason string) {
	result.State = SemanticCoverageBuildPartial
	if result.Reason == "" {
		result.Reason = reason
	}
}
