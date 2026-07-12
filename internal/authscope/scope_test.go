package authscope

import (
	"context"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type fakeResourceAccessClient struct {
	resources []string
}

func (f fakeResourceAccessClient) GetAllowedResources(ctx context.Context, authorizationHeader, method, service string) ([]string, error) {
	return append([]string(nil), f.resources...), nil
}

func TestScopeResolverResolveReadAuthResourcePathsIntersectsDBPaths(t *testing.T) {
	resolver := NewScopeResolver(ScopeResolverConfig{
		ConnectionOptions: arangostore.ConnectionOptions{},
		ResourceAccess: fakeResourceAccessClient{
			resources: []string{
				"/programs/EllrottLab/projects/GDC_Data",
				"/programs/EllrottLab/projects/Other",
			},
		},
		ListExistingAuthResourcePaths: func(ctx context.Context, opts catalog.AuthResourcePathOptions) ([]string, error) {
			return []string{"EllrottLab-GDC_Data", "Another-Missing"}, nil
		},
	})

	paths, err := resolver.ResolveReadAuthResourcePaths(context.Background(), &Principal{
		AuthorizationHeader: "Bearer header.payload.signature",
	}, "P1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "EllrottLab-GDC_Data" {
		t.Fatalf("unexpected resolved paths: %#v", paths)
	}
}

func TestScopeResolverRejectsRequestedPathOutsideIntersection(t *testing.T) {
	resolver := NewScopeResolver(ScopeResolverConfig{
		ConnectionOptions: arangostore.ConnectionOptions{},
		ResourceAccess: fakeResourceAccessClient{
			resources: []string{"/programs/EllrottLab/projects/GDC_Data"},
		},
		ListExistingAuthResourcePaths: func(ctx context.Context, opts catalog.AuthResourcePathOptions) ([]string, error) {
			return []string{"EllrottLab-GDC_Data"}, nil
		},
	})

	_, err := resolver.ResolveReadAuthResourcePaths(context.Background(), &Principal{
		AuthorizationHeader: "Bearer header.payload.signature",
	}, "P1", []string{"EllrottLab-Other"})
	if err == nil {
		t.Fatal("expected requested path validation error")
	}
}

func TestScopeResolverResolveReadScopeKeepsRestrictedEmptyIntersection(t *testing.T) {
	resolver := NewScopeResolver(ScopeResolverConfig{
		ConnectionOptions: arangostore.ConnectionOptions{},
		ResourceAccess: fakeResourceAccessClient{
			resources: []string{"/programs/EllrottLab/projects/GDC_Data"},
		},
		ListExistingAuthResourcePaths: func(context.Context, catalog.AuthResourcePathOptions) ([]string, error) {
			// The caller has access to GDC_Data, but this project currently has
			// no matching catalog scope. This must be a deny-all scope, not a
			// no-filter scope.
			return []string{"Another-Project"}, nil
		},
	})

	scope, err := resolver.ResolveReadScope(context.Background(), &Principal{
		AuthorizationHeader: "Bearer header.payload.signature",
	}, "P1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if scope.Mode != ReadScopeRestricted || scope.Unrestricted() {
		t.Fatalf("scope mode = %#v, want restricted", scope)
	}
	if len(scope.AuthResourcePaths) != 0 {
		t.Fatalf("restricted empty scope paths = %#v, want none", scope.AuthResourcePaths)
	}

	// Keep the legacy wrapper's payload contract while proving that new query
	// callers must consume ResolveReadScope for its mode as well as its paths.
	paths, err := resolver.ResolveReadAuthResourcePaths(context.Background(), &Principal{
		AuthorizationHeader: "Bearer header.payload.signature",
	}, "P1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("legacy paths = %#v, want empty", paths)
	}
}

func TestScopeResolverCachesExistingPathsPerNormalizedGeneration(t *testing.T) {
	var calls []catalog.AuthResourcePathOptions
	resolver := NewScopeResolver(ScopeResolverConfig{
		ResourceAccess: fakeResourceAccessClient{
			resources: []string{"/programs/example/projects/allowed"},
		},
		ListExistingAuthResourcePaths: func(_ context.Context, opts catalog.AuthResourcePathOptions) ([]string, error) {
			calls = append(calls, opts)
			switch opts.DatasetGeneration {
			case "generation-a":
				return []string{"example-allowed"}, nil
			case "generation-b":
				return []string{"example-other"}, nil
			default:
				t.Fatalf("unexpected catalog generation %q", opts.DatasetGeneration)
				return nil, nil
			}
		},
	})
	principal := &Principal{AuthorizationHeader: "Bearer header.payload.signature"}

	first, err := resolver.ResolveReadScopeForGeneration(context.Background(), principal, " P1 ", " generation-a ", nil)
	if err != nil {
		t.Fatalf("ResolveReadScopeForGeneration(generation-a) error = %v", err)
	}
	if first.Mode != ReadScopeRestricted || !reflect.DeepEqual(first.AuthResourcePaths, []string{"example-allowed"}) {
		t.Fatalf("generation-a scope = %#v, want restricted example-allowed", first)
	}

	second, err := resolver.ResolveReadScopeForGeneration(context.Background(), principal, "P1", "generation-b", nil)
	if err != nil {
		t.Fatalf("ResolveReadScopeForGeneration(generation-b) error = %v", err)
	}
	if second.Mode != ReadScopeRestricted || len(second.AuthResourcePaths) != 0 {
		t.Fatalf("generation-b scope = %#v, want restricted empty", second)
	}

	// The normalized generation-a lookup must use its own cache entry instead
	// of reusing generation-b's empty intersection.
	again, err := resolver.ResolveReadScopeForGeneration(context.Background(), principal, "P1", "generation-a", nil)
	if err != nil {
		t.Fatalf("cached ResolveReadScopeForGeneration(generation-a) error = %v", err)
	}
	if !reflect.DeepEqual(again.AuthResourcePaths, []string{"example-allowed"}) {
		t.Fatalf("cached generation-a scope = %#v, want example-allowed", again)
	}
	if got, want := len(calls), 2; got != want {
		t.Fatalf("existing-path catalog calls = %d, want %d distinct generations", got, want)
	}
	if calls[0].Project != "P1" || calls[0].DatasetGeneration != "generation-a" || calls[1].Project != "P1" || calls[1].DatasetGeneration != "generation-b" {
		t.Fatalf("generation-aware catalog options = %#v", calls)
	}
}

func TestScopeResolverKeepsRestrictedEmptyScopeWithinGeneration(t *testing.T) {
	called := false
	resolver := NewScopeResolver(ScopeResolverConfig{
		ResourceAccess: fakeResourceAccessClient{
			resources: []string{"/programs/example/projects/allowed"},
		},
		ListExistingAuthResourcePaths: func(_ context.Context, opts catalog.AuthResourcePathOptions) ([]string, error) {
			called = true
			if opts.Project != "P1" || opts.DatasetGeneration != "generation-a" {
				t.Fatalf("existing auth paths options = %+v, want P1/generation-a", opts)
			}
			return []string{"example-unrelated"}, nil
		},
	})

	scope, err := resolver.ResolveReadScopeForGeneration(context.Background(), &Principal{
		AuthorizationHeader: "Bearer header.payload.signature",
	}, "P1", " generation-a ", nil)
	if err != nil {
		t.Fatalf("ResolveReadScopeForGeneration() error = %v", err)
	}
	if !called || scope.Mode != ReadScopeRestricted || scope.Unrestricted() || len(scope.AuthResourcePaths) != 0 {
		t.Fatalf("generation-scoped restricted empty scope = %#v", scope)
	}
}

func TestScopeAuthorizerRequiresScopedWritePath(t *testing.T) {
	authz := ScopeAuthorizer{
		Resolver: NewScopeResolver(ScopeResolverConfig{
			ConnectionOptions: arangostore.ConnectionOptions{},
			ResourceAccess: fakeResourceAccessClient{
				resources: []string{"/programs/EllrottLab/projects/GDC_Data"},
			},
		}),
	}
	err := authz.AuthorizeWrite(context.Background(), &Principal{
		AuthorizationHeader: "Bearer header.payload.signature",
	}, "P1", "")
	if err == nil {
		t.Fatal("expected missing auth_resource_path error")
	}
}

func TestNormalizeAuthResourcePathAcceptsResourcePath(t *testing.T) {
	got := NormalizeAuthResourcePath("/programs/EllrottLab/projects/GDC_Data")
	if got != "EllrottLab-GDC_Data" {
		t.Fatalf("NormalizeAuthResourcePath = %q", got)
	}
}
