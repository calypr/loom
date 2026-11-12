package server

// Unit B tests for the typed route composition contract
// (arch-backend-08, backend-architecture-coverage-20261109-v1 amendment):
// the optional ...any route-dependency locator is replaced by named typed
// fields, the scoped-auth policy is explicit per auth mode, and incomplete
// scoped composition is refused before any route installs.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
)

func newRouteTestHTTPServer(t *testing.T) *httpapi.HTTPServer {
	t.Helper()
	server, err := httpapi.NewHTTPServer(httpapi.HTTPConfig{Authorizer: authscope.AllowAllAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestRouteScopePolicyValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		policy  routeScopePolicy
		wantErr string
	}{
		{name: "unset_zero_policy_is_refused", policy: routeScopePolicy{}, wantErr: "missing explicit scope policy"},
		{name: "scoped_without_resolver_is_refused", policy: scopedRouteScope(nil), wantErr: "requires its scope resolver"},
		{name: "unrestricted_with_resolver_is_refused", policy: routeScopePolicy{mode: routeScopeUnrestricted, resolver: &authscope.ScopeResolver{}}, wantErr: "must not carry a scope resolver"},
		{name: "unrestricted_is_valid", policy: unrestrictedRouteScope()},
		{name: "scoped_with_resolver_is_valid", policy: scopedRouteScope(&authscope.ScopeResolver{})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.policy.validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("valid policy rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected refusal containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestRegisterRoutesRefusesIncompleteScopedCompositionBeforeInstall(t *testing.T) {
	server := newRouteTestHTTPServer(t)
	err := registerRoutes(server, nil, authscope.AllowAllAuthorizer{}, nil, nil, routeDependencies{scopes: scopedRouteScope(nil)})
	if err == nil {
		t.Fatal("incomplete scoped composition must be refused")
	}
	if !strings.Contains(err.Error(), "requires its scope resolver") {
		t.Fatalf("refusal must name the missing resolver, got %v", err)
	}
	// The refusal must reach the caller as a startup failure, not a panic
	// or a half-installed route table: the app must still answer its
	// dependency-free liveness route, and no generated route may have been
	// registered -- the load route a successful composition would have
	// installed must 404 rather than serve.
	if _, status := server.Liveness(nil); status != 200 {
		t.Fatalf("liveness must stay dependency-free, got %d", status)
	}
	response, err := server.App().Test(httptest.NewRequest(http.MethodPost, "/api/v1/datasets/acme/generations/gen-1", nil))
	if err != nil {
		t.Fatalf("load-route probe after refused install: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("a refused registerRoutes must leave no load route registered: POST load got %d, want 404", response.StatusCode)
	}
}

func TestRegisterRoutesUnsetPolicyIsRefused(t *testing.T) {
	server := newRouteTestHTTPServer(t)
	if err := registerRoutes(server, nil, authscope.AllowAllAuthorizer{}, nil, nil, routeDependencies{}); err == nil || !strings.Contains(err.Error(), "missing explicit scope policy") {
		t.Fatalf("unset scope policy must be refused, got %v", err)
	}
}

func TestWireAuthScopePoliciesAreExplicit(t *testing.T) {
	// no-auth: explicit unrestricted.
	noAuth, err := wireAuth(Config{}, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if noAuth.scopePolicy.mode != routeScopeUnrestricted || noAuth.scopePolicy.resolverOrNil() != nil {
		t.Fatalf("no-auth must be explicitly unrestricted, got %+v", noAuth.scopePolicy)
	}
	// basic: explicit unrestricted, no resolver.
	basic, err := wireAuth(Config{Auth: AuthConfig{Mode: "basic", Basic: BasicAuthConfig{Username: "u", Password: "p"}}}, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if basic.scopePolicy.mode != routeScopeUnrestricted || basic.scopePolicy.resolverOrNil() != nil {
		t.Fatalf("basic must be explicitly unrestricted, got %+v", basic.scopePolicy)
	}
	// calypr: scoped policy carrying its required resolver.
	calypr, err := wireAuth(Config{Auth: AuthConfig{Mode: "calypr", Calypr: CalyprAuthConfig{TrustedIssuer: "https://issuer.example"}}}, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calypr.scopePolicy.mode != routeScopeScoped || calypr.scopePolicy.resolverOrNil() == nil {
		t.Fatalf("calypr must carry its required resolver, got %+v", calypr.scopePolicy)
	}
	if calypr.scopePolicy.resolverOrNil() != calypr.scopeResolver {
		t.Fatal("the scoped policy must carry the same resolver the authorizer uses")
	}
	if err := calypr.scopePolicy.validate(); err != nil {
		t.Fatalf("calypr policy must validate: %v", err)
	}
}
