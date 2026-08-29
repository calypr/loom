package ingest

import (
	"testing"

	"github.com/bmeg/jsonschemagraph/model"
)

func TestEdgeFromGripRejectsNonConcreteResourceEndpoints(t *testing.T) {
	for _, target := range []string{"PractitionerQualification", "qualification", "issuer", "Resource", "CustomResource"} {
		_, err := EdgeFromGrip("project", "Practitioner", &model.Edge{Id: "edge-1", From: "practitioner-1", To: target + "/target-1", Label: "link"})
		if err == nil {
			t.Fatalf("target type %q was accepted", target)
		}
	}
}

func TestEdgeFromGripCanonicalizesConcreteEndpoints(t *testing.T) {
	edge, err := EdgeFromGrip("project", " patient ", &model.Edge{Id: "edge-1", From: "patient-1", To: "specimen-1", Label: "subject_Specimen"})
	if err != nil {
		t.Fatal(err)
	}
	if edge.FromType != "Patient" || edge.ToType != "Specimen" {
		t.Fatalf("edge types = %q -> %q, want Patient -> Specimen", edge.FromType, edge.ToType)
	}
	if edge.From != "Patient/patient-1" || edge.To != "Specimen/specimen-1" {
		t.Fatalf("edge endpoints = %q -> %q, want canonical collections", edge.From, edge.To)
	}
}

func TestVertexFromFHIRWithExtraRejectsNonConcreteResourceTypes(t *testing.T) {
	if _, err := VertexFromFHIRWithExtra("project", "PractitionerQualification", map[string]any{"id": "qualification-1"}, nil); err == nil {
		t.Fatal("backbone definition was accepted as a graph vertex")
	}
}
