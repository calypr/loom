package authscope

import (
	"context"
	"errors"
)

// Transport-neutral authorization conditions. Callers classify these
// sentinels at the API boundary instead of matching driver/Fence messages.
var (
	ErrUnauthenticated                 = errors.New("authentication is required")
	ErrForbidden                       = errors.New("authorization denied")
	ErrAuthorizationBackendUnavailable = errors.New("authorization backend unavailable")
)

// Permission is the method recorded by Fence for a resource grant.
// Values are intentionally normalized at the authorization boundary so Loom
// accepts both the lowercase values emitted by current Gen3 services and
// uppercase values used by some deployments.
type Permission string

const (
	PermissionRead  Permission = "read"
	PermissionWrite Permission = "write"
)

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
