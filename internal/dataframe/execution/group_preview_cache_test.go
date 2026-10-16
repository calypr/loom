package execution

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func newGroupPreviewCacheTestEngine(t *testing.T, rows QueryRows, revision func(context.Context, string) (string, error)) (*Engine, Resolved) {
	t.Helper()
	engine, err := New(Config{
		Registry: invalidRecipeRegistry{},
		ScopeDigest: func(recipe.RuntimeBindings) string {
			return "cache-test-scope"
		},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return nil
		},
		PreviewQueryRows:          rows,
		PreviewCollectionRevision: revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved := compileGroupPreviewCacheTestOutput(t, engine, recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "G1"})
	return engine, resolved
}

func compileGroupPreviewCacheTestOutput(t *testing.T, engine *Engine, bindings recipe.RuntimeBindings) Resolved {
	t.Helper()
	resolved, err := engine.CompileResolvedBundle(context.Background(), recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "group_preview_cache",
		TranslationVersion:  "test",
		Outputs:             []recipe.Output{executionGroupPreviewOutput()},
	}, bindings)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func groupPreviewCacheRows() QueryRows {
	return func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		for _, row := range []map[string]any{
			{"status": "final", "rows": float64(2)},
			{"status": "preliminary", "rows": float64(1)},
		} {
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	}
}

func TestGroupPreviewRowsCacheReusesRawRowsAndSeparatesRevisionBindingsAndLimit(t *testing.T) {
	queryCalls := 0
	currentRevision := "revision-1"
	engine, resolved := newGroupPreviewCacheTestEngine(t, func(ctx context.Context, query string, batch int, bindVars map[string]any, visit func(map[string]any) error) error {
		queryCalls++
		return groupPreviewCacheRows()(ctx, query, batch, bindVars, visit)
	}, func(context.Context, string) (string, error) {
		return currentRevision, nil
	})
	preview := func(resolved Resolved, limit int) error {
		_, err := engine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "group_preview", Limit: limit}, func(map[string]any) error {
			return nil
		})
		return err
	}

	if err := preview(resolved, 1); err != nil {
		t.Fatal(err)
	}
	if err := preview(resolved, 1); err != nil {
		t.Fatalf("identical preview cache hit: %v", err)
	}
	if queryCalls != 1 {
		t.Fatalf("identical preview query calls=%d, want 1", queryCalls)
	}

	currentRevision = "revision-2"
	if err := preview(resolved, 1); err != nil {
		t.Fatalf("preview after source revision change: %v", err)
	}
	if queryCalls != 2 {
		t.Fatalf("revision change query calls=%d, want 2", queryCalls)
	}

	restricted := compileGroupPreviewCacheTestOutput(t, engine, recipe.RuntimeBindings{
		Project: "P1", DatasetGeneration: "G1", AuthScopeMode: authscope.ReadScopeRestricted,
		AuthResourcePaths: []string{"Observation/allowed"},
	})
	if err := preview(restricted, 1); err != nil {
		t.Fatalf("preview under a different authorization scope: %v", err)
	}
	if err := preview(resolved, 2); err != nil {
		t.Fatalf("preview with a different row limit: %v", err)
	}
	if queryCalls != 4 {
		t.Fatalf("revision/auth/limit-separated query calls=%d, want 4", queryCalls)
	}
}

