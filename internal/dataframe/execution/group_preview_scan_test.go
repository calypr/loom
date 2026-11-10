package execution

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
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
	originalQuery := commandText(t, query.Command)
	originalBinds := commandBinds(t, query.Command)
	largeFields := append(append([]string(nil), required.Fields...), "payload.extra", "resourceType")
	smallFields := []string{"project", "dataset_generation", "resourceType"}
	smallFields = append(smallFields, required.Fields[2:]...)
	indexes := []PreviewCollectionIndex{
		{Collection: required.Collection, Name: "loom_pivot_preview_existing_larger", Fields: largeFields},
		{Collection: required.Collection, Name: "loom_pivot_preview_existing_smaller", Fields: smallFields},
	}
	indexCalls, explainCalls := 0, 0
	engine.previewCoveringIndexes = func(_ context.Context, collection string) ([]PreviewCollectionIndex, error) {
		indexCalls++
		if collection != required.Collection {
			t.Fatalf("index collection = %q, want %q", collection, required.Collection)
		}
		return indexes, nil
	}
	engine.previewCollectionScans = func(_ context.Context, command command.CompiledCommand, collection string) (PreviewCollectionScanEvidence, error) {
		explainCalls++
		if collection != required.Collection {
			t.Fatalf("scan collection = %q, want %q", collection, required.Collection)
		}
		queryText, bindVars, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return PreviewCollectionScanEvidence{}, dispatchErr
		}
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
		return coveringGroupScans(indexes[1].Name), nil
	}
	engine.previewCollectionCount = func(context.Context, string) (int64, error) {
		t.Fatal("covering candidate should skip the sequential-scan assessment")
		return 0, nil
	}
	stream.sourceRows = func(_ context.Context, request RowsRequest, _ RowVisitor) error {
		command := request.Command
		_, _, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}

		t.Fatal("covering candidate should skip the scoped source count")
		return nil
	}
	if err := engine.chooseGroupPreviewScan(context.Background(), &query, &stream); err != nil {
		t.Fatal(err)
	}
	if indexCalls != 1 || explainCalls != 1 {
		t.Fatalf("metadata/explain calls = %d/%d, want one each", indexCalls, explainCalls)
	}
	selectedText, selectedBinds := aqlView(t, query.Command)
	streamText, streamBinds := aqlView(t, stream.command)
	if selectedText == originalQuery || selectedText != streamText || query.PreviewCoveringIndex.Name != indexes[1].Name ||
		!reflect.DeepEqual(query.PreviewCoveringIndex.Fields, smallFields) || !reflect.DeepEqual(selectedBinds, originalBinds) ||
		!reflect.DeepEqual(selectedBinds, streamBinds) {
		t.Fatalf("selected compatible query was not applied consistently: query=%q stream=%q spec=%+v", selectedText, streamText, query.PreviewCoveringIndex)
	}
}

func TestChooseGroupPreviewScanKeepsSequentialFallbackWhenNoCompatibleIndexExists(t *testing.T) {
	engine, query, stream := compiledGroupPreviewScanFixture(t, recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "G1"})
	originalQuery := commandText(t, query.Command)
	required := query.PreviewCoveringIndex
	engine.previewCoveringIndexes = func(context.Context, string) ([]PreviewCollectionIndex, error) {
		return []PreviewCollectionIndex{{
			Collection: required.Collection, Name: "loom_pivot_preview_missing_auth",
			Fields: []string{"project", "dataset_generation", "payload.status"},
		}}, nil
	}
	explainCalls, scopeCountCalls := 0, 0
	engine.previewCollectionScans = func(_ context.Context, command command.CompiledCommand, collection string) (PreviewCollectionScanEvidence, error) {
		explainCalls++
		if collection != required.Collection {
			t.Fatalf("scan collection = %q, want %q", collection, required.Collection)
		}
		queryText, _, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return PreviewCollectionScanEvidence{}, dispatchErr
		}
		if queryText != originalQuery {
			t.Fatalf("incompatible index changed the canonical query:\n%s", queryText)
		}
		return notCoveringGroupScans(), nil
	}
	engine.previewCollectionCount = func(context.Context, string) (int64, error) { return 1000, nil }
	stream.sourceRows = func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		command := request.Command
		batchSize := request.BatchSize
		queryText, _, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}

		scopeCountCalls++
		if !reflect.DeepEqual(command, query.PreviewGroupScan.ScopeCount) || batchSize != 1 {
			t.Fatalf("scope-count query = %q batch=%d", queryText, batchSize)
		}
		return visit(map[string]any{"count": float64(950)})
	}
	if err := engine.chooseGroupPreviewScan(context.Background(), &query, &stream); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(query.Command, query.PreviewGroupScan.Sequential) || explainCalls != 1 || scopeCountCalls != 1 {
		t.Fatalf("sequential fallback state: query=%q explains=%d scope counts=%d", commandText(t, query.Command), explainCalls, scopeCountCalls)
	}
}

