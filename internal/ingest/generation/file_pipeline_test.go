package generation

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/calypr/loom/internal/ingest/source"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestLoadFileSupportsGenerationAndCancellation(t *testing.T) {
	schema := source.LoadRuntimeFHIRSchema()
	file := filepath.Join(t.TempDir(), "Patient.ndjson")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	opts := normalizeLoadOptions(LoadOptions{Project: "project"})

	result, err := fileLoader{options: opts, client: nil, schema: schema, datasetGeneration: "generation-1", overwrite: false, insert: func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error {
		return nil
	}}.load(context.Background(), file, time.Now(), 0, 0)
	if err != nil {
		t.Fatalf("loadFile(): %v", err)
	}
	if result.ResourceType != "Patient" || result.Rows != 0 || result.Catalog == nil {
		t.Fatalf("loadFile() = %+v", result)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = fileLoader{options: opts, client: nil, schema: schema, datasetGeneration: "generation-1", overwrite: false, insert: func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error {
		return nil
	}}.load(ctx, file, time.Now(), 0, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("loadFile(canceled) error = %v, want context.Canceled", err)
	}
}

func TestLoadFilePreservesGenerationWrites(t *testing.T) {
	schema := source.LoadRuntimeFHIRSchema()
	file := filepath.Join(t.TempDir(), "Patient.ndjson")
	if err := os.WriteFile(file, []byte("{\"resourceType\":\"Patient\",\"id\":\"patient-1\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := normalizeLoadOptions(LoadOptions{Project: "project"})

	for _, test := range []struct {
		name, generation string
	}{
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
			result, err := fileLoader{options: opts, client: nil, schema: schema, datasetGeneration: test.generation, overwrite: false, insert: insert}.load(context.Background(), file, time.Now(), 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			if result.Rows != 1 || result.VerticesInserted != 1 || result.ValidationErrors != 0 || len(result.Catalog.Documents()) == 0 {
				t.Fatalf("result = %+v", result)
			}
			if len(collections) != 1 || collections[0] != "Patient" || len(overwrites) != 1 || overwrites[0] || len(documents) != 1 {
				t.Fatalf("writes collections=%v overwrite=%v documents=%d", collections, overwrites, len(documents))
			}
			var vertex map[string]any
			if err := json.Unmarshal(documents[0], &vertex); err != nil {
				t.Fatal(err)
			}
			if vertex["id"] != "patient-1" {
				t.Fatalf("logical id = %#v", vertex["id"])
			}
			if vertex["dataset_generation"] != test.generation {
				t.Fatalf("dataset_generation = %#v, want %q", vertex["dataset_generation"], test.generation)
			}
		})
	}
}

func TestLoadFileReportsPhysicalLinesAndBoundedErrors(t *testing.T) {
	schema := source.LoadRuntimeFHIRSchema()
	opts := normalizeLoadOptions(LoadOptions{Project: "project"})
	insert := func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error { return nil }

	t.Run("fail fast physical line", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "Patient.ndjson")
		if err := os.WriteFile(file, []byte("\n \n\t\n{\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		failFastOpts := opts
		failFastOpts.FailFast = true
		_, err := fileLoader{options: failFastOpts, client: nil, schema: schema, datasetGeneration: "generation-1", overwrite: false, insert: insert}.load(context.Background(), file, time.Now(), 0, 0)
		if err == nil || !strings.Contains(err.Error(), "Patient.ndjson Patient row 4:") {
			t.Fatalf("error = %v, want physical row 4", err)
		}
	})

	t.Run("earliest ten errors", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "Patient.ndjson")
		var input strings.Builder
		input.WriteString("\n")
		for i := 0; i < 12; i++ {
			if i == 1 {
				input.WriteString("\n")
			}
			input.WriteString("{\n")
		}
		if err := os.WriteFile(file, []byte(input.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		result, err := fileLoader{options: opts, client: nil, schema: schema, datasetGeneration: "generation-1", overwrite: false, insert: insert}.load(context.Background(), file, time.Now(), 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		wantLines := []int{2, 4, 5, 6, 7, 8, 9, 10, 11, 12}
		if result.ValidationErrors != 12 || len(result.RowErrors) != len(wantLines) {
			t.Fatalf("validation errors=%d samples=%d", result.ValidationErrors, len(result.RowErrors))
		}
		for i, sample := range result.RowErrors {
			if sample.File != "Patient.ndjson" || sample.ResourceType != "Patient" || sample.Category != "validation" || sample.Message == "" || sample.Line != wantLines[i] {
				t.Fatalf("sample[%d] = %+v, want line %d validation error", i, sample, wantLines[i])
			}
		}
	})
}

func TestLoadFilePreservesRowErrorsAcrossLineBatches(t *testing.T) {
	schema := source.LoadRuntimeFHIRSchema()
	file := filepath.Join(t.TempDir(), "Patient.ndjson")
	data := "{\"resourceType\":\"Patient\",\"id\":\"one\"}\n{\n" +
		"{\"resourceType\":\"Patient\",\"id\":\"three\"}\n" +
		"{\"resourceType\":\"Patient\",\"id\":\"four\"}\n"
	if err := os.WriteFile(file, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := normalizeLoadOptions(LoadOptions{Project: "project", LineQueueSize: 2, WorkerCount: 2})
	insert := func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error { return nil }
	result, err := fileLoader{options: opts, client: nil, schema: schema, datasetGeneration: "generation-1", overwrite: false, insert: insert}.load(context.Background(), file, time.Now(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != 3 || result.VerticesInserted != 3 || result.ValidationErrors != 1 || len(result.RowErrors) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if result.RowErrors[0].Line != 2 {
		t.Fatalf("error line = %d, want physical line 2", result.RowErrors[0].Line)
	}
}

func TestLoadFileStopsAtCancellationBetweenLinesInABatch(t *testing.T) {
	schema := source.LoadRuntimeFHIRSchema()
	file := filepath.Join(t.TempDir(), "Patient.ndjson")
	data := "{\"resourceType\":\"Patient\",\"id\":\"one\"}\n" +
		"{\"resourceType\":\"Patient\",\"id\":\"two\"}\n" +
		"{\"resourceType\":\"Patient\",\"id\":\"three\"}\n"
	if err := os.WriteFile(file, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	progressEvents := 0
	opts := normalizeLoadOptions(LoadOptions{
		Project:       "project",
		LineQueueSize: 8,
		WorkerCount:   1,
		ProgressEvery: 1,
		EventSink: func(event string, fields map[string]any) {
			if event == "go_load_progress" {
				progressEvents++
				if progressEvents == 1 {
					cancel()
				}
			}
		},
	})
	workerBudget := make(chan struct{}, 1)
	opts.fileWorkerBudget = workerBudget
	insert := func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error { return nil }
	_, err := fileLoader{options: opts, client: nil, schema: schema, datasetGeneration: "generation-1", overwrite: false, insert: insert}.load(ctx, file, time.Now(), 0, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("loadFile error = %v, want context.Canceled", err)
	}
	if progressEvents != 1 {
		t.Fatalf("progress events = %d, want only the completed first row", progressEvents)
	}
	if len(workerBudget) != 0 {
		t.Fatalf("worker permit leaked after cancellation: %d", len(workerBudget))
	}
}

func TestLoadFileBatchesAndPropagatesWriterFailure(t *testing.T) {
	schema := source.LoadRuntimeFHIRSchema()
	file := filepath.Join(t.TempDir(), "Patient.ndjson")
	data := []byte("{\"resourceType\":\"Patient\",\"id\":\"patient-1\"}\n{\"resourceType\":\"Patient\",\"id\":\"patient-2\"}\n{\"resourceType\":\"Patient\",\"id\":\"patient-3\"}\n")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	opts := normalizeLoadOptions(LoadOptions{Project: "project", BatchSize: 1, WriterCount: 2})
	var inserted int64
	result, err := fileLoader{options: opts, client: nil, schema: schema, datasetGeneration: "generation-1", overwrite: false, insert: func(_ context.Context, _ *arangostore.Client, _ string, docs []json.RawMessage, _ bool, _ string) error {
		atomic.AddInt64(&inserted, int64(len(docs)))
		return nil
	}}.load(context.Background(), file, time.Now(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.VerticesInserted != 3 || result.VertexBatches != 3 || atomic.LoadInt64(&inserted) != 3 {
		t.Fatalf("result=%+v inserted=%d", result, inserted)
	}

	writeErr := errors.New("writer failed")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	writerBudget := make(chan struct{}, 1)
	budgetedOpts := opts
	budgetedOpts.fileWriterBudget = writerBudget
	_, err = fileLoader{options: budgetedOpts, client: nil, schema: schema, datasetGeneration: "generation-1", overwrite: false, insert: func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error {
		return writeErr
	}}.load(ctx, file, time.Now(), 0, 0)
	if !errors.Is(err, writeErr) {
		t.Fatalf("error = %v, want %v", err, writeErr)
	}
	if len(writerBudget) != 0 {
		t.Fatalf("writer permit leaked after insert error: %d permits held", len(writerBudget))
	}
}
