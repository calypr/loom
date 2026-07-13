package authscope

import "context"

type Principal struct {
	Subject             string            `json:"subject"`
	Claims              map[string]string `json:"claims,omitempty"`
	Projects            []string          `json:"projects,omitempty"`
	AuthResourcePaths   []string          `json:"auth_resource_paths,omitempty"`
	AuthorizationHeader string            `json:"-"`
}

type principalContextKey struct{}

func ContextWithPrincipal(ctx context.Context, principal *Principal) context.Context {
	if principal == nil {
		return ctx
	}
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (*Principal, bool) {
	if ctx == nil {
		return nil, false
	}
	principal, ok := ctx.Value(principalContextKey{}).(*Principal)
	return principal, ok
}

type Authenticator interface {
	Authenticate(ctx context.Context, headers map[string][]string) (*Principal, error)
}

type Authorizer interface {
	AuthorizeWrite(ctx context.Context, principal *Principal, project, authResourcePath string) error
}

type StaticAuthenticator struct {
	Principal Principal
}

func (a StaticAuthenticator) Authenticate(ctx context.Context, headers map[string][]string) (*Principal, error) {
	principal := a.Principal
	if principal.Subject == "" {
		principal.Subject = "anonymous"
	}
	return &principal, nil
}

type AllowAllAuthorizer struct{}

func (AllowAllAuthorizer) AuthorizeWrite(ctx context.Context, principal *Principal, project, authResourcePath string) error {
	return nil
}
