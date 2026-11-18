package server

import (
	"net/http"

	receiptapi "github.com/calypr/loom/internal/explorer/receipt"

	"github.com/calypr/loom/internal/explorer/importedsource"

	reciperuntime "github.com/calypr/loom/internal/dataframe/recipe/runtime"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"

	executionarango "github.com/calypr/loom/internal/dataframe/execution/arango"

	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	recipeapi "github.com/calypr/loom/internal/api/recipe"

	loadapi "github.com/calypr/loom/internal/api/bulk/load"

	dataframeapi "github.com/calypr/loom/internal/api/dataframe/dataframe"

	queryapi "github.com/calypr/loom/internal/api/dataframe/query"

	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"

	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	"github.com/calypr/loom/internal/dataframe/compiler"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"

	chartifactarango "github.com/calypr/loom/internal/dataframe/execution/chartifact/arango"

	publication "github.com/calypr/loom/internal/dataframe/publication"

	bundlearango "github.com/calypr/loom/internal/dataframe/publication/arango"

	publicationclickhouse "github.com/calypr/loom/internal/dataframe/publication/clickhouse"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"

	recipearango "github.com/calypr/loom/internal/dataframe/recipe/exec/arango"

	publicationcontract "github.com/calypr/loom/internal/dataset"

	publicationarango "github.com/calypr/loom/internal/dataset/arango"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/constructioninputs"

	explorerarango "github.com/calypr/loom/internal/explorer/arango"
	"github.com/calypr/loom/internal/explorer/artifactfs"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/ingest"
	"github.com/calypr/loom/internal/projectid"

	arangostore "github.com/calypr/loom/internal/store/arango"

	clickhousestore "github.com/calypr/loom/internal/store/clickhouse"
)

func Run() {
	options, err := parseServerOptions(os.Args[1:], flag.ContinueOnError)
	if err != nil {
		if err == flag.ErrHelp {
			return
		}
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, options); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

const cleanupTimeout = 10 * time.Second

func recordDegradation(logger *slog.Logger, current error, stage string, cause error) error {
	if cause == nil {
		return current
	}
	if logger != nil {
		logger.Error("dataframe startup degraded", "stage", stage, "error", cause)
	}
	return errors.Join(current, fmt.Errorf("%s: %w", stage, cause))
}

func preserveConfiguredQueryError(err error) error {
	err = executionarango.ClassifyDataframeQueryError(err)
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return previewRouteError(err)
	}
	if userErr, ok := dataframeerrors.AsUserError(err); ok {
		switch userErr.Code() {
		case string(dataframeerrors.CodeUnauthenticated):
			return &explorer.AuthoringError{Status: http.StatusUnauthorized, Diagnostic: explorer.AuthoringDiagnostic{
				Severity: "ERROR", Stage: "preview", Code: userErr.Code(), Message: dataframeerrors.PublicMessage(err),
			}, Cause: err}
		case string(dataframeerrors.CodeForbidden), string(dataframeerrors.CodeUnauthorizedProject):
			return &explorer.AuthoringError{Status: http.StatusForbidden, Diagnostic: explorer.AuthoringDiagnostic{
				Severity: "ERROR", Stage: "preview", Code: userErr.Code(), Message: dataframeerrors.PublicMessage(err),
			}, Cause: err}
		}
		return previewRouteError(err)
	}
	return err
}

func newExplorerReleaseActivator(releaseService *publicationcontract.ReleaseService, devReleaseCASFault *atomic.Bool) func(context.Context, string, string, []publicationcontract.DataframeSelector) error {
	return func(ctx context.Context, project, generation string, selectors []publicationcontract.DataframeSelector) error {
		expectedRevision := int64(0)

		devFaultArmed := devReleaseCASFault != nil && devReleaseCASFault.CompareAndSwap(true, false)
		active, err := releaseService.Active(ctx, project)
		if err == nil {
			expectedRevision = active.Revision

			if devFaultArmed {
				expectedRevision++
			}
		} else if !errors.Is(err, publicationcontract.ErrNoActiveRelease) {
			return err
		}
		_, err = releaseService.Activate(ctx, publicationcontract.ActivationRequest{
			Project: project, Generation: generation, GitCommit: generation,
			ExpectedRevision: expectedRevision, OptionalSelectors: selectors,
		})
		return err
	}
}

