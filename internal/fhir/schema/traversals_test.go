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
