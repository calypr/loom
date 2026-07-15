package authscope

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/calypr/loom/internal/catalog"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type ResourceAccessClient interface {
	GetAllowedResources(ctx context.Context, authorizationHeader, method, service string) ([]string, error)
}

type ScopeResolverConfig struct {
	ConnectionOptions             arangostore.ConnectionOptions
	ResourceAccess                ResourceAccessClient
	ListExistingAuthResourcePaths func(context.Context, catalog.AuthResourcePathOptions) ([]string, error)
	CacheTTL                      time.Duration
}

type ScopeResolver struct {
	connOpts       arangostore.ConnectionOptions
	resourceAccess ResourceAccessClient
	listExisting   func(context.Context, catalog.AuthResourcePathOptions) ([]string, error)
	cacheTTL       time.Duration

	mu    sync.RWMutex
	cache map[scopeCacheKey]cachedPaths
}

// ReadScopeMode records whether an effective read scope may bypass the
// auth_resource_path predicate. It is deliberately separate from the path
// list: a restricted caller can legitimately resolve to zero paths, and that
// must never be confused with an unrestricted caller.
//
// The empty value is reserved for legacy callers that carry only a path list.
// Those callers retain the historical rule that an empty list is unrestricted.
type ReadScopeMode string

const (
	ReadScopeUnrestricted ReadScopeMode = "unrestricted"
	ReadScopeRestricted   ReadScopeMode = "restricted"
)

// ReadScope is the authorization result that downstream catalog and dataframe
// code must propagate together. AuthResourcePaths may be empty in either mode;
// Mode is therefore authoritative for the AQL bypass bind variable.
type ReadScope struct {
	AuthResourcePaths []string
	Mode              ReadScopeMode
}

// Unrestricted reports whether this scope may bypass auth_resource_path
// filtering. Unknown modes are treated as restricted so an invalid internal
// value cannot widen access.
func (s ReadScope) Unrestricted() bool {
	return s.Mode == ReadScopeUnrestricted
}

// Clone returns an independent copy suitable for passing between request
// layers without sharing a resolver-owned path slice.
func (s ReadScope) Clone() ReadScope {
	return ReadScope{
		AuthResourcePaths: cloneStrings(s.AuthResourcePaths),
		Mode:              s.Mode,
	}
}

type cachedPaths struct {
	paths     []string
	expiresAt time.Time
}

// scopeCacheKey keeps authorization-path discovery isolated between immutable
// dataset generations. A project can legitimately have a different set of
// populated auth_resource_path values after a reload, so caching by project
// alone would otherwise leak stale paths into a new generation's catalog and
// dataframe queries.
type scopeCacheKey struct {
	project           string
	datasetGeneration string
}

type BearerTokenAuthenticator struct{}

type ScopeAuthorizer struct {
	Resolver *ScopeResolver
}

func (a BearerTokenAuthenticator) Authenticate(ctx context.Context, headers map[string][]string) (*Principal, error) {
	principal := &Principal{Subject: "anonymous"}
	auth := firstHeaderValue(headers, "Authorization")
	if auth == "" {
		return principal, nil
	}
	principal.AuthorizationHeader = strings.TrimSpace(auth)
	if sub := subjectFromAuthorizationHeader(auth); sub != "" {
		principal.Subject = sub
	}
	return principal, nil
}

func NewScopeResolver(cfg ScopeResolverConfig) *ScopeResolver {
	if cfg.ListExistingAuthResourcePaths == nil {
		cfg.ListExistingAuthResourcePaths = catalog.DiscoverExistingAuthResourcePaths
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 30 * time.Second
	}
	if cfg.ResourceAccess == nil {
		cfg.ResourceAccess = NewFenceUserAccessClient(nil)
	}
	return &ScopeResolver{
		connOpts:       cfg.ConnectionOptions,
		resourceAccess: cfg.ResourceAccess,
		listExisting:   cfg.ListExistingAuthResourcePaths,
		cacheTTL:       cfg.CacheTTL,
		cache:          make(map[scopeCacheKey]cachedPaths),
	}
}

func (a ScopeAuthorizer) AuthorizeWrite(ctx context.Context, principal *Principal, project, authResourcePath string) error {
	if a.Resolver == nil {
		return nil
	}
	return a.Resolver.AuthorizeWrite(ctx, principal, project, authResourcePath)
}

// ResolveReadScope returns the effective read authorization mode and paths.
// In particular, it preserves a restricted-empty intersection instead of
// encoding it as an empty slice that a later AQL layer could mistake for an
// unrestricted scope.
func (r *ScopeResolver) ResolveReadScope(ctx context.Context, principal *Principal, project string, requested []string) (ReadScope, error) {
	return r.ResolveReadScopeForGeneration(ctx, principal, project, "", requested)
}

