package writeapi

import (
	"context"
	"testing"

	"arangodb-proto/internal/proto"
)

type fakeResourceAccessClient struct {
	resources []string
}

func (f fakeResourceAccessClient) GetAllowedResources(ctx context.Context, authorizationHeader, method, service string) ([]string, error) {
	return append([]string(nil), f.resources...), nil
}

func TestScopeResolverResolveReadAuthResourcePathsIntersectsDBPaths(t *testing.T) {
	resolver := NewScopeResolver(ScopeResolverConfig{
		ConnectionOptions: proto.ConnectionOptions{Backend: "arango"},
		ResourceAccess: fakeResourceAccessClient{
			resources: []string{
				"/programs/EllrottLab/projects/GDC_Data",
				"/programs/EllrottLab/projects/Other",
			},
		},
		ListExistingAuthResourcePaths: func(ctx context.Context, opts proto.AuthResourcePathOptions) ([]string, error) {
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
		ConnectionOptions: proto.ConnectionOptions{Backend: "arango"},
		ResourceAccess: fakeResourceAccessClient{
			resources: []string{"/programs/EllrottLab/projects/GDC_Data"},
		},
		ListExistingAuthResourcePaths: func(ctx context.Context, opts proto.AuthResourcePathOptions) ([]string, error) {
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

func TestScopeAuthorizerRequiresScopedWritePath(t *testing.T) {
	authz := ScopeAuthorizer{
		Resolver: NewScopeResolver(ScopeResolverConfig{
			ConnectionOptions: proto.ConnectionOptions{Backend: "arango"},
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