func TestChooseGroupPreviewScanRequiresExplainToProveCoveringIndex(t *testing.T) {
	// Each candidate-plan evidence shape below certifies nothing, so the
	// canonical query must survive. The first three mirror vendor coverage
	// flag shapes (mixed pair, inverted mixed pair, missing pair) that the
	// adapter decodes to Unknown coverage; the last two pin the policy
	// itself: a covering node must name the index and be the only one.
	for _, test := range []struct {
		name      string
		candidate PreviewCollectionScanEvidence
	}{
		{name: "projection uncovered", candidate: unknownCoverageGroupScans()},
		{name: "output uncovered", candidate: unknownCoverageGroupScans()},
		{name: "coverage unknown", candidate: unknownCoverageGroupScans()},
		{name: "covering wrong index", candidate: coveringGroupScans("loom_pivot_preview_something_else")},
		{name: "covering twice", candidate: PreviewCollectionScanEvidence{IndexNodes: []PreviewIndexNodeEvidence{
			{IndexNames: []string{"loom_pivot_preview_unproven"}, Coverage: PreviewIndexCoverageCovering},
			{IndexNames: []string{"loom_pivot_preview_unproven"}, Coverage: PreviewIndexCoverageCovering},
		}}},
		{name: "enumeration with covering", candidate: PreviewCollectionScanEvidence{
			FullCollectionEnumeration: true,
			IndexNodes: []PreviewIndexNodeEvidence{
				{IndexNames: []string{"loom_pivot_preview_unproven"}, Coverage: PreviewIndexCoverageCovering},
			},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine, query, stream := compiledGroupPreviewScanFixture(t, recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "G1"})
			originalQuery := commandText(t, query.Command)
			candidate := PreviewCollectionIndex{
				Collection: query.PreviewCoveringIndex.Collection, Name: "loom_pivot_preview_unproven", Fields: append([]string(nil), query.PreviewCoveringIndex.Fields...),
			}
			engine.previewCoveringIndexes = func(context.Context, string) ([]PreviewCollectionIndex, error) {
				return []PreviewCollectionIndex{candidate}, nil
			}
			explainCalls := 0
			engine.previewCollectionScans = func(_ context.Context, command command.CompiledCommand, collection string) (PreviewCollectionScanEvidence, error) {
				explainCalls++
				if collection != query.PreviewGroupScan.Collection && collection != query.PreviewCoveringIndex.Collection {
					t.Fatalf("scan collection = %q", collection)
				}
				queryText, _, dispatchErr := command.AQLParameters()
				if dispatchErr != nil {
					return PreviewCollectionScanEvidence{}, dispatchErr
				}
				if queryText == originalQuery {
					return notCoveringGroupScans(), nil
				}
				return test.candidate, nil
			}
			engine.previewCollectionCount = func(context.Context, string) (int64, error) { return 1000, nil }
			stream.sourceRows = func(_ context.Context, request RowsRequest, visit RowVisitor) error {
				command := request.Command
				_, _, dispatchErr := command.AQLParameters()
				if dispatchErr != nil {
					return dispatchErr
				}

				return visit(map[string]any{"count": float64(500)})
			}
			if err := engine.chooseGroupPreviewScan(context.Background(), &query, &stream); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(query.Command, stream.command) || commandText(t, query.Command) != originalQuery || commandText(t, stream.command) != originalQuery || explainCalls != 2 {
				t.Fatalf("unproven candidate changed canonical query: query=%q stream=%q explains=%d", commandText(t, query.Command), commandText(t, stream.command), explainCalls)
			}
		})
	}
}

func TestChooseGroupPreviewScanDoesNotReplaceSequentialChoiceWithProjectionIndex(t *testing.T) {
	engine, _, query, stream := semanticGroupProjectionExecutionFixture(t, nil)
	query.PreviewGroupScan = &compiler.PreviewGroupScanSpec{
		Collection: query.PreviewGroupProjection.Collection,
		Sequential: mustAQLCommand("sequential-query", map[string]any{"scan": "sequential"}),
		ScopeCount: query.PreviewGroupProjection.ScopeCount,
	}
	ordinaryQuery := commandText(t, query.Command)
	indexCalls := 0
	engine.previewCoveringIndexes = func(context.Context, string) ([]PreviewCollectionIndex, error) {
		indexCalls++
		return []PreviewCollectionIndex{{
			Collection: query.PreviewGroupProjection.Collection,
			Name:       "loom_pivot_preview_projection",
			Fields:     []string{"project", "dataset_generation", "auth_resource_path", "payload.code.coding"},
		}}, nil
	}
	engine.previewCollectionScans = func(_ context.Context, command command.CompiledCommand, collection string) (PreviewCollectionScanEvidence, error) {
		if collection != query.PreviewGroupProjection.Collection {
			t.Fatalf("scan collection = %q, want %q", collection, query.PreviewGroupProjection.Collection)
		}
		queryText, _, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return PreviewCollectionScanEvidence{}, dispatchErr
		}
		if queryText != ordinaryQuery {
			t.Fatalf("initial Group scan explain=%q, want original plan", queryText)
		}
		return notCoveringGroupScans(), nil
	}
	engine.previewCollectionCount = func(context.Context, string) (int64, error) { return 1000, nil }
	stream.sourceRows = func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		command := request.Command
		queryText, _, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}

		if !reflect.DeepEqual(command, query.PreviewGroupProjection.ScopeCount) {
			t.Fatalf("unexpected query %q", queryText)
		}
		return visit(map[string]any{"count": int64(950)})
	}
	engine.previewCollectionRevision = func(context.Context, string) (string, error) {
		t.Fatal("sequential scan choice must skip projection assessment")
		return "", nil
	}
	if err := engine.chooseGroupPreviewScan(context.Background(), &query, &stream); err != nil {
		t.Fatal(err)
	}
	if indexCalls != 0 || commandText(t, query.Command) != "sequential-query" || commandText(t, query.Command) == ordinaryQuery {
		t.Fatalf("sequential plan was overridden: index lookups=%d query=%q", indexCalls, commandText(t, query.Command))
	}
}

