package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	queryapi "github.com/calypr/loom/internal/api/graphql/graph/query"
	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	arangofactory "github.com/calypr/loom/internal/backends/arango"
	backendspostgres "github.com/calypr/loom/internal/backends/postgres"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	publication "github.com/calypr/loom/internal/dataframe/publication"
	publicationclickhouse "github.com/calypr/loom/internal/dataframe/publication/clickhouse"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	publicationcontract "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/artifactfs"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/tableshapecap"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/ingest"
	clickhousestore "github.com/calypr/loom/internal/store/clickhouse"
)

var (
	openArangoBackend   = arangofactory.Open
	openPostgresBackend = backendspostgres.Open
	newClickHouseClient = clickhousestore.New
)

func serveRuntime(ctx context.Context, server *httpapi.HTTPServer, listen string, closeRuntime func() error, closeLabel string, logger *slog.Logger, startupAttrs ...any) error {
	defer func() {
		if closeRuntime == nil {
			return
		}
		if closeErr := closeRuntime(); closeErr != nil {
			logger.Error(closeLabel, "error", closeErr)
		}
	}()
	errCh := make(chan error, 1)
	go func() {
		logger.Info("starting HTTP server", startupAttrs...)
		errCh <- server.App().Listen(listen)
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

type publicationPlaneInput struct {
	InitialDegradation      error
	InitialPublicationReady bool
	ClickHouse              clickhousestore.Options
	AcquireArtifactCatalog  func(context.Context, *clickhousestore.Client) (chartifact.Catalog, error)
	BundleCatalog           publication.BundleCatalog
	ActiveManifestResolver  publicationcontract.ActiveResolver
	ActiveReleaseResolver   publicationcontract.ActiveReleaseResolver
	Scopes                  *authscope.ScopeResolver
}

type publicationPlane struct {
	ClickHouse              *clickhousestore.Client
	PrivateArtifacts        *chartifact.Manager
	MaterializationReader   *published.Reader
	ClickHouseQueryRows     dataframeexecution.ClickHouseQueryRows
	ResolveClickHouseInputs dataframeexecution.ResolveClickHouseInputs
	WithExecutionReadPins   dataframeexecution.WithExecutionReadPins
	BundleTarget            publication.Target
	Degradation             error
	PublicationReady        bool
}

type publicationSweeper func(ctx context.Context, clickhouse *clickhousestore.Client, bundleStore *publicationclickhouse.ClickHouseBundleStore, cutoff time.Time) (stage string, cause error)

func newPublicationPlane(ctx context.Context, serverConfig Config, logger *slog.Logger, in publicationPlaneInput) (plane *publicationPlane, closeClickHouse func() error, err error) {
	plane = &publicationPlane{Degradation: in.InitialDegradation, PublicationReady: in.InitialPublicationReady}
	plane.ClickHouse, err = newClickHouseClient(in.ClickHouse)
	if err != nil {
		return plane, nil, fmt.Errorf("create ClickHouse client: %w", err)
	}
	closeClickHouse = plane.ClickHouse.Close
	if err := plane.ClickHouse.EnsureDatabase(ctx); err != nil {
		plane.Degradation = recordDegradation(logger, plane.Degradation, "ClickHouse database", err)
		plane.PublicationReady = false
	}
	artifactCatalog, err := in.AcquireArtifactCatalog(ctx, plane.ClickHouse)
	if err != nil {
		return plane, closeClickHouse, err
	}
	plane.PrivateArtifacts, err = chartifact.New(chartifact.Config{Catalog: artifactCatalog, ClickHouse: plane.ClickHouse,
		BatchRows:  serverConfig.Server.RecipeBatchRows,
		BatchBytes: serverConfig.Server.RecipeBatchBytes,
	})
	if err != nil {
		return plane, closeClickHouse, fmt.Errorf("create private ClickHouse artifact manager: %w", err)
	}
	plane.MaterializationReader = &published.Reader{ClickHouse: plane.ClickHouse, Catalog: in.BundleCatalog, Logger: logger, MaxPage: 1000, ActiveManifestResolver: in.ActiveManifestResolver, ActiveReleaseResolver: in.ActiveReleaseResolver}
	plane.ClickHouseQueryRows = plane.ClickHouse.QueryRowsArgsVisit
	resolver := clickHouseCombineInputResolver{reader: plane.MaterializationReader, scopes: in.Scopes}
	plane.ResolveClickHouseInputs = resolver.resolve
	plane.WithExecutionReadPins = plane.MaterializationReader.WithExecutionReadPins
	return plane, closeClickHouse, nil
}

func (p *publicationPlane) reconcileBundles(ctx context.Context, logger *slog.Logger, ready bool, degradation error, bundleCatalog publication.BundleCatalog, activeReleaseReader publicationclickhouse.ActiveReleaseReader, sweep publicationSweeper) error {
	if !ready {
		return nil
	}
	bundleStore, err := publicationclickhouse.NewBundleStore(p.ClickHouse, bundleCatalog)
	if err != nil {
		return fmt.Errorf("create dataframe bundle store: %w", err)
	}
	bundleStore.SetActiveReleaseReader(activeReleaseReader)
	if stage, cause := sweep(ctx, p.ClickHouse, bundleStore, time.Now().UTC().Add(-2*time.Minute)); cause != nil {
		degradation = recordDegradation(logger, degradation, stage, cause)
		p.PublicationReady = false
	}
	p.Degradation = degradation
	if p.PublicationReady {
		p.BundleTarget = bundleStore
	}
	return nil
}

type releaseHooks struct {
	Activate           func(context.Context, string, string, []publicationcontract.DataframeSelector) error
	Prepare            func(context.Context, string, string, []publicationcontract.DataframeSelector) (publicationcontract.ProjectRelease, int64, error)
	ValidateGeneration func(context.Context, string, string) error
}

func newReleaseHooks(serverConfig Config, manifests publicationcontract.ManifestReader, releases publicationcontract.ReleaseRepository, executions publication.ExactExecutionCatalog) releaseHooks {
	verificationStore := publicationVerificationStore{executions: executions}
	releaseService := &publicationcontract.ReleaseService{Manifests: manifests, Releases: releases, Verifier: verificationStore, Required: serverConfig.Server.RequiredDataframeSelectors}
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
	return releaseHooks{Activate: activateExplorerRelease, Prepare: prepareExplorerRelease, ValidateGeneration: validateExplorerReleaseGeneration}
}

type explicitGroupRevisionSource interface {
	explorer.ExplicitGroupRepository
	GetSelection(context.Context, string, string) (*explorer.SelectionRevision, error)
}

type lifecycleLanePorts struct {
	SemanticInventory                  func(context.Context, catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error)
	ResolveSemanticInventorySelections func(context.Context, catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error)
	SelectionMembersCollection         string
	InterpretationRepository           explorer.InterpretationRepository
	TableShapeCapabilities             tableshapecap.Repository
	ExplicitGroups                     explicitGroupRevisionSource
	SelectionReferenceValidator        lifecycle.SelectionReferenceValidator
	ReceiptExecutions                  receiptExecutionFinder
}

type lifecycleCompositionInput struct {
	ServerConfig          Config
	Logger                *slog.Logger
	RecipeEngine          *dataframeexecution.Engine
	CapabilityResolver    *explorerCapabilityResolver
	ExplorerService       *explorer.Service
	ArtifactStore         *artifactfs.Store
	MaterializationReader *published.Reader
	ScopeResolver         *authscope.ScopeResolver
	BundleTarget          publication.Target
	Degradation           error
	Hooks                 releaseHooks
	Ports                 lifecycleLanePorts
}

func buildLifecycleConfig(in lifecycleCompositionInput) (lifecycle.Config, error) {
	serverConfig := in.ServerConfig
	logger := in.Logger
	recipeEngine := in.RecipeEngine
	capabilityResolver := in.CapabilityResolver
	explorerService := in.ExplorerService
	materializationReader := in.MaterializationReader
	scopeResolver := in.ScopeResolver
	artifactStore := in.ArtifactStore
	var lifecycleConfig lifecycle.Config
	compileReceipt := func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		var resolveCombineSchemas combineInputSchemaResolver
		if materializationReader != nil {
			resolveCombineSchemas = func(ctx context.Context, output lower.CompiledRecipeOutput, bindings recipe.RuntimeBindings) (resolvedCombineInputSchema, error) {
				return exactPublishedCombineInputSchemas(ctx, output, bindings, materializationReader, materializationReader.WithExecutionReadPins, scopeResolver)
			}
		}
		return compileExplorerReceipt(ctx, request, capabilityResolver, recipeEngine, explorerService, logger, resolveCombineSchemas)
	}
	persistPublishedWorkspace, err := localWorkspaceWriter(serverConfig.Server.LocalWorkspaceWriteback, serverConfig.Server.LocalWorkspaceProject)
	if err != nil {
		return lifecycle.Config{}, fmt.Errorf("configure local workspace writeback: %w", err)
	}
	populationMappingCursorCodec, err := lifecycle.NewHMACPopulationMappingCursorCodec(serverConfig.Server.PopulationMappingCursorSecret)
	if err != nil {
		return lifecycle.Config{}, fmt.Errorf("configure population mapping cursor signing: %w", err)
	}
	var artifactPublishedReader lifecycle.ArtifactPublishedReader
	if materializationReader != nil {
		artifactPublishedReader = materializationReader
		if serverConfig.Server.DevArtifactRowDelay > 0 {
			artifactPublishedReader = artifactDelayReader{next: artifactPublishedReader, delay: serverConfig.Server.DevArtifactRowDelay}
		}
	}
	schemaIndex, err := fhirschema.GeneratedIndex()
	if err != nil {
		return lifecycle.Config{}, fmt.Errorf("load generated schema index for row choices: %w", err)
	}
	rowChoiceResolver, err := lifecycle.NewSchemaRowChoiceResolver(schemaIndex)
	if err != nil {
		return lifecycle.Config{}, fmt.Errorf("configure row-choice schema resolver: %w", err)
	}
	explicitGroupResolver, err := lifecycle.NewRepositoryExplicitGroupRevisionResolver(in.Ports.ExplicitGroups)
	if err != nil {
		return lifecycle.Config{}, fmt.Errorf("configure explicit group revision resolver: %w", err)
	}
	lifecycleConfig = lifecycle.Config{
		SemanticInventory:                  in.Ports.SemanticInventory,
		ResolveSemanticInventorySelections: in.Ports.ResolveSemanticInventorySelections,
		SelectionMembersCollection:         in.Ports.SelectionMembersCollection,
		InterpretationRepository:           in.Ports.InterpretationRepository,
		PopulationMappingCursorCodec:       populationMappingCursorCodec,
		SelectionSourceResolver:            published.SelectionSourceAdapter{Reader: materializationReader},
		SelectionReferenceValidator:        in.Ports.SelectionReferenceValidator,
		RowChoiceResolver:                  rowChoiceResolver,
		RowChoicePlanner:                   rowChoiceResolver,
		ExplicitGroupResolver:              explicitGroupResolver,
		ExplicitGroupRepository:            in.Ports.ExplicitGroups,
		TableShapeCapabilities:             in.Ports.TableShapeCapabilities,
		ScanCategories:                     configuredCategoryScanner(logger, recipeEngine),
		CompileReceipt:                     compileReceipt,
		ConstructionSourceStage: func(ctx context.Context, request lifecycle.ConstructionSourceStageRequest) (explorer.ReceiptConstructionStage, error) {
			return compileConstructionSourceStage(ctx, request, recipeEngine)
		},
		Capability: lifecycle.CapabilityResolver{
			Current: func(ctx context.Context, project, _ string, generation string) (capability.Snapshot, error) {
				return capabilityResolver.Resolve(ctx, project, generation)
			},
			Token:          capabilityResolver.ResolveToken,
			ForCompilation: capabilityResolver.ResolveForCompilation,
			ForExecution:   capabilityResolver.ResolveForExecution,
			Catalog:        authoringV2Catalog,
		},
		ReceiptLookup: func(ctx context.Context, project, explorerID, receiptID string) (*explorer.CompilationReceipt, error) {
			return explorerService.CompilationReceiptForExplorer(ctx, project, explorerID, receiptID)
		},
		ArtifactStore:    artifactStore,
		PublishedReader:  artifactPublishedReader,
		ArtifactTTL:      serverConfig.Server.ArtifactTTL,
		ArtifactMaxRows:  serverConfig.Server.ArtifactMaxRows,
		ArtifactMaxBytes: serverConfig.Server.ArtifactMaxBytes,
		PreviewReceipt: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			if receipt == nil {
				return dataframeexecution.PreviewSummary{}, fmt.Errorf("compilation receipt is required")
			}
			resolved, err := compileValidatedReceiptResolution(ctx, recipeEngine, receipt, bindings)
			if err != nil {
				logger.Error("Explorer receipt preview resolution failed", "receipt_id", receipt.ID, "error", err)
				return dataframeexecution.PreviewSummary{}, classifyReceiptPreviewResolutionError(receipt.ID, err)
			}
			output := ""
			if len(bindings.OutputNames) > 0 {
				output = bindings.OutputNames[0]
			}
			summary, previewErr := recipeEngine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{Output: output, Limit: bindings.PreviewLimit, IncludeRowIdentity: bindings.IncludeRowIdentity}, visit)
			if previewErr != nil {
				logReceiptPreviewContractFailure(logger, requestIDFromContext(ctx), receipt.ID, output, previewErr)
			}
			return summary, previewErr
		},
		ValidateReceiptStream: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings) error {
			err := validateReceiptFullStream(ctx, recipeEngine, receipt, bindings)
			if err != nil {
				logger.Error("Explorer row-value policy full-stream validation failed", "receipt_id", receipt.ID, "error", err)
			}
			return err
		},
		PopulationMapping: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, output string, memberIDs []string, after string, limit int) (dataframeexecution.PopulationMappingResult, error) {
			if receipt == nil {
				return dataframeexecution.PopulationMappingResult{}, fmt.Errorf("compilation receipt is required")
			}
			resolved, err := compileValidatedReceiptResolution(ctx, recipeEngine, receipt, bindings)
			if err != nil {
				logger.Error("Explorer receipt population mapping resolution failed", "receipt_id", receipt.ID, "error", err)
				return dataframeexecution.PopulationMappingResult{}, classifyReceiptPreviewResolutionError(receipt.ID, err)
			}
			reader := dataframeexecution.PopulationMemberReaderFunc(func(_ context.Context, visit func(string) error) error {
				for _, id := range memberIDs {
					if err := visit(id); err != nil {
						return err
					}
				}
				return nil
			})
			return recipeEngine.PopulationMapping(ctx, resolved, dataframeexecution.PopulationMappingRequest{Output: output, AfterMemberID: after, MaxUnmapped: limit}, reader)
		},
		CellTrace: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error) {
			if receipt == nil {
				return dataframeexecution.CellTraceResult{}, fmt.Errorf("compilation receipt is required")
			}
			resolved, err := compileValidatedReceiptResolution(ctx, recipeEngine, receipt, bindings)
			if err != nil {
				logger.Error("Explorer receipt cell trace resolution failed", "receipt_id", receipt.ID, "error", err)
				return dataframeexecution.CellTraceResult{}, classifyReceiptPreviewResolutionError(receipt.ID, err)
			}
			return recipeEngine.CellTrace(ctx, resolved, request)
		},
		RowLineage: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.RowLineageRequest) (dataframeexecution.RowLineageResult, error) {
			if receipt == nil {
				return dataframeexecution.RowLineageResult{}, fmt.Errorf("compilation receipt is required")
			}
			resolved, err := compileValidatedReceiptResolution(ctx, recipeEngine, receipt, bindings)
			if err != nil {
				logger.Error("Explorer receipt row lineage resolution failed", "receipt_id", receipt.ID, "error", err)
				return dataframeexecution.RowLineageResult{}, classifyReceiptPreviewResolutionError(receipt.ID, err)
			}
			return recipeEngine.RowLineage(ctx, resolved, request)
		},
		TableShapeExclusions: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.TableShapeExclusionRequest) (dataframeexecution.TableShapeExclusionResult, error) {
			if receipt == nil {
				return dataframeexecution.TableShapeExclusionResult{}, fmt.Errorf("compilation receipt is required")
			}
			resolved, err := compileValidatedReceiptResolution(ctx, recipeEngine, receipt, bindings)
			if err != nil {
				logger.Error("Explorer receipt table-shape exclusion resolution failed", "receipt_id", receipt.ID, "error", err)
				return dataframeexecution.TableShapeExclusionResult{}, classifyReceiptPreviewResolutionError(receipt.ID, err)
			}
			return recipeEngine.TableShapeExclusions(ctx, resolved, request)
		},
		MaterializeReceipt: explorerReceiptMaterializer(
			recipeEngine, in.BundleTarget, in.Ports.ReceiptExecutions, in.Degradation, logger,
			serverConfig.Server.RecipeBatchRows, serverConfig.Server.RecipeBatchBytes,
			serverConfig.Server.RecipeQualityMaxRows, serverConfig.Server.RecipeQualityMaxDistinctKeys,
		),
		ValidateReleaseGeneration: in.Hooks.ValidateGeneration,
		ActivateRelease:           in.Hooks.Activate,
		PrepareRelease:            in.Hooks.Prepare,
		PersistPublishedWorkspace: persistPublishedWorkspace,
	}
	return lifecycleConfig, nil
}

