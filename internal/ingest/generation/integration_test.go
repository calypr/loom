package generation

import (
	"context"
	"github.com/calypr/loom/internal/catalog/fieldcatalog"
	"os"
	"path/filepath"
	"testing"
	"time"

	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	publication "github.com/calypr/loom/internal/dataset"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestLoadAndQueryFixture(t *testing.T) {
	if os.Getenv("ARANGO_PROTO_INTEGRATION") == "" {
		t.Skip("set ARANGO_PROTO_INTEGRATION=1 to run Arango integration tests")
	}
	ctx := context.Background()
	fixtureDir := t.TempDir()
	fixtures := map[string]string{
		"Patient.ndjson":   `{"resourceType":"Patient","id":"patient-1","gender":"female"}`,
		"Condition.ndjson": `{"resourceType":"Condition","id":"condition-1","clinicalStatus":{"text":"active"},"subject":{"reference":"Patient/patient-1"}}`,
	}
	for name, line := range fixtures {
		if err := os.WriteFile(filepath.Join(fixtureDir, name), []byte(line+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	generation, err := publication.NewRef("ARANGO_PROTO_TEST", "generated-generation")
	if err != nil {
		t.Fatal(err)
	}
	database := "fhir_proto_int_generated_" + time.Now().Format("20060102150405")
	loadSummary, err := Load(ctx, LoadOptions{
		ConnectionOptions: arangostore.ConnectionOptions{URL: "http://127.0.0.1:8529", Database: database},
		MetaDir:           fixtureDir,
		Project:           "ARANGO_PROTO_TEST",
		Dataset:           &generation,
		BatchSize:         100,
		ProgressEvery:     1000,
	})
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	if loadSummary.VerticesInserted != 2 {
		t.Fatalf("vertices inserted = %d, want 2", loadSummary.VerticesInserted)
	}
	if loadSummary.EdgesInserted != 1 {
		t.Fatalf("edges inserted = %d, want 1 for one subject reference", loadSummary.EdgesInserted)
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
	fields, err := catalogStore.DiscoverFields(ctx, fieldcatalog.PopulatedFieldOptions{
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
}
