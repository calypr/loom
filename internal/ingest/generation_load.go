package ingest

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/bmeg/jsonschemagraph/graph"
	postgres "github.com/calypr/loom/internal/backends/postgres"
	"github.com/calypr/loom/internal/backends/postgres/control"
	"github.com/calypr/loom/internal/catalog"
	publication "github.com/calypr/loom/internal/dataset"
)

const (
	pgResourceBatchPass    = "r"
	pgEdgeBatchPass        = "e"
	pgBatchIDPrefix        = "ing03:"
	pgApplicationNameBase  = "loom-ing03"
	pgSemanticFlushMinimum = 2000
)

type PGSourceOptions struct {
	DSN                    string
	Schema                 string
	ManifestSchema         string
	MaxConns               int32
	AcquireTimeout         time.Duration
	ApplicationName        string
	SemanticFlushThreshold int
	Catalog                GenerationCatalogWriters
}

var (
	ErrGenerationAlreadyStaged              = errors.New("dataset generation is already staged")
	ErrGenerationLoadAlreadyFailed          = errors.New("dataset generation load already failed")
	ErrGenerationManifestIdentityConflict   = errors.New("dataset generation manifest identity conflict")
	ErrGenerationManifestOutcomeUnknown     = errors.New("dataset generation manifest commit outcome is unknown")
	ErrPGGenerationDatasetRequired          = errors.New("postgres generation load requires an explicit dataset generation")
	ErrGenerationLoadRequiresCatalogWriters = errors.New("postgres generation load requires all four catalog evidence writers")
)

type GenerationFieldProfileWriter interface {
	WriteFieldProfile(ctx context.Context, scope control.FieldProfileScope, docs []catalog.FieldCatalogDocument, batchSize int) error
}

type GenerationInventoryRebuilder interface {
	RebuildResourceInventory(ctx context.Context, opts catalog.ResourceInventoryRebuildOptions) (catalog.ResourceInventoryRebuildSummary, error)
}

type GenerationRelationshipDiscoverer interface {
	DiscoverRelationshipObservations(ctx context.Context, opts catalog.RelationshipObservationOptions) (catalog.RelationshipObservationResult, error)
}

type GenerationSemanticContributor interface {
	WriteSemanticInventoryContributions(ctx context.Context, contributions []catalog.SemanticInventoryContribution, batchSize int) error
}

type GenerationCatalogWriters struct {
	FieldProfiles GenerationFieldProfileWriter
	Inventory     GenerationInventoryRebuilder
	Relationships GenerationRelationshipDiscoverer
	Contributions GenerationSemanticContributor
}

func (w GenerationCatalogWriters) Complete() bool {
	return w.FieldProfiles != nil && w.Inventory != nil &&
		w.Relationships != nil && w.Contributions != nil
}

type pgGenerationLoad struct {
	opts LoadOptions
	pg   PGSourceOptions
	plan *generationLoadPlan
	ref  publication.Ref

	runtime     *postgres.Runtime
	lifecycle   publication.ManifestLifecycle
	resourceW   *PostgresResourceWriter
	edgeW       *PostgresEdgeWriter
	profiles    GenerationFieldProfileWriter
	inventory   GenerationInventoryRebuilder
	relations   GenerationRelationshipDiscoverer
	contribs    GenerationSemanticContributor
	application string

	manifest     publication.Manifest
	ownsManifest bool
	staged       bool

	pendingCatalogs map[generationCatalogKey]*catalog.Profiler
	semanticBuffer  []catalog.SemanticInventoryContribution
	semanticFlush   int

	resourceWindow int
	edgeWindow     int
	profileBatch   int
	inventoryBatch int
}

