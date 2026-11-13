package benchmark_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/benchmark"
)

func newScratchResolver(t *testing.T, fenceURL string) *authscope.ScopeResolver {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	ra, err := authscope.NewFenceUserAccessClientWithTrustedIssuer(client, fenceURL, time.Second)
	if err != nil {
		t.Fatalf("fence client: %v", err)
	}
	return authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ResourceAccess: ra,
		ListExistingAuthResourcePaths: func(context.Context, catalog.AuthResourcePathOptions) ([]string, error) {
			return nil, nil
		},
		CacheTTL: time.Second,
	})
}

func TestScratchAuthorizeWriteWithBearerPrincipal(t *testing.T) {
	fence, err := benchmark.NewMockFence()
	if err != nil {
		t.Fatalf("fence: %v", err)
	}
	defer fence.Close()
	const path = "/programs/loom/projects/loom-bench/benchmark"
	fence.Grant("bench-admin", path)
	resolver := newScratchResolver(t, fence.URL())

	// Variant A: header-carrying principal, deployer double-prefix shape.
	token := "Bearer " + benchmark.MakeBearerToken(fence.URL(), "bench-admin")
	principal := &authscope.Principal{AuthorizationHeader: token, Subject: "bench-admin"}
	if err := resolver.AuthorizeWrite(context.Background(), principal, "loom-bench", path); err != nil {
		t.Fatalf("variant A AuthorizeWrite: %v (fence requests=%d)", err, fence.Requests())
	}
	t.Logf("variant A passed; fence requests=%d", fence.Requests())

	// Variant B: nil principal — expect the rehearsal's shape.
	errB := resolver.AuthorizeWrite(context.Background(), nil, "loom-bench", path)
	if errB == nil {
		t.Fatalf("variant B unexpectedly passed")
	}
	t.Logf("variant B refused as expected: %v", errB)

	// Variant C: principal with no AuthorizationHeader (locals/ctx drop).
	errC := resolver.AuthorizeWrite(context.Background(), &authscope.Principal{Subject: "bench-admin"}, "loom-bench", path)
	if errC == nil {
		t.Fatalf("variant C unexpectedly passed")
	}
	t.Logf("variant C refused as expected: %v", errC)
}