func TestCompatibleGroupProjectionIndexesRequiresOwnedScopeAndProjectionCoverage(t *testing.T) {
	required := &compiler.PreviewGroupProjectionIndexSpec{
		RequiredFields: []string{"project", "dataset_generation", "auth_resource_path"},
		ProjectionPath: "payload.code.coding",
	}
	baseFields := []string{"project", "dataset_generation", "auth_resource_path"}
	withProjection := append(append([]string(nil), baseFields...), required.ProjectionPath)
	indexes := []PreviewCollectionIndex{
		{Collection: "Observation", Name: "other_preview_owned", Fields: withProjection},
		{Collection: "Observation", Name: "loom_pivot_preview_missing_scope", Fields: []string{"project", "dataset_generation", required.ProjectionPath}},
		{Collection: "Patient", Name: "loom_pivot_preview_wrong_collection", Fields: withProjection},
		{Collection: "Observation", Name: "loom_pivot_preview_missing_projection", Fields: baseFields},
		{Collection: "Observation", Name: "loom_pivot_preview_stored_projection", Fields: baseFields, StoredValues: []string{required.ProjectionPath}},
		{Collection: "Observation", Name: "loom_pivot_preview_key_projection", Fields: withProjection},
	}
	got := compatibleGroupProjectionIndexes("Observation", required, indexes)
	if len(got) != 2 || got[0].Name != "loom_pivot_preview_key_projection" || got[1].Name != "loom_pivot_preview_stored_projection" {
		t.Fatalf("compatible projection indexes=%+v, want only owned fully covered definitions", got)
	}
}

func TestPreviewOutputReusesStoredValuesGroupIndexWithoutPrewarmingIt(t *testing.T) {
	queryCalls, prepareCalls := 0, 0
	index := PreviewCollectionIndex{
		Collection:   "Observation",
		Name:         "loom_pivot_preview_existing_stored_values",
		Fields:       []string{"project", "dataset_generation", "auth_resource_path", "_key", "payload.id", "payload.status"},
		StoredValues: []string{"payload.valueQuantity"},
	}
	engine, err := New(Config{
		CompileTarget: compiler.ArangoTarget(),
		Registry:      invalidRecipeRegistry{},
		ScopeDigest:   func(recipe.RuntimeBindings) string { return "group-preview-existing-index" },
		SourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
			command := request.Command
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}
			return nil
		},
		PreviewSourceRows: func(ctx context.Context, request RowsRequest, visit RowVisitor) error {
			command := request.Command
			batch := request.BatchSize
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			queryCalls++
			return groupPreviewCacheRows()(ctx, RowsRequest{Command: command, BatchSize: batch}, visit)
		},
		PreparePreviewIndex: func(context.Context, compiler.PreviewCoveringIndexSpec) error {
			prepareCalls++
			return errors.New("reused indexes must not be prepared")
		},
		PreviewCoveringIndexes: func(context.Context, string) ([]PreviewCollectionIndex, error) {
			return []PreviewCollectionIndex{index}, nil
		},
		PreviewCollectionRevision: func(context.Context, string) (string, error) { return "revision-1", nil },
		PreviewCollectionScans: func(_ context.Context, command command.CompiledCommand, collection string) (PreviewCollectionScanEvidence, error) {
			if collection != index.Collection {
				return PreviewCollectionScanEvidence{}, nil
			}
			queryText, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return PreviewCollectionScanEvidence{}, dispatchErr
			}
			if !strings.Contains(queryText, index.Name) {
				return PreviewCollectionScanEvidence{}, errors.New("candidate index hint was not rendered")
			}
			return coveringGroupScans(index.Name), nil
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
		CompileTarget: compiler.ArangoTarget(),
		Registry:      invalidRecipeRegistry{},
		ScopeDigest:   func(recipe.RuntimeBindings) string { return "group-preview-scan-test" },
		SourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
			command := request.Command
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}
			return nil
		},
		PreviewSourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
			command := request.Command
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved := compileGroupPreviewCacheTestOutput(t, engine, bindings)
	stream, query, err := engine.streamForOutput(context.Background(), resolved, "group_preview", 25)
	if err != nil {
		t.Fatal(err)
	}
	return engine, query, stream
}

// Neutral scan-evidence builders. The collection a builder used to carry is
// now asserted at the capability call: evidence is already filtered to the
// queried collection by the adapter that produced it.

func coveringGroupScans(indexName string) PreviewCollectionScanEvidence {
	return PreviewCollectionScanEvidence{IndexNodes: []PreviewIndexNodeEvidence{
		{IndexNames: []string{indexName}, Coverage: PreviewIndexCoverageCovering},
	}}
}

func notCoveringGroupScans() PreviewCollectionScanEvidence {
	return PreviewCollectionScanEvidence{IndexNodes: []PreviewIndexNodeEvidence{
		{Coverage: PreviewIndexCoverageNotCovering},
	}}
}

func unknownCoverageGroupScans() PreviewCollectionScanEvidence {
	return PreviewCollectionScanEvidence{IndexNodes: []PreviewIndexNodeEvidence{
		{Coverage: PreviewIndexCoverageUnknown},
	}}
}

