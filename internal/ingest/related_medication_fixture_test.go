package ingest

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	generatedfhir "github.com/calypr/loom/generated/fhir"
)

type medicationFixtureScope struct {
	Project    string `json:"project"`
	Generation string `json:"generation"`
}

type medicationFixtureDoc struct {
	ID           string
	ResourceType string
	Value        string
	Project      string
	Generation   string
}

type medicationFixtureEdge struct {
	From       string `json:"_from"`
	To         string `json:"_to"`
	Label      string `json:"label"`
	Project    string `json:"project"`
	FromType   string `json:"from_type"`
	ToType     string `json:"to_type"`
	Generation string `json:"dataset_generation"`
}

type medicationFixtureEdgeContract struct {
	From, To, Label, FromType, ToType string
}

type medicationFixtureRoute struct {
	Specimen, Observation, Condition, Patient string
	Administration, Medication, Value         string
}

func TestCDAZeroColumnMedicationPositiveFixture(t *testing.T) {
	scope, docs, edges := readMedicationFixture(t)
	if len(docs) != 9 {
		t.Fatalf("want eight route documents and one unmatched Specimen, got %d", len(docs))
	}

	wantEdges := []medicationFixtureEdgeContract{
		{"Observation/route-observation", "Specimen/positive-specimen", "focus_Specimen", "Observation", "Specimen"},
		{"Condition/route-condition", "Observation/route-observation", "stage_assessment_Observation", "Condition", "Observation"},
		{"Condition/route-condition", "Patient/route-patient", "subject_Patient", "Condition", "Patient"},
		{"MedicationAdministration/route-admin-a", "Patient/route-patient", "subject_Patient", "MedicationAdministration", "Patient"},
		{"MedicationAdministration/route-admin-b", "Patient/route-patient", "subject_Patient", "MedicationAdministration", "Patient"},
		{"MedicationAdministration/route-admin-a", "Medication/medication-a", "medication_reference_Medication", "MedicationAdministration", "Medication"},
		{"MedicationAdministration/route-admin-b", "Medication/medication-b", "medication_reference_Medication", "MedicationAdministration", "Medication"},
	}
	if got := fixtureEdgeContracts(edges); !reflect.DeepEqual(got, sortMedicationFixtureEdges(wantEdges)) {
		t.Fatalf("generated FHIR extractors returned wrong typed route edges:\n got: %#v\nwant: %#v", got, sortMedicationFixtureEdges(wantEdges))
	}
	for _, edge := range edges {
		if edge.Project != scope.Project || edge.Generation != scope.Generation {
			t.Fatalf("edge escaped project/generation scope: %#v", edge)
		}
		if !isMedicationRouteLabel(edge.Label) {
			continue
		}
		from, fromOK := docs[edge.From]
		to, toOK := docs[edge.To]
		if !fromOK || !toOK || from.ResourceType != edge.FromType || to.ResourceType != edge.ToType ||
			from.Project != scope.Project || from.Generation != scope.Generation ||
			to.Project != scope.Project || to.Generation != scope.Generation {
			t.Fatalf("typed edge endpoints must be in fixture scope: %#v", edge)
		}
	}

	want := []medicationFixtureRoute{
		{"Specimen/positive-specimen", "Observation/route-observation", "Condition/route-condition", "Patient/route-patient", "MedicationAdministration/route-admin-a", "Medication/medication-a", "Medication A"},
		{"Specimen/positive-specimen", "Observation/route-observation", "Condition/route-condition", "Patient/route-patient", "MedicationAdministration/route-admin-b", "Medication/medication-b", "Medication B"},
	}
	if got := medicationFixtureRoutes("Specimen/positive-specimen", docs, edges, scope); !reflect.DeepEqual(got, want) {
		t.Fatalf("positive route IDs, values, or multiplicity differ:\n got: %#v\nwant: %#v", got, want)
	}
	if got := medicationFixtureRoutes("Specimen/unmatched-specimen", docs, edges, scope); len(got) != 0 {
		t.Fatalf("unmatched parent must remain available for PRESERVE_PARENT/EXCLUDE contrast, got %#v", got)
	}
}

