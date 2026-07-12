package ingest

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"

	"github.com/bmeg/jsonschemagraph/graph"
)

func TestGenericRowBuilderPropagatesAuthScopeToEdges(t *testing.T) {
	schema, err := graph.Load(repoPath(t, "schemas", "graph-fhir.json"))
	if err != nil {
		t.Fatal(err)
	}
	class := schema.GetClass("Specimen")
	if class == nil {
		t.Fatal("Specimen class is missing from checked-in graph schema")
	}
	line := firstFixtureLine(t, repoPath(t, "META", "Specimen.ndjson"))
	builder := NewGenericRowBuilder("P1", class, schema, graphExtraArgs("/programs/p1"))
	result, kind, err := builder.Build("Specimen", line, map[string]float64{})
	if err != nil || kind != "" {
		t.Fatalf("generic Build() kind=%q error=%v", kind, err)
	}
	if len(result.edges) == 0 {
		t.Fatal("fixture Specimen produced no graph edges")
	}
	for _, raw := range result.edges {
		var edge map[string]any
		if err := json.Unmarshal(raw, &edge); err != nil {
			t.Fatal(err)
		}
		if got := edge["auth_resource_path"]; got != "/programs/p1" {
			t.Fatalf("generic edge scope = %#v, want /programs/p1; edge=%s", got, raw)
		}
	}
}

func firstFixtureLine(t *testing.T, path string) []byte {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatalf("fixture %s is empty", path)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), scanner.Bytes()...)
}
