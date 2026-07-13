package compiler

import "testing"

func TestRowGrainValidate(t *testing.T) {
	for _, grain := range []RowGrain{
		RowGrainResource, RowGrainPatient, RowGrainSpecimen, RowGrainFile, RowGrainDiagnosis,
		RowGrainObservation, RowGrainStudyEnrollment,
	} {
		if err := grain.Validate(); err != nil {
			t.Errorf("Validate(%q): %v", grain, err)
		}
	}
	for _, grain := range []RowGrain{"", "encounter"} {
		if err := grain.Validate(); err == nil {
			t.Errorf("Validate(%q) unexpectedly succeeded", grain)
		}
	}
}

func TestProjectionModeProperties(t *testing.T) {
	modes := []ProjectionMode{
		ProjectionScalar, ProjectionFirst, ProjectionArray,
		ProjectionDistinctArray, ProjectionAggregate, ProjectionPivot,
		ProjectionExplode,
	}
	for _, mode := range modes {
		if err := mode.Validate(); err != nil {
			t.Errorf("Validate(%q): %v", mode, err)
		}
		if got := mode.ExpandsRows(); got != (mode == ProjectionExplode) {
			t.Errorf("ExpandsRows(%q) = %v", mode, got)
		}
	}
	if err := ProjectionMode("flatten").Validate(); err == nil {
		t.Fatal("unknown projection mode unexpectedly succeeded")
	}
}

func TestCardinalityProperties(t *testing.T) {
	tests := []struct {
		cardinality Cardinality
		many        bool
		required    bool
	}{
		{CardinalityRequiredOne, false, true},
		{CardinalityOptionalOne, false, false},
		{CardinalityMany, true, false},
		{CardinalityUnknownObservedMany, true, false},
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
	if err := ValidateProjection(CardinalityMany, ProjectionScalar); err == nil {
		t.Fatal("many-to-scalar projection unexpectedly succeeded")
	}
	for _, mode := range []ProjectionMode{
		ProjectionFirst, ProjectionArray, ProjectionDistinctArray,
		ProjectionAggregate, ProjectionPivot, ProjectionExplode,
	} {
		if err := ValidateProjection(CardinalityMany, mode); err != nil {
			t.Errorf("many-to-%s projection: %v", mode, err)
		}
	}
	if err := ValidateProjection(CardinalityRequiredOne, ProjectionScalar); err != nil {
		t.Errorf("one-to-scalar projection: %v", err)
	}
}

func TestRowIdentityValidate(t *testing.T) {
	valid := RowIdentity{Grain: RowGrainFile, Fields: []string{"project", "id"}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid identity: %v", err)
	}

	tests := []RowIdentity{
		{},
		{Grain: RowGrainFile},
		{Grain: RowGrainFile, Fields: []string{"id", " "}},
		{Grain: RowGrainFile, Fields: []string{"id", "id"}},
	}
	for _, identity := range tests {
		if err := identity.Validate(); err == nil {
			t.Errorf("invalid identity %#v unexpectedly succeeded", identity)
		}
	}
}

func TestInferRowGrainAndDefaultIdentity(t *testing.T) {
	for resourceType, want := range map[string]RowGrain{
		"Patient":           RowGrainPatient,
		"Specimen":          RowGrainSpecimen,
		"DocumentReference": RowGrainFile,
		"Condition":         RowGrainDiagnosis,
		"Observation":       RowGrainObservation,
		"ResearchSubject":   RowGrainStudyEnrollment,
	} {
		grain, ok := InferRowGrain(resourceType)
		if !ok || grain != want {
			t.Fatalf("InferRowGrain(%q) = %q, %v; want %q, true", resourceType, grain, ok, want)
		}
		identity, ok := DefaultRowIdentity(grain)
		if !ok || identity.Grain != grain || len(identity.Fields) != 2 {
			t.Fatalf("DefaultRowIdentity(%q) = %#v, %v", grain, identity, ok)
		}
	}
	if grain, ok := InferRowGrain("Organization"); !ok || grain != RowGrainResource {
		t.Fatalf("Organization generic row grain = %q, %v; want %q, true", grain, ok, RowGrainResource)
	}
}

func TestValidateRootGrainRejectsImplicitCrossGrainOutput(t *testing.T) {
	if err := ValidateRootGrain("Specimen", RowGrainSpecimen); err != nil {
		t.Fatalf("matching named grain: %v", err)
	}
	if err := ValidateRootGrain("Organization", RowGrainResource); err != nil {
		t.Fatalf("generic generated root: %v", err)
	}
	if err := ValidateRootGrain("Patient", RowGrainSpecimen); err == nil {
		t.Fatal("cross-grain root unexpectedly accepted")
	}
}
