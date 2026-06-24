package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/calypr/loom/internal/api"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/catalog/cache"
	"github.com/calypr/loom/internal/graphqlapi"
	"github.com/calypr/loom/internal/ingest"
)

const (
	defaultURL      = "http://127.0.0.1:8529"
	defaultDatabase = "fhir_proto"
	defaultSchema   = "schemas/graph-fhir.json"
)

func main() {
	fs := flag.NewFlagSet("arango-fhir-server", flag.ExitOnError)
	listenAddr := fs.String("listen", ":8080", "HTTP listen address")
	bodyLimit := fs.Int("body-limit", 1024*1024*1024, "Maximum request body size in bytes")
	readBufferSize := fs.Int("read-buffer-size", 1024*1024, "Fiber request read buffer size in bytes; also limits max header size")
	noAuth := fs.Bool("no-auth", false, "Disable scope-based auth for local demo use")

	loadOpts := ingest.LoadOptions{}
	fs.StringVar(&loadOpts.URL, "url", defaultURL, "Backend base URL")
	fs.StringVar(&loadOpts.Database, "database", defaultDatabase, "Backend database")
	fs.StringVar(&loadOpts.Schema, "schema", defaultSchema, "graph-fhir JSON schema")
	fs.IntVar(&loadOpts.BatchSize, "batch-size", 5000, "Bulk insert batch size")
	fs.IntVar(&loadOpts.ProgressEvery, "progress-every", 50000, "Emit progress every N input rows")
	fs.IntVar(&loadOpts.WriterCount, "writers", 8, "Concurrent writer goroutines")
	fs.BoolVar(&loadOpts.FailFast, "fail-fast", false, "Stop on the first decode, validation, or edge conversion error")
	fs.StringVar(&loadOpts.WriteAPI, "write-api", "import", "Bulk write API: import or document")

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	discoveryCache := cache.New()
	var scopeResolver *authscope.ScopeResolver
	authenticator := authscope.Authenticator(authscope.BearerTokenAuthenticator{})
	authorizer := authscope.Authorizer(authscope.ScopeAuthorizer{})
	if *noAuth {
		authenticator = authscope.StaticAuthenticator{
			Principal: authscope.Principal{Subject: "local-demo"},
		}
		authorizer = authscope.AllowAllAuthorizer{}
	} else {
		scopeResolver = authscope.NewScopeResolver(authscope.ScopeResolverConfig{
			ConnectionOptions: loadOpts.ConnectionOptions,
		})
		authorizer = authscope.ScopeAuthorizer{Resolver: scopeResolver}
	}
	service, err := api.NewService(api.ServiceConfig{
		Runner: api.IngestRunner{BaseOptions: loadOpts},
		Logger: logger,
		OnSuccess: func(project string) {
			discoveryCache.InvalidateProject(project)
			if scopeResolver != nil {
				scopeResolver.InvalidateProject(project)
			}
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	graphResolver := graphqlapi.NewResolver(graphqlapi.ResolverConfig{
		ConnectionOptions:  loadOpts.ConnectionOptions,
		DiscoverReferences: discoveryCache.DiscoverReferences(catalog.DiscoverPopulatedReferences),
		DiscoverFields:     discoveryCache.DiscoverFields(catalog.DiscoverPopulatedFields),
		ScopeResolver:      scopeResolver,
	})
	graphHandler := graphqlapi.NewHandler(graphResolver)
	server, err := api.NewHTTPServer(api.HTTPConfig{
		Service:                  service,
		Authenticator:            authenticator,
		Authorizer:               authorizer,
		GraphQLHandler:           graphHandler,
		GraphQLPlaygroundHandler: graphqlapi.NewPlaygroundHandler("/graphql"),
		ApolloSandboxHandler:     graphqlapi.NewApolloSandboxHandler("/graphql"),
		Logger:                   logger,
		BodyLimit:                *bodyLimit,
		ReadBufferSize:           *readBufferSize,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	logger.Info("starting server", "listen", *listenAddr, "backend", "arango", "database", loadOpts.Database, "no_auth", *noAuth)
	if err := server.App().Listen(*listenAddr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
