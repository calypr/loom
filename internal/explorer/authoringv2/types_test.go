package authoringv2

import (
	"strings"
	"testing"
)

func testCatalog() CatalogSnapshot {
	nodes := []CatalogNode{
		{ID: "patient", ResourceType: "Patient"},
		{ID: "encounter", ResourceType: "Encounter"},
	}
	edges := []CatalogEdge{
		{ID: "patient-encounter", FromNodeID: "patient", ToNodeID: "encounter", Label: "encounters"},
		{ID: "encounter-self", FromNodeID: "encounter", ToNodeID: "encounter", Label: "revisits"},
	}
	return CatalogSnapshot{
		APIVersion: APIVersion, Kind: CatalogKind, Project: "p", ExplorerID: "e",
		SourceGeneration: "g", AuthorizationScopeDigest: "scope", SnapshotToken: "sha256:snapshot",
		Complete: true, Nodes: nodes, Edges: edges,
		Candidates: []CatalogCandidate{
			{ID: "patient-id", NodeID: "patient", Label: "ID", LogicalType: "string", ProjectionModes: []string{"SCALAR"}, DefaultProjectionMode: "SCALAR"},
			{ID: "encounter-id", NodeID: "encounter", Label: "ID", LogicalType: "string", ProjectionModes: []string{"SCALAR"}, DefaultProjectionMode: "SCALAR"},
		},
		RoutePolicy: RoutePolicy{Unbounded: true, AllowRepeatedEdges: true, AllowSelfLoops: true},
	}
}

func testDocument() Document {
	return Document{
		APIVersion: APIVersion, Kind: Kind, Output: Output{ID: "out", Title: "Patients"}, RootNodeID: "patient",
		RouteSteps:   []RouteStep{{EdgeID: "patient-encounter", OccurrenceID: "first"}, {EdgeID: "encounter-self", OccurrenceID: "second"}, {EdgeID: "encounter-self", OccurrenceID: "third"}},
		Selections:   []Selection{{CandidateID: "patient-id", OccurrenceID: "base", ProjectionMode: "SCALAR"}, {CandidateID: "encounter-id", OccurrenceID: "third", ProjectionMode: "SCALAR"}},
		Presentation: map[string]Presentation{"patient-id": {Label: "Patient ID"}},
	}
}

func TestBuilderStateSupportsRepeatedOccurrencesSelfLoopsAndLongFiniteRoutes(t *testing.T) {
	catalog := testCatalog()
	document := testDocument()
	for i := 4; i < 12; i++ {
		document.RouteSteps = append(document.RouteSteps, RouteStep{EdgeID: "encounter-self", OccurrenceID: "step-explicit-" + string(rune('a'+i))})
	}
	state := BuilderState{APIVersion: APIVersion, Kind: StateKind, Document: &document, Catalog: catalog}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
	tail, err := document.TailOccurrence(catalog)
	if err != nil || tail.ID != "step-explicit-l" {
		t.Fatalf("tail=%#v err=%v", tail, err)
	}
}

func TestBuilderStateRejectsStaleAndDuplicateStructure(t *testing.T) {
	catalog := testCatalog()
	document := testDocument()
	document.RouteSteps = append(document.RouteSteps, RouteStep{EdgeID: "missing"})
	state := BuilderState{APIVersion: APIVersion, Kind: StateKind, Document: &document, Catalog: catalog}
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "stale edge") {
		t.Fatalf("stale edge error=%v", err)
	}
	document = testDocument()
	document.Selections = append(document.Selections, document.Selections[0])
	if err := document.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate selection") {
		t.Fatalf("duplicate selection error=%v", err)
	}
	catalog.Edges = append(catalog.Edges, catalog.Edges[0])
	if err := catalog.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate catalog edge") {
		t.Fatalf("duplicate edge error=%v", err)
	}
}

func TestStrictV2DecodeRejectsUnknownDuplicateAndTrailingFields(t *testing.T) {
	raw := `{"apiVersion":"` + APIVersion + `","kind":"ExplorerBuilderDocument","output":{"id":"out","title":"Title"},"rootNodeId":"patient","routeSteps":[],"selections":[],"presentation":{},"aql":"for x in c return x"}`
	if _, err := DecodeDocument([]byte(raw)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error=%v", err)
	}
	raw = strings.Replace(raw, `,"aql":"for x in c return x"`, `,"presentation":{},"presentation":{}`, 1)
	if _, err := DecodeDocument([]byte(raw)); err == nil || !strings.Contains(err.Error(), "duplicate JSON key") {
		t.Fatalf("duplicate field error=%v", err)
	}
	valid := `{"apiVersion":"` + APIVersion + `","kind":"ExplorerBuilderDocument","output":{"id":"out","title":"Title"},"rootNodeId":"patient","routeSteps":[],"selections":[],"presentation":{}} {}`
	if _, err := DecodeDocument([]byte(valid)); err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("trailing value error=%v", err)
	}
}

func TestCanonicalizationAndDigestAreStableAcrossUnorderedCollections(t *testing.T) {
	first := testDocument()
	second := testDocument()
	first.Selections = []Selection{first.Selections[1], first.Selections[0]}
	second.Presentation = map[string]Presentation{"patient-id": {Label: "Patient ID"}}
	first.Presentation = map[string]Presentation{"patient-id": {Label: "Patient ID"}}
	a, err := first.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	b, err := second.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("canonical JSON differs:\n%s\n%s", a, b)
	}
	da, err := first.Digest()
	if err != nil {
		t.Fatal(err)
	}
	db, err := second.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if da != db || !strings.HasPrefix(da, "sha256:") {
		t.Fatalf("digests=%q,%q", da, db)
	}
}

func TestEmptyBuilderStateAndBackendLeakage(t *testing.T) {
	state := BuilderState{APIVersion: APIVersion, Kind: StateKind, Catalog: testCatalog()}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := state.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"rowNodeId", "selector", "aql", "physicalCollection", "backend"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("backend field leaked: %q in %s", forbidden, raw)
		}
	}
}