func TestChooseGroupPreviewScanUsesSequentialOnlyForBroadNonCoveringIndex(t *testing.T) {
	explainCalls, scopeCountCalls, collectionCountCalls := 0, 0, 0
	engine, query, stream := groupPreviewScanFixture()
	engine.previewCollectionScans = func(_ context.Context, command command.CompiledCommand, collection string) (PreviewCollectionScanEvidence, error) {
		explainCalls++
		if collection != "Specimen" {
			t.Fatalf("scan collection = %q, want Specimen", collection)
		}
		queryText, _, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return PreviewCollectionScanEvidence{}, dispatchErr
		}
		if queryText != "normal-query" {
			t.Fatalf("explain query = %q, want original query", queryText)
		}
		return notCoveringGroupScans(), nil
	}
	engine.previewCollectionCount = func(_ context.Context, collection string) (int64, error) {
		collectionCountCalls++
		if collection != "Specimen" {
			t.Fatalf("collection = %q, want Specimen", collection)
		}
		return 1000, nil
	}
	stream.sourceRows = func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		command := request.Command
		batchSize := request.BatchSize
		queryText, _, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}

		scopeCountCalls++
		if queryText != "scope-count-query" || batchSize != 1 {
			t.Fatalf("scope query = %q batch size = %d", queryText, batchSize)
		}
		return visit(map[string]any{"count": float64(950)})
	}

	before, _, ok := newGroupPreviewRowsKey(query.Command, "Specimen", "revision-1", 25)
	if !ok {
		t.Fatal("original preview cache key was not created")
	}
	if err := engine.chooseGroupPreviewScan(context.Background(), &query, &stream); err != nil {
		t.Fatal(err)
	}
	after, _, ok := newGroupPreviewRowsKey(query.Command, "Specimen", "revision-1", 25)
	if !ok || before == after {
		t.Fatal("selected AQL must produce a distinct cache key")
	}
	streamText, streamBinds := aqlView(t, stream.command)
	if commandText(t, query.Command) != "sequential-query" || !reflect.DeepEqual(stream.command, query.Command) ||
		commandBinds(t, query.Command)["scan"] != "sequential" || streamBinds["scan"] != "sequential" {
		t.Fatalf("sequential query was not applied consistently: query=%q stream=%q binds=%v streamBinds=%v", commandText(t, query.Command), streamText, commandBinds(t, query.Command), streamBinds)
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
			// The first four rows mirror vendor coverage flag shapes (true+false,
			// false+true, missing pair, one-sided pair) that the adapter decodes
			// to Unknown coverage; unknown-plan/scan-error keep the advisory
			// failures explicit.
			name: "covering index",
			setup: func(engine *Engine) {
				engine.previewCollectionScans = func(context.Context, command.CompiledCommand, string) (PreviewCollectionScanEvidence, error) {
					return unknownCoverageGroupScans(), nil
				}
			},
		},
		{
			name: "covering output projections",
			setup: func(engine *Engine) {
				engine.previewCollectionScans = func(context.Context, command.CompiledCommand, string) (PreviewCollectionScanEvidence, error) {
					return unknownCoverageGroupScans(), nil
				}
			},
		},
		{
			name: "unknown coverage",
			setup: func(engine *Engine) {
				engine.previewCollectionScans = func(context.Context, command.CompiledCommand, string) (PreviewCollectionScanEvidence, error) {
					return unknownCoverageGroupScans(), nil
				}
			},
		},
		{
			name: "unknown output coverage",
			setup: func(engine *Engine) {
				engine.previewCollectionScans = func(context.Context, command.CompiledCommand, string) (PreviewCollectionScanEvidence, error) {
					return unknownCoverageGroupScans(), nil
				}
			},
		},
		{
			name: "unknown plan",
			setup: func(engine *Engine) {
				engine.previewCollectionScans = func(context.Context, command.CompiledCommand, string) (PreviewCollectionScanEvidence, error) {
					return PreviewCollectionScanEvidence{}, nil
				}
			},
		},
		{
			name: "explain error",
			setup: func(engine *Engine) {
				engine.previewCollectionScans = func(context.Context, command.CompiledCommand, string) (PreviewCollectionScanEvidence, error) {
					return PreviewCollectionScanEvidence{}, errors.New("unavailable")
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
			// A broad-scan certification must never be inferred: an
			// enumeration fact vetoes every decision, and the sequential
			// fallback needs exactly one certified not-covering node.
			name: "enumeration with not covering",
			setup: func(engine *Engine) {
				engine.previewCollectionScans = func(context.Context, command.CompiledCommand, string) (PreviewCollectionScanEvidence, error) {
					return PreviewCollectionScanEvidence{
						FullCollectionEnumeration: true,
						IndexNodes:                []PreviewIndexNodeEvidence{{Coverage: PreviewIndexCoverageNotCovering}},
					}, nil
				}
			},
		},
		{
			name: "multiple not covering nodes",
			setup: func(engine *Engine) {
				engine.previewCollectionScans = func(context.Context, command.CompiledCommand, string) (PreviewCollectionScanEvidence, error) {
					return PreviewCollectionScanEvidence{IndexNodes: []PreviewIndexNodeEvidence{
						{Coverage: PreviewIndexCoverageNotCovering},
						{Coverage: PreviewIndexCoverageNotCovering},
					}}, nil
				}
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
			engine.previewCollectionScans = nonCoveringGroupScansFn
			engine.previewCollectionCount = func(context.Context, string) (int64, error) { return 1000, nil }
			stream.sourceRows = func(_ context.Context, request RowsRequest, visit RowVisitor) error {
				command := request.Command
				_, _, dispatchErr := command.AQLParameters()
				if dispatchErr != nil {
					return dispatchErr
				}

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
			if got := commandText(t, query.Command) == "sequential-query"; got != test.wantSequential {
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
		engine.previewCollectionScans = func(context.Context, command.CompiledCommand, string) (PreviewCollectionScanEvidence, error) {
			cancel()
			return PreviewCollectionScanEvidence{}, context.Canceled
		}
		if err := engine.chooseGroupPreviewScan(ctx, &query, &stream); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want canceled request", err)
		}
		if commandText(t, query.Command) != "normal-query" {
			t.Fatalf("canceled preview switched to %q", commandText(t, query.Command))
		}
	})
	t.Run("during scoped count", func(t *testing.T) {
		engine, query, stream := groupPreviewScanFixture()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		engine.previewCollectionScans = nonCoveringGroupScansFn
		engine.previewCollectionCount = func(context.Context, string) (int64, error) {
			t.Fatal("collection count called after scoped-count cancellation")
			return 0, nil
		}
		stream.sourceRows = func(_ context.Context, request RowsRequest, _ RowVisitor) error {
			command := request.Command
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			cancel()
			return nil
		}
		if err := engine.chooseGroupPreviewScan(ctx, &query, &stream); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want canceled request", err)
		}
		if commandText(t, query.Command) != "normal-query" {
			t.Fatalf("canceled preview switched to %q", commandText(t, query.Command))
		}
	})
}

func TestChooseGroupPreviewScanKeepsNoneligiblePlansOnCanonicalQuery(t *testing.T) {
	engine, query, stream := groupPreviewScanFixture()
	query.PreviewGroupScan = nil
	called := false
	engine.previewCollectionScans = func(context.Context, command.CompiledCommand, string) (PreviewCollectionScanEvidence, error) {
		called = true
		return PreviewCollectionScanEvidence{}, nil
	}
	if err := engine.chooseGroupPreviewScan(context.Background(), &query, &stream); err != nil {
		t.Fatal(err)
	}
	if called || commandText(t, query.Command) != "normal-query" {
		t.Fatalf("noneligible query performed assessment or changed query: called=%t query=%q", called, commandText(t, query.Command))
	}
}

func groupPreviewScanFixture() (*Engine, compiler.CompiledQuery, OutputStream) {
	return &Engine{compileTarget: compiler.ArangoTarget()}, compiler.CompiledQuery{
			Command: mustAQLCommand("normal-query", map[string]any{"scan": "normal"}),
			PreviewGroupScan: &compiler.PreviewGroupScanSpec{
				Collection: "Specimen", Sequential: mustAQLCommand("sequential-query", map[string]any{"scan": "sequential"}),
				ScopeCount: mustAQLCommand("scope-count-query", map[string]any{"scope": true}),
			},
		}, OutputStream{command: mustAQLCommand("normal-query", map[string]any{"scan": "normal"}), sourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
			command := request.Command
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			return nil
		}}
}

