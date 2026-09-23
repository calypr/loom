package ingest

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strconv"
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
			inventory, err := catalogStore.PageSemanticInventory(ctx, catalog.SemanticInventoryPageOptions{
				Project:                       generation.Project,
				DatasetGeneration:             generation.Generation,
				AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
				Limit:                         1,
			})
			if err != nil {
				t.Fatalf("read completed semantic inventory: %v", err)
			}
			if got, want := inventory.Build.SourceAvailability, catalog.SemanticInventorySourceAvailabilityVerified; got != want {
				t.Fatalf("semantic inventory source availability = %q, want %q", got, want)
			}
		})
	}
}

func TestGenerationLoadPrunesDanglingNestedReferenceTargets(t *testing.T) {
	if os.Getenv("ARANGO_PROTO_INTEGRATION") == "" {
		t.Skip("set ARANGO_PROTO_INTEGRATION=1 to run Arango integration tests")
	}
	ctx := context.Background()
	metaDir := t.TempDir()
	patientFile := filepath.Join(metaDir, "Patient.ndjson")
	researchStudyFile := filepath.Join(metaDir, "ResearchStudy.ndjson")
	patientPayload := `{"resourceType":"Patient","id":"patient-present","extension":[{"url":"urn:part-of-study","valueReference":{"reference":"ResearchStudy/study-present"}},{"url":"urn:missing-study","valueReference":{"reference":"ResearchStudy/study-missing"}}]}`
	researchStudyPayload := `{"resourceType":"ResearchStudy","id":"study-present","status":"active"}`
	if err := os.WriteFile(patientFile, []byte(patientPayload+"\n"), 0o644); err != nil {
		t.Fatalf("write Patient fixture: %v", err)
	}
	if err := os.WriteFile(researchStudyFile, []byte(researchStudyPayload+"\n"), 0o644); err != nil {
		t.Fatalf("write ResearchStudy fixture: %v", err)
	}

	project := "ARANGO_PROTO_TEST"
	generation, err := publication.NewRef(project, "nested-reference-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err != nil {
		t.Fatalf("create generation reference: %v", err)
	}
	database := "fhir_nested_ref_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	summary, err := Load(ctx, LoadOptions{
		ConnectionOptions: arangostore.ConnectionOptions{URL: "http://127.0.0.1:8529", Database: database},
		Schema:            repoPath(t, "schemas", "graph-fhir.json"),
		MetaDir:           metaDir,
		Project:           project,
		Dataset:           &generation,
		BatchSize:         100,
	})
	if err != nil {
		t.Fatalf("load nested-reference generation: %v", err)
	}
	if got, want := summary.EdgesInserted, 2; got != want {
		t.Fatalf("traversable edges inserted = %d, want %d edges for the one resolved reference", got, want)
	}

	client, err := arangostore.Open(ctx, "http://127.0.0.1:8529", database)
	if err != nil {
		t.Fatalf("open loaded graph: %v", err)
	}
	defer client.Close(ctx)
	var edges []map[string]any
	err = client.QueryRows(ctx, `
FOR edge IN fhir_edge
  FILTER edge.project == @project AND edge.dataset_generation == @generation
  LET source = DOCUMENT(edge._from)
  LET target = DOCUMENT(edge._to)
  RETURN {
    from_type: edge.from_type,
    to_type: edge.to_type,
    label: edge.label,
    source_id: source.id,
    target_id: target.id,
    source_path: edge.source_path,
    extension_url: edge.extension_url
  }`, 100, map[string]any{"project": project, "generation": generation.Generation}, func(row map[string]any) error {
		edges = append(edges, row)
		return nil
	})
	if err != nil {
		t.Fatalf("query loaded nested-reference edges: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("persisted nested-reference edges = %d, want forward/backref only for the existing target: %#v", len(edges), edges)
	}
	for _, edge := range edges {
		if edge["source_path"] != "Patient.extension[].valueReference.reference" {
			t.Errorf("persisted source_path = %#v, want Patient.extension[].valueReference.reference", edge["source_path"])
		}
		if edge["extension_url"] != "urn:part-of-study" {
			t.Errorf("persisted extension_url = %#v, want urn:part-of-study", edge["extension_url"])
		}
		if edge["source_id"] == "patient-present" && edge["target_id"] != "study-present" {
			t.Errorf("Patient edge target = %#v, want existing study-present", edge["target_id"])
		}
		if edge["target_id"] == "patient-present" && edge["source_id"] != "study-present" {
			t.Errorf("back-reference source = %#v, want existing study-present", edge["source_id"])
		}
		if edge["source_id"] == "patient-present" && edge["target_id"] == "study-missing" || edge["source_id"] == "study-missing" {
			t.Errorf("dangling target was persisted as an edge: %#v", edge)
		}
	}

	catalogStore, err := catalogarango.New(client)
	if err != nil {
		t.Fatalf("create catalog store: %v", err)
	}
	references, err := catalogStore.DiscoverReferences(ctx, catalog.PopulatedReferenceOptions{
		Project:           project,
		DatasetGeneration: generation.Generation,
		CursorBatch:       100,
	})
	if err != nil {
		t.Fatalf("discover populated nested references: %v", err)
	}
	var resolvedCount int64
	for _, reference := range references {
		if reference.FromType == "Patient" && reference.Label == "extension_valueReference_ResearchStudy" && reference.ToType == "ResearchStudy" {
			resolvedCount += reference.EdgeCount
		}
	}
	if resolvedCount != 1 {
		t.Fatalf("relationship catalog reports %d Patient-to-ResearchStudy links, want exactly one resolved target", resolvedCount)
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
