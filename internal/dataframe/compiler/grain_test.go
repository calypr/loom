package compiler

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestRowGrainValidate(t *testing.T) {
	for _, grain := range []spec.RowGrain{
		spec.RowGrainResource, spec.RowGrainPatient, spec.RowGrainSpecimen, spec.RowGrainFile, spec.RowGrainDiagnosis,
		spec.RowGrainObservation, spec.RowGrainStudyEnrollment,
	} {
		if err := grain.Validate(); err != nil {
			t.Errorf("Validate(%q): %v", grain, err)
		}
	}
	for _, grain := range []spec.RowGrain{"", "encounter"} {
		if err := grain.Validate(); err == nil {
			t.Errorf("Validate(%q) unexpectedly succeeded", grain)
		}
	}
}

func TestProjectionModeProperties(t *testing.T) {
	modes := []spec.ProjectionMode{
		spec.ProjectionScalar, spec.ProjectionFirst, spec.ProjectionArray,
		spec.ProjectionDistinctArray, spec.ProjectionAggregate, spec.ProjectionPivot,
		spec.ProjectionExplode,
	}
	for _, mode := range modes {
		if err := mode.Validate(); err != nil {
			t.Errorf("Validate(%q): %v", mode, err)
		}
		if got := mode.ExpandsRows(); got != (mode == spec.ProjectionExplode) {
			t.Errorf("ExpandsRows(%q) = %v", mode, got)
		}
	}
	if err := spec.ProjectionMode("flatten").Validate(); err == nil {
		t.Fatal("unknown projection mode unexpectedly succeeded")
	}
}

func TestCardinalityProperties(t *testing.T) {
	tests := []struct {
		cardinality spec.Cardinality
		many        bool
		required    bool
	}{
		{spec.CardinalityRequiredOne, false, true},
		{spec.CardinalityOptionalOne, false, false},
		{spec.CardinalityMany, true, false},
		{spec.CardinalityUnknownObservedMany, true, false},
	}
	for _, test := range tests {
		if err := test.cardinality.Validate(); err != nil {
			t.Errorf("Validate(%q): %v", test.cardinality, err)
		}
		if got := test.cardinality.AllowsMany(); got != test.many {
			t.Errorf("AllowsMany(%q) = %v", test.cardinality, got)
		}
		if got := test.cardinality.IsRequired(); got != test.required {
			t.Errorf("IsRequired(%q) = %v", test.cardinality, got)
		}
	}
}

func TestValidateProjection(t *testing.T) {
	if err := spec.ValidateProjection(spec.CardinalityMany, spec.ProjectionScalar); err == nil {
		t.Fatal("many-to-scalar projection unexpectedly succeeded")
	}
	for _, mode := range []spec.ProjectionMode{
		spec.ProjectionFirst, spec.ProjectionArray, spec.ProjectionDistinctArray,
		spec.ProjectionAggregate, spec.ProjectionPivot, spec.ProjectionExplode,
	} {
		if err := spec.ValidateProjection(spec.CardinalityMany, mode); err != nil {
			t.Errorf("many-to-%s projection: %v", mode, err)
		}
	}
	if err := spec.ValidateProjection(spec.CardinalityRequiredOne, spec.ProjectionScalar); err != nil {
		t.Errorf("one-to-scalar projection: %v", err)
	}
}

func TestRowIdentityValidate(t *testing.T) {
	valid := spec.RowIdentity{Grain: spec.RowGrainFile, Fields: []string{"project", "id"}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid identity: %v", err)
	}

	tests := []spec.RowIdentity{
		{},
		{Grain: spec.RowGrainFile},
		{Grain: spec.RowGrainFile, Fields: []string{"id", " "}},
		{Grain: spec.RowGrainFile, Fields: []string{"id", "id"}},
	}
	for _, identity := range tests {
		if err := identity.Validate(); err == nil {
			t.Errorf("invalid identity %#v unexpectedly succeeded", identity)
		}
	}
}

func TestInferRowGrainAndDefaultIdentity(t *testing.T) {
	for resourceType, want := range map[string]spec.RowGrain{
		"Patient":           spec.RowGrainPatient,
		"Specimen":          spec.RowGrainSpecimen,
		" specimen ":        spec.RowGrainSpecimen,
		"DocumentReference": spec.RowGrainFile,
		"Condition":         spec.RowGrainDiagnosis,
		"Observation":       spec.RowGrainObservation,
		"ResearchSubject":   spec.RowGrainStudyEnrollment,
	} {
		grain, ok := spec.InferRowGrain(resourceType)
		if !ok || grain != want {
			t.Fatalf("InferRowGrain(%q) = %q, %v; want %q, true", resourceType, grain, ok, want)
		}
		identity, ok := spec.DefaultRowIdentity(grain)
		if !ok || identity.Grain != grain || len(identity.Fields) != 2 {
			t.Fatalf("DefaultRowIdentity(%q) = %#v, %v", grain, identity, ok)
		}
	}
	if grain, ok := spec.InferRowGrain("Organization"); !ok || grain != spec.RowGrainResource {
		t.Fatalf("Organization generic row grain = %q, %v; want %q, true", grain, ok, spec.RowGrainResource)
	}
	for _, resourceType := range []string{"PractitionerQualification", "qualification", "issuer", "Resource", "CustomResource"} {
		if grain, ok := spec.InferRowGrain(resourceType); ok || grain != "" {
			t.Fatalf("InferRowGrain(%q) = %q, %v; non-resource type must be rejected", resourceType, grain, ok)
		}
	}
}

func TestValidateRootGrainRejectsImplicitCrossGrainOutput(t *testing.T) {
	if err := spec.ValidateRootGrain("Specimen", spec.RowGrainSpecimen); err != nil {
		t.Fatalf("matching named grain: %v", err)
	}
	if err := spec.ValidateRootGrain("Organization", spec.RowGrainResource); err != nil {
		t.Fatalf("generic generated root: %v", err)
	}
	if err := spec.ValidateRootGrain("Patient", spec.RowGrainSpecimen); err == nil {
		t.Fatal("cross-grain root unexpectedly accepted")
	}
}
