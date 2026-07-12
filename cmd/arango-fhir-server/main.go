package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/calypr/loom/internal/api"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	catalogcache "github.com/calypr/loom/internal/catalog/cache"
	"github.com/calypr/loom/internal/dataframe"
	"github.com/calypr/loom/internal/dataframebuilder"
	"github.com/calypr/loom/internal/datasetstore"
	"github.com/calypr/loom/internal/graphqlapi"
	"github.com/calypr/loom/internal/ingest"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func main() {
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

	var activeManifestResolver *datasetstore.Store
	if *datasetGenerations {
		lifecycleClient, err := arangostore.Open(context.Background(), connOpts.URL, connOpts.Database)
		if err != nil {
			exitf("open dataset lifecycle store: %v", err)
		}
		defer lifecycleClient.Close(context.Background())
		if err := lifecycleClient.Bootstrap(context.Background(), datasetstore.BootstrapSpec()); err != nil {
			exitf("bootstrap dataset lifecycle store: %v", err)
		}
		activeManifestResolver, err = datasetstore.New(lifecycleClient)
		if err != nil {
			exitf("create dataset lifecycle store: %v", err)
		}
	}

	discoveryCache := catalogcache.New()
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
	resolver := graphqlapi.NewResolver(dataframebuilder.Config{
		ConnectionOptions:      connOpts,
		DiscoverReferences:     discoverReferences,
		DiscoverFields:         discoverFields,
		Dataframes:             dataframes,
		ScopeResolver:          scopeResolver,
		ActiveManifestResolver: activeManifestResolver,
	})

	importService, err := api.NewService(api.ServiceConfig{
		Runner: api.IngestRunner{BaseOptions: ingest.LoadOptions{
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

func exitf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
