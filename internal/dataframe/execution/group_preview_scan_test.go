package execution

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/store/arango"
)

func TestChooseGroupPreviewScanReusesSmallestCompatibleCoveringIndex(t *testing.T) {
	engine, query, stream := compiledGroupPreviewScanFixture(t, recipe.RuntimeBindings{
		Project: "P1", DatasetGeneration: "G1", AuthScopeMode: authscope.ReadScopeRestricted,
		AuthResourcePaths: []string{"Observation/allowed"},
	})
	required := query.PreviewCoveringIndex
	if required == nil {
		t.Fatal("fixture query has no compiler-owned covering index requirements")
	}
	originalQuery := query.Query
	originalBinds := query.BindVars
	largeFields := append(append([]string(nil), required.Fields...), "payload.extra", "resourceType")
	smallFields := []string{"project", "dataset_generation", "resourceType"}
	smallFields = append(smallFields, required.Fields[2:]...)
	indexes := []arango.PreviewCoveringIndex{
		{Collection: required.Collection, Name: "loom_pivot_preview_existing_larger", Fields: largeFields},
		{Collection: required.Collection, Name: "loom_pivot_preview_existing_smaller", Fields: smallFields},
	}
	indexCalls, explainCalls := 0, 0
	engine.previewCoveringIndexes = func(_ context.Context, collection string) ([]arango.PreviewCoveringIndex, error) {
		indexCalls++
		if collection != required.Collection {
			t.Fatalf("index collection = %q, want %q", collection, required.Collection)
		}
		return indexes, nil
	}
	engine.previewExplainQuery = func(_ context.Context, queryText string, bindVars map[string]any) (arango.ExplainResult, error) {
		explainCalls++
		if strings.Contains(queryText, "loom_pivot_preview_existing_larger") {
			t.Fatalf("larger compatible index was explained before the smaller one:\n%s", queryText)
		}
		if !strings.Contains(queryText, `OPTIONS { indexHint: "loom_pivot_preview_existing_smaller", forceIndexHint: false }`) ||
			strings.Contains(queryText, "forceIndexHint: true") {
			t.Fatalf("candidate query did not use the expected non-forcing hint:\n%s", queryText)
		}
		if !reflect.DeepEqual(bindVars, originalBinds) || bindVars["auth_resource_paths"] == nil ||
			!reflect.DeepEqual(bindVars["auth_resource_paths"], originalBinds["auth_resource_paths"]) {
			t.Fatalf("candidate query changed restricted authorization binds: got=%v want=%v", bindVars, originalBinds)
		}
		return coveringGroupExplain(required.Collection), nil
	}
	engine.previewCollectionCount = func(context.Context, string) (int64, error) {
		t.Fatal("covering candidate should skip the sequential-scan assessment")
		return 0, nil
	}
	stream.stream = func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
		t.Fatal("covering candidate should skip the scoped source count")
		return nil
	}
	if err := engine.chooseGroupPreviewScan(context.Background(), &query, &stream); err != nil {
		t.Fatal(err)
	}
	if indexCalls != 1 || explainCalls != 1 {
		t.Fatalf("metadata/explain calls = %d/%d, want one each", indexCalls, explainCalls)
	}
	if query.Query == originalQuery || query.Query != stream.query || query.PreviewCoveringIndex.Name != indexes[1].Name ||
		!reflect.DeepEqual(query.PreviewCoveringIndex.Fields, smallFields) || !reflect.DeepEqual(query.BindVars, originalBinds) ||
		!reflect.DeepEqual(query.BindVars, stream.bindVars) {
		t.Fatalf("selected compatible query was not applied consistently: query=%q stream=%q spec=%+v", query.Query, stream.query, query.PreviewCoveringIndex)
	}
}

