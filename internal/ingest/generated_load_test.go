package ingest

import (
	"encoding/json"
	"strings"
	"testing"

	fhir "github.com/calypr/loom/generated/fhir"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestGeneratedLoadCapabilityMatchesGeneratedFHIRMethods(t *testing.T) {
	for _, resourceType := range []string{"Patient", "Specimen", "Observation", "DiagnosticReport", "Task"} {
		if !supportsGeneratedLoad(resourceType) {
			t.Fatalf("generated fast path unexpectedly unavailable for %s", resourceType)
		}
	}
	for _, resourceType := range []string{"Unknown", "Resource"} {
		if supportsGeneratedLoad(resourceType) {
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
	want := map[string]struct {
		from, to, fromType, toType string
	}{
		"qualification_issuer":       {"Practitioner/practitioner-1", "Organization/org-1", "Practitioner", "Organization"},
		"practitioner_qualification": {"Organization/org-1", "Practitioner/practitioner-1", "Organization", "Practitioner"},
	}
	found := map[string]bool{}
	counts := map[string]int{}
	for _, raw := range edges {
		var edge fhir.EdgeDocument
		if err := json.Unmarshal(raw, &edge); err != nil {
			t.Fatalf("decode generated edge: %v", err)
		}
		wantEdge, ok := want[edge.Label]
		if !ok {
			continue
		}
		found[edge.Label] = true
		counts[edge.Label]++
		if edge.From != wantEdge.from || edge.To != wantEdge.to || edge.FromType != wantEdge.fromType || edge.ToType != wantEdge.toType {
			t.Errorf("%s edge = %#v, want %s/%s %s -> %s", edge.Label, edge, wantEdge.fromType, wantEdge.toType, wantEdge.from, wantEdge.to)
		}
	}
	for label := range want {
		if !found[label] {
			t.Errorf("generated Practitioner edges missing %s", label)
		}
		if counts[label] != 1 {
			t.Errorf("generated Practitioner edge %s count = %d, want exactly one", label, counts[label])
		}
	}
}

func TestGeneratedNestedExtensionReferenceIsSchemaLegalAndCarriesProvenance(t *testing.T) {
	const extensionURL = "http://example.org/fhir/StructureDefinition/part-of-study"
	const resourceID = "patient-1"
	line := []byte(`{
  "resourceType": "Patient",
  "id": "patient-1",
  "extension": [
    {"url": "http://example.org/fhir/StructureDefinition/part-of-study", "valueReference": {"reference": "ResearchStudy/study-1"}},
    {"url": "urn:second-study-link", "valueReference": {"reference": "ResearchStudy/study-1"}}
  ],
  "modifierExtension": [
    {"url": "urn:modifier-study-link", "valueReference": {"reference": "ResearchStudy/study-1"}}
  ]
}`)
	vertex, firstEdges, _, err := loadRowGenerated("Patient", line, "project-1", map[string]float64{})
	if err != nil {
		t.Fatalf("loadRowGenerated(Patient): %v", err)
	}
	_, secondEdges, _, err := loadRowGenerated("Patient", line, "project-1", map[string]float64{})
	if err != nil {
		t.Fatalf("repeat loadRowGenerated(Patient): %v", err)
	}
	var rawPayload map[string]any
	if err := json.Unmarshal(vertex.Payload.(json.RawMessage), &rawPayload); err != nil {
		t.Fatalf("decode source payload: %v", err)
	}
	extensions, ok := rawPayload["extension"].([]any)
	if !ok {
		t.Fatalf("source payload extension = %#v, want original FHIR extension array", rawPayload["extension"])
	}
	referencePreserved := false
	for _, extensionValue := range extensions {
		extension, ok := extensionValue.(map[string]any)
		if !ok || extension["url"] != extensionURL {
			continue
		}
		valueReference, ok := extension["valueReference"].(map[string]any)
		if ok && valueReference["reference"] == "ResearchStudy/study-1" {
			referencePreserved = true
			break
		}
	}
	if !referencePreserved {
		t.Fatal("source payload lost the raw ResearchStudy Reference")
	}

	if _, ok := fhirschema.LookupTraversal("Patient", "extension_valueReference_ResearchStudy", "ResearchStudy"); !ok {
		t.Fatal("Patient.extension.valueReference -> ResearchStudy is missing generated schema traversal metadata")
	}
	if _, ok := fhirschema.LookupTraversal("Organization", "extension_valueReference_ResearchStudy", "ResearchStudy"); !ok {
		t.Fatal("nested extension traversal metadata was not composed for a second resource root")
	}
	if _, ok := fhirschema.LookupTraversal("Patient", "modifierExtension_valueReference_ResearchStudy", "ResearchStudy"); !ok {
		t.Fatal("modifierExtension route was not distinguished from extension in traversal metadata")
	}
	if _, ok := fhirschema.LookupTraversal("ResearchStudy", "extension_extension", "Patient"); !ok {
		t.Fatal("nested extension back-reference is missing generated schema traversal metadata")
	}
	_, organizationEdges, _, err := loadRowGenerated("Organization", []byte(`{
  "resourceType": "Organization",
  "id": "organization-1",
  "extension": [{"url": "urn:organization-study", "valueReference": {"reference": "ResearchStudy/study-1"}}]
}`), "project-1", map[string]float64{})
	if err != nil {
		t.Fatalf("loadRowGenerated(Organization): %v", err)
	}
	organizationEdgeFound := false
	for _, raw := range organizationEdges {
		var edge fhir.EdgeDocument
		if err := json.Unmarshal(raw, &edge); err != nil {
			t.Fatalf("decode Organization edge: %v", err)
		}
		if edge.Label == "extension_valueReference_ResearchStudy" && edge.From == "Organization/organization-1" && edge.To == "ResearchStudy/study-1" && edge.SourcePath == "Organization.extension[].valueReference.reference" {
			organizationEdgeFound = true
		}
	}
	if !organizationEdgeFound {
		t.Fatalf("generated Organization extension edge missing or lacks source path: %#v", organizationEdges)
	}

	firstNestedKeys := map[string]struct{}{}
	firstStudyEdges := map[string]fhir.EdgeDocument{}
	labels := map[string]int{}
	for _, raw := range firstEdges {
		var edge fhir.EdgeDocument
		if err := json.Unmarshal(raw, &edge); err != nil {
			t.Fatalf("decode generated edge: %v", err)
		}
		if edge.SourcePath != "" {
			firstNestedKeys[edge.Key] = struct{}{}
		}
		if edge.Label != "extension_valueReference_ResearchStudy" && edge.Label != "modifierExtension_valueReference_ResearchStudy" {
			continue
		}
		if edge.From != "Patient/"+resourceID || edge.To != "ResearchStudy/study-1" {
			t.Fatalf("nested extension edge endpoints = %s -> %s, want Patient/%s -> ResearchStudy/study-1", edge.From, edge.To, resourceID)
		}
		wantPath := "Patient.extension[].valueReference.reference"
		if edge.Label == "modifierExtension_valueReference_ResearchStudy" {
			wantPath = "Patient.modifierExtension[].valueReference.reference"
		}
		if edge.SourcePath != wantPath {
			t.Errorf("nested extension source_path = %q, want %s", edge.SourcePath, wantPath)
		}
		if edge.ExtensionURL == "" {
			t.Errorf("nested extension edge %q has no extension_url provenance", edge.Key)
		}
		firstStudyEdges[edge.Key] = edge
		labels[edge.Label]++
	}
	if len(firstStudyEdges) != 3 || labels["extension_valueReference_ResearchStudy"] != 2 || labels["modifierExtension_valueReference_ResearchStudy"] != 1 {
		t.Fatalf("generated nested ResearchStudy edges by source path = %#v, want two extension and one modifierExtension route", labels)
	}
	urls := map[string]struct{}{}
	for _, edge := range firstStudyEdges {
		urls[edge.ExtensionURL] = struct{}{}
	}
	if len(urls) != 3 {
		t.Fatalf("distinct extension provenance must have distinct idempotent edge identities: %#v", firstStudyEdges)
	}
	foundExpectedURL := false
	for _, edge := range firstStudyEdges {
		if edge.ExtensionURL == extensionURL {
			foundExpectedURL = true
		}
	}
	if !foundExpectedURL {
		t.Fatalf("generated edges missing part-of-study URL provenance %q: %#v", extensionURL, firstStudyEdges)
	}
	for _, raw := range secondEdges {
		var edge fhir.EdgeDocument
		if err := json.Unmarshal(raw, &edge); err != nil {
			t.Fatalf("decode repeated generated edge: %v", err)
		}
		if _, ok := firstNestedKeys[edge.Key]; !ok && edge.SourcePath != "" {
			t.Errorf("repeated extraction changed nested edge identity: %q", edge.Key)
		}
	}
}

func TestGeneratedDeepNestedReferenceTraversalIsSchemaLegal(t *testing.T) {
	const (
		edgeLabel    = "modifierExtension_extension_valueReference_ResearchStudy"
		sourcePath   = "Organization.modifierExtension[].extension[].valueReference.reference"
		extensionURL = "urn:deep-study-link"
	)
	line := []byte(`{
  "resourceType": "Organization",
  "id": "organization-deep",
  "modifierExtension": [{
    "url": "urn:outer-modifier",
    "extension": [{
      "url": "urn:deep-study-link",
      "valueReference": {"reference": "ResearchStudy/study-deep"}
    }]
  }]
}`)
	_, edges, _, err := loadRowGenerated("Organization", line, "project-1", map[string]float64{})
	if err != nil {
		t.Fatalf("loadRowGenerated(Organization): %v", err)
	}
	spec, ok := fhirschema.LookupTraversal("Organization", edgeLabel, "ResearchStudy")
	if !ok || spec.FromType != "Organization" || spec.ToType != "ResearchStudy" {
		t.Fatalf("deep nested Reference traversal = %#v, found %t", spec, ok)
	}
	for _, raw := range edges {
		var edge fhir.EdgeDocument
		if err := json.Unmarshal(raw, &edge); err != nil {
			t.Fatalf("decode generated edge: %v", err)
		}
		if edge.Label != edgeLabel {
			continue
		}
		if edge.From != "Organization/organization-deep" || edge.To != "ResearchStudy/study-deep" || edge.SourcePath != sourcePath || edge.ExtensionURL != extensionURL {
			t.Fatalf("deep nested edge = %#v, want source %q with URL %q", edge, sourcePath, extensionURL)
		}
		return
	}
	t.Fatalf("deep nested edge %q not found: %#v", edgeLabel, edges)
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

func TestGeneratedLoadRejectsMissingFHIRID(t *testing.T) {
	_, _, kind, err := loadRowGenerated("DocumentReference", []byte(`{
  "resourceType": "DocumentReference",
  "status": "current",
  "content": [{"attachment": {}}]
}`), "project-1", map[string]float64{})
	if kind != rowErrorValidation || err == nil || !strings.Contains(err.Error(), "DocumentReference payload missing string id") {
		t.Fatalf("loadRowGenerated() = kind %q err %v, want missing ID validation error", kind, err)
	}
}
