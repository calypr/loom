package execution

// TestPreviewCapabilityBoundaryContract is the arch-backend-02 acceptance
// gate: shared preview orchestration consumes only the neutral capabilities
// in preview_capabilities.go, a rows-only adapter must still produce a
// correct bounded preview, fresh matching evidence may enable the existing
// optimization, missing/stale/failed evidence must take the canonical
// fallback without changing authorization, row completeness, limits, or
// query errors, and compile-only Explain must work without a database.
// Every subtest runs service-free; no subtest may skip.

import (
	"context"
	"errors"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/recipe"
	exec "github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/store/arango"
)

// forbiddenPreviewPkgPrefixes name the vendor implementation trees that the
// shared preview contracts and configuration must never reference by type.
var forbiddenPreviewPkgPrefixes = []string{
	"github.com/calypr/loom/internal/store/arango",
	"github.com/jackc/pgx",
}

func isForbiddenPreviewPkg(pkgPath string) bool {
	for _, prefix := range forbiddenPreviewPkgPrefixes {
		if pkgPath == prefix || strings.HasPrefix(pkgPath, prefix+"/") {
			return true
		}
	}
	return false
}

func previewContractVendorOffenders(ty reflect.Type, seen map[reflect.Type]bool, depth int) []string {
	if ty == nil || depth > 6 {
		return nil
	}
	if seen[ty] {
		return nil
	}
	seen[ty] = true
	if pkg := ty.PkgPath(); pkg != "" && isForbiddenPreviewPkg(pkg) {
		return []string{ty.String()}
	}
	switch ty.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Chan:
		return previewContractVendorOffenders(ty.Elem(), seen, depth+1)
	case reflect.Map:
		offenders := previewContractVendorOffenders(ty.Key(), seen, depth+1)
		return append(offenders, previewContractVendorOffenders(ty.Elem(), seen, depth+1)...)
	case reflect.Func:
		var offenders []string
		for i := 0; i < ty.NumIn(); i++ {
			offenders = append(offenders, previewContractVendorOffenders(ty.In(i), seen, depth+1)...)
		}
		for i := 0; i < ty.NumOut(); i++ {
			offenders = append(offenders, previewContractVendorOffenders(ty.Out(i), seen, depth+1)...)
		}
		return offenders
	case reflect.Struct:
		var offenders []string
		for i := 0; i < ty.NumField(); i++ {
			offenders = append(offenders, previewContractVendorOffenders(ty.Field(i).Type, seen, depth+1)...)
		}
		return offenders
	}
	return nil
}

type previewCapabilityBoundaryFixture struct {
	engine   *Engine
	resolved Resolved
	commands []capturedAQLCommand
	rows     []map[string]any
	summary  PreviewSummary
	err      error
}

type capturedAQLCommand struct {
	Text  string
	Binds map[string]any
}

type previewCapabilityBoundaryOptions struct {
	scans           PreviewCollectionScans
	coveringIndexes func(context.Context, string) ([]PreviewCollectionIndex, error)
	rowsFailure     error
	visitorFailure  error
	restrictedAuth  bool
	limit           int
}

