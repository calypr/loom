package proto

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"arangodb-proto/internal/catalog"

	"github.com/bmeg/jsonschema/v6"
	"github.com/bmeg/jsonschemagraph/graph"
	"github.com/bmeg/jsonschemagraph/util"
)

type LoadOptions struct {
	ConnectionOptions
	Schema           string
	MetaDir          string
	Project          string
	AuthResourcePath string
	BatchSize        int
	ProgressEvery    int
	WriterCount      int
	Truncate         bool
	FailFast         bool
	UseGeneric       bool
	WriteAPI         string
	EventSink        EventSink
}

type LoadSummary struct {
	Files            int                `json:"files"`
	VerticesInserted int                `json:"vertices_inserted"`
	EdgesInserted    int                `json:"edges_inserted"`
	ValidationErrors int                `json:"validation_errors"`
	GenerationErrors int                `json:"generation_errors"`
	EdgeErrors       int                `json:"edge_errors"`
	BatchCounts      map[string]int     `json:"batch_counts,omitempty"`
	Resources        map[string]int     `json:"resources"`
	StageSeconds     map[string]float64 `json:"stage_seconds"`
}

func Load(ctx context.Context, opts LoadOptions) (LoadSummary, error) {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 5000
	}
	if opts.ProgressEvery <= 0 {
		opts.ProgressEvery = 50000
	}
	if opts.WriterCount <= 0 {
		opts.WriterCount = 8
	}
	if opts.WriteAPI == "" {
		opts.WriteAPI = "import"
	}
	start := time.Now()
	files, err := DiscoverNDJSON(opts.MetaDir)
	if err != nil {
		return LoadSummary{}, err
	}
	resourceTypes := make([]string, 0, len(files))
	for _, file := range files {
		resourceTypes = append(resourceTypes, ResourceTypeFromPath(file))
	}
	emitEvent(opts.EventSink, "go_backend_connect_start", map[string]any{
		"backend":   backendName(opts.Backend),
		"url":       opts.URL,
		"database":  opts.Database,
		"namespace": opts.Namespace,
	})
	connectStart := time.Now()
	client, err := openBackend(ctx, opts.ConnectionOptions)
	if err != nil {
		return LoadSummary{}, err
	}
	defer client.Close(ctx)
	emitEvent(opts.EventSink, "go_backend_connect_complete", map[string]any{
		"backend":   backendName(opts.Backend),
		"url":       opts.URL,
		"database":  opts.Database,
		"namespace": opts.Namespace,
		"seconds":   time.Since(connectStart).Seconds(),
	})
	emitEvent(opts.EventSink, "go_bootstrap_start", map[string]any{
		"database":  opts.Database,
		"resources": len(resourceTypes),
		"truncate":  opts.Truncate,
	})
	bootstrapStart := time.Now()
	if err := client.Bootstrap(ctx, bootstrapSpecWithReporter(resourceTypes, opts.Truncate, opts.EventSink)); err != nil {
		return LoadSummary{}, err
	}
	schema, err := graph.Load(opts.Schema)
	if err != nil {
		return LoadSummary{}, err
	}
	summary := LoadSummary{Files: len(files), Resources: map[string]int{}, BatchCounts: map[string]int{}, StageSeconds: map[string]float64{}}
	summary.StageSeconds["bootstrap"] = time.Since(bootstrapStart).Seconds()
	extraArgs := graphExtraArgs(opts.AuthResourcePath)

	for _, file := range files {
		resourceType := ResourceTypeFromPath(file)
		class := schema.GetClass(resourceType)
		if class == nil {
			return summary, fmt.Errorf("%s: class %q not found in graph schema", filepath.Base(file), resourceType)
		}
		scanner, closeFn, err := OpenLineScanner(file)
		if err != nil {
			return summary, err
		}
		fileStart := time.Now()
		emitEvent(opts.EventSink, "go_load_file_start", map[string]any{"file": file, "resource": resourceType})
		var rowBuilder RowBuilder
		if opts.UseGeneric {
			rowBuilder = NewGenericRowBuilder(opts.Project, class, schema, extraArgs)
		} else {
			rowBuilder = NewGeneratedRowBuilder(opts.Project, opts.AuthResourcePath)
		}
		shapeCache := catalog.NewShapePlanCache()

		// 1. Channel definitions
		linesChan := make(chan string, 10000)

		type writeTask struct {
			collection string
			docs       []json.RawMessage
		}
		writeChan := make(chan writeTask, 100)

		// 2. Cancellation context and error coordination
		fileCtx, cancel := context.WithCancel(ctx)
		var pipelineErr error
		var errOnce sync.Once
		setPipelineErr := func(e error) {
			errOnce.Do(func() {
				pipelineErr = e
				cancel()
			})
		}

		// 3. Reader goroutine
		go func() {
			defer close(linesChan)
			for scanner.Scan() {
				select {
				case <-fileCtx.Done():
					return
				default:
					line := strings.TrimSpace(scanner.Text())
					if line == "" {
						continue
					}
					select {
					case linesChan <- line:
					case <-fileCtx.Done():
						return
					}
				}
			}
			if err := scanner.Err(); err != nil {
				setPipelineErr(err)
			}
		}()

		// 4. CPU Worker goroutines
		numWorkers := 8
		var workersWG sync.WaitGroup
		workerTimingsChan := make(chan map[string]float64, numWorkers)
		workerCatalogsChan := make(chan *catalog.Profiler, numWorkers)

		var fileRows int64
		var fileVertices int64
		var fileEdges int64
		var verticesInserted int64
		var edgesInserted int64
		var validationErrors int64
		var generationErrors int64
		var edgeErrors int64

		for w := 0; w < numWorkers; w++ {
			workersWG.Add(1)
			go func() {
				defer workersWG.Done()
				localTimings := make(map[string]float64)
				localCatalog := catalog.NewProfiler(opts.Project, opts.AuthResourcePath, resourceType, shapeCache)
				lineCounter := 0
				vertexBatch := make([]json.RawMessage, 0, opts.BatchSize)
				edgeBatch := make([]json.RawMessage, 0, opts.BatchSize)

				flushVertexBatch := func() bool {
					if len(vertexBatch) == 0 {
						return true
					}
					waitStart := time.Now()
					select {
					case writeChan <- writeTask{collection: resourceType, docs: vertexBatch}:
						localTimings["vertex_queue_wait"] += time.Since(waitStart).Seconds()
						localTimings["vertex_batches"]++
						vertexBatch = make([]json.RawMessage, 0, opts.BatchSize)
						return true
					case <-fileCtx.Done():
						localTimings["vertex_queue_wait"] += time.Since(waitStart).Seconds()
						return false
					}
				}
				flushEdgeBatch := func() bool {
					if len(edgeBatch) == 0 {
						return true
					}
					waitStart := time.Now()
					select {
					case writeChan <- writeTask{collection: EdgeCollection, docs: edgeBatch}:
						localTimings["edge_queue_wait"] += time.Since(waitStart).Seconds()
						localTimings["edge_batches"]++
						edgeBatch = make([]json.RawMessage, 0, opts.BatchSize)
						return true
					case <-fileCtx.Done():
						localTimings["edge_queue_wait"] += time.Since(waitStart).Seconds()
						return false
					}
				}

				for {
					select {
					case <-fileCtx.Done():
						return
					case line, ok := <-linesChan:
						if !ok {
							if !flushVertexBatch() || !flushEdgeBatch() {
								return
							}
							select {
							case workerTimingsChan <- localTimings:
							case <-ctx.Done():
							}
							select {
							case workerCatalogsChan <- localCatalog:
							case <-ctx.Done():
							}
							return
						}
						lineCounter++

						result, errType, parseErr := rowBuilder.Build(resourceType, []byte(line), localTimings)
						if parseErr != nil {
							if opts.FailFast {
								setPipelineErr(fmt.Errorf("%s %s row %d: %w", filepath.Base(file), resourceType, lineCounter, parseErr))
								return
							}
							switch errType {
							case rowErrorValidation:
								atomic.AddInt64(&validationErrors, 1)
							case rowErrorGeneration:
								atomic.AddInt64(&generationErrors, 1)
							case rowErrorEdge:
								atomic.AddInt64(&edgeErrors, 1)
							}
							continue
						}

						localCatalog.ObservePayload(result.payload, localTimings)

						vertexBatch = append(vertexBatch, result.vertex)
						atomic.AddInt64(&fileVertices, 1)
						if len(vertexBatch) >= opts.BatchSize {
							if !flushVertexBatch() {
								return
							}
						}

						for _, eRaw := range result.edges {
							edgeBatch = append(edgeBatch, eRaw)
							atomic.AddInt64(&fileEdges, 1)
							if len(edgeBatch) >= opts.BatchSize {
								if !flushEdgeBatch() {
									return
								}
							}
						}

						currentRows := atomic.AddInt64(&fileRows, 1)
						if currentRows%int64(opts.ProgressEvery) == 0 {
							emitEvent(opts.EventSink, "go_load_progress", map[string]any{
								"file":              filepath.Base(file),
								"resource":          resourceType,
								"file_rows":         currentRows,
								"file_vertices":     atomic.LoadInt64(&fileVertices),
								"file_edges":        atomic.LoadInt64(&fileEdges),
								"vertices_inserted": summary.VerticesInserted + int(atomic.LoadInt64(&verticesInserted)),
								"edges_inserted":    summary.EdgesInserted + int(atomic.LoadInt64(&edgesInserted)),
								"seconds":           SecondsSince(start),
							})
						}
					}
				}
			}()
		}

		// Close writeChan when all workers finish
		go func() {
			workersWG.Wait()
			close(writeChan)
			close(workerTimingsChan)
			close(workerCatalogsChan)
		}()

		// 6. I/O Writer goroutines
		numWriters := opts.WriterCount
		var writersWG sync.WaitGroup
		writerTimingsChan := make(chan map[string]float64, numWriters)

		for w := 0; w < numWriters; w++ {
			writersWG.Add(1)
			go func() {
				defer writersWG.Done()
				localTimings := make(map[string]float64)
				writerClient := client
				if backendName(opts.Backend) == backendSurreal {
					openStart := time.Now()
					dedicatedClient, err := openBackend(fileCtx, opts.ConnectionOptions)
					localTimings["writer_client_open"] += time.Since(openStart).Seconds()
					if err != nil {
						setPipelineErr(err)
						return
					}
					writerClient = dedicatedClient
					defer writerClient.Close(fileCtx)
				}
				for {
					select {
					case <-fileCtx.Done():
						return
					case task, ok := <-writeChan:
						if !ok {
							select {
							case writerTimingsChan <- localTimings:
							case <-ctx.Done():
							}
							return
						}

						insertStart := time.Now()
						overwrite := !opts.Truncate
						if err := insertRawDocuments(fileCtx, writerClient, task.collection, task.docs, overwrite, opts.WriteAPI); err != nil {
							setPipelineErr(err)
							return
						}
						elapsed := time.Since(insertStart).Seconds()

						if task.collection == EdgeCollection {
							localTimings["edge_insert"] += elapsed
							atomic.AddInt64(&edgesInserted, int64(len(task.docs)))
						} else {
							localTimings["vertex_insert"] += elapsed
							atomic.AddInt64(&verticesInserted, int64(len(task.docs)))
						}
					}
				}
			}()
		}

		// Close writer timings channel when all writers finish
		go func() {
			writersWG.Wait()
			close(writerTimingsChan)
		}()

		// 7. Wait for completion of Writers
		writersWG.Wait()
		_ = closeFn()

		// If any pipeline error occurred, return it
		if pipelineErr != nil {
			return summary, pipelineErr
		}

		// Update counts
		summary.VerticesInserted += int(verticesInserted)
		summary.EdgesInserted += int(edgesInserted)
		summary.ValidationErrors += int(validationErrors)
		summary.GenerationErrors += int(generationErrors)
		summary.EdgeErrors += int(edgeErrors)
		summary.Resources[resourceType] += int(fileRows)

		fileVertexBatches := 0
		fileEdgeBatches := 0
		mergedCatalog := catalog.NewProfiler(opts.Project, opts.AuthResourcePath, resourceType, catalog.NewShapePlanCache())

		// Aggregate timings from workers
		for workerTimings := range workerTimingsChan {
			for k, v := range workerTimings {
				switch k {
				case "vertex_batches":
					fileVertexBatches += int(v)
				case "edge_batches":
					fileEdgeBatches += int(v)
				default:
					summary.StageSeconds[k] += v
				}
			}
		}
		for workerCatalog := range workerCatalogsChan {
			mergedCatalog.Merge(workerCatalog)
		}

		// Aggregate timings from writers
		for writerTimings := range writerTimingsChan {
			for k, v := range writerTimings {
				summary.StageSeconds[k] += v
			}
		}
		summary.BatchCounts["vertex_insert"] += fileVertexBatches
		summary.BatchCounts["edge_insert"] += fileEdgeBatches

		overwrite := !opts.Truncate
		if err := catalog.WriteFieldCatalog(ctx, client, catalog.FieldCatalogCollection, mergedCatalog.Documents(), opts.BatchSize, overwrite, opts.WriteAPI, summary.StageSeconds); err != nil {
			return summary, err
		}

		emitEvent(opts.EventSink, "go_load_file_complete", map[string]any{
			"file":          filepath.Base(file),
			"resource":      resourceType,
			"file_rows":     fileRows,
			"file_vertices": fileVertices,
			"file_edges":    fileEdges,
			"seconds":       SecondsSince(fileStart),
		})
	}

	emitEvent(opts.EventSink, "go_load_complete", map[string]any{
		"files":             summary.Files,
		"vertices_inserted": summary.VerticesInserted,
		"edges_inserted":    summary.EdgesInserted,
		"validation_errors": summary.ValidationErrors,
		"generation_errors": summary.GenerationErrors,
		"edge_errors":       summary.EdgeErrors,
		"seconds":           SecondsSince(start),
	})
	return summary, nil
}