func TestChooseGroupPreviewScanKeepsSequentialFallbackWhenNoCompatibleIndexExists(t *testing.T) {
	engine, query, stream := compiledGroupPreviewScanFixture(t, recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "G1"})
	originalQuery := query.Query
	required := query.PreviewCoveringIndex
	engine.previewCoveringIndexes = func(context.Context, string) ([]arango.PreviewCoveringIndex, error) {
		return []arango.PreviewCoveringIndex{{
			Collection: required.Collection, Name: "loom_pivot_preview_missing_auth",
			Fields: []string{"project", "dataset_generation", "payload.status"},
		}}, nil
	}
	explainCalls, scopeCountCalls := 0, 0
	engine.previewExplainQuery = func(_ context.Context, queryText string, _ map[string]any) (arango.ExplainResult, error) {
		explainCalls++
		if queryText != originalQuery {
			t.Fatalf("incompatible index changed the canonical query:\n%s", queryText)
		}
		return groupExplainWithCoverage(false, false, required.Collection), nil
	}
	engine.previewCollectionCount = func(context.Context, string) (int64, error) { return 1000, nil }
	stream.stream = func(_ context.Context, queryText string, batchSize int, _ map[string]any, visit func(map[string]any) error) error {
		scopeCountCalls++
		if queryText != query.PreviewGroupScan.ScopeCountQuery || batchSize != 1 {
			t.Fatalf("scope-count query = %q batch=%d", queryText, batchSize)
		}
		return visit(map[string]any{"count": float64(950)})
	}
	if err := engine.chooseGroupPreviewScan(context.Background(), &query, &stream); err != nil {
		t.Fatal(err)
	}
	if query.Query != query.PreviewGroupScan.SequentialQuery || explainCalls != 1 || scopeCountCalls != 1 {
		t.Fatalf("sequential fallback state: query=%q explains=%d scope counts=%d", query.Query, explainCalls, scopeCountCalls)
	}
}

func TestChooseGroupPreviewScanRequiresExplainToProveCoveringIndex(t *testing.T) {
	for _, test := range []struct {
		name string
		plan arango.ExplainResult
	}{
		{name: "projection uncovered", plan: groupExplainWithCoverage(false, true)},
		{name: "output uncovered", plan: groupExplainWithCoverage(true, false)},
		{name: "coverage unknown", plan: arango.ExplainResult{Plan: &arango.ExplainPlan{Nodes: []arango.ExplainNode{{Type: "IndexNode", Collection: "Observation"}}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine, query, stream := compiledGroupPreviewScanFixture(t, recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "G1"})
			originalQuery := query.Query
			candidate := arango.PreviewCoveringIndex{
				Collection: query.PreviewCoveringIndex.Collection, Name: "loom_pivot_preview_unproven", Fields: append([]string(nil), query.PreviewCoveringIndex.Fields...),
			}
			engine.previewCoveringIndexes = func(context.Context, string) ([]arango.PreviewCoveringIndex, error) {
				return []arango.PreviewCoveringIndex{candidate}, nil
			}
			explainCalls := 0
			engine.previewExplainQuery = func(_ context.Context, queryText string, _ map[string]any) (arango.ExplainResult, error) {
				explainCalls++
				if queryText == originalQuery {
					return groupExplainWithCoverage(false, false, query.PreviewGroupScan.Collection), nil
				}
				return test.plan, nil
			}
			engine.previewCollectionCount = func(context.Context, string) (int64, error) { return 1000, nil }
			stream.stream = func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
				return visit(map[string]any{"count": float64(500)})
			}
			if err := engine.chooseGroupPreviewScan(context.Background(), &query, &stream); err != nil {
				t.Fatal(err)
			}
			if query.Query != originalQuery || stream.query != originalQuery || explainCalls != 2 {
				t.Fatalf("unproven candidate changed canonical query: query=%q stream=%q explains=%d", query.Query, stream.query, explainCalls)
			}
		})
	}
}

