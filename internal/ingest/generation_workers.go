package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/calypr/loom/internal/catalog"
	arangostore "github.com/calypr/loom/internal/store/arango"
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

type generationWriteStatsKey struct{}

type generationWriteSample struct {
	bytes      int64
	permitWait time.Duration
	duration   time.Duration
}

type generationWriteStats struct {
	mu      sync.Mutex
	samples map[string][]generationWriteSample
}

func (stats *generationWriteStats) record(class string, sample generationWriteSample) {
	stats.mu.Lock()
	defer stats.mu.Unlock()
	stats.samples[class] = append(stats.samples[class], sample)
}

func (stats *generationWriteStats) summary() map[string]map[string]any {
	stats.mu.Lock()
	defer stats.mu.Unlock()
	result := make(map[string]map[string]any, len(stats.samples))
	for class, samples := range stats.samples {
		var bytes int64
		var permitWait, duration time.Duration
		durations := make([]time.Duration, 0, len(samples))
		for _, sample := range samples {
			bytes += sample.bytes
			permitWait += sample.permitWait
			duration += sample.duration
			durations = append(durations, sample.duration)
		}
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		result[class] = map[string]any{
			"requests": len(samples), "request_bytes": bytes,
			"writer_permit_wait_seconds": permitWait.Seconds(),
			"request_seconds":            duration.Seconds(),
			"p95_request_seconds":        durations[(95*len(samples)+99)/100-1].Seconds(),
		}
	}
	return result
}

type fileLoadResult struct {
	ResourceType               string
	Rows                       int
	VerticesBuilt              int
	EdgesBuilt                 int
	VerticesInserted           int
	EdgesInserted              int
	ValidationErrors           int
	GenerationErrors           int
	EdgeErrors                 int
	VertexBatches              int
	EdgeBatches                int
	SemanticInventoryBatches   int
	SemanticInventoryEntryRuns []string
	StageSeconds               map[string]float64
	Catalog                    *catalog.Profiler
	RelationshipCounts         map[catalog.RelationshipKey]int64
	RowErrors                  []RowErrorSample
}

type generationInsertCounters struct {
	vertices int64
	edges    int64
}

const rowErrorSampleLimit = 10

func selectGenerationRowErrorSamples(fileSamples [][]RowErrorSample) []RowErrorSample {
	failing := make([][]RowErrorSample, 0, len(fileSamples))
	longest := 0
	for _, candidates := range fileSamples {
		if len(candidates) == 0 {
			continue
		}
		failing = append(failing, candidates)
		if len(candidates) > longest {
			longest = len(candidates)
		}
	}
	if len(failing) == 0 {
		return nil
	}

	picked := make([][]RowErrorSample, len(failing))
	seen := make(map[RowErrorSample]struct{}, rowErrorSampleLimit)
	total := 0
	for rank := 0; rank < longest; rank++ {
		for file, candidates := range failing {
			if total >= rowErrorSampleLimit {
				return flattenRowErrorSamples(picked)
			}
			if rank >= len(candidates) {
				continue
			}
			sample := candidates[rank]
			if _, duplicate := seen[sample]; duplicate {
				continue
			}
			seen[sample] = struct{}{}
			picked[file] = append(picked[file], sample)
			total++
		}
	}
	return flattenRowErrorSamples(picked)
}

func flattenRowErrorSamples(picked [][]RowErrorSample) []RowErrorSample {
	total := 0
	for _, samples := range picked {
		total += len(samples)
	}
	samples := make([]RowErrorSample, 0, total)
	for _, fileSamples := range picked {
		samples = append(samples, fileSamples...)
	}

	sort.SliceStable(samples, func(i, j int) bool {
		if samples[i].File != samples[j].File {
			return samples[i].File < samples[j].File
		}
		return samples[i].Line < samples[j].Line
	})
	return samples
}

type fileLine struct {
	number int
	text   string
}

type fileWriteTask struct {
	collection         string
	docs               []json.RawMessage
	relationshipCounts map[catalog.RelationshipKey]int64
	replace            bool
}

type documentInserter func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error

