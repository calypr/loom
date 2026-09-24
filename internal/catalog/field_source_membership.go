package catalog

import (
	"fmt"
	"sort"
	"strings"
)

const (
	FieldSourceMembershipCollection      = "fhir_field_source_membership"
	FieldSourceMembershipBuildCollection = "fhir_field_source_membership_builds"
	FieldSourceMembershipSchemaVersion   = 2
)

type FieldSourceMembershipState string

const (
	FieldSourceMembershipBuilding FieldSourceMembershipState = "building"
	FieldSourceMembershipComplete FieldSourceMembershipState = "complete"
	FieldSourceMembershipFailed   FieldSourceMembershipState = "failed"
)

// FieldSourceMembership records which non-null scalar paths are present on
// one retained FHIR vertex. VertexID is the physical Arango document handle.
type FieldSourceMembership struct {
	Key               string                       `json:"_key"`
	Project           string                       `json:"project"`
	DatasetGeneration string                       `json:"dataset_generation"`
	AuthResourcePath  string                       `json:"auth_resource_path,omitempty"`
	ResourceType      string                       `json:"resource_type"`
	VertexID          string                       `json:"vertex_id"`
	ScalarPaths       []string                     `json:"scalar_paths"`
	SemanticFeatures  []SemanticInventoryReference `json:"semantic_features"`
}

// SemanticFeatureReferences keeps one feature identity per populated resource.
// Repeated occurrences remain in the semantic inventory, not this membership.
func SemanticFeatureReferences(contributions []SemanticInventoryContribution) []SemanticInventoryReference {
	seen := make(map[SemanticInventoryReference]struct{}, len(contributions))
	for _, contribution := range contributions {
		if contribution.ConceptID == "" || contribution.BindingID == "" {
			continue
		}
		seen[SemanticInventoryReference{ConceptID: contribution.ConceptID, BindingID: contribution.BindingID}] = struct{}{}
	}
	refs := make([]SemanticInventoryReference, 0, len(seen))
	for ref := range seen {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].ConceptID != refs[j].ConceptID {
			return refs[i].ConceptID < refs[j].ConceptID
		}
		return refs[i].BindingID < refs[j].BindingID
	})
	return refs
}

// FieldSourceMembershipBuild is an independent version/completeness marker
// for the derived membership sidecars. A partial build is never advertised as
// complete; its typed checkpoint advances only after sidecars are persisted.
type FieldSourceMembershipBuild struct {
	Key               string                          `json:"_key"`
	Project           string                          `json:"project"`
	DatasetGeneration string                          `json:"dataset_generation"`
	SchemaVersion     int                             `json:"schema_version"`
	State             FieldSourceMembershipState      `json:"state"`
	ScannedResources  int64                           `json:"scanned_resources"`
	Checkpoint        FieldSourceMembershipCheckpoint `json:"checkpoint"`
	Diagnostic        string                          `json:"diagnostic,omitempty"`
}

type FieldSourceMembershipCheckpoint struct {
	Collection string `json:"collection,omitempty"`
	Key        string `json:"key,omitempty"`
}

func NewFieldSourceMembershipBuild(project, datasetGeneration string) FieldSourceMembershipBuild {
	generation := NormalizeDatasetGeneration(datasetGeneration)
	key := "fsmb_" + catalogIdentityDigest(
		"field-source-membership-build/v1",
		project,
		generation,
		fmt.Sprint(FieldSourceMembershipSchemaVersion),
	)
	return FieldSourceMembershipBuild{
		Key:               key,
		Project:           project,
		DatasetGeneration: generation,
		SchemaVersion:     FieldSourceMembershipSchemaVersion,
		State:             FieldSourceMembershipBuilding,
	}
}

func NewFieldSourceMembership(project, datasetGeneration, authResourcePath, resourceType, vertexID string, payload map[string]any) (FieldSourceMembership, error) {
	return newFieldSourceMembership(project, datasetGeneration, authResourcePath, resourceType, vertexID, ScalarPathsPresent(payload))
}

func newFieldSourceMembership(project, datasetGeneration, authResourcePath, resourceType, vertexID string, scalarPaths []string) (FieldSourceMembership, error) {
	project = strings.TrimSpace(project)
	generation := NormalizeDatasetGeneration(datasetGeneration)
	resourceType = strings.TrimSpace(resourceType)
	vertexID = strings.TrimSpace(vertexID)
	if project == "" || generation == "" || resourceType == "" || !strings.HasPrefix(vertexID, resourceType+"/") || len(vertexID) == len(resourceType)+1 {
		return FieldSourceMembership{}, fmt.Errorf("field-source membership requires project, generation, resource type, and a physical vertex ID")
	}
	key := "fsm_" + catalogIdentityDigest(
		"field-source-membership/v1",
		project,
		generation,
		authResourcePath,
		resourceType,
		vertexID,
	)
	return FieldSourceMembership{
		Key:               key,
		Project:           project,
		DatasetGeneration: generation,
		AuthResourcePath:  authResourcePath,
		ResourceType:      resourceType,
		VertexID:          vertexID,
		ScalarPaths:       append([]string(nil), scalarPaths...),
		SemanticFeatures:  []SemanticInventoryReference{},
	}, nil
}

// ScalarPathsPresent uses the full canonical shape/accessor plan. It is
// deliberately independent of profiler field/value retention limits.
func ScalarPathsPresent(payload map[string]any) []string {
	if payload == nil {
		return []string{}
	}
	plan := buildShapePlan(payload)
	return scalarPathsPresentFromPlan(payload, plan)
}

func scalarPathsPresentFromPlan(payload map[string]any, plan *shapePlan) []string {
	if payload == nil || plan == nil {
		return []string{}
	}
	paths := make([]string, 0, len(plan.fields))
	for _, field := range plan.fields {
		if field.Kind != fieldKindScalar {
			continue
		}
		values, ok := extractAccessorValues(payload, field.Accessor)
		if !ok {
			continue
		}
		for _, value := range values {
			if _, scalar := scalarStringValue(value); scalar {
				paths = append(paths, field.Path)
				break
			}
		}
	}
	sort.Strings(paths)
	return paths
}