func runPreviewCapabilityBoundary(t *testing.T, options previewCapabilityBoundaryOptions) *previewCapabilityBoundaryFixture {
	t.Helper()
	fixture := &previewCapabilityBoundaryFixture{}
	limit := options.limit
	if limit == 0 {
		limit = 1
	}
	engine, err := New(Config{
		CompileTarget: compiler.ArangoTarget(),
		Registry:      invalidRecipeRegistry{},
		ScopeDigest:   func(recipe.RuntimeBindings) string { return "preview-capability-boundary" },
		SourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
			command := request.Command
			_, _, dispatchErr := command.AQLParameters()
			return dispatchErr
		},
		PreviewSourceRows: func(_ context.Context, request RowsRequest, visit RowVisitor) error {
			command := request.Command
			text, binds, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}
			fixture.commands = append(fixture.commands, capturedAQLCommand{Text: text, Binds: binds})
			if options.rowsFailure != nil {
				return options.rowsFailure
			}
			for _, row := range []map[string]any{
				{"status": "final", "rows": float64(2)},
				{"status": "preliminary", "rows": float64(1)},
			} {
				visitor := visit
				if options.visitorFailure != nil {
					visitor = func(map[string]any) error { return options.visitorFailure }
				}
				if err := visitor(row); err != nil {
					return err
				}
			}
			return nil
		},
		PreviewCollectionScans:    options.scans,
		PreviewCoveringIndexes:    options.coveringIndexes,
		PreviewCollectionRevision: func(context.Context, string) (string, error) { return "boundary-revision", nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fixture.engine = engine
	bindings := recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "G1"}
	if options.restrictedAuth {
		bindings.AuthScopeMode = authscope.ReadScopeRestricted
		bindings.AuthResourcePaths = []string{"Observation/allowed"}
	}
	fixture.resolved = compileGroupPreviewCacheTestOutput(t, engine, bindings)
	visitorCalls := 0
	fixture.summary, fixture.err = engine.PreviewOutput(context.Background(), fixture.resolved,
		PreviewRequest{Output: "group_preview", Limit: limit}, func(row map[string]any) error {
			visitorCalls++
			fixture.rows = append(fixture.rows, row)
			return nil
		})
	return fixture
}

func comparablePreviewSummary(summary PreviewSummary) PreviewSummary {
	summary.LoweringDuration = 0
	summary.QueryDuration = 0
	return summary
}