func nonCoveringGroupScansFn(context.Context, command.CompiledCommand, string) (PreviewCollectionScanEvidence, error) {
	return notCoveringGroupScans(), nil
}

func semanticGroupProjectionExecutionFixture(t *testing.T, previewRows SourceRows) (*Engine, Resolved, compiler.CompiledQuery, OutputStream) {
	t.Helper()
	engine, err := New(Config{
		CompileTarget: compiler.ArangoTarget(),
		Registry:      invalidRecipeRegistry{},
		ScopeDigest:   func(recipe.RuntimeBindings) string { return "group-projection-runtime-test" },
		SourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
			command := request.Command
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}
			return nil
		},
		PreviewSourceRows: previewRows,
	})
	if err != nil {
		t.Fatal(err)
	}
	bindings := recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "G1"}
	resolved, err := engine.CompileResolvedBundle(context.Background(), recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "semantic_group_projection_runtime",
		TranslationVersion:  "test",
		Outputs:             []recipe.Output{semanticGroupProjectionExecutionOutput()},
	}, bindings)
	if err != nil {
		t.Fatal(err)
	}
	stream, query, err := engine.streamForOutput(context.Background(), resolved, "semantic_group_projection", 600)
	if err != nil {
		t.Fatal(err)
	}
	if query.PreviewGroupProjection == nil {
		_, eligibilityErr := compiler.RenderPreviewGroupProjectionAssessment(query.PhysicalPlan, "", false)
		stageCount, ordinaryGroupKeys := 0, -1
		sequenceWindow := false
		if sequence := query.PhysicalPlan.StageSequence; sequence != nil {
			stageCount = len(sequence.Stages)
			sequenceWindow = sequence.PreviewSourceWindowByRootID
			if stageCount > 0 && sequence.Stages[0].Group != nil {
				ordinaryGroupKeys = len(sequence.Stages[0].Group.Keys)
			}
		}
		t.Fatalf("compiled direct semantic Group has no projection marker: assessment=%v sourceWindow=%t stages=%d ordinaryGroupKeys=%d operations=%d", eligibilityErr, sequenceWindow, stageCount, ordinaryGroupKeys, len(query.PhysicalPlan.Operations))
	}
	return engine, resolved, query, stream
}

func semanticGroupProjectionExecutionOutput() recipe.Output {
	return recipe.Output{
		Name: "semantic_group_projection", RootResourceType: "Observation", RowGrain: "resource",
		Fields: []recipe.Field{{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}}},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status", Type: "string"}},
			Steps: []recipe.ConstructionStep{{
				ID:     "group_semantic_category",
				Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "group_semantic_category",
					Keys: []recipe.ConstructionGroupKey{
						{OutputColumnID: "category_id", SemanticCategory: &recipe.ConstructionGroupSemanticCategory{
							Kind: "BINDING", BindingID: "test-binding", RuleVersion: "1", FamilyID: "test-family",
							SourceKind: "retained_vertex", System: "urn:example:test", ResourceType: "Observation",
							CodingPath: "code.coding[]",
						}},
					},
					Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
				}},
				Outputs: []recipe.StageColumn{
					{ID: "category_id", Name: "category", Type: "string", Nullable: true},
					{ID: "rows_id", Name: "rows", Type: "integer"},
				},
			}},
		},
	}
}

func configureSemanticGroupProjectionIndex(
	t *testing.T,
	engine *Engine,
	query compiler.CompiledQuery,
) (PreviewCollectionIndex, command.CompiledCommand, command.CompiledCommand) {
	t.Helper()
	if query.PreviewGroupProjection == nil || query.PreviewGroupProjection.ReadOnlyIndex == nil {
		t.Fatalf("semantic Group has no read-only projection index descriptor: %+v", query.PreviewGroupProjection)
	}
	descriptor := query.PreviewGroupProjection.ReadOnlyIndex
	indexFields := append([]string(nil), descriptor.RequiredFields...)
	indexFields = append(indexFields, descriptor.ProjectionPath)
	index := PreviewCollectionIndex{
		Collection: query.PreviewGroupProjection.Collection,
		Name:       "loom_pivot_preview_runtime_existing",
		Fields:     indexFields,
	}
	assessmentCommand, err := compiler.RenderPreviewGroupProjectionAssessment(query.PhysicalPlan, index.Name, false)
	if err != nil {
		t.Fatal(err)
	}
	factoredCommand, err := compiler.RenderPreviewGroupProjectionFactorization(query.PhysicalPlan, index.Name, false)
	if err != nil {
		t.Fatal(err)
	}
	engine.previewCoveringIndexes = func(_ context.Context, collection string) ([]PreviewCollectionIndex, error) {
		if collection != index.Collection {
			t.Fatalf("index collection=%q, want %q", collection, index.Collection)
		}
		return []PreviewCollectionIndex{index}, nil
	}
	engine.previewCollectionScans = func(_ context.Context, command command.CompiledCommand, collection string) (PreviewCollectionScanEvidence, error) {
		if collection != index.Collection {
			return PreviewCollectionScanEvidence{}, nil
		}
		factoredText, factoredBinds, dispatchErr := factoredCommand.AQLParameters()
		queryText, bindVars, commandDispatchErr := command.AQLParameters()
		if commandDispatchErr != nil {
			return PreviewCollectionScanEvidence{}, commandDispatchErr
		}
		if dispatchErr != nil || queryText != factoredText || !reflect.DeepEqual(bindVars, factoredBinds) {
			return PreviewCollectionScanEvidence{}, fmt.Errorf("candidate explain did not use exact factored query and binds")
		}
		return coveringGroupScans(index.Name), nil
	}
	return index, assessmentCommand, factoredCommand
}

