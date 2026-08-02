package ingest

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/calypr/loom/internal/catalog"
	publication "github.com/calypr/loom/internal/publication"
	publicationarango "github.com/calypr/loom/internal/publication/arango"

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
	schemaIdentity, err := loadSchemaSnapshot(opts.Schema)
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
		"schemaSha256":       schemaIdentity.SchemaSHA256,
		"generatedRootCount": len(schemaIdentity.GeneratedResourceTypes),
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

	lifecycleStore, err := publicationarango.New(client)
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
		// Once READY is persisted we deliberately leave it alone,
		// because an activation error is an unknown outcome rather than proof
		// that the generation failed.
		_, cleanupErr := lifecycleStore.TransitionManifest(
			context.WithoutCancel(ctx),
			manifest,
			publication.StateFailed,
		)
		if cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("mark dataset generation %s/%s failed: %w", plan.Dataset.Project, plan.Dataset.Generation, cleanupErr))
		}
	}()
	catalogs := make(map[generationCatalogKey]*catalog.Profiler)
	relationshipCounts := make(map[catalog.RelationshipKey]int64)
	for _, file := range files {
		if err = ctx.Err(); err != nil {
			return summary, err
		}
		fileStart := time.Now()
		resourceType := ResourceTypeFromPath(file)
		emitEvent(opts.EventSink, "go_load_file_start", map[string]any{"file": file, "resource": resourceType})

		result, fileErr := loadFile(
			ctx,
			opts,
			client,
			schema,
			file,
			plan.Dataset.Generation,
			false,
			start,
			summary.VerticesInserted,
			summary.EdgesInserted,
			insertRawDocuments,
		)
		if fileErr != nil {
			return summary, fileErr
		}

		summary.VerticesInserted += result.VerticesInserted
		summary.EdgesInserted += result.EdgesInserted
		summary.ValidationErrors += result.ValidationErrors
		summary.GenerationErrors += result.GenerationErrors
		summary.EdgeErrors += result.EdgeErrors
		if remaining := rowErrorSampleLimit - len(summary.RowErrors); remaining > 0 {
			if remaining > len(result.RowErrors) {
				remaining = len(result.RowErrors)
			}
			summary.RowErrors = append(summary.RowErrors, result.RowErrors[:remaining]...)
		}
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
	readyManifest, transitionErr := lifecycleStore.TransitionManifest(ctx, manifest, publication.StateReady)
	if transitionErr != nil {
		return summary, transitionErr
	}
	manifest = readyManifest
	manifestReady = true
	if activationErr := lifecycleStore.Activate(ctx, manifest); activationErr != nil {
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
