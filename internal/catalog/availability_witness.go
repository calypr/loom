package catalog

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	AvailableColumnWitnessCollection      = "fhir_available_column_witnesses"
	AvailableColumnWitnessBuildCollection = "fhir_available_column_witness_builds"
	AvailableColumnWitnessSchemaVersion   = 1
)

type AvailableColumnWitnessState string

const (
	AvailableColumnWitnessBuilding AvailableColumnWitnessState = "BUILDING"
	AvailableColumnWitnessComplete AvailableColumnWitnessState = "COMPLETE"
	AvailableColumnWitnessFailed   AvailableColumnWitnessState = "FAILED"
)

// AvailableColumnWitnessBuild is the completeness marker for one root cohort.
// InputDigest identifies the source and route rules used to produce its rows.
type AvailableColumnWitnessBuild struct {
	Key               string                      `json:"_key"`
	Project           string                      `json:"project"`
	DatasetGeneration string                      `json:"dataset_generation"`
	RootResourceType  string                      `json:"root_resource_type"`
	InputDigest       string                      `json:"input_digest"`
	SchemaVersion     int                         `json:"schema_version"`
	AllRoots          bool                        `json:"all_roots"`
	Unrestricted      bool                        `json:"unrestricted"`
	State             AvailableColumnWitnessState `json:"state"`
	Exhaustive        bool                        `json:"exhaustive"`
	Diagnostic        string                      `json:"diagnostic,omitempty"`
}

// AvailableColumnWitness stores one source-to-root proof for a retained generation.
type AvailableColumnWitness struct {
	Key                string              `json:"_key"`
	Project            string              `json:"project"`
	DatasetGeneration  string              `json:"dataset_generation"`
	RootResourceType   string              `json:"root_resource_type"`
	InputDigest        string              `json:"input_digest"`
	SchemaVersion      int                 `json:"schema_version"`
	SourceResourceType string              `json:"source_resource_type"`
	Source             RouteCoverageSource `json:"source"`
	Route              []RouteCoverageStep `json:"route"`
	SourceWitnessID    string              `json:"source_witness_id"`
	RootWitnessID      string              `json:"root_witness_id"`
}

func NewAvailableColumnWitnessBuild(project, generation, rootResourceType, inputDigest string) AvailableColumnWitnessBuild {
	build := AvailableColumnWitnessBuild{
		Project:           strings.TrimSpace(project),
		DatasetGeneration: NormalizeDatasetGeneration(generation),
		RootResourceType:  strings.TrimSpace(rootResourceType),
		InputDigest:       strings.TrimSpace(inputDigest),
		SchemaVersion:     AvailableColumnWitnessSchemaVersion,
		State:             AvailableColumnWitnessBuilding,
	}
	build.Key = availableColumnWitnessBuildKey(build.Project, build.DatasetGeneration, build.RootResourceType, build.InputDigest)
	return build
}

func NewAvailableColumnWitness(build AvailableColumnWitnessBuild, witness AvailabilityWitness) (AvailableColumnWitness, error) {
	if err := ValidateAvailableColumnWitnessBuild(build); err != nil {
		return AvailableColumnWitness{}, err
	}
	feature, err := availableColumnWitnessSource(witness.Feature)
	if err != nil {
		return AvailableColumnWitness{}, err
	}
	if witness.RootID == "" || !strings.HasPrefix(witness.RootID, build.RootResourceType+"/") || len(witness.RootID) == len(build.RootResourceType)+1 {
		return AvailableColumnWitness{}, fmt.Errorf("availability witness root ID must belong to %q", build.RootResourceType)
	}
	if witness.SourceID == "" || !strings.HasPrefix(witness.SourceID, witness.Feature.ResourceType+"/") || len(witness.SourceID) == len(witness.Feature.ResourceType)+1 {
		return AvailableColumnWitness{}, fmt.Errorf("availability witness source ID must belong to %q", witness.Feature.ResourceType)
	}

	route := make([]RouteCoverageStep, len(witness.Route))
	currentType := build.RootResourceType
	for i, relation := range witness.Route {
		if relation.FromResourceType != currentType || relation.ToResourceType == "" || relation.Relationship == "" ||
			(relation.StorageDirection != "OUTBOUND" && relation.StorageDirection != "INBOUND") {
			return AvailableColumnWitness{}, fmt.Errorf("availability witness route step %d is invalid or disconnected", i)
		}
		route[i] = RouteCoverageStep{
			FromResourceType: relation.FromResourceType,
			ToResourceType:   relation.ToResourceType,
			Relationship:     relation.Relationship,
			StorageDirection: relation.StorageDirection,
		}
		currentType = relation.ToResourceType
	}
	if currentType != witness.Feature.ResourceType {
		return AvailableColumnWitness{}, fmt.Errorf("availability witness route ends at %q, want source type %q", currentType, witness.Feature.ResourceType)
	}

	stored := AvailableColumnWitness{
		Project:            build.Project,
		DatasetGeneration:  build.DatasetGeneration,
		RootResourceType:   build.RootResourceType,
		InputDigest:        build.InputDigest,
		SchemaVersion:      build.SchemaVersion,
		SourceResourceType: witness.Feature.ResourceType,
		Source:             feature,
		Route:              route,
		SourceWitnessID:    witness.SourceID,
		RootWitnessID:      witness.RootID,
	}
	stored.Key, err = availableColumnWitnessKey(stored)
	if err != nil {
		return AvailableColumnWitness{}, err
	}
	return stored, nil
}

