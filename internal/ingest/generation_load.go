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
	"github.com/calypr/loom/internal/dataset"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	"github.com/calypr/loom/internal/graphschema"
	arangostore "github.com/calypr/loom/internal/store/arango"

	"github.com/bmeg/jsonschemagraph/graph"
)

// Load selects the legacy loader only when no immutable Dataset reference was
// supplied. Dataset mode is a separate write contract: every physical graph
// document, catalog row, and lifecycle operation is bound to one generation.
func Load(ctx context.Context, opts LoadOptions) (LoadSummary, error) {
	if opts.Dataset == nil {
		return loadLegacy(ctx, opts)
	}
	return loadGeneration(ctx, opts)
}

func loadGeneration(ctx context.Context, opts LoadOptions) (summary LoadSummary, err error) {
	opts = normalizeLoadOptions(opts)

	start := time.Now()
	files, err := DiscoverNDJSON(opts.MetaDir)
	if err != nil {
		return LoadSummary{}, err
	}
	summary = LoadSummary{
		Files:        len(files),
		Resources:    map[string]int{},
		BatchCounts:  map[string]int{},
		StageSeconds: map[string]float64{},
	}

	// Keep the existing ordered evidence path: discover staged files, parse the
	// graph with its authoritative loader, capture identity from the same file,
	// then preflight every staged payload before contacting Arango.
	schema, err := graph.Load(opts.Schema)
	if err != nil {
		return summary, err
	}
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

	plan, err := newGenerationLoadPlan(opts, files, schemaIdentity)
	if err != nil {
		return summary, err
	}
	// Load dispatch guarantees a plan, but retaining this check makes the
	// generation-only helper safe if it is called directly in a future test.
	if plan == nil {
		return summary, fmt.Errorf("dataset generation plan is required")
	}
	datasetRef := plan.Dataset
	summary.Dataset = &datasetRef
	if err := ctx.Err(); err != nil {
		return summary, err
	}

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
	defer func() { _ = client.Close(context.WithoutCancel(ctx)) }()
	emitEvent(opts.EventSink, "go_backend_connect_complete", map[string]any{
		"backend":  "arango",
		"url":      opts.URL,
		"database": opts.Database,
		"seconds":  time.Since(connectStart).Seconds(),
	})

	bootstrapStart := time.Now()
	// Metadata comes first and never truncates. This order means a failed
	// graph bootstrap cannot delete lifecycle history from a prior generation.
	emitEvent(opts.EventSink, "go_bootstrap_start", map[string]any{
		"database":    opts.Database,
		"resources":   0,
		"truncate":    false,
		"collections": "dataset_lifecycle",
	})
	if err = client.Bootstrap(ctx, lifecycleBootstrapSpecWithReporter(opts.EventSink)); err != nil {
		return summary, err
	}
	emitEvent(opts.EventSink, "go_bootstrap_start", map[string]any{
		"database":  opts.Database,
		"resources": len(resourceTypes),
		"truncate":  false,
	})
	if err = client.Bootstrap(ctx, bootstrapSpecWithReporter(resourceTypes, false, opts.EventSink)); err != nil {
		return summary, err
	}
	summary.StageSeconds["bootstrap"] = time.Since(bootstrapStart).Seconds()

	lifecycleStore, err := datasetarango.New(client)
	if err != nil {
		return summary, err
	}
	manifest, err := lifecycleStore.CreateManifest(ctx, plan.Manifest)
	if err != nil {
		return summary, err
	}
	manifestReady := false
	defer func() {
		if err == nil || manifestReady {
			return
		}
		// A canceled request must not strand a PRE-FLIGHT/LOADING/ANALYZING
		// manifest. Once READY is persisted we deliberately leave it alone,
		// because an activation error is an unknown outcome rather than proof
		// that the generation failed.
		_, cleanupErr := lifecycleStore.TransitionManifest(
			context.WithoutCancel(ctx),
			manifest,
			dataset.ManifestStateFailed,
		)
		if cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("mark dataset generation %s/%s failed: %w", plan.Dataset.Project, plan.Dataset.Generation, cleanupErr))
		}
	}()
	loadingManifest, transitionErr := lifecycleStore.TransitionManifest(ctx, manifest, dataset.ManifestStateLoading)
	if transitionErr != nil {
		return summary, transitionErr
	}
	manifest = loadingManifest

	catalogs := make(map[generationCatalogKey]*catalog.Profiler)
	relationshipCounts := make(map[catalog.RelationshipKey]int64)
	for _, file := range files {
		if err = ctx.Err(); err != nil {
			return summary, err
		}
		fileStart := time.Now()
		resourceType := ResourceTypeFromPath(file)
		emitEvent(opts.EventSink, "go_load_file_start", map[string]any{"file": file, "resource": resourceType})

		result, fileErr := loadGenerationFile(
			ctx,
			opts,
			client,
			schema,
			file,
			plan.Dataset.Generation,
			start,
			summary.VerticesInserted,
			summary.EdgesInserted,
		)
		if fileErr != nil {
			return summary, fileErr
		}

		summary.VerticesInserted += result.VerticesInserted
		summary.EdgesInserted += result.EdgesInserted
		summary.ValidationErrors += result.ValidationErrors
		summary.GenerationErrors += result.GenerationErrors
		summary.EdgeErrors += result.EdgeErrors
		summary.Resources[result.ResourceType] += result.Rows
		summary.BatchCounts["vertex_insert"] += result.VertexBatches
		summary.BatchCounts["edge_insert"] += result.EdgeBatches
		for name, seconds := range result.StageSeconds {
			summary.StageSeconds[name] += seconds
		}
		catalog.MergeRelationshipCounts(relationshipCounts, result.RelationshipCounts)

		key := generationCatalogKey{
			project:           opts.Project,
			datasetGeneration: plan.Dataset.Generation,
			authResourcePath:  opts.AuthResourcePath,
			resourceType:      result.ResourceType,
		}
		merged := catalogs[key]
		if merged == nil {
			merged = catalog.NewProfilerForGeneration(
				key.project,
				key.datasetGeneration,
				key.authResourcePath,
				key.resourceType,
				catalog.NewShapePlanCache(),
			)
			catalogs[key] = merged
		}
		if err = merged.Merge(result.Catalog); err != nil {
			return summary, fmt.Errorf("merge catalog for dataset generation %s/%s %s: %w", plan.Dataset.Project, plan.Dataset.Generation, result.ResourceType, err)
		}

		emitEvent(opts.EventSink, "go_load_file_complete", map[string]any{
			"file":          filepath.Base(file),
			"resource":      result.ResourceType,
			"file_rows":     result.Rows,
			"file_vertices": result.VerticesBuilt,
			"file_edges":    result.EdgesBuilt,
			"seconds":       time.Since(fileStart).Seconds(),
		})
	}

	if summary.ValidationErrors != 0 || summary.GenerationErrors != 0 || summary.EdgeErrors != 0 {
		return summary, &GenerationLoadIncompleteError{
			Dataset:          plan.Dataset,
			ValidationErrors: summary.ValidationErrors,
			GenerationErrors: summary.GenerationErrors,
			EdgeErrors:       summary.EdgeErrors,
		}
	}
	if err = ctx.Err(); err != nil {
		return summary, err
	}

	// ANALYZING is intentionally limited to catalog finalization in this
	// packet. No synthetic analysis version is attached; a later analysis owner
	// can add one without changing the load/activation contract.
	analyzingManifest, transitionErr := lifecycleStore.TransitionManifest(ctx, manifest, dataset.ManifestStateAnalyzing)
	if transitionErr != nil {
		return summary, transitionErr
	}
	manifest = analyzingManifest
	for _, key := range sortedGenerationCatalogKeys(catalogs) {
		if err = ctx.Err(); err != nil {
			return summary, err
		}
		if err = catalog.WriteFieldCatalog(
			ctx,
			client,
			catalog.FieldCatalogCollection,
			catalogs[key].Documents(),
			opts.BatchSize,
			false,
			opts.WriteAPI,
			summary.StageSeconds,
		); err != nil {
			return summary, err
		}
	}
	if err = catalog.WriteRelationshipCatalog(
		ctx,
		client,
		catalog.RelationshipCatalogDocuments(relationshipCounts),
		opts.BatchSize,
		false,
		opts.WriteAPI,
		summary.StageSeconds,
	); err != nil {
		return summary, err
	}
	if err = ctx.Err(); err != nil {
		return summary, err
	}
	readyManifest, transitionErr := lifecycleStore.TransitionManifest(ctx, manifest, dataset.ManifestStateReady)
	if transitionErr != nil {
		return summary, transitionErr
	}
	manifest = readyManifest
	manifestReady = true
	if _, activationErr := lifecycleStore.Activate(ctx, manifest); activationErr != nil {
		return summary, &ActivationOutcomeError{Dataset: plan.Dataset, Err: activationErr}
	}

	emitEvent(opts.EventSink, "go_load_complete", map[string]any{
		"files":              summary.Files,
		"vertices_inserted":  summary.VerticesInserted,
		"edges_inserted":     summary.EdgesInserted,
		"validation_errors":  summary.ValidationErrors,
		"generation_errors":  summary.GenerationErrors,
		"edge_errors":        summary.EdgeErrors,
		"dataset_generation": plan.Dataset.Generation,
		"seconds":            SecondsSince(start),
	})
	return summary, nil
}

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