func TestChooseGroupProjectionFactorizationAdmitsLowDiversityAndCachesDecision(t *testing.T) {
	engine, resolved, query, _ := semanticGroupProjectionExecutionFixture(t, nil)
	index, assessmentCommand, factoredCommand := configureSemanticGroupProjectionIndex(t, engine, query)
	engine.previewCollectionRevision = func(context.Context, string) (string, error) { return "revision-1", nil }
	decisionKey, _, keyOK := newGroupPreviewProjectionDecisionKey(query.Command,
		query.PreviewGroupProjection.Collection, "revision-1", groupProjectionIndexIdentity(index))
	if !keyOK {
		t.Fatal("could not construct exact projection decision key")
	}
	countCalls, sampleCalls, finalCalls := 0, 0, 0
	engine.previewSourceRows = func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		command := request.Command
		queryText, binds, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}

		switch queryText {
		case commandText(t, query.PreviewGroupProjection.ScopeCount):
			countCalls++
			return visit(map[string]any{"count": int64(10000)})
		case commandText(t, assessmentCommand):
			sampleCalls++
			if !reflect.DeepEqual(binds, commandBinds(t, assessmentCommand)) {
				t.Fatalf("sample binds=%v, want %v", binds, commandBinds(t, assessmentCommand))
			}
			return visit(map[string]any{"sampleRows": int64(512), "distinctProjections": int64(2), "serializedCharacters": int64(40)})
		case commandText(t, factoredCommand):
			if finalCalls == 0 {
				if _, found := engine.groupPreviewRowsCache.getDecision(decisionKey); found {
					t.Fatal("positive strategy was cached before the final query completed")
				}
			}
			finalCalls++
			return visit(map[string]any{"category": "respiratory", "rows": int64(12)})
		default:
			return fmt.Errorf("unexpected ordinary or fallback query: %s", queryText)
		}
	}
	for call := 1; call <= 2; call++ {
		summary, err := engine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "semantic_group_projection", Limit: 600}, func(map[string]any) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		if !summary.Complete || summary.RowCount != 1 {
			t.Fatalf("preview summary=%+v, want one complete row", summary)
		}
		if countCalls != 1 || sampleCalls != 1 || finalCalls != call {
			t.Fatalf("preview %d count/sample/final calls=%d/%d/%d, want 1/1/%d", call, countCalls, sampleCalls, finalCalls, call)
		}
		if call == 1 {
			decision, found := engine.groupPreviewRowsCache.getDecision(decisionKey)
			if !found || !decision.Factorized {
				t.Fatalf("successful final query did not confirm positive strategy: decision=%+v found=%t", decision, found)
			}
		}
	}
	if factoredCommand.Backend() != command.CompiledBackendAQL || index.Name == "" {
		t.Fatalf("missing selected projection index or rendered binds: index=%+v", index)
	}
}

func TestChooseGroupProjectionFactorizationContinuesAfterCoveringIndexSelection(t *testing.T) {
	engine, _, query, stream := semanticGroupProjectionExecutionFixture(t, nil)
	index, assessmentCommand, factoredCommand := configureSemanticGroupProjectionIndex(t, engine, query)
	engine.previewCollectionRevision = func(context.Context, string) (string, error) { return "revision-covered", nil }
	sampleCalls := 0
	stream.sourceRows = func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		command := request.Command
		queryText, _, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}

		switch queryText {
		case commandText(t, query.PreviewGroupProjection.ScopeCount):
			return visit(map[string]any{"count": int64(1000)})
		case commandText(t, assessmentCommand):
			sampleCalls++
			return visit(map[string]any{"sampleRows": int64(512), "distinctProjections": int64(2), "serializedCharacters": int64(40)})
		default:
			t.Fatalf("unexpected query after covered plan selection:\n%s", queryText)
			return nil
		}
	}
	if err := engine.chooseGroupPreviewScan(context.Background(), &query, &stream); err != nil {
		t.Fatal(err)
	}
	if query.PreviewCoveringIndex != nil || !reflect.DeepEqual(query.Command, factoredCommand) || sampleCalls != 1 || index.Name == "" {
		t.Fatalf("read-only covering-index path index=%+v factored=%t sample calls=%d selected=%+v", query.PreviewCoveringIndex, reflect.DeepEqual(query.Command, factoredCommand), sampleCalls, index)
	}
}