func wireExplorerRoutes(server *httpapi.HTTPServer, generationService *loadapi.Service, authorizer authscope.Authorizer, graphResolver *graphresolver.Resolver, scopeResolver *authscope.ScopeResolver, explorerService *explorer.Service, lifecycleConfig lifecycle.Config, inputs constructionInputsCatalog, deps routeDependencies) error {
	explorerHandlers := newExplorerHTTPHandlers(authorizer, func(ctx context.Context, principal *authscope.Principal, project string) error {
		if scopeResolver == nil {
			return nil
		}
		return scopeResolver.AuthorizeReadProject(ctx, principal, project)
	}, explorerService, lifecycleConfig)
	explorerHandlers.constructionInputs = inputs
	if err := registerRoutes(server, generationService, authorizer, graphResolver, explorerHandlers, deps); err != nil {
		return fmt.Errorf("register HTTP routes: %w", err)
	}
	return nil
}

func composeArangoRuntime(ctx context.Context, serverConfig Config, logger *slog.Logger) (server *httpapi.HTTPServer, closeRuntime func() error, err error) {
	var cleanups []func() error
	defer func() {
		if err == nil {
			return
		}
		_ = runRuntimeCloseSteps(cleanups)
	}()
	arangoBackend, err := openArangoBackend(ctx, arangofactory.Config{
		URL:      serverConfig.Server.URL,
		Database: serverConfig.Server.Database,
	})
	if err != nil {
		return nil, nil, err
	}
	cleanups = append(cleanups, func() error {
		closeCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		return arangoBackend.Close(closeCtx)
	})
	lifecycleClient := arangoBackend.Client
	var degradation error
	if err := lifecycleClient.Bootstrap(ctx, arangofactory.RecipeBootstrapSpec()); err != nil {
		degradation = recordDegradation(logger, degradation, "bootstrap recipe registry", err)
	}
	if err := lifecycleClient.Bootstrap(ctx, arangofactory.RevisionBootstrapSpec()); err != nil {
		degradation = recordDegradation(logger, degradation, "bootstrap recipe revision registry", err)
	}
	recipeRegistry := arangoBackend.RecipeRegistry
	if serverConfig.Server.ClickHouse.Enabled {
		data, err := os.ReadFile(serverConfig.Server.Dataframer.Recipe)
		if err != nil {
			return nil, nil, fmt.Errorf("read dataframer recipe %q: %w", serverConfig.Server.Dataframer.Recipe, err)
		}
		defaultBundle, err := recipe.Parse(data)
		if err != nil {
			return nil, nil, fmt.Errorf("parse dataframer recipe %q: %w", serverConfig.Server.Dataframer.Recipe, err)
		}
		if _, err := (exec.PersistentRegistry{Store: recipeRegistry}).RegisterVersion(ctx, defaultBundle); err != nil {
			degradation = recordDegradation(logger, degradation, "register default dataframe recipe", err)
		}
	}
	lifecycleStore := arangoBackend.Dataset
	activeManifestResolver := publicationcontract.ActiveResolver(lifecycleStore)

	discoveryCache := catalog.NewCache()
	catalogStore := arangoBackend.Catalog
	discoverFields := discoveryCache.DiscoverFields(catalogStore.DiscoverFields)
	discoverReferences := discoveryCache.DiscoverReferences(catalogStore.DiscoverReferences)

	auth, err := wireAuth(serverConfig, serverConfig.Server.AllowUnauthenticated || serverConfig.Auth.AllowUnauthenticated, catalogStore.DiscoverExistingAuthResourcePaths)
	if err != nil {
		return nil, nil, fmt.Errorf("configure authentication: %w", err)
	}
	authenticator, authorizer, scopeResolver := auth.authenticator, auth.authorizer, auth.scopeResolver

	aqlRows := selectedAQLAdapter{exec: lifecycleClient}
	dataframes := dataframeexecution.NewService(dataframeexecution.ServiceConfig{SourceRows: aqlRows.serviceRows})
	sourceCompileTarget := compiler.ArangoTarget()

	publishedRegistry := arangoBackend.PublishedRegistry
	publicationReady := true
	if err := lifecycleClient.Bootstrap(ctx, arangofactory.PublishedBootstrapSpec()); err != nil {
		degradation = recordDegradation(logger, degradation, "bootstrap dataframe registry", err)
		publicationReady = false
	}
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
			AcquireArtifactCatalog: func(ctx context.Context, clickhouse *clickhousestore.Client) (chartifact.Catalog, error) {
				if err := lifecycleClient.Bootstrap(ctx, arangofactory.ArtifactBootstrapSpec()); err != nil {
					return nil, fmt.Errorf("bootstrap private dataframe artifact catalog: %w", err)
				}
				privateArtifactCatalog, err := arangofactory.NewArtifactCatalog(lifecycleClient)
				if err != nil {
					return nil, fmt.Errorf("create private dataframe artifact catalog: %w", err)
				}
				return privateArtifactCatalog, nil
			},
			BundleCatalog:          publishedRegistry,
			ActiveManifestResolver: activeManifestResolver,
			ActiveReleaseResolver:  lifecycleStore,
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
	recipeRevisions := arangoBackend.RecipeRevisions
	var clickHouseQueryRows dataframeexecution.ClickHouseQueryRows
	var resolveClickHouseInputs dataframeexecution.ResolveClickHouseInputs
	var withExecutionReadPins dataframeexecution.WithExecutionReadPins
	if plane != nil {
		clickHouseQueryRows = plane.ClickHouseQueryRows
		resolveClickHouseInputs = plane.ResolveClickHouseInputs
		withExecutionReadPins = plane.WithExecutionReadPins
	}
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		CompileTarget: sourceCompileTarget,
		Registry:      recipeRegistry,
		Revisions:     recipeRevisions,
		ResolveBundle: recipeSchemaResolver(catalogStore.DiscoverFields, discoveryCache),
		PreparePreviewIndex: loggedPreviewIndexPreparation(logger, func(ctx context.Context, spec compiler.PreviewCoveringIndexSpec) error {
			var err error
			if len(spec.StoredValues) != 0 {
				if spec.Supersedes != nil {
					err = lifecycleClient.EnsurePreviewCoveringIndexWithStoredValuesReplacing(
						ctx, spec.Collection, spec.Name, spec.Fields, spec.StoredValues,
						spec.Supersedes.Name, spec.Supersedes.Fields, spec.Supersedes.StoredValues,
					)
				} else {
					err = lifecycleClient.EnsurePreviewCoveringIndexWithStoredValues(ctx, spec.Collection, spec.Name, spec.Fields, spec.StoredValues)
				}
			} else if spec.Supersedes != nil {
				err = lifecycleClient.EnsurePreviewCoveringIndexReplacing(ctx, spec.Collection, spec.Name, spec.Fields, spec.Supersedes.Name, spec.Supersedes.Fields)
			} else {
				err = lifecycleClient.EnsurePreviewCoveringIndex(ctx, spec.Collection, spec.Name, spec.Fields)
			}
			return err
		}),
		PreviewCoveringIndexes: func(ctx context.Context, collection string) ([]dataframeexecution.PreviewCollectionIndex, error) {
			indexes, err := lifecycleClient.ListPreviewCoveringIndexes(ctx, collection)
			if err != nil {
				return nil, err
			}
			return previewCollectionIndexesFromStore(indexes), nil
		},
		PreviewCollectionRevision: lifecycleClient.CollectionRevision,
		PreviewCollectionScans: func(ctx context.Context, command command.CompiledCommand, collection string) (dataframeexecution.PreviewCollectionScanEvidence, error) {
			query, bindVars, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dataframeexecution.PreviewCollectionScanEvidence{}, dispatchErr
			}
			result, err := lifecycleClient.Explain(ctx, arangofactory.ExplainRequest{Query: query, BindVars: bindVars})
			if err != nil {
				return dataframeexecution.PreviewCollectionScanEvidence{}, err
			}
			return PreviewCollectionScanEvidenceFromExplain(result, collection), nil
		},
		PreviewCollectionCount:     lifecycleClient.CollectionCount,
		ClickHouseQueryRows:        clickHouseQueryRows,
		PreviewSourceRows:          withConfiguredAQLRows(logger, "preview_query_rows", aqlRows.dispatchPreviewRows),
		ResolveClickHouseInputs:    resolveClickHouseInputs,
		WithExecutionReadPins:      withExecutionReadPins,
		PrivateClickHouseArtifacts: privateClickHouseArtifacts,
		SourceRows:                 withConfiguredAQLRows(logger, "query_rows", aqlRows.dispatchRows),
		ScopeDigest:                recipeScopeDigest,
		RootPageRows:               serverConfig.Server.RecipeQueryPageRows,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create dataframe recipe engine: %w", err)
	}
	var bundleTarget publication.Target
	if plane != nil {
		if err := plane.reconcileBundles(ctx, logger, publicationReady, degradation, publishedRegistry, lifecycleStore,
			func(ctx context.Context, clickhouse *clickhousestore.Client, bundleStore *publicationclickhouse.ClickHouseBundleStore, cutoff time.Time) (string, error) {
				if err := bundleStore.Reconcile(ctx, cutoff); err != nil {
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
	hooks := newReleaseHooks(serverConfig, lifecycleStore, lifecycleStore, publishedRegistry)
	explorerStore := arangoBackend.Explorer
	if err := explorerStore.MigrateLegacyRepositoryConfigs(ctx); err != nil {
		return nil, nil, fmt.Errorf("migrate legacy Explorer repository configs: %w", err)
	}
	capabilitySnapshots := arangoBackend.ExplorerCapabilitySnapshots
	capabilityResolver, err := newExplorerCapabilityResolver(catalogStore, scopeResolver, activeManifestResolver, capabilitySnapshots)
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
			DiscoverReferences:     discoverReferences,
			DiscoverFields:         discoverFields,
			Dataframes:             dataframes,
			ScopeResolver:          scopeResolver,
			ActiveManifestResolver: activeManifestResolver,
			Explain: func(ctx context.Context, compiled compiler.CompiledQuery) error {
				_, err := explainCompiledQuery(ctx, lifecycleClient, compiled)
				return err
			},
		},
		MaterializationReader: materializationReader,
		Logger:                logger,
		RecipeControl: dataframeexecution.Control{Engine: recipeEngine, ExplainConnection: func(ctx context.Context, compiled compiler.CompiledQuery) (dataframeexecution.ExplainAssessment, error) {
			return explainCompiledQuery(ctx, lifecycleClient, compiled)
		}, ExplainPostgreSQLConnection: func(ctx context.Context, compiled compiler.CompiledQuery) (dataframeexecution.PostgreSQLPlanAssessment, error) {
			return explainPostgreSQLCompiledQuery(ctx, nil, compiled)
		}},
		RecipeAuthorizer: recipeAuthorization{resolver: scopeResolver, selectionMembersCollection: arangofactory.SelectionMembersCollection},
		RecipeRevisions:  recipeRevisions,
		RecipeExecutions: graphresolver.NewAuthorizedRecipeExecutionReader(publishedRegistry, scopeResolver),
	})
	ingestRunner := loadapi.IngestRunner{BaseOptions: ingest.LoadOptions{
		ConnectionOptions: arangofactory.Config{
			URL:      serverConfig.Server.URL,
			Database: serverConfig.Server.Database,
		},
		Schema: serverConfig.Server.Schema,
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
		return nil, nil, fmt.Errorf("create generation load service: %w", err)
	}
	tableShapeCapabilities, err := arangofactory.NewTableShapeCapabilityRepository(lifecycleClient)
	if err != nil {
		return nil, nil, fmt.Errorf("configure table-shape capability repository: %w", err)
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
			SemanticInventory:                  catalogStore.PageSemanticInventory,
			ResolveSemanticInventorySelections: catalogStore.ResolveSemanticInventorySelections,
			SelectionMembersCollection:         arangofactory.SelectionMembersCollection,
			InterpretationRepository:           explorerStore,
			TableShapeCapabilities:             tableShapeCapabilities,
			ExplicitGroups:                     explorerStore,
			SelectionReferenceValidator:        explorerStore.ValidateSelectionReferences,
			ReceiptExecutions:                  publishedRegistry,
		},
	})
	if err != nil {
		return nil, nil, err
	}
	server, err = httpapi.NewHTTPServer(httpapi.HTTPConfig{Authenticator: authenticator, Authorizer: authorizer, Logger: logger,
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
		return nil, nil, fmt.Errorf("create HTTP server: %w", err)
	}
	if err := wireExplorerRoutes(server, generationService, authorizer, resolver, scopeResolver, explorerService, lifecycleConfig, constructionInputsCatalog{
		reader: materializationReader, catalog: publishedRegistry, capabilities: lifecycleConfig.Capability,
		scopes: scopeResolver, explorers: explorerService, revisions: explorerService,
	}, routeDependencies{releases: publishedRegistry, scopes: auth.scopePolicy}); err != nil {
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

func runArangoRuntime(ctx context.Context, serverConfig Config, logger *slog.Logger) error {
	server, closeRuntime, err := composeArangoRuntime(ctx, serverConfig, logger)
	if err != nil {
		return err
	}
	return serveRuntime(ctx, server, serverConfig.Server.Listen, closeRuntime, "close Arango server runtime", logger,
		"listen", serverConfig.Server.Listen, "database", serverConfig.Server.Database, "no_auth", serverConfig.Server.AllowUnauthenticated || serverConfig.Auth.AllowUnauthenticated)
}
