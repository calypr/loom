package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	queryapi "github.com/calypr/loom/internal/api/graphql/graph/query"
	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/backends/postgres/control"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	publication "github.com/calypr/loom/internal/dataframe/publication"
	publicationclickhouse "github.com/calypr/loom/internal/dataframe/publication/clickhouse"
	"github.com/calypr/loom/internal/dataframe/published"
	publicationcontract "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/artifactfs"
	"github.com/calypr/loom/internal/ingest"
	clickhousestore "github.com/calypr/loom/internal/store/clickhouse"
)

const postgreSQLRuntimeAcquireTimeout = 30 * time.Second

func postgreSQLRuntimeUnavailable(operation string) error {
	return dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable,
		operation+" has no PostgreSQL adapter yet; the PostgreSQL runtime refuses it instead of serving ArangoDB data",
		dataframeerrors.WithRetryable(false),
		dataframeerrors.WithDetails(map[string]any{"backend": "postgresql", "stage": "adapter"}))
}

func postgresRuntimeAssignment(pg PostgreSQLConfig) (control.SeriesSchemas, map[control.Series]string, error) {
	required := []control.Series{
		control.SeriesSource,
		control.SeriesExplorerControl,
		control.SeriesPublicationControl,
		control.SeriesDatasetRelease,
		control.SeriesRecipeChains,
		control.SeriesRecipeExecRegistries,
	}
	if len(pg.Schemas) != len(required) {
		return nil, nil, ErrPostgreSQLRuntimeNotWired
	}
	assignment := control.SeriesSchemas{}
	names := map[control.Series]string{}
	seen := map[string]bool{}
	for _, series := range required {
		schema, assigned := pg.Schemas[string(series)]
		schema = strings.TrimSpace(schema)
		if !assigned || schema == "" || seen[schema] {
			return nil, nil, ErrPostgreSQLRuntimeNotWired
		}
		seen[schema] = true
		assignment[series] = schema
		names[series] = schema
	}
	return assignment, names, nil
}

type pgExplorerStore struct {
	*control.ExplorerStore
	publish *control.PublishStore
}

var _ explorer.Store = pgExplorerStore{}

func (s pgExplorerStore) PublishAuthoring(ctx context.Context, receipt explorer.CompilationReceipt, revision explorer.Revision, release publicationcontract.ProjectRelease, expectedReleaseRevision int64) (*explorer.Revision, error) {
	if s.publish == nil {
		return nil, postgreSQLRuntimeUnavailable("Explorer publication (the dataset_release assignment is missing)")
	}
	return s.publish.PublishAuthoring(ctx, receipt, revision, release, expectedReleaseRevision)
}

func (s pgExplorerStore) ActivateRepositoryGeneration(context.Context, string, string, string) error {
	return postgreSQLRuntimeUnavailable("Repository-generation activation")
}

func (s pgExplorerStore) ValidateSelectionReferences(context.Context, string, string, authscope.ReadScope, []explorer.ResourceRef) error {
	return postgreSQLRuntimeUnavailable("Explicit-selection reference validation")
}

type pgExplorerReads struct {
	*control.ExplorerStore
	*control.ExplicitGroupStore
}

var _ explorer.ExplicitGroupRepository = pgExplorerReads{}

type pgIngestRunner struct {
	BaseOptions ingest.LoadOptions
	PG          ingest.PGSourceOptions
}

func (r pgIngestRunner) RunGeneration(ctx context.Context, req loadapi.GenerationLoadRequest, sink ingest.EventSink) (ingest.LoadSummary, error) {
	ref, err := publicationcontract.NewRef(req.Project, req.Generation)
	if err != nil {
		return ingest.LoadSummary{}, err
	}
	opts := r.BaseOptions
	opts.Project = req.Project
	opts.AuthResourcePath = req.AuthResourcePath
	opts.MetaDir = req.StagedDir
	opts.Dataset = &ref
	opts.DeferActivation = req.DeferActivation
	opts.Truncate = false
	opts.EventSink = sink
	opts.StageOnly = false
	summary, err := ingest.LoadPostgresGeneration(ctx, opts, r.PG)
	if err != nil {
		return summary, err
	}
	if !req.DeferActivation {
		if err := ingest.ActivatePostgresGeneration(ctx, ref, r.PG); err != nil {
			return summary, err
		}
	}
	return summary, nil
}

type pgGenerationActivator struct {
	manifests publicationcontract.ManifestLifecycle
}

