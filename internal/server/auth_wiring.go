package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type authComponents struct {
	authenticator authscope.Authenticator
	authorizer    authscope.Authorizer
	scopeResolver *authscope.ScopeResolver
}

func wireAuth(cfg Config, noAuth bool, connOpts arangostore.ConnectionOptions) (authComponents, error) {
	if noAuth {
		return authComponents{
			authenticator: authscope.StaticAuthenticator{Principal: authscope.Principal{Subject: "anonymous"}},
			authorizer:    authscope.AllowAllAuthorizer{},
		}, nil
	}
	switch {
	case strings.EqualFold(cfg.Auth.Mode, "basic"):
		return authComponents{
			authenticator: authscope.BasicAuthenticator{Username: cfg.Auth.Basic.Username, Password: cfg.Auth.Basic.Password},
			authorizer:    authscope.AllowAllAuthorizer{},
		}, nil
	case strings.EqualFold(cfg.Auth.Mode, "calypr"):
		client := &http.Client{Timeout: cfg.Auth.Calypr.RequestTimeout}
		resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
			ConnectionOptions: connOpts,
			ResourceAccess:    authscope.NewFenceUserAccessClientWithTTL(client, cfg.Auth.Calypr.CacheTTL),
			CacheTTL:          cfg.Auth.Calypr.CacheTTL,
		})
		return authComponents{
			authenticator: authscope.CalyprAuthenticator{},
			authorizer:    authscope.ScopeAuthorizer{Resolver: resolver},
			scopeResolver: resolver,
		}, nil
	default:
		return authComponents{}, fmt.Errorf("unsupported auth mode %q", cfg.Auth.Mode)
	}
}
