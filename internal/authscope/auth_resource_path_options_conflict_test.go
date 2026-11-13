package authscope

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

func TestFB27ResolverNeverEmitsTheConflictingShape(t *testing.T) {
	l := &fb27ctlLister{behind: func(opts catalog.AuthResourcePathOptions) ([]string, error) {
		if opts.AllGenerations && catalog.NormalizeDatasetGeneration(opts.DatasetGeneration) != "" {
			t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): resolver emitted the rejected option combination %+v", opts)
		}
		return []string{"projA/data"}, nil
	}}
	resolver := fb27ctlResolver(l)
	principal := fb27ctlPrincipal("/projA/data")
	ctx := context.Background()

	if err := resolver.AuthorizeWrite(ctx, principal, "projA", "/projA/data"); err != nil {
		t.Fatalf("VERDICT=UNEXPECTED same-project member write = %v", err)
	}
	if _, err := resolver.ResolveReadScopeForGeneration(ctx, principal, "projA", " gen-1 ", nil); err != nil {
		t.Fatalf("VERDICT=UNEXPECTED generation read scope = %v", err)
	}
	if _, err := resolver.ResolveWriteScopeForGeneration(ctx, principal, "projA", "gen-2", nil); err != nil {
		t.Fatalf("VERDICT=UNEXPECTED generation write scope = %v", err)
	}
	if l.calls != 3 {
		t.Fatalf("VERDICT=UNEXPECTED discovery calls = %d, want 3 ( %+v)", l.calls, l.opts)
	}
	if !l.opts[0].AllGenerations || l.opts[0].DatasetGeneration != "" {
		t.Fatalf("VERDICT=UNEXPECTED project-binding request = %+v", l.opts[0])
	}
	if l.opts[1].AllGenerations || l.opts[1].DatasetGeneration != "gen-1" {
		t.Fatalf("VERDICT=UNEXPECTED read request = %+v", l.opts[1])
	}
	if l.opts[2].AllGenerations || l.opts[2].DatasetGeneration != "gen-2" {
		t.Fatalf("VERDICT=UNEXPECTED write-scope request = %+v", l.opts[2])
	}
	t.Logf("VERDICT=REPAIRED resolver requests stay on one side of the F-2 boundary")
}
