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

// CoversExecutionPaths reports whether this restricted read scope authorizes
// every non-blank auth resource path an execution declares. A scope that
// declares no paths, or an execution that declares only blank paths, is not a
// resolved authorization for that execution. A declaration holding a malformed
// non-blank entry is likewise not a resolvable authorization: see
// normalizeDeclaredExecutionPaths. The two execution-status twins use this
// single set rule, instead of a path-count comparison, as their defense-in-depth
// gate against resolver drift.
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

// normalizeDeclaredExecutionPaths renders the auth resource paths an execution
// declares for the coverage gate.
//
// Whitespace-only entries keep their long-standing meaning of "absent" and are
// dropped, so a declaration that carries blank padding alongside real paths
// still resolves. A non-blank entry that NormalizeAuthResourcePath refuses
// (a forbidden C0/DEL control byte, or bytes that normalize away to nothing,
// e.g. "/") is different in kind: it is a malformed persisted declaration, not
// an absent one. Dropping it would let the surviving paths cover a declaration
// that asserts a path the system can never name or grant, so the whole
// declaration is refused (the boolean result) and the caller conceals. The
// malformed bytes are never echoed, only the refusal.
//
// Incoming caller/grant normalization deliberately stays on the other helper
// (normalizeAuthResourcePathList); this stricter rule applies to the persisted
// declaration a read is being authorized against.
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
	// allGenerations separates the single project-level entry (the
	// auth_resource_path union that AuthorizeWrite uses to bind a write to its
	// target project, F-B27) from the per-generation entries that read and
	// write scope resolution use. The discriminator is part of the key type, so
	// the two namespaces cannot overlap: no datasetGeneration value, and in
	// particular not the "\x00all-generations" literal that the first F-B27 fix
	// reserved inside this keyspace, can address the project-level entry. That
	// matters because a caller chooses datasetGeneration: read-path generations
	// arrive on request payloads and catalog.NormalizeDatasetGeneration only
	// TrimSpace's them (the charset gate, publish's validateOpaqueIdentifier,
	// runs on the write side only). Such a generation is therefore just an
	// ordinary allGenerations=false key that the project-write-binding lookup
	// never consults.
	allGenerations bool
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
			// F-B27: path membership alone lets a principal holding one
			// project's path publish/activate into any other project,
			// because `project` never constrains this decision. Bind the
			// grant to the target project: the path must be one the project
			// itself has recorded. A project with no catalog paths yet keeps
			// first-generation bootstrap (no evidence to compare against).
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

// AuthorizeReadProject verifies project-level read access without attempting
// to resolve a dataset-generation scope. Explorer lifecycle metadata is
// stored independently of a dataframe generation, so using
// ResolveReadScope here would intersect the caller's Fence grants with the
// empty-generation catalog namespace and incorrectly reject valid users.
// Generation-specific catalog and dataframe callers must continue using
// ResolveReadScopeForGeneration.
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
	// A generation-scoped lookup is the false half of the discriminator, so it
	// can never be answered from (nor overwrite) the project-write-binding
	// entry, whatever bytes the caller supplied as its generation.
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

// listExistingPathsAllGenerations returns the project's recorded auth paths
// across every dataset generation for project-level write authorization
// (F-B27). It is the only writer of an allGenerations=true cache key, whose
// generation half stays empty because the listing is not scoped to one
// generation. The discriminator, not a reserved generation string, is what
// keeps this entry unaddressable from the caller-supplied generation keyspace
// (see scopeCacheKey), so the collision class that let a restricted caller
// poison the project binding is eliminated structurally. It fails closed like
// the generation-scoped path so an unavailable catalog cannot silently
// downgrade the project binding to the vulnerable pre-repair posture.
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

// InvalidateGeneration removes a single project/generation auth-path entry.
// It is useful after a generation-specific catalog rebuild without evicting
// the active scope cache for every other immutable generation in the project.
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