func TestPreviewCapabilityBoundaryContract(t *testing.T) {
	t.Run("preview contracts carry no vendor plan or index types", func(t *testing.T) {
		seen := map[reflect.Type]bool{}
		if offenders := previewContractVendorOffenders(reflect.TypeOf(Config{CompileTarget: compiler.ArangoTarget()}), seen, 0); len(offenders) != 0 {
			t.Fatalf("execution Config references vendor preview types: %v", offenders)
		}
		if offenders := previewContractVendorOffenders(reflect.TypeOf(Engine{}), seen, 0); len(offenders) != 0 {
			t.Fatalf("execution Engine references vendor preview types: %v", offenders)
		}
		for _, contract := range []reflect.Type{
			reflect.TypeOf(PreviewCollectionIndex{}),
			reflect.TypeOf(PreviewCollectionScanEvidence{}),
			reflect.TypeOf(PreviewIndexNodeEvidence{}),
			reflect.TypeOf(PreviewCollectionScans(nil)),
		} {
			if offenders := previewContractVendorOffenders(contract, map[reflect.Type]bool{}, 0); len(offenders) != 0 {
				t.Fatalf("neutral preview contract %v references vendor types: %v", contract, offenders)
			}
		}
		// Non-vacuity control: the same walker must flag the superseded
		// vendor-shaped callbacks, proving the guard would fire if concrete
		// Arango plan/index types returned to the shared contracts.
		legacy := struct {
			PreviewExplainQuery    func(context.Context, string, map[string]any) (arango.ExplainResult, error)
			PreviewCoveringIndexes func(context.Context, string) ([]arango.PreviewCoveringIndex, error)
		}{}
		offenders := previewContractVendorOffenders(reflect.TypeOf(legacy), map[reflect.Type]bool{}, 0)
		if len(offenders) != 2 {
			t.Fatalf("walker missed the superseded vendor shapes: offenders=%v, want two", offenders)
		}
	})

	// Baseline: the rows-only adapter (no optional evidence capability at
	// all) must produce the canonical bounded preview.
	rowsOnly := runPreviewCapabilityBoundary(t, previewCapabilityBoundaryOptions{restrictedAuth: true})
	if rowsOnly.err != nil {
		t.Fatalf("rows-only bounded preview: %v", rowsOnly.err)
	}
	if len(rowsOnly.commands) != 1 {
		t.Fatalf("rows-only adapter invocations = %d, want exactly one canonical query", len(rowsOnly.commands))
	}
	canonicalText := rowsOnly.commands[0].Text
	if !strings.Contains(canonicalText, "COLLECT") {
		t.Fatalf("rows-only fixture did not run a Group preview command:\n%s", canonicalText)
	}
	// The canonical Group query may carry its compiler-owned hint, but with
	// no evidence capability it must never switch to the boundary candidate.
	if strings.Contains(canonicalText, "loom_pivot_preview_boundary_covering") {
		t.Fatalf("rows-only preview switched to the boundary candidate without evidence:\n%s", canonicalText)
	}
	if len(rowsOnly.rows) != 1 || rowsOnly.rows[0]["status"] != "final" {
		t.Fatalf("rows-only bounded rows = %+v, want one bounded row", rowsOnly.rows)
	}
	if rowsOnly.summary.RowCount != 1 || !rowsOnly.summary.Truncated || rowsOnly.summary.Complete {
		t.Fatalf("rows-only summary = %+v, want truncated bounded preview", rowsOnly.summary)
	}
	if rowsOnly.commands[0].Binds["auth_resource_paths"] == nil {
		t.Fatalf("rows-only restricted preview dropped the authorization binds: %v", rowsOnly.commands[0].Binds)
	}

	t.Run("fresh matching evidence enables the existing optimization", func(t *testing.T) {
		// The identical fixture with fresh matching evidence must take the
		// hinted command — proving the rows-only canonical choice above came
		// from missing evidence, not from a dead optimizer (guard-drop).
		index := PreviewCollectionIndex{
			Collection:   "Observation",
			Name:         "loom_pivot_preview_boundary_covering",
			Fields:       []string{"project", "dataset_generation", "auth_resource_path", "_key", "payload.id", "payload.status"},
			StoredValues: []string{"payload.valueQuantity"},
		}
		scanCalls := 0
		fixture := runPreviewCapabilityBoundary(t, previewCapabilityBoundaryOptions{
			restrictedAuth: true,
			coveringIndexes: func(_ context.Context, collection string) ([]PreviewCollectionIndex, error) {
				if collection != "Observation" {
					t.Fatalf("index collection = %q, want Observation", collection)
				}
				return []PreviewCollectionIndex{index}, nil
			},
			scans: func(_ context.Context, command command.CompiledCommand, collection string) (PreviewCollectionScanEvidence, error) {
				scanCalls++
				if collection != "Observation" {
					t.Fatalf("scan collection = %q, want Observation", collection)
				}
				text, _, dispatchErr := command.AQLParameters()
				if dispatchErr != nil {
					return PreviewCollectionScanEvidence{}, dispatchErr
				}
				if !strings.Contains(text, `indexHint: "`+index.Name+`"`) {
					return PreviewCollectionScanEvidence{}, nil
				}
				return PreviewCollectionScanEvidence{IndexNodes: []PreviewIndexNodeEvidence{
					{IndexNames: []string{index.Name}, Coverage: PreviewIndexCoverageCovering},
				}}, nil
			},
		})
		if fixture.err != nil {
			t.Fatalf("evidence-armed bounded preview: %v", fixture.err)
		}
		if scanCalls == 0 {
			t.Fatal("the plan-inspection capability was never consulted")
		}
		if len(fixture.commands) != 1 || !strings.Contains(fixture.commands[0].Text, `indexHint: "`+index.Name+`"`) {
			t.Fatalf("fresh covering evidence did not reach the executed command: %+v", fixture.commands)
		}
		if len(fixture.rows) != 1 || fixture.rows[0]["status"] != "final" {
			t.Fatalf("evidence-armed bounded rows = %+v, want the same bounded row", fixture.rows)
		}
		if !reflect.DeepEqual(fixture.commands[0].Binds, rowsOnly.commands[0].Binds) {
			t.Fatalf("optimized command changed the authorization binds: %v vs %v", fixture.commands[0].Binds, rowsOnly.commands[0].Binds)
		}
		if !reflect.DeepEqual(comparablePreviewSummary(fixture.summary), comparablePreviewSummary(rowsOnly.summary)) {
			t.Fatalf("optimized preview changed the bounded summary: %+v vs %+v", fixture.summary, rowsOnly.summary)
		}
	})

	t.Run("absent or failed evidence takes the canonical fallback", func(t *testing.T) {
		for _, test := range []struct {
			name            string
			scans           PreviewCollectionScans
			coveringIndexes func(context.Context, string) ([]PreviewCollectionIndex, error)
		}{
			{
				name: "scan inspection fails",
				scans: func(context.Context, command.CompiledCommand, string) (PreviewCollectionScanEvidence, error) {
					return PreviewCollectionScanEvidence{}, errors.New("inspection unavailable")
				},
				coveringIndexes: func(context.Context, string) ([]PreviewCollectionIndex, error) {
					return []PreviewCollectionIndex{{
						Collection: "Observation", Name: "loom_pivot_preview_boundary_covering",
						Fields:       []string{"project", "dataset_generation", "auth_resource_path", "_key", "payload.id", "payload.status"},
						StoredValues: []string{"payload.valueQuantity"},
					}}, nil
				},
			},
			{
				name: "empty evidence certifies nothing",
				scans: func(context.Context, command.CompiledCommand, string) (PreviewCollectionScanEvidence, error) {
					return PreviewCollectionScanEvidence{}, nil
				},
				coveringIndexes: func(context.Context, string) ([]PreviewCollectionIndex, error) {
					return []PreviewCollectionIndex{{
						Collection: "Observation", Name: "loom_pivot_preview_boundary_covering",
						Fields:       []string{"project", "dataset_generation", "auth_resource_path", "_key", "payload.id", "payload.status"},
						StoredValues: []string{"payload.valueQuantity"},
					}}, nil
				},
			},
			{
				// With the listing unavailable no covering candidate can be
				// attempted; the ordinary broad-scan check still consults
				// the capability, and empty evidence certifies nothing.
				name: "index listing fails",
				scans: func(context.Context, command.CompiledCommand, string) (PreviewCollectionScanEvidence, error) {
					return PreviewCollectionScanEvidence{}, nil
				},
				coveringIndexes: func(context.Context, string) ([]PreviewCollectionIndex, error) {
					return nil, errors.New("listing unavailable")
				},
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				fixture := runPreviewCapabilityBoundary(t, previewCapabilityBoundaryOptions{
					restrictedAuth:  true,
					scans:           test.scans,
					coveringIndexes: test.coveringIndexes,
				})
				if fixture.err != nil {
					t.Fatalf("advisory evidence failure must not fail the preview: %v", fixture.err)
				}
				if len(fixture.commands) != 1 || fixture.commands[0].Text != canonicalText {
					t.Fatalf("fallback did not run the canonical command: %+v", fixture.commands)
				}
				if !reflect.DeepEqual(fixture.commands[0].Binds, rowsOnly.commands[0].Binds) {
					t.Fatalf("fallback changed the authorization binds: %v vs %v", fixture.commands[0].Binds, rowsOnly.commands[0].Binds)
				}
				if len(fixture.rows) != 1 || fixture.rows[0]["status"] != "final" {
					t.Fatalf("fallback bounded rows = %+v, want the same bounded row", fixture.rows)
				}
				if !reflect.DeepEqual(comparablePreviewSummary(fixture.summary), comparablePreviewSummary(rowsOnly.summary)) {
					t.Fatalf("fallback changed the bounded summary: %+v vs %+v", fixture.summary, rowsOnly.summary)
				}
			})
		}
	})

	t.Run("execution errors remain errors", func(t *testing.T) {
		queryFailure := errors.New("boundary query failed")
		fixture := runPreviewCapabilityBoundary(t, previewCapabilityBoundaryOptions{
			restrictedAuth: true,
			rowsFailure:    queryFailure,
		})
		if !errors.Is(fixture.err, queryFailure) {
			t.Fatalf("preview error = %v, want the execution error to surface", fixture.err)
		}
		if len(fixture.commands) != 1 {
			t.Fatalf("failed execution retried %d times; fallback must not re-run queries", len(fixture.commands))
		}
		visitorFailure := errors.New("boundary visitor refused")
		visitorFixture := runPreviewCapabilityBoundary(t, previewCapabilityBoundaryOptions{
			restrictedAuth: true,
			visitorFailure: visitorFailure,
		})
		if !errors.Is(visitorFixture.err, visitorFailure) {
			t.Fatalf("visitor error = %v, want the visitor error to surface", visitorFixture.err)
		}
	})

	t.Run("compile only explain works without a database", func(t *testing.T) {
		// A local AQL engine (no adapter armed) proves the compile-only
		// Explain needs no backend and live dispatch stays tag-routed.
		aqlCalls, pgCalls := 0, 0
		engine, err := New(Config{
			CompileTarget: compiler.ArangoTarget(),
			Registry:      previewBoundaryExplainRegistry{},
			ScopeDigest:   func(recipe.RuntimeBindings) string { return "preview-boundary" },
			SourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
				if _, _, err := request.Command.AQLParameters(); err != nil {
					return err
				}
				aqlCalls++
				return nil
			},
			PreviewSourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
				if _, _, err := request.Command.AQLParameters(); err != nil {
					return err
				}
				aqlCalls++
				return nil
			},
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		bindings := recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "G1", PreviewLimit: 25, OutputNames: []string{"resources"}}
		control := Control{Engine: engine}
		result, err := control.ExplainPhysical(context.Background(), "preview-boundary", bindings, false)
		if err != nil {
			t.Fatalf("compile-only ExplainPhysical: %v", err)
		}
		if aqlCalls != 0 || pgCalls != 0 {
			t.Fatalf("compile-only Explain touched adapters: aql=%d pg=%d", aqlCalls, pgCalls)
		}
		for _, output := range result.Outputs {
			if output.Live != nil || output.LivePostgreSQL != nil {
				t.Fatalf("compile-only Explain carried live assessments: %+v", output)
			}
		}
		// Live dispatch stays backend-tagged: an AQL-tagged output must be
		// refused when only the PostgreSQL connection is armed.
		liveOnly := Control{Engine: engine, ExplainPostgreSQLConnection: func(context.Context, compiler.CompiledQuery) (PostgreSQLPlanAssessment, error) {
			pgCalls++
			return PostgreSQLPlanAssessment{}, nil
		}}
		if _, err := liveOnly.ExplainPhysical(context.Background(), "preview-boundary", bindings, true); err == nil ||
			!strings.Contains(err.Error(), "Arango") {
			t.Fatalf("AQL live Explain without an Arango connection: err=%v, want refusal", err)
		}
		if pgCalls != 0 {
			t.Fatal("the PostgreSQL Explain callback ran for an AQL-tagged command")
		}
	})
}

