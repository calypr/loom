package server

import (
	"errors"
	"fmt"

	loomapi "github.com/calypr/loom/generated/loomapi"
	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	graphapi "github.com/calypr/loom/internal/api/graphql/graph"
	"github.com/calypr/loom/internal/api/graphql/graph/resolver"
	api "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	publication "github.com/calypr/loom/internal/dataframe/publication"
)

type routeScopeMode uint8

const (
	routeScopeUnset routeScopeMode = iota
	routeScopeUnrestricted
	routeScopeScoped
)

type routeScopePolicy struct {
	mode     routeScopeMode
	resolver *authscope.ScopeResolver
}

func unrestrictedRouteScope() routeScopePolicy {
	return routeScopePolicy{mode: routeScopeUnrestricted}
}

func scopedRouteScope(resolver *authscope.ScopeResolver) routeScopePolicy {
	return routeScopePolicy{mode: routeScopeScoped, resolver: resolver}
}

func (p routeScopePolicy) validate() error {
	switch p.mode {
	case routeScopeUnrestricted:
		if p.resolver != nil {
			return errors.New("route composition is inconsistent: unrestricted scope policy must not carry a scope resolver")
		}
		return nil
	case routeScopeScoped:
		if p.resolver == nil {
			return errors.New("route composition is incomplete: the calypr scoped policy requires its scope resolver before routes are installed")
		}
		return nil
	default:
		return fmt.Errorf("route composition is incomplete: missing explicit scope policy (got mode %d)", uint8(p.mode))
	}
}

func (p routeScopePolicy) resolverOrNil() *authscope.ScopeResolver {
	if p.mode != routeScopeScoped {
		return nil
	}
	return p.resolver
}

type routeDependencies struct {
	releases publication.BundleCatalog
	scopes   routeScopePolicy
}

func (d routeDependencies) validate() error {
	return d.scopes.validate()
}

func registerRoutes(server *api.HTTPServer, generationService *loadapi.Service, authorizer authscope.Authorizer, graphResolver *resolver.Resolver, explorerHandlers *explorerHTTPHandlers, deps routeDependencies) error {
	if err := deps.validate(); err != nil {
		return fmt.Errorf("validate route composition: %w", err)
	}
	loadHandler, err := loadapi.NewHandler(loadapi.Config{Service: generationService, Authorizer: authorizer})
	if err != nil {
		return fmt.Errorf("create generation load handler: %w", err)
	}
	routes := &HTTPRoutes{
		server:   server,
		load:     loadHandler,
		releases: deps.releases,
		scopes:   deps.scopes.resolverOrNil(),
		graphql:  graphapi.RouteConfig{Handler: graphapi.NewHandler(graphResolver, server.Logger()), Playground: graphapi.NewPlaygroundHandler("/graphql/graph"), Sandbox: graphapi.NewApolloSandboxHandler("/graphql/graph")},
		explorer: explorerHandlers,
	}
	handler := loomapi.NewStrictHandler(routes, nil)
	loomapi.RegisterHandlersWithOptions(server.App(), handler, loomapi.FiberServerOptions{})
	return nil
}
