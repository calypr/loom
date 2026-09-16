package authoringv2

import (
	"encoding/json"
	"strings"
	"testing"
)

func testCatalog() CatalogSnapshot {
	nodes := []CatalogNode{
		{ID: "patient", ResourceType: "Patient", RowRootEligible: true},
		{ID: "encounter", ResourceType: "Encounter", RowRootEligible: true},
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

func TestDecodeStrictJSONRejectsUnknownAndDuplicateFields(t *testing.T) {
	var value struct {
		Title string `json:"title"`
	}
	for name, raw := range map[string]string{
		"unknown":   `{"title":"Patients","extra":true}`,
		"duplicate": `{"title":"Patients","title":"Other"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := DecodeStrictJSON([]byte(raw), &value); err == nil {
				t.Fatalf("accepted invalid JSON: %s", raw)
			}
		})
	}
}

func TestWorkspaceDatasetDesignMetadataRequiresMatchingPair(t *testing.T) {
	const digest = "sha256:43258cff783fe7036d8a43033f830adfc60ec037382473548ac742b888292777"
	valid := emptyCommandWorkspace()
	valid.DatasetDesign = json.RawMessage(` { "b": 2, "a": 1 } `)
	valid.DatasetDesignDigest = digest
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	canonical, err := valid.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	compact := emptyCommandWorkspace()
	compact.DatasetDesign = json.RawMessage(`{"a":1,"b":2}`)
	compact.DatasetDesignDigest = digest
	compactCanonical, err := compact.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) != string(compactCanonical) {
		t.Fatalf("equivalent design JSON changed workspace canonical form:\n%s\n%s", canonical, compactCanonical)
	}
	for name, workspace := range map[string]Workspace{
		"missing payload": func() Workspace {
			value := emptyCommandWorkspace()
			value.DatasetDesignDigest = digest
			return value
		}(),
		"missing digest": func() Workspace {
			value := emptyCommandWorkspace()
			value.DatasetDesign = json.RawMessage(`{"a":1,"b":2}`)
			return value
		}(),
		"mismatched digest": func() Workspace {
			value := compact
			value.DatasetDesignDigest = "sha256:wrong"
			return value
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := workspace.Validate(); err == nil {
				t.Fatalf("accepted invalid metadata pair: %#v", workspace)
			}
		})
	}
}
