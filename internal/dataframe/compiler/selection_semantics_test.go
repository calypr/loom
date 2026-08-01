package compiler

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestResolveSemanticFieldScalarAuto(t *testing.T) {
	selector, _ := spec.ParseSelector("gender")
	got, err := semantic.ResolveSemanticField("Patient", "root", 0, semantic.SemanticField{Name: "gender", Selector: selector})
	if err != nil {
		t.Fatalf("ResolveSemanticField: %v", err)
	}
	if got.Cardinality != spec.CardinalityOptionalOne || got.Projection != spec.ProjectionScalar || !got.LegacyAuto {
		t.Fatalf("unexpected scalar semantics: %#v", got)
	}
}

func TestResolveSemanticFieldDetectsRepeatedAncestor(t *testing.T) {
	selector, _ := spec.ParseSelector("name[].family")
	got, err := semantic.ResolveSemanticField("Patient", "root", 0, semantic.SemanticField{Name: "family", Selector: selector})
	if err != nil {
		t.Fatalf("ResolveSemanticField: %v", err)
	}
	if got.Cardinality != spec.CardinalityMany || got.Projection != spec.ProjectionFirst || !got.LegacyAuto {
		t.Fatalf("unexpected repeated AUTO semantics: %#v", got)
	}
	if len(got.RepeatedPaths) != 1 || got.RepeatedPaths[0] != "name[]" {
		t.Fatalf("unexpected repeated paths: %#v", got.RepeatedPaths)
	}
}

func TestResolveSemanticFieldValueModes(t *testing.T) {
	selector, _ := spec.ParseSelector("identifier[].value")
	tests := []struct {
		mode string
		want spec.ProjectionMode
	}{
		{"FIRST", spec.ProjectionFirst},
		{"ALL", spec.ProjectionArray},
		{"DISTINCT", spec.ProjectionDistinctArray},
	}
	for _, test := range tests {
		got, err := semantic.ResolveSemanticField("Patient", "root", 0, semantic.SemanticField{Name: "id", Selector: selector, ValueMode: test.mode})
		if err != nil {
			t.Errorf("mode %s: %v", test.mode, err)
			continue
		}
		if got.Projection != test.want || got.LegacyAuto {
			t.Errorf("mode %s = %#v", test.mode, got)
		}
	}
}

func TestResolveSemanticFieldRejectsInvalidSemantics(t *testing.T) {
	valid, _ := spec.ParseSelector("gender")
	missing, _ := spec.ParseSelector("notAField")
	implicitArray, _ := spec.ParseSelector("name.family")
	tests := []struct {
		name         string
		resourceType string
		field        semantic.SemanticField
	}{
		{"unknown resource", "Imaginary", semantic.SemanticField{Name: "x", Selector: valid}},
		{"unknown path", "Patient", semantic.SemanticField{Name: "x", Selector: missing}},
		{"invalid value mode", "Patient", semantic.SemanticField{Name: "x", Selector: valid, ValueMode: "SCALAR"}},
		{"implicit array traversal", "Patient", semantic.SemanticField{Name: "x", Selector: implicitArray}},
		{"empty selector", "Patient", semantic.SemanticField{Name: "x"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := semantic.ResolveSemanticField(test.resourceType, "root", 0, test.field); err == nil {
				t.Fatal("invalid selection unexpectedly succeeded")
			}
		})
	}
}

func TestNormalizeSelectionPlanStableAliases(t *testing.T) {
	gender, _ := spec.ParseSelector("gender")
	id, _ := spec.ParseSelector("identifier[].value")
	plan := semantic.SemanticPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Fields: []semantic.SemanticField{{Name: "z", Selector: gender}, {Selector: id}},
	}}
	got, err := semantic.NormalizeSelectionPlan(plan)
	if err != nil {
		t.Fatalf("NormalizeSelectionPlan: %v", err)
	}
	if len(got) != 2 || got[0].Alias != "root.field_2" || got[1].Alias != "root.z" {
		t.Fatalf("unexpected stable aliases: %#v", got)
	}
}
