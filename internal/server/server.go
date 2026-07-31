package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/calypr/loom/graphqlapi"
	clickhousegraphql "github.com/calypr/loom/graphqlapi/clickhouse"
	materializationapi "github.com/calypr/loom/graphqlapi/materialization"
	queryapi "github.com/calypr/loom/graphqlapi/query"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/materialization"
	materializationarango "github.com/calypr/loom/internal/dataframe/materialization/arango"
	publication "github.com/calypr/loom/internal/dataframe/publication"
	publicationclickhouse "github.com/calypr/loom/internal/dataframe/publication/clickhouse"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/engine"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	recipearango "github.com/calypr/loom/internal/dataframe/recipe/exec/arango"
	"github.com/calypr/loom/internal/dataframe/runtime"
	"github.com/calypr/loom/internal/dataset"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	api "github.com/calypr/loom/internal/httpapi"
	"github.com/calypr/loom/internal/ingest"
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

	serverConfig, err := LoadConfig(*configPath)
	if err != nil {
		exitf("load server config: %v", err)
	}
	if *configPath != "" {
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
	if err := lifecycleClient.Bootstrap(context.Background(), datasetarango.BootstrapSpec()); err != nil {
		exitf("bootstrap dataset lifecycle store: %v", err)
	}
	var dataframeDegradation error
	recordDataframeDegradation := func(stage string, cause error) {
		if cause == nil {
			return
		}
		dataframeDegradation = errors.Join(dataframeDegradation, fmt.Errorf("%s: %w", stage, cause))
		logger.Error("dataframe startup degraded", "stage", stage, "error", cause)
	}
	if err := lifecycleClient.Bootstrap(context.Background(), recipearango.BootstrapSpec()); err != nil {
		recordDataframeDegradation("bootstrap recipe registry", err)
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
			recordDataframeDegradation("register default dataframe recipe", err)
		}
	}
	lifecycleStore, err := datasetarango.New(lifecycleClient)
	if err != nil {
		exitf("create dataset lifecycle store: %v", err)
	}
	// Keep this as an interface, not a typed *datasetarango.Store nil. Passing a
	// typed nil into queryapi.Config makes the interface non-nil and
	// incorrectly activates immutable-generation lookup for legacy META loads.
	var activeManifestResolver dataset.ActiveManifestResolver
	if *datasetGenerations {
		activeManifestResolver = lifecycleStore
	}

	discoveryCache := catalog.NewCache()
	discoverFields := discoveryCache.DiscoverFields(catalog.DiscoverPopulatedFields)
	discoverReferences := discoveryCache.DiscoverReferences(catalog.DiscoverPopulatedReferences)

	var scopeResolver *authscope.ScopeResolver
	var authorizer authscope.Authorizer
	var authenticator authscope.Authenticator
	switch {
	case *noAuth:
		authenticator = authscope.StaticAuthenticator{Principal: authscope.Principal{Subject: "anonymous"}}
		authorizer = authscope.AllowAllAuthorizer{}
	case strings.EqualFold(serverConfig.Auth.Mode, "basic"):
		authenticator = authscope.BasicAuthenticator{Username: serverConfig.Auth.Basic.Username, Password: serverConfig.Auth.Basic.Password}
		authorizer = authscope.AllowAllAuthorizer{}
	case strings.EqualFold(serverConfig.Auth.Mode, "calypr"):
		authenticator = authscope.CalyprAuthenticator{}
		client := &http.Client{Timeout: serverConfig.Auth.Calypr.RequestTimeout}
		scopeResolver = authscope.NewScopeResolver(authscope.ScopeResolverConfig{
			ConnectionOptions: connOpts,
			ResourceAccess:    authscope.NewFenceUserAccessClientWithTTL(client, serverConfig.Auth.Calypr.CacheTTL),
			CacheTTL:          serverConfig.Auth.Calypr.CacheTTL,
		})
		authorizer = authscope.ScopeAuthorizer{Resolver: scopeResolver}
	default:
		exitf("unsupported auth mode %q", serverConfig.Auth.Mode)
	}

	dataframes := runtime.NewService(runtime.ServiceConfig{
		ConnectionOptions:      connOpts,
		ScopeResolver:          scopeResolver,
		ActiveManifestResolver: activeManifestResolver,
	})
	// The lifecycle client already owns this Arango database. Reusing it avoids
	// a second connection that can fail independently during optional startup.
	registry, err := materializationarango.New(lifecycleClient)
	if err != nil {
		exitf("create dataframe registry: %v", err)
	}
	publicationReady := true
	if err := lifecycleClient.Bootstrap(context.Background(), materializationarango.BootstrapSpec()); err != nil {
		recordDataframeDegradation("bootstrap dataframe registry", err)
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
			recordDataframeDegradation("ClickHouse database", err)
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
			recordDataframeDegradation("dataframe publication reconciliation", err)
			publicationReady = false
		}
		if publicationReady {
			bundleTarget, err = publicationclickhouse.New(bundleStore)
			if err != nil {
				exitf("create dataframe publication target: %v", err)
			}
		}
	}
	resolver := graphqlapi.NewResolver(graphqlapi.ResolverConfig{
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
		RecipeExecutions:      graphqlapi.NewAuthorizedRecipeExecutionReader(registry, scopeResolver),
		RecipeMaterialize: func(ctx context.Context, name string, bindings recipe.RuntimeBindings) (graphqlapi.RecipeExecution, error) {
			if bundleTarget == nil {
				cause := dataframeDegradation
				if cause == nil {
					cause = dataframeerrors.ErrBackendUnavailable
				}
				return graphqlapi.RecipeExecution{}, dataframeerrors.Wrap(cause, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
			}
			bindings.IncludeAuthResourcePath = true
			var identity materialization.BundleIdentity
			_, err := recipeEngine.Materialize(ctx, name, bindings, func(ctx context.Context, full engine.Resolved) error {
				streams, err := recipeEngine.Streams(ctx, full)
				if err != nil {
					return err
				}
				identity = materialization.BundleIdentity{Name: name, Project: bindings.Project, DatasetGeneration: bindings.DatasetGeneration, RecipeDigest: full.StoredRecipeDigest, SchemaDigest: full.ResolvedSchemaDigest, ScopeDigest: full.Semantic.ScopeDigest, EngineVersion: "loom-recipe-v1", AuthResourcePaths: append([]string(nil), bindings.AuthResourcePaths...)}
				streamInputs := make([]publication.OutputStream, 0, len(streams))
				for _, stream := range streams {
					stream := stream
					columns := recipeOutputLogicalColumns(full, stream.Name)
					rootResourceType := recipeOutputRootResourceType(full, stream.Name)
					streamInputs = append(streamInputs, publication.OutputStream{
						Name: stream.Name, Columns: columns,
						Stream: func(streamCtx context.Context, visit func(map[string]any) error) error {
							_, err := stream.Stream(streamCtx, func(row map[string]any) error {
								qualified, err := materialization.QualifyFlatRow(rootResourceType, row)
								if err != nil {
									return err
								}
								return visit(qualified)
							})
							return err
						},
					})
				}
				publicationIdentity := publication.PublicationIdentity{Name: identity.Name, Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, RecipeDigest: identity.RecipeDigest, SchemaDigest: identity.SchemaDigest, ScopeDigest: identity.ScopeDigest, EngineVersion: identity.EngineVersion, AuthResourcePaths: append([]string(nil), bindings.AuthResourcePaths...)}
				_, err = publication.Publish(ctx, bundleTarget, publicationIdentity, streamInputs, publication.Limits{BatchRows: *recipeBatchRows, BatchBytes: *recipeBatchBytes})
				return err
			})
			if err != nil {
				logger.Error("recipe materialization failed", "name", name, "project", bindings.Project, "error", err.Error())
				return graphqlapi.RecipeExecution{}, err
			}
			published, err := registry.FindExecutionByKey(ctx, identity.Key())
			if err != nil {
				logger.Error("load published recipe execution failed", "name", name, "project", bindings.Project, "error", err.Error())
				return graphqlapi.RecipeExecution{}, fmt.Errorf("load published recipe execution: %w", err)
			}
			return graphqlapi.RecipeExecution{ID: published.ID, Name: name, RecipeDigest: identity.RecipeDigest, ResolvedSchemaDigest: identity.SchemaDigest, SourceGeneration: identity.DatasetGeneration, State: string(materialization.BundleReady)}, nil
		},
	})
	clickhouseService := materializationapi.NewService(materializationapi.Config{
		Reader:                 materializationReader,
		ScopeResolver:          scopeResolver,
		ActiveManifestResolver: activeManifestResolver,
	})

	importService, err := api.NewService(api.ServiceConfig{
		Runner: api.IngestRunner{BaseOptions: ingest.LoadOptions{
			ConnectionOptions: connOpts,
			Schema:            *schema,
		}},
		BundleRunner: api.IngestRunner{BaseOptions: ingest.LoadOptions{
			ConnectionOptions: connOpts,
			Schema:            *schema,
		}},
		GenerationRunner: api.IngestRunner{BaseOptions: ingest.LoadOptions{
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

	server, err := api.NewHTTPServer(api.HTTPConfig{
		Service:                      importService,
		Authenticator:                authenticator,
		Authorizer:                   authorizer,
		ScopeResolver:                scopeResolver,
		GraphQLHandler:               graphqlapi.NewHandler(resolver),
		ClickHouseGraphQLHandler:     clickhousegraphql.NewHandler(clickhouseService),
		DataframeExporter:            clickhouseService,
		GraphQLPlaygroundHandler:     graphqlapi.NewPlaygroundHandler("/graphql/graph"),
		ApolloSandboxHandler:         graphqlapi.NewApolloSandboxHandler("/graphql/graph"),
		DisableSingleResourceImports: *datasetGenerations,
		RawExporter:                  api.ArangoRawExporter{Query: lifecycleClient, Manifests: lifecycleStore},
		Logger:                       logger,
		CoreReadyCheck: func(ctx context.Context) error {
			return lifecycleClient.QueryRows(ctx, "RETURN 1", 1, nil, func(map[string]any) error { return nil })
		},
		ClickHouseReadyCheck: func(ctx context.Context) error {
			if dataframeDegradation != nil {
				return dataframeDegradation
			}
			if clickhouse == nil {
				return nil
			}
			_, err := clickhouse.QueryRowsArgs(ctx, "SELECT 1", []string{"ok"})
			return err
		},
		ClickHouseEnabled: serverConfig.Server.ClickHouse.Enabled,
	})
	if err != nil {
		exitf("create HTTP server: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("starting HTTP server", "listen", *listen, "database", *database, "no_auth", *noAuth, "dataset_generations", *datasetGenerations)
		errCh <- server.App().Listen(*listen)
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
		if err := server.App().ShutdownWithContext(context.Background()); err != nil {
			exitf("shutdown failed: %v", err)
		}
	}
}

func recipeScopeDigest(bindings recipe.RuntimeBindings) string {
	paths := append([]string(nil), bindings.AuthResourcePaths...)
	sort.Strings(paths)
	hash := sha256.Sum256([]byte(bindings.Project + "\x00" + bindings.DatasetGeneration + "\x00" + strings.Join(paths, "\x00")))
	return hex.EncodeToString(hash[:])
}

// recipeOutputLogicalColumns is the one conversion point from the finalized
// compiler schema to the backend-neutral publication schema. Publication must
// not reconstruct nested names from semantic recipe nodes because those names
// are finalized by physical lowering.
func recipeOutputLogicalColumns(plan engine.Resolved, outputName string) []publication.LogicalColumn {
	for _, output := range plan.Compiled.Outputs {
		if output.Name != outputName {
			continue
		}
		columns := make([]publication.LogicalColumn, 0, len(output.OutputSchema)+1)
		identityAdded := false
		for _, column := range output.OutputSchema {
			if column.Identity && column.Name == "__loom_row_id" {
				columns = append(columns, publication.LogicalColumn{Name: column.Name, Kind: "string", IsIdentity: true})
				identityAdded = true
				break
			}
		}
		if !identityAdded {
			columns = append(columns, publication.LogicalColumn{Name: "__loom_row_id", Kind: "string", IsIdentity: true})
		}
		for _, column := range output.OutputSchema {
			if column.Internal {
				continue
			}
			kind := column.Kind
			if kind == "date_time" {
				kind = "date-time"
			}
			if kind == "" {
				kind = "string"
			}
			columns = append(columns, publication.LogicalColumn{Name: materialization.FlatColumnName(output.RootResourceType, column.Name), Kind: kind, Repeated: column.Cardinality == "many", Nullable: column.Nullable})
		}
		return columns
	}
	return []publication.LogicalColumn{{Name: "__loom_row_id", Kind: "string", IsIdentity: true}}
}

func recipeOutputRootResourceType(plan engine.Resolved, outputName string) string {
	for _, output := range plan.Compiled.Outputs {
		if output.Name == outputName {
			return output.RootResourceType
		}
	}
	return ""
}

func exitf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