// ResolveReadScopeForGeneration resolves an authorization scope against the
// populated auth-resource paths for exactly one dataset generation. An empty
// generation preserves the legacy null-generation namespace; it never means
// every generation. Callers that have selected an active manifest must use
// this method so the scope cache and catalog reads remain generation-aligned.
func (r *ScopeResolver) ResolveReadScopeForGeneration(ctx context.Context, principal *Principal, project, datasetGeneration string, requested []string) (ReadScope, error) {
	callerPaths, restricted, err := r.resolveCallerPaths(ctx, principal, PermissionRead, "*")
	if err != nil {
		return ReadScope{}, err
	}
	normalizedRequested := normalizeAuthResourcePathList(requested)
	if !restricted {
		if len(normalizedRequested) == 0 {
			return ReadScope{Mode: ReadScopeUnrestricted}, nil
		}
		// An unrestricted caller can still deliberately narrow a read to a
		// requested subset. That subset is a real AQL constraint, not an
		// authorization bypass.
		return ReadScope{AuthResourcePaths: normalizedRequested, Mode: ReadScopeRestricted}, nil
	}

	existingPaths, err := r.listExistingPaths(ctx, project, datasetGeneration)
	if err != nil {
		return ReadScope{}, err
	}
	allowedSet := make(map[string]struct{}, len(callerPaths))
	for _, path := range callerPaths {
		allowedSet[path] = struct{}{}
	}
	effective := make([]string, 0, len(existingPaths))
	for _, path := range existingPaths {
		if _, ok := allowedSet[path]; ok {
			effective = append(effective, path)
		}
	}
	if len(normalizedRequested) == 0 {
		return ReadScope{AuthResourcePaths: effective, Mode: ReadScopeRestricted}, nil
	}
	effectiveSet := make(map[string]struct{}, len(effective))
	for _, path := range effective {
		effectiveSet[path] = struct{}{}
	}
	for _, path := range normalizedRequested {
		if _, ok := effectiveSet[path]; !ok {
			return ReadScope{}, fmt.Errorf("authResourcePath %q is outside caller scope", path)
		}
	}
	return ReadScope{AuthResourcePaths: normalizedRequested, Mode: ReadScopeRestricted}, nil
}

// ResolveReadAuthResourcePaths is retained for existing callers that only
// accept paths. New query-building code must use ResolveReadScope so a
// restricted empty result cannot become an unrestricted AQL query.
func (r *ScopeResolver) ResolveReadAuthResourcePaths(ctx context.Context, principal *Principal, project string, requested []string) ([]string, error) {
	return r.ResolveReadAuthResourcePathsForGeneration(ctx, principal, project, "", requested)
}

// ResolveReadAuthResourcePathsForGeneration is the compatibility payload form
// of ResolveReadScopeForGeneration. New query callers must carry the returned
// ReadScope mode as well so a restricted empty result cannot become an
// unrestricted AQL query.
func (r *ScopeResolver) ResolveReadAuthResourcePathsForGeneration(ctx context.Context, principal *Principal, project, datasetGeneration string, requested []string) ([]string, error) {
	scope, err := r.ResolveReadScopeForGeneration(ctx, principal, project, datasetGeneration, requested)
	if err != nil {
		return nil, err
	}
	return cloneStrings(scope.AuthResourcePaths), nil
}

func (r *ScopeResolver) AuthorizeWrite(ctx context.Context, principal *Principal, project, authResourcePath string) error {
	callerPaths, restricted, err := r.resolveCallerPaths(ctx, principal, PermissionWrite, "*")
	if err != nil {
		return err
	}
	if !restricted {
		return nil
	}
	if strings.TrimSpace(authResourcePath) == "" {
		return fmt.Errorf("auth_resource_path is required when scoped authorization is enabled")
	}
	normalized := NormalizeAuthResourcePath(authResourcePath)
	for _, path := range callerPaths {
		if path == normalized {
			return nil
		}
	}
	return fmt.Errorf("auth_resource_path %q is outside caller scope for project %q", normalized, project)
}