func ValidateAvailableColumnWitnessBuild(build AvailableColumnWitnessBuild) error {
	if build.Project == "" || build.DatasetGeneration == "" || build.RootResourceType == "" || build.InputDigest == "" ||
		build.SchemaVersion != AvailableColumnWitnessSchemaVersion ||
		build.Key != availableColumnWitnessBuildKey(build.Project, build.DatasetGeneration, build.RootResourceType, build.InputDigest) {
		return fmt.Errorf("invalid available-column witness build identity")
	}
	switch build.State {
	case AvailableColumnWitnessBuilding, AvailableColumnWitnessFailed:
	case AvailableColumnWitnessComplete:
		if !build.Exhaustive || !build.AllRoots || !build.Unrestricted {
			return fmt.Errorf("complete available-column witness build must be exhaustive, all-roots, and unrestricted")
		}
	default:
		return fmt.Errorf("unsupported available-column witness build state %q", build.State)
	}
	return nil
}

func ValidateAvailableColumnWitness(witness AvailableColumnWitness) error {
	if witness.Project == "" || witness.DatasetGeneration == "" || witness.RootResourceType == "" || witness.InputDigest == "" ||
		witness.SchemaVersion != AvailableColumnWitnessSchemaVersion || witness.SourceResourceType == "" || witness.SourceWitnessID == "" || witness.RootWitnessID == "" {
		return fmt.Errorf("invalid available-column witness identity")
	}
	if err := witness.Source.Validate(); err != nil {
		return fmt.Errorf("invalid available-column witness source: %w", err)
	}
	if !strings.HasPrefix(witness.SourceWitnessID, witness.SourceResourceType+"/") || len(witness.SourceWitnessID) == len(witness.SourceResourceType)+1 {
		return fmt.Errorf("available-column source witness ID must belong to %q", witness.SourceResourceType)
	}
	if !strings.HasPrefix(witness.RootWitnessID, witness.RootResourceType+"/") || len(witness.RootWitnessID) == len(witness.RootResourceType)+1 {
		return fmt.Errorf("available-column root witness ID must belong to %q", witness.RootResourceType)
	}
	currentType := witness.RootResourceType
	for i, step := range witness.Route {
		if step.FromResourceType != currentType || step.ToResourceType == "" || step.Relationship == "" ||
			(step.StorageDirection != "OUTBOUND" && step.StorageDirection != "INBOUND") {
			return fmt.Errorf("available-column witness route step %d is invalid or disconnected", i)
		}
		currentType = step.ToResourceType
	}
	if currentType != witness.SourceResourceType {
		return fmt.Errorf("available-column witness route ends at %q, want source type %q", currentType, witness.SourceResourceType)
	}
	wantKey, err := availableColumnWitnessKey(witness)
	if err != nil {
		return err
	}
	if witness.Key != wantKey {
		return fmt.Errorf("available-column witness key does not match its identity")
	}
	return nil
}

func availableColumnWitnessSource(feature AvailabilityFeature) (RouteCoverageSource, error) {
	var source RouteCoverageSource
	switch feature.Kind {
	case string(RouteCoverageField):
		source = RouteCoverageSource{Kind: RouteCoverageField, FieldPath: feature.FieldPath}
	case string(RouteCoverageSemantic):
		source = RouteCoverageSource{Kind: RouteCoverageSemantic, ConceptID: feature.ConceptID, BindingID: feature.BindingID}
	default:
		return RouteCoverageSource{}, fmt.Errorf("unsupported availability witness feature kind %q", feature.Kind)
	}
	if feature.ResourceType == "" {
		return RouteCoverageSource{}, fmt.Errorf("availability witness feature requires a resource type")
	}
	if err := source.Validate(); err != nil {
		return RouteCoverageSource{}, err
	}
	return source, nil
}

func availableColumnWitnessBuildKey(project, generation, rootResourceType, inputDigest string) string {
	return "acwb_" + catalogIdentityDigest("available-column-witness-build/v1", project, generation, rootResourceType, inputDigest)
}

func availableColumnWitnessKey(witness AvailableColumnWitness) (string, error) {
	route, err := json.Marshal(witness.Route)
	if err != nil {
		return "", fmt.Errorf("encode available-column witness route: %w", err)
	}
	return "acw_" + catalogIdentityDigest(
		"available-column-witness/v1",
		witness.Project,
		witness.DatasetGeneration,
		witness.RootResourceType,
		witness.InputDigest,
		fmt.Sprint(witness.SchemaVersion),
		witness.SourceResourceType,
		string(witness.Source.Kind),
		witness.Source.FieldPath,
		witness.Source.ConceptID,
		witness.Source.BindingID,
		string(route),
		witness.SourceWitnessID,
		witness.RootWitnessID,
	), nil
}