func (a pgGenerationActivator) ReadManifest(ctx context.Context, ref publicationcontract.Ref) (publicationcontract.Manifest, error) {
	if a.manifests == nil {
		return publicationcontract.Manifest{}, postgreSQLRuntimeUnavailable("Generation manifest reads (the dataset_release assignment is missing)")
	}
	return a.manifests.ReadManifest(ctx, ref)
}

func (a pgGenerationActivator) Activate(ctx context.Context, manifest publicationcontract.Manifest) error {
	if a.manifests == nil {
		return postgreSQLRuntimeUnavailable("Server-side generation activation (the dataset_release assignment is missing)")
	}
	ref := manifest.Dataset
	if !manifest.IsStaged() {
		if err := manifest.Validate(); err != nil {
			return err
		}
		return fmt.Errorf("activate dataset generation %s/%s: %w: state is %s", ref.Project, ref.Generation, publicationcontract.ErrGenerationNotReady, manifest.State)
	}
	var expected *publicationcontract.Ref
	active, resolveErr := a.manifests.ResolveActiveManifest(ctx, ref.Project)
	switch {
	case resolveErr == nil:
		if active.IsStaged() {
			expected = &active.Dataset
		}
	case errors.Is(resolveErr, publicationcontract.ErrNoActiveGeneration):
		expected = nil
	default:
		expected = nil
	}
	if err := a.manifests.ActivateGeneration(ctx, manifest, expected); err != nil {
		if errors.Is(err, control.ErrActivationConflict) ||
			errors.Is(err, publicationcontract.ErrGenerationNotReady) ||
			errors.Is(err, publicationcontract.ErrInvalidActiveGeneration) {
			return err
		}
		if pgActivationOutcomeUnknown(err) {
			return &ingest.ActivationOutcomeError{Dataset: ref, Err: err}
		}
		return err
	}
	return nil
}