func loadFile(
	ctx context.Context,
	opts LoadOptions,
	client *arangostore.Client,
	schema *runtimeFHIRSchema,
	file string,
	datasetGeneration string,
	overwrite bool,
	start time.Time,
	priorVertices int,
	priorEdges int,
	insert documentInserter,
) (result fileLoadResult, err error) {
	resourceType := ResourceTypeFromPath(file)
	if strings.TrimSpace(opts.SourceFileOwner) != "" {
		resourceType = strings.TrimSpace(opts.SourceFileOwner)
	}
	result.ResourceType = resourceType
	semanticSourceFile := ""
	if catalog.NormalizeDatasetGeneration(datasetGeneration) != "" {
		if strings.TrimSpace(opts.SourceFileOwner) != "" {
			semanticSourceFile = "META/" + strings.TrimSpace(opts.SourceFileOwner) + ".ndjson"
		} else {
			semanticSourceFile = semanticInventorySourceFileID(opts.MetaDir, file)
		}
	}
	if !schema.HasClass(resourceType) {
		return result, fmt.Errorf("%s: resource type %q is not in the generated FHIR resource registry", filepath.Base(file), resourceType)
	}
	if !supportsGeneratedLoad(resourceType) {
		return result, fmt.Errorf("%s: resource type %q has no generated loader", filepath.Base(file), resourceType)
	}

	scanner, closeFn, err := OpenLineScanner(file)
	if err != nil {
		return result, err
	}
	defer func() {
		err = errors.Join(err, closeFn())
	}()

	var rowBuilder RowBuilder = NewGeneratedRowBuilder(opts.Project, opts.AuthResourcePath)
	if strings.TrimSpace(datasetGeneration) != "" {
		rowBuilder, err = newGenerationRowBuilder(rowBuilder, opts.Project, datasetGeneration)
		if err != nil {
			return result, err
		}
	}
	if strings.TrimSpace(opts.SourceFileOwner) != "" {
		rowBuilder = sourceFileOwnerRowBuilder{delegate: rowBuilder, owner: opts.SourceFileOwner}
	}

	linesChan := make(chan fileLine, opts.LineQueueSize)
	writeChan := make(chan fileWriteTask, opts.WriteQueueSize)
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

	workerCount := opts.WorkerCount
	var workersWG sync.WaitGroup
	workerTimingsChan := make(chan map[string]float64, workerCount)
	workerCatalogsChan := make(chan *catalog.Profiler, workerCount)
	workerInventoryChan := make(chan *catalog.SemanticInventoryEntryAggregator, workerCount)
	shapeCache := catalog.NewShapePlanCacheWithLimits(opts.CatalogLimits.MaxShapePlans, opts.CatalogLimits.MaxRetainedBytes)
	if opts.fileShapePlanCache != nil {
		shapeCache = catalog.NewShapePlanCacheWithSharedPlans(opts.CatalogLimits.MaxShapePlans, opts.CatalogLimits.MaxRetainedBytes, opts.fileShapePlanCache)
	}

	var fileRows int64
	var fileVertices int64
	var fileEdges int64
	var verticesInserted int64
	var edgesInserted int64
	var validationErrors int64
	var generationErrors int64
	var edgeErrors int64
	var inventoryBuildSeconds float64
	var inventoryRunsMu sync.Mutex
	var inventoryRuns []string
	defer func() {
		if err == nil {
			return
		}
		inventoryRunsMu.Lock()
		paths := append([]string(nil), inventoryRuns...)
		inventoryRunsMu.Unlock()
		for _, path := range paths {
			_ = os.Remove(path)
		}
	}()
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
			localCatalog := catalog.NewProfilerForGenerationWithLimits(opts.Project, datasetGeneration, opts.AuthResourcePath, resourceType, shapeCache, opts.CatalogLimits)
			localInventory := catalog.NewSemanticInventoryEntryAggregator(opts.Project, datasetGeneration, opts.AuthResourcePath, resourceType, catalog.SemanticInventorySourceFile)
			vertexBatch := make([]json.RawMessage, 0, opts.BatchSize)
			edgeBatch := make([]json.RawMessage, 0, opts.BatchSize)
			edgeRelationshipCounts := make(map[catalog.RelationshipKey]int64)
			edgeRelationshipsKnown := true
			semanticInventoryFailed := false
			flushInventoryRun := func() bool {
				if localInventory.Len() == 0 {
					return true
				}
				spillStart := time.Now()
				path, spillErr := writeSemanticInventoryEntryRun(localInventory)
				localTimings["semantic_inventory_spill"] += time.Since(spillStart).Seconds()
				if spillErr != nil {
					semanticInventoryFailed = true
					setPipelineErr(fmt.Errorf("spill semantic inventory for %s: %w", filepath.Base(file), spillErr))
					return false
				}
				inventoryRunsMu.Lock()
				inventoryRuns = append(inventoryRuns, path)
				inventoryRunsMu.Unlock()
				return true
			}

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
				relationshipCounts := edgeRelationshipCounts
				if !edgeRelationshipsKnown {
					var countErr error
					relationshipCounts, countErr = catalog.RelationshipCountsFromRawEdges(edgeBatch)
					if countErr != nil {
						setPipelineErr(countErr)
						return false
					}
				}
				waitStart := time.Now()
				select {
				case writeChan <- fileWriteTask{collection: EdgeCollection, docs: edgeBatch, relationshipCounts: relationshipCounts}:
					localTimings["edge_queue_wait"] += time.Since(waitStart).Seconds()
					localTimings["edge_batches"]++
					edgeBatch = make([]json.RawMessage, 0, opts.BatchSize)
					edgeRelationshipCounts = make(map[catalog.RelationshipKey]int64)
					edgeRelationshipsKnown = true
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
						workerInventoryChan <- localInventory
						return
					}
					if opts.fileWorkerBudget != nil {
						select {
						case opts.fileWorkerBudget <- struct{}{}:
						case <-fileCtx.Done():
							return
						}
					}
					releaseWorkerBudget := func() {
						if opts.fileWorkerBudget != nil {
							<-opts.fileWorkerBudget
						}
					}
					built, errorType, buildErr := rowBuilder.Build(resourceType, []byte(line.text), localTimings)
					if buildErr != nil {
						if opts.FailFast {
							setPipelineErr(&RowError{File: filepath.Base(file), ResourceType: resourceType,
								Row: line.number, Category: string(errorType), Cause: buildErr})
							releaseWorkerBudget()
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
						releaseWorkerBudget()
						continue
					}

					if semanticSourceFile == "" {
						if observeErr := localCatalog.ObservePayload(built.payload, localTimings); observeErr != nil {
							setPipelineErr(fmt.Errorf("observe payload for %s row %d: %w", filepath.Base(file), line.number, observeErr))
							releaseWorkerBudget()
							return
						}
					} else {
						sourceID := semanticInventorySourceRecordID(semanticSourceFile, line.number)
						semanticSink := func(contribution catalog.SemanticInventoryContribution) {
							if semanticInventoryFailed || fileCtx.Err() != nil {
								return
							}
							if addErr := localInventory.Add(contribution); addErr != nil {
								semanticInventoryFailed = true
								setPipelineErr(fmt.Errorf("aggregate semantic inventory contribution for %s row %d: %w", filepath.Base(file), line.number, addErr))
							}
						}
						if observeErr := localCatalog.ObservePayloadWithInventory(built.payload, localTimings, sourceID, semanticSink); observeErr != nil {
							semanticInventoryFailed = true
							setPipelineErr(fmt.Errorf("observe semantic inventory for %s row %d: %w", filepath.Base(file), line.number, observeErr))
						}
						if fileCtx.Err() != nil {
							releaseWorkerBudget()
							return
						}
						if localInventory.Len() >= semanticInventoryRunKeyLimit && !flushInventoryRun() {
							releaseWorkerBudget()
							return
						}
					}
					vertexBatch = append(vertexBatch, built.vertex)
					atomic.AddInt64(&fileVertices, 1)
					if len(vertexBatch) >= opts.BatchSize && !flushVertexBatch() {
						releaseWorkerBudget()
						return
					}
					for edgeIndex, edge := range built.edges {
						if edgeIndex < len(built.relationships) {
							edgeRelationshipCounts[built.relationships[edgeIndex]]++
						} else {
							edgeRelationshipsKnown = false
						}
						edgeBatch = append(edgeBatch, edge)
						atomic.AddInt64(&fileEdges, 1)
						if len(edgeBatch) >= opts.BatchSize && !flushEdgeBatch() {
							releaseWorkerBudget()
							return
						}
					}

					currentRows := atomic.AddInt64(&fileRows, 1)
					if currentRows%int64(opts.ProgressEvery) == 0 {
						verticesTotal := priorVertices + int(atomic.LoadInt64(&verticesInserted))
						edgesTotal := priorEdges + int(atomic.LoadInt64(&edgesInserted))
						if opts.fileInsertedCounters != nil {
							verticesTotal = int(atomic.LoadInt64(&opts.fileInsertedCounters.vertices))
							edgesTotal = int(atomic.LoadInt64(&opts.fileInsertedCounters.edges))
						}
						emitEvent(opts.EventSink, "go_load_progress", map[string]any{
							"file":              filepath.Base(file),
							"resource":          resourceType,
							"file_rows":         currentRows,
							"file_vertices":     atomic.LoadInt64(&fileVertices),
							"file_edges":        atomic.LoadInt64(&fileEdges),
							"vertices_inserted": verticesTotal,
							"edges_inserted":    edgesTotal,
							"seconds":           SecondsSince(start),
						})
					}
					releaseWorkerBudget()
				}
			}
		}()
	}

	mergeDone := make(chan struct{})
	go func() {
		defer close(mergeDone)
		workersWG.Wait()
		mergeStart := time.Now()
		inventoryRunsMu.Lock()
		runs := append([]string(nil), inventoryRuns...)
		inventoryRunsMu.Unlock()
		close(workerInventoryChan)
		workers := make([]*catalog.SemanticInventoryEntryAggregator, 0, workerCount)
		for worker := range workerInventoryChan {
			workers = append(workers, worker)
		}
		var mergeErr error
		if strings.TrimSpace(opts.SourceFileOwner) == "" {
			for _, worker := range workers {
				path, spillErr := writeSemanticInventoryEntryRun(worker)
				if spillErr != nil {
					mergeErr = spillErr
					break
				}
				if path != "" {
					runs = append(runs, path)
				}
			}
			inventoryRunsMu.Lock()
			inventoryRuns = runs
			inventoryRunsMu.Unlock()
		} else {
			batch := make([]json.RawMessage, 0, opts.BatchSize)
			flush := func() error {
				if len(batch) == 0 {
					return nil
				}
				select {
				case writeChan <- fileWriteTask{collection: catalog.SemanticInventoryEntryCollection, docs: batch, replace: true}:
					batch = make([]json.RawMessage, 0, opts.BatchSize)
					return nil
				case <-fileCtx.Done():
					return fileCtx.Err()
				}
			}
			mergeErr = mergeWorkerSemanticInventoryEntries(fileCtx, runs, workers, func(partial catalog.SemanticInventoryEntryPartial) error {
				document := catalog.SemanticInventoryEntryDocument(partial, opts.Project, catalog.NormalizeDatasetGeneration(datasetGeneration), catalog.SemanticInventoryBuildID(opts.Project, datasetGeneration), catalog.SemanticInventorySourceFile)
				encoded, encodeErr := json.Marshal(document)
				if encodeErr != nil {
					return encodeErr
				}
				batch = append(batch, encoded)
				if len(batch) == opts.BatchSize {
					return flush()
				}
				return nil
			})
			if mergeErr == nil {
				mergeErr = flush()
			}
		}
		if mergeErr != nil {
			setPipelineErr(fmt.Errorf("merge semantic inventory entries for %s: %w", resourceType, mergeErr))
		}
		inventoryBuildSeconds = time.Since(mergeStart).Seconds()
		close(writeChan)
		close(workerTimingsChan)
		close(workerCatalogsChan)
	}()

	writeStats, _ := ctx.Value(generationWriteStatsKey{}).(*generationWriteStats)
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
					var permitWait time.Duration
					if opts.fileWriterBudget != nil {
						permitStart := time.Now()
						select {
						case opts.fileWriterBudget <- struct{}{}:
							permitWait = time.Since(permitStart)
						case <-fileCtx.Done():
							return
						}
					}
					insertStart := time.Now()
					var insertErr error
					taskOverwrite := overwrite || task.replace
					insertCtx := fileCtx
					if writeStats != nil {
						class := "vertex"
						switch task.collection {
						case EdgeCollection:
							class = "edge"
						case catalog.SemanticInventoryCollection, catalog.SemanticInventoryEntryCollection:
							class = "semantic_inventory"
						}
						insertCtx = arangostore.WithRawInsertObserver(fileCtx, func(bytes int64, duration time.Duration) {
							writeStats.record(class, generationWriteSample{bytes: bytes, permitWait: permitWait, duration: duration})
						})
					}
					insertErr = insert(insertCtx, client, task.collection, task.docs, taskOverwrite, opts.WriteAPI)
					if opts.fileWriterBudget != nil {
						<-opts.fileWriterBudget
					}
					if insertErr != nil {
						setPipelineErr(insertErr)
						return
					}
					elapsed := time.Since(insertStart).Seconds()
					switch task.collection {
					case EdgeCollection:
						localTimings["edge_insert"] += elapsed
						atomic.AddInt64(&edgesInserted, int64(len(task.docs)))
						if opts.fileInsertedCounters != nil {
							atomic.AddInt64(&opts.fileInsertedCounters.edges, int64(len(task.docs)))
						}
						catalog.MergeRelationshipCounts(localRelationships, task.relationshipCounts)
					case catalog.SemanticInventoryCollection, catalog.SemanticInventoryEntryCollection:
						localTimings["semantic_inventory_insert"] += elapsed
						localTimings["semantic_inventory_batches"]++
					default:
						localTimings["vertex_insert"] += elapsed
						atomic.AddInt64(&verticesInserted, int64(len(task.docs)))
						if opts.fileInsertedCounters != nil {
							atomic.AddInt64(&opts.fileInsertedCounters.vertices, int64(len(task.docs)))
						}
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
	<-mergeDone
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
	inventoryRunsMu.Lock()
	result.SemanticInventoryEntryRuns = append([]string(nil), inventoryRuns...)
	inventoryRunsMu.Unlock()
	sort.Slice(rowErrors, func(i, j int) bool { return rowErrors[i].Line < rowErrors[j].Line })
	result.RowErrors = rowErrors
	result.StageSeconds = make(map[string]float64)
	result.RelationshipCounts = make(map[catalog.RelationshipKey]int64)
	mergedCatalog := catalog.NewProfilerForGenerationWithLimits(opts.Project, datasetGeneration, opts.AuthResourcePath, resourceType, catalog.NewShapePlanCacheWithLimits(opts.CatalogLimits.MaxShapePlans, opts.CatalogLimits.MaxRetainedBytes), opts.CatalogLimits)
	for timings := range workerTimingsChan {
		for key, value := range timings {
			switch key {
			case "vertex_batches":
				result.VertexBatches += int(value)
			case "edge_batches":
				result.EdgeBatches += int(value)
			case "semantic_inventory_batches":
				result.SemanticInventoryBatches += int(value)
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
			if key == "semantic_inventory_batches" {
				result.SemanticInventoryBatches += int(value)
			} else {
				result.StageSeconds[key] += value
			}
		}
		catalog.MergeRelationshipCounts(result.RelationshipCounts, writerResult.relationshipCounts)
	}
	result.StageSeconds["semantic_inventory_aggregate_encode"] = inventoryBuildSeconds
	result.Catalog = mergedCatalog
	return result, nil
}

func semanticInventorySourceFileID(root, file string) string {
	absoluteRoot, rootErr := filepath.Abs(root)
	absoluteFile, fileErr := filepath.Abs(file)
	if rootErr != nil || fileErr != nil {
		return filepath.ToSlash(filepath.Clean(file))
	}
	relative, err := filepath.Rel(absoluteRoot, absoluteFile)
	if err != nil {
		return filepath.ToSlash(filepath.Clean(absoluteFile))
	}
	return filepath.ToSlash(filepath.Clean(relative))
}

func semanticInventorySourceRecordID(sourceFile string, line int) string {
	return sourceFile + "#" + strconv.Itoa(line)
}