// TestChooseGroupPreviewScanHintLoopAbortsWhenContextExpiresDuringCapabilityCall
// pins the post-capability assessment-ctx boundary (verdict F-2): a covering
// candidate whose inspection SUCCEEDS under an already-dead budget must never
// reach DECIDE. The guard after the capability call, not the loop-top or the
// final gate, is what keeps the hinted selection unapplied — deleting it
// flips query.Command/stream.command to the hinted command before the final
// context check can fail the preview, which this test's decision snapshot
// catches.
func TestChooseGroupPreviewScanHintLoopAbortsWhenContextExpiresDuringCapabilityCall(t *testing.T) {
	engine, query, stream := compiledGroupPreviewScanFixture(t, recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "G1"})
	original := commandText(t, query.Command)
	candidate := PreviewCollectionIndex{
		Collection: query.PreviewCoveringIndex.Collection, Name: "loom_pivot_preview_verdict_hint",
		Fields: append([]string(nil), query.PreviewCoveringIndex.Fields...),
	}
	engine.previewCoveringIndexes = func(context.Context, string) ([]PreviewCollectionIndex, error) {
		return []PreviewCollectionIndex{candidate}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scanCalls := 0
	engine.previewCollectionScans = func(context.Context, command.CompiledCommand, string) (PreviewCollectionScanEvidence, error) {
		scanCalls++
		// The inspection returns a covering verdict, but the budget dies
		// while it is in flight: the post-call guard owns the abort.
		cancel()
		return PreviewCollectionScanEvidence{IndexNodes: []PreviewIndexNodeEvidence{
			{IndexNames: []string{candidate.Name}, Coverage: PreviewIndexCoverageCovering},
		}}, nil
	}
	err := engine.chooseGroupPreviewScan(ctx, &query, &stream)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want the canceled-budget path", err)
	}
	if scanCalls != 1 {
		t.Fatalf("capability consultations = %d, want exactly one (the aborted candidate)", scanCalls)
	}
	if got := commandText(t, query.Command); got != original || strings.Contains(got, `indexHint: "`+candidate.Name+`"`) {
		t.Fatalf("a canceled inspection still flipped the decision to the hinted command: %q", got)
	}
	if got := commandText(t, stream.command); got != original {
		t.Fatalf("stream command drifted after the canceled inspection: %q", got)
	}
}

