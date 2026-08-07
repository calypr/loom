package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/calypr/loom/internal/catalog"
	arangostore "github.com/calypr/loom/internal/store/arango"

	"github.com/bmeg/jsonschemagraph/graph"
)

type generationCatalogKey struct {
	project           string
	datasetGeneration string
	authResourcePath  string
	resourceType      string
}

func sortedGenerationCatalogKeys(catalogs map[generationCatalogKey]*catalog.Profiler) []generationCatalogKey {
	keys := make([]generationCatalogKey, 0, len(catalogs))
	for key := range catalogs {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].project != keys[right].project {
			return keys[left].project < keys[right].project
		}
		if keys[left].datasetGeneration != keys[right].datasetGeneration {
			return keys[left].datasetGeneration < keys[right].datasetGeneration
		}
		if keys[left].authResourcePath != keys[right].authResourcePath {
			return keys[left].authResourcePath < keys[right].authResourcePath
		}
		return keys[left].resourceType < keys[right].resourceType
	})
	return keys
}

type fileLoadResult struct {
	ResourceType       string
	Rows               int
	VerticesBuilt      int
	EdgesBuilt         int
	VerticesInserted   int
	EdgesInserted      int
	ValidationErrors   int
	GenerationErrors   int
	EdgeErrors         int
	VertexBatches      int
	EdgeBatches        int
	StageSeconds       map[string]float64
	Catalog            *catalog.Profiler
	RelationshipCounts map[catalog.RelationshipKey]int64
	RowErrors          []RowErrorSample
}

const rowErrorSampleLimit = 10

type fileLine struct {
	number int
	text   string
}

type fileWriteTask struct {
	collection         string
	docs               []json.RawMessage
	relationshipCounts map[catalog.RelationshipKey]int64
}

type documentInserter func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error

