package generation

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/calypr/loom/internal/ingest/bootstrap"
	"github.com/calypr/loom/internal/ingest/source"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/semantic"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type ingestPerfVolume struct {
	Docs  int64 `json:"docs"`
	Bytes int64 `json:"bytes"`
}

type ingestPerfResult struct {
	Wall             float64                     `json:"wall_seconds"`
	Rows             int                         `json:"rows"`
	RowsPerSecond    float64                     `json:"rows_per_second"`
	Edges            int                         `json:"edges"`
	ValidationErrors int                         `json:"validation_errors"`
	GenerationErrors int                         `json:"generation_errors"`
	EdgeErrors       int                         `json:"edge_errors"`
	Stages           map[string]float64          `json:"stage_worker_seconds"`
	Volumes          map[string]ingestPerfVolume `json:"volumes"`
}

func TestIngestPerf(t *testing.T) {
	meta := os.Getenv("LOOM_INGEST_PERF_META")
	if meta == "" {
		t.Skip("set LOOM_INGEST_PERF_META to run the ingestion benchmark")
	}
	files, err := source.DiscoverNDJSON(meta)
	if err != nil || len(files) == 0 {
		t.Fatalf("discover files in %q: %d files, %v", meta, len(files), err)
	}
	capture := os.Getenv("LOOM_INGEST_PERF_CAPTURE")
	outputs := map[string]*bufio.Writer{}
	handles := map[string]*os.File{}
	if capture != "" {
		if err = os.MkdirAll(capture, 0755); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			for name, output := range outputs {
				if err := output.Flush(); err != nil {
					t.Error(err)
				}
				if err := handles[name].Close(); err != nil {
					t.Error(err)
				}
			}
		})
	}
	result := ingestPerfResult{Stages: map[string]float64{}, Volumes: map[string]ingestPerfVolume{}}
	var mu sync.Mutex
	insert := func(_ context.Context, _ *arangostore.Client, collection string, docs []json.RawMessage, _ bool, _ string) error {
		var bytes int64
		for _, doc := range docs {
			bytes += int64(len(doc))
		}
		class := "vertices"
		if collection == bootstrap.EdgeCollection || collection == semantic.Collection || collection == semantic.EntryCollection {
			class = collection
		}
		mu.Lock()
		defer mu.Unlock()
		volume := result.Volumes[class]
		volume.Bytes += bytes
		volume.Docs += int64(len(docs))
		result.Volumes[class] = volume
		if capture == "" {
			return nil
		}
		output := outputs[collection]
		if output == nil {
			handle, err := os.OpenFile(filepath.Join(capture, collection+".ndjson"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
			if err != nil {
				return err
			}
			handles[collection] = handle
			output = bufio.NewWriterSize(handle, 1<<20)
			outputs[collection] = output
		}
		for _, doc := range docs {
			if _, err := output.Write(doc); err != nil {
				return err
			}
			if err := output.WriteByte('\n'); err != nil {
				return err
			}
		}
		return nil
	}

	schema := source.LoadRuntimeFHIRSchema()
	opts := normalizeLoadOptions(LoadOptions{Project: "perf-project", MetaDir: meta, AuthResourcePath: "/programs/perf/projects/p"})
	generation := os.Getenv("LOOM_INGEST_PERF_GEN")
	start := time.Now()
	err = runGenerationFiles(t.Context(), opts, files, func(ctx context.Context, opts LoadOptions, index int, file string) error {
		fileStart := time.Now()
		loaded, err := fileLoader{options: opts, client: nil, schema: schema, datasetGeneration: generation, overwrite: false, insert: insert}.load(ctx, file, start, 0, 0)
		if err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		wall := time.Since(fileStart).Seconds()
		mu.Lock()
		defer mu.Unlock()
		result.Rows += loaded.Rows
		result.Edges += loaded.EdgesBuilt
		result.ValidationErrors += loaded.ValidationErrors
		result.GenerationErrors += loaded.GenerationErrors
		result.EdgeErrors += loaded.EdgeErrors
		for name, seconds := range loaded.StageSeconds {
			result.Stages[name] += seconds
		}
		fmt.Printf("%-30s rows=%d edges=%d wall=%.3fs rows/s=%.0f errors=%d/%d/%d\n", filepath.Base(file), loaded.Rows, loaded.EdgesBuilt, wall, float64(loaded.Rows)/wall, loaded.ValidationErrors, loaded.GenerationErrors, loaded.EdgeErrors)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	result.Wall = time.Since(start).Seconds()
	result.RowsPerSecond = float64(result.Rows) / result.Wall
	fmt.Printf("total rows=%d edges=%d wall=%.3fs rows/s=%.0f errors=%d/%d/%d\n", result.Rows, result.Edges, result.Wall, result.RowsPerSecond, result.ValidationErrors, result.GenerationErrors, result.EdgeErrors)
	for name, seconds := range result.Stages {
		fmt.Printf("stage %-30s %.3f worker-s\n", name, seconds)
	}
	for name, volume := range result.Volumes {
		fmt.Printf("collection %-30s %d docs %d bytes\n", name, volume.Docs, volume.Bytes)
	}
	line, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("perf_result %s\n", line)
}
