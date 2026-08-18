package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	queryapi "github.com/calypr/loom/internal/api/graphql/graph/query"
	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	publication "github.com/calypr/loom/internal/dataframe/publication"
	bundlearango "github.com/calypr/loom/internal/dataframe/publication/arango"
	publicationclickhouse "github.com/calypr/loom/internal/dataframe/publication/clickhouse"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/engine"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	recipearango "github.com/calypr/loom/internal/dataframe/recipe/exec/arango"
	dataframeruntime "github.com/calypr/loom/internal/dataframe/runtime"
	publicationcontract "github.com/calypr/loom/internal/dataset"
	publicationarango "github.com/calypr/loom/internal/dataset/arango"
	"github.com/calypr/loom/internal/explorer"
	explorerarango "github.com/calypr/loom/internal/explorer/arango"
	"github.com/calypr/loom/internal/ingest"
	arangostore "github.com/calypr/loom/internal/store/arango"
	clickhousestore "github.com/calypr/loom/internal/store/clickhouse"
)

// Run starts the Loom HTTP server using the process command-line flags.
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
	recipeRegistry, err := recipearango.New(lifecycleClient)
	if err != nil {
		return fmt.Errorf("create recipe registry: %w", err)
	}
	if serverConfig.Server.ClickHouse.Enabled {
		data, err := os.ReadFile(serverConfig.Server.Dataframer.Recipe)
		if err != nil {
			return fmt.Errorf("read dataframer recipe %q: %w", serverConfig.Server.Dataframer.Recipe, err)
		}
		defaultBundle, err := recipe.Parse(data)
		if err != nil {
			return fmt.Errorf("parse dataframer recipe %q: %w", serverConfig.Server.Dataframer.Recipe, err)
		}
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

	dataframes := dataframeruntime.NewService(dataframeruntime.ServiceConfig{QueryRows: func(ctx context.Context, query string, batch int, binds map[string]any, visit func(map[string]any) error) error {
		return lifecycleClient.QueryRows(ctx, query, batch, binds, visit)
	}})
	// The lifecycle client already owns this Arango database. Reusing it avoids
	// a second connection that can fail independently during optional startup.
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
	if serverConfig.Server.ClickHouse.Enabled {
		clickhouse, err = clickhousestore.New(clickhousestore.Options{URL: serverConfig.Server.ClickHouse.URL, Database: serverConfig.Server.ClickHouse.Database, Username: serverConfig.Server.ClickHouse.Username, Password: serverConfig.Server.ClickHouse.Password})
		if err != nil {
			return fmt.Errorf("create ClickHouse client: %w", err)
		}
		defer clickhouse.Close()
		// The Arango-backed dataframe loader publishes into this database. Create
		// it during server startup so a fresh ClickHouse instance does not require
		// an operator to run a separate DDL/API step before materialization.
		if err := clickhouse.EnsureDatabase(ctx); err != nil {
			degradation = recordDegradation(logger, degradation, "ClickHouse database", err)
			publicationReady = false
		}
		materializationReader = &published.Reader{ClickHouse: clickhouse, Catalog: publishedRegistry, Logger: logger, MaxPage: 1000, ActiveManifestResolver: activeManifestResolver}
	}
	recipeRevisions, err := recipearango.NewRevisionRegistry(lifecycleClient)
	if err != nil {
		return fmt.Errorf("create recipe revision registry: %w", err)
	}
	recipeEngine, err := engine.New(engine.Config{
		Registry:      recipeRegistry,
		Revisions:     recipeRevisions,
		ResolveBundle: recipeSchemaResolver(catalogStore.DiscoverFields, discoveryCache),
		QueryRows: func(ctx context.Context, query string, batchSize int, bindVars map[string]any, visit func(map[string]any) error) error {
			started := time.Now()
			digest := sha256.Sum256([]byte(query))
			queryID := hex.EncodeToString(digest[:8])
			logger.Info("dataframe AQL start", "query_id", queryID, "query_bytes", len(query), "bind_vars", len(bindVars), "cursor_batch_size", batchSize)
			err := lifecycleClient.QueryRows(ctx, query, batchSize, bindVars, visit)
			fields := []any{"query_id", queryID, "query_bytes", len(query), "bind_vars", len(bindVars), "seconds", time.Since(started).Seconds()}
			if err != nil {
				logger.Error("dataframe AQL failed", append(fields, "error", err.Error())...)
				return err
			}
			logger.Info("dataframe AQL complete", fields...)
			return nil
		},
		ScopeDigest: recipeScopeDigest,
	})
	if err != nil {
		return fmt.Errorf("create dataframe recipe engine: %w", err)
	}
	var bundleTarget publication.Target
	var publicationWorker *publicationclickhouse.Worker
	if serverConfig.Server.ClickHouse.Enabled && publicationReady {
		bundleStore, err := publicationclickhouse.NewBundleStore(clickhouse, publishedRegistry)
		if err != nil {
			return fmt.Errorf("create dataframe bundle store: %w", err)
		}
		if err := bundleStore.Reconcile(ctx, time.Now().UTC().Add(-2*time.Minute)); err != nil {
			degradation = recordDegradation(logger, degradation, "dataframe publication reconciliation", err)
			publicationReady = false
		}
		if publicationReady {
			bundleTarget, err = publicationclickhouse.New(bundleStore)
			if err != nil {
				return fmt.Errorf("create dataframe publication target: %w", err)
			}
			publicationWorker, err = publicationclickhouse.NewWorker(bundleStore, recipePublicationProcessor(recipeEngine, logger, serverConfig.Server.RecipeBatchRows, serverConfig.Server.RecipeBatchBytes), publicationclickhouse.WorkerConfig{Lease: serverConfig.Server.PublicationWorkerLease, MaxAttempts: serverConfig.Server.PublicationMaxAttempts})
			if err != nil {
				return fmt.Errorf("create dataframe publication worker: %w", err)
			}
		}
	}
	verificationStore := publicationVerificationStore{executions: publishedRegistry, query: lifecycleClient}
	releaseService := &publicationcontract.ReleaseService{Snapshots: lifecycleStore, Releases: lifecycleStore, Verifier: verificationStore, Required: serverConfig.Server.RequiredDataframeSelectors}
	activateExplorerRelease := func(ctx context.Context, project, generation string, selectors []publicationcontract.DataframeSelector) error {
		expectedRevision := int64(0)
		active, err := releaseService.Active(ctx, project)
		if err == nil {
			expectedRevision = active.Revision
		} else if !errors.Is(err, publicationcontract.ErrNoActiveRelease) {
			return err
		}
		_, err = releaseService.Activate(ctx, publicationcontract.ActivationRequest{
			Project: project, Generation: generation, GitCommit: generation,
			ExpectedRevision: expectedRevision, OptionalSelectors: selectors,
		})
		return err
	}
	noAuthEnabled := serverConfig.Server.AllowUnauthenticated || serverConfig.Auth.AllowUnauthenticated
	var candidateProjects func(context.Context) ([]string, error)
	if noAuthEnabled {
		inventory := publicationcontract.ProjectInventory{Snapshots: lifecycleStore, Releases: lifecycleStore, Executions: verificationStore}
		candidateProjects = func(ctx context.Context) ([]string, error) {
			return inventory.ExpectedProjects(ctx, nil, true)
		}
	}
	if materializationReader != nil {
		statusResolver := releaseProjectStatusResolver{releases: lifecycleStore, executions: publishedRegistry}
		materializationReader.ProjectStatusResolver = statusResolver
		materializationReader.ReleaseExecutionResolver = statusResolver
		materializationReader.FederationSnapshotResolver = statusResolver
	}
	var exactStarter graphresolver.ExactMaterializationStarter
	if publicationWorker != nil {
		exactStarter = func(ctx context.Context, selector publicationcontract.DataframeSelector, bindings recipe.RuntimeBindings) (graphresolver.RecipeExecution, error) {
			identity := publication.BundleIdentity{
				Name: selector.Recipe, TranslationVersion: selector.TranslationVersion,
				Project: bindings.Project, DatasetGeneration: bindings.DatasetGeneration,
				ScopeDigest: recipeScopeDigest(bindings), EngineVersion: "loom-recipe-v1",
				AuthScopeMode:     string(bindings.AuthScopeMode),
				AuthResourcePaths: append([]string(nil), bindings.AuthResourcePaths...),
			}
			execution, err := publicationWorker.Enqueue(ctx, identity)
			if err != nil {
				return graphresolver.RecipeExecution{}, err
			}
			return graphresolver.RecipeExecution{
				ID: execution.ID, Name: execution.Name, TranslationVersion: execution.TranslationVersion,
				SourceGeneration: execution.DatasetGeneration, State: string(execution.State.Canonical()),
				Outputs: []graphresolver.RecipeExecutionOutput{{Name: selector.Output, State: string(execution.State.Canonical()), Selector: selector}},
			}, nil
		}
	}
	releaseActivator := func(ctx context.Context, project, releaseID, expectedRevision string) (graphresolver.ProjectRelease, error) {
		revision := int64(0)
		if strings.TrimSpace(expectedRevision) != "" {
			parsed, err := strconv.ParseInt(expectedRevision, 10, 64)
			if err != nil || parsed < 0 {
				return graphresolver.ProjectRelease{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
			}
			revision = parsed
		}
		active, err := releaseService.ActivateExisting(ctx, project, releaseID, revision)
		if err != nil {
			return graphresolver.ProjectRelease{}, err
		}
		return graphresolver.ProjectRelease{ID: active.Release.ID, Project: active.Release.Project, Generation: active.Release.Generation, Revision: strconv.FormatInt(active.Revision, 10), State: "ACTIVE"}, nil
	}
	explorerStore, err := explorerarango.New(lifecycleClient)
	if err != nil {
		return fmt.Errorf("create Explorer store: %w", err)
	}
	explorerService, err := explorer.NewService(explorerStore)
	if err != nil {
		return fmt.Errorf("create Explorer service: %w", err)
	}
	explorerMaterializer := explorerBundleMaterializer(recipeEngine, bundleTarget, publishedRegistry, degradation, logger, serverConfig.Server.RecipeBatchRows, serverConfig.Server.RecipeBatchBytes)
	explorerCatalog := explorerCatalogReader(discoverFields, discoverReferences, scopeResolver, activeManifestResolver)
	resolver := graphresolver.NewResolver(graphresolver.ResolverConfig{
		DataframeQuery: queryapi.Config{
			DiscoverReferences:     discoverReferences,
			DiscoverFields:         discoverFields,
			Dataframes:             dataframes,
			ScopeResolver:          scopeResolver,
			ActiveManifestResolver: activeManifestResolver,
			Explain: func(ctx context.Context, compiled dataframeruntime.CompiledQuery) error {
				_, err := explainCompiledQuery(ctx, lifecycleClient, compiled)
				return err
			},
		},
		MaterializationReader: materializationReader,
		Logger:                logger,
		RecipeControl: engine.Control{Engine: recipeEngine, ExplainConnection: func(ctx context.Context, compiled dataframeruntime.CompiledQuery) (engine.ExplainAssessment, error) {
			return explainCompiledQuery(ctx, lifecycleClient, compiled)
		}},
		RecipeAuthorizer:            recipeAuthorization{resolver: scopeResolver},
		RecipeRevisions:             recipeRevisions,
		RecipeExecutions:            graphresolver.NewAuthorizedRecipeExecutionReader(publishedRegistry, scopeResolver),
		RecipeMaterialize:           recipeMaterializer(recipeEngine, bundleTarget, publishedRegistry, degradation, logger, serverConfig.Server.RecipeBatchRows, serverConfig.Server.RecipeBatchBytes),
		ExactMaterializationStarter: exactStarter,
		ProjectReleaseActivator:     releaseActivator,
		CandidateProjects:           candidateProjects,
	})
	ingestRunner := loadapi.IngestRunner{BaseOptions: ingest.LoadOptions{
		ConnectionOptions: connOpts,
		Schema:            serverConfig.Server.Schema,
	}}
	resourceService, err := loadapi.NewService(loadapi.ServiceConfig{
		Runner:              ingestRunner,
		GenerationRunner:    ingestRunner,
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
		return fmt.Errorf("create resource load service: %w", err)
	}
	snapshotService := &loadapi.SnapshotService{Repository: lifecycleStore, Blobs: loadapi.LocalSnapshotBlobs{Root: serverConfig.Server.SnapshotDirectory}, Runner: ingestRunner}
	deletedGenerations, retentionErr := (publicationcontract.RetentionService{
		Repository: lifecycleStore,
		Blobs:      loadapi.LocalSnapshotBlobs{Root: serverConfig.Server.SnapshotDirectory},
		Retention:  serverConfig.Server.SnapshotRetention,
	}).RunOnce(ctx)
	if retentionErr != nil {
		logger.Error("snapshot retention cleanup failed", "error", retentionErr)
	} else if len(deletedGenerations) != 0 {
		logger.Info("snapshot retention cleanup complete", "deleted_generations", len(deletedGenerations))
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
	if err := registerRoutes(server, resourceService, snapshotService, releaseService, authorizer, resolver, publishedRegistry, scopeResolver); err != nil {
		return fmt.Errorf("register HTTP routes: %w", err)
	}
	RegisterExplorerConfigV2Route(server.App(), authorizer, func(ctx context.Context, principal *authscope.Principal, project string) error {
		if scopeResolver == nil {
			return nil
		}
		scope, err := scopeResolver.ResolveReadScope(ctx, principal, project, nil)
		if err != nil || (!scope.Unrestricted() && len(scope.AuthResourcePaths) == 0) {
			return authscope.ErrForbidden
		}
		return nil
	}, explorerService, explorerMaterializer, ExplorerV2LifecycleConfig{
		Compile: explorerV2Compiler(recipeEngine, explorerCatalog),
		Catalog: explorerCatalog,
		Preview: func(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (map[string][]map[string]any, error) {
			return recipeEngine.PreviewBundle(ctx, bundle, bindings)
		},
		Materialize:     explorerMaterializer,
		ActivateRelease: activateExplorerRelease,
	})
	if publicationWorker != nil {
		go func() {
			err := publicationWorker.Run(ctx, time.Second, func(workerErr error) {
				logger.Error("dataframe publication worker iteration failed", "error", workerErr)
			})
			if err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("dataframe publication worker stopped", "error", err)
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