// TestChooseGroupPreviewScanOrdinaryPathSkippedWhenAssessmentBudgetExpired
// pins the ordinary-path conjunct of the same boundary (verdict F-3): once
// the assessment budget expires while the broad-scan inspection is in
// flight, the advisory decision must be observably skipped — the scoped
// count query is not executed at all — rather than merely ending non-fatally.
// readGroupPreviewCount's own ctx hygiene makes the resulting STATE look
// identical under a dropped conjunct, so the executor counter is the witness.
func TestChooseGroupPreviewScanOrdinaryPathSkippedWhenAssessmentBudgetExpired(t *testing.T) {
	engine, query, stream := groupPreviewScanFixture()
	engine.previewCollectionCount = func(context.Context, string) (int64, error) { return 1000, nil }
	scopeCountExecutions := 0
	stream.sourceRows = func(_ context.Context, request RowsRequest, visit RowVisitor) error {
		command := request.Command
		_, _, dispatchErr := command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}
		scopeCountExecutions++
		return visit(map[string]any{"count": float64(950)})
	}
	scanCalls := 0
	engine.previewCollectionScans = func(ctx context.Context, _ command.CompiledCommand, _ string) (PreviewCollectionScanEvidence, error) {
		scanCalls++
		// Certify broad non-covering, but only after the assessment budget
		// has expired during the call.
		<-ctx.Done()
		return notCoveringGroupScans(), nil
	}
	// The assessment budget is advisory: its expiry must silently void the
	// broad-scan decision (the user preview survives a slow external
	// inspection), unlike a canceled REQUEST which fails through
	// contextError(ctx) on the parent.
	if err := engine.chooseGroupPreviewScan(context.Background(), &query, &stream); err != nil {
		t.Fatalf("advisory budget expiry failed the preview: %v", err)
	}
	if scanCalls != 1 {
		t.Fatalf("capability consultations = %d, want the ordinary-path check to have run", scanCalls)
	}
	if scopeCountExecutions != 0 {
		t.Fatalf("expired budget still executed the scoped count query %d times; the decision must be skipped", scopeCountExecutions)
	}
	if got := commandText(t, query.Command); got != "normal-query" {
		t.Fatalf("expired budget switched the command to %q", got)
	}
}

// previewBoundaryExplainRegistry serves one compilable bundle for the
// compile-only Explain subtest.
type previewBoundaryExplainRegistry struct{}

func (previewBoundaryExplainRegistry) LoadRecipe(context.Context, string) (exec.Entry, error) {
	return exec.Entry{Bundle: recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "preview-boundary",
		TranslationVersion:  "test",
		Outputs: []recipe.Output{{
			Name: "resources", RootResourceType: "Patient", RowGrain: "patient",
			Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		}},
	}}, nil
}

func (r previewBoundaryExplainRegistry) LoadRecipeVersion(ctx context.Context, name, _ string) (exec.Entry, error) {
	return r.LoadRecipe(ctx, name)
}
