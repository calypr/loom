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
	"sort"
	"strings"
	"syscall"

	"github.com/calypr/loom/graphqlapi"
	queryapi "github.com/calypr/loom/graphqlapi/query"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe"
	"github.com/calypr/loom/internal/dataframe/materialization"
	materializationarango "github.com/calypr/loom/internal/dataframe/materialization/arango"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeengine"
	"github.com/calypr/loom/internal/dataframe/recipeexec"
	recipeexecarango "github.com/calypr/loom/internal/dataframe/recipeexec/arango"
	"github.com/calypr/loom/internal/dataframe/semantic"
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
		listen   = flag.String("listen", ":8080", "HTTP listen address")
		noAuth   = flag.Bool("no-auth", false, "disable scoped authorization for local development")
		backend  = flag.String("backend", "arango", "storage backend")
		url      = flag.String("url", "http://127.0.0.1:8529", "ArangoDB URL")
		database = flag.String("database", "fhir_proto", "ArangoDB database")
		schema   = flag.String("schema", "schemas/graph-fhir.json", "FHIR graph schema path for imports")
		// Dataset generations opt the server into resolving a project's READY
		// active manifest before dataframe discovery or execution. This mode
		// disables the legacy one-file import route because that route cannot
		// safely construct a complete immutable snapshot.
		datasetGenerations = flag.Bool("dataset-generations", false, "resolve active immutable dataset generations for dataframe reads and disable legacy single-resource imports")
		clickhouseURL      = flag.String("clickhouse-url", "clickhouse://127.0.0.1:9000", "ClickHouse native URL for published dataframe reads")
		clickhouseDatabase = flag.String("clickhouse-database", "loom", "ClickHouse database for published dataframe reads")
		recipeBatchRows    = flag.Int("recipe-batch-rows", 1000, "maximum recipe materialization rows per ClickHouse batch")
		recipeBatchBytes   = flag.Int("recipe-batch-bytes", 4<<20, "maximum recipe materialization bytes per ClickHouse batch")
	)
	flag.Parse()

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
	if err := lifecycleClient.Bootstrap(context.Background(), recipeexecarango.BootstrapSpec()); err != nil {
		exitf("bootstrap recipe registry: %v", err)
	}
	recipeRegistry, err := recipeexecarango.New(lifecycleClient)
	if err != nil {
		exitf("create recipe registry: %v", err)
	}
	defaultBundle, err := recipe.DefaultACEDBundle()
	if err != nil {
		exitf("load default dataframe recipe: %v", err)
	}
	if _, err := (recipeexec.PersistentRegistry{Store: recipeRegistry}).Register(context.Background(), defaultBundle); err != nil {
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
	if *noAuth {
		authorizer = authscope.AllowAllAuthorizer{}
	} else {
		scopeResolver = authscope.NewScopeResolver(authscope.ScopeResolverConfig{
			ConnectionOptions: connOpts,
		})
		authorizer = authscope.ScopeAuthorizer{Resolver: scopeResolver}
	}

	dataframes := dataframe.NewService(dataframe.ServiceConfig{
		ConnectionOptions:      connOpts,
		DiscoverReferences:     discoverReferences,
		DiscoverFields:         discoverFields,
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
	recipeEngine, err := recipeengine.New(recipeengine.Config{
		Registry: recipeRegistry,
		QueryRows: func(ctx context.Context, query string, batchSize int, bindVars map[string]any, visit func(map[string]any) error) error {
			return lifecycleClient.QueryRows(ctx, query, batchSize, bindVars, visit)
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
		RecipeControl:         recipeengine.Control{Engine: recipeEngine},
		RecipeMaterialize: func(ctx context.Context, name string, bindings recipe.RuntimeBindings, plan semantic.ResolvedRecipePlan) (graphqlapi.RecipeExecution, error) {
			full, err := recipeEngine.Resolve(ctx, name, bindings)
			if err != nil {
				return graphqlapi.RecipeExecution{}, err
			}
			if full.Semantic.SemanticPlan.RecipeDigest != plan.SemanticPlan.RecipeDigest || full.Semantic.ResolvedSchemaDigest != plan.ResolvedSchemaDigest {
				return graphqlapi.RecipeExecution{}, fmt.Errorf("recipe plan changed during materialization")
			}
			streams, err := recipeEngine.Streams(ctx, full)
			if err != nil {
				return graphqlapi.RecipeExecution{}, err
			}
			identity := materialization.BundleIdentity{Name: name, Project: bindings.Project, DatasetGeneration: bindings.DatasetGeneration, RecipeDigest: plan.SemanticPlan.RecipeDigest, SchemaDigest: plan.ResolvedSchemaDigest, ScopeDigest: plan.ScopeDigest, EngineVersion: "loom-recipe-v1"}
			streamInputs := make([]materialization.StreamOutput, 0, len(streams))
			for _, stream := range streams {
				stream := stream
				columns := recipeOutputColumns(full, stream.Name)
				rowNumber := 0
				streamInputs = append(streamInputs, materialization.StreamOutput{
					Name: stream.Name, Columns: columns,
					Stream: func(streamCtx context.Context, visit func(map[string]any) error) error {
						_, err := stream.Stream(streamCtx, func(row map[string]any) error {
							if _, ok := row["__loom_row_id"]; !ok {
								row["__loom_row_id"] = fmt.Sprintf("%s:%d", stream.Name, rowNumber)
							}
							rowNumber++
							return visit(row)
						})
						return err
					},
				})
			}
			if err := materialization.PublishStreamBundle(ctx, bundleStore, identity, streamInputs, materialization.StreamPublishConfig{BatchRows: *recipeBatchRows, BatchBytes: *recipeBatchBytes}); err != nil {
				return graphqlapi.RecipeExecution{}, err
			}
			return graphqlapi.RecipeExecution{ID: identity.Key(), Name: name, RecipeDigest: identity.RecipeDigest, ResolvedSchemaDigest: identity.SchemaDigest, SourceGeneration: identity.DatasetGeneration, State: string(materialization.BundleReady)}, nil
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
		Authorizer:                   authorizer,
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

func recipeOutputColumns(plan recipeengine.Resolved, outputName string) []clickhousestore.Column {
	columns := []clickhousestore.Column{{Name: "__loom_row_id", Type: "String"}}
	seen := map[string]struct{}{"__loom_row_id": {}}
	for _, output := range plan.Semantic.SemanticPlan.Outputs {
		if output.Name != outputName {
			continue
		}
		for _, field := range output.Fields {
			if _, ok := seen[field.Name]; ok {
				continue
			}
			columns = append(columns, clickhousestore.Column{Name: field.Name, Type: recipeColumnType(string(field.Expr.Type.Kind))})
			seen[field.Name] = struct{}{}
		}
		var addTraversal func(semantic.SemanticNode)
		addTraversal = func(node semantic.SemanticNode) {
			for _, field := range node.Fields {
				if _, ok := seen[field.Name]; ok {
					continue
				}
				if field.Expr != nil {
					columns = append(columns, clickhousestore.Column{Name: field.Name, Type: recipeColumnType(string(field.Expr.Type.Kind))})
				}
				seen[field.Name] = struct{}{}
			}
			for _, child := range node.Children {
				addTraversal(child)
			}
		}
		for _, child := range output.Root.Children {
			addTraversal(child)
		}
		keys := make([]string, 0, len(plan.Semantic.ResolvedColumns))
		for key := range plan.Semantic.ResolvedColumns {
			if strings.HasPrefix(key, outputName+":") {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			for _, column := range plan.Semantic.ResolvedColumns[key] {
				if _, ok := seen[column.Column.Name]; ok {
					continue
				}
				columns = append(columns, clickhousestore.Column{Name: column.Column.Name, Type: recipeColumnType(column.Column.ValueType)})
				seen[column.Column.Name] = struct{}{}
			}
		}
	}
	return columns
}

func recipeColumnType(kind string) string {
	switch kind {
	case "boolean":
		return "Bool"
	case "integer":
		return "Int64"
	case "decimal":
		return "Float64"
	case "date", "datetime", "code", "uuid", "string", "object", "null", "":
		return "String"
	default:
		return "String"
	}
}

func exitf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
