package authscope

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/calypr/loom/internal/catalog"
)

func fb27ctlSeed(r *ScopeResolver, project, datasetGeneration string, allGenerations bool, paths ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[scopeCacheKey{project: project, datasetGeneration: datasetGeneration, allGenerations: allGenerations}] = cachedPaths{
		paths:     append([]string(nil), paths...),
		expiresAt: time.Now().Add(time.Minute),
	}
}

func fb27ctlForbiddingLister(t *testing.T) func(context.Context, catalog.AuthResourcePathOptions) ([]string, error) {
	t.Helper()
	return func(context.Context, catalog.AuthResourcePathOptions) ([]string, error) {
		t.Fatalf("VERDICT=UNEXPECTED cache-resident decision re-consulted the catalog")
		return nil, nil
	}
}

func TestFB27ScopeCacheNamespacesDoNotLeakAcrossDiscriminator(t *testing.T) {
	resolver := NewScopeResolver(ScopeResolverConfig{
		CacheTTL:                      time.Minute,
		ListExistingAuthResourcePaths: fb27ctlForbiddingLister(t),
	})
	fb27ctlSeed(resolver, "projA", "gen-1", false, "from-generation-key")
	fb27ctlSeed(resolver, "projA", "", true, "from-all-generations-key")
	ctx := context.Background()

	generationPaths, err := resolver.listExistingPaths(ctx, "projA", "gen-1")
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED generation-scoped lookup = %v", err)
	}
	if !reflect.DeepEqual(generationPaths, []string{"from-generation-key"}) {
		t.Fatalf("VERDICT=UNEXPECTED mode=false lookup returned %q, want the generation entry only", generationPaths)
	}
	allGenerationsPaths, err := resolver.listExistingPathsAllGenerations(ctx, "projA")
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED all-generations lookup = %v", err)
	}
	if !reflect.DeepEqual(allGenerationsPaths, []string{"from-all-generations-key"}) {
		t.Fatalf("VERDICT=UNEXPECTED mode=true lookup returned %q, want the all-generations entry only", allGenerationsPaths)
	}
	t.Logf("VERDICT=REPAIRED both namespaces served their own entry with no catalog call")
}

func TestFB27GenerationLookupNeverServedFromAllGenerationsEntry(t *testing.T) {
	l := &fb27ctlLister{behind: func(catalog.AuthResourcePathOptions) ([]string, error) {
		return []string{"fresh-generation-entry"}, nil
	}}
	resolver := fb27ctlResolver(l)
	fb27ctlSeed(resolver, "projA", "", true, "all-generations-only")
	ctx := context.Background()

	paths, err := resolver.listExistingPaths(ctx, "projA", "")
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED empty-generation lookup = %v", err)
	}
	if l.calls != 1 {
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): the mode=false lookup was answered from the mode=true entry without consulting the catalog")
	}
	if !reflect.DeepEqual(paths, []string{"fresh-generation-entry"}) {
		t.Fatalf("VERDICT=UNEXPECTED mode=false lookup = %q, want the freshly listed generation entry", paths)
	}
}

func TestFB27AllGenerationsLookupNeverServedFromGenerationEntry(t *testing.T) {
	l := &fb27ctlLister{behind: func(catalog.AuthResourcePathOptions) ([]string, error) {
		return []string{"fresh-all-generations-entry"}, nil
	}}
	resolver := fb27ctlResolver(l)
	fb27ctlSeed(resolver, "projA", "", false, "generation-only")
	ctx := context.Background()

	paths, err := resolver.listExistingPathsAllGenerations(ctx, "projA")
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED all-generations lookup = %v", err)
	}
	if l.calls != 1 {
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): the mode=true lookup was answered from the mode=false entry without consulting the catalog")
	}
	if !reflect.DeepEqual(paths, []string{"fresh-all-generations-entry"}) {
		t.Fatalf("VERDICT=UNEXPECTED mode=true lookup = %q, want the freshly listed all-generations entry", paths)
	}
}

