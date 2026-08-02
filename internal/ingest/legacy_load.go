package ingest

import (
	"context"
	"path/filepath"
	"time"

	"github.com/bmeg/jsonschemagraph/graph"
	"github.com/calypr/loom/internal/catalog"
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
	for _, file := range files {
		resourceType := ResourceTypeFromPath(file)
		fileStart := time.Now()
		emitEvent(opts.EventSink, "go_load_file_start", map[string]any{"file": file, "resource": resourceType})
		result, err := loadFile(
			ctx,
			opts,
			client,
			schema,
			file,
			"",
			!opts.Truncate,
			start,
			summary.VerticesInserted,
			summary.EdgesInserted,
			insertRawDocuments,
		)
		if err != nil {
			return summary, err
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
		summary.Resources[resourceType] += result.Rows
		summary.BatchCounts["vertex_insert"] += result.VertexBatches
		summary.BatchCounts["edge_insert"] += result.EdgeBatches
		for name, seconds := range result.StageSeconds {
			summary.StageSeconds[name] += seconds
		}

		overwrite := !opts.Truncate
		if err := catalog.WriteFieldCatalog(ctx, client, catalog.FieldCatalogCollection, result.Catalog.Documents(), opts.BatchSize, overwrite, opts.WriteAPI, summary.StageSeconds); err != nil {
			return summary, err
		}
		relationshipDocs := catalog.RelationshipCatalogDocuments(result.RelationshipCounts)
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
			"file_rows":     result.Rows,
			"file_vertices": result.VerticesBuilt,
			"file_edges":    result.EdgesBuilt,
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
