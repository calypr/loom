package execution

// Folded adversarial pins for arch-backend-01. These preserve the guarantees
// the pg-exec-02 verifier proved (PR #99 probes P1, P11, P12) across the
// typed compiled-command boundary migration: resolver panics fail closed, and
// every preview marker-reset site (visitor error, non-limit query error, and
// post-stream context error) clears the completed/truncated markers.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/store/arango"
)

type verifyArch01Trace struct {
	mu         sync.Mutex
	aqlFull    []string
	aqlPreview []string
	pgFull     []string
	pgPreview  []string
	controls   []string
	chCalls    int
}

func (v *verifyArch01Trace) add(list *[]string, item string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	*list = append(*list, item)
}

func (v *verifyArch01Trace) snapshot() (aqlFull, aqlPreview, pgFull, pgPreview, controls []string, ch int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.aqlFull...), append([]string(nil), v.aqlPreview...),
		append([]string(nil), v.pgFull...), append([]string(nil), v.pgPreview...),
		append([]string(nil), v.controls...), v.chCalls
}

func (v *verifyArch01Trace) quietAQL(t *testing.T, stage string) {
	t.Helper()
	aqlFull, aqlPreview, _, _, controls, ch := v.snapshot()
	if len(aqlFull) != 0 || len(aqlPreview) != 0 || len(controls) != 0 || ch != 0 {
		t.Fatalf("%s: PostgreSQL plan touched Arango/ClickHouse callbacks: aql=%v preview=%v controls=%v ch=%d",
			stage, aqlFull, aqlPreview, controls, ch)
	}
}

func verifyArch01Config(v *verifyArch01Trace) Config {
	return Config{
		Registry:    invalidRecipeRegistry{},
		ScopeDigest: func(recipe.RuntimeBindings) string { return "verify-arch01-scope" },
		QueryRows: func(_ context.Context, command compiler.CompiledCommand, _ int, _ RowVisitor) error {
			text, _, err := command.AQLParameters()
			if err != nil {
				return err
			}
			v.add(&v.aqlFull, text)
			return nil
		},
		PreviewQueryRows: func(_ context.Context, command compiler.CompiledCommand, _ int, _ RowVisitor) error {
			text, _, err := command.AQLParameters()
			if err != nil {
				return err
			}
			v.add(&v.aqlPreview, text)
			return nil
		},
		PreparePreviewIndex: func(context.Context, compiler.PreviewCoveringIndexSpec) error {
			v.add(&v.controls, "preparePreviewIndex")
			return nil
		},
		PreviewCoveringIndexes: func(context.Context, string) ([]arango.PreviewCoveringIndex, error) {
			v.add(&v.controls, "previewCoveringIndexes")
			return nil, nil
		},
		PreviewCollectionRevision: func(context.Context, string) (string, error) {
			v.add(&v.controls, "previewCollectionRevision")
			return "rev-arch01", nil
		},
		PreviewExplainQuery: func(context.Context, string, map[string]any) (arango.ExplainResult, error) {
			v.add(&v.controls, "previewExplainQuery")
			return arango.ExplainResult{}, nil
		},
		PreviewCollectionCount: func(context.Context, string) (int64, error) {
			v.add(&v.controls, "previewCollectionCount")
			return 3, nil
		},
		ClickHouseQueryRows: func(context.Context, string, []string, func(map[string]any) error, ...any) error {
			v.mu.Lock()
			v.chCalls++
			v.mu.Unlock()
			return nil
		},
	}
}

func verifyArch01Resolved(columns []string, schema []lower.CompiledOutputColumn) Resolved {
	output := lower.CompiledRecipeOutput{
		Name:             "resources",
		RootResourceType: "Patient",
		Columns:          append([]string(nil), columns...),
		OutputSchema:     schema,
		RowIdentity:      &spec.RowIdentity{Fields: []string{"id"}},
		Plan:             ir.PhysicalPlan{Version: 1, Engine: ir.PhysicalEngineAQL},
	}
	return Resolved{
		Bundle: recipe.Bundle{Name: "verify_arch01"},
		Semantic: semantic.ResolvedRecipePlan{
			SemanticPlan: semantic.RecipePlan{Bindings: recipe.RuntimeBindings{
				Project: "P1", DatasetGeneration: "G1", OutputNames: []string{"resources"},
			}},
		},
		Compiled:           lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{output}},
		StoredRecipeDigest: "verify-arch01",
	}
}

func verifyArch01Schema(names ...string) []lower.CompiledOutputColumn {
	schema := make([]lower.CompiledOutputColumn, 0, len(names))
	for _, name := range names {
		schema = append(schema, lower.CompiledOutputColumn{ID: name, Name: name, Label: name, Kind: "string", Cardinality: "one"})
	}
	return schema
}

func verifyArch01Command(t *testing.T) compiler.CompiledCommand {
	t.Helper()
	command, err := compiler.NewPostgreSQLCommand(
		`SELECT id FROM loom.resource WHERE project = $1`,
		[]compiler.PostgreSQLParameter{{Type: compiler.PostgreSQLParameterText, Value: "P1"}})
	if err != nil {
		t.Fatalf("forge command: %v", err)
	}
	return command
}