func LoadPostgresGeneration(ctx context.Context, opts LoadOptions, pg PGSourceOptions) (summary LoadSummary, err error) {
	start := time.Now()
	opts = normalizeLoadOptions(opts)
	if !pg.Catalog.Complete() {
		return LoadSummary{}, fmt.Errorf("%w: field profiles, inventory, relationships, and semantic contributions must all be wired", ErrGenerationLoadRequiresCatalogWriters)
	}

	if info, statErr := os.Stat(opts.MetaDir); statErr != nil || !info.IsDir() {
		return LoadSummary{}, fmt.Errorf("%w: %s", ErrGenerationLoadRequiresDirectory, opts.MetaDir)
	}
	files, err := DiscoverNDJSON(opts.MetaDir)
	if err != nil {
		return LoadSummary{}, err
	}
	if len(files) == 0 {
		return LoadSummary{}, fmt.Errorf("%w: %s", ErrGenerationLoadRequiresFiles, opts.MetaDir)
	}
	summary = LoadSummary{
		Files:        len(files),
		Resources:    map[string]int{},
		BatchCounts:  map[string]int{},
		StageSeconds: map[string]float64{},
	}

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
		"backend":            "postgres",
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
	if plan == nil {
		return summary, fmt.Errorf("dataset generation plan is required")
	}
	if strings.TrimSpace(plan.Dataset.Generation) == "" {
		return summary, ErrPGGenerationDatasetRequired
	}
	datasetRef := plan.Dataset
	summary.Dataset = &datasetRef
	if err := ctx.Err(); err != nil {
		return summary, safePointCancel(err)
	}

	resourceTypes := make([]string, 0, len(preflight.Resources))
	for _, resource := range preflight.Resources {
		resourceTypes = append(resourceTypes, resource.ResourceType)
	}

	load := &pgGenerationLoad{opts: opts, pg: pg, plan: plan, ref: datasetRef}
	if load.application = strings.TrimSpace(pg.ApplicationName); load.application == "" {
		load.application = fmt.Sprintf("%s-%d-%s", pgApplicationNameBase, time.Now().UnixNano(), pgRandomHexToken())
	}

	emitEvent(opts.EventSink, "go_pg_connect_start", map[string]any{
		"backend": "postgres",
		"schema":  pg.Schema,
	})
	connectStart := time.Now()
	if err := load.open(); err != nil {
		return summary, err
	}
	defer func() {
		err = generationCleanupError(ctx, generationCleanupTimeout, err, "close postgres generation backend",
			func(context.Context) error { return load.close() })
	}()
	summary.StageSeconds["pg_connect"] = time.Since(connectStart).Seconds()
	emitEvent(opts.EventSink, "go_pg_connect_complete", map[string]any{
		"backend":   "postgres",
		"schema":    pg.Schema,
		"seconds":   summary.StageSeconds["pg_connect"],
		"app_name":  load.application,
		"resources": len(resourceTypes),
	})

	emitEvent(opts.EventSink, "go_pg_bootstrap_start", map[string]any{
		"schema":   pg.Schema,
		"truncate": false,
	})
	bootstrapStart := time.Now()
	if _, err := load.runtime.MigrateSchema(ctx); err != nil {
		return summary, err
	}
	if _, err := load.runtime.MigrateDatasetReleaseSchema(ctx); err != nil {
		return summary, err
	}
	summary.StageSeconds["pg_bootstrap"] = time.Since(bootstrapStart).Seconds()
	emitEvent(opts.EventSink, "go_pg_bootstrap_complete", map[string]any{
		"schema":  pg.Schema,
		"seconds": summary.StageSeconds["pg_bootstrap"],
	})

	if err := load.claimManifest(ctx); err != nil {
		return summary, err
	}
	defer func() {
		if err == nil || load.staged || !load.ownsManifest {
			return
		}
		if pgCommitOutcomeUnknown(err) {
			return
		}
		err = generationCleanupError(ctx, generationCleanupTimeout, err,
			fmt.Sprintf("mark dataset generation %s/%s failed", plan.Dataset.Project, plan.Dataset.Generation),
			func(cleanupCtx context.Context) error {
				_, transitionErr := load.lifecycle.TransitionManifest(cleanupCtx, load.manifest, publication.StateFailed)
				return transitionErr
			})
	}()

	var rowErrorCandidates [][]RowErrorSample
	defer func() {
		summary.RowErrors = selectGenerationRowErrorSamples(rowErrorCandidates)
	}()

	if err := load.writeResourcePass(ctx, schema, files, &summary, &rowErrorCandidates); err != nil {
		return summary, load.surfaceStoreError(err, "resource pass")
	}
	if summary.ValidationErrors != 0 || summary.GenerationErrors != 0 || summary.EdgeErrors != 0 {
		return summary, &GenerationLoadIncompleteError{
			Dataset:          plan.Dataset,
			ValidationErrors: summary.ValidationErrors,
			GenerationErrors: summary.GenerationErrors,
			EdgeErrors:       summary.EdgeErrors,
		}
	}
	if err := ctx.Err(); err != nil {
		return summary, safePointCancel(err)
	}

	if err := load.writeEdgePass(ctx, schema, files, &summary); err != nil {
		return summary, load.surfaceStoreError(err, "edge pass")
	}
	if err := ctx.Err(); err != nil {
		return summary, safePointCancel(err)
	}

	if err := load.writeCatalogEvidence(ctx, schemaIdentity, &summary); err != nil {
		return summary, load.surfaceStoreError(err, "catalog evidence")
	}
	if err := ctx.Err(); err != nil {
		return summary, safePointCancel(err)
	}

	stagedManifest, transitionErr := load.lifecycle.TransitionManifest(ctx, load.manifest, publication.StateStaged)
	if transitionErr != nil {
		if pgCommitOutcomeUnknown(transitionErr) {
			return summary, fmt.Errorf("%w: stage dataset generation %s/%s: %w",
				ErrGenerationManifestOutcomeUnknown, plan.Dataset.Project, plan.Dataset.Generation, transitionErr)
		}
		if errors.Is(transitionErr, control.ErrManifestTransitionConflict) {
			if stored, readErr := load.lifecycle.ReadManifest(ctx, load.ref); readErr == nil {
				if stored.IsStaged() {
					return summary, fmt.Errorf("%w: %s/%s is already STAGED (a concurrent loader of the same identity won the transition); read back the committed load instead of re-running it",
						ErrGenerationAlreadyStaged, plan.Dataset.Project, plan.Dataset.Generation)
				}
				if stored.State == publication.StateFailed {
					return summary, fmt.Errorf("%w: %s/%s; a new dataset generation is required",
						ErrGenerationLoadAlreadyFailed, plan.Dataset.Project, plan.Dataset.Generation)
				}
			}
		}
		return summary, transitionErr
	}
	load.manifest = stagedManifest
	load.staged = true

	emitEvent(opts.EventSink, "go_load_complete", map[string]any{
		"backend":            "postgres",
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

func ActivatePostgresGeneration(ctx context.Context, ref publication.Ref, pg PGSourceOptions) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(pg.ApplicationName) == "" {
		pg.ApplicationName = pgApplicationNameBase + "-activate"
	}
	client, err := openPGGenerationClient(pg, nil)
	if err != nil {
		return err
	}
	defer client.Close()
	store := client.Manifests
	if store == nil {
		return fmt.Errorf("postgres runtime exposes no dataset manifest lifecycle port; the DatasetReleaseSeries schema must be assigned in the factory config")
	}
	manifest, err := store.ReadManifest(ctx, ref)
	if err != nil {
		return err
	}
	if !manifest.IsStaged() {
		if err := manifest.Validate(); err != nil {
			return err
		}
		return fmt.Errorf("activate dataset generation %s/%s: %w: state is %s",
			ref.Project, ref.Generation, publication.ErrGenerationNotReady, manifest.State)
	}
	var expected *publication.Ref
	active, resolveErr := store.ResolveActiveManifest(ctx, ref.Project)
	switch {
	case resolveErr == nil:
		if active.IsStaged() {
			expected = &active.Dataset
		}
	case errors.Is(resolveErr, publication.ErrNoActiveGeneration):
		expected = nil
	default:
		expected = nil
	}
	if activationErr := store.ActivateGeneration(ctx, manifest, expected); activationErr != nil {
		if errors.Is(activationErr, control.ErrActivationConflict) ||
			errors.Is(activationErr, publication.ErrGenerationNotReady) ||
			errors.Is(activationErr, publication.ErrInvalidActiveGeneration) {
			return activationErr
		}
		if pgCommitOutcomeUnknown(activationErr) {
			return &ActivationOutcomeError{Dataset: ref, Err: activationErr}
		}
		return activationErr
	}
	return nil
}

func openPGGenerationClient(pg PGSourceOptions, eventSink EventSink) (*postgres.Runtime, error) {
	maxConns := pg.MaxConns
	if maxConns == 0 {
		maxConns = 4
	}
	acquireTimeout := pg.AcquireTimeout
	if acquireTimeout <= 0 {
		acquireTimeout = 30 * time.Second
	}
	dsn := strings.TrimSpace(pg.DSN)
	application := strings.TrimSpace(pg.ApplicationName)
	if application != "" {
		var tagged bool
		dsn, tagged = joinApplicationName(dsn, application)
		if !tagged {
			emitEvent(eventSink, "go_pg_application_name_skipped", map[string]any{
				"application": application,
				"reason":      "DSN is not URL-form; keyword/value DSNs keep an untagged connection rather than a guessed syntax",
			})
		}
	}
	return postgres.Open(context.Background(), PGLoadConfig(pg, dsn, control.SetupNone))
}

func PGLoadConfig(pg PGSourceOptions, taggedDSN string, setup control.SetupPolicy) control.Config {
	maxConns := pg.MaxConns
	if maxConns == 0 {
		maxConns = 4
	}
	acquireTimeout := pg.AcquireTimeout
	if acquireTimeout <= 0 {
		acquireTimeout = 30 * time.Second
	}
	schemas := make(control.SeriesSchemas, 2)
	schemas[control.SeriesSource] = pg.Schema
	schemas[control.SeriesDatasetRelease] = pg.ManifestSchema
	return control.Config{
		DSN:            taggedDSN,
		MaxConns:       maxConns,
		MinConns:       0,
		AcquireTimeout: acquireTimeout,
		Schemas:        schemas,
		Setup:          setup,
	}
}

func (l *pgGenerationLoad) open() error {
	runtime, err := openPGGenerationClient(l.pg, l.opts.EventSink)
	if err != nil {
		return err
	}
	l.runtime = runtime
	if err := l.bind(); err != nil {
		l.close()
		return err
	}
	return nil
}

func (l *pgGenerationLoad) close() error {
	if l.runtime != nil {
		_ = l.runtime.Close()
		l.runtime = nil
	}
	return nil
}

func (l *pgGenerationLoad) bind() error {
	schema := l.pg.Schema
	var err error
	if l.runtime.Manifests == nil {
		return fmt.Errorf("postgres runtime exposes no dataset manifest lifecycle port; the DatasetReleaseSeries schema must be assigned in the factory config")
	}
	l.lifecycle = l.runtime.Manifests
	if l.resourceW, err = NewPostgresResourceWriter(l.runtime.ConnSource(), schema); err != nil {
		return err
	}
	if l.edgeW, err = NewPostgresEdgeWriter(l.runtime.ConnSource(), schema); err != nil {
		return err
	}
	l.profiles = l.pg.Catalog.FieldProfiles
	l.inventory = l.pg.Catalog.Inventory
	l.relations = l.pg.Catalog.Relationships
	l.contribs = l.pg.Catalog.Contributions
	l.semanticFlush = l.pg.SemanticFlushThreshold
	if l.semanticFlush <= 0 {
		l.semanticFlush = 2 * l.opts.BatchSize
		if l.semanticFlush < pgSemanticFlushMinimum {
			l.semanticFlush = pgSemanticFlushMinimum
		}
	}
	l.resourceWindow = min(l.opts.BatchSize, control.MaxBatchResources)
	l.edgeWindow = min(l.opts.BatchSize, control.MaxBatchEdges)
	l.profileBatch = min(l.opts.BatchSize, control.MaxFieldProfileWriteBatch)
	l.inventoryBatch = min(l.opts.BatchSize, control.MaxInventoryWriteBatch)
	return nil
}

func (l *pgGenerationLoad) claimManifest(ctx context.Context) error {
	created, err := l.lifecycle.CreateManifest(ctx, l.plan.Manifest)
	if err == nil {
		l.manifest = created
		l.ownsManifest = true
		emitEvent(l.opts.EventSink, "go_pg_manifest_create", map[string]any{
			"dataset_generation": l.ref.Generation,
			"state":              string(created.State),
		})
		return nil
	}
	if !errors.Is(err, control.ErrManifestAlreadyExists) {
		if pgCommitOutcomeUnknown(err) {
			return fmt.Errorf("%w: create dataset generation manifest %s/%s: %w",
				ErrGenerationManifestOutcomeUnknown, l.ref.Project, l.ref.Generation, err)
		}
		return err
	}
	stored, readErr := l.lifecycle.ReadManifest(ctx, l.ref)
	if readErr != nil {
		if pgCommitOutcomeUnknown(readErr) {
			return fmt.Errorf("%w: read dataset generation manifest %s/%s: %w",
				ErrGenerationManifestOutcomeUnknown, l.ref.Project, l.ref.Generation, readErr)
		}
		return readErr
	}
	if !reflect.DeepEqual(stored.SchemaIdentity, l.plan.Manifest.SchemaIdentity) {
		return fmt.Errorf("%w: %s/%s stores a different immutable schema identity; one load cannot claim another load's manifest",
			ErrGenerationManifestIdentityConflict, l.ref.Project, l.ref.Generation)
	}
	switch {
	case stored.IsStaged():
		l.manifest = stored
		return fmt.Errorf("%w: %s/%s is already STAGED; read back the committed load instead of re-running it",
			ErrGenerationAlreadyStaged, l.ref.Project, l.ref.Generation)
	case stored.State == publication.StateFailed:
		l.manifest = stored
		return fmt.Errorf("%w: %s/%s; a new dataset generation is required",
			ErrGenerationLoadAlreadyFailed, l.ref.Project, l.ref.Generation)
	default:
		l.manifest = stored
		l.ownsManifest = false
		emitEvent(l.opts.EventSink, "go_pg_manifest_resume", map[string]any{
			"dataset_generation": l.ref.Generation,
			"state":              string(stored.State),
		})
		return nil
	}
}

func (l *pgGenerationLoad) writeResourcePass(
	ctx context.Context,
	schema *graph.GraphSchema,
	files []string,
	summary *LoadSummary,
	rowErrorCandidates *[][]RowErrorSample,
) error {
	shapeCache := catalog.NewShapePlanCacheWithLimits(l.opts.CatalogLimits.MaxShapePlans, l.opts.CatalogLimits.MaxRetainedBytes)
	catalogs := make(map[generationCatalogKey]*catalog.Profiler)
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return safePointCancel(err)
		}
		fileStart := time.Now()
		resourceType := ResourceTypeFromPath(file)
		fileID := semanticInventorySourceFileID(l.opts.MetaDir, file)
		emitEvent(l.opts.EventSink, "go_pg_load_file_start", map[string]any{
			"file": file, "resource": resourceType, "pass": "resources",
		})

		rowBuilder, err := l.newRowBuilder(schema, resourceType)
		if err != nil {
			return err
		}
		profiler := catalog.NewProfilerForGenerationWithLimits(
			l.opts.Project, l.plan.Dataset.Generation, l.opts.AuthResourcePath, resourceType,
			shapeCache, l.opts.CatalogLimits)
		localTimings := map[string]float64{}

		scanner, closeFn, err := OpenLineScanner(file)
		if err != nil {
			return err
		}
		var windowRows []rowBuildResult
		windowIndex := 0
		lineNumber := 0
		fileRows := 0
		var fileErrors fileLoadResult
		scanErr := func() (loopErr error) {
			defer func() {
				if loopErr == nil && len(windowRows) > 0 {
					loopErr = l.commitResourceWindow(ctx, fileID, windowIndex, windowRows, summary)
					windowRows = nil
				}
			}()
			for scanner.Scan() {
				lineNumber++
				if err := ctx.Err(); err != nil {
					return safePointCancel(err)
				}
				line := strings.TrimSpace(scanner.Text())
				if line == "" {
					continue
				}
				built, errorType, buildErr := rowBuilder.Build(resourceType, []byte(line), localTimings)
				if buildErr != nil {
					if l.opts.FailFast {
						return fmt.Errorf("%s %s row %d: %w", filepath.Base(file), resourceType, lineNumber, buildErr)
					}
					*rowErrorCandidates = append(*rowErrorCandidates, []RowErrorSample{{
						File:         filepath.Base(file),
						Line:         lineNumber,
						ResourceType: resourceType,
						Category:     string(errorType),
						Message:      buildErr.Error(),
					}})
					switch errorType {
					case rowErrorValidation:
						fileErrors.ValidationErrors++
					case rowErrorGeneration:
						fileErrors.GenerationErrors++
					case rowErrorEdge:
						fileErrors.EdgeErrors++
					}
					continue
				}
				sourceID := semanticInventorySourceRecordID(fileID, lineNumber)
				if err := profiler.ObservePayloadWithInventory(built.payload, localTimings, sourceID,
					func(contribution catalog.SemanticInventoryContribution) {
						l.semanticBuffer = append(l.semanticBuffer, contribution)
					}); err != nil {
					return fmt.Errorf("observe semantic inventory for %s row %d: %w", filepath.Base(file), lineNumber, err)
				}
				if len(l.semanticBuffer) >= l.semanticFlush {
					if err := l.flushSemanticContributions(ctx, summary); err != nil {
						return err
					}
				}
				windowRows = append(windowRows, built)
				fileRows++
				if lineNumber%l.resourceWindow == 0 {
					if err := l.commitResourceWindow(ctx, fileID, windowIndex, windowRows, summary); err != nil {
						return err
					}
					windowRows = nil
					windowIndex++
				}
			}
			return scanner.Err()
		}()
		if err := errors.Join(scanErr, closeFn()); err != nil {
			return err
		}

		summary.Resources[resourceType] += fileRows
		summary.ValidationErrors += fileErrors.ValidationErrors
		summary.GenerationErrors += fileErrors.GenerationErrors
		summary.EdgeErrors += fileErrors.EdgeErrors
		for name, seconds := range localTimings {
			summary.StageSeconds[name] += seconds
		}
		key := generationCatalogKey{
			project:           l.opts.Project,
			datasetGeneration: l.plan.Dataset.Generation,
			authResourcePath:  l.opts.AuthResourcePath,
			resourceType:      resourceType,
		}
		if merged := catalogs[key]; merged == nil {
			catalogs[key] = profiler
		} else if err := merged.Merge(profiler); err != nil {
			return fmt.Errorf("merge catalog for dataset generation %s/%s %s: %w",
				l.plan.Dataset.Project, l.plan.Dataset.Generation, resourceType, err)
		}
		emitEvent(l.opts.EventSink, "go_pg_load_file_complete", map[string]any{
			"file":      filepath.Base(file),
			"resource":  resourceType,
			"file_rows": fileRows,
			"pass":      "resources",
			"seconds":   time.Since(fileStart).Seconds(),
		})
	}
	l.pendingCatalogs = catalogs
	return nil
}