func TestGroupPreviewRowsCacheOnlyStoresSuccessfulStableSourceReads(t *testing.T) {
	t.Run("revision changes during query", func(t *testing.T) {
		queryCalls := 0
		revisions := []string{"before", "after", "after", "after"}
		revisionCall := 0
		engine, resolved := newGroupPreviewCacheTestEngine(t, func(ctx context.Context, query string, batch int, bindVars map[string]any, visit func(map[string]any) error) error {
			queryCalls++
			return groupPreviewCacheRows()(ctx, query, batch, bindVars, visit)
		}, func(context.Context, string) (string, error) {
			value := revisions[revisionCall]
			revisionCall++
			return value, nil
		})
		for range 2 {
			if _, err := engine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "group_preview", Limit: 1}, func(map[string]any) error { return nil }); err != nil {
				t.Fatal(err)
			}
		}
		if queryCalls != 2 {
			t.Fatalf("revision-raced preview query calls=%d, want 2", queryCalls)
		}
	})

	t.Run("revision read failure bypasses lookup and insertion", func(t *testing.T) {
		queryCalls := 0
		revisionCalls := 0
		engine, resolved := newGroupPreviewCacheTestEngine(t, func(ctx context.Context, query string, batch int, bindVars map[string]any, visit func(map[string]any) error) error {
			queryCalls++
			return groupPreviewCacheRows()(ctx, query, batch, bindVars, visit)
		}, func(context.Context, string) (string, error) {
			revisionCalls++
			if revisionCalls == 2 {
				return "", errors.New("revision unavailable")
			}
			return "revision-1", nil
		})
		for range 3 {
			if _, err := engine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "group_preview", Limit: 1}, func(map[string]any) error { return nil }); err != nil {
				t.Fatal(err)
			}
		}
		if queryCalls != 2 {
			t.Fatalf("failed revision read query calls=%d, want 2 then one hit", queryCalls)
		}
	})

	for _, test := range []struct {
		name       string
		queryError bool
		visitError bool
	}{
		{name: "query failure", queryError: true},
		{name: "visitor failure", visitError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			queryCalls := 0
			engine, resolved := newGroupPreviewCacheTestEngine(t, func(ctx context.Context, query string, batch int, bindVars map[string]any, visit func(map[string]any) error) error {
				queryCalls++
				if test.queryError && queryCalls == 1 {
					return errors.New("query failed")
				}
				return groupPreviewCacheRows()(ctx, query, batch, bindVars, visit)
			}, func(context.Context, string) (string, error) { return "revision-1", nil })
			visitor := func(map[string]any) error {
				if test.visitError && queryCalls == 1 {
					return errors.New("visitor failed")
				}
				return nil
			}
			if _, err := engine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "group_preview", Limit: 1}, visitor); err == nil {
				t.Fatal("first preview unexpectedly succeeded")
			}
			if _, err := engine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "group_preview", Limit: 1}, func(map[string]any) error { return nil }); err != nil {
				t.Fatalf("retry after failed preview: %v", err)
			}
			if queryCalls != 2 {
				t.Fatalf("failed preview was cached: query calls=%d, want 2", queryCalls)
			}
		})
	}

	t.Run("canceled during post-query revision read", func(t *testing.T) {
		queryCalls := 0
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		revisionCalls := 0
		engine, resolved := newGroupPreviewCacheTestEngine(t, func(ctx context.Context, query string, batch int, bindVars map[string]any, visit func(map[string]any) error) error {
			queryCalls++
			return groupPreviewCacheRows()(ctx, query, batch, bindVars, visit)
		}, func(context.Context, string) (string, error) {
			revisionCalls++
			if revisionCalls == 2 {
				cancel()
			}
			return "revision-1", nil
		})
		if _, err := engine.PreviewOutput(ctx, resolved, PreviewRequest{Output: "group_preview", Limit: 1}, func(map[string]any) error { return nil }); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled post-query revision error=%v", err)
		}
		if _, err := engine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "group_preview", Limit: 1}, func(map[string]any) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if queryCalls != 2 {
			t.Fatalf("canceled preview was cached: query calls=%d, want 2", queryCalls)
		}
	})
}

