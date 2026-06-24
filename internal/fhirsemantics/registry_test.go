package fhirsemantics

import (
	"testing"

	"arangodb-proto/internal/fhirschema"
)

func TestResolveFieldRef(t *testing.T) {
	spec, ok := ResolveFieldRef("Patient", "Patient.birth_sex")
	if !ok {
		t.Fatal("expected fieldRef to resolve")
	}
	if got := fhirschema.SelectorExpression(spec.Selector); got != `extension[].valueCode where url contains "us-core-birthsex"` {
		t.Fatalf("unexpected selector expression: %q", got)
	}
	if got := fhirschema.CanonicalPath(spec.Selector); got != "extension[].valueCode" {
		t.Fatalf("unexpected canonical path: %q", got)
	}
}

func TestDocumentReferenceSummaryField(t *testing.T) {
	got, ok := DocumentReferenceSummaryField(`category[].coding[].display where system contains "workflow_type"`)
	if !ok {
		t.Fatal("expected selector to map to summary field")
	}
	if got != "workflow_type" {
		t.Fatalf("unexpected summary field: %q", got)
	}
}

func TestResolveTraversal(t *testing.T) {
	spec, ok := ResolveTraversal("Patient", "focus_Patient", "Observation")
	if !ok {
		t.Fatal("expected traversal to resolve")
	}
	if spec.Role != TraversalRolePatientDirectChild {
		t.Fatalf("unexpected traversal role: %q", spec.Role)
	}
	if spec.SetName != "patient_focus_observation_set" {
		t.Fatalf("unexpected set name: %q", spec.SetName)
	}
}
