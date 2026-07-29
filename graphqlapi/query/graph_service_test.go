package queryapi

import (
	"encoding/json"
	"testing"

	"github.com/calypr/loom/graphqlapi/model"
)

func TestNormalizeFHIRGraphQueryDefaultsRequiredAndLimit(t *testing.T) {
	query, err := normalizeFHIRGraphQuery(FHIRGraphQuery{
		Project: "p", RootResourceType: "Patient",
		Traverse: []FHIRGraphTraversal{{EdgeLabel: "subject", ToResourceType: "Specimen", Alias: "specimen"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if query.Limit != FhirGraphDefaultLimit {
		t.Fatalf("limit = %d, want %d", query.Limit, FhirGraphDefaultLimit)
	}
	if query.Traverse[0].MatchMode != model.FhirTraversalMatchModeRequired {
		t.Fatalf("match mode = %q, want REQUIRED", query.Traverse[0].MatchMode)
	}
}

func TestNormalizeFHIRGraphQueryBounds(t *testing.T) {
	for _, limit := range []int{-1, FhirGraphMaxLimit + 1} {
		if _, err := normalizeFHIRGraphQuery(FHIRGraphQuery{Project: "p", RootResourceType: "Patient", Limit: limit}); err == nil {
			t.Errorf("limit %d accepted", limit)
		}
	}
	deep := FHIRGraphTraversal{EdgeLabel: "e", ToResourceType: "Patient", Alias: "a"}
	for i := 0; i < FhirGraphMaxDepth; i++ {
		deep = FHIRGraphTraversal{EdgeLabel: "e", ToResourceType: "Patient", Alias: "a", Traverse: []FHIRGraphTraversal{deep}}
	}
	if _, err := normalizeFHIRGraphQuery(FHIRGraphQuery{Project: "p", RootResourceType: "Patient", Traverse: []FHIRGraphTraversal{deep}}); err == nil {
		t.Fatal("depth overflow accepted")
	}
}

func TestDecodeGraphPathSanitizesStorageMetadata(t *testing.T) {
	row := map[string]any{"path": map[string]any{
		"terminalAlias": "specimen",
		"nodes": []any{map[string]any{
			"alias": "patient", "resourceType": "Patient", "id": "p1",
			"payload": map[string]any{"resourceType": "Patient", "id": "p1", "project": "secret", "auth_resource_path": "/x"},
		}},
		"relationships": []any{map[string]any{"alias": "specimen", "label": "subject", "fromResourceType": "Patient", "toResourceType": "Specimen"}},
	}}
	path, ok, err := decodeGraphPath(row)
	if err != nil || !ok {
		t.Fatalf("decode failed: ok=%v err=%v", ok, err)
	}
	if _, exists := path.Nodes[0].Resource["project"]; exists {
		t.Fatal("storage project leaked")
	}
	if len(path.Relationships) != 1 || path.TerminalAlias != "specimen" {
		t.Fatalf("unexpected path: %#v", path)
	}
	encoded, _ := json.Marshal(path.Nodes[0].Resource)
	if string(encoded) == "" {
		t.Fatal("empty resource")
	}
}