func readMedicationFixture(t *testing.T) (medicationFixtureScope, map[string]medicationFixtureDoc, []medicationFixtureEdge) {
	t.Helper()
	dir := filepath.Join("..", "..", "testdata", "cda-zero-column-related-medication-positive")
	scopeJSON, err := os.ReadFile(filepath.Join(dir, "scope.json"))
	if err != nil {
		t.Fatal(err)
	}
	var scope medicationFixtureScope
	if err := json.Unmarshal(scopeJSON, &scope); err != nil || scope.Project == "" || scope.Generation == "" {
		t.Fatalf("invalid project/generation scope %s: %v", scopeJSON, err)
	}
	docs := make(map[string]medicationFixtureDoc)
	var edges []medicationFixtureEdge
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ndjson") {
			continue
		}
		file, err := os.Open(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(file)
		for line := 1; scanner.Scan(); line++ {
			var source struct {
				ResourceType string `json:"resourceType"`
				ID           string `json:"id"`
				Code         struct {
					Text string `json:"text"`
				} `json:"code"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &source); err != nil || source.ID == "" || source.ResourceType == "" {
				file.Close()
				t.Fatalf("invalid %s:%d source: %v", entry.Name(), line, err)
			}
			key := source.ResourceType + "/" + source.ID
			if _, exists := docs[key]; exists {
				file.Close()
				t.Fatalf("duplicate fixture document %s", key)
			}
			docs[key] = medicationFixtureDoc{source.ID, source.ResourceType, source.Code.Text, scope.Project, scope.Generation}
			for _, raw := range extractMedicationFixtureEdges(t, source.ResourceType, scanner.Bytes(), scope.Project) {
				var edge medicationFixtureEdge
				if err := json.Unmarshal(raw, &edge); err != nil {
					file.Close()
					t.Fatal(err)
				}
				edge.Generation = scope.Generation // Dataset loaders attach this scope to extracted edges.
				edges = append(edges, edge)
			}
		}
		if err := scanner.Err(); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return scope, docs, edges
}

func extractMedicationFixtureEdges(t *testing.T, resourceType string, raw []byte, project string) []json.RawMessage {
	t.Helper()
	switch resourceType {
	case "Condition":
		var resource generatedfhir.Condition
		if err := json.Unmarshal(raw, &resource); err != nil {
			t.Fatal(err)
		}
		edges, err := resource.ExtractEdges(project)
		if err != nil {
			t.Fatal(err)
		}
		return edges
	case "MedicationAdministration":
		var resource generatedfhir.MedicationAdministration
		if err := json.Unmarshal(raw, &resource); err != nil {
			t.Fatal(err)
		}
		edges, err := resource.ExtractEdges(project)
		if err != nil {
			t.Fatal(err)
		}
		return edges
	case "Observation":
		var resource generatedfhir.Observation
		if err := json.Unmarshal(raw, &resource); err != nil {
			t.Fatal(err)
		}
		edges, err := resource.ExtractEdges(project)
		if err != nil {
			t.Fatal(err)
		}
		return edges
	default:
		return nil
	}
}

func medicationFixtureRoutes(specimen string, docs map[string]medicationFixtureDoc, edges []medicationFixtureEdge, scope medicationFixtureScope) []medicationFixtureRoute {
	var routes []medicationFixtureRoute
	for _, focus := range scopedMedicationEdges(edges, "", specimen, "Observation", "Specimen", "focus_Specimen", scope) {
		for _, stage := range scopedMedicationEdges(edges, "", focus.From, "Condition", "Observation", "stage_assessment_Observation", scope) {
			for _, conditionSubject := range scopedMedicationEdges(edges, stage.From, "", "Condition", "Patient", "subject_Patient", scope) {
				for _, administrationSubject := range scopedMedicationEdges(edges, "", conditionSubject.To, "MedicationAdministration", "Patient", "subject_Patient", scope) {
					for _, medication := range scopedMedicationEdges(edges, administrationSubject.From, "", "MedicationAdministration", "Medication", "medication_reference_Medication", scope) {
						doc, exists := docs[medication.To]
						if exists && docs[specimen].ResourceType == "Specimen" {
							routes = append(routes, medicationFixtureRoute{specimen, focus.From, stage.From, conditionSubject.To, administrationSubject.From, medication.To, doc.Value})
						}
					}
				}
			}
		}
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Medication < routes[j].Medication })
	return routes
}

func scopedMedicationEdges(edges []medicationFixtureEdge, from, to, fromType, toType, label string, scope medicationFixtureScope) []medicationFixtureEdge {
	var matches []medicationFixtureEdge
	for _, edge := range edges {
		if (from == "" || edge.From == from) && (to == "" || edge.To == to) &&
			edge.FromType == fromType && edge.ToType == toType && edge.Label == label &&
			edge.Project == scope.Project && edge.Generation == scope.Generation {
			matches = append(matches, edge)
		}
	}
	return matches
}

func fixtureEdgeContracts(edges []medicationFixtureEdge) []medicationFixtureEdgeContract {
	var out []medicationFixtureEdgeContract
	for _, edge := range edges {
		if isMedicationRouteLabel(edge.Label) {
			out = append(out, medicationFixtureEdgeContract{edge.From, edge.To, edge.Label, edge.FromType, edge.ToType})
		}
	}
	return sortMedicationFixtureEdges(out)
}

func isMedicationRouteLabel(label string) bool {
	return label == "focus_Specimen" || label == "stage_assessment_Observation" || label == "subject_Patient" || label == "medication_reference_Medication"
}

func sortMedicationFixtureEdges(edges []medicationFixtureEdgeContract) []medicationFixtureEdgeContract {
	sort.Slice(edges, func(i, j int) bool {
		return edges[i].From+"\x00"+edges[i].Label+"\x00"+edges[i].To < edges[j].From+"\x00"+edges[j].Label+"\x00"+edges[j].To
	})
	return edges
}
