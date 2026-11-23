package row

import (
	"encoding/json"
	"strings"
	"testing"

	fhir "github.com/calypr/loom/fhir/model"
)

func TestGeneratedLoadCapabilityMatchesGeneratedFHIRMethods(t *testing.T) {
	for _, resourceType := range []string{"Patient", "Specimen", "Observation", "DiagnosticReport", "Task"} {
		if !SupportsGeneratedLoad(resourceType) {
			t.Fatalf("generated fast path unexpectedly unavailable for %s", resourceType)
		}
	}
	for _, resourceType := range []string{"Unknown", "Resource"} {
		if SupportsGeneratedLoad(resourceType) {
			t.Fatalf("unknown root %s should use generic loader fallback", resourceType)
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

func TestGeneratedNestedLinkUsesConcreteRuntimeEndpoints(t *testing.T) {
	_, edges, _, err := loadRowGenerated("Practitioner", []byte(`{
  "resourceType": "Practitioner",
  "id": "practitioner-1",
  "qualification": [{"code": {"text": "board-certified"}, "issuer": {"reference": "Organization/org-1"}}]
}`), "project-1", map[string]float64{})
	if err != nil {
		t.Fatalf("loadRowGenerated(Practitioner): %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected one forward qualification edge, got %d", len(edges))
	}
	var edge fhir.EdgeDocument
	if err := json.Unmarshal(edges[0], &edge); err != nil {
		t.Fatalf("decode generated edge: %v", err)
	}
	if edge.Label != "qualification_issuer" || edge.From != "Practitioner/practitioner-1" ||
		edge.To != "Organization/org-1" || edge.FromType != "Practitioner" || edge.ToType != "Organization" {
		t.Fatalf("unexpected qualification issuer edge: %#v", edge)
	}
}

func TestGeneratedTaskUsesOfficialForProperty(t *testing.T) {
	_, edges, _, err := loadRowGenerated("Task", []byte(`{
		"resourceType":"Task",
		"id":"task-1",
		"status":"requested",
		"intent":"order",
		"for":{"reference":"Patient/patient-1"}
	}`), "project-1", map[string]float64{})
	if err != nil {
		t.Fatalf("load official Task shape: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected one Task.for edge, got %d", len(edges))
	}
	var edge fhir.EdgeDocument
	if err := json.Unmarshal(edges[0], &edge); err != nil {
		t.Fatalf("decode Task.for edge: %v", err)
	}
	if edge.Label != "for_Patient" || edge.From != "Task/task-1" || edge.To != "Patient/patient-1" {
		t.Fatalf("unexpected Task.for edge: %#v", edge)
	}
}

func TestGeneratedLoadAddsProjectIDToEnvelopeAndPayload(t *testing.T) {
	project := "HTAN_INT-BForePC"
	vertex, _, _, err := loadRowGenerated("Patient", []byte(`{
  "resourceType": "Patient",
  "id": "patient-1"
}`), project, map[string]float64{})
	if err != nil {
		t.Fatalf("loadRowGenerated(Patient): %v", err)
	}
	if vertex.ProjectID != project {
		t.Fatalf("project_id envelope = %q, want %q", vertex.ProjectID, project)
	}
	var payload map[string]any
	if err := json.Unmarshal(vertex.Payload.(json.RawMessage), &payload); err != nil {
		t.Fatalf("decode generated payload: %v", err)
	}
	if got := payload["project_id"]; got != project {
		t.Fatalf("payload project_id = %#v, want %q", got, project)
	}
}

func TestGeneratedLoadRejectsUnknownAndUnsupportedResourceTypes(t *testing.T) {
	for _, resourceType := range []string{"Unknown", "Resource"} {
		t.Run(resourceType, func(t *testing.T) {
			_, _, kind, err := loadRowGenerated(resourceType, []byte(`{}`), "project-1", map[string]float64{})
			if kind != RowErrorGeneration || err == nil || !strings.Contains(err.Error(), "generated loader does not support") {
				t.Fatalf("loadRowGenerated(%q) = kind %q err %v, want unsupported-resource generation error", resourceType, kind, err)
			}
		})
	}
}

func TestWithProjectIDRetainsInputWhenPayloadCannotBeDecoded(t *testing.T) {
	line := []byte(`{"resourceType":`)
	if got := withProjectID(line, "project-1"); string(got) != string(line) {
		t.Fatalf("withProjectID malformed-input fallback = %q, want original %q", got, line)
	}
}

func TestGeneratedLoadRejectsMissingFHIRID(t *testing.T) {
	_, _, kind, err := loadRowGenerated("DocumentReference", []byte(`{
  "resourceType": "DocumentReference",
  "status": "current",
  "content": [{"attachment": {}}]
}`), "project-1", map[string]float64{})
	if kind != RowErrorValidation || err == nil || !strings.Contains(err.Error(), "DocumentReference payload missing string id") {
		t.Fatalf("loadRowGenerated() = kind %q err %v, want missing ID validation error", kind, err)
	}
}

func loadRowGenerated(resourceType string, line []byte, project string, stageSeconds map[string]float64) (VertexDocument, []json.RawMessage, RowErrorType, error) {
	vertex, documents, _, errorType, err := loadRowGeneratedInternal(resourceType, line, project, stageSeconds, false)
	if err != nil {
		return VertexDocument{}, nil, errorType, err
	}
	edges, err := marshalEdgeDocuments(documents)
	return vertex, edges, errorType, err
}

func TestGeneratedLoadNormalizesNumericFHIRInteger64(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  string
	}{
		{"numeric", "411314", "411314"},
		{"exact", "9007199254740993", "9007199254740993"},
		{"max", "9223372036854775807", "9223372036854775807"},
		{"min", "-9223372036854775808", "-9223372036854775808"},
		{"quoted", `"411314"`, "411314"},
		{"quoted exact", `"9007199254740993"`, "9007199254740993"},
		{"quoted max", `"9223372036854775807"`, "9223372036854775807"},
		{"quoted min", `"-9223372036854775808"`, "-9223372036854775808"},
		{"malformed quoted", `"abc1"`, ""},
		{"trailing junk", `"0garbage"`, ""},
		{"quoted overflow", `"9223372036854775808"`, ""},
		{"quoted underflow", `"-9223372036854775809"`, ""},
		{"exponent", "1e1", ""},
		{"fraction", "1.5", ""},
		{"overflow", "9223372036854775808", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := []byte(`{"resourceType":"DocumentReference","id":"doc","status":"current","content":[{"attachment":{"size":` + test.value + `,"pages":2}},{"attachment":{"size":"7"}}],"unknownNumber":12}`)
			vertex, _, _, _, err := loadRowGeneratedWithPayload("DocumentReference", raw, "test", map[string]float64{})
			if test.want == "" {
				if err == nil {
					t.Fatal("expected invalid integer64 to be rejected")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var stored struct {
				Content []struct {
					Attachment struct {
						Size  string
						Pages int
					}
				}
				UnknownNumber int
				ProjectID     string `json:"project_id"`
			}
			if err := json.Unmarshal(vertex.Payload.(json.RawMessage), &stored); err != nil {
				t.Fatal(err)
			}
			if len(stored.Content) != 2 || stored.Content[0].Attachment.Size != test.want ||
				stored.Content[0].Attachment.Pages != 2 || stored.Content[1].Attachment.Size != "7" || stored.UnknownNumber != 12 || stored.ProjectID != "test" {
				t.Fatalf("unexpected stored payload: %+v", stored)
			}
		})
	}
}

func TestGeneratedLoadRejectsCodingSystemWhitespace(t *testing.T) {
	raw := []byte(`{"resourceType":"ImagingStudy","id":"study","status":"available","subject":{"reference":"Patient/p"},"series":[{"uid":"1.2.3","modality":{"coding":[{"system":" http://dicom.nema.org/resources/ontology/DCM","code":"CT"}]}}]}`)
	_, _, _, kind, err := loadRowGeneratedWithPayload("ImagingStudy", raw, "test", map[string]float64{})
	if kind != RowErrorValidation || err == nil || !strings.Contains(err.Error(), "field 'system' does not match its schema pattern") {
		t.Fatalf("expected Coding.system validation error, got %v (%v)", err, kind)
	}
}