func (l *pgGenerationLoad) commitResourceWindow(ctx context.Context, fileID string, window int, rows []rowBuildResult, summary *LoadSummary) error {
	if len(rows) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return safePointCancel(err)
	}
	batchID := pgGenerationBatchID(l.plan.Dataset.Project, l.plan.Dataset.Generation, fileID, pgResourceBatchPass, window)
	result, err := l.resourceW.WriteResourceBatch(ctx, PGResourceBatchScope{
		BatchID:           batchID,
		Project:           l.plan.Dataset.Project,
		DatasetGeneration: l.plan.Dataset.Generation,
		AuthResourcePath:  l.opts.AuthResourcePath,
	}, rows)
	if err != nil {
		return fmt.Errorf("commit postgres resource batch %s (%d rows): %w", batchID, len(rows), err)
	}
	if !result.Replay {
		summary.VerticesInserted += int(result.Committed)
	}
	summary.BatchCounts["resource_batch"]++
	if result.Replay {
		summary.BatchCounts["resource_batch_replay"]++
	}
	emitEvent(l.opts.EventSink, "go_pg_resource_batch_commit", map[string]any{
		"batch":   batchID,
		"offered": result.Offered,
		"commit":  result.Committed,
		"replay":  result.Replay,
		"digest":  result.Digest,
	})
	return nil
}