func TestChooseGroupProjectionFactorizationRejectsHighEntropyAndAllEmptySamples(t *testing.T) {
	for _, test := range []struct {
		name string
		row  map[string]any
	}{
		{name: "high entropy", row: map[string]any{"sampleRows": int64(512), "distinctProjections": int64(80), "serializedCharacters": int64(1600)}},
		{name: "all empty", row: map[string]any{"sampleRows": int64(0), "distinctProjections": int64(0), "serializedCharacters": int64(0)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine, _, query, stream := semanticGroupProjectionExecutionFixture(t, nil)
			_, assessmentCommand, _ := configureSemanticGroupProjectionIndex(t, engine, query)
			engine.previewCollectionRevision = func(context.Context, string) (string, error) { return "revision-1", nil }
			ordinaryQuery := commandText(t, query.Command)
			stream.sourceRows = func(_ context.Context, request RowsRequest, visit RowVisitor) error {
				command := request.Command
				queryText, _, dispatchErr := command.AQLParameters()
				if dispatchErr != nil {
					return dispatchErr
				}

				switch queryText {
				case commandText(t, query.PreviewGroupProjection.ScopeCount):
					return visit(map[string]any{"count": int64(1000)})
				case commandText(t, assessmentCommand):
					return visit(test.row)
				default:
					t.Fatalf("unexpected query %q", queryText)
					return nil
				}
			}
			if err := engine.chooseGroupPreviewScan(context.Background(), &query, &stream); err != nil {
				t.Fatal(err)
			}
			if commandText(t, query.Command) != ordinaryQuery || stream.queryMemoryLimit != 0 {
				t.Fatalf("ineligible sample changed plan or enabled cap: query changed=%t cap=%d", commandText(t, query.Command) != ordinaryQuery, stream.queryMemoryLimit)
			}
		})
	}
}

func TestChooseGroupProjectionFactorizationKeepsPrefixEstimateAdvisory(t *testing.T) {
	engine, resolved, query, stream := semanticGroupProjectionExecutionFixture(t, nil)
	_, assessmentCommand, factoredCommand := configureSemanticGroupProjectionIndex(t, engine, query)
	engine.previewCollectionRevision = func(context.Context, string) (string, error) { return "revision-unique-tail", nil }
	stream.sourceRows = func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		command := request.Command
		queryText, _, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}

		switch queryText {
		case commandText(t, query.PreviewGroupProjection.ScopeCount):
			return visit(map[string]any{"count": int64(1024)})
		case commandText(t, assessmentCommand):
			// The first 512 rows are low diversity. A later tail can contain a
			// unique Coding projection for every row; the sample is cost advice,
			// never the source of the factored query's output values.
			return visit(map[string]any{"sampleRows": int64(512), "distinctProjections": int64(2), "serializedCharacters": int64(40)})
		default:
			t.Fatalf("unexpected query %q", queryText)
			return nil
		}
	}
	if err := engine.chooseGroupPreviewScan(context.Background(), &query, &stream); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(query.Command, factoredCommand) {
		t.Fatalf("sample metadata was used as output; query is not the complete factored plan:\n%s", commandText(t, query.Command))
	}
	if estimate, ok := estimateGroupProjectionDictionaryBytes(groupProjectionSample{sampleRows: 512, distinctProjections: 2, serializedCharacters: 40}, 1024); !ok || estimate == 0 {
		t.Fatalf("advisory estimate=%d valid=%t", estimate, ok)
	}
	engine.previewCollectionRevision = func(context.Context, string) (string, error) { return "revision-unique-tail", nil }
	engine.previewSourceRows = func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		command := request.Command
		queryText, _, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}

		switch queryText {
		case commandText(t, query.PreviewGroupProjection.ScopeCount):
			return visit(map[string]any{"count": int64(1024)})
		case commandText(t, assessmentCommand):
			return visit(map[string]any{"sampleRows": int64(512), "distinctProjections": int64(2), "serializedCharacters": int64(40)})
		case commandText(t, factoredCommand):
			for index := range 600 {
				if err := visit(map[string]any{"category": fmt.Sprintf("tail-%03d", index), "rows": int64(1)}); err != nil {
					return err
				}
			}
			return nil
		default:
			return fmt.Errorf("unexpected ordinary fallback query: %s", queryText)
		}
	}
	categories := make([]string, 0, 600)
	summary, err := engine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "semantic_group_projection", Limit: 600}, func(row map[string]any) error {
		categories = append(categories, row["category"].(string))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.RowCount != 600 || len(categories) != 600 || categories[0] != "tail-000" || categories[599] != "tail-599" {
		t.Fatalf("factored full-query tail output count=%d categories=%d first=%q last=%q", summary.RowCount, len(categories), categories[0], categories[len(categories)-1])
	}
}

func TestEstimateGroupProjectionDictionaryBytesRejectsOverflowingPopulationEstimate(t *testing.T) {
	sample := groupProjectionSample{sampleRows: 1, distinctProjections: 1, serializedCharacters: 1}
	if estimate, ok := estimateGroupProjectionDictionaryBytes(sample, int64(^uint64(0)>>1)); ok || estimate != 0 {
		t.Fatalf("overflowing estimated cardinality accepted: bytes=%d valid=%t", estimate, ok)
	}
}

