package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/graphschema"
	arangostore "github.com/calypr/loom/internal/store/arango"

	"github.com/bmeg/jsonschema/v6"
	"github.com/bmeg/jsonschemagraph/graph"
	"github.com/bmeg/jsonschemagraph/util"
)

type LoadOptions struct {
	arangostore.ConnectionOptions
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
	// Dataset selects immutable generation mode. A nil value preserves the
	// original unversioned loader behavior. A non-nil value requires a complete
	// directory import, writes generation-qualified graph identities, and only
	// activates the generation after every graph file and catalog finalization
	// succeeds.
	Dataset *dataset.DatasetRef
	// PreflightSampleRows bounds the number of payloads inspected from every
	// staged file before Loom opens or mutates Arango. Zero uses the safe
	// default; full row validation still happens in the loader.
	PreflightSampleRows int
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
	Preflight        PreflightReport    `json:"preflight"`
	// SchemaIdentity is the exact configured graph-schema evidence used for
	// this load. It remains nil when Loom cannot load the configured schema, so
	// an early failure never looks like a successful schema observation.
	SchemaIdentity *graphschema.Identity `json:"schema_identity,omitempty"`
	// Dataset is the immutable target when this was a generation load. It is
	// present even on a failed generation load so callers can identify the
	// inactive manifest that needs operational inspection.
	Dataset *dataset.DatasetRef `json:"dataset,omitempty"`
}

// normalizeLoadOptions applies the operational defaults shared by both loader
// modes. The immutable-generation loader deliberately has a different write
// lifecycle, but both modes must agree on batching, progress, concurrency, and
// the Arango write API before any input is examined.
func normalizeLoadOptions(opts LoadOptions) LoadOptions {
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
	return opts
}

var (
	// ErrGenerationLoadRequiresDirectory prevents a one-file or arbitrary-file
	// load from being mistaken for a complete immutable dataset snapshot.
	ErrGenerationLoadRequiresDirectory = errors.New("dataset generation load requires a directory")
	// ErrGenerationLoadRequiresFiles prevents an empty staged directory from
	// becoming an active but meaningless generation.
	ErrGenerationLoadRequiresFiles = errors.New("dataset generation load requires at least one NDJSON file")
	// ErrGenerationLoadTruncateForbidden prevents a new generation from
	// deleting active or historical graph data.
	ErrGenerationLoadTruncateForbidden = errors.New("dataset generation load cannot truncate collections")
	// ErrGenerationDatasetProjectMismatch prevents graph documents and their
	// lifecycle manifest from being scoped to different projects.
	ErrGenerationDatasetProjectMismatch = errors.New("dataset generation project does not match load project")
	// ErrGenerationSingleResourceUnsupported makes the legacy HTTP one-file
	// path explicitly unavailable in immutable snapshot mode.
	ErrGenerationSingleResourceUnsupported = errors.New("single-resource imports cannot create a dataset generation")
)

// ActivationOutcomeError means the generation reached READY but Loom could
// not prove that the active-generation pointer was updated. READY is kept for
// an operator to reconcile; it must never be downgraded to FAILED because the
// activation request may have committed before its error reached the caller.
type ActivationOutcomeError struct {
	Dataset dataset.DatasetRef
	Err     error
}

func (e *ActivationOutcomeError) Error() string {
	if e == nil {
		return "dataset generation activation outcome is unknown"
	}
	return fmt.Sprintf("activate dataset generation %s/%s: %v", e.Dataset.Project, e.Dataset.Generation, e.Err)
}

func (e *ActivationOutcomeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// GenerationLoadIncompleteError means row-level validation, generation, or
// edge construction failures were observed while FailFast was disabled. The
// graph may contain partial immutable documents, but the lifecycle manifest is
// deliberately left FAILED and cannot be selected for reads.
type GenerationLoadIncompleteError struct {
	Dataset          dataset.DatasetRef
	ValidationErrors int
	GenerationErrors int
	EdgeErrors       int
}

func (e *GenerationLoadIncompleteError) Error() string {
	if e == nil {
		return "dataset generation load is incomplete"
	}
	return fmt.Sprintf(
		"dataset generation %s/%s is incomplete: validation_errors=%d generation_errors=%d edge_errors=%d",
		e.Dataset.Project,
		e.Dataset.Generation,
		e.ValidationErrors,
		e.GenerationErrors,
		e.EdgeErrors,
	)
}

type generationLoadPlan struct {
	Dataset  dataset.DatasetRef
	Manifest dataset.Manifest
}

// newGenerationLoadPlan validates and snapshots all immutable information
// after input preflight and before a database connection is opened. Nil keeps
// the legacy loader path exactly unversioned.
func newGenerationLoadPlan(opts LoadOptions, files []string, identity graphschema.Identity) (*generationLoadPlan, error) {
	if opts.Dataset == nil {
		return nil, nil
	}

	ref := *opts.Dataset
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	if ref.Project != opts.Project {
		return nil, fmt.Errorf("%w: dataset project %q, load project %q", ErrGenerationDatasetProjectMismatch, ref.Project, opts.Project)
	}
	if opts.Truncate {
		return nil, ErrGenerationLoadTruncateForbidden
	}
	info, err := os.Stat(opts.MetaDir)
	if err != nil {
		return nil, fmt.Errorf("inspect dataset generation directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %q", ErrGenerationLoadRequiresDirectory, opts.MetaDir)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrGenerationLoadRequiresFiles, opts.MetaDir)
	}

	schemaSnapshot, err := dataset.SnapshotSchemaIdentity(identity)
	if err != nil {
		return nil, fmt.Errorf("snapshot dataset generation schema identity: %w", err)
	}
	manifest, err := dataset.NewManifest(ref, schemaSnapshot)
	if err != nil {
		return nil, fmt.Errorf("create dataset generation manifest: %w", err)
	}
	return &generationLoadPlan{Dataset: ref, Manifest: manifest}, nil
}

// loadLegacy is the original unversioned loader. Load dispatches to it only
// when Dataset is nil so existing import/API behavior and physical identities
// remain unchanged while generation mode evolves independently.
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
						if err := insertRawDocuments(fileCtx, client, task.collection, task.docs, overwrite, opts.WriteAPI); err != nil {
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
	if opts.Dataset != nil {
		return LoadSummary{}, ErrGenerationSingleResourceUnsupported
	}
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
	if opts.Dataset != nil {
		return LoadSummary{}, ErrGenerationSingleResourceUnsupported
	}
	file, err := os.Open(path)
	if err != nil {
		return LoadSummary{}, err
	}
	defer file.Close()
	return LoadSingleResourceReader(ctx, opts, resourceType, file, strings.HasSuffix(path, ".gz"))
}
