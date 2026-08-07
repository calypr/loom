package ingest

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/catalog"
	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	arangostore "github.com/calypr/loom/internal/store/arango"

	"github.com/bmeg/jsonschemagraph/graph"
	"github.com/bmeg/jsonschemagraph/util"
	"github.com/bytedance/sonic"
	publication "github.com/calypr/loom/internal/dataset"
)

func TestLoadAndQueryFixture(t *testing.T) {
	if os.Getenv("ARANGO_PROTO_INTEGRATION") == "" {
		t.Skip("set ARANGO_PROTO_INTEGRATION=1 to run Arango integration tests")
	}
	ctx := context.Background()
	fixtureDir := t.TempDir()
	sourceMetaDir := repoPath(t, "META")
	files, err := DiscoverNDJSON(sourceMetaDir)
	if err != nil {
		t.Fatalf("discover fixture source: %v", err)
	}
	expectedVertices := 0
	expectedEdges := 0
	schema, err := graph.Load(repoPath(t, "schemas", "graph-fhir.json"))
	if err != nil {
		t.Fatalf("load graph schema: %v", err)
	}
	for _, file := range files {
		resource := ResourceTypeFromPath(file)
		payload := copyFirstLineFixture(t, file, filepath.Join(fixtureDir, filepath.Base(file)))
		expectedVertices++
		class := schema.GetClass(resource)
		if class == nil {
			t.Fatalf("class %s not found", resource)
		}
		id, err := util.GetObjectID(payload, class)
		if err != nil {
			t.Fatalf("object id for %s: %v", resource, err)
		}
		edges, err := schema.BuildEdgesWithID(resource, id, payload, nil, true)
		if err != nil {
			t.Fatalf("edges for %s: %v", resource, err)
		}
		expectedEdges += len(edges)
	}

	for _, useGeneric := range []bool{true, false} {
		name := "Generated"
		if useGeneric {
			name = "Generic"
		}
		t.Run(name, func(t *testing.T) {
			generation, err := publication.NewRef("ARANGO_PROTO_TEST", strings.ToLower(name)+"-generation")
			if err != nil {
				t.Fatal(err)
			}
			database := "fhir_proto_int_" + strings.ToLower(name) + "_" + time.Now().Format("20060102150405")
			loadSummary, err := Load(ctx, LoadOptions{
				ConnectionOptions: arangostore.ConnectionOptions{
					URL:      "http://127.0.0.1:8529",
					Database: database,
				},
				Schema:        repoPath(t, "schemas", "graph-fhir.json"),
				MetaDir:       fixtureDir,
				Project:       "ARANGO_PROTO_TEST",
				Dataset:       &generation,
				BatchSize:     100,
				ProgressEvery: 1000,
				UseGeneric:    useGeneric,
			})
			if err != nil {
				t.Fatalf("load fixture: %v", err)
			}
			if loadSummary.VerticesInserted != expectedVertices {
				t.Fatalf("vertices inserted = %d, want %d", loadSummary.VerticesInserted, expectedVertices)
			}
			if loadSummary.EdgesInserted != expectedEdges {
				t.Fatalf("edges inserted = %d, want %d", loadSummary.EdgesInserted, expectedEdges)
			}
			for _, key := range []string{"bootstrap", "decode", "validate", "object_id", "edge_generation", "vertex_insert", "edge_insert"} {
				if _, ok := loadSummary.StageSeconds[key]; !ok {
					t.Fatalf("stage timing %q missing", key)
				}
			}

			client, err := arangostore.Open(ctx, "http://127.0.0.1:8529", database)
			if err != nil {
				t.Fatalf("open catalog client: %v", err)
			}
			defer client.Close(ctx)
			catalogStore, err := catalogarango.New(client)
			if err != nil {
				t.Fatalf("create catalog store: %v", err)
			}
			fields, err := catalogStore.DiscoverFields(ctx, catalog.PopulatedFieldOptions{
				Project:      "ARANGO_PROTO_TEST",
				ResourceType: "Condition",
				CursorBatch:  100,
			})
			if err != nil {
				t.Fatalf("discover populated fields: %v", err)
			}
			if len(fields) == 0 {
				t.Fatalf("discover populated fields returned no rows")
			}
		})
	}
}

func copyFirstLineFixture(t *testing.T, src, dst string) map[string]any {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatalf("open fixture source %s: %v", src, err)
	}
	defer in.Close()
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		t.Fatalf("fixture source %s is empty", src)
	}
	line := strings.TrimSpace(scanner.Text())
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan fixture source %s: %v", src, err)
	}
	if err := os.WriteFile(dst, []byte(line+"\n"), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", dst, err)
	}
	var payload map[string]any
	if err := sonic.ConfigFastest.Unmarshal([]byte(line), &payload); err != nil {
		t.Fatalf("decode fixture %s: %v", src, err)
	}
	return payload
}
