package generation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/calypr/loom/internal/ingest/bootstrap"
	ingestevent "github.com/calypr/loom/internal/ingest/event"
	"github.com/calypr/loom/internal/ingest/row"
	"github.com/calypr/loom/internal/ingest/source"

	"github.com/calypr/loom/internal/catalog"
	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	"github.com/calypr/loom/internal/catalog/fieldprofile"
	publication "github.com/calypr/loom/internal/dataset"
	publicationarango "github.com/calypr/loom/internal/dataset/arango"
	"github.com/calypr/loom/internal/releasecommit"
	"github.com/calypr/loom/internal/semantic"
	semanticarango "github.com/calypr/loom/internal/semantic/arango"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func Load(ctx context.Context, opts LoadOptions) (LoadSummary, error) {
	if opts.Dataset == nil {
		return loadResource(ctx, opts)
	}
	return loadGeneration(ctx, opts)
}

const generationCleanupTimeout = 10 * time.Second

func totalResourceRows(resources map[string]int) int64 {
	var total int64
	for _, count := range resources {
		total += int64(count)
	}
	return total
}

func boundedGenerationCleanup(parent context.Context, timeout time.Duration, cleanup func(context.Context) error) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), timeout)
	defer cancel()
	return cleanup(cleanupCtx)
}

func generationCleanupError(parent context.Context, timeout time.Duration, original error, label string, cleanup func(context.Context) error) error {
	cleanupErr := boundedGenerationCleanup(parent, timeout, cleanup)
	if cleanupErr == nil {
		return original
	}
	return errors.Join(original, fmt.Errorf("%s: %w", label, cleanupErr))
}

func prepareGenerationLoad(opts LoadOptions, summary *LoadSummary) (files []string, schema *source.RuntimeFHIRSchema, plan *generationLoadPlan, err error) {
	files, err = source.DiscoverNDJSON(opts.MetaDir)
	if err != nil {
		return nil, nil, nil, err
	}
	*summary = LoadSummary{
		Files:        len(files),
		Resources:    map[string]int{},
		BatchCounts:  map[string]int{},
		StageSeconds: map[string]float64{},
	}

	var schemaIdentity publication.SchemaSnapshot
	schemaIdentity, schema, err = source.LoadSchema()
	if err != nil {
		return nil, nil, nil, err
	}
	summary.SchemaIdentity = &schemaIdentity
	preflightSampleRows := opts.PreflightSampleRows
	if preflightSampleRows <= 0 {
		preflightSampleRows = source.DefaultPreflightSampleRows
	}
	ingestevent.EmitEvent(opts.EventSink, "go_preflight_start", map[string]any{
		"files":              len(files),
		"sampleRows":         preflightSampleRows,
		"schemaSha256":       schemaIdentity.SchemaSHA256,
		"generatedRootCount": len(schemaIdentity.GeneratedResourceTypes),
	})
	preflightStart := time.Now()
	preflight, err := source.PreflightFiles(files, schema, preflightSampleRows)
	summary.Preflight = preflight
	summary.StageSeconds["preflight"] = time.Since(preflightStart).Seconds()
	ingestevent.EmitEvent(opts.EventSink, "go_preflight_complete", map[string]any{
		"files":     len(files),
		"resources": len(preflight.Resources),
		"issues":    len(preflight.Issues),
		"seconds":   summary.StageSeconds["preflight"],
	})
	if err != nil {
		return nil, nil, nil, err
	}

	plan, err = newGenerationLoadPlan(opts, files, schemaIdentity)
	if err != nil {
		return nil, nil, nil, err
	}

	return files, schema, plan, nil
}

type generationStores struct {
	client    *arangostore.Client
	catalog   *catalogarango.Store
	semantic  *semanticarango.Store
	lifecycle *publicationarango.Store
}

