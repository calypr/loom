package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	graphresolver "github.com/calypr/loom/generated/graphql/graph/resolver"
	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	materializationapi "github.com/calypr/loom/internal/api/graphql/graph/materialization"
	queryapi "github.com/calypr/loom/internal/api/graphql/graph/query"
	"github.com/calypr/loom/internal/dataframe/materialization"
	materializationarango "github.com/calypr/loom/internal/dataframe/materialization/arango"
	publication "github.com/calypr/loom/internal/dataframe/publication"
	publicationclickhouse "github.com/calypr/loom/internal/dataframe/publication/clickhouse"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/engine"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	recipearango "github.com/calypr/loom/internal/dataframe/recipe/exec/arango"
	"github.com/calypr/loom/internal/ingest"
	publicationcontract "github.com/calypr/loom/internal/publication"
	publicationarango "github.com/calypr/loom/internal/publication/arango"
	arangostore "github.com/calypr/loom/internal/store/arango"
	clickhousestore "github.com/calypr/loom/internal/store/clickhouse"
)

// Run starts the Loom HTTP server using the process command-line flags.
func Run() {
	var (
		configPath = flag.String("config", "", "YAML server configuration file")
		listen     = flag.String("listen", ":8080", "HTTP listen address")
		noAuth     = flag.Bool("no-auth", false, "disable scoped authorization for local development")
		backend    = flag.String("backend", "arango", "storage backend")
		url        = flag.String("url", "http://127.0.0.1:8529", "ArangoDB URL")
		database   = flag.String("database", "fhir_proto", "ArangoDB database")
		schema     = flag.String("schema", "schemas/graph-fhir.json", "FHIR graph schema path for imports")
		// Dataset generations opt the server into resolving a project's READY
		// active manifest before dataframe discovery or execution. This mode
		// disables the legacy one-file import route because that route cannot
		// safely construct a complete immutable snapshot.
		datasetGenerations = flag.Bool("dataset-generations", false, "resolve active immutable dataset generations for dataframe reads and disable legacy single-resource imports")
		clickhouseURL      = flag.String("clickhouse-url", "clickhouse://127.0.0.1:9000", "ClickHouse native URL for published dataframe reads")
		clickhouseDatabase = flag.String("clickhouse-database", "loom", "ClickHouse database for published dataframe reads")
		clickhouseUsername = flag.String("clickhouse-username", "default", "ClickHouse username for published dataframe reads")
		clickhousePassword = flag.String("clickhouse-password", "", "ClickHouse password for published dataframe reads")
		dataframerRecipe   = flag.String("dataframer-recipe", "", "dataframer recipe JSON file (required when ClickHouse is enabled)")
		recipeBatchRows    = flag.Int("recipe-batch-rows", 1000, "maximum recipe materialization rows per ClickHouse batch")
		recipeBatchBytes   = flag.Int("recipe-batch-bytes", 4<<20, "maximum recipe materialization bytes per ClickHouse batch")
	)
	flag.Parse()
	options := serverOptions{ConfigPath: *configPath, Listen: *listen, Backend: *backend, URL: *url, Database: *database, Schema: *schema, NoAuth: *noAuth, DatasetGenerations: *datasetGenerations, ClickHouseURL: *clickhouseURL, ClickHouseDatabase: *clickhouseDatabase, ClickHouseUsername: *clickhouseUsername, ClickHousePassword: *clickhousePassword, DataframerRecipe: *dataframerRecipe, RecipeBatchRows: *recipeBatchRows, RecipeBatchBytes: *recipeBatchBytes}

	serverConfig, err := LoadConfig(options.ConfigPath)
	if err != nil {
		exitf("load server config: %v", err)
	}
	if options.ConfigPath != "" {
		*listen = serverConfig.Server.Listen
		*backend = serverConfig.Server.Backend
		*url = serverConfig.Server.URL
		*database = serverConfig.Server.Database
		*schema = serverConfig.Server.Schema
		*datasetGenerations = serverConfig.Server.DatasetGenerations
		*clickhouseURL = serverConfig.Server.ClickHouse.URL
		*clickhouseDatabase = serverConfig.Server.ClickHouse.Database
		*clickhouseUsername = serverConfig.Server.ClickHouse.Username
		*clickhousePassword = serverConfig.Server.ClickHouse.Password
		*dataframerRecipe = serverConfig.Server.Dataframer.Recipe
		if !serverConfig.Server.ClickHouse.Enabled {
			*clickhouseURL = ""
			*clickhouseDatabase = ""
			*clickhouseUsername = ""
			*clickhousePassword = ""
		}
		*recipeBatchRows = serverConfig.Server.RecipeBatchRows
		*recipeBatchBytes = serverConfig.Server.RecipeBatchBytes
		*noAuth = serverConfig.Server.AllowUnauthenticated || serverConfig.Auth.AllowUnauthenticated
	} else {
		serverConfig.Server.Dataframer.Recipe = *dataframerRecipe
	}
	if *noAuth {
		serverConfig.Server.AllowUnauthenticated = true
		serverConfig.Auth.AllowUnauthenticated = true
	}
	if err := serverConfig.Validate(); err != nil {
		exitf("invalid server config: %v", err)
	}

	if *backend != "arango" {
		exitf("unsupported backend %q: only arango is wired in this server", *backend)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{}))
	connOpts := arangostore.ConnectionOptions{
		URL:      *url,
		Database: *database,
	}

	lifecycleClient, err := arangostore.Open(context.Background(), connOpts.URL, connOpts.Database)
	if err != nil {
		exitf("open dataset lifecycle store: %v", err)
	}
	defer lifecycleClient.Close(context.Background())
	if err := lifecycleClient.Bootstrap(context.Background(), publicationarango.BootstrapSpec()); err != nil {
		exitf("bootstrap dataset lifecycle store: %v", err)
	}
	dataframe := &dataframeComponents{logger: logger}
	if err := lifecycleClient.Bootstrap(context.Background(), recipearango.BootstrapSpec()); err != nil {
		dataframe.record("bootstrap recipe registry", err)
	}
	recipeRegistry, err := recipearango.New(lifecycleClient)
	if err != nil {
		exitf("create recipe registry: %v", err)
	}
	if serverConfig.Server.ClickHouse.Enabled {
		data, err := os.ReadFile(*dataframerRecipe)
		if err != nil {
			exitf("read dataframer recipe %q: %v", *dataframerRecipe, err)
		}
		defaultBundle, err := recipe.Parse(data)
		if err != nil {
			exitf("parse dataframer recipe %q: %v", *dataframerRecipe, err)
		}
		if _, err := (exec.PersistentRegistry{Store: recipeRegistry}).RegisterDefault(context.Background(), defaultBundle); err != nil {
			dataframe.record("register default dataframe recipe", err)
		}
	}
	lifecycleStore, err := publicationarango.New(lifecycleClient)
	if err != nil {
		exitf("create dataset lifecycle store: %v", err)
	}
	// Keep this as an interface, not a typed *publicationarango.Store nil. Passing a
	// typed nil into queryapi.Config makes the interface non-nil and
	// incorrectly activates immutable-generation lookup for legacy META loads.
	var activeManifestResolver publicationcontract.ActiveResolver
	if *datasetGenerations {
		activeManifestResolver = lifecycleStore
	}

	discovery := newDiscoveryComponents()
	discoveryCache := discovery.cache
	discoverFields, discoverReferences := discovery.discoverFields, discovery.discoverReferences

	auth, err := wireAuth(serverConfig, *noAuth, connOpts)
	if err != nil {
		exitf("configure authentication: %v", err)
	}
	authenticator, authorizer, scopeResolver := auth.authenticator, auth.authorizer, auth.scopeResolver

	dataframes := newDataframeService(connOpts, scopeResolver, activeManifestResolver)
	// The lifecycle client already owns this Arango database. Reusing it avoids
	// a second connection that can fail independently during optional startup.
	registry, err := materializationarango.New(lifecycleClient)
	if err != nil {
		exitf("create dataframe registry: %v", err)
	}
	publicationReady := true
	if err := lifecycleClient.Bootstrap(context.Background(), materializationarango.BootstrapSpec()); err != nil {
		dataframe.record("bootstrap dataframe registry", err)
		publicationReady = false
	}
	var clickhouse *clickhousestore.Client
	var materializationReader *materialization.Reader
	if serverConfig.Server.ClickHouse.Enabled {
		clickhouse, err = clickhousestore.New(clickhousestore.Options{URL: *clickhouseURL, Database: *clickhouseDatabase, Username: *clickhouseUsername, Password: *clickhousePassword})
		if err != nil {
			exitf("create ClickHouse client: %v", err)
		}
		defer clickhouse.Close()
		// The Arango-backed dataframe loader publishes into this database. Create
		// it during server startup so a fresh ClickHouse instance does not require
		// an operator to run a separate DDL/API step before materialization.
		if err := clickhouse.EnsureDatabase(context.Background()); err != nil {
			dataframe.record("ClickHouse database", err)
			publicationReady = false
		}
		materializationReader = &materialization.Reader{ClickHouse: clickhouse, Catalog: registry, MaxPage: 1000, ActiveManifestResolver: activeManifestResolver}
	}
	recipeEngine, err := engine.New(engine.Config{
		Registry:      recipeRegistry,
		ResolveBundle: recipeSchemaResolver(connOpts, discoveryCache),
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
		exitf("create dataframe recipe engine: %v", err)
	}
	var bundleTarget publication.Target
	if serverConfig.Server.ClickHouse.Enabled && publicationReady {
		bundleStore, err := materialization.NewClickHouseBundleStore(clickhouse, registry)
		if err != nil {
			exitf("create dataframe bundle store: %v", err)
		}
		if err := bundleStore.Reconcile(context.Background(), time.Now().UTC().Add(-2*time.Minute)); err != nil {
			dataframe.record("dataframe publication reconciliation", err)
			publicationReady = false
		}
		if publicationReady {
			bundleTarget, err = publicationclickhouse.New(bundleStore)
			if err != nil {
				exitf("create dataframe publication target: %v", err)
			}
		}
	}
	resolver := graphresolver.NewResolver(graphresolver.ResolverConfig{
		DataframeQuery: queryapi.Config{
			ConnectionOptions:      connOpts,
			DiscoverReferences:     discoverReferences,
			DiscoverFields:         discoverFields,
			Dataframes:             dataframes,
			ScopeResolver:          scopeResolver,
			ActiveManifestResolver: activeManifestResolver,
		},
		MaterializationReader: materializationReader,
		RecipeControl:         engine.Control{Engine: recipeEngine, ExplainConnection: &connOpts},
		RecipeAuthorizer:      recipeAuthorization{resolver: scopeResolver},
		RecipeExecutions:      graphresolver.NewAuthorizedRecipeExecutionReader(registry, scopeResolver),
		RecipeMaterialize:     recipeMaterializer(recipeEngine, bundleTarget, registry, dataframe, logger, *recipeBatchRows, *recipeBatchBytes),
	})
	clickhouseService := materializationapi.NewService(materializationapi.Config{
		Reader:                 materializationReader,
		ScopeResolver:          scopeResolver,
		ActiveManifestResolver: activeManifestResolver,
	})

	importService, err := loadapi.NewService(loadapi.ServiceConfig{
		Runner: loadapi.IngestRunner{BaseOptions: ingest.LoadOptions{
			ConnectionOptions: connOpts,
			Schema:            *schema,
		}},
		BundleRunner: loadapi.IngestRunner{BaseOptions: ingest.LoadOptions{
			ConnectionOptions: connOpts,
			Schema:            *schema,
		}},
		GenerationRunner: loadapi.IngestRunner{BaseOptions: ingest.LoadOptions{
			ConnectionOptions: connOpts,
			Schema:            *schema,
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
		exitf("create import service: %v", err)
	}

	server, err := buildHTTPServer(authenticator, authorizer, logger,
		func(ctx context.Context) error {
			return lifecycleClient.QueryRows(ctx, "RETURN 1", 1, nil, func(map[string]any) error { return nil })
		},
		func(ctx context.Context) error {
			if dataframe.degradation != nil {
				return dataframe.degradation
			}
			if clickhouse == nil {
				return nil
			}
			_, err := clickhouse.QueryRowsArgs(ctx, "SELECT 1", []string{"ok"})
			return err
		}, serverConfig.Server.ClickHouse.Enabled)
	if err != nil {
		exitf("create HTTP server: %v", err)
	}
	app := application{server: server}
	if err := registerRoutes(app.server, importService, authorizer, scopeResolver, *datasetGenerations, lifecycleClient, lifecycleStore, clickhouseService, resolver); err != nil {
		exitf("register HTTP routes: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("starting HTTP server", "listen", *listen, "database", *database, "no_auth", *noAuth, "dataset_generations", *datasetGenerations)
		errCh <- app.server.App().Listen(*listen)
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil {
			exitf("server stopped: %v", err)
		}
	case sig := <-stop:
		logger.Info("shutting down HTTP server", "signal", sig.String())
		if err := app.server.App().ShutdownWithContext(context.Background()); err != nil {
			exitf("shutdown failed: %v", err)
		}
	}
}

func exitf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
