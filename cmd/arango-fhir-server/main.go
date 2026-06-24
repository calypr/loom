package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"arangodb-proto/internal/catalogcache"
	"arangodb-proto/internal/graphqlapi"
	"arangodb-proto/internal/proto"
	"arangodb-proto/internal/writeapi"
)

const (
	defaultBackend  = "arango"
	defaultURL      = "http://127.0.0.1:8529"
	defaultNS       = "fhir_proto"
	defaultDatabase = "fhir_proto"
	defaultSchema   = "schemas/graph-fhir.json"
)

func main() {
	fs := flag.NewFlagSet("arango-fhir-server", flag.ExitOnError)
	listenAddr := fs.String("listen", ":8080", "HTTP listen address")
	maxConcurrent := fs.Int("max-concurrent-imports", 1, "Maximum concurrent in-process imports")
	bodyLimit := fs.Int("body-limit", 1024*1024*1024, "Maximum request body size in bytes")
	readBufferSize := fs.Int("read-buffer-size", 1024*1024, "Fiber request read buffer size in bytes; also limits max header size")
	noAuth := fs.Bool("no-auth", false, "Disable scope-based auth for local demo use")

	loadOpts := proto.LoadOptions{}
	fs.StringVar(&loadOpts.Backend, "backend", defaultBackend, "Backend: arango, surreal, or postgres")
	fs.StringVar(&loadOpts.URL, "url", defaultURL, "Backend base URL")
	fs.StringVar(&loadOpts.Namespace, "namespace", defaultNS, "SurrealDB namespace")
	fs.StringVar(&loadOpts.Database, "database", defaultDatabase, "Backend database")
	fs.StringVar(&loadOpts.Username, "username", "root", "Backend username")
	fs.StringVar(&loadOpts.Password, "password", "root", "Backend password")
	fs.StringVar(&loadOpts.AuthToken, "auth-token", "", "SurrealDB auth token; overrides username/password when set")
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
	discoveryCache := catalogcache.New()
	var scopeResolver *writeapi.ScopeResolver
	authenticator := writeapi.Authenticator(writeapi.BearerTokenAuthenticator{})
	authorizer := writeapi.Authorizer(writeapi.ScopeAuthorizer{})
	if *noAuth {
		authenticator = writeapi.StaticAuthenticator{
			Principal: writeapi.Principal{Subject: "local-demo"},
		}
		authorizer = writeapi.AllowAllAuthorizer{}
	} else {
		scopeResolver = writeapi.NewScopeResolver(writeapi.ScopeResolverConfig{
			ConnectionOptions: loadOpts.ConnectionOptions,
		})
		authorizer = writeapi.ScopeAuthorizer{Resolver: scopeResolver}
	}
	service, err := writeapi.NewService(writeapi.ServiceConfig{
		Runner:        writeapi.ProtoRunner{BaseOptions: loadOpts},
		Logger:        logger,
		MaxConcurrent: *maxConcurrent,
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
	graphService := graphqlapi.NewService(graphqlapi.ServiceConfig{
		ConnectionOptions:  loadOpts.ConnectionOptions,
		DiscoverReferences: discoveryCache.DiscoverReferences(proto.DiscoverPopulatedReferences),
		DiscoverFields:     discoveryCache.DiscoverFields(proto.DiscoverPopulatedFields),
		ScopeResolver:      scopeResolver,
	})
	graphHandler := graphqlapi.NewHandler(graphqlapi.NewResolver(graphService))
	server, err := writeapi.NewHTTPServer(writeapi.HTTPConfig{
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

	logger.Info("starting write api server", "listen", *listenAddr, "backend", loadOpts.Backend, "database", loadOpts.Database, "no_auth", *noAuth)
	if err := server.App().Listen(*listenAddr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
