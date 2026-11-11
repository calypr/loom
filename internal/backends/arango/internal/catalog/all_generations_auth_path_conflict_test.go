package arango

// F-2 durable control for the F-B27 all-generations discovery (REPAIR-15),
// relocated into the adapter tree with the implementation (arch-backend-07
// Wave B). Auth-path discovery asks for either one dataset generation or the
// union over all of them, never both: the all-generations query ignores
// @dataset_generation, so accepting both would silently discard the
// caller's selection. The producer-side half of this control stays in
// internal/authscope (auth_resource_path_options_conflict_test.go), where
// authscope remains the only producer of AllGenerations requests. The fake
// Arango client keeps it hermetic.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	store "github.com/calypr/loom/internal/backends/arango/internal/store"
	"github.com/calypr/loom/internal/catalog"
)

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
	s, err := New(client)
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
		if !errors.Is(err, ErrAllGenerationsWithGeneration) {
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
