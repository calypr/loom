package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/store/arango"
)

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
