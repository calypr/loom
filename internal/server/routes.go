package server

import (
	"fmt"

	loomapi "github.com/calypr/loom/generated/loomapi"
	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	graphapi "github.com/calypr/loom/internal/api/graphql/graph"
	"github.com/calypr/loom/internal/api/graphql/graph/resolver"
	api "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	publication "github.com/calypr/loom/internal/dataframe/publication"
)

func registerRoutes(server *api.HTTPServer, generationService *loadapi.Service, authorizer authscope.Authorizer, graphResolver *resolver.Resolver, explorerHandlers *explorerHTTPHandlers, optional ...any) error {
	if explorerHandlers == nil {
		return fmt.Errorf("create Explorer HTTP routes: Explorer handlers are required")
	}
	var sourceLoader loadapi.SourceLoader
	var sourceLimits loadapi.SourceLoadLimits
	for _, value := range optional {
		switch typed := value.(type) {
		case loadapi.SourceLoader:
			sourceLoader = typed
		case loadapi.SourceLoadLimits:
			sourceLimits = typed
		}
	}
	loadHandler, err := loadapi.NewHandler(loadapi.Config{Service: generationService, Authorizer: authorizer, SourceLoader: sourceLoader, SourceLoadLimits: sourceLimits})
	if err != nil {
		return fmt.Errorf("create generation load handler: %w", err)
	}
	var releases publication.BundleCatalog
	var scopes *authscope.ScopeResolver
	var dataframes dataframeReadRoutes
	for _, value := range optional {
		switch typed := value.(type) {
		case dataframeReadRoutes:
			dataframes = typed
		case publication.BundleCatalog:
			releases = typed
		case *authscope.ScopeResolver:
			scopes = typed
		}
	}
	routes := &HTTPRoutes{
		server:     server,
		load:       loadHandler,
		releases:   releases,
		scopes:     scopes,
		graphql:    graphapi.RouteConfig{Handler: graphapi.NewHandler(graphResolver, server.Logger()), Playground: graphapi.NewPlaygroundHandler("/graphql/graph"), Sandbox: graphapi.NewApolloSandboxHandler("/graphql/graph")},
		explorer:   explorerHandlers,
		dataframes: dataframes,
	}
	handler := loomapi.NewStrictHandler(routes, nil)
	loomapi.RegisterHandlersWithOptions(server.App(), handler, loomapi.FiberServerOptions{})
	return nil
}