func verifyArch01Row(v *verifyArch01Trace, target *[]string, rows []map[string]any) PostgreSQLQueryRows {
	return func(ctx context.Context, command compiler.CompiledCommand, _ []string, visit RowVisitor) error {
		sqlText, _, err := command.PostgreSQLParameters()
		if err != nil {
			return err
		}
		v.add(target, sqlText)
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	}
}

func verifyArch01Rows() []map[string]any {
	return []map[string]any{{"id": "one"}, {"id": "two"}}
}

func verifyArch01Resolver(selection *ResolvedPostgreSQLCommand, failure error) ResolvePostgreSQLCommands {
	return func(context.Context, lower.CompiledRecipeOutput, ir.PhysicalPlan, recipe.RuntimeBindings, int) (*ResolvedPostgreSQLCommand, error) {
		return selection, failure
	}
}

// P1 (folded): a PANICKING resolver must surface the panic (fail closed) and
// must not fall through to AQL nor touch any adapter.
func TestVerifyArch01ResolverPanicFailsClosedNoCallbacks(t *testing.T) {
	v := &verifyArch01Trace{}
	config := verifyArch01Config(v)
	config.ResolvePostgreSQLCommands = func(context.Context, lower.CompiledRecipeOutput, ir.PhysicalPlan, recipe.RuntimeBindings, int) (*ResolvedPostgreSQLCommand, error) {
		panic("resolver exploded")
	}
	config.PostgreSQLQueryRows = verifyArch01Row(v, &v.pgFull, verifyArch01Rows())
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatalf("resolver panic was swallowed; engine must not silently continue")
		}
		if msg := fmt.Sprint(recovered); !strings.Contains(msg, "resolver exploded") {
			t.Fatalf("panic identity lost: %v", recovered)
		}
		aqlFull, aqlPreview, pgFull, _, controls, ch := v.snapshot()
		if len(aqlFull) != 0 || len(aqlPreview) != 0 || len(pgFull) != 0 || len(controls) != 0 || ch != 0 {
			t.Fatalf("resolver panic touched adapters: aql=%v preview=%v pg=%d controls=%v ch=%d",
				aqlFull, aqlPreview, len(pgFull), controls, ch)
		}
	}()
	_, err = engine.Streams(context.Background(), verifyArch01Resolved([]string{"id"}, verifyArch01Schema("id")))
	t.Fatalf("panic must propagate; Streams returned err=%v", err)
}

// P11 (folded): an outer visitor error (visitorErr path) must clear the
// completed marker on a PostgreSQL preview.
func TestVerifyArch01VisitorErrorClearsMarkers(t *testing.T) {
	v := &verifyArch01Trace{}
	config := verifyArch01Config(v)
	config.ResolvePostgreSQLCommands = verifyArch01Resolver(&ResolvedPostgreSQLCommand{Command: verifyArch01Command(t), Columns: []string{"id"}}, nil)
	config.PostgreSQLQueryRows = verifyArch01Row(v, &v.pgFull, verifyArch01Rows())
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	visitorFailure := errors.New("verify_arch01 visitor refused")
	summary, err := engine.PreviewOutput(context.Background(), verifyArch01Resolved([]string{"id"}, verifyArch01Schema("id")),
		PreviewRequest{Output: "resources", Limit: 10}, func(map[string]any) error { return visitorFailure })
	if !errors.Is(err, visitorFailure) {
		t.Fatalf("visitor failure must surface: %v", err)
	}
	if summary.Complete || summary.Truncated {
		t.Fatalf("visitor-interrupted preview must carry neither marker: %+v", summary)
	}
	v.quietAQL(t, "visitor error")
}

// P12 (folded): a cancellation that lands after the final delivered row (the
// stream completes cleanly, then the context error path fires) must clear the
// completed marker.
func TestVerifyArch01ContextErrorAfterFinalRowClearsMarkers(t *testing.T) {
	v := &verifyArch01Trace{}
	config := verifyArch01Config(v)
	config.ResolvePostgreSQLCommands = verifyArch01Resolver(&ResolvedPostgreSQLCommand{Command: verifyArch01Command(t), Columns: []string{"id"}}, nil)
	config.PostgreSQLQueryRows = verifyArch01Row(v, &v.pgFull, verifyArch01Rows())
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rows := 0
	summary, err := engine.PreviewOutput(ctx, verifyArch01Resolved([]string{"id"}, verifyArch01Schema("id")),
		PreviewRequest{Output: "resources", Limit: 10}, func(map[string]any) error {
			rows++
			if rows == 2 { // cancel on the last row: the stream then ends cleanly
				cancel()
			}
			return nil
		})
	if err == nil {
		t.Fatalf("canceled preview must report the context error, not nil: %+v", summary)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("must satisfy errors.Is(context.Canceled): %v", err)
	}
	if summary.Complete || summary.Truncated {
		t.Fatalf("post-stream cancellation must clear both markers: %+v", summary)
	}
}
