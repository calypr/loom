package server

import (
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
)

func TestWireAuthCalyprRequiresAndUsesConfiguredTrustedIssuer(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.Mode = "calypr"
	cfg.Auth.Calypr.TrustedIssuer = "https://fence.example/base"
	cfg.Auth.Calypr.RequestTimeout = 3 * time.Second
	cfg.Auth.Calypr.CacheTTL = 17 * time.Second

	components, err := wireAuth(cfg, false, nil)
	if err != nil {
		t.Fatalf("wireAuth() error = %v", err)
	}
	if _, ok := components.authenticator.(authscope.CalyprAuthenticator); !ok {
		t.Fatalf("authenticator = %T, want CalyprAuthenticator", components.authenticator)
	}
	authorizer, ok := components.authorizer.(authscope.ScopeAuthorizer)
	if !ok || components.scopeResolver == nil || authorizer.Resolver != components.scopeResolver {
		t.Fatalf("authorizer/resolver = %T/%#v, want scoped Calypr wiring", components.authorizer, components.scopeResolver)
	}

	for _, issuer := range []string{"", "not-an-issuer"} {
		cfg.Auth.Calypr.TrustedIssuer = issuer
		if _, err := wireAuth(cfg, false, nil); err == nil {
			t.Fatalf("wireAuth() unexpectedly accepted trusted issuer %q", issuer)
		}
	}
}