func TestPreviewOutputReusesStoredValuesGroupIndexWithoutPrewarmingIt(t *testing.T) {
	queryCalls, prepareCalls := 0, 0
	index := arango.PreviewCoveringIndex{
		Collection:   "Observation",
		Name:         "loom_pivot_preview_existing_stored_values",
		Fields:       []string{"project", "dataset_generation", "auth_resource_path", "_key", "payload.id", "payload.status"},
		StoredValues: []string{"payload.valueQuantity"},
	}
	engine, err := New(Config{
		Registry:    invalidRecipeRegistry{},
		ScopeDigest: func(recipe.RuntimeBindings) string { return "group-preview-existing-index" },
		QueryRows:   func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
		PreviewQueryRows: func(ctx context.Context, query string, batch int, binds map[string]any, visit func(map[string]any) error) error {
			queryCalls++
			return groupPreviewCacheRows()(ctx, query, batch, binds, visit)
		},
		PreparePreviewIndex: func(context.Context, compiler.PreviewCoveringIndexSpec) error {
			prepareCalls++
			return errors.New("reused indexes must not be prepared")
		},
		PreviewCoveringIndexes: func(context.Context, string) ([]arango.PreviewCoveringIndex, error) {
			return []arango.PreviewCoveringIndex{index}, nil
		},
		PreviewCollectionRevision: func(context.Context, string) (string, error) { return "revision-1", nil },
		PreviewExplainQuery: func(_ context.Context, queryText string, _ map[string]any) (arango.ExplainResult, error) {
			if !strings.Contains(queryText, index.Name) {
				return arango.ExplainResult{}, errors.New("candidate index hint was not rendered")
			}
			return coveringGroupExplain(index.Collection), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved := compileGroupPreviewCacheTestOutput(t, engine, recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "G1"})
	for range 2 {
		if _, err := engine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "group_preview", Limit: 1}, func(map[string]any) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if queryCalls != 1 {
		t.Fatalf("preview query calls = %d, want one execution and one cached replay", queryCalls)
	}
	if prepareCalls != 0 {
		t.Fatalf("reused stored-values index was sent to preview prewarm %d times", prepareCalls)
	}
}

func compiledGroupPreviewScanFixture(t *testing.T, bindings recipe.RuntimeBindings) (*Engine, compiler.CompiledQuery, OutputStream) {
	t.Helper()
	engine, err := New(Config{
		Registry:         invalidRecipeRegistry{},
		ScopeDigest:      func(recipe.RuntimeBindings) string { return "group-preview-scan-test" },
		QueryRows:        func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
		PreviewQueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved := compileGroupPreviewCacheTestOutput(t, engine, bindings)
	stream, query, err := engine.streamForOutput(resolved, "group_preview", 25)
	if err != nil {
		t.Fatal(err)
	}
	return engine, query, stream
}

func coveringGroupExplain(collection string) arango.ExplainResult {
	return groupExplainWithCoverage(true, true, collection)
}

func groupExplainWithCoverage(indexCoversProjections, indexCoversOutProjections bool, collection ...string) arango.ExplainResult {
	name := "Observation"
	if len(collection) != 0 {
		name = collection[0]
	}
	return arango.ExplainResult{Plan: &arango.ExplainPlan{Nodes: []arango.ExplainNode{{
		Type: "IndexNode", Collection: name,
		IndexCoversProjections: &indexCoversProjections, IndexCoversOutProjections: &indexCoversOutProjections,
	}}}}
}

func TestChooseGroupPreviewScanUsesSequentialOnlyForBroadNonCoveringIndex(t *testing.T) {
	covering := false
	outputCovering := false
	explainCalls, scopeCountCalls, collectionCountCalls := 0, 0, 0
	engine, query, stream := groupPreviewScanFixture()
	engine.previewExplainQuery = func(_ context.Context, queryText string, _ map[string]any) (arango.ExplainResult, error) {
		explainCalls++
		if queryText != "normal-query" {
			t.Fatalf("explain query = %q, want original query", queryText)
		}
		return arango.ExplainResult{Plan: &arango.ExplainPlan{Nodes: []arango.ExplainNode{{
			Type: "IndexNode", Collection: "Specimen", IndexCoversProjections: &covering, IndexCoversOutProjections: &outputCovering,
		}}}}, nil
	}
	engine.previewCollectionCount = func(_ context.Context, collection string) (int64, error) {
		collectionCountCalls++
		if collection != "Specimen" {
			t.Fatalf("collection = %q, want Specimen", collection)
		}
		return 1000, nil
	}
	stream.stream = func(_ context.Context, queryText string, batchSize int, _ map[string]any, visit func(map[string]any) error) error {
		scopeCountCalls++
		if queryText != "scope-count-query" || batchSize != 1 {
			t.Fatalf("scope query = %q batch size = %d", queryText, batchSize)
		}
		return visit(map[string]any{"count": float64(950)})
	}

	before, _, ok := newGroupPreviewRowsKey(query.Query, query.BindVars, "Specimen", "revision-1", 25)
	if !ok {
		t.Fatal("original preview cache key was not created")
	}
	if err := engine.chooseGroupPreviewScan(context.Background(), &query, &stream); err != nil {
		t.Fatal(err)
	}
	after, _, ok := newGroupPreviewRowsKey(query.Query, query.BindVars, "Specimen", "revision-1", 25)
	if !ok || before == after {
		t.Fatal("selected AQL must produce a distinct cache key")
	}
	if query.Query != "sequential-query" || stream.query != query.Query || query.BindVars["scan"] != "sequential" || stream.bindVars["scan"] != "sequential" {
		t.Fatalf("sequential query was not applied consistently: query=%q stream=%q binds=%v streamBinds=%v", query.Query, stream.query, query.BindVars, stream.bindVars)
	}
	if explainCalls != 1 || scopeCountCalls != 1 || collectionCountCalls != 1 {
		t.Fatalf("calls: explain=%d scoped=%d collection=%d", explainCalls, scopeCountCalls, collectionCountCalls)
	}
}

func TestChooseGroupPreviewScanFallsBackWhenNotProvenBroadAndNoncovering(t *testing.T) {
	tests := []struct {
		name           string
		setup          func(*Engine)
		wantSequential bool
	}{
		{
			name: "selective source",
			setup: func(engine *Engine) {
				engine.previewCollectionCount = func(context.Context, string) (int64, error) { return 1000, nil }
			},
		},
		{
			name: "covering index",
			setup: func(engine *Engine) {
				covering := true
				engine.previewExplainQuery = func(context.Context, string, map[string]any) (arango.ExplainResult, error) {
					outputCovering := false
					return arango.ExplainResult{Plan: &arango.ExplainPlan{Nodes: []arango.ExplainNode{{Type: "IndexNode", Collection: "Specimen", IndexCoversProjections: &covering, IndexCoversOutProjections: &outputCovering}}}}, nil
				}
			},
		},
		{
			name: "covering output projections",
			setup: func(engine *Engine) {
				outputCovering := true
				engine.previewExplainQuery = func(context.Context, string, map[string]any) (arango.ExplainResult, error) {
					inputCovering := false
					return arango.ExplainResult{Plan: &arango.ExplainPlan{Nodes: []arango.ExplainNode{{Type: "IndexNode", Collection: "Specimen", IndexCoversProjections: &inputCovering, IndexCoversOutProjections: &outputCovering}}}}, nil
				}
			},
		},
		{
			name: "unknown coverage",
			setup: func(engine *Engine) {
				engine.previewExplainQuery = func(context.Context, string, map[string]any) (arango.ExplainResult, error) {
					return arango.ExplainResult{Plan: &arango.ExplainPlan{Nodes: []arango.ExplainNode{{Type: "IndexNode", Collection: "Specimen"}}}}, nil
				}
			},
		},
		{
			name: "unknown output coverage",
			setup: func(engine *Engine) {
				engine.previewExplainQuery = func(context.Context, string, map[string]any) (arango.ExplainResult, error) {
					inputCovering := false
					return arango.ExplainResult{Plan: &arango.ExplainPlan{Nodes: []arango.ExplainNode{{Type: "IndexNode", Collection: "Specimen", IndexCoversProjections: &inputCovering}}}}, nil
				}
			},
		},
		{
			name: "unknown plan",
			setup: func(engine *Engine) {
				engine.previewExplainQuery = func(context.Context, string, map[string]any) (arango.ExplainResult, error) {
					return arango.ExplainResult{}, nil
				}
			},
		},
		{
			name: "explain error",
			setup: func(engine *Engine) {
				engine.previewExplainQuery = func(context.Context, string, map[string]any) (arango.ExplainResult, error) {
					return arango.ExplainResult{}, errors.New("unavailable")
				}
			},
		},
		{
			name: "collection count error",
			setup: func(engine *Engine) {
				engine.previewCollectionCount = func(context.Context, string) (int64, error) { return 0, errors.New("unavailable") }
			},
		},
		{
			name: "empty collection",
			setup: func(engine *Engine) {
				engine.previewCollectionCount = func(context.Context, string) (int64, error) { return 0, nil }
			},
		},
		{
			name: "invalid scope count",
			setup: func(engine *Engine) {
				engine.previewCollectionCount = func(context.Context, string) (int64, error) { return 1000, nil }
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine, query, stream := groupPreviewScanFixture()
			engine.previewExplainQuery = nonCoveringGroupExplain
			engine.previewCollectionCount = func(context.Context, string) (int64, error) { return 1000, nil }
			stream.stream = func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
				value := any(float64(950))
				if test.name == "selective source" {
					value = float64(899)
				}
				if test.name == "invalid scope count" {
					value = "950"
				}
				return visit(map[string]any{"count": value})
			}
			if test.setup != nil {
				test.setup(engine)
			}
			if err := engine.chooseGroupPreviewScan(context.Background(), &query, &stream); err != nil {
				t.Fatal(err)
			}
			if got := query.Query == "sequential-query"; got != test.wantSequential {
				t.Fatalf("sequential selected=%t, want %t", got, test.wantSequential)
			}
		})
	}
}

func TestChooseGroupPreviewScanPropagatesRequestCancellation(t *testing.T) {
	t.Run("during explain", func(t *testing.T) {
		engine, query, stream := groupPreviewScanFixture()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		engine.previewCollectionCount = func(context.Context, string) (int64, error) { return 1000, nil }
		engine.previewExplainQuery = func(context.Context, string, map[string]any) (arango.ExplainResult, error) {
			cancel()
			return arango.ExplainResult{}, context.Canceled
		}
		if err := engine.chooseGroupPreviewScan(ctx, &query, &stream); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want canceled request", err)
		}
		if query.Query != "normal-query" {
			t.Fatalf("canceled preview switched to %q", query.Query)
		}
	})
	t.Run("during scoped count", func(t *testing.T) {
		engine, query, stream := groupPreviewScanFixture()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		engine.previewExplainQuery = nonCoveringGroupExplain
		engine.previewCollectionCount = func(context.Context, string) (int64, error) {
			t.Fatal("collection count called after scoped-count cancellation")
			return 0, nil
		}
		stream.stream = func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			cancel()
			return nil
		}
		if err := engine.chooseGroupPreviewScan(ctx, &query, &stream); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want canceled request", err)
		}
		if query.Query != "normal-query" {
			t.Fatalf("canceled preview switched to %q", query.Query)
		}
	})
}

