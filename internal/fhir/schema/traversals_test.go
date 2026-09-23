package schema

import "testing"

func TestLookupTraversal(t *testing.T) {
	cases := []struct {
		fromType  string
		edgeLabel string
		toType    string
	}{
		{"Patient", "subject_Patient", "Condition"},
		{"Patient", "subject_Patient", "Specimen"},
		{"Patient", "focus_Patient", "Observation"},
		{"Specimen", "subject_Specimen", "DocumentReference"},
		{"Group", "subject_Group", "DocumentReference"},
	}
	for _, tc := range cases {
		spec, ok := LookupTraversal(tc.fromType, tc.edgeLabel, tc.toType)
		if !ok {
			t.Fatalf("expected traversal %s %s %s", tc.fromType, tc.edgeLabel, tc.toType)
		}
		if spec.FromType != tc.fromType || spec.EdgeLabel != tc.edgeLabel || spec.ToType != tc.toType {
			t.Fatalf("unexpected traversal spec: %#v", spec)
		}
	}
}

func TestLookupTraversalRejectsUnknownTuple(t *testing.T) {
	if _, ok := LookupTraversal("Patient", "subject_Patient", "Medication"); ok {
		t.Fatal("expected unsupported tuple to miss")
	}
}

func TestLookupTraversalResolvesDeepNestedReferenceFromSchema(t *testing.T) {
	const (
		fromType  = "Organization"
		edgeLabel = "modifierExtension_extension_valueReference_ResearchStudy"
		toType    = "ResearchStudy"
	)
	spec, ok := LookupTraversal(fromType, edgeLabel, toType)
	if !ok {
		t.Fatalf("expected deep traversal %s %s %s", fromType, edgeLabel, toType)
	}
	if spec.FromType != fromType || spec.EdgeLabel != edgeLabel || spec.ToType != toType {
		t.Fatalf("unexpected deep traversal spec: %#v", spec)
	}
	if _, ok := LookupTraversal(fromType, "unrelated_extension_valueReference_ResearchStudy", toType); ok {
		t.Fatal("expected path outside the generated schema to miss")
	}
}
