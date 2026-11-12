package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// TestBackendNeutralDiagnosticsAndPlanIdentity is the cross-backend witness
// for one diagnostic meaning and two identity families:
//
//   - the public planFingerprint (Explain output and PreviewSummary) is the
//     value-free SourcePlanFingerprint on both lanes, and Explain and
//     preview report identical bytes for the same plan;
//   - the value-bound artifact identity (query.PlanDiagnostics.Fingerprint,
//     which feeds chartifact.Identity.PlanDigest) moves with bound values
//     while the public diagnostic shape does not;
//   - one internal cursor timer produces literal, materialization-excluded
//     source-query durations for both the AQL and PostgreSQL adapters under
//     a controlled clock, with cancellation/error accounting intact.
//
// Identity-interchange sabotage (bound digest projected publicly, or the
// value-free shape used as the artifact key) fails the equality and
// distinctness assertions below, not just a golden.
func TestBackendNeutralDiagnosticsAndPlanIdentity(t *testing.T) {
	ctx := context.Background()

	arch12PGCompile := func(t *testing.T, resolved Resolved, limit int) compiler.CompiledQuery {
		t.Helper()
		query, err := compiler.CompileRecipeOutput(exec02PostgreSQLTarget(t), resolved.Compiled.Outputs[0], resolved.Semantic.SemanticPlan.Bindings, limit, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("postgresql reference compile: %v", err)
		}
		if query.Command.Backend() != command.CompiledBackendPostgreSQL {
			t.Fatalf("reference command tag = %q", query.Command.Backend())
		}
		return query
	}
	arch12AQLCompile := func(t *testing.T, resolved Resolved, limit int) compiler.CompiledQuery {
		t.Helper()
		query, err := compiler.CompileRecipeOutput(compiler.ArangoTarget(), resolved.Compiled.Outputs[0], resolved.Semantic.SemanticPlan.Bindings, limit, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("aql reference compile: %v", err)
		}
		if query.Command.Backend() != command.CompiledBackendAQL {
			t.Fatalf("reference command tag = %q", query.Command.Backend())
		}
		return query
	}

	t.Run("postgresql diagnostic shape ignores bound values while artifact identity follows them", func(t *testing.T) {
		resolved := exec02TestResolved()
		baseline := arch12PGCompile(t, resolved, 5)
		shape := SourcePlanFingerprint(baseline)
		bound := baseline.PlanDiagnostics.Fingerprint
		if shape == "" || bound == "" {
			t.Fatalf("baseline digests must be present: shape=%q bound=%q", shape, bound)
		}
		if shape == bound {
			t.Fatal("the value-free diagnostic shape must not be the value-bound artifact digest")
		}
		if again := SourcePlanFingerprint(arch12PGCompile(t, resolved, 5)); again != shape {
			t.Fatalf("diagnostic shape is not deterministic: %q vs %q", again, shape)
		}
		// Bound-value-only change (scope argument rebound): the public
		// diagnostic shape stays byte-identical while the value-bound
		// artifact identity moves. If diagnostic and artifact identity
		// were interchanged this is the assertion that fails.
		scopeChange := exec02TestResolved()
		scopeChange.Compiled.Outputs[0].Plan.BindVars["project"] = "P2"
		rebound := arch12PGCompile(t, scopeChange, 5)
		if got := SourcePlanFingerprint(rebound); got != shape {
			t.Fatalf("bound-value-only change moved the diagnostic shape: %q vs %q", got, shape)
		}
		if rebound.PlanDiagnostics.Fingerprint == bound {
			t.Fatal("bound-value-only change left the value-bound artifact digest unchanged")
		}
		// SQL/text change, parameter-type change, and output-metadata
		// change each move the diagnostic shape.
		sqlTextA, _, err := baseline.Command.PostgreSQLParameters()
		if err != nil {
			t.Fatal(err)
		}
		remapped, err := command.NewPostgreSQLCommand(sqlTextA+" ", nil)
		if err != nil {
			t.Fatal(err)
		}
		shapeQuery := compiler.CompiledQuery{Command: mustPGCommand(t, sqlTextA, []command.PostgreSQLParameter{
			{Type: command.PostgreSQLParameterText, Value: "abc"},
			{Type: command.PostgreSQLParameterBigint, Value: int64(1)},
		}), Columns: []string{"id", "name"}, PublicColumns: []string{"id", "name"}}
		if SourcePlanFingerprint(shapeQuery) == SourcePlanFingerprint(compiler.CompiledQuery{Command: remapped, Columns: []string{"id", "name"}, PublicColumns: []string{"id", "name"}}) {
			t.Fatal("statement SQL change did not move the diagnostic shape")
		}
		valueSwap := compiler.CompiledQuery{Command: mustPGCommand(t, sqlTextA, []command.PostgreSQLParameter{
			{Type: command.PostgreSQLParameterText, Value: "totally-different"},
			{Type: command.PostgreSQLParameterBigint, Value: int64(999)},
		}), Columns: []string{"id", "name"}, PublicColumns: []string{"id", "name"}}
		if SourcePlanFingerprint(valueSwap) != SourcePlanFingerprint(shapeQuery) {
			t.Fatal("bound values leaked into the PostgreSQL diagnostic shape")
		}
		typeSwap := compiler.CompiledQuery{Command: mustPGCommand(t, sqlTextA, []command.PostgreSQLParameter{
			{Type: command.PostgreSQLParameterText, Value: "abc"},
			{Type: command.PostgreSQLParameterJSONB, Value: jsonRaw(t)},
		}), Columns: []string{"id", "name"}, PublicColumns: []string{"id", "name"}}
		if SourcePlanFingerprint(typeSwap) == SourcePlanFingerprint(shapeQuery) {
			t.Fatal("ordered parameter type change did not move the diagnostic shape")
		}
		publicChange := shapeQuery
		publicChange.PublicColumns = []string{"id"}
		if SourcePlanFingerprint(publicChange) == SourcePlanFingerprint(shapeQuery) {
			t.Fatal("public output metadata change did not move the diagnostic shape")
		}
		pivotChange := shapeQuery
		pivotChange.PivotFields = []string{"category"}
		if SourcePlanFingerprint(pivotChange) == SourcePlanFingerprint(shapeQuery) {
			t.Fatal("pivot metadata change did not move the diagnostic shape")
		}
		// Projection-level interchange guard: the preview summary and the
		// Explain output report the shape, never the bound artifact digest.
		spies := &dispatchSpies{}
		engine, _ := exec02pgEngine(t, spies, exec02Rows())
		limited := exec02TestResolved()
		limited.Semantic.SemanticPlan.Bindings.PreviewLimit = 5
		summary, err := engine.PreviewOutput(ctx, limited, PreviewRequest{Output: "resources", Limit: 5}, func(map[string]any) error { return nil })
		if err != nil {
			t.Fatalf("postgresql PreviewOutput: %v", err)
		}
		control := Control{Engine: engine}
		explanation, err := control.ExplainResolved(ctx, limited, false)
		if err != nil {
			t.Fatalf("postgresql ExplainResolved: %v", err)
		}
		wantShape := SourcePlanFingerprint(arch12PGCompile(t, limited, 5))
		if summary.PlanFingerprint != wantShape {
			t.Fatalf("preview summary must report the value-free shape: got %q want %q", summary.PlanFingerprint, wantShape)
		}
		if summary.PlanFingerprint == bound {
			t.Fatal("preview summary leaked the value-bound artifact digest as the public plan fingerprint")
		}
		if explanation.Outputs[0].PlanFingerprint != summary.PlanFingerprint {
			t.Fatalf("Explain and preview diverged for the same postgresql plan: explain=%q preview=%q", explanation.Outputs[0].PlanFingerprint, summary.PlanFingerprint)
		}
	})

	t.Run("postgresql pinned diagnostic shape bytes", func(t *testing.T) {
		// The versioned domain-separated encoding (loom:source-plan-shape:v1
		// inside the hash input, 64-hex output) is pinned so a careless
		// change to the field set or framing cannot pass unnoticed.
		query := compiler.CompiledQuery{Command: mustPGCommand(t, "SELECT id, name FROM loom.fhir_resource WHERE project = $1 AND dataset_generation = $2", []command.PostgreSQLParameter{
			{Type: command.PostgreSQLParameterText, Value: "P1"},
			{Type: command.PostgreSQLParameterText, Value: "G1"},
		}), Columns: []string{"id", "name"}, PublicColumns: []string{"id", "name"}}
		if got := SourcePlanFingerprint(query); got != "86d795c00159019f31b5270e5b4f20b4406e939475e3980646866d513427394d" {
			t.Fatalf("postgres diagnostic shape digest drifted from the pinned bytes: got %s", got)
		}
	})

	t.Run("aql public bytes are the historical shape and exclude bind values", func(t *testing.T) {
		resolved := exec02TestResolved()
		baseline := arch12AQLCompile(t, resolved, 5)
		shape := SourcePlanFingerprint(baseline)
		if shape != CompiledQueryFingerprint(baseline) {
			t.Fatal("the AQL public diagnostic fingerprint must remain the historical CompiledQueryFingerprint bytes")
		}
		bound := baseline.PlanDiagnostics.Fingerprint
		if shape == "" || bound == "" || shape == bound {
			t.Fatalf("aql shape and bound artifact digest must both exist and differ: shape=%q bound=%q", shape, bound)
		}
		// Bound-value-only change: bind VALUES stay out of the public shape
		// while the value-bound physical digest follows them.
		scopeChange := exec02TestResolved()
		scopeChange.Compiled.Outputs[0].Plan.BindVars["project"] = "P2"
		rebound := arch12AQLCompile(t, scopeChange, 5)
		if got := SourcePlanFingerprint(rebound); got != shape {
			t.Fatalf("AQL bound-value-only change moved the public shape: %q vs %q", got, shape)
		}
		if rebound.PlanDiagnostics.Fingerprint == bound {
			t.Fatal("AQL bound-value-only change left the value-bound artifact digest unchanged")
		}
		// Output metadata participates.
		metadataChange := baseline
		metadataChange.Columns = append([]string(nil), baseline.Columns...)
		metadataChange.Columns[0] = "identifier"
		if SourcePlanFingerprint(metadataChange) == shape {
			t.Fatal("AQL output column change did not move the public shape")
		}
		// Preview and Explain report the same public shape per lane, and
		// the preview summary never carries the bound artifact digest.
		spies := &dispatchSpies{}
		config := spies.arangoEngine()
		engine := config.newEngineForTest(t)
		limited := exec02TestResolved()
		limited.Semantic.SemanticPlan.Bindings.PreviewLimit = 5
		summary, err := engine.PreviewOutput(ctx, limited, PreviewRequest{Output: "resources", Limit: 5}, func(map[string]any) error { return nil })
		if err != nil {
			t.Fatalf("aql PreviewOutput: %v", err)
		}
		control := Control{Engine: engine}
		explanation, err := control.ExplainResolved(ctx, limited, false)
		if err != nil {
			t.Fatalf("aql ExplainResolved: %v", err)
		}
		if summary.PlanFingerprint == "" || summary.PlanFingerprint != explanation.Outputs[0].PlanFingerprint {
			t.Fatalf("aql Explain and preview diverged: explain=%q preview=%q", explanation.Outputs[0].PlanFingerprint, summary.PlanFingerprint)
		}
		if summary.PlanFingerprint == bound {
			t.Fatal("aql preview summary leaked the value-bound artifact digest as the public plan fingerprint")
		}
		if want := CompiledQueryFingerprint(arch12AQLCompile(t, limited, 5)); summary.PlanFingerprint != want {
			t.Fatalf("aql preview summary must report the historical AQL shape bytes: got %q want %q", summary.PlanFingerprint, want)
		}
	})

	t.Run("one cursor interval excludes materialization for both adapters", func(t *testing.T) {
		const (
			cursorQuantum       = 5 * time.Second
			rowMaterialization  = 400 * time.Millisecond
			arch12RowsPerStream = 2
		)
		for _, lane := range []struct {
			name   string
			tag    command.CompiledBackend
			ported func(RowsRequest, RowVisitor, *arch12Clock) error
		}{
			{
				name: "AQL", tag: command.CompiledBackendAQL,
				ported: func(request RowsRequest, visit RowVisitor, clock *arch12Clock) error {
					if _, _, err := request.Command.AQLParameters(); err != nil {
						return err
					}
					clock.advance(cursorQuantum)
					for range arch12RowsPerStream {
						if err := visit(map[string]any{"id": "row"}); err != nil {
							return err
						}
					}
					return nil
				},
			},
			{
				name: "PostgreSQL", tag: command.CompiledBackendPostgreSQL,
				ported: func(request RowsRequest, visit RowVisitor, clock *arch12Clock) error {
					if _, _, err := request.Command.PostgreSQLParameters(); err != nil {
						return err
					}
					clock.advance(cursorQuantum)
					for range arch12RowsPerStream {
						if err := visit(map[string]any{"id": "row"}); err != nil {
							return err
						}
					}
					return nil
				},
			},
		} {
			t.Run(lane.name, func(t *testing.T) {
				clock := newArch12Clock()
				restore := useArch12Clock(t, clock)
				defer restore()
				compiled := compiler.CompiledQuery{
					Columns: []string{"id"}, PublicColumns: []string{"id"},
					Command: arch12TaggedCommand(t, lane.tag),
				}
				service := NewService(ServiceConfig{SourceRows: func(_ context.Context, request RowsRequest, visit RowVisitor) error {
					return lane.ported(request, visit, clock)
				}})
				streamed, err := service.streamQuery(ctx, compiled, func(map[string]any) error {
					// The caller's row handling is Loom-side work: it must be
					// attributed to row materialization, never to the source.
					clock.advance(rowMaterialization)
					return nil
				})
				if err != nil {
					t.Fatalf("streamQuery: %v", err)
				}
				if got := streamed.Diagnostics.SourceQuery; got != cursorQuantum {
					t.Fatalf("%s source query = %v, want exactly the held cursor quantum %v", lane.name, got, cursorQuantum)
				}
				if got, want := streamed.Diagnostics.RowMaterialization, time.Duration(arch12RowsPerStream)*rowMaterialization; got != want {
					t.Fatalf("%s row materialization = %v, want %v", lane.name, got, want)
				}
				if got := streamed.Diagnostics.SourceBackend; got != lane.tag {
					t.Fatalf("%s source backend tag = %q, want %q", lane.name, got, lane.tag)
				}
			})
		}
	})

	t.Run("cursor interval survives visitor failure and cancellation accounting", func(t *testing.T) {
		const cursorQuantum = 3 * time.Second
		visitorFailure := errors.New("visitor rejected the row")
		for _, lane := range []struct {
			name string
			tag  command.CompiledBackend
		}{
			{name: "AQL", tag: command.CompiledBackendAQL},
			{name: "PostgreSQL", tag: command.CompiledBackendPostgreSQL},
		} {
			t.Run(lane.name, func(t *testing.T) {
				clock := newArch12Clock()
				restore := useArch12Clock(t, clock)
				defer restore()
				compiled := compiler.CompiledQuery{Columns: []string{"id"}, Command: arch12TaggedCommand(t, lane.tag)}
				failing := NewService(ServiceConfig{SourceRows: func(_ context.Context, request RowsRequest, visit RowVisitor) error {
					clock.advance(cursorQuantum)
					if err := visit(map[string]any{"id": "row"}); err != nil {
						return err
					}
					return nil
				}})
				_, err := failing.streamQuery(ctx, compiled, func(map[string]any) error {
					clock.advance(250 * time.Millisecond)
					return visitorFailure
				})
				if !errors.Is(err, visitorFailure) {
					t.Fatalf("visitor failure must surface unchanged: %v", err)
				}
				// A canceled caller must not open a backend cursor at all and
				// reports zero source time rather than a stale interval.
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				blocked := false
				cancelling := NewService(ServiceConfig{SourceRows: func(context.Context, RowsRequest, RowVisitor) error {
					blocked = true
					return nil
				}})
				streamed, err := cancelling.streamQuery(canceled, compiled, func(map[string]any) error { return nil })
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled run = %v, want context.Canceled", err)
				}
				if blocked {
					t.Fatal("a canceled caller opened a backend cursor")
				}
				if streamed.Diagnostics.SourceQuery != 0 || streamed.Diagnostics.RowMaterialization != 0 {
					t.Fatalf("canceled run must report zero source accounting, got %+v", streamed.Diagnostics)
				}
			})
		}
	})

	t.Run("preview source duration excludes row delivery for both lanes", func(t *testing.T) {
		const (
			cursorQuantum = 3 * time.Second
			rowDelivery   = 250 * time.Millisecond
		)
		for _, lane := range []struct {
			name string
			pg   bool
		}{
			{name: "AQL"},
			{name: "PostgreSQL", pg: true},
		} {
			t.Run(lane.name, func(t *testing.T) {
				clock := newArch12Clock()
				restore := useArch12Clock(t, clock)
				defer restore()
				spies := &dispatchSpies{}
				config := spies.arangoEngine()
				port := func(_ context.Context, request RowsRequest, visit RowVisitor) error {
					if lane.pg {
						if _, _, err := request.Command.PostgreSQLParameters(); err != nil {
							return err
						}
					} else if _, _, err := request.Command.AQLParameters(); err != nil {
						return err
					}
					clock.advance(cursorQuantum)
					for _, row := range exec02Rows() {
						if err := visit(row); err != nil {
							return err
						}
					}
					return nil
				}
				if lane.pg {
					config.CompileTarget = exec02PostgreSQLTarget(t)
				}
				config.SourceRows = port
				config.PreviewSourceRows = port
				engine := config.newEngineForTest(t)
				limited := exec02TestResolved()
				limited.Semantic.SemanticPlan.Bindings.PreviewLimit = 10
				summary, err := engine.PreviewOutput(ctx, limited, PreviewRequest{Output: "resources", Limit: 10}, func(map[string]any) error {
					clock.advance(rowDelivery)
					return nil
				})
				if err != nil {
					t.Fatalf("%s PreviewOutput: %v", lane.name, err)
				}
				if summary.RowCount != 2 {
					t.Fatalf("%s preview row count = %d, want 2", lane.name, summary.RowCount)
				}
				if got, want := summary.SourceQueryDuration, cursorQuantum; got != want {
					t.Fatalf("%s preview source duration = %v, want the cursor quantum %v with row delivery excluded", lane.name, got, want)
				}
			})
		}
	})

	t.Run("clickhouse combine preview keeps its ClickHouse-owned digest", func(t *testing.T) {
		// The unified source shape applies to source commands only; the
		// ClickHouse combine preview summary keeps reporting the combine
		// fingerprint it carried before this ticket.
		query := compiler.CompiledQuery{PlanMode: "clickhouse", PlanProfile: "pinned_table_combine", Limit: 2, PlanDiagnostics: ir.CompilerPlanDiagnostics{Fingerprint: "combine-fingerprint"}}
		stream := OutputStream{physicalEngine: ir.PhysicalEngineClickHouse}
		if got := previewPlanFingerprint(query, stream); got != "combine-fingerprint" {
			t.Fatalf("clickhouse combine preview fingerprint = %q, want the combine digest", got)
		}
	})
}