func pgActivationOutcomeUnknown(err error) bool {
	if err == nil {
		return false
	}
	if control.CommitOutcomeUnknown(err) {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

func composePostgreSQLFullRuntime(ctx context.Context, serverConfig Config, logger *slog.Logger) (server *httpapi.HTTPServer, closeRuntime func() error, err error) {
	var cleanups []func() error
	defer func() {
		if err == nil {
			return
		}
		_ = runRuntimeCloseSteps(cleanups)
	}()
	assignment, schemas, err := postgresRuntimeAssignment(serverConfig.Server.PostgreSQL)
	if err != nil {
		return nil, nil, err
	}
	pg := serverConfig.Server.PostgreSQL
	if err := pg.validate(); err != nil {
		return nil, nil, err
	}
	dsn := postgresFirstQueryRouteDSN(pg)
	runtime, err := openPostgresBackend(ctx, control.Config{
		DSN:            dsn,
		MaxConns:       int32(pg.MaxOpenConns),
		MinConns:       int32(pg.MaxIdleConns),
		AcquireTimeout: postgreSQLRuntimeAcquireTimeout,
		Schemas:        assignment,
		Setup:          control.SetupNone,
	})
	if err != nil {
		return nil, nil, postgresFirstQueryRouteError(fmt.Errorf("open PostgreSQL server runtime: %w", err), pg, dsn)
	}
	cleanups = append(cleanups, func() error {
		return postgresFirstQueryRouteError(runtime.Close(), pg, dsn)
	})
	if runtime.Explorer == nil || runtime.Publish == nil || runtime.Interpretations == nil ||
		runtime.ExplicitGroups == nil || runtime.CapabilitySnapshots == nil || runtime.TableShapes == nil ||
		runtime.Bundles == nil || runtime.RecipeRevisions == nil || runtime.RecipeExecutions == nil ||
		runtime.Catalog == nil || runtime.SemanticReads == nil || runtime.Releases == nil || runtime.Manifests == nil {
		return nil, nil, postgresFirstQueryRouteError(fmt.Errorf("%w: assembled PostgreSQL runtime is missing a required data-plane port", control.ErrBootstrap), pg, dsn)
	}

	binding, err := ir.NewPostgresStorageBinding(schemas[control.SeriesSource])
	if err != nil {
		return nil, nil, postgresFirstQueryRouteError(fmt.Errorf("bind PostgreSQL storage placement: %w", err), pg, dsn)
	}
	sourceCompileTarget, err := compiler.PostgreSQLTarget(binding)
	if err != nil {
		return nil, nil, postgresFirstQueryRouteError(fmt.Errorf("select PostgreSQL compile target: %w", err), pg, dsn)
	}

	dataframes := dataframeexecution.NewService(dataframeexecution.ServiceConfig{
		SourceRows: configuredPostgreSQLSourceRows(logger, "query_rows", runtime.SourceRows),
	})

	discoveryCache := catalog.NewCache()
	discoverFields := discoveryCache.DiscoverFields(runtime.DiscoverFields)

	auth, err := wireAuth(serverConfig, serverConfig.Server.AllowUnauthenticated || serverConfig.Auth.AllowUnauthenticated, runtime.DiscoverExistingAuthResourcePaths)
	if err != nil {
		return nil, nil, fmt.Errorf("configure authentication: %w", err)
	}
	authenticator, authorizer, scopeResolver := auth.authenticator, auth.authorizer, auth.scopeResolver

	degradation := error(nil)
	publicationReady := true
	var clickhouse *clickhousestore.Client
	var materializationReader *published.Reader
	var privateClickHouseArtifacts *chartifact.Manager
	var plane *publicationPlane
	if serverConfig.Server.ClickHouse.Enabled {
		var closeClickHouse func() error
		var planeErr error
		plane, closeClickHouse, planeErr = newPublicationPlane(ctx, serverConfig, logger, publicationPlaneInput{
			InitialDegradation:      degradation,
			InitialPublicationReady: publicationReady,
			ClickHouse:              clickhousestore.Options{URL: serverConfig.Server.ClickHouse.URL, Database: serverConfig.Server.ClickHouse.Database, Username: serverConfig.Server.ClickHouse.Username, Password: serverConfig.Server.ClickHouse.Password},
			AcquireArtifactCatalog: func(context.Context, *clickhousestore.Client) (chartifact.Catalog, error) {
				return runtime.Chartifacts, nil
			},
			BundleCatalog:          runtime.Bundles,
			ActiveManifestResolver: runtime.Manifests,
			ActiveReleaseResolver:  runtime.Releases,
			Scopes:                 scopeResolver,
		})
		if closeClickHouse != nil {
			cleanups = append(cleanups, closeClickHouse)
		}
		if planeErr != nil {
			return nil, nil, planeErr
		}
		clickhouse = plane.ClickHouse
		privateClickHouseArtifacts = plane.PrivateArtifacts
		materializationReader = plane.MaterializationReader
		degradation = plane.Degradation
		publicationReady = plane.PublicationReady
	}

	var clickHouseQueryRows dataframeexecution.ClickHouseQueryRows
	var resolveClickHouseInputs dataframeexecution.ResolveClickHouseInputs
	var withExecutionReadPins dataframeexecution.WithExecutionReadPins
	if plane != nil {
		clickHouseQueryRows = plane.ClickHouseQueryRows
		resolveClickHouseInputs = plane.ResolveClickHouseInputs
		withExecutionReadPins = plane.WithExecutionReadPins
	}

	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		CompileTarget:              sourceCompileTarget,
		Registry:                   runtime.RecipeExecutions,
		Revisions:                  runtime.RecipeRevisions,
		ResolveBundle:              recipeSchemaResolver(runtime.DiscoverFields, discoveryCache),
		PreparePreviewIndex:        nil,
		PreviewCoveringIndexes:     nil,
		PreviewCollectionRevision:  nil,
		PreviewCollectionScans:     nil,
		PreviewCollectionCount:     nil,
		ClickHouseQueryRows:        clickHouseQueryRows,
		PreviewSourceRows:          configuredPostgreSQLSourceRows(logger, "preview_query_rows", runtime.SourceRows),
		ResolveClickHouseInputs:    resolveClickHouseInputs,
		WithExecutionReadPins:      withExecutionReadPins,
		PrivateClickHouseArtifacts: privateClickHouseArtifacts,
		SourceRows:                 configuredPostgreSQLSourceRows(logger, "query_rows", runtime.SourceRows),
		ScopeDigest:                recipeScopeDigest,
		RootPageRows:               serverConfig.Server.RecipeQueryPageRows,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create dataframe recipe engine: %w", err)
	}
	if serverConfig.Server.ClickHouse.Enabled && !recipeEngine.CombinePortsConfigured() {
		return nil, nil, fmt.Errorf("PostgreSQL runtime has ClickHouse enabled but the recipe engine is missing a private workspace Combine port")
	}

	var bundleTarget publication.Target
	if plane != nil {
		if err := plane.reconcileBundles(ctx, logger, publicationReady, degradation, runtime.Bundles, runtime.Releases,
			func(ctx context.Context, clickhouse *clickhousestore.Client, _ *publicationclickhouse.ClickHouseBundleStore, cutoff time.Time) (string, error) {
				reconciler, err := runtime.NewPublicationReconciler(runtime.Releases)
				if err != nil {
					return "publication reconciler", err
				}
				if err := reconciler.SweepStale(ctx, cutoff, func(ctx context.Context, physicalTable string) error {
					return clickhouse.DropTable(ctx, physicalTable)
				}); err != nil {
					return "dataframe publication reconciliation", err
				}
				return "", nil
			}); err != nil {
			return nil, nil, err
		}
		publicationReady = plane.PublicationReady
		degradation = plane.Degradation
		bundleTarget = plane.BundleTarget
	}

	hooks := newReleaseHooks(serverConfig, runtime.Manifests, runtime.Releases, runtime.Bundles)

	explorerStore := pgExplorerStore{ExplorerStore: runtime.Explorer, publish: runtime.Publish}
	explorerReads := pgExplorerReads{ExplorerStore: runtime.Explorer, ExplicitGroupStore: runtime.ExplicitGroups}
	capabilityResolver, err := newExplorerCapabilityResolver(runtime.Catalog, scopeResolver, runtime.Manifests, runtime.CapabilitySnapshots)
	if err != nil {
		return nil, nil, fmt.Errorf("create Explorer capability resolver: %w", err)
	}
	explorerService, err := explorer.NewService(explorerStore)
	if err != nil {
		return nil, nil, fmt.Errorf("create Explorer service: %w", err)
	}
	artifactStore, err := artifactfs.New(serverConfig.Server.ArtifactDirectory)
	if err != nil {
		return nil, nil, fmt.Errorf("create Explorer artifact store: %w", err)
	}

	resolver := graphresolver.NewResolver(graphresolver.ResolverConfig{
		DataframeQuery: queryapi.Config{
			CompileTarget:          sourceCompileTarget,
			DiscoverReferences:     nil,
			DiscoverFields:         discoverFields,
			Dataframes:             dataframes,
			ScopeResolver:          scopeResolver,
			ActiveManifestResolver: runtime.Manifests,
			Explain:                nil,
		},
		MaterializationReader: materializationReader,
		Logger:                logger,
		RecipeControl: dataframeexecution.Control{
			Engine: recipeEngine,
			ExplainPostgreSQLConnection: func(ctx context.Context, compiled compiler.CompiledQuery) (dataframeexecution.PostgreSQLPlanAssessment, error) {
				return explainPostgreSQLCompiledQuery(ctx, runtime.PlanJSON, compiled)
			},
		},
		RecipeAuthorizer: recipeAuthorization{resolver: scopeResolver},
		RecipeRevisions:  runtime.RecipeRevisions,
		RecipeExecutions: graphresolver.NewAuthorizedRecipeExecutionReader(runtime.Bundles, scopeResolver),
	})

	ingestRunner := pgIngestRunner{
		BaseOptions: ingest.LoadOptions{Schema: serverConfig.Server.Schema},
		PG: ingest.PGSourceOptions{
			DSN:            dsn,
			Schema:         schemas[control.SeriesSource],
			ManifestSchema: schemas[control.SeriesDatasetRelease],
			Catalog: ingest.GenerationCatalogWriters{
				FieldProfiles: runtime.FieldProfiles,
				Inventory:     runtime.Inventory,
				Relationships: runtime.Relationships,
				Contributions: runtime.Contributions,
			},
		},
	}
	generationService, err := loadapi.NewService(loadapi.ServiceConfig{
		LoadGeneration:      ingestRunner.RunGeneration,
		GenerationActivator: pgGenerationActivator{manifests: runtime.Manifests},
		DataframeReleases:   runtime.Bundles,
		Logger:              logger,
		OnSuccess: func(project string) {
			discoveryCache.InvalidateProject(project)
			if scopeResolver != nil {
				scopeResolver.InvalidateProject(project)
			}
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create generation load service: %w", err)
	}

	lifecycleConfig, err := buildLifecycleConfig(lifecycleCompositionInput{
		ServerConfig:          serverConfig,
		Logger:                logger,
		RecipeEngine:          recipeEngine,
		CapabilityResolver:    capabilityResolver,
		ExplorerService:       explorerService,
		ArtifactStore:         artifactStore,
		MaterializationReader: materializationReader,
		ScopeResolver:         scopeResolver,
		BundleTarget:          bundleTarget,
		Degradation:           degradation,
		Hooks:                 hooks,
		Ports: lifecycleLanePorts{
			SemanticInventory:                  runtime.SemanticReads.PageSemanticInventory,
			ResolveSemanticInventorySelections: runtime.SemanticReads.ResolveSemanticInventorySelections,
			SelectionMembersCollection:         "",
			InterpretationRepository:           runtime.Interpretations,
			TableShapeCapabilities:             runtime.TableShapes,
			ExplicitGroups:                     explorerReads,
			SelectionReferenceValidator:        explorerStore.ValidateSelectionReferences,
			ReceiptExecutions:                  runtime.Bundles,
		},
	})
	if err != nil {
		return nil, nil, err
	}
	server, err = httpapi.NewHTTPServer(httpapi.HTTPConfig{Authenticator: authenticator, Authorizer: authorizer, Logger: logger,
		CoreReadyCheck: func(ctx context.Context) error {
			return runtime.Readiness(ctx)
		},
		CoreState: postgreSQLCoreReadinessState,
		ClickHouseReadyCheck: func(ctx context.Context) error {
			if clickhouse == nil {
				return nil
			}
			return clickhouse.Ping(ctx)
		}, ClickHouseEnabled: serverConfig.Server.ClickHouse.Enabled})
	if err != nil {
		return nil, nil, fmt.Errorf("create HTTP server: %w", err)
	}
	if err := wireExplorerRoutes(server, generationService, authorizer, resolver, scopeResolver, explorerService, lifecycleConfig, constructionInputsCatalog{
		reader: materializationReader, catalog: runtime.Bundles, capabilities: lifecycleConfig.Capability,
		scopes: scopeResolver, explorers: explorerService, revisions: explorerService,
	}, routeDependencies{releases: runtime.Bundles, scopes: auth.scopePolicy}); err != nil {
		return nil, nil, err
	}
	if privateClickHouseArtifacts != nil {
		reconcileCtx, cancelReconciler := context.WithCancel(ctx)
		reconcilerDone := startPrivateArtifactReconciler(reconcileCtx, privateClickHouseArtifacts, logger)
		cleanups = append(cleanups, func() error {
			cancelReconciler()
			timer := time.NewTimer(cleanupTimeout)
			defer timer.Stop()
			select {
			case <-reconcilerDone:
			case <-timer.C:
				logger.Error("private ClickHouse artifact reconciler did not stop before shutdown deadline")
			}
			return nil
		})
	}
	if artifactStore != nil {
		artifactReconcileCtx, cancelArtifactReconciler := context.WithCancel(ctx)
		artifactReconcilerDone := startArtifactFSReconciler(artifactReconcileCtx, artifactStore, logger)
		cleanups = append(cleanups, func() error {
			cancelArtifactReconciler()
			timer := time.NewTimer(cleanupTimeout)
			defer timer.Stop()
			select {
			case <-artifactReconcilerDone:
			case <-timer.C:
				logger.Error("filesystem artifact reconciler did not stop before shutdown deadline")
			}
			return nil
		})
	}
	closeRuntime = func() error { return runRuntimeCloseSteps(cleanups) }
	return server, closeRuntime, nil
}

func runRuntimeCloseSteps(steps []func() error) error {
	var firstErr error
	for i := len(steps) - 1; i >= 0; i-- {
		if cerr := steps[i](); cerr != nil && firstErr == nil {
			firstErr = cerr
		}
	}
	return firstErr
}

func runPostgreSQLFullRuntime(ctx context.Context, serverConfig Config, logger *slog.Logger) error {
	server, closeRuntime, err := composePostgreSQLFullRuntime(ctx, serverConfig, logger)
	if err != nil {
		return err
	}
	_, schemas, _ := postgresRuntimeAssignment(serverConfig.Server.PostgreSQL)
	return serveRuntime(ctx, server, serverConfig.Server.Listen, closeRuntime, "close PostgreSQL server runtime", logger,
		"listen", serverConfig.Server.Listen, "backend", serverConfig.Server.Backend,
		"source_schema", schemas[control.SeriesSource],
		"explorer_schema", schemas[control.SeriesExplorerControl],
		"publication_schema", schemas[control.SeriesPublicationControl],
		"dataset_release_schema", schemas[control.SeriesDatasetRelease],
		"recipe_chains_schema", schemas[control.SeriesRecipeChains],
		"recipe_exec_schema", schemas[control.SeriesRecipeExecRegistries],
		"no_auth", serverConfig.Server.AllowUnauthenticated || serverConfig.Auth.AllowUnauthenticated)
}