func (l *pgGenerationLoad) writeEdgePass(ctx context.Context, schema *graph.GraphSchema, files []string, summary *LoadSummary) error {
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return safePointCancel(err)
		}
		fileStart := time.Now()
		resourceType := ResourceTypeFromPath(file)
		fileID := semanticInventorySourceFileID(l.opts.MetaDir, file)
		emitEvent(l.opts.EventSink, "go_pg_load_file_start", map[string]any{
			"file": file, "resource": resourceType, "pass": "edges",
		})
		rowBuilder, err := l.newRowBuilder(schema, resourceType)
		if err != nil {
			return err
		}
		localTimings := map[string]float64{}
		scanner, closeFn, err := OpenLineScanner(file)
		if err != nil {
			return err
		}
		var windowRows []rowBuildResult
		windowIndex := 0
		lineNumber := 0
		windowEdges := 0
		scanErr := func() (loopErr error) {
			defer func() {
				if loopErr == nil && len(windowRows) > 0 {
					loopErr = l.commitEdgeWindow(ctx, fileID, windowIndex, windowRows, summary)
					windowRows = nil
				}
			}()
			for scanner.Scan() {
				lineNumber++
				if err := ctx.Err(); err != nil {
					return safePointCancel(err)
				}
				line := strings.TrimSpace(scanner.Text())
				if line == "" {
					continue
				}
				built, _, buildErr := rowBuilder.Build(resourceType, []byte(line), localTimings)
				if buildErr != nil {
					return fmt.Errorf("%s %s row %d rebuilt with error: %w", filepath.Base(file), resourceType, lineNumber, buildErr)
				}
				if len(built.edges) > control.MaxBatchEdges {
					return fmt.Errorf("%s %s row %d produces %d edges, exceeding the %d-per-batch edge bound; nothing was written",
						filepath.Base(file), resourceType, lineNumber, len(built.edges), control.MaxBatchEdges)
				}
				if len(windowRows) > 0 && windowEdges+len(built.edges) > control.MaxBatchEdges {
					if err := l.commitEdgeWindow(ctx, fileID, windowIndex, windowRows, summary); err != nil {
						return err
					}
					windowRows = nil
					windowEdges = 0
					windowIndex++
				}
				if len(built.edges) > 0 {
					windowRows = append(windowRows, built)
					windowEdges += len(built.edges)
				}
				if lineNumber%l.edgeWindow == 0 {
					if err := l.commitEdgeWindow(ctx, fileID, windowIndex, windowRows, summary); err != nil {
						return err
					}
					windowRows = nil
					windowEdges = 0
					windowIndex++
				}
			}
			return scanner.Err()
		}()
		if err := errors.Join(scanErr, closeFn()); err != nil {
			return err
		}
		emitEvent(l.opts.EventSink, "go_pg_load_file_complete", map[string]any{
			"file":     filepath.Base(file),
			"resource": resourceType,
			"pass":     "edges",
			"seconds":  time.Since(fileStart).Seconds(),
		})
	}
	return nil
}

