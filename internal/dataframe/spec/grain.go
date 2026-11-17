package spec

import (
	"fmt"
	"strings"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

type RowGrain string

const (
	RowGrainResource        RowGrain = "resource"
	RowGrainPatient         RowGrain = "patient"
	RowGrainSpecimen        RowGrain = "specimen"
	RowGrainFile            RowGrain = "file"
	RowGrainDiagnosis       RowGrain = "diagnosis"
	RowGrainObservation     RowGrain = "observation"
	RowGrainStudyEnrollment RowGrain = "study_enrollment"
	RowGrainExpanded        RowGrain = "expanded"
	RowGrainGroups          RowGrain = "groups"
)

func (g RowGrain) Validate() error {
	switch g {
	case RowGrainResource, RowGrainPatient, RowGrainSpecimen, RowGrainFile, RowGrainDiagnosis,
		RowGrainObservation, RowGrainStudyEnrollment, RowGrainExpanded, RowGrainGroups:
		return nil
	case "":
		return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "row grain is required", dataframeerrors.WithRetryable(false))
	default:
		return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, fmt.Sprintf("unsupported row grain %q", g), dataframeerrors.WithRetryable(false))
	}
}

func InferRowGrain(resourceType string) (RowGrain, bool) {
	canonical, ok := fhirschema.CanonicalResourceType(resourceType)
	if !ok {
		return "", false
	}
	switch canonical {
	case "Patient":
		return RowGrainPatient, true
	case "Specimen":
		return RowGrainSpecimen, true
	case "DocumentReference":
		return RowGrainFile, true
	case "Condition":
		return RowGrainDiagnosis, true
	case "Observation":
		return RowGrainObservation, true
	case "ResearchSubject":
		return RowGrainStudyEnrollment, true
	default:
		return RowGrainResource, true
	}
}

func RootResourceForGrain(grain RowGrain) (string, bool) {
	switch grain {
	case RowGrainResource:
		return "", true
	case RowGrainExpanded, RowGrainGroups:
		return "", true
	case RowGrainPatient:
		return "Patient", true
	case RowGrainSpecimen:
		return "Specimen", true
	case RowGrainFile:
		return "DocumentReference", true
	case RowGrainDiagnosis:
		return "Condition", true
	case RowGrainObservation:
		return "Observation", true
	case RowGrainStudyEnrollment:
		return "ResearchSubject", true
	default:
		return "", false
	}
}

func ValidateRootGrain(resourceType string, grain RowGrain) error {
	if err := grain.Validate(); err != nil {
		return err
	}
	canonical, ok := fhirschema.ConcreteResourceType(resourceType)
	if !ok {
		return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, fmt.Sprintf("root resource type %q is not represented by the active generated FHIR schema", resourceType), dataframeerrors.WithRetryable(false))
	}
	expected, ok := RootResourceForGrain(grain)
	if !ok {
		return fmt.Errorf("unsupported row grain %q", grain)
	}
	if expected != "" && canonical != expected {
		return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, fmt.Sprintf("row grain %q requires root resource type %q, got %q", grain, expected, canonical), dataframeerrors.WithRetryable(false))
	}
	return nil
}

func DefaultRowIdentity(grain RowGrain) (RowIdentity, bool) {
	if err := grain.Validate(); err != nil {
		return RowIdentity{}, false
	}
	if grain == RowGrainGroups {
		return RowIdentity{Grain: grain, Fields: []string{"group_revision_id", "group_id"}}, true
	}
	return RowIdentity{Grain: grain, Fields: []string{"project", "_key"}}, true
}

type ProjectionMode string

const (
	ProjectionScalar        ProjectionMode = "scalar"
	ProjectionFirst         ProjectionMode = "first"
	ProjectionArray         ProjectionMode = "array"
	ProjectionDistinctArray ProjectionMode = "distinct_array"
	ProjectionAggregate     ProjectionMode = "aggregate"
	ProjectionPivot         ProjectionMode = "pivot"
	ProjectionExplode       ProjectionMode = "explode"
)

func (m ProjectionMode) Validate() error {
	switch m {
	case ProjectionScalar, ProjectionFirst, ProjectionArray,
		ProjectionDistinctArray, ProjectionAggregate, ProjectionPivot,
		ProjectionExplode:
		return nil
	case "":
		return fmt.Errorf("projection mode is required")
	default:
		return fmt.Errorf("unsupported projection mode %q", m)
	}
}

func (m ProjectionMode) ExpandsRows() bool { return m == ProjectionExplode }

type Cardinality string

const (
	CardinalityRequiredOne         Cardinality = "required_one"
	CardinalityOptionalOne         Cardinality = "optional_one"
	CardinalityMany                Cardinality = "many"
	CardinalityUnknownObservedMany Cardinality = "unknown_observed_many"
)

func (c Cardinality) Validate() error {
	switch c {
	case CardinalityRequiredOne, CardinalityOptionalOne, CardinalityMany,
		CardinalityUnknownObservedMany:
		return nil
	case "":
		return fmt.Errorf("cardinality is required")
	default:
		return fmt.Errorf("unsupported cardinality %q", c)
	}
}

func (c Cardinality) AllowsMany() bool {
	return c == CardinalityMany || c == CardinalityUnknownObservedMany
}

func (c Cardinality) IsRequired() bool { return c == CardinalityRequiredOne }

func ValidateProjection(cardinality Cardinality, mode ProjectionMode) error {
	if err := cardinality.Validate(); err != nil {
		return err
	}
	if err := mode.Validate(); err != nil {
		return err
	}
	if cardinality.AllowsMany() && mode == ProjectionScalar {
		return fmt.Errorf("scalar projection cannot represent %s cardinality", cardinality)
	}
	return nil
}

type RowIdentity struct {
	Grain  RowGrain
	Fields []string
}

func (i *RowIdentity) Clone() *RowIdentity {
	if i == nil {
		return nil
	}
	out := *i
	out.Fields = append([]string(nil), i.Fields...)
	return &out
}

func (i RowIdentity) Validate() error {
	if err := i.Grain.Validate(); err != nil {
		return err
	}
	if len(i.Fields) == 0 {
		return fmt.Errorf("row identity requires at least one field")
	}
	seen := make(map[string]struct{}, len(i.Fields))
	for index, field := range i.Fields {
		field = strings.TrimSpace(field)
		if field == "" {
			return fmt.Errorf("row identity field %d is empty", index)
		}
		if _, exists := seen[field]; exists {
			return fmt.Errorf("row identity field %q is duplicated", field)
		}
		seen[field] = struct{}{}
	}
	return nil
}