func TestChooseGroupProjectionFactorizationFallsBackOnUnknownEvidenceAndDeadline(t *testing.T) {
	t.Run("unknown revision", func(t *testing.T) {
		engine, _, query, stream := semanticGroupProjectionExecutionFixture(t, nil)
		_, _, _ = configureSemanticGroupProjectionIndex(t, engine, query)
		engine.previewCollectionRevision = func(context.Context, string) (string, error) { return "", errors.New("revision unavailable") }
		ordinaryQuery := commandText(t, query.Command)
		stream.sourceRows = func(_ context.Context, request RowsRequest, _ RowVisitor) error {
			command := request.Command
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			t.Fatal("sample ran without source revision evidence")
			return nil
		}
		if err := engine.chooseGroupPreviewScan(context.Background(), &query, &stream); err != nil {
			t.Fatal(err)
		}
		if commandText(t, query.Command) != ordinaryQuery {
			t.Fatal("unknown source revision changed the ordinary plan")
		}
	})

	t.Run("unknown scoped count", func(t *testing.T) {
		engine, _, query, stream := semanticGroupProjectionExecutionFixture(t, nil)
		_, _, _ = configureSemanticGroupProjectionIndex(t, engine, query)
		engine.previewCollectionRevision = func(context.Context, string) (string, error) { return "revision-1", nil }
		ordinaryQuery := commandText(t, query.Command)
		stream.sourceRows = func(_ context.Context, request RowsRequest, _ RowVisitor) error {
			command := request.Command
			queryText, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			if !reflect.DeepEqual(command, query.PreviewGroupProjection.ScopeCount) {
				t.Fatalf("query=%q, want exact scope count", queryText)
			}
			return nil
		}
		if err := engine.chooseGroupPreviewScan(context.Background(), &query, &stream); err != nil {
			t.Fatal(err)
		}
		if commandText(t, query.Command) != ordinaryQuery {
			t.Fatal("missing scoped count evidence changed the ordinary plan")
		}
	})

	t.Run("unknown scan plan", func(t *testing.T) {
		engine, _, query, stream := semanticGroupProjectionExecutionFixture(t, nil)
		query.PreviewGroupScan = &compiler.PreviewGroupScanSpec{
			Collection: query.PreviewGroupProjection.Collection,
			ScopeCount: query.PreviewGroupProjection.ScopeCount,
		}
		engine.previewCollectionScans = func(context.Context, command.CompiledCommand, string) (PreviewCollectionScanEvidence, error) {
			return PreviewCollectionScanEvidence{}, nil
		}
		engine.previewCollectionRevision = func(context.Context, string) (string, error) {
			t.Fatal("source revision read without a resolved scan plan")
			return "", nil
		}
		ordinaryQuery := commandText(t, query.Command)
		stream.sourceRows = func(_ context.Context, request RowsRequest, _ RowVisitor) error {
			command := request.Command
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			t.Fatal("scope count or sample ran without a resolved scan plan")
			return nil
		}
		if err := engine.chooseGroupPreviewScan(context.Background(), &query, &stream); err != nil {
			t.Fatal(err)
		}
		if commandText(t, query.Command) != ordinaryQuery {
			t.Fatal("unknown scan plan changed the ordinary query")
		}
	})

	t.Run("assessment deadline", func(t *testing.T) {
		engine, _, query, stream := semanticGroupProjectionExecutionFixture(t, nil)
		_, assessmentCommand, _ := configureSemanticGroupProjectionIndex(t, engine, query)
		engine.previewCollectionRevision = func(context.Context, string) (string, error) { return "revision-deadline", nil }
		ordinaryQuery := commandText(t, query.Command)
		stream.sourceRows = func(ctx context.Context, request RowsRequest, visit RowVisitor) error {
			command := request.Command
			queryText, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			if reflect.DeepEqual(command, query.PreviewGroupProjection.ScopeCount) {
				return visit(map[string]any{"count": int64(1000)})
			}
			if queryText != commandText(t, assessmentCommand) {
				t.Fatalf("unexpected query %q", queryText)
			}
			<-ctx.Done()
			return ctx.Err()
		}
		if err := engine.chooseGroupPreviewScan(context.Background(), &query, &stream); err != nil {
			t.Fatal(err)
		}
		if commandText(t, query.Command) != ordinaryQuery {
			t.Fatal("assessment timeout changed the ordinary plan")
		}
	})
}

func TestPreviewOutputDoesNotRetryOrdinaryQueryAfterFactoredQueryFailure(t *testing.T) {
	engine, resolved, query, _ := semanticGroupProjectionExecutionFixture(t, nil)
	_, assessmentCommand, factoredCommand := configureSemanticGroupProjectionIndex(t, engine, query)
	engine.previewCollectionRevision = func(context.Context, string) (string, error) { return "revision-error", nil }
	wantErr := shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrResourceLimit, ErrorMessage: "AQL: query would use more memory than allowed"}
	countCalls, sampleCalls, factoredCalls, ordinaryCalls := 0, 0, 0, 0
	engine.previewSourceRows = func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		command := request.Command
		queryText, _, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}

		switch queryText {
		case commandText(t, query.PreviewGroupProjection.ScopeCount):
			countCalls++
			return visit(map[string]any{"count": int64(1000)})
		case commandText(t, assessmentCommand):
			sampleCalls++
			return visit(map[string]any{"sampleRows": int64(512), "distinctProjections": int64(2), "serializedCharacters": int64(40)})
		case commandText(t, factoredCommand):
			factoredCalls++
			// The production AQL adapter classifies its own driver failures
			// at the seam; this fake simulates a correctly wired adapter.
			translated, _ := arango.TranslateQueryError(fmt.Errorf("arango query: %w", wantErr))
			return translated
		case commandText(t, query.Command):
			ordinaryCalls++
			return visit(map[string]any{"category": "ordinary", "rows": int64(1)})
		default:
			return fmt.Errorf("unexpected ordinary or fallback query: %s", queryText)
		}
	}
	_, err := engine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "semantic_group_projection", Limit: 600}, func(map[string]any) error { return nil })
	// Execution consumes the classified Loom error; the vendor cause stays
	// reachable through the chain exactly as when the server classified it.
	userErr, isUser := dataframeerrors.AsUserError(err)
	if !isUser || userErr.Code() != string(dataframeerrors.CodeQueryMemoryLimitExceeded) {
		t.Fatalf("preview error=%#v, want classified QUERY_MEMORY_LIMIT_EXCEEDED factored-query error", err)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("driver cause lost through the classified preview failure: %v", err)
	}
	if countCalls != 1 || sampleCalls != 1 || factoredCalls != 1 || ordinaryCalls != 0 {
		t.Fatalf("first preview count/sample/factored/ordinary=%d/%d/%d/%d, want 1/1/1/0", countCalls, sampleCalls, factoredCalls, ordinaryCalls)
	}
	if _, err := engine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "semantic_group_projection", Limit: 600}, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if countCalls != 1 || sampleCalls != 1 || factoredCalls != 1 || ordinaryCalls != 1 {
		t.Fatalf("retry count/sample/factored/ordinary=%d/%d/%d/%d, want 1/1/1/1", countCalls, sampleCalls, factoredCalls, ordinaryCalls)
	}
}
