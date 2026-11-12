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

// routeScopeMode is the closed set of scoped-auth composition policies the
// route layer accepts. There is deliberately no usable zero value: a
// composition that forgets to state its policy fails validation instead of
// silently defaulting to unrestricted reads.
type routeScopeMode uint8

const (
	routeScopeUnset routeScopeMode = iota
	// routeScopeUnrestricted is the explicit basic/no-auth policy: no
	// resolver participates and scope-bearing routes serve without a
	// backend scope read (the published basic/test behavior).
	routeScopeUnrestricted
	// routeScopeScoped is the calypr policy: the lane's ScopeResolver is
	// required and completeness is validated before any route installs.
	routeScopeScoped
)

// routeScopePolicy states, explicitly, how scope-bearing routes authorize.
// It replaces the old "nil ScopeResolver means unrestricted" convention at
// the composition boundary (arch-backend-08); the nil-means-unrestricted
// reading survives only inside handlers, derived from a validated policy.
type routeScopePolicy struct {
	mode     routeScopeMode
	resolver *authscope.ScopeResolver
}

// unrestrictedRouteScope is the explicit basic/no-auth policy.
func unrestrictedRouteScope() routeScopePolicy {
	return routeScopePolicy{mode: routeScopeUnrestricted}
}

// scopedRouteScope is the calypr-lane policy; the resolver is required and
// its absence is caught by validate before routes are installed.
func scopedRouteScope(resolver *authscope.ScopeResolver) routeScopePolicy {
	return routeScopePolicy{mode: routeScopeScoped, resolver: resolver}
}

// validate enforces the composition contract before route installation: an
// incomplete scoped composition (calypr without its resolver), an
// inconsistent unrestricted composition carrying a resolver, and the unset
// zero policy are all startup errors.
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

// resolverOrNil renders the validated policy for the handler-side
// nil-means-unrestricted convention, so route handlers are unchanged.
func (p routeScopePolicy) resolverOrNil() *authscope.ScopeResolver {
	if p.mode != routeScopeScoped {
		return nil
	}
	return p.resolver
}

// routeDependencies is the typed composition contract for registerRoutes,
// replacing the optional ...any type-switch locator (arch-backend-08): each
// route family receives exactly the dependencies it declares, every
// dependency is a named field (no runtime type sniffing), and completeness
// is validated before any route is installed. Handlers never receive this
// bundle -- it is consumed once, at composition.
type routeDependencies struct {
	// releases answers dataframe-release pointer reads for the
	// execution/activation routes. A lane that does not serve them either
	// passes an explicit typed-refusal value (the PostgreSQL full lane's
	// stores, the first-query lane's refusal siblings at the loadapi seam)
	// or the lane's pinned nil posture, which the route-census test holds
	// to its published guard.
	releases publication.BundleCatalog
	// scopes is the explicit scoped-auth policy for scope-bearing routes.
	scopes routeScopePolicy
}

// validate checks every declared dependency before route installation so an
// incomplete composition can never serve a partially wired route table.
func (d routeDependencies) validate() error {
	return d.scopes.validate()
}

func registerRoutes(server *api.HTTPServer, generationService *loadapi.Service, authorizer authscope.Authorizer, graphResolver *resolver.Resolver, explorerHandlers *explorerHTTPHandlers, deps routeDependencies) error {
	// Validate the complete composition BEFORE installing any route: an
	// incomplete scoped composition must not leave a half-wired app able to
	// serve (backend-simplification-20261110 amendment).
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