func (l *pgGenerationLoad) commitEdgeWindow(ctx context.Context, fileID string, window int, rows []rowBuildResult, summary *LoadSummary) error {
	if len(rows) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return safePointCancel(err)
	}
	batchID := pgGenerationBatchID(l.plan.Dataset.Project, l.plan.Dataset.Generation, fileID, pgEdgeBatchPass, window)
	result, err := l.edgeW.WriteEdgeBatch(ctx, PGEdgeBatchScope{
		BatchID:           batchID,
		Project:           l.plan.Dataset.Project,
		DatasetGeneration: l.plan.Dataset.Generation,
		AuthResourcePath:  l.opts.AuthResourcePath,
	}, rows)
	if err != nil {
		return fmt.Errorf("commit postgres edge batch %s (%d rows): %w", batchID, len(rows), err)
	}
	if !result.Replay {
		summary.EdgesInserted += int(result.Committed)
	}
	summary.BatchCounts["edge_batch"]++
	if result.Replay {
		summary.BatchCounts["edge_batch_replay"]++
	}
	emitEvent(l.opts.EventSink, "go_pg_edge_batch_commit", map[string]any{
		"batch":   batchID,
		"offered": result.Offered,
		"commit":  result.Committed,
		"replay":  result.Replay,
		"digest":  result.Digest,
	})
	return nil
}

