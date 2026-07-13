package compiler

import "testing"

func TestResolveSemanticFieldScalarAuto(t *testing.T) {
	selector, _ := ParseSelector("gender")
	got, err := ResolveSemanticField("Patient", "root", 0, SemanticField{Name: "gender", Selector: selector})
	if err != nil {
		t.Fatalf("ResolveSemanticField: %v", err)
	}
	if got.Cardinality != CardinalityOptionalOne || got.Projection != ProjectionScalar || !got.LegacyAuto {
		t.Fatalf("unexpected scalar semantics: %#v", got)
	}
}

func TestResolveSemanticFieldDetectsRepeatedAncestor(t *testing.T) {
	selector, _ := ParseSelector("name[].family")
	got, err := ResolveSemanticField("Patient", "root", 0, SemanticField{Name: "family", Selector: selector})
	if err != nil {
		t.Fatalf("ResolveSemanticField: %v", err)
	}
	if got.Cardinality != CardinalityMany || got.Projection != ProjectionFirst || !got.LegacyAuto {
		t.Fatalf("unexpected repeated AUTO semantics: %#v", got)
	}
	if len(got.RepeatedPaths) != 1 || got.RepeatedPaths[0] != "name[]" {
		t.Fatalf("unexpected repeated paths: %#v", got.RepeatedPaths)
	}
}

func TestResolveSemanticFieldValueModes(t *testing.T) {
	selector, _ := ParseSelector("identifier[].value")
	tests := []struct {
		mode string
		want ProjectionMode
	}{
		{"FIRST", ProjectionFirst},
		{"ALL", ProjectionArray},
		{"DISTINCT", ProjectionDistinctArray},
	}
	for _, test := range tests {
		got, err := ResolveSemanticField("Patient", "root", 0, SemanticField{Name: "id", Selector: selector, ValueMode: test.mode})
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
	valid, _ := ParseSelector("gender")
	missing, _ := ParseSelector("notAField")
	implicitArray, _ := ParseSelector("name.family")
	tests := []struct {
		name         string
		resourceType string
		field        SemanticField
	}{
		{"unknown resource", "Imaginary", SemanticField{Name: "x", Selector: valid}},
		{"unknown path", "Patient", SemanticField{Name: "x", Selector: missing}},
		{"invalid value mode", "Patient", SemanticField{Name: "x", Selector: valid, ValueMode: "SCALAR"}},
		{"implicit array traversal", "Patient", SemanticField{Name: "x", Selector: implicitArray}},
		{"empty selector", "Patient", SemanticField{Name: "x"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ResolveSemanticField(test.resourceType, "root", 0, test.field); err == nil {
				t.Fatal("invalid selection unexpectedly succeeded")
			}
		})
	}
}

func TestNormalizeSelectionPlanStableAliases(t *testing.T) {
	gender, _ := ParseSelector("gender")
	id, _ := ParseSelector("identifier[].value")
	plan := SemanticPlan{Root: SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Fields: []SemanticField{{Name: "z", Selector: gender}, {Selector: id}},
	}}
	got, err := NormalizeSelectionPlan(plan)
	if err != nil {
		t.Fatalf("NormalizeSelectionPlan: %v", err)
	}
	if len(got) != 2 || got[0].Alias != "root.field_2" || got[1].Alias != "root.z" {
		t.Fatalf("unexpected stable aliases: %#v", got)
	}
}
