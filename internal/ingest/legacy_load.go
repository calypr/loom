package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/graphschema"

	"github.com/bmeg/jsonschemagraph/graph"
)

func loadLegacy(ctx context.Context, opts LoadOptions) (LoadSummary, error) {
	opts = normalizeLoadOptions(opts)
	start := time.Now()
	files, err := DiscoverNDJSON(opts.MetaDir)
	if err != nil {
		return LoadSummary{}, err
	}
	summary := LoadSummary{Files: len(files), Resources: map[string]int{}, BatchCounts: map[string]int{}, StageSeconds: map[string]float64{}}
	schema, err := graph.Load(opts.Schema)
	if err != nil {
		return summary, err
	}
	// Keep graph.Load as the authoritative graph parser so its established
	// validation and error behavior remain unchanged. Identity records evidence
	// for the same configured file once graph loading succeeds.
	schemaIdentity, err := graphschema.Load(opts.Schema)
	if err != nil {
		return summary, err
	}
	summary.SchemaIdentity = &schemaIdentity
	preflightSampleRows := opts.PreflightSampleRows
	if preflightSampleRows <= 0 {
		preflightSampleRows = defaultPreflightSampleRows
	}
	emitEvent(opts.EventSink, "go_preflight_start", map[string]any{
		"files":              len(files),
		"sampleRows":         preflightSampleRows,
		"schemaSha256":       schemaIdentity.SchemaSHA256(),
		"generatedRootCount": len(schemaIdentity.GeneratedResourceTypes()),
	})
	preflightStart := time.Now()
	preflight, err := PreflightFiles(files, schema, preflightSampleRows)
	summary.Preflight = preflight
	summary.StageSeconds["preflight"] = time.Since(preflightStart).Seconds()
	emitEvent(opts.EventSink, "go_preflight_complete", map[string]any{
		"files":     len(files),
		"resources": len(preflight.Resources),
		"issues":    len(preflight.Issues),
		"seconds":   summary.StageSeconds["preflight"],
	})
	if err != nil {
		return summary, err
	}
	// Preflight groups staged files by resource type, which prevents duplicate
	// collection bootstrap specs when callers stage multiple files for one type.
	resourceTypes := make([]string, 0, len(preflight.Resources))
	for _, resource := range preflight.Resources {
		resourceTypes = append(resourceTypes, resource.ResourceType)
	}
	emitEvent(opts.EventSink, "go_backend_connect_start", map[string]any{
		"backend":  "arango",
		"url":      opts.URL,
		"database": opts.Database,
	})
	connectStart := time.Now()
	client, err := openBackend(ctx, opts.ConnectionOptions)
	if err != nil {
		return summary, err
	}
	defer client.Close(ctx)
	emitEvent(opts.EventSink, "go_backend_connect_complete", map[string]any{
		"backend":  "arango",
		"url":      opts.URL,
		"database": opts.Database,
		"seconds":  time.Since(connectStart).Seconds(),
	})
	emitEvent(opts.EventSink, "go_bootstrap_start", map[string]any{
		"database":  opts.Database,
		"resources": len(resourceTypes),
		"truncate":  opts.Truncate,
	})
	bootstrapStart := time.Now()
	if err := client.Bootstrap(ctx, bootstrapSpecWithReporter(resourceTypes, opts.Truncate, opts.EventSink)); err != nil {
		return summary, err
	}
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
		if opts.UseGeneric || !supportsGeneratedLoad(resourceType) {
			rowBuilder = NewGenericRowBuilder(opts.Project, class, schema, extraArgs)
		} else {
			rowBuilder = NewGeneratedRowBuilder(opts.Project, opts.AuthResourcePath)
		}
		shapeCache := catalog.NewShapePlanCache()

		// 1. Channel definitions
		linesChan := make(chan string, 10000)

		type writeTask struct {
			collection         string
			docs               []json.RawMessage
			relationshipCounts map[catalog.RelationshipKey]int64
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
					relationshipCounts, err := catalog.RelationshipCountsFromRawEdges(edgeBatch)
					if err != nil {
						setPipelineErr(err)
						return false
					}
					waitStart := time.Now()
					select {
					case writeChan <- writeTask{collection: EdgeCollection, docs: edgeBatch, relationshipCounts: relationshipCounts}:
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
		type writerResult struct {
			timings            map[string]float64
			relationshipCounts map[catalog.RelationshipKey]int64
		}
		writerTimingsChan := make(chan writerResult, numWriters)

		for w := 0; w < numWriters; w++ {
			writersWG.Add(1)
			go func() {
				defer writersWG.Done()
				localTimings := make(map[string]float64)
				localRelationships := make(map[catalog.RelationshipKey]int64)
				for {
					select {
					case <-fileCtx.Done():
						return
					case task, ok := <-writeChan:
						if !ok {
							select {
							case writerTimingsChan <- writerResult{timings: localTimings, relationshipCounts: localRelationships}:
							case <-ctx.Done():
							}
							return
						}

						insertStart := time.Now()
						overwrite := !opts.Truncate
						if err := insertRawDocuments(fileCtx, client, task.collection, task.docs, overwrite, opts.WriteAPI); err != nil {
							setPipelineErr(err)
							return
						}
						elapsed := time.Since(insertStart).Seconds()

						if task.collection == EdgeCollection {
							localTimings["edge_insert"] += elapsed
							atomic.AddInt64(&edgesInserted, int64(len(task.docs)))
							catalog.MergeRelationshipCounts(localRelationships, task.relationshipCounts)
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
		mergedRelationships := make(map[catalog.RelationshipKey]int64)
		for writerResult := range writerTimingsChan {
			for k, v := range writerResult.timings {
				summary.StageSeconds[k] += v
			}
			catalog.MergeRelationshipCounts(mergedRelationships, writerResult.relationshipCounts)
		}
		summary.BatchCounts["vertex_insert"] += fileVertexBatches
		summary.BatchCounts["edge_insert"] += fileEdgeBatches

		overwrite := !opts.Truncate
		if err := catalog.WriteFieldCatalog(ctx, client, catalog.FieldCatalogCollection, mergedCatalog.Documents(), opts.BatchSize, overwrite, opts.WriteAPI, summary.StageSeconds); err != nil {
			return summary, err
		}
		relationshipDocs := catalog.RelationshipCatalogDocuments(mergedRelationships)
		if opts.Truncate {
			if err := catalog.WriteRelationshipCatalog(ctx, client, relationshipDocs, opts.BatchSize, false, opts.WriteAPI, summary.StageSeconds); err != nil {
				return summary, err
			}
		} else if err := catalog.AccumulateRelationshipCatalog(ctx, client, relationshipDocs, summary.StageSeconds); err != nil {
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
