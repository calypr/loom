package authscope

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	store "github.com/calypr/loom/internal/store/arango"
)

// F-2 durable control for the F-B27 all-generations discovery (REPAIR-15).
// Auth-path discovery asks for either one dataset generation or the union over
// all of them, never both: the all-generations query ignores @dataset_generation,
// so accepting both would silently discard the caller's selection. This control
// sits in the F-B27 family because authscope is the only producer of
// AllGenerations requests, which makes the boundary part of its contract. The
// fake Arango client keeps it hermetic.

type fb27ctlArangoClient struct {
	queries []string
	vars    []map[string]any
}

func (c *fb27ctlArangoClient) QueryRows(_ context.Context, query string, _ int, vars map[string]any, _ store.RowVisitor) error {
	c.queries = append(c.queries, query)
	copied := make(map[string]any, len(vars))
	for k, v := range vars {
		copied[k] = v
	}
	c.vars = append(c.vars, copied)
	return nil
}

func (c *fb27ctlArangoClient) InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error {
	panic("unused by the auth path discovery controls")
}

func (c *fb27ctlArangoClient) ExecuteAQL(context.Context, string, map[string]any) error {
	panic("unused by the auth path discovery controls")
}

func (c *fb27ctlArangoClient) CollectionExists(context.Context, string) (bool, error) {
	panic("unused by the auth path discovery controls")
}

func (c *fb27ctlArangoClient) Bootstrap(context.Context, store.BootstrapSpec) error {
	panic("unused by the auth path discovery controls")
}

// fb27ctlDiscover runs one discovery against a fresh fake client and asserts
// the guard/query boundary: a rejected request must not reach Arango, an
// accepted one must issue exactly one query.
func fb27ctlDiscover(t *testing.T, opts catalog.AuthResourcePathOptions) ([]string, error) {
	t.Helper()
	client := &fb27ctlArangoClient{}
	s, err := catalogarango.New(client)
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED store construction = %v", err)
	}
	paths, err := s.DiscoverExistingAuthResourcePaths(context.Background(), opts)
	if err != nil {
		if len(client.queries) != 0 {
			t.Fatalf("VERDICT=UNEXPECTED rejected request reached Arango (%d queries) for %+v: %v", len(client.queries), opts, err)
		}
		return paths, err
	}
	if len(client.queries) != 1 {
		t.Fatalf("VERDICT=UNEXPECTED accepted request issued %d queries for %+v, want 1", len(client.queries), opts)
	}
	if paths == nil {
		t.Fatalf("VERDICT=UNEXPECTED accepted request returned nil paths for %+v", opts)
	}
	return paths, nil
}

func TestFB27AllGenerationsWithSelectedGenerationIsRejected(t *testing.T) {
	for _, generation := range []string{"gen-1", " gen-1 ", "sha256:0000000000000000000000000000000000000000000000000000000000000000"} {
		_, err := fb27ctlDiscover(t, catalog.AuthResourcePathOptions{
			Project:           "projA",
			DatasetGeneration: generation,
			AllGenerations:    true,
		})
		if err == nil {
			t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): AllGenerations with datasetGeneration %q was accepted, so discovery silently ignored the selected generation", generation)
		}
		if !errors.Is(err, catalogarango.ErrAllGenerationsWithGeneration) {
			t.Fatalf("VERDICT=UNEXPECTED conflict error must wrap ErrAllGenerationsWithGeneration, got %v", err)
		}
		for _, want := range []string{"AllGenerations", "datasetGeneration", generation} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("VERDICT=UNEXPECTED conflict error %q must name the conflicting option and its value %q", err, want)
			}
		}
	}
	t.Logf("VERDICT=REPAIRED AllGenerations plus a selected generation is refused before any query")
}

func TestFB27SupportedAuthResourcePathOptionShapesStillDiscover(t *testing.T) {
	cases := []struct {
		name string
		opts catalog.AuthResourcePathOptions
	}{
		{"all generations with empty generation", catalog.AuthResourcePathOptions{Project: "projA", AllGenerations: true}},
		{"all generations with whitespace only generation", catalog.AuthResourcePathOptions{Project: "projA", DatasetGeneration: "   ", AllGenerations: true}},
		{"generation scoped", catalog.AuthResourcePathOptions{Project: "projA", DatasetGeneration: " gen-1 "}},
		{"legacy null namespace", catalog.AuthResourcePathOptions{Project: "projA"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := fb27ctlDiscover(t, tc.opts); err != nil {
				t.Fatalf("VERDICT=UNEXPECTED valid options %+v rejected: %v", tc.opts, err)
			}
		})
	}
}

// TestFB27ResolverNeverEmitsTheConflictingShape pins the producer side of the
// same boundary: every all-generations request the resolver makes carries an
// empty generation, and every generation-bearing request leaves AllGenerations
// false, so the rejected combination is unreachable from the authz path.
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