func TestGroupPreviewRowsCacheReplaysDefensiveRawRowsAndOnlyEligiblePlans(t *testing.T) {
	queryCalls := 0
	revisionCalls := 0
	engine, resolved := newGroupPreviewCacheTestEngine(t, func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		queryCalls++
		return visit(map[string]any{
			"status": map[string]any{"labels": []any{"original"}},
			"rows":   float64(1),
		})
	}, func(context.Context, string) (string, error) {
		revisionCalls++
		return "revision-1", nil
	})
	mutatingVisitor := func(row map[string]any) error {
		status := row["status"].(map[string]any)
		status["labels"].([]any)[0] = "mutated"
		return nil
	}
	if _, err := engine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "group_preview", Limit: 2}, mutatingVisitor); err != nil {
		t.Fatal(err)
	}
	var replay map[string]any
	if _, err := engine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "group_preview", Limit: 2}, func(row map[string]any) error {
		replay = row
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if queryCalls != 1 || revisionCalls != 3 {
		t.Fatalf("query calls=%d revision calls=%d, want one query and three revision reads", queryCalls, revisionCalls)
	}
	if got := replay["status"].(map[string]any)["labels"].([]any)[0]; got != "original" {
		t.Fatalf("visitor mutation changed cached source row: %v", got)
	}

	ordinaryEngine := testEngine(groupPreviewCacheRows())
	ordinaryResolved, err := ordinaryEngine.CompileResolvedBundle(context.Background(), testResolvedBundle([]string{}), recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "G1"})
	if err != nil {
		t.Fatal(err)
	}
	ordinaryEngine.previewCollectionRevision = func(context.Context, string) (string, error) {
		t.Fatal("non-Group preview requested a source revision")
		return "", nil
	}
	for range 2 {
		if _, err := ordinaryEngine.PreviewOutput(context.Background(), ordinaryResolved, PreviewRequest{Output: "Patient", Limit: 1}, func(map[string]any) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGroupPreviewRowsCacheEnforcesEntryAndTotalByteBounds(t *testing.T) {
	cache := &groupPreviewRowsCache{}
	oversizedKey := groupPreviewRowsKey{query: "too-large"}
	cache.put(oversizedKey, [][]byte{[]byte(strings.Repeat("x", groupPreviewCacheMaxEntryBytes))})
	if _, ok := cache.get(oversizedKey); ok {
		t.Fatal("oversized entry was retained")
	}

	keys := make([]groupPreviewRowsKey, 10)
	row := []byte(strings.Repeat("x", 900<<10))
	for index := range keys {
		keys[index] = groupPreviewRowsKey{query: fmt.Sprintf("query-%d", index), revision: "r"}
		cache.put(keys[index], [][]byte{row})
	}
	if len(cache.entries) > groupPreviewCacheMaxEntries || cache.bytes > groupPreviewCacheMaxBytes {
		t.Fatalf("cache exceeded bounds: entries=%d bytes=%d", len(cache.entries), cache.bytes)
	}
	if _, ok := cache.get(keys[0]); ok {
		t.Fatal("oldest entry was not evicted to honor the total byte limit")
	}
	if _, ok := cache.get(keys[len(keys)-1]); !ok {
		t.Fatal("most recent bounded entry was evicted")
	}
}

func TestGroupPreviewRowsCacheKeyUsesExactQueryBindingsCollectionRevisionAndLimit(t *testing.T) {
	base, _, ok := newGroupPreviewRowsKey("RETURN @value", map[string]any{"value": "x"}, "Observation", "rev-1", 25)
	if !ok {
		t.Fatal("base key was rejected")
	}
	variants := []groupPreviewRowsKey{
		{query: "RETURN @other", bindVars: base.bindVars, collection: base.collection, revision: base.revision, limit: base.limit},
		{query: base.query, bindVars: `{"value":"y"}`, collection: base.collection, revision: base.revision, limit: base.limit},
		{query: base.query, bindVars: base.bindVars, collection: "Patient", revision: base.revision, limit: base.limit},
		{query: base.query, bindVars: base.bindVars, collection: base.collection, revision: "rev-2", limit: base.limit},
		{query: base.query, bindVars: base.bindVars, collection: base.collection, revision: base.revision, limit: 50},
	}
	for _, variant := range variants {
		if reflect.DeepEqual(base, variant) {
			t.Fatalf("cache key failed to separate variant %#v", variant)
		}
	}
}
