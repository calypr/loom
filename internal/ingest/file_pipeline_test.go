package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bmeg/jsonschemagraph/graph"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestLoadFileSupportsLegacyGenerationAndCancellation(t *testing.T) {
	schema, err := graph.Load(repoPath(t, "schemas", "graph-fhir.json"))
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "Patient.ndjson")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	opts := normalizeLoadOptions(LoadOptions{Project: "project"})

	for _, generation := range []string{"", "generation-1"} {
		result, err := loadFile(
			context.Background(),
			opts,
			nil,
			schema,
			file,
			generation,
			generation == "",
			time.Now(),
			0,
			0,
			func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error {
				return nil
			},
		)
		if err != nil {
			t.Fatalf("loadFile(generation=%q): %v", generation, err)
		}
		if result.ResourceType != "Patient" || result.Rows != 0 || result.Catalog == nil {
			t.Fatalf("loadFile(generation=%q) = %+v", generation, result)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = loadFile(ctx, opts, nil, schema, file, "", true, time.Now(), 0, 0, func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error {
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("loadFile(canceled) error = %v, want context.Canceled", err)
	}
}

func TestLoadFilePreservesLegacyAndGenerationWrites(t *testing.T) {
	schema, err := graph.Load(repoPath(t, "schemas", "graph-fhir.json"))
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "Patient.ndjson")
	if err := os.WriteFile(file, []byte("{\"resourceType\":\"Patient\",\"id\":\"patient-1\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := normalizeLoadOptions(LoadOptions{Project: "project"})

	for _, test := range []struct {
		name, generation string
		overwrite        bool
	}{
		{name: "legacy", overwrite: true},
		{name: "generation", generation: "generation-1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var mu sync.Mutex
			var documents []json.RawMessage
			var collections []string
			var overwrites []bool
			insert := func(_ context.Context, _ *arangostore.Client, collection string, docs []json.RawMessage, overwrite bool, _ string) error {
				mu.Lock()
				defer mu.Unlock()
				collections = append(collections, collection)
				overwrites = append(overwrites, overwrite)
				documents = append(documents, docs...)
				return nil
			}
			result, err := loadFile(context.Background(), opts, nil, schema, file, test.generation, test.overwrite, time.Now(), 0, 0, insert)
			if err != nil {
				t.Fatal(err)
			}
			if result.Rows != 1 || result.VerticesInserted != 1 || result.ValidationErrors != 0 || len(result.Catalog.Documents()) == 0 {
				t.Fatalf("result = %+v", result)
			}
			if len(collections) != 1 || collections[0] != "Patient" || len(overwrites) != 1 || overwrites[0] != test.overwrite || len(documents) != 1 {
				t.Fatalf("writes collections=%v overwrite=%v documents=%d", collections, overwrites, len(documents))
			}
			var vertex map[string]any
			if err := json.Unmarshal(documents[0], &vertex); err != nil {
				t.Fatal(err)
			}
			if vertex["id"] != "patient-1" {
				t.Fatalf("logical id = %#v", vertex["id"])
			}
			if test.generation == "" {
				if _, exists := vertex["dataset_generation"]; exists {
					t.Fatalf("legacy vertex has dataset_generation: %#v", vertex)
				}
			} else if vertex["dataset_generation"] != test.generation {
				t.Fatalf("dataset_generation = %#v, want %q", vertex["dataset_generation"], test.generation)
			}
		})
	}
}
