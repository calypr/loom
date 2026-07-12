package fhirschema

import "testing"

func TestResourceTypesAreSortedDefensiveCopy(t *testing.T) {
	resources := ResourceTypes()
	if len(resources) == 0 || !HasResource("Patient") || HasResource("UnknownResource") {
		t.Fatalf("unexpected generated resource inventory: %#v", resources)
	}
	for i := 1; i < len(resources); i++ {
		if resources[i-1] >= resources[i] {
			t.Fatalf("resource inventory is not sorted and unique: %#v", resources)
		}
	}
	resources[0] = "mutated"
	if ResourceTypes()[0] == "mutated" {
		t.Fatal("ResourceTypes exposed generated storage")
	}
}

func TestResolveFieldSemanticsFromGeneratedMetadata(t *testing.T) {
	tests := []struct {
		path string
		want FieldSemantics
	}{
		{"gender", FieldSemantics{Kind: FieldKindScalar}},
		{"name", FieldSemantics{Kind: FieldKindArray, ElementKind: FieldKindObject, Reference: "HumanName"}},
		{"name[].family", FieldSemantics{Kind: FieldKindScalar}},
		{"managingOrganization", FieldSemantics{Kind: FieldKindObject, Reference: "Reference"}},
	}
	for _, test := range tests {
		got, ok := ResolveFieldSemantics("Patient", test.path)
		if !ok || got != test.want {
			t.Errorf("ResolveFieldSemantics(Patient, %q) = %#v, %v; want %#v, true", test.path, got, ok, test.want)
		}
	}
	if _, ok := ResolveFieldSemantics("Patient", "doesNotExist"); ok {
		t.Fatal("unknown path resolved")
	}
}

func TestResolveCompilerTraversalFromGeneratedMetadata(t *testing.T) {
	got, ok, err := ResolveCompilerTraversal("Patient", "subject_Patient", "Specimen")
	if err != nil || !ok {
		t.Fatalf("ResolveCompilerTraversal() = %#v, %v, %v", got, ok, err)
	}
	if got.Direction != TraversalOutbound || got.Multiplicity != TraversalOne {
		t.Fatalf("unexpected normalized traversal: %#v", got)
	}
	if _, ok, err := ResolveCompilerTraversal("Patient", "missing", "Specimen"); err != nil || ok {
		t.Fatalf("missing traversal = ok %v, err %v", ok, err)
	}
}

func TestTraversalNormalizationRejectsUnknownAndCombinesSafeValues(t *testing.T) {
	if got, err := normalizeTraversalDirection([]string{"outbound", "inbound"}); err != nil || got != TraversalAny {
		t.Fatalf("combined direction = %q, %v", got, err)
	}
	if _, err := normalizeTraversalDirection([]string{"sideways"}); err == nil {
		t.Fatal("unknown direction accepted")
	}
	if got, err := normalizeTraversalMultiplicity([]string{"has_one", "has_many"}); err != nil || got != TraversalMany {
		t.Fatalf("combined multiplicity = %q, %v", got, err)
	}
	if _, err := normalizeTraversalMultiplicity([]string{"sometimes"}); err == nil {
		t.Fatal("unknown multiplicity accepted")
	}
}
