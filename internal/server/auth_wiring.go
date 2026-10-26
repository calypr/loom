package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
)

type authComponents struct {
	authenticator authscope.Authenticator
	authorizer    authscope.Authorizer
	scopeResolver *authscope.ScopeResolver
}

func wireAuth(cfg Config, noAuth bool, listExisting func(context.Context, catalog.AuthResourcePathOptions) ([]string, error)) (authComponents, error) {
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
		resourceAccess, err := authscope.NewFenceUserAccessClientWithTrustedIssuer(client, cfg.Auth.Calypr.TrustedIssuer, cfg.Auth.Calypr.CacheTTL)
		if err != nil {
			return authComponents{}, fmt.Errorf("create Fence resource access client: %w", err)
		}
		resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
			ResourceAccess:                resourceAccess,
			ListExistingAuthResourcePaths: listExisting,
			CacheTTL:                      cfg.Auth.Calypr.CacheTTL,
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