func (l *pgGenerationLoad) writeCatalogEvidence(ctx context.Context, schemaIdentity publication.SchemaSnapshot, summary *LoadSummary) error {
	fieldStart := time.Now()
	keys := sortedGenerationCatalogKeys(l.pendingCatalogs)
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return safePointCancel(err)
		}
		scope := control.FieldProfileScope{
			Project:           key.project,
			DatasetGeneration: key.datasetGeneration,
			AuthResourcePath:  key.authResourcePath,
			ResourceType:      key.resourceType,
		}
		if err := l.profiles.WriteFieldProfile(ctx, scope, l.pendingCatalogs[key].Documents(), l.profileBatch); err != nil {
			return fmt.Errorf("write field profile for %s/%s %s: %w", key.project, key.datasetGeneration, key.resourceType, err)
		}
		summary.BatchCounts["field_profile_write"]++
	}
	summary.StageSeconds["catalog_field_profiles"] = time.Since(fieldStart).Seconds()
	emitEvent(l.opts.EventSink, "go_pg_catalog_field_profiles", map[string]any{"scopes": len(keys)})

	inventoryStart := time.Now()
	var inventory catalog.ResourceInventoryRebuildSummary
	inventoryErr := control.WithAdvisoryLock(ctx, l.runtime.ConnSource(),
		control.GenerationAdvisoryLockKey(
			l.plan.Dataset.Project, l.plan.Dataset.Generation),
		func() error {
			var rebuildErr error
			inventory, rebuildErr = l.inventory.RebuildResourceInventory(ctx, catalog.ResourceInventoryRebuildOptions{
				Project:           l.plan.Dataset.Project,
				DatasetGeneration: l.plan.Dataset.Generation,
				ResourceTypes:     schemaIdentity.GeneratedResourceTypes,
				BatchSize:         l.inventoryBatch,
			})
			return rebuildErr
		})
	if inventoryErr != nil {
		return fmt.Errorf("build resource inventory for dataset generation %s/%s: %w",
			l.plan.Dataset.Project, l.plan.Dataset.Generation, inventoryErr)
	}
	summary.StageSeconds["catalog_resource_inventory"] = time.Since(inventoryStart).Seconds()
	summary.BatchCounts["resource_inventory"]++
	emitEvent(l.opts.EventSink, "go_pg_resource_inventory_complete", map[string]any{
		"dataset_generation":  l.plan.Dataset.Generation,
		"build_id":            inventory.BuildID,
		"resource_type_count": inventory.ResourceTypeCount,
		"count_row_count":     inventory.CountRowCount,
		"document_count":      inventory.DocumentCount,
	})

	relationshipStart := time.Now()
	relationship, err := l.relations.DiscoverRelationshipObservations(ctx, catalog.RelationshipObservationOptions{
		Project:           l.plan.Dataset.Project,
		DatasetGeneration: l.plan.Dataset.Generation,
		AuthResourcePaths: []string{l.opts.AuthResourcePath},
	})
	if err != nil {
		return fmt.Errorf("derive relationship evidence for dataset generation %s/%s: %w",
			l.plan.Dataset.Project, l.plan.Dataset.Generation, err)
	}
	summary.StageSeconds["catalog_relationship_evidence"] = time.Since(relationshipStart).Seconds()
	summary.BatchCounts["relationship_observations"] = len(relationship.Values)
	emitEvent(l.opts.EventSink, "go_pg_relationship_evidence_complete", map[string]any{
		"observations": len(relationship.Values),
		"digest":       relationship.Digest,
		"diagnostics":  len(relationship.Diagnostics),
	})

	semanticStart := time.Now()
	if err := l.flushSemanticContributions(ctx, summary); err != nil {
		return err
	}
	summary.StageSeconds["catalog_semantic_evidence"] = time.Since(semanticStart).Seconds()
	return nil
}