// loadFile owns one scanner and closes it before returning. A blank generation
// preserves the primary resource-loader identity; overwrite controls its
// upsert contract while immutable generations always pass false.
func loadFile(
	ctx context.Context,
	opts LoadOptions,
	client *arangostore.Client,
	schema *graph.GraphSchema,
	file string,
	datasetGeneration string,
	overwrite bool,
	start time.Time,
	priorVertices int,
	priorEdges int,
	insert documentInserter,
) (result fileLoadResult, err error) {
	resourceType := ResourceTypeFromPath(file)
	result.ResourceType = resourceType
	class := schema.GetClass(resourceType)
	if class == nil {
		return result, fmt.Errorf("%s: class %q not found in graph schema", filepath.Base(file), resourceType)
	}

	scanner, closeFn, err := OpenLineScanner(file)
	if err != nil {
		return result, err
	}
	defer func() {
		err = errors.Join(err, closeFn())
	}()

	var rowBuilder RowBuilder
	if opts.UseGeneric || !supportsGeneratedLoad(resourceType) {
		rowBuilder = NewGenericRowBuilder(opts.Project, class, schema, graphExtraArgs(opts.Project, opts.AuthResourcePath))
	} else {
		rowBuilder = NewGeneratedRowBuilder(opts.Project, opts.AuthResourcePath)
	}
	if strings.TrimSpace(datasetGeneration) != "" {
		rowBuilder, err = newGenerationRowBuilder(rowBuilder, opts.Project, datasetGeneration)
		if err != nil {
			return result, err
		}
	}

	linesChan := make(chan fileLine, 10000)
	writeChan := make(chan fileWriteTask, 100)
	fileCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var pipelineErr error
	var pipelineErrMu sync.Mutex
	var errOnce sync.Once
	setPipelineErr := func(value error) {
		errOnce.Do(func() {
			pipelineErrMu.Lock()
			pipelineErr = value
			pipelineErrMu.Unlock()
			cancel()
		})
	}

	go func() {
		defer close(linesChan)
		lineNumber := 0
		for scanner.Scan() {
			lineNumber++
			select {
			case <-fileCtx.Done():
				return
			default:
			}
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			select {
			case linesChan <- fileLine{number: lineNumber, text: line}:
			case <-fileCtx.Done():
				return
			}
		}
		if scanErr := scanner.Err(); scanErr != nil {
			setPipelineErr(scanErr)
		}
	}()

	const workerCount = 8
	var workersWG sync.WaitGroup
	workerTimingsChan := make(chan map[string]float64, workerCount)
	workerCatalogsChan := make(chan *catalog.Profiler, workerCount)
	shapeCache := catalog.NewShapePlanCache()

	var fileRows int64
	var fileVertices int64
	var fileEdges int64
	var verticesInserted int64
	var edgesInserted int64
	var validationErrors int64
	var generationErrors int64
	var edgeErrors int64
	var rowErrorsMu sync.Mutex
	rowErrors := make([]RowErrorSample, 0, rowErrorSampleLimit)
	recordRowError := func(line int, category rowErrorType, buildErr error) {
		sample := RowErrorSample{
			File:         filepath.Base(file),
			Line:         line,
			ResourceType: resourceType,
			Category:     string(category),
			Message:      buildErr.Error(),
		}
		rowErrorsMu.Lock()
		defer rowErrorsMu.Unlock()
		if len(rowErrors) < rowErrorSampleLimit {
			rowErrors = append(rowErrors, sample)
			return
		}
		latest := 0
		for i := 1; i < len(rowErrors); i++ {
			if rowErrors[i].Line > rowErrors[latest].Line {
				latest = i
			}
		}
		if sample.Line < rowErrors[latest].Line {
			rowErrors[latest] = sample
		}
	}

	for worker := 0; worker < workerCount; worker++ {
		workersWG.Add(1)
		go func() {
			defer workersWG.Done()
			localTimings := make(map[string]float64)
			localCatalog := catalog.NewProfilerForGeneration(opts.Project, datasetGeneration, opts.AuthResourcePath, resourceType, shapeCache)
			vertexBatch := make([]json.RawMessage, 0, opts.BatchSize)
			edgeBatch := make([]json.RawMessage, 0, opts.BatchSize)

			flushVertexBatch := func() bool {
				if len(vertexBatch) == 0 {
					return true
				}
				waitStart := time.Now()
				select {
				case writeChan <- fileWriteTask{collection: resourceType, docs: vertexBatch}:
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
				relationshipCounts, countErr := catalog.RelationshipCountsFromRawEdges(edgeBatch)
				if countErr != nil {
					setPipelineErr(countErr)
					return false
				}
				waitStart := time.Now()
				select {
				case writeChan <- fileWriteTask{collection: EdgeCollection, docs: edgeBatch, relationshipCounts: relationshipCounts}:
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
				case line, open := <-linesChan:
					if !open {
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
					built, errorType, buildErr := rowBuilder.Build(resourceType, []byte(line.text), localTimings)
					if buildErr != nil {
						if opts.FailFast {
							setPipelineErr(fmt.Errorf("%s %s row %d: %w", filepath.Base(file), resourceType, line.number, buildErr))
							return
						}
						recordRowError(line.number, errorType, buildErr)
						switch errorType {
						case rowErrorValidation:
							atomic.AddInt64(&validationErrors, 1)
						case rowErrorGeneration:
							atomic.AddInt64(&generationErrors, 1)
						case rowErrorEdge:
							atomic.AddInt64(&edgeErrors, 1)
						}
						continue
					}

					localCatalog.ObservePayload(built.payload, localTimings)
					vertexBatch = append(vertexBatch, built.vertex)
					atomic.AddInt64(&fileVertices, 1)
					if len(vertexBatch) >= opts.BatchSize && !flushVertexBatch() {
						return
					}
					for _, edge := range built.edges {
						edgeBatch = append(edgeBatch, edge)
						atomic.AddInt64(&fileEdges, 1)
						if len(edgeBatch) >= opts.BatchSize && !flushEdgeBatch() {
							return
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
							"vertices_inserted": priorVertices + int(atomic.LoadInt64(&verticesInserted)),
							"edges_inserted":    priorEdges + int(atomic.LoadInt64(&edgesInserted)),
							"seconds":           SecondsSince(start),
						})
					}
				}
			}
		}()
	}

	go func() {
		workersWG.Wait()
		close(writeChan)
		close(workerTimingsChan)
		close(workerCatalogsChan)
	}()

	var writersWG sync.WaitGroup
	type writerResult struct {
		timings            map[string]float64
		relationshipCounts map[catalog.RelationshipKey]int64
	}
	writerTimingsChan := make(chan writerResult, opts.WriterCount)
	for writer := 0; writer < opts.WriterCount; writer++ {
		writersWG.Add(1)
		go func() {
			defer writersWG.Done()
			localTimings := make(map[string]float64)
			localRelationships := make(map[catalog.RelationshipKey]int64)
			for {
				select {
				case <-fileCtx.Done():
					return
				case task, open := <-writeChan:
					if !open {
						select {
						case writerTimingsChan <- writerResult{timings: localTimings, relationshipCounts: localRelationships}:
						case <-ctx.Done():
						}
						return
					}
					insertStart := time.Now()
					if insertErr := insert(fileCtx, client, task.collection, task.docs, overwrite, opts.WriteAPI); insertErr != nil {
						setPipelineErr(insertErr)
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
	go func() {
		writersWG.Wait()
		close(writerTimingsChan)
	}()

	writersWG.Wait()
	pipelineErrMu.Lock()
	currentPipelineErr := pipelineErr
	pipelineErrMu.Unlock()
	if currentPipelineErr != nil {
		return result, currentPipelineErr
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	result.Rows = int(atomic.LoadInt64(&fileRows))
	result.VerticesBuilt = int(atomic.LoadInt64(&fileVertices))
	result.EdgesBuilt = int(atomic.LoadInt64(&fileEdges))
	result.VerticesInserted = int(atomic.LoadInt64(&verticesInserted))
	result.EdgesInserted = int(atomic.LoadInt64(&edgesInserted))
	result.ValidationErrors = int(atomic.LoadInt64(&validationErrors))
	result.GenerationErrors = int(atomic.LoadInt64(&generationErrors))
	result.EdgeErrors = int(atomic.LoadInt64(&edgeErrors))
	sort.Slice(rowErrors, func(i, j int) bool { return rowErrors[i].Line < rowErrors[j].Line })
	result.RowErrors = rowErrors
	result.StageSeconds = make(map[string]float64)
	result.RelationshipCounts = make(map[catalog.RelationshipKey]int64)
	mergedCatalog := catalog.NewProfilerForGeneration(opts.Project, datasetGeneration, opts.AuthResourcePath, resourceType, shapeCache)
	for timings := range workerTimingsChan {
		for key, value := range timings {
			switch key {
			case "vertex_batches":
				result.VertexBatches += int(value)
			case "edge_batches":
				result.EdgeBatches += int(value)
			default:
				result.StageSeconds[key] += value
			}
		}
	}
	for workerCatalog := range workerCatalogsChan {
		if mergeErr := mergedCatalog.Merge(workerCatalog); mergeErr != nil {
			return result, fmt.Errorf("merge worker field catalog for %s: %w", resourceType, mergeErr)
		}
	}
	for writerResult := range writerTimingsChan {
		for key, value := range writerResult.timings {
			result.StageSeconds[key] += value
		}
		catalog.MergeRelationshipCounts(result.RelationshipCounts, writerResult.relationshipCounts)
	}
	result.Catalog = mergedCatalog
	return result, nil
}