func connectGenerationStores(ctx context.Context, opts LoadOptions, preflight source.PreflightReport, summary *LoadSummary) (generationStores, error) {
	resourceTypes := make([]string, 0, len(preflight.Resources))
	for _, resource := range preflight.Resources {
		resourceTypes = append(resourceTypes, resource.ResourceType)
	}

	ingestevent.EmitEvent(opts.EventSink, "go_backend_connect_start", map[string]any{
		"backend":  "arango",
		"url":      opts.URL,
		"database": opts.Database,
	})
	connectStart := time.Now()
	client, err := bootstrap.OpenBackend(ctx, opts.ConnectionOptions)
	if err != nil {
		return generationStores{}, err
	}
	defer func() {
		err = generationCleanupError(ctx, generationCleanupTimeout, err, "close generation backend", client.Close)
	}()
	catalogStore, err := catalogarango.New(client)
	if err != nil {
		return generationStores{}, err
	}
	semanticStore, err := semanticarango.New(client)
	if err != nil {
		return generationStores{}, err
	}
	ingestevent.EmitEvent(opts.EventSink, "go_backend_connect_complete", map[string]any{
		"backend":  "arango",
		"url":      opts.URL,
		"database": opts.Database,
		"seconds":  time.Since(connectStart).Seconds(),
	})

	bootstrapStart := time.Now()

	ingestevent.EmitEvent(opts.EventSink, "go_bootstrap_start", map[string]any{
		"database":    opts.Database,
		"resources":   0,
		"truncate":    false,
		"collections": "dataset_lifecycle",
	})
	if err = client.Bootstrap(ctx, bootstrap.LifecycleBootstrapSpecWithReporter(opts.EventSink)); err != nil {
		return generationStores{}, err
	}
	ingestevent.EmitEvent(opts.EventSink, "go_bootstrap_start", map[string]any{
		"database":  opts.Database,
		"resources": len(resourceTypes),
		"truncate":  false,
	})
	if err = client.Bootstrap(ctx, bootstrap.BootstrapSpecWithReporter(resourceTypes, false, opts.EventSink)); err != nil {
		return generationStores{}, err
	}
	summary.StageSeconds["bootstrap"] = time.Since(bootstrapStart).Seconds()

	lifecycleStore, err := publicationarango.New(client)
	if err != nil {
		return generationStores{}, err
	}

	return generationStores{client: client, catalog: catalogStore, semantic: semanticStore, lifecycle: lifecycleStore}, nil
}