func (l *pgGenerationLoad) flushSemanticContributions(ctx context.Context, summary *LoadSummary) error {
	if len(l.semanticBuffer) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return safePointCancel(err)
	}
	chunk := l.semanticBuffer
	if err := l.contribs.WriteSemanticInventoryContributions(ctx, chunk, l.opts.BatchSize); err != nil {
		return fmt.Errorf("write semantic inventory contributions: %w", err)
	}
	summary.BatchCounts["semantic_contributions"] += len(chunk)
	summary.BatchCounts["semantic_chunks"]++
	emitEvent(l.opts.EventSink, "go_pg_semantic_write", map[string]any{"contributions": len(chunk)})
	l.semanticBuffer = nil
	return nil
}

func (l *pgGenerationLoad) newRowBuilder(schema *graph.GraphSchema, resourceType string) (RowBuilder, error) {
	class := schema.GetClass(resourceType)
	if class == nil {
		return nil, fmt.Errorf("%s: class %q not found in graph schema", filepath.Base(resourceType), resourceType)
	}
	var rowBuilder RowBuilder
	if l.opts.UseGeneric || !supportsGeneratedLoad(resourceType) {
		rowBuilder = NewGenericRowBuilder(l.opts.Project, class, schema, graphExtraArgs(l.opts.Project, l.opts.AuthResourcePath))
	} else {
		rowBuilder = NewGeneratedRowBuilder(l.opts.Project, l.opts.AuthResourcePath)
	}
	qualified, err := newGenerationRowBuilder(rowBuilder, l.opts.Project, l.plan.Dataset.Generation)
	if err != nil {
		return nil, err
	}
	return qualified, nil
}

