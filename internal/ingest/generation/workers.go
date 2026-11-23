package generation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/calypr/loom/internal/ingest/bootstrap"
	ingestevent "github.com/calypr/loom/internal/ingest/event"
	"github.com/calypr/loom/internal/ingest/row"
	"github.com/calypr/loom/internal/ingest/source"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/catalog/fieldprofile"
	"github.com/calypr/loom/internal/semantic"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type generationCatalogKey struct {
	project           string
	datasetGeneration string
	authResourcePath  string
	resourceType      string
}

func sortedGenerationCatalogKeys(catalogs map[generationCatalogKey]*fieldprofile.Profiler) []generationCatalogKey {
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
	Catalog                    *fieldprofile.Profiler
	RelationshipCounts         map[catalog.RelationshipKey]int64
	RowErrors                  []row.RowErrorSample
}

type generationInsertCounters struct {
	vertices int64
	edges    int64
}

const rowErrorSampleLimit = 10
const lineBatchSize = 256
const lineBatchByteLimit = 1 << 20

func selectGenerationRowErrorSamples(fileSamples [][]row.RowErrorSample) []row.RowErrorSample {
	failing := make([][]row.RowErrorSample, 0, len(fileSamples))
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

	picked := make([][]row.RowErrorSample, len(failing))
	seen := make(map[row.RowErrorSample]struct{}, rowErrorSampleLimit)
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

func flattenRowErrorSamples(picked [][]row.RowErrorSample) []row.RowErrorSample {
	total := 0
	for _, samples := range picked {
		total += len(samples)
	}
	samples := make([]row.RowErrorSample, 0, total)
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

type fileLoader struct {
	options           LoadOptions
	client            *arangostore.Client
	schema            *source.RuntimeFHIRSchema
	datasetGeneration string
	overwrite         bool
	insert            documentInserter
}

func (loader fileLoader) load(ctx context.Context, file string, start time.Time, priorVertices, priorEdges int) (result fileLoadResult, err error) {
	opts, schema := loader.options, loader.schema
	datasetGeneration := loader.datasetGeneration
	resourceType := source.ResourceTypeFromPath(file)
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
	if !row.SupportsGeneratedLoad(resourceType) {
		return result, fmt.Errorf("%s: resource type %q has no generated loader", filepath.Base(file), resourceType)
	}

	scanner, closeFn, err := source.OpenLineScanner(file)
	if err != nil {
		return result, err
	}
	defer func() {
		err = errors.Join(err, closeFn())
	}()

	var rowBuilder row.RowBuilder = row.NewGeneratedRowBuilder(opts.Project, opts.AuthResourcePath)
	if strings.TrimSpace(datasetGeneration) != "" {
		rowBuilder, err = row.NewGenerationRowBuilderWithFormat(rowBuilder, opts.Project, datasetGeneration, opts.IdentityFormat)
		if err != nil {
			return result, err
		}
	}
	if strings.TrimSpace(opts.SourceFileOwner) != "" {
		rowBuilder = row.WithSourceFileOwner(rowBuilder, opts.SourceFileOwner)
	}

	batchSize := min(lineBatchSize, opts.LineQueueSize)
	linesChan := make(chan []fileLine, 1)
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

	go scanFileLines(fileCtx, scanner, linesChan, batchSize, setPipelineErr)

	workerCount := opts.WorkerCount
	var workersWG sync.WaitGroup
	workerTimingsChan := make(chan map[string]float64, workerCount)
	workerCatalogsChan := make(chan *fieldprofile.Profiler, workerCount)
	workerInventoryChan := make(chan *semantic.EntryAggregator, workerCount)
	shapeCache := fieldprofile.NewShapePlanCacheWithLimits(opts.CatalogLimits.MaxShapePlans, opts.CatalogLimits.MaxRetainedBytes)
	if opts.fileShapePlanCache != nil {
		shapeCache = fieldprofile.NewShapePlanCacheWithSharedPlans(opts.CatalogLimits.MaxShapePlans, opts.CatalogLimits.MaxRetainedBytes, opts.fileShapePlanCache)
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
	rowErrors := make([]row.RowErrorSample, 0, rowErrorSampleLimit)
	recordRowError := func(line int, category row.RowErrorType, buildErr error) {
		sample := row.RowErrorSample{
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

	workerState := &fileLoadWorkerState{
		opts: opts, datasetGeneration: datasetGeneration, resourceType: resourceType,
		semanticSourceFile: semanticSourceFile, file: file, shapeCache: shapeCache,
		fileCtx: fileCtx, ctx: ctx, linesChan: linesChan, writeChan: writeChan,
		workerTimingsChan: workerTimingsChan, workerCatalogsChan: workerCatalogsChan,
		workerInventoryChan: workerInventoryChan, setPipelineErr: setPipelineErr,
		recordRowError: recordRowError, inventoryRunsMu: &inventoryRunsMu, inventoryRuns: &inventoryRuns,
		fileRows: &fileRows, fileVertices: &fileVertices, fileEdges: &fileEdges,
		verticesInserted: &verticesInserted, edgesInserted: &edgesInserted,
		validationErrors: &validationErrors, generationErrors: &generationErrors, edgeErrors: &edgeErrors,
		rowBuilder: rowBuilder, start: start, priorVertices: priorVertices, priorEdges: priorEdges,
	}
	for worker := 0; worker < workerCount; worker++ {
		workersWG.Add(1)
		go func() {
			defer workersWG.Done()
			runFileLoadWorker(workerState)
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
		workers := make([]*semantic.EntryAggregator, 0, workerCount)
		for worker := range workerInventoryChan {
			workers = append(workers, worker)
		}
		var mergeErr error
		if strings.TrimSpace(opts.SourceFileOwner) == "" {
			runs, mergeErr = spillWorkerInventoryRuns(runs, workers)
			inventoryRunsMu.Lock()
			inventoryRuns = runs
			inventoryRunsMu.Unlock()
		} else {
			inventory := sourceFileInventory{project: opts.Project, generation: datasetGeneration, batchSize: opts.BatchSize}
			mergeErr = inventory.enqueueEntries(fileCtx, runs, workers, writeChan)
		}
		if mergeErr != nil {
			setPipelineErr(fmt.Errorf("merge semantic inventory entries for %s: %w", resourceType, mergeErr))
		}
		inventoryBuildSeconds = time.Since(mergeStart).Seconds()
		close(writeChan)
		close(workerTimingsChan)
		close(workerCatalogsChan)
	}()

	writers := fileWriteFlow{
		loader: loader, verticesInserted: &verticesInserted, edgesInserted: &edgesInserted,
		pipelineError: setPipelineErr,
	}
	writersWG, writerTimingsChan := writers.start(ctx, fileCtx, writeChan)

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
	mergedCatalog := fieldprofile.NewProfilerForGenerationWithLimits(opts.Project, datasetGeneration, opts.AuthResourcePath, resourceType, fieldprofile.NewShapePlanCacheWithLimits(opts.CatalogLimits.MaxShapePlans, opts.CatalogLimits.MaxRetainedBytes), opts.CatalogLimits)
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

type fileLoadWorkerState struct {
	opts                LoadOptions
	datasetGeneration   string
	resourceType        string
	semanticSourceFile  string
	file                string
	shapeCache          *fieldprofile.ShapePlanCache
	fileCtx             context.Context
	ctx                 context.Context
	linesChan           chan []fileLine
	writeChan           chan fileWriteTask
	workerTimingsChan   chan map[string]float64
	workerCatalogsChan  chan *fieldprofile.Profiler
	workerInventoryChan chan *semantic.EntryAggregator
	setPipelineErr      func(error)
	recordRowError      func(int, row.RowErrorType, error)
	inventoryRunsMu     *sync.Mutex
	inventoryRuns       *[]string
	fileRows            *int64
	fileVertices        *int64
	fileEdges           *int64
	verticesInserted    *int64
	edgesInserted       *int64
	validationErrors    *int64
	generationErrors    *int64
	edgeErrors          *int64
	rowBuilder          row.RowBuilder
	start               time.Time
	priorVertices       int
	priorEdges          int
}

func runFileLoadWorker(state *fileLoadWorkerState) {
	opts := state.opts
	datasetGeneration := state.datasetGeneration
	resourceType := state.resourceType
	semanticSourceFile := state.semanticSourceFile
	file := state.file
	shapeCache := state.shapeCache
	fileCtx := state.fileCtx
	ctx := state.ctx
	linesChan := state.linesChan
	writeChan := state.writeChan
	workerTimingsChan := state.workerTimingsChan
	workerCatalogsChan := state.workerCatalogsChan
	workerInventoryChan := state.workerInventoryChan
	setPipelineErr := state.setPipelineErr
	recordRowError := state.recordRowError
	rowBuilder := state.rowBuilder
	start := state.start
	priorVertices := state.priorVertices
	priorEdges := state.priorEdges
	localTimings := make(map[string]float64)
	localCatalog := fieldprofile.NewProfilerForGenerationWithLimits(opts.Project, datasetGeneration, opts.AuthResourcePath, resourceType, shapeCache, opts.CatalogLimits)
	localInventory := semantic.NewEntryAggregator(opts.Project, datasetGeneration, opts.AuthResourcePath, resourceType, semantic.SourceFile)
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
		state.inventoryRunsMu.Lock()
		*state.inventoryRuns = append(*state.inventoryRuns, path)
		state.inventoryRunsMu.Unlock()
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
		case writeChan <- fileWriteTask{collection: bootstrap.EdgeCollection, docs: edgeBatch, relationshipCounts: relationshipCounts}:
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
		case lines, open := <-linesChan:
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
			for _, line := range lines {
				select {
				case <-fileCtx.Done():
					releaseWorkerBudget()
					return
				default:
				}
				built, errorType, buildErr := rowBuilder.Build(resourceType, []byte(line.text), localTimings)
				if buildErr != nil {
					if opts.FailFast {
						setPipelineErr(&row.RowError{File: filepath.Base(file), ResourceType: resourceType,
							Row: line.number, Category: string(errorType), Cause: buildErr})
						releaseWorkerBudget()
						return
					}
					recordRowError(line.number, errorType, buildErr)
					switch errorType {
					case row.RowErrorValidation:
						atomic.AddInt64(state.validationErrors, 1)
					case row.RowErrorGeneration:
						atomic.AddInt64(state.generationErrors, 1)
					case row.RowErrorEdge:
						atomic.AddInt64(state.edgeErrors, 1)
					}
					continue
				}

				if semanticSourceFile == "" {
					if observeErr := localCatalog.ObservePayload(built.Payload(), localTimings); observeErr != nil {
						setPipelineErr(fmt.Errorf("observe payload for %s row %d: %w", filepath.Base(file), line.number, observeErr))
						releaseWorkerBudget()
						return
					}
				} else {
					sourceID := semanticInventorySourceRecordID(semanticSourceFile, line.number)
					semanticSink := func(contribution semantic.Contribution) {
						if semanticInventoryFailed || fileCtx.Err() != nil {
							return
						}
						if addErr := localInventory.Add(contribution); addErr != nil {
							semanticInventoryFailed = true
							setPipelineErr(fmt.Errorf("aggregate semantic inventory contribution for %s row %d: %w", filepath.Base(file), line.number, addErr))
						}
					}
					if observeErr := localCatalog.ObservePayloadWithInventory(built.Payload(), localTimings, sourceID, semanticSink); observeErr != nil {
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
				vertexBatch = append(vertexBatch, built.Vertex())
				atomic.AddInt64(state.fileVertices, 1)
				if len(vertexBatch) >= opts.BatchSize && !flushVertexBatch() {
					releaseWorkerBudget()
					return
				}
				for edgeIndex, edge := range built.Edges() {
					if edgeIndex < len(built.Relationships()) {
						edgeRelationshipCounts[built.Relationships()[edgeIndex]]++
					} else {
						edgeRelationshipsKnown = false
					}
					edgeBatch = append(edgeBatch, edge)
					atomic.AddInt64(state.fileEdges, 1)
					if len(edgeBatch) >= opts.BatchSize && !flushEdgeBatch() {
						releaseWorkerBudget()
						return
					}
				}

				currentRows := atomic.AddInt64(state.fileRows, 1)
				if currentRows%int64(opts.ProgressEvery) == 0 {
					verticesTotal := priorVertices + int(atomic.LoadInt64(state.verticesInserted))
					edgesTotal := priorEdges + int(atomic.LoadInt64(state.edgesInserted))
					if opts.fileInsertedCounters != nil {
						verticesTotal = int(atomic.LoadInt64(&opts.fileInsertedCounters.vertices))
						edgesTotal = int(atomic.LoadInt64(&opts.fileInsertedCounters.edges))
					}
					ingestevent.EmitEvent(opts.EventSink, "go_load_progress", map[string]any{
						"file":              filepath.Base(file),
						"resource":          resourceType,
						"file_rows":         currentRows,
						"file_vertices":     atomic.LoadInt64(state.fileVertices),
						"file_edges":        atomic.LoadInt64(state.fileEdges),
						"vertices_inserted": verticesTotal,
						"edges_inserted":    edgesTotal,
						"seconds":           ingestevent.SecondsSince(start),
					})
				}
			}
			releaseWorkerBudget()
		}
	}
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

func LoadSourceFile(ctx context.Context, file string, options LoadOptions, client *arangostore.Client, schema *source.RuntimeFHIRSchema, datasetGeneration string) (fileLoadResult, error) {
	loader := fileLoader{options: normalizeLoadOptions(options), client: client, schema: schema, datasetGeneration: datasetGeneration, insert: bootstrap.InsertRawDocuments}
	return loader.load(ctx, file, time.Now(), 0, 0)
}
