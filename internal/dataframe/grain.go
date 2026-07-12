package dataframe

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/fhirschema"
)

// RowGrain identifies the resource represented by one output row.
type RowGrain string

const (
	// RowGrainResource is the safe fallback for a generated FHIR resource that
	// does not yet have a product-specific "one row per" label. It still has a
	// stable root resource identity and is therefore safe to compile.
	RowGrainResource        RowGrain = "resource"
	RowGrainPatient         RowGrain = "patient"
	RowGrainSpecimen        RowGrain = "specimen"
	RowGrainFile            RowGrain = "file"
	RowGrainDiagnosis       RowGrain = "diagnosis"
	RowGrainObservation     RowGrain = "observation"
	RowGrainStudyEnrollment RowGrain = "study_enrollment"
)

func (g RowGrain) Validate() error {
	switch g {
	case RowGrainResource, RowGrainPatient, RowGrainSpecimen, RowGrainFile, RowGrainDiagnosis,
		RowGrainObservation, RowGrainStudyEnrollment:
		return nil
	case "":
		return fmt.Errorf("row grain is required")
	default:
		return fmt.Errorf("unsupported row grain %q", g)
	}
}

// InferRowGrain returns the product grain naturally represented by a generated
// FHIR root type. Valid schema resource types without a named product grain use
// RowGrainResource so every valid semantic request still carries a stable row
// identity.
func InferRowGrain(resourceType string) (RowGrain, bool) {
	switch strings.TrimSpace(resourceType) {
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
		if fhirschema.HasResource(resourceType) {
			return RowGrainResource, true
		}
		return "", false
	}
}

// RootResourceForGrain returns the only root resource type currently capable
// of representing a named product grain without row expansion. The generic
// resource grain deliberately accepts any generated resource root.
func RootResourceForGrain(grain RowGrain) (string, bool) {
	switch grain {
	case RowGrainResource:
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

// ValidateRootGrain prevents an API caller from asking for a named row grain
// while compiling rows rooted at a different resource. Cross-grain output will
// require an explicit future EXPLODE/root-rewrite operation; accepting it now
// would silently lie about the output identity.
func ValidateRootGrain(resourceType string, grain RowGrain) error {
	if err := grain.Validate(); err != nil {
		return err
	}
	if !fhirschema.HasResource(resourceType) {
		return fmt.Errorf("root resource type %q is not represented by the active generated FHIR schema", resourceType)
	}
	expected, ok := RootResourceForGrain(grain)
	if !ok {
		return fmt.Errorf("unsupported row grain %q", grain)
	}
	if expected != "" && resourceType != expected {
		return fmt.Errorf("row grain %q requires root resource type %q, got %q", grain, expected, resourceType)
	}
	return nil
}

// DefaultRowIdentity uses the immutable project/key pair emitted by the graph
// loader. The grain remains explicit so downstream code cannot silently use a
// related resource as the output identity.
func DefaultRowIdentity(grain RowGrain) (RowIdentity, bool) {
	if err := grain.Validate(); err != nil {
		return RowIdentity{}, false
	}
	return RowIdentity{Grain: grain, Fields: []string{"project", "_key"}}, true
}

// ProjectionMode defines how values at a selected path contribute to a row.
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

// ExpandsRows reports whether the projection deliberately changes row
// multiplicity instead of reducing values into the current row grain.
func (m ProjectionMode) ExpandsRows() bool { return m == ProjectionExplode }

// Cardinality describes the formal multiplicity of a relationship or field.
// Observed data may refine UnknownObservedMany, but must not weaken a declared
// required, optional, or repeated relationship.
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

// ValidateProjection rejects implicit row expansion. Repeated values must be
// reduced explicitly or deliberately exploded into rows.
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

// RowIdentity declares the stable fields that uniquely identify a row at a
// particular grain. Fields are ordered so callers can construct deterministic
// composite keys.
type RowIdentity struct {
	Grain  RowGrain
	Fields []string
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
