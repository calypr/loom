package ingest

import (
	"encoding/json"
	"testing"

	fhir "github.com/calypr/loom/fhirstructs"
)

func TestGeneratedLoadCapabilityFallsBackForSchemaOnlyRoots(t *testing.T) {
	for _, resourceType := range []string{"Patient", "Specimen", "Observation"} {
		if !supportsGeneratedLoad(resourceType) {
			t.Fatalf("generated fast path unexpectedly unavailable for %s", resourceType)
		}
	}
	for _, resourceType := range []string{"DiagnosticReport", "MedicationRequest", "MedicationStatement", "Procedure", "Task"} {
		if supportsGeneratedLoad(resourceType) {
			t.Fatalf("schema-only root %s should use generic loader fallback", resourceType)
		}
	}
}

func TestGeneratedResearchSubjectStudyEdgeTargetsResearchStudy(t *testing.T) {
	_, edges, _, err := loadRowGenerated("ResearchSubject", []byte(`{
  "resourceType": "ResearchSubject",
  "id": "research-subject-1",
  "status": "active",
  "study": {"reference": "ResearchStudy/study-1"},
  "subject": {"reference": "Patient/patient-1"}
}`), "project-1", map[string]float64{})
	if err != nil {
		t.Fatalf("loadRowGenerated(ResearchSubject): %v", err)
	}

	var studyEdge *fhir.EdgeDocument
	for _, raw := range edges {
		var edge fhir.EdgeDocument
		if err := json.Unmarshal(raw, &edge); err != nil {
			t.Fatalf("decode generated edge: %v", err)
		}
		if edge.Label == "study" {
			studyEdge = &edge
			break
		}
	}
	if studyEdge == nil {
		t.Fatalf("generated ResearchSubject edges do not contain study: %#v", edges)
	}
	if got, want := studyEdge.From, "ResearchSubject/research-subject-1"; got != want {
		t.Fatalf("study edge _from = %q, want %q", got, want)
	}
	if got, want := studyEdge.To, "ResearchStudy/study-1"; got != want {
		t.Fatalf("study edge _to = %q, want %q", got, want)
	}
	if got, want := studyEdge.FromType, "ResearchSubject"; got != want {
		t.Fatalf("study edge from_type = %q, want %q", got, want)
	}
	if got, want := studyEdge.ToType, "ResearchStudy"; got != want {
		t.Fatalf("study edge to_type = %q, want %q", got, want)
	}
}
