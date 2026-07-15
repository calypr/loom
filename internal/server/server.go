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
	queryapi "github.com/calypr/loom/graphqlapi/query"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe"
	"github.com/calypr/loom/internal/dataframe/materialization"
	materializationarango "github.com/calypr/loom/internal/dataframe/materialization/arango"
	publication "github.com/calypr/loom/internal/dataframe/publication"
	publicationclickhouse "github.com/calypr/loom/internal/dataframe/publication/clickhouse"
	publicationelasticsearch "github.com/calypr/loom/internal/dataframe/publication/elasticsearch"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/engine"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	recipearango "github.com/calypr/loom/internal/dataframe/recipe/exec/arango"
	"github.com/calypr/loom/internal/dataset"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	api "github.com/calypr/loom/internal/httpapi"
	"github.com/calypr/loom/internal/ingest"
	arangostore "github.com/calypr/loom/internal/store/arango"
	clickhousestore "github.com/calypr/loom/internal/store/clickhouse"
	elasticsearchstore "github.com/calypr/loom/internal/store/elasticsearch"
	"github.com/google/uuid"
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
		datasetGenerations  = flag.Bool("dataset-generations", false, "resolve active immutable dataset generations for dataframe reads and disable legacy single-resource imports")
		clickhouseURL       = flag.String("clickhouse-url", "clickhouse://127.0.0.1:9000", "ClickHouse native URL for published dataframe reads")
		clickhouseDatabase  = flag.String("clickhouse-database", "loom", "ClickHouse database for published dataframe reads")
		recipeBatchRows     = flag.Int("recipe-batch-rows", 1000, "maximum recipe materialization rows per ClickHouse batch")
		recipeBatchBytes    = flag.Int("recipe-batch-bytes", 4<<20, "maximum recipe materialization bytes per ClickHouse batch")
		publicationTarget   = flag.String("publication-target", "clickhouse", "recipe publication target: clickhouse or elasticsearch")
		elasticsearchURL    = flag.String("elasticsearch-url", os.Getenv("LOOM_ELASTICSEARCH_URL"), "Elasticsearch/OpenSearch URL for direct recipe publication")
		elasticsearchPrefix = flag.String("elasticsearch-index-prefix", "loom", "prefix for staged Elasticsearch/OpenSearch indices and aliases")
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
		*recipeBatchRows = serverConfig.Server.RecipeBatchRows
		*recipeBatchBytes = serverConfig.Server.RecipeBatchBytes
		*publicationTarget = serverConfig.Server.PublicationTarget
		*elasticsearchURL = serverConfig.Server.Elasticsearch.URL
		*elasticsearchPrefix = serverConfig.Server.Elasticsearch.IndexPrefix
		*noAuth = serverConfig.Server.AllowUnauthenticated || serverConfig.Auth.AllowUnauthenticated
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
	if err := lifecycleClient.Bootstrap(context.Background(), recipearango.BootstrapSpec()); err != nil {
		exitf("bootstrap recipe registry: %v", err)
	}
	recipeRegistry, err := recipearango.New(lifecycleClient)
	if err != nil {
		exitf("create recipe registry: %v", err)
	}
	defaultBundle, err := recipe.DefaultACEDBundle()
	if err != nil {
		exitf("load default dataframe recipe: %v", err)
	}
	if _, err := (exec.PersistentRegistry{Store: recipeRegistry}).Register(context.Background(), defaultBundle); err != nil {
		exitf("register default dataframe recipe: %v", err)
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

	dataframes := dataframe.NewService(dataframe.ServiceConfig{
		ConnectionOptions:      connOpts,
		ScopeResolver:          scopeResolver,
		ActiveManifestResolver: activeManifestResolver,
	})
	registryClient, err := arangostore.Open(context.Background(), connOpts.URL, connOpts.Database)
	if err != nil {
		exitf("open dataframe registry store: %v", err)
	}
	defer registryClient.Close(context.Background())
	if err := registryClient.Bootstrap(context.Background(), materializationarango.BootstrapSpec()); err != nil {
		exitf("bootstrap dataframe registry store: %v", err)
	}
	registry, err := materializationarango.New(registryClient)
	if err != nil {
		exitf("create dataframe registry: %v", err)
	}
	clickhouse, err := clickhousestore.New(clickhousestore.Options{URL: *clickhouseURL, Database: *clickhouseDatabase})
	if err != nil {
		exitf("create ClickHouse client: %v", err)
	}
	defer clickhouse.Close()
	materializationReader := &materialization.Reader{ClickHouse: clickhouse, Registry: registry, MaxPage: 1000}
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
	bundleStore, err := materialization.NewClickHouseBundleStore(clickhouse, registry)
	if err != nil {
		exitf("create dataframe bundle store: %v", err)
	}
	bundleTarget, err := publicationclickhouse.New(bundleStore)
	if err != nil {
		exitf("create dataframe publication target: %v", err)
	}
	var directTarget publication.Target = bundleTarget
	directElasticsearch := false
	switch strings.ToLower(strings.TrimSpace(*publicationTarget)) {
	case "clickhouse":
	case "elasticsearch":
		if strings.TrimSpace(*elasticsearchURL) == "" {
			exitf("elasticsearch publication target requires -elasticsearch-url or LOOM_ELASTICSEARCH_URL")
		}
		esClient, err := elasticsearchstore.New(elasticsearchstore.Options{
			URL: *elasticsearchURL, Username: os.Getenv("LOOM_ELASTICSEARCH_USERNAME"), Password: os.Getenv("LOOM_ELASTICSEARCH_PASSWORD"),
			RequestTimeout: 30 * time.Second, MaxRetries: 3,
		})
		if err != nil {
			exitf("create Elasticsearch client: %v", err)
		}
		esTarget, err := publicationelasticsearch.New(publicationelasticsearch.Options{Client: esClient, IndexPrefix: *elasticsearchPrefix, MaxRetries: 3})
		if err != nil {
			exitf("create Elasticsearch publication target: %v", err)
		}
		directTarget = esTarget
		directElasticsearch = true
	default:
		exitf("unsupported publication target %q", *publicationTarget)
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
					streamInputs = append(streamInputs, publication.OutputStream{
						Name: stream.Name, Columns: columns,
						Stream: func(streamCtx context.Context, visit func(map[string]any) error) error {
							_, err := stream.Stream(streamCtx, func(row map[string]any) error {
								return visit(row)
							})
							return err
						},
					})
				}
				publicationIdentity := publication.PublicationIdentity{Name: identity.Name, Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, RecipeDigest: identity.RecipeDigest, SchemaDigest: identity.SchemaDigest, ScopeDigest: identity.ScopeDigest, EngineVersion: identity.EngineVersion, AuthResourcePaths: append([]string(nil), bindings.AuthResourcePaths...)}
				if directElasticsearch {
					return publishDirectElasticsearch(ctx, registry, directTarget, publicationIdentity, streamInputs, publication.Limits{BatchRows: *recipeBatchRows, BatchBytes: *recipeBatchBytes})
				}
				_, err = publication.Publish(ctx, directTarget, publicationIdentity, streamInputs, publication.Limits{BatchRows: *recipeBatchRows, BatchBytes: *recipeBatchBytes})
				return err
			})
			if err != nil {
				return graphqlapi.RecipeExecution{}, err
			}
			published, err := registry.FindExecutionByKey(ctx, identity.Key())
			if err != nil {
				return graphqlapi.RecipeExecution{}, fmt.Errorf("load published recipe execution: %w", err)
			}
			return graphqlapi.RecipeExecution{ID: published.ID, Name: name, RecipeDigest: identity.RecipeDigest, ResolvedSchemaDigest: identity.SchemaDigest, SourceGeneration: identity.DatasetGeneration, State: string(materialization.BundleReady)}, nil
		},
	})

	importService, err := api.NewService(api.ServiceConfig{
		Runner: api.IngestRunner{BaseOptions: ingest.LoadOptions{
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
		GraphQLPlaygroundHandler:     graphqlapi.NewPlaygroundHandler("/graphql"),
		ApolloSandboxHandler:         graphqlapi.NewApolloSandboxHandler("/graphql"),
		DisableSingleResourceImports: *datasetGenerations,
		RawExporter:                  api.ArangoRawExporter{Query: lifecycleClient, Manifests: lifecycleStore},
		Logger:                       logger,
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

func publishDirectElasticsearch(ctx context.Context, catalog materialization.BundleCatalog, target publication.Target, identity publication.PublicationIdentity, outputs []publication.OutputStream, limits publication.Limits) error {
	if catalog == nil {
		return fmt.Errorf("publication execution catalog is required")
	}
	key := materialization.BundleIdentity{Name: identity.Name, Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, RecipeDigest: identity.RecipeDigest, SchemaDigest: identity.SchemaDigest, ScopeDigest: identity.ScopeDigest, EngineVersion: identity.EngineVersion, AuthResourcePaths: append([]string(nil), identity.AuthResourcePaths...)}.Key()
	if existing, err := catalog.FindExecutionByKey(ctx, key); err == nil {
		if existing.State == materialization.BundleReady {
			return nil
		}
		if existing.State != materialization.BundleFailed {
			return fmt.Errorf("identical Elasticsearch publication is already in state %s", existing.State)
		}
	} else if !errors.Is(err, materialization.ErrBundleNotFound) {
		return err
	}
	now := time.Now().UTC()
	execution := materialization.BundleExecution{
		ID: uuid.NewString(), Key: key,
		BundleIdentity: materialization.BundleIdentity{Name: identity.Name, Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, RecipeDigest: identity.RecipeDigest, SchemaDigest: identity.SchemaDigest, ScopeDigest: identity.ScopeDigest, EngineVersion: identity.EngineVersion, AuthResourcePaths: append([]string(nil), identity.AuthResourcePaths...)},
		State:          materialization.BundleLoading, CreatedAt: now, UpdatedAt: now,
	}
	if err := catalog.SaveExecution(ctx, execution); err != nil {
		return err
	}
	result, err := publication.Publish(ctx, target, identity, outputs, limits)
	if err != nil {
		execution.State = materialization.BundleFailed
		execution.Error = err.Error()
		execution.UpdatedAt = time.Now().UTC()
		_ = catalog.SaveExecution(context.Background(), execution)
		return err
	}
	execution.State = materialization.BundleReady
	readyAt := time.Now().UTC()
	execution.ReadyAt = &readyAt
	execution.UpdatedAt = readyAt
	execution.Outputs = make([]materialization.BundleOutputRecord, 0, len(result.Outputs))
	for _, output := range result.Outputs {
		execution.Outputs = append(execution.Outputs, materialization.BundleOutputRecord{Name: output.Name, PhysicalTable: output.PhysicalName, RowCount: output.RowCount, ByteCount: output.ByteCount, State: materialization.BundleReady})
	}
	return catalog.SaveExecution(ctx, execution)
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
			columns = append(columns, publication.LogicalColumn{Name: column.Name, Kind: kind, Repeated: column.Cardinality == "many", Nullable: column.Nullable})
		}
		return columns
	}
	return []publication.LogicalColumn{{Name: "__loom_row_id", Kind: "string", IsIdentity: true}}
}

func exitf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