// arch12Clock is a manually advanced clock behind the package clock seam.
type arch12Clock struct {
	current time.Time
}

func newArch12Clock() *arch12Clock {
	return &arch12Clock{current: time.Date(2026, time.November, 12, 9, 0, 0, 0, time.UTC)}
}

func (c *arch12Clock) now() time.Time { return c.current }

func (c *arch12Clock) advance(d time.Duration) { c.current = c.current.Add(d) }

func useArch12Clock(t *testing.T, clock *arch12Clock) func() {
	t.Helper()
	previous := now
	now = clock.now
	return func() { now = previous }
}

func arch12TaggedCommand(t *testing.T, tag command.CompiledBackend) command.CompiledCommand {
	t.Helper()
	switch tag {
	case command.CompiledBackendAQL:
		return mustAQLCommand("FOR d IN Patient FILTER d.project == @project RETURN d", map[string]any{"project": "P1"})
	case command.CompiledBackendPostgreSQL:
		return mustPGCommand(t, "SELECT id FROM loom.fhir_resource WHERE project = $1", []command.PostgreSQLParameter{{Type: command.PostgreSQLParameterText, Value: "P1"}})
	default:
		t.Fatalf("unsupported tag %q", tag)
		return command.CompiledCommand{}
	}
}

func mustPGCommand(t *testing.T, sqlText string, args []command.PostgreSQLParameter) command.CompiledCommand {
	t.Helper()
	cmd, err := command.NewPostgreSQLCommand(sqlText, args)
	if err != nil {
		t.Fatalf("build PostgreSQL command: %v", err)
	}
	return cmd
}

func jsonRaw(t *testing.T) []byte {
	t.Helper()
	return []byte(`{"k":1}`)
}