func pgGenerationBatchID(project, generation, fileID, pass string, window int) string {
	return pgBatchIDPrefix + pass + ":" + generationHash(
		"pg-ingest-03/batch/v1", project, generation, fileID, pass, strconv.Itoa(window),
	)
}

type pgSafePointCancelError struct{ err error }

func (e *pgSafePointCancelError) Error() string { return e.err.Error() }
func (e *pgSafePointCancelError) Unwrap() error { return e.err }

func safePointCancel(err error) error {
	if err == nil {
		return nil
	}
	return &pgSafePointCancelError{err: err}
}

func (l *pgGenerationLoad) surfaceStoreError(err error, stage string) error {
	if err == nil || !pgCommitOutcomeUnknown(err) {
		return err
	}
	return fmt.Errorf("%w: %s/%s %s left a commit outcome unknown: %w",
		ErrGenerationManifestOutcomeUnknown, l.ref.Project, l.ref.Generation, stage, err)
}

func pgCommitOutcomeUnknown(err error) bool {
	if err == nil {
		return false
	}
	var safeCancel *pgSafePointCancelError
	if errors.As(err, &safeCancel) {
		return false
	}
	if control.CommitOutcomeUnknown(err) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	return false
}

func joinApplicationName(dsn, application string) (string, bool) {
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return dsn, false
	}
	query := parsed.Query()
	if query.Get("application_name") != "" {
		return dsn, true
	}
	query.Set("application_name", application)
	parsed.RawQuery = query.Encode()
	return parsed.String(), true
}

func pgRandomHexToken() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "000000000000"
	}
	return fmt.Sprintf("%x", b[:])
}
