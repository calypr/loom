package ingest

import (
	"sync"
	"testing"

	"github.com/bmeg/jsonschemagraph/graph"
)

func TestGenericRowBuilderIsSafeForParallelWorkers(t *testing.T) {
	schema, err := graph.Load(repoPath(t, "schemas", "graph-fhir.json"))
	if err != nil {
		t.Fatal(err)
	}
	const resourceType = "SubstanceDefinition"
	class := schema.GetClass(resourceType)
	if class == nil {
		t.Fatalf("class %q not found", resourceType)
	}
	extraArgs := map[string]any{"auth_resource_path": "HTAN_INT-BForePC"}
	builder := NewGenericRowBuilder("HTAN_INT-BForePC", class, schema, extraArgs)
	line := []byte(`{"resourceType":"SubstanceDefinition","id":"substance-1","name":[{"name":"Gemcitabine"}]}`)

	const workers = 32
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _, buildErr := builder.Build(resourceType, line, map[string]float64{})
			errs <- buildErr
		}()
	}
	wait.Wait()
	close(errs)
	for buildErr := range errs {
		if buildErr != nil {
			t.Fatal(buildErr)
		}
	}
	if got := extraArgs["auth_resource_path"]; got != "HTAN_INT-BForePC" {
		t.Fatalf("shared extra arguments were mutated: %#v", extraArgs)
	}
}