type generationFileResult struct {
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
}

type generationWriteTask struct {
	collection         string
	docs               []json.RawMessage
	relationshipCounts map[catalog.RelationshipKey]int64
}

// loadGenerationFile owns one scanner and closes it with a defer before it
// returns. This keeps file descriptors bounded for large staged directories,
// including failures from worker, writer, or scanner goroutines.
func loadGenerationFile(
	ctx context.Context,
	opts LoadOptions,
	client *arangostore.Client,
	schema *graph.GraphSchema,
	file string,
	datasetGeneration string,
	start time.Time,
	priorVertices int,
	priorEdges int,
) (result generationFileResult, err error) {
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
		if closeErr := closeFn(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	var rowBuilder RowBuilder
	if opts.UseGeneric || !supportsGeneratedLoad(resourceType) {
		rowBuilder = NewGenericRowBuilder(opts.Project, class, schema, graphExtraArgs(opts.AuthResourcePath))
	} else {
		rowBuilder = NewGeneratedRowBuilder(opts.Project, opts.AuthResourcePath)
	}
	rowBuilder, err = newGenerationRowBuilder(rowBuilder, opts.Project, datasetGeneration)
	if err != nil {
		return result, err
	}

	linesChan := make(chan string, 10000)
	writeChan := make(chan generationWriteTask, 100)
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
		for scanner.Scan() {
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
			case linesChan <- line:
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

	var fileRows int64
	var fileVertices int64
	var fileEdges int64
	var verticesInserted int64
	var edgesInserted int64
	var validationErrors int64
	var generationErrors int64
	var edgeErrors int64

	for worker := 0; worker < workerCount; worker++ {
		workersWG.Add(1)
		go func() {
			defer workersWG.Done()
			localTimings := make(map[string]float64)
			localCatalog := catalog.NewProfilerForGeneration(opts.Project, datasetGeneration, opts.AuthResourcePath, resourceType, catalog.NewShapePlanCache())
			lineCounter := 0
			vertexBatch := make([]json.RawMessage, 0, opts.BatchSize)
			edgeBatch := make([]json.RawMessage, 0, opts.BatchSize)

			flushVertexBatch := func() bool {
				if len(vertexBatch) == 0 {
					return true
				}
				waitStart := time.Now()
				select {
				case writeChan <- generationWriteTask{collection: resourceType, docs: vertexBatch}:
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
				case writeChan <- generationWriteTask{collection: EdgeCollection, docs: edgeBatch, relationshipCounts: relationshipCounts}:
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
					lineCounter++
					built, errorType, buildErr := rowBuilder.Build(resourceType, []byte(line), localTimings)
					if buildErr != nil {
						if opts.FailFast {
							setPipelineErr(fmt.Errorf("%s %s row %d: %w", filepath.Base(file), resourceType, lineCounter, buildErr))
							return
						}
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
					// A generation never overwrites a physical graph or catalog
					// document. Collisions are evidence of a non-immutable load.
					if insertErr := insertRawDocuments(fileCtx, client, task.collection, task.docs, false, opts.WriteAPI); insertErr != nil {
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
	result.StageSeconds = make(map[string]float64)
	result.RelationshipCounts = make(map[catalog.RelationshipKey]int64)
	mergedCatalog := catalog.NewProfilerForGeneration(opts.Project, datasetGeneration, opts.AuthResourcePath, resourceType, catalog.NewShapePlanCache())
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