func TestChooseGroupPreviewScanKeepsNoneligiblePlansOnCanonicalQuery(t *testing.T) {
	engine, query, stream := groupPreviewScanFixture()
	query.PreviewGroupScan = nil
	called := false
	engine.previewExplainQuery = func(context.Context, string, map[string]any) (arango.ExplainResult, error) {
		called = true
		return arango.ExplainResult{}, nil
	}
	if err := engine.chooseGroupPreviewScan(context.Background(), &query, &stream); err != nil {
		t.Fatal(err)
	}
	if called || query.Query != "normal-query" {
		t.Fatalf("noneligible query performed assessment or changed query: called=%t query=%q", called, query.Query)
	}
}

func groupPreviewScanFixture() (*Engine, compiler.CompiledQuery, OutputStream) {
	return &Engine{}, compiler.CompiledQuery{
			Query: "normal-query", BindVars: map[string]any{"scan": "normal"},
			PreviewGroupScan: &compiler.PreviewGroupScanSpec{
				Collection: "Specimen", SequentialQuery: "sequential-query",
				SequentialBindVars: map[string]any{"scan": "sequential"},
				ScopeCountQuery:    "scope-count-query", ScopeCountBindVars: map[string]any{"scope": true},
			},
		}, OutputStream{query: "normal-query", bindVars: map[string]any{"scan": "normal"}, stream: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return nil
		}}
}

func nonCoveringGroupExplain(context.Context, string, map[string]any) (arango.ExplainResult, error) {
	covering := false
	outputCovering := false
	return arango.ExplainResult{Plan: &arango.ExplainPlan{Nodes: []arango.ExplainNode{{
		Type: "IndexNode", Collection: "Specimen", IndexCoversProjections: &covering, IndexCoversOutProjections: &outputCovering,
	}}}}, nil
}