// ResolveWriteScopeForGeneration proves that the caller may publish or mutate
// data for the selected project. It mirrors read-scope resolution but asks
// Fence for write permission and never treats an empty intersection as an
// unrestricted result.
func (r *ScopeResolver) ResolveWriteScopeForGeneration(ctx context.Context, principal *Principal, project, datasetGeneration string, requested []string) (ReadScope, error) {
	callerPaths, restricted, err := r.resolveCallerPaths(ctx, principal, PermissionWrite, "*")
	if err != nil {
		return ReadScope{}, err
	}
	normalizedRequested := normalizeAuthResourcePathList(requested)
	if !restricted {
		if len(normalizedRequested) == 0 {
			return ReadScope{Mode: ReadScopeUnrestricted}, nil
		}
		return ReadScope{AuthResourcePaths: normalizedRequested, Mode: ReadScopeRestricted}, nil
	}
	existingPaths, err := r.listExistingPaths(ctx, project, datasetGeneration)
	if err != nil {
		return ReadScope{}, err
	}
	allowed := make(map[string]struct{}, len(callerPaths))
	for _, path := range callerPaths {
		allowed[path] = struct{}{}
	}
	effective := make([]string, 0, len(existingPaths))
	for _, path := range existingPaths {
		if _, ok := allowed[path]; ok {
			effective = append(effective, path)
		}
	}
	if len(normalizedRequested) > 0 {
		effectiveSet := make(map[string]struct{}, len(effective))
		for _, path := range effective {
			effectiveSet[path] = struct{}{}
		}
		for _, path := range normalizedRequested {
			if _, ok := effectiveSet[path]; !ok {
				return ReadScope{}, fmt.Errorf("authResourcePath %q is outside caller write scope", path)
			}
		}
		effective = normalizedRequested
	}
	if len(effective) == 0 {
		return ReadScope{}, fmt.Errorf("caller has no write access to project %q", project)
	}
	return ReadScope{AuthResourcePaths: effective, Mode: ReadScopeRestricted}, nil
}

func (r *ScopeResolver) resolveCallerPaths(ctx context.Context, principal *Principal, method Permission, service string) ([]string, bool, error) {
	if principal == nil {
		if r.resourceAccess != nil {
			return []string{}, true, nil
		}
		return nil, false, nil
	}
	if auth := strings.TrimSpace(principal.AuthorizationHeader); auth != "" && r.resourceAccess != nil {
		resources, err := r.resourceAccess.GetAllowedResources(ctx, auth, string(method), service)
		if err != nil {
			return nil, true, err
		}
		return authResourcePathsFromResources(resources), true, nil
	}
	if len(principal.AuthResourcePaths) > 0 {
		return normalizeAuthResourcePathList(principal.AuthResourcePaths), true, nil
	}
	if r.resourceAccess != nil {
		return []string{}, true, nil
	}
	return nil, false, nil
}

func (r *ScopeResolver) listExistingPaths(ctx context.Context, project, datasetGeneration string) ([]string, error) {
	project = strings.TrimSpace(project)
	datasetGeneration = catalog.NormalizeDatasetGeneration(datasetGeneration)
	if project == "" {
		return []string{}, nil
	}
	key := scopeCacheKey{project: project, datasetGeneration: datasetGeneration}
	now := time.Now()
	r.mu.RLock()
	entry, ok := r.cache[key]
	r.mu.RUnlock()
	if ok && now.Before(entry.expiresAt) {
		return cloneStrings(entry.paths), nil
	}
	paths, err := r.listExisting(ctx, catalog.AuthResourcePathOptions{
		ConnectionOptions: r.connOpts,
		Project:           project,
		DatasetGeneration: datasetGeneration,
	})
	if err != nil {
		return nil, err
	}
	paths = normalizeAuthResourcePathList(paths)
	r.mu.Lock()
	r.cache[key] = cachedPaths{
		paths:     cloneStrings(paths),
		expiresAt: now.Add(r.cacheTTL),
	}
	r.mu.Unlock()
	return paths, nil
}

func (r *ScopeResolver) InvalidateProject(project string) {
	project = strings.TrimSpace(project)
	if project == "" {
		r.mu.Lock()
		r.cache = make(map[scopeCacheKey]cachedPaths)
		r.mu.Unlock()
		return
	}
	r.mu.Lock()
	for key := range r.cache {
		if key.project == project {
			delete(r.cache, key)
		}
	}
	r.mu.Unlock()
}

// InvalidateGeneration removes a single project/generation auth-path entry.
// It is useful after a generation-specific catalog rebuild without evicting
// the active scope cache for every other immutable generation in the project.
func (r *ScopeResolver) InvalidateGeneration(project, datasetGeneration string) {
	key := scopeCacheKey{
		project:           strings.TrimSpace(project),
		datasetGeneration: catalog.NormalizeDatasetGeneration(datasetGeneration),
	}
	if key.project == "" {
		return
	}
	r.mu.Lock()
	delete(r.cache, key)
	r.mu.Unlock()
}