func run(ctx context.Context, serverConfig Config) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{}))
	connOpts := arangostore.ConnectionOptions{
		URL:      serverConfig.Server.URL,
		Database: serverConfig.Server.Database,
	}

	lifecycleClient, err := arangostore.Open(ctx, connOpts.URL, connOpts.Database)
	if err != nil {
		return fmt.Errorf("open dataset lifecycle store: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		_ = lifecycleClient.Close(closeCtx)
	}()
	if err := lifecycleClient.Bootstrap(ctx, publicationarango.BootstrapSpec()); err != nil {
		return fmt.Errorf("bootstrap dataset lifecycle store: %w", err)
	}
	if err := lifecycleClient.Bootstrap(ctx, arangostore.BootstrapSpec{
		Collections: []arangostore.CollectionSpec{
			{Name: catalog.FieldCatalogCollection},
			catalogarango.SemanticCoverageCollectionSpec(),
		},
	}); err != nil {
		return fmt.Errorf("bootstrap authorization catalog: %w", err)
	}
	var degradation error
	if err := lifecycleClient.Bootstrap(ctx, recipearango.BootstrapSpec()); err != nil {
		degradation = recordDegradation(logger, degradation, "bootstrap recipe registry", err)
	}
	if err := lifecycleClient.Bootstrap(ctx, recipearango.RevisionBootstrapSpec()); err != nil {
		degradation = recordDegradation(logger, degradation, "bootstrap recipe revision registry", err)
	}
	if err := lifecycleClient.Bootstrap(ctx, explorerarango.BootstrapSpec()); err != nil {
		return fmt.Errorf("bootstrap Explorer store: %w", err)
	}
	explorerStore, err := explorerarango.New(lifecycleClient)
	if err != nil {
		return fmt.Errorf("create Explorer store: %w", err)
	}
	recipeRegistry, err := recipearango.New(lifecycleClient)
	if err != nil {
		return fmt.Errorf("create recipe registry: %w", err)
	}
	var configuredRecipeName, configuredRecipeVersion string
	if serverConfig.Server.ClickHouse.Enabled {
		data, err := os.ReadFile(serverConfig.Server.Dataframer.Recipe)
		if err != nil {
			return fmt.Errorf("read dataframer recipe %q: %w", serverConfig.Server.Dataframer.Recipe, err)
		}
		defaultBundle, err := recipe.Parse(data)
		if err != nil {
			return fmt.Errorf("parse dataframer recipe %q: %w", serverConfig.Server.Dataframer.Recipe, err)
		}
		configuredRecipeName = defaultBundle.Name
		configuredRecipeVersion = defaultBundle.TranslationVersion
		if _, err := (exec.PersistentRegistry{Store: recipeRegistry}).RegisterVersion(ctx, defaultBundle); err != nil {
			degradation = recordDegradation(logger, degradation, "register default dataframe recipe", err)
		}
	}
	lifecycleStore, err := publicationarango.New(lifecycleClient)
	if err != nil {
		return fmt.Errorf("create dataset lifecycle store: %w", err)
	}
	activeManifestResolver := publicationcontract.ActiveResolver(lifecycleStore)

	discoveryCache := catalog.NewCache()
	catalogStore, err := catalogarango.New(lifecycleClient)
	if err != nil {
		return fmt.Errorf("create catalog store: %w", err)
	}
	discoverFields := discoveryCache.DiscoverFields(catalogStore.DiscoverFields)
	discoverReferences := discoveryCache.DiscoverReferences(catalogStore.DiscoverReferences)

	auth, err := wireAuth(serverConfig, serverConfig.Server.AllowUnauthenticated || serverConfig.Auth.AllowUnauthenticated, catalogStore.DiscoverExistingAuthResourcePaths)
	if err != nil {
		return fmt.Errorf("configure authentication: %w", err)
	}
	authenticator, authorizer, scopeResolver := auth.authenticator, auth.authorizer, auth.scopeResolver

	dataframes := dataframeexecution.NewService(dataframeexecution.ServiceConfig{QueryRows: func(ctx context.Context, query string, batch int, binds map[string]any, visit func(map[string]any) error) error {
		return executionarango.ClassifyDataframeQueryError(lifecycleClient.QueryRows(ctx, query, batch, binds, visit))
	}})

	publishedRegistry, err := bundlearango.New(lifecycleClient)
	if err != nil {
		return fmt.Errorf("create published dataframe registry: %w", err)
	}
	publicationReady := true
	if err := lifecycleClient.Bootstrap(ctx, bundlearango.BootstrapSpec()); err != nil {
		degradation = recordDegradation(logger, degradation, "bootstrap dataframe registry", err)
		publicationReady = false
	}
	var clickhouse *clickhousestore.Client
	var materializationReader *published.Reader
	var privateClickHouseArtifacts *chartifact.Manager
	if serverConfig.Server.ClickHouse.Enabled {
		clickhouse, err = clickhousestore.New(clickhousestore.Options{URL: serverConfig.Server.ClickHouse.URL, Database: serverConfig.Server.ClickHouse.Database, Username: serverConfig.Server.ClickHouse.Username, Password: serverConfig.Server.ClickHouse.Password})
		if err != nil {
			return fmt.Errorf("create ClickHouse client: %w", err)
		}
		defer clickhouse.Close()

		if err := clickhouse.EnsureDatabase(ctx); err != nil {
			degradation = recordDegradation(logger, degradation, "ClickHouse database", err)
			publicationReady = false
		}
		if err := lifecycleClient.Bootstrap(ctx, chartifactarango.BootstrapSpec()); err != nil {
			return fmt.Errorf("bootstrap private dataframe artifact catalog: %w", err)
		}
		privateArtifactCatalog, err := chartifactarango.NewCatalog(lifecycleClient)
		if err != nil {
			return fmt.Errorf("create private dataframe artifact catalog: %w", err)
		}
		privateClickHouseArtifacts, err = chartifact.New(chartifact.Config{
			Catalog: privateArtifactCatalog, ClickHouse: clickhouse,
			BatchRows: serverConfig.Server.RecipeBatchRows, BatchBytes: serverConfig.Server.RecipeBatchBytes,
		})
		if err != nil {
			return fmt.Errorf("create private ClickHouse artifact manager: %w", err)
		}
		materializationReader = &published.Reader{ClickHouse: clickhouse, Catalog: publishedRegistry, Logger: logger, MaxPage: 1000, ActiveManifestResolver: activeManifestResolver, ActiveReleaseResolver: lifecycleStore}
	}
	recipeRevisions, err := recipearango.NewRevisionRegistry(lifecycleClient)
	if err != nil {
		return fmt.Errorf("create recipe revision registry: %w", err)
	}
	var clickHouseQueryRows dataframeexecution.ClickHouseQueryRows
	if clickhouse != nil {
		clickHouseQueryRows = clickhouse.QueryRowsArgsVisit
	}
	var resolveClickHouseInputs dataframeexecution.ResolveClickHouseInputs
	var withExecutionReadPins dataframeexecution.WithExecutionReadPins
	if materializationReader != nil {
		resolver := reciperuntime.NewCombineInputResolver(materializationReader, scopeResolver)
		resolveClickHouseInputs = resolver.Resolve
		withExecutionReadPins = materializationReader.WithExecutionReadPins
	}
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:      recipeRegistry,
		Revisions:     recipeRevisions,
		ResolveBundle: recipeapi.RecipeSchemaResolver(catalogStore.DiscoverFields, discoveryCache),
		PreparePreviewIndex: executionarango.LoggedPreviewIndexPreparation(logger, func(ctx context.Context, spec compiler.PreviewCoveringIndexSpec) error {
			var err error
			if len(spec.StoredValues) != 0 {
				err = lifecycleClient.EnsurePreviewCoveringIndexWithStoredValues(ctx, spec.Collection, spec.Name, spec.Fields, spec.StoredValues)
			} else {
				err = lifecycleClient.EnsurePreviewCoveringIndex(ctx, spec.Collection, spec.Name, spec.Fields)
			}
			return err
		}),
		PreviewCoveringIndexes:    lifecycleClient.ListPreviewCoveringIndexes,
		PreviewCollectionRevision: lifecycleClient.CollectionRevision,
		PreviewExplainQuery: func(ctx context.Context, query string, bindVars map[string]any) (arangostore.ExplainResult, error) {
			return lifecycleClient.Explain(ctx, arangostore.ExplainRequest{Query: query, BindVars: bindVars})
		},
		PreviewCollectionCount: lifecycleClient.CollectionCount,
		ClickHouseQueryRows:    clickHouseQueryRows,
		PreviewQueryRows: executionarango.ConfiguredAQLQueryRows(logger, "preview_query_rows", preserveConfiguredQueryError, func(ctx context.Context, query string, batchSize int, bindVars map[string]any, visit func(map[string]any) error) error {
			return lifecycleClient.QueryRowsWithMaxRuntime(ctx, query, batchSize, bindVars, executionarango.PreviewQueryMaxRuntime(ctx), arangostore.RowVisitor(visit))
		}),
		WithPreviewReadSnapshot: func(ctx context.Context, run func(context.Context, dataframeexecution.QueryRows) error) error {
			collections, err := lifecycleClient.ListReadSnapshotCollections(ctx)
			if err != nil {
				return err
			}
			return lifecycleClient.WithReadSnapshot(ctx, collections, func(snapshotCtx context.Context, reader arangostore.RowQueryer) error {
				query := executionarango.ConfiguredAQLQueryRows(logger, "preview_query_rows", preserveConfiguredQueryError, executionarango.WithPreviewQueryTimeout(func(ctx context.Context, query string, batchSize int, bindVars map[string]any, visit func(map[string]any) error) error {
					ctx = arangostore.WithQueryMaxNumberOfPlans(ctx, 1)
					return reader.QueryRows(ctx, query, batchSize, bindVars, arangostore.RowVisitor(visit))
				}))
				return run(snapshotCtx, query)
			})
		},
		ResolveClickHouseInputs:    resolveClickHouseInputs,
		WithExecutionReadPins:      withExecutionReadPins,
		PrivateClickHouseArtifacts: privateClickHouseArtifacts,
		QueryRows: executionarango.ConfiguredAQLQueryRows(logger, "query_rows", preserveConfiguredQueryError, func(ctx context.Context, query string, batchSize int, bindVars map[string]any, visit func(map[string]any) error) error {
			return lifecycleClient.QueryRows(ctx, query, batchSize, bindVars, arangostore.RowVisitor(visit))
		}),
		ScopeDigest:  reciperuntime.ScopeDigest,
		RootPageRows: serverConfig.Server.RecipeQueryPageRows,
	})
	if err != nil {
		return fmt.Errorf("create dataframe recipe engine: %w", err)
	}
	var bundleTarget publication.Target
	if serverConfig.Server.ClickHouse.Enabled && publicationReady {
		bundleStore, err := publicationclickhouse.NewBundleStore(clickhouse, publishedRegistry)
		if err != nil {
			return fmt.Errorf("create dataframe bundle store: %w", err)
		}
		bundleStore.SetActiveReleaseReader(lifecycleStore)
		if err := bundleStore.Reconcile(ctx, time.Now().UTC().Add(-2*time.Minute)); err != nil {
			degradation = recordDegradation(logger, degradation, "dataframe publication reconciliation", err)
			publicationReady = false
		}
		if publicationReady {
			bundleTarget = bundleStore
		}
	}
	verificationStore := publication.NewPublicationVerifier(publishedRegistry)
	releaseService := &publicationcontract.ReleaseService{Manifests: lifecycleStore, Releases: lifecycleStore, Verifier: verificationStore, Required: serverConfig.Server.RequiredDataframeSelectors}
	var activationConflictOnce atomic.Bool
	activationConflictOnce.Store(serverConfig.Server.DevActivationConflictOnce)
	var repositoryActivationConflictOnce atomic.Bool
	repositoryActivationConflictOnce.Store(serverConfig.Server.DevRepositoryActivationConflictOnce)
	activateExplorerRelease := newExplorerReleaseActivator(releaseService, &repositoryActivationConflictOnce)
	prepareExplorerRelease := func(ctx context.Context, project, generation string, selectors []publicationcontract.DataframeSelector) (publicationcontract.ProjectRelease, int64, error) {
		expectedRevision := int64(0)
		active, err := releaseService.Active(ctx, project)
		if err == nil {
			expectedRevision = active.Revision
		} else if !errors.Is(err, publicationcontract.ErrNoActiveRelease) {
			return publicationcontract.ProjectRelease{}, 0, err
		}
		release, err := releaseService.Create(ctx, publicationcontract.ActivationRequest{
			Project: project, Generation: generation, GitCommit: generation,
			OptionalSelectors: selectors,
		})

		if err == nil && activationConflictOnce.CompareAndSwap(true, false) {
			expectedRevision++
		}
		return release, expectedRevision, err
	}
	validateExplorerReleaseGeneration := func(ctx context.Context, project, generation string) error {
		return releaseService.ValidateGeneration(ctx, project, generation)
	}
	if err := explorerStore.MigrateLegacyRepositoryConfigs(ctx); err != nil {
		return fmt.Errorf("migrate legacy Explorer repository configs: %w", err)
	}
	capabilitySnapshots, err := explorerarango.NewCapabilitySnapshotStore(lifecycleClient)
	if err != nil {
		return fmt.Errorf("create Explorer capability snapshot store: %w", err)
	}
	capabilityResolver, err := capabilityresolver.NewResolver(catalogStore, scopeResolver, activeManifestResolver, capabilitySnapshots)
	if err != nil {
		return fmt.Errorf("create Explorer capability resolver: %w", err)
	}
	explorerService, err := explorer.NewService(explorerStore)
	if err != nil {
		return fmt.Errorf("create Explorer service: %w", err)
	}
	artifactStore, err := artifactfs.New(serverConfig.Server.ArtifactDirectory)
	if err != nil {
		return fmt.Errorf("create Explorer artifact store: %w", err)
	}
	readRecipeExecution := dataframeapi.NewRecipeExecutionAuthorizer(publishedRegistry, scopeResolver)
	queryConfig := queryapi.Config{
		DiscoverReferences:     discoverReferences,
		DiscoverFields:         discoverFields,
		Dataframes:             dataframes,
		ScopeResolver:          scopeResolver,
		ActiveManifestResolver: activeManifestResolver,
		Explain: func(ctx context.Context, compiled dataframeexecution.CompiledQuery) error {
			_, err := executionarango.ExplainCompiledQuery(ctx, lifecycleClient, compiled)
			return err
		},
	}
	dataframeReads := dataframeReadRoutes{
		query: queryapi.NewService(queryConfig),
		published: dataframeapi.NewService(dataframeapi.Config{
			Reader: materializationReader, ScopeResolver: scopeResolver, Logger: logger,
		}),
	}
	ingestRunner := loadapi.IngestRunner{BaseOptions: ingest.LoadOptions{
		ConnectionOptions: connOpts,
	}}
	generationService, err := loadapi.NewService(loadapi.ServiceConfig{
		LoadGeneration:      ingestRunner.RunGeneration,
		GenerationActivator: lifecycleStore,
		DataframeReleases:   publishedRegistry,
		Logger:              logger,
		OnSuccess: func(project string) {
			discoveryCache.InvalidateProject(project)
			if scopeResolver != nil {
				scopeResolver.InvalidateProject(project)
			}
		},
	})
	if err != nil {
		return fmt.Errorf("create generation load service: %w", err)
	}
	sourceLoadLimits := loadapi.SourceLoadLimits{MaxFileBytes: serverConfig.Server.SourceLoadMaxFileBytes}
	var sourceLoader loadapi.SourceLoader
	if bundleTarget != nil && configuredRecipeName != "" {
		sourceLoader, err = ingest.NewRepositorySourceLoader(ingest.SourceLoadPublisherConfig{
			Client: lifecycleClient, DatasetStore: lifecycleStore,
			ValidateProject: func(ctx context.Context, project string) error {
				if scopeResolver == nil {
					return nil
				}
				principal, _ := authscope.PrincipalFromContext(ctx)
				if err := scopeResolver.AuthorizeReadProject(ctx, principal, project); err != nil {
					return reciperuntime.AuthorizationError(err)
				}
				return nil
			},
			MaterializeAndSelect: func(ctx context.Context, project, generation, operationID, commit string, expectedRevision int64) (string, int64, error) {
				bindings, err := reciperuntime.NewAuthorization(scopeResolver).AuthorizeRead(ctx, recipe.RuntimeBindings{Project: project, DatasetGeneration: generation})
				if err != nil {
					return "", 0, err
				}
				var identity publication.BundleIdentity
				resolved, err := recipeEngine.Materialize(ctx, configuredRecipeName, bindings, func(publishCtx context.Context, full dataframeexecution.Resolved) error {
					var publishErr error
					identity, publishErr = reciperuntime.PublishResolvedRecipe(
						publishCtx, recipeEngine, bundleTarget, configuredRecipeName, bindings, full,
						operationID, nil, serverConfig.Server.RecipeBatchRows, serverConfig.Server.RecipeBatchBytes,
						serverConfig.Server.RecipeQualityMaxRows, serverConfig.Server.RecipeQualityMaxDistinctKeys,
					)
					return publishErr
				})
				if err != nil {
					return "", 0, err
				}
				execution, err := publishedRegistry.FindExecutionByKey(ctx, identity.Key())
				if err != nil {
					return "", 0, err
				}
				if execution.State.Canonical() != publication.BundlePublished || execution.ReceiptID != operationID || execution.Project != project || execution.DatasetGeneration != generation {
					return "", 0, fmt.Errorf("configured recipe did not publish the candidate generation")
				}
				catalogSnapshot, err := explorer.PrepareViewBuilderCatalogSnapshot(resolved, execution, commit)
				if err != nil {
					return "", 0, fmt.Errorf("prepare view builder catalog snapshot: %w", err)
				}
				settings, err := explorerStore.GetViewBuilderSettings(ctx, project)
				if err != nil {
					return "", 0, fmt.Errorf("load project view builder settings: %w", err)
				}
				catalogRevisionID, catalogAnchored := settings.Catalogs[configuredRecipeName]
				if catalogAnchored && (strings.TrimSpace(catalogRevisionID) == "" || catalogRevisionID != strings.TrimSpace(catalogRevisionID)) {
					return "", 0, fmt.Errorf("project view builder catalog anchor for recipe %q is invalid", configuredRecipeName)
				}
				owners, err := explorerStore.List(ctx, projectid.Legacy(project))
				if err != nil {
					return "", 0, fmt.Errorf("load project-owned Explorer views: %w", err)
				}
				definitions, expectedConfigRevisionIDs, err := explorer.ProjectConfigsForSourcePublication(
					owners, configuredRecipeName, catalogAnchored, catalogSnapshot,
				)
				if err != nil {
					return "", 0, fmt.Errorf("resolve project views for source publication: %w", err)
				}
				selectors := make([]publicationcontract.DataframeSelector, 0, len(execution.Outputs))
				for _, output := range execution.Outputs {
					selectors = append(selectors, execution.Selector(output.Name))
				}
				configs, err := explorer.PrepareProjectConfigsForPublication(definitions, resolved, execution, commit)
				if err != nil {
					return "", 0, fmt.Errorf("prepare project configs: %w", err)
				}
				candidateReleaseService := *releaseService
				candidateReleaseService.Required = append(append([]publicationcontract.DataframeSelector(nil), releaseService.Required...), selectors...)
				release, err := candidateReleaseService.Create(ctx, publicationcontract.ActivationRequest{
					Project: project, Generation: generation, GitCommit: commit,
					ExpectedRevision: expectedRevision,
				})
				if err != nil {
					return "", 0, err
				}
				active, err := explorerStore.PublishProjectConfigs(ctx, explorer.ProjectConfigPublishRequest{
					RecipeName: configuredRecipeName, Configs: configs, Release: release,
					ExpectedReleaseRevision: expectedRevision, ExpectedConfigRevisionIDs: expectedConfigRevisionIDs,
					CatalogSnapshot: &catalogSnapshot,
				})
				if err != nil {
					return "", 0, fmt.Errorf("activate project publication: %w", err)
				}
				discoveryCache.InvalidateProject(project)
				if scopeResolver != nil {
					scopeResolver.InvalidateProject(project)
				}
				return execution.ID, active.Revision, nil
			},
		})
		if err != nil {
			return fmt.Errorf("create per-file source loader: %w", err)
		}
	}
	importedSources := importedsource.New(recipeRegistry, recipeEngine, capabilityResolver, configuredRecipeName, configuredRecipeVersion)
	var combineInputSchemas receiptapi.CombineInputSchemaResolver
	if materializationReader != nil {
		combineInputSchemas = receiptapi.NewCombineInputSchemaResolver(materializationReader, materializationReader.WithExecutionReadPins, scopeResolver)
	}
	receiptRuntime := receiptapi.NewRuntime(receiptapi.RuntimeConfig{
		Engine: recipeEngine, Explorers: explorerService, CapabilityResolver: capabilityResolver,
		Logger: logger, CombineInputSchemas: combineInputSchemas, ImportedSources: importedSources,
		ConstructionSourceStage: importedSources.SourceStage,
		Materializer: receiptapi.NewMaterializer(
			recipeEngine, bundleTarget, publishedRegistry, degradation, logger,
			serverConfig.Server.RecipeBatchRows, serverConfig.Server.RecipeBatchBytes,
			serverConfig.Server.RecipeQualityMaxRows, serverConfig.Server.RecipeQualityMaxDistinctKeys,
		),
	})
	compileReceipt := receiptRuntime.CompileReceipt
	persistPublishedWorkspace, err := explorer.LocalWorkspaceWriter(serverConfig.Server.LocalWorkspaceWriteback, serverConfig.Server.LocalWorkspaceProject)
	if err != nil {
		return fmt.Errorf("configure local workspace writeback: %w", err)
	}
	populationMappingCursorCodec, err := lifecycle.NewHMACPopulationMappingCursorCodec(serverConfig.Server.PopulationMappingCursorSecret)
	if err != nil {
		return fmt.Errorf("configure population mapping cursor signing: %w", err)
	}
	var artifactPublishedReader lifecycle.ArtifactPublishedReader
	if materializationReader != nil {
		artifactPublishedReader = materializationReader
		if serverConfig.Server.DevArtifactRowDelay > 0 {
			artifactPublishedReader = lifecycle.DelayArtifactRows(artifactPublishedReader, serverConfig.Server.DevArtifactRowDelay)
		}
	}
	schemaIndex, err := fhirschema.GeneratedIndex()
	if err != nil {
		return fmt.Errorf("load generated schema index for row choices: %w", err)
	}
	rowChoiceResolver, err := lifecycle.NewSchemaRowChoiceResolver(schemaIndex)
	if err != nil {
		return fmt.Errorf("configure row-choice schema resolver: %w", err)
	}
	explicitGroupResolver, err := lifecycle.NewRepositoryExplicitGroupRevisionResolver(explorerStore)
	if err != nil {
		return fmt.Errorf("configure explicit group revision resolver: %w", err)
	}
	var selectionSourceResolver lifecycle.SelectionSourceResolver
	if materializationReader != nil {
		selectionSourceResolver = published.SelectionSourceAdapter{Reader: materializationReader}
	}
	lifecycleConfig := lifecycle.Config{
		DefaultDataframeWorkspace:          importedSources.DefaultWorkspace,
		EditImportedSource:                 importedSources.Edit,
		CreateImportedSourceRule:           importedSources.CreateSourceRule,
		EditImportedSources:                importedSources.EditBatch,
		RemoveImportedField:                importedSources.RemoveField,
		RemoveImportedOutput:               importedSources.RemoveOutput,
		EditImportedRelationship:           importedSources.EditRelationship,
		AuthoringNetwork:                   importedSources.Network,
		SemanticInventory:                  catalogStore.PageSemanticInventory,
		SemanticCoverage:                   catalogStore.LookupSemanticCoverage,
		ResolveSemanticInventorySelections: catalogStore.ResolveSemanticInventorySelections,
		SelectionMembersCollection:         explorerarango.SelectionMembersCollection,
		InterpretationRepository:           explorerStore,
		PopulationMappingCursorCodec:       populationMappingCursorCodec,
		SelectionSourceResolver:            selectionSourceResolver,
		SelectionReferenceValidator:        explorerStore.ValidateSelectionReferences,
		RowChoiceResolver:                  rowChoiceResolver,
		RowChoicePlanner:                   rowChoiceResolver,
		ExplicitGroupResolver:              explicitGroupResolver,
		ExplicitGroupRepository:            explorerStore,
		ScanCategories:                     receiptRuntime.ScanCategories,
		ScanColumnPopulation:               receiptRuntime.ScanColumnPopulation,
		CompileReceipt:                     compileReceipt,
		ConstructionSourceStage:            receiptRuntime.ConstructionSourceStage,
		Capability: lifecycle.CapabilityResolver{
			Current: func(ctx context.Context, project, _ string, generation string) (capability.Snapshot, error) {
				return capabilityResolver.Resolve(ctx, project, generation)
			},
			Token:          capabilityResolver.ResolveToken,
			ForCompilation: capabilityResolver.ResolveForCompilation,
			ForExecution:   capabilityResolver.ResolveForExecution,
			Catalog:        authoringv2.CatalogFromCapability,
		},
		ReceiptLookup:             receiptRuntime.ReceiptLookup,
		ArtifactStore:             artifactStore,
		PublishedReader:           artifactPublishedReader,
		ArtifactTTL:               serverConfig.Server.ArtifactTTL,
		ArtifactMaxRows:           serverConfig.Server.ArtifactMaxRows,
		ArtifactMaxBytes:          serverConfig.Server.ArtifactMaxBytes,
		PreviewReceipt:            receiptRuntime.PreviewReceipt,
		ValidateReceiptStream:     receiptRuntime.ValidateReceiptStream,
		PopulationMapping:         receiptRuntime.PopulationMapping,
		CellTrace:                 receiptRuntime.CellTrace,
		RowLineage:                receiptRuntime.RowLineage,
		MaterializeReceipt:        receiptRuntime.MaterializeReceipt,
		ValidateReleaseGeneration: validateExplorerReleaseGeneration,
		ActivateRelease:           activateExplorerRelease,
		PrepareRelease:            prepareExplorerRelease,
		PersistPublishedWorkspace: persistPublishedWorkspace,
	}
	server, err := httpapi.NewHTTPServer(httpapi.HTTPConfig{Authenticator: authenticator, Authorizer: authorizer, Logger: logger,
		CoreReadyCheck: func(ctx context.Context) error {
			return lifecycleClient.QueryRows(ctx, "RETURN {ready: true}", 1, nil, func(map[string]any) error { return nil })
		},
		ClickHouseReadyCheck: func(ctx context.Context) error {
			if degradation != nil {
				return degradation
			}
			if clickhouse == nil {
				return nil
			}
			return clickhouse.Ping(ctx)
		}, ClickHouseEnabled: serverConfig.Server.ClickHouse.Enabled})
	if err != nil {
		return fmt.Errorf("create HTTP server: %w", err)
	}
	explorerHandlers := newExplorerHTTPHandlers(authorizer, func(ctx context.Context, principal *authscope.Principal, project string) error {
		if scopeResolver == nil {
			return nil
		}
		return scopeResolver.AuthorizeReadProject(ctx, principal, project)
	}, explorerService, lifecycleConfig)
	explorerHandlers.authorizeExecution = func(ctx context.Context, id string) error {
		return readRecipeExecution(ctx, id)
	}
	var constructionInputsReader constructioninputs.ExactReader
	if materializationReader != nil {
		constructionInputsReader = materializationReader
	}
	explorerHandlers.constructionInputs = constructioninputs.New(constructionInputsReader, publishedRegistry, lifecycleConfig.Capability, scopeResolver, explorerService, explorerService)
	if err := registerRoutes(server, generationService, authorizer, explorerHandlers, publishedRegistry, scopeResolver, sourceLoader, sourceLoadLimits, dataframeReads); err != nil {
		return fmt.Errorf("register HTTP routes: %w", err)
	}
	if privateClickHouseArtifacts != nil {
		reconcileCtx, cancelReconciler := context.WithCancel(ctx)
		reconcilerDone := artifactfs.StartPrivateArtifactReconciler(reconcileCtx, privateClickHouseArtifacts, logger)
		defer func() {
			cancelReconciler()
			timer := time.NewTimer(cleanupTimeout)
			defer timer.Stop()
			select {
			case <-reconcilerDone:
			case <-timer.C:
				logger.Error("private ClickHouse artifact reconciler did not stop before shutdown deadline")
			}
		}()
	}
	if artifactStore != nil {
		artifactReconcileCtx, cancelArtifactReconciler := context.WithCancel(ctx)
		artifactReconcilerDone := artifactfs.StartArtifactFSReconciler(artifactReconcileCtx, artifactStore, logger)
		defer func() {
			cancelArtifactReconciler()
			timer := time.NewTimer(cleanupTimeout)
			defer timer.Stop()
			select {
			case <-artifactReconcilerDone:
			case <-timer.C:
				logger.Error("filesystem artifact reconciler did not stop before shutdown deadline")
			}
		}()
	}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("starting HTTP server", "listen", serverConfig.Server.Listen, "database", serverConfig.Server.Database, "no_auth", serverConfig.Server.AllowUnauthenticated || serverConfig.Auth.AllowUnauthenticated)
		errCh <- server.App().Listen(serverConfig.Server.Listen)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("server stopped: %w", err)
		}
	case <-ctx.Done():
		logger.Info("shutting down HTTP server", "reason", ctx.Err())
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		if err := server.App().ShutdownWithContext(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown failed: %w", err)
		}
	}
	return nil
}