func TestFB27LiteralSentinelGenerationIsAnOrdinaryKey(t *testing.T) {
	l := &fb27ctlLister{behind: func(opts catalog.AuthResourcePathOptions) ([]string, error) {
		if opts.AllGenerations {
			return []string{"projA/data"}, nil
		}
		return []string{"gen-only-path"}, nil
	}}
	resolver := fb27ctlResolver(l)
	principal := fb27ctlPrincipal("projA/data")
	ctx := context.Background()

	first, err := resolver.listExistingPaths(ctx, "projA", fb27ctlSentinelGeneration)
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED sentinel-literal generation listing = %v", err)
	}
	if !reflect.DeepEqual(first, []string{"gen-only-path"}) {
		t.Fatalf("VERDICT=UNEXPECTED sentinel-literal generation listing = %q", first)
	}
	if l.calls != 1 {
		t.Fatalf("VERDICT=UNEXPECTED the first sentinel-literal generation listing should hit the catalog once, calls=%d", l.calls)
	}
	second, err := resolver.listExistingPaths(ctx, "projA", fb27ctlSentinelGeneration)
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED cached sentinel-literal generation listing = %v", err)
	}
	if !reflect.DeepEqual(second, first) || l.calls != 1 {
		t.Fatalf("VERDICT=UNEXPECTED the literal generation must cache like any ordinary generation: calls=%d paths=%q", l.calls, second)
	}
	if got := l.opts[0]; got.Project != "projA" || got.DatasetGeneration != fb27ctlSentinelGeneration || got.AllGenerations {
		t.Fatalf("VERDICT=UNEXPECTED the literal must reach the catalog as a plain generation, got %+v", got)
	}

	allGenerations, err := resolver.listExistingPathsAllGenerations(ctx, "projA")
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED all-generations listing = %v", err)
	}
	if l.calls != 2 {
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): the all-generations lookup reused the literal-keyed entry (calls=%d)", l.calls)
	}
	if !reflect.DeepEqual(allGenerations, []string{"projA/data"}) {
		t.Fatalf("VERDICT=UNEXPECTED all-generations listing = %q", allGenerations)
	}
	if err := resolver.AuthorizeWrite(ctx, principal, "projA", "projA/data"); err != nil {
		t.Fatalf("VERDICT=UNEXPECTED same-project write after the literal-keyed read = %v", err)
	}
	if l.calls != 2 {
		t.Fatalf("VERDICT=UNEXPECTED the write decision must reuse the mode=true entry, calls=%d", l.calls)
	}
	t.Logf("VERDICT=REPAIRED %q behaved as a plain generation key and never substituted for the project-binding entry", fb27ctlSentinelGeneration)
}

func TestFB27AuthoritativeProjectBindingProducerUsesModeTrueOnly(t *testing.T) {
	l := &fb27ctlLister{behind: func(opts catalog.AuthResourcePathOptions) ([]string, error) {
		if opts.AllGenerations {
			return []string{"projA/data"}, nil
		}
		return []string{"gen-only-path"}, nil
	}}
	resolver := fb27ctlResolver(l)
	principal := fb27ctlPrincipal("/projA/data")
	ctx := context.Background()

	if err := resolver.AuthorizeWrite(ctx, principal, "HTAN_INT/cohort", " /projA/data "); err != nil {
		t.Fatalf("VERDICT=UNEXPECTED same-project member write = %v", err)
	}
	if l.calls != 1 {
		t.Fatalf("VERDICT=UNEXPECTED write decision issued %d listings, want 1", l.calls)
	}
	if got := l.opts[0]; got.Project != "HTAN_INT-cohort" || got.DatasetGeneration != "" || !got.AllGenerations {
		t.Fatalf("VERDICT=UNEXPECTED the sole mode=true writer must send an empty generation with AllGenerations, got %+v", got)
	}
	if err := resolver.AuthorizeWrite(ctx, principal, "HTAN_INT/cohort", "projA/data"); err != nil {
		t.Fatalf("VERDICT=UNEXPECTED cached same-project member write = %v", err)
	}
	if l.calls != 1 {
		t.Fatalf("VERDICT=UNEXPECTED warm write must reuse the mode=true entry, calls=%d", l.calls)
	}

	scope, err := resolver.ResolveReadScopeForGeneration(ctx, principal, "HTAN_INT/cohort", "", nil)
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED empty-generation read scope = %v", err)
	}
	if l.calls != 2 {
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): the (mode,false) lookup reused the project-binding entry: calls=%d", l.calls)
	}
	if got := l.opts[1]; got.Project != "HTAN_INT-cohort" || got.DatasetGeneration != "" || got.AllGenerations {
		t.Fatalf("VERDICT=UNEXPECTED generation-scoped read lookup options = %+v", got)
	}
	if scope.Mode != ReadScopeRestricted || len(scope.AuthResourcePaths) != 0 {
		t.Fatalf("VERDICT=UNEXPECTED read scope must intersect the generation listing (gen-only-path), got %+v", scope)
	}
	t.Logf("VERDICT=REPAIRED the all-generations producer is the only mode=true writer and read lookups stay on the false side")
}