func loadGeneration(ctx context.Context, opts LoadOptions) (summary LoadSummary, err error) {
	opts = normalizeLoadOptions(opts)

	start := time.Now()
	files, schema, plan, err := prepareGenerationLoad(opts, &summary)
	if err != nil {
		return summary, err
	}
	if plan == nil {
		return summary, fmt.Errorf("dataset generation plan is required")
	}
	datasetRef := plan.Dataset
	opts.IdentityFormat = plan.Manifest.IdentityFormat
	summary.Dataset = &datasetRef
	if err := ctx.Err(); err != nil {
		return summary, err
	}

	writeStats := &generationWriteStats{samples: make(map[string][]generationWriteSample)}
	ctx = context.WithValue(ctx, generationWriteStatsKey{}, writeStats)

	stores, err := connectGenerationStores(ctx, opts, summary.Preflight, &summary)
	if err != nil {
		return summary, err
	}
	client, catalogStore, semanticStore, lifecycleStore := stores.client, stores.catalog, stores.semantic, stores.lifecycle
	defer func() {
		err = generationCleanupError(ctx, generationCleanupTimeout, err, "close generation backend", client.Close)
	}()
	manifest, err := lifecycleStore.CreateManifest(ctx, plan.Manifest)
	if err != nil {
		return summary, err
	}
	manifestStaged := false
	defer func() {
		if err == nil || manifestStaged {
			return
		}

		err = generationCleanupError(ctx, generationCleanupTimeout, err, fmt.Sprintf("mark dataset generation %s/%s failed", plan.Dataset.Project, plan.Dataset.Generation), func(cleanupCtx context.Context) error {
			_, err := lifecycleStore.TransitionManifest(cleanupCtx, manifest, publication.StateFailed)
			return err
		})
	}()
	inventoryBuild := semantic.NewBuild(opts.Project, plan.Dataset.Generation, opts.AuthResourcePath)
	inventoryBuild.EntryIndexVersion = semantic.EntryIndexVersion
	if err = semanticStore.BeginSemanticInventoryBuild(ctx, inventoryBuild); err != nil {
		return summary, err
	}
	inventoryComplete := false
	defer func() {
		if err == nil || inventoryComplete {
			return
		}
		diagnostic := err.Error()
		err = generationCleanupError(ctx, generationCleanupTimeout, err, "mark semantic inventory build failed", func(cleanupCtx context.Context) error {
			return semanticStore.FailSemanticInventoryBuild(cleanupCtx, inventoryBuild, diagnostic)
		})
	}()
	catalogs := make(map[catalog.FieldProfileIdentity]*fieldprofile.Profiler)
	relationshipCounts := make(map[catalog.RelationshipKey]int64)
	lastInventoryCheckpoint := ""
	rowErrorCandidates := make([][]row.RowErrorSample, len(files))
	defer func() {
		summary.RowErrors = selectGenerationRowErrorSamples(rowErrorCandidates)
	}()
	outcomes := newGenerationFileOutcomes(opts.MetaDir, files, func(advanceCtx context.Context, scanned int64, checkpoint string) error {
		return semanticStore.AdvanceSemanticInventoryBuild(advanceCtx, inventoryBuild, scanned, checkpoint)
	})
	insertCounters := &generationInsertCounters{}
	eventSink := opts.EventSink
	var eventMutex sync.Mutex
	opts.EventSink = func(event string, fields map[string]any) {
		eventMutex.Lock()
		defer eventMutex.Unlock()
		if event == "go_load_progress" {
			fields["vertices_inserted"] = int(atomic.LoadInt64(&insertCounters.vertices))
			fields["edges_inserted"] = int(atomic.LoadInt64(&insertCounters.edges))
			fields["seconds"] = ingestevent.SecondsSince(start)
		}
		ingestevent.EmitEvent(eventSink, event, fields)
	}
	opts.fileInsertedCounters = insertCounters
	shapeCaches := make(map[string]*fieldprofile.ShapePlanCache)
	for _, file := range files {
		resourceType := source.ResourceTypeFromPath(file)
		if shapeCaches[resourceType] == nil {
			shapeCaches[resourceType] = fieldprofile.NewShapePlanCacheWithLimits(opts.CatalogLimits.MaxShapePlans, opts.CatalogLimits.MaxRetainedBytes)
		}
	}
	loadErr := runGenerationFiles(ctx, opts, files, func(fileCtx context.Context, fileOpts LoadOptions, index int, file string) error {
		fileStart := time.Now()
		resourceType := source.ResourceTypeFromPath(file)
		ingestevent.EmitEvent(fileOpts.EventSink, "go_load_file_start", map[string]any{"file": file, "resource": resourceType})
		fileOpts.fileShapePlanCache = shapeCaches[resourceType]

		result, fileErr := fileLoader{options: fileOpts, client: client, schema: schema, datasetGeneration: plan.Dataset.Generation, overwrite: false, insert: bootstrap.InsertRawDocuments}.load(fileCtx, file, start, 0, 0)
		checkpointErr := outcomes.complete(ctx, index, result, fileErr)
		if fileErr == nil && checkpointErr == nil {
			ingestevent.EmitEvent(fileOpts.EventSink, "go_load_file_complete", map[string]any{
				"file":          filepath.Base(file),
				"resource":      result.ResourceType,
				"file_rows":     result.Rows,
				"file_vertices": result.VerticesBuilt,
				"file_edges":    result.EdgesBuilt,
				"seconds":       time.Since(fileStart).Seconds(),
			})
		}
		return errors.Join(fileErr, checkpointErr)
	})
	fileResults := outcomes.results
	fileErrors := outcomes.errors
	fileFinished := outcomes.finished
	lastInventoryCheckpoint = outcomes.lastCheckpoint
	inventoryRunsByResource := make(map[string][]string)
	for _, result := range fileResults {
		if len(result.SemanticInventoryEntryRuns) == 0 {
			continue
		}
		inventoryRunsByResource[result.ResourceType] = append(inventoryRunsByResource[result.ResourceType], result.SemanticInventoryEntryRuns...)
	}
	defer func() {
		for _, paths := range inventoryRunsByResource {
			for _, path := range paths {
				_ = os.Remove(path)
			}
		}
	}()
	for index := range files {
		if !fileFinished[index] {
			continue
		}
		if fileErrors[index] != nil {
			if loadErr == nil {
				loadErr = fileErrors[index]
			}
			continue
		}
		result := fileResults[index]
		summary.VerticesInserted += result.VerticesInserted
		summary.EdgesInserted += result.EdgesInserted
		summary.ValidationErrors += result.ValidationErrors
		summary.GenerationErrors += result.GenerationErrors
		summary.EdgeErrors += result.EdgeErrors
		rowErrorCandidates[index] = result.RowErrors
		summary.Resources[result.ResourceType] += result.Rows
		summary.BatchCounts["vertex_insert"] += result.VertexBatches
		summary.BatchCounts["edge_insert"] += result.EdgeBatches
		for name, seconds := range result.StageSeconds {
			summary.StageSeconds[name] += seconds
		}
		summary.BatchCounts["semantic_inventory"] += result.SemanticInventoryBatches
		catalog.MergeRelationshipCounts(relationshipCounts, result.RelationshipCounts)

		key := catalog.NewFieldProfileIdentity(opts.Project, plan.Dataset.Generation, opts.AuthResourcePath, result.ResourceType)
		merged := catalogs[key]
		if merged == nil {
			merged = fieldprofile.NewProfilerForGenerationWithLimits(
				key.Project,
				key.DatasetGeneration,
				key.AuthResourcePath,
				key.ResourceType,
				nil,
				opts.CatalogLimits,
			)
			catalogs[key] = merged
		}
		if mergeErr := merged.Merge(result.Catalog); mergeErr != nil && loadErr == nil {
			loadErr = fmt.Errorf("merge catalog for dataset generation %s/%s %s: %w", plan.Dataset.Project, plan.Dataset.Generation, result.ResourceType, mergeErr)
		}

	}
	if loadErr != nil {
		return summary, loadErr
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
	inventoryWriter := generationInventoryWriter{
		client: client, dataset: plan.Dataset, batchSize: opts.BatchSize, writeAPI: opts.WriteAPI, stats: writeStats,
	}
	if err := inventoryWriter.writeRuns(ctx, inventoryRunsByResource, &summary); err != nil {
		return summary, err
	}

	finalizationStart := time.Now()
	recordFinalization := func(stage string) {
		now := time.Now()
		seconds := now.Sub(finalizationStart).Seconds()
		summary.StageSeconds[stage] = seconds
		ingestevent.EmitEvent(opts.EventSink, "go_load_finalization", map[string]any{"stage": stage, "seconds": seconds})
		finalizationStart = now
	}
	for _, key := range sortedGenerationCatalogKeys(catalogs) {
		if err = ctx.Err(); err != nil {
			return summary, err
		}
		if err = catalogStore.WriteFieldCatalog(
			ctx,
			fieldprofile.FieldCatalogCollection,
			catalogs[key].Documents(),
			opts.BatchSize,
			false,
			opts.WriteAPI,
			summary.StageSeconds,
		); err != nil {
			return summary, err
		}
	}
	if err = catalogStore.WriteRelationshipCatalog(
		ctx,
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
	recordFinalization("catalog_writes")
	resourceInventorySummary, inventoryErr := catalogStore.RebuildResourceInventory(ctx, catalog.ResourceInventoryRebuildOptions{
		Project: opts.Project, DatasetGeneration: plan.Dataset.Generation,
		ResourceTypes: summary.SchemaIdentity.GeneratedResourceTypes, BatchSize: opts.BatchSize, WriteAPI: opts.WriteAPI,
	})
	if inventoryErr != nil {
		return summary, fmt.Errorf("build resource inventory for dataset generation %s/%s: %w", plan.Dataset.Project, plan.Dataset.Generation, inventoryErr)
	}
	ingestevent.EmitEvent(opts.EventSink, "go_resource_inventory_complete", map[string]any{
		"dataset_generation": plan.Dataset.Generation, "build_id": resourceInventorySummary.BuildID,
		"resource_type_count": resourceInventorySummary.ResourceTypeCount, "count_row_count": resourceInventorySummary.CountRowCount,
		"document_count": resourceInventorySummary.DocumentCount,
	})
	recordFinalization("resource_inventory")
	stagedManifest, transitionErr := StageManifest(ctx, lifecycleStore, manifest, func() error {
		if err := semanticStore.CompleteSemanticInventoryBuild(ctx, inventoryBuild, totalResourceRows(summary.Resources), lastInventoryCheckpoint); err != nil {
			return fmt.Errorf("complete semantic inventory build: %w", err)
		}
		recordFinalization("semantic_inventory_completion")
		inventoryComplete = true
		return nil
	})
	if transitionErr != nil {
		return summary, transitionErr
	}
	recordFinalization("manifest_transition")
	manifest = stagedManifest
	manifestStaged = true
	if !opts.StageOnly && !opts.DeferActivation {
		if activationErr := releasecommit.ActivateGeneration(ctx, client, lifecycleStore, manifest); activationErr != nil {
			return summary, &ActivationOutcomeError{Dataset: plan.Dataset, Err: activationErr}
		}
		recordFinalization("activation")
	}

	ingestevent.EmitEvent(opts.EventSink, "go_load_complete", map[string]any{
		"write_stats":        writeStats.summary(),
		"files":              summary.Files,
		"vertices_inserted":  summary.VerticesInserted,
		"edges_inserted":     summary.EdgesInserted,
		"validation_errors":  summary.ValidationErrors,
		"generation_errors":  summary.GenerationErrors,
		"edge_errors":        summary.EdgeErrors,
		"dataset_generation": plan.Dataset.Generation,
		"seconds":            ingestevent.SecondsSince(start),
	})
	return summary, nil
}
