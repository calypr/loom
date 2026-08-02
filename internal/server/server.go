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
	"syscall"
	"time"

	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	materializationapi "github.com/calypr/loom/internal/api/graphql/graph/materialization"
	queryapi "github.com/calypr/loom/internal/api/graphql/graph/query"
	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/catalog"
	catalogarango "github.com/calypr/loom/internal/catalog/arango"
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
	if serverConfig.Server.Backend != "arango" {
		return fmt.Errorf("unsupported backend %q: only arango is wired in this server", serverConfig.Server.Backend)
	}

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
		if _, err := (exec.PersistentRegistry{Store: recipeRegistry}).RegisterDefault(ctx, defaultBundle); err != nil {
			degradation = recordDegradation(logger, degradation, "register default dataframe recipe", err)
		}
	}
	lifecycleStore, err := publicationarango.New(lifecycleClient)
	if err != nil {
		return fmt.Errorf("create dataset lifecycle store: %w", err)
	}
	// Keep this as an interface, not a typed *publicationarango.Store nil. Passing a
	// typed nil into queryapi.Config makes the interface non-nil and
	// incorrectly activates immutable-generation lookup for legacy META loads.
	var activeManifestResolver publicationcontract.ActiveResolver
	if serverConfig.Server.DatasetGenerations {
		activeManifestResolver = lifecycleStore
	}

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
	}, ScopeResolver: scopeResolver, ActiveManifestResolver: activeManifestResolver})
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
		materializationReader = &published.Reader{ClickHouse: clickhouse, Catalog: publishedRegistry, MaxPage: 1000, ActiveManifestResolver: activeManifestResolver}
	}
	recipeEngine, err := engine.New(engine.Config{
		Registry:      recipeRegistry,
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
		}
	}
	resolver := graphresolver.NewResolver(graphresolver.ResolverConfig{
		DataframeQuery: queryapi.Config{
			DiscoverReferences:     discoverReferences,
			DiscoverFields:         discoverFields,
			DiscoverDatasets:       catalogStore.DiscoverDatasets,
			Dataframes:             dataframes,
			ScopeResolver:          scopeResolver,
			ActiveManifestResolver: activeManifestResolver,
			Explain: func(ctx context.Context, compiled dataframeruntime.CompiledQuery) error {
				_, err := explainCompiledQuery(ctx, lifecycleClient, compiled)
				return err
			},
		},
		MaterializationReader: materializationReader,
		RecipeControl: engine.Control{Engine: recipeEngine, ExplainConnection: func(ctx context.Context, compiled dataframeruntime.CompiledQuery) (engine.ExplainAssessment, error) {
			return explainCompiledQuery(ctx, lifecycleClient, compiled)
		}},
		RecipeAuthorizer:  recipeAuthorization{resolver: scopeResolver},
		RecipeExecutions:  graphresolver.NewAuthorizedRecipeExecutionReader(publishedRegistry, scopeResolver),
		RecipeMaterialize: recipeMaterializer(recipeEngine, bundleTarget, publishedRegistry, degradation, logger, serverConfig.Server.RecipeBatchRows, serverConfig.Server.RecipeBatchBytes),
	})
	clickhouseService := materializationapi.NewService(materializationapi.Config{
		Reader:        materializationReader,
		ScopeResolver: scopeResolver,
	})

	importService, err := loadapi.NewService(loadapi.ServiceConfig{
		Runner: loadapi.IngestRunner{BaseOptions: ingest.LoadOptions{
			ConnectionOptions: connOpts,
			Schema:            serverConfig.Server.Schema,
		}},
		BundleRunner: loadapi.IngestRunner{BaseOptions: ingest.LoadOptions{
			ConnectionOptions: connOpts,
			Schema:            serverConfig.Server.Schema,
		}},
		GenerationRunner: loadapi.IngestRunner{BaseOptions: ingest.LoadOptions{
			ConnectionOptions: connOpts,
			Schema:            serverConfig.Server.Schema,
		}},
		Logger: logger,
		OnSuccess: func(project string) {
			discoveryCache.InvalidateProject(project)
			if scopeResolver != nil {
				scopeResolver.InvalidateProject(project)
			}
		},
	})
	if err != nil {
		return fmt.Errorf("create import service: %w", err)
	}

	server, err := httpapi.NewHTTPServer(httpapi.HTTPConfig{Authenticator: authenticator, Authorizer: authorizer, Logger: logger,
		CoreReadyCheck: func(ctx context.Context) error {
			return lifecycleClient.QueryRows(ctx, "RETURN 1", 1, nil, func(map[string]any) error { return nil })
		},
		ClickHouseReadyCheck: func(ctx context.Context) error {
			if degradation != nil {
				return degradation
			}
			if clickhouse == nil {
				return nil
			}
			_, err := clickhouse.QueryRowsArgs(ctx, "SELECT 1", []string{"ok"})
			return err
		}, ClickHouseEnabled: serverConfig.Server.ClickHouse.Enabled})
	if err != nil {
		return fmt.Errorf("create HTTP server: %w", err)
	}
	if err := registerRoutes(server, importService, authorizer, scopeResolver, serverConfig.Server.DatasetGenerations, lifecycleClient, lifecycleStore, clickhouseService, resolver); err != nil {
		return fmt.Errorf("register HTTP routes: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("starting HTTP server", "listen", serverConfig.Server.Listen, "database", serverConfig.Server.Database, "no_auth", serverConfig.Server.AllowUnauthenticated || serverConfig.Auth.AllowUnauthenticated, "dataset_generations", serverConfig.Server.DatasetGenerations)
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
