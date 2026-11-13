package authscope

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/projectid"
)

type ResourceAccessClient interface {
	GetAllowedResources(ctx context.Context, authorizationHeader, method, service string) ([]string, error)
}

type ScopeResolverConfig struct {
	ResourceAccess                ResourceAccessClient
	ListExistingAuthResourcePaths func(context.Context, catalog.AuthResourcePathOptions) ([]string, error)
	CacheTTL                      time.Duration
}

type ScopeResolver struct {
	resourceAccess ResourceAccessClient
	listExisting   func(context.Context, catalog.AuthResourcePathOptions) ([]string, error)
	cacheTTL       time.Duration

	mu    sync.RWMutex
	cache map[scopeCacheKey]cachedPaths
}

type ReadScopeMode string

const (
	ReadScopeUnrestricted ReadScopeMode = "unrestricted"
	ReadScopeRestricted   ReadScopeMode = "restricted"
)

type ReadScope struct {
	AuthResourcePaths []string
	Mode              ReadScopeMode
}

func (s ReadScope) Unrestricted() bool {
	return s.Mode == ReadScopeUnrestricted
}

func (s ReadScope) CoversExecutionPaths(executionAuthResourcePaths []string) bool {
	if s.Mode != ReadScopeRestricted {
		return false
	}
	declared, malformed := normalizeDeclaredExecutionPaths(executionAuthResourcePaths)
	if malformed || len(declared) == 0 {
		return false
	}
	allowed := make(map[string]struct{}, len(s.AuthResourcePaths))
	for _, path := range normalizeAuthResourcePathList(s.AuthResourcePaths) {
		allowed[path] = struct{}{}
	}
	for _, path := range declared {
		if _, ok := allowed[path]; !ok {
			return false
		}
	}
	return true
}

func normalizeDeclaredExecutionPaths(paths []string) ([]string, bool) {
	kept := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		normalized := NormalizeAuthResourcePath(path)
		if normalized == "" {
			return nil, true
		}
		kept = append(kept, normalized)
	}
	return normalizeAuthResourcePathList(kept), false
}

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

type scopeCacheKey struct {
	project           string
	datasetGeneration string
	allGenerations    bool
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
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 30 * time.Second
	}
	return &ScopeResolver{
		resourceAccess: cfg.ResourceAccess,
		listExisting:   cfg.ListExistingAuthResourcePaths,
		cacheTTL:       cfg.CacheTTL,
		cache:          make(map[scopeCacheKey]cachedPaths),
	}
}

func (a ScopeAuthorizer) AuthorizeWrite(ctx context.Context, principal *Principal, project, authResourcePath string) error {
	if a.Resolver == nil {
		return fmt.Errorf("%w: scope resolver is required", ErrForbidden)
	}
	return a.Resolver.AuthorizeWrite(ctx, principal, project, authResourcePath)
}

func (r *ScopeResolver) ResolveReadScope(ctx context.Context, principal *Principal, project string, requested []string) (ReadScope, error) {
	return r.ResolveReadScopeForGeneration(ctx, principal, project, "", requested)
}

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
			return ReadScope{}, fmt.Errorf("%w: authResourcePath %q is outside caller scope", ErrForbidden, path)
		}
	}
	return ReadScope{AuthResourcePaths: normalizedRequested, Mode: ReadScopeRestricted}, nil
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
		return fmt.Errorf("%w: auth_resource_path is required when scoped authorization is enabled", ErrForbidden)
	}
	normalized := NormalizeAuthResourcePath(authResourcePath)
	for _, path := range callerPaths {
		if path == normalized {
			projectPaths, err := r.listExistingPathsAllGenerations(ctx, project)
			if err != nil {
				return err
			}
			if len(projectPaths) == 0 {
				return nil
			}
			for _, recorded := range projectPaths {
				if recorded == normalized {
					return nil
				}
			}
			return fmt.Errorf("%w: auth_resource_path %q is not a recorded auth path of project %q", ErrForbidden, normalized, project)
		}
	}
	return fmt.Errorf("%w: auth_resource_path %q is outside caller scope for project %q", ErrForbidden, normalized, project)
}

func (r *ScopeResolver) AuthorizeReadProject(ctx context.Context, principal *Principal, project string) error {
	callerPaths, restricted, err := r.resolveCallerPaths(ctx, principal, PermissionRead, "*")
	if err != nil {
		return err
	}
	if !restricted {
		return nil
	}
	projectKey := projectid.Legacy(projectid.Canonical(project))
	for _, path := range callerPaths {
		if path == projectKey {
			return nil
		}
	}
	return fmt.Errorf("%w: caller has no read access to project %q", ErrForbidden, project)
}

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
				return ReadScope{}, fmt.Errorf("%w: authResourcePath %q is outside caller write scope", ErrForbidden, path)
			}
		}
		effective = normalizedRequested
	}
	if len(effective) == 0 {
		return ReadScope{}, fmt.Errorf("%w: caller has no write access to project %q", ErrForbidden, project)
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
	if auth := strings.TrimSpace(principal.AuthorizationHeader); auth != "" {
		if r.resourceAccess == nil {
			return nil, true, fmt.Errorf("%w: Fence resource access client is not configured", ErrAuthorizationBackendUnavailable)
		}
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
	project = projectid.Canonical(project)
	datasetGeneration = catalog.NormalizeDatasetGeneration(datasetGeneration)
	if project == "" {
		return []string{}, nil
	}
	key := scopeCacheKey{project: project, datasetGeneration: datasetGeneration, allGenerations: false}
	now := time.Now()
	r.mu.RLock()
	entry, ok := r.cache[key]
	r.mu.RUnlock()
	if ok && now.Before(entry.expiresAt) {
		return cloneStrings(entry.paths), nil
	}
	if r.listExisting == nil {
		return nil, ErrAuthorizationBackendUnavailable
	}
	paths, err := r.listExisting(ctx, catalog.AuthResourcePathOptions{
		Project:           projectid.Legacy(project),
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

func (r *ScopeResolver) listExistingPathsAllGenerations(ctx context.Context, project string) ([]string, error) {
	project = projectid.Canonical(project)
	if project == "" {
		return nil, fmt.Errorf("%w: project is required for project-level write authorization", ErrAuthorizationBackendUnavailable)
	}
	key := scopeCacheKey{project: project, datasetGeneration: "", allGenerations: true}
	now := time.Now()
	r.mu.RLock()
	entry, ok := r.cache[key]
	r.mu.RUnlock()
	if ok && now.Before(entry.expiresAt) {
		return cloneStrings(entry.paths), nil
	}
	if r.listExisting == nil {
		return nil, ErrAuthorizationBackendUnavailable
	}
	paths, err := r.listExisting(ctx, catalog.AuthResourcePathOptions{
		Project:        projectid.Legacy(project),
		AllGenerations: true,
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
	project = projectid.Canonical(project)
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

func (r *ScopeResolver) InvalidateGeneration(project, datasetGeneration string) {
	key := scopeCacheKey{
		project:           projectid.Canonical(project),
		datasetGeneration: catalog.NormalizeDatasetGeneration(datasetGeneration),
		allGenerations:    false,
	}
	if key.project == "" {
		return
	}
	r.mu.Lock()
	delete(r.cache, key)
	r.mu.Unlock()
}
