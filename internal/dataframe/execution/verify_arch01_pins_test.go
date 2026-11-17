package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
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
		CompileTarget: compiler.ArangoTarget(),
		Registry:      invalidRecipeRegistry{},
		ScopeDigest:   func(recipe.RuntimeBindings) string { return "verify-arch01-scope" },
		SourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
			command := request.Command
			text, _, err := command.AQLParameters()
			if err != nil {
				return err
			}
			v.add(&v.aqlFull, text)
			return nil
		},
		PreviewSourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
			command := request.Command
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
		PreviewCoveringIndexes: func(context.Context, string) ([]PreviewCollectionIndex, error) {
			v.add(&v.controls, "previewCoveringIndexes")
			return nil, nil
		},
		PreviewCollectionRevision: func(context.Context, string) (string, error) {
			v.add(&v.controls, "previewCollectionRevision")
			return "rev-arch01", nil
		},
		PreviewCollectionScans: func(context.Context, command.CompiledCommand, string) (PreviewCollectionScanEvidence, error) {
			v.add(&v.controls, "previewCollectionScans")
			return PreviewCollectionScanEvidence{}, nil
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

func verifyArch01Command(t *testing.T) command.CompiledCommand {
	t.Helper()
	compiledCommand, err := command.NewPostgreSQLCommand(
		`SELECT id FROM loom.resource WHERE project = $1`,
		[]command.PostgreSQLParameter{{Type: command.PostgreSQLParameterText, Value: "P1"}})
	if err != nil {
		t.Fatalf("forge command: %v", err)
	}
	return compiledCommand
}

func verifyArch01PostgreSQLPort(v *verifyArch01Trace, target *[]string, rows []map[string]any) SourceRows {
	return func(ctx context.Context, request RowsRequest, visit RowVisitor) error {
		sqlText, _, err := request.Command.PostgreSQLParameters()
		if err != nil {
			v.add(&v.aqlFull, "misrouted:"+err.Error())
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

func TestVerifyArch01ResolverPanicFailsClosedNoCallbacks(t *testing.T) {
	v := &verifyArch01Trace{}
	config := verifyArch01Config(v)
	config.CompileTarget = verifyArch01PostgreSQLTarget(t)
	config.SourceRows = func(context.Context, RowsRequest, RowVisitor) error {
		panic("resolver exploded")
	}
	config.PreviewSourceRows = nil
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatalf("port panic was swallowed; engine must not silently continue")
		}
		if msg := fmt.Sprint(recovered); !strings.Contains(msg, "resolver exploded") {
			t.Fatalf("panic identity lost: %v", recovered)
		}
		aqlFull, aqlPreview, pgFull, _, controls, ch := v.snapshot()
		if len(aqlFull) != 0 || len(aqlPreview) != 0 || len(pgFull) != 0 || len(controls) != 0 || ch != 0 {
			t.Fatalf("port panic touched adapters: aql=%v preview=%v pg=%d controls=%v ch=%d",
				aqlFull, aqlPreview, len(pgFull), controls, ch)
		}
	}()
	_, err = engine.Run(context.Background(), verifyArch01PostgreSQLResolved("resources", []string{"id"}))
	t.Fatalf("panic must propagate; Run returned err=%v", err)
}

func TestVerifyArch01VisitorErrorClearsMarkers(t *testing.T) {
	v := &verifyArch01Trace{}
	config := verifyArch01Config(v)
	config.CompileTarget = verifyArch01PostgreSQLTarget(t)
	config.SourceRows = verifyArch01PostgreSQLPort(v, &v.pgFull, verifyArch01Rows())
	config.PreviewSourceRows = nil
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	visitorFailure := errors.New("verify_arch01 visitor refused")
	summary, err := engine.PreviewOutput(context.Background(), verifyArch01PostgreSQLResolved("resources", []string{"id"}),
		PreviewRequest{Output: "resources", Limit: 10}, func(map[string]any) error { return visitorFailure })
	if !errors.Is(err, visitorFailure) {
		t.Fatalf("visitor failure must surface: %v", err)
	}
	if summary.Complete || summary.Truncated {
		t.Fatalf("visitor-interrupted preview must carry neither marker: %+v", summary)
	}
	v.quietAQL(t, "visitor error")
}

func TestVerifyArch01ContextErrorAfterFinalRowClearsMarkers(t *testing.T) {
	v := &verifyArch01Trace{}
	config := verifyArch01Config(v)
	config.CompileTarget = verifyArch01PostgreSQLTarget(t)
	config.SourceRows = verifyArch01PostgreSQLPort(v, &v.pgFull, verifyArch01Rows())
	config.PreviewSourceRows = nil
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rows := 0
	summary, err := engine.PreviewOutput(ctx, verifyArch01PostgreSQLResolved("resources", []string{"id"}),
		PreviewRequest{Output: "resources", Limit: 10}, func(map[string]any) error {
			rows++
			if rows == 2 {
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