func graphObjectID(payload map[string]any, class *jsonschema.Schema) (string, error) {
	return util.GetObjectID(payload, class)
}

func graphExtraArgs(authResourcePath string) map[string]any {
	if authResourcePath == "" {
		return nil
	}
	return map[string]any{
		"auth_resource_path": authResourcePath,
	}
}

func LoadSingleResourceReader(ctx context.Context, opts LoadOptions, resourceType string, reader io.Reader, compressed bool) (LoadSummary, error) {
	dir, err := os.MkdirTemp("", "arango-fhir-single-resource-*")
	if err != nil {
		return LoadSummary{}, err
	}
	defer os.RemoveAll(dir)

	name := resourceType + ".ndjson"
	if compressed {
		name += ".gz"
	}
	target := filepath.Join(dir, name)
	f, err := os.Create(target)
	if err != nil {
		return LoadSummary{}, err
	}
	if _, err := io.Copy(f, reader); err != nil {
		f.Close()
		return LoadSummary{}, err
	}
	if err := f.Close(); err != nil {
		return LoadSummary{}, err
	}

	singleOpts := opts
	singleOpts.MetaDir = dir
	return Load(ctx, singleOpts)
}

func LoadSingleResourceFile(ctx context.Context, opts LoadOptions, resourceType, path string) (LoadSummary, error) {
	file, err := os.Open(path)
	if err != nil {
		return LoadSummary{}, err
	}
	defer file.Close()
	return LoadSingleResourceReader(ctx, opts, resourceType, file, strings.HasSuffix(path, ".gz"))
}
