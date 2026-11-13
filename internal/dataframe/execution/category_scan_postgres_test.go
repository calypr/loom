package execution_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	exec "github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

type exec06Call struct {
	sql      string
	args     []command.PostgreSQLParameter
	declared []string
	tag      command.CompiledBackend
}

type exec06Port struct {
	calls       []exec06Call
	rows        []map[string]any
	visitBefore func(index int) error
	fail        error
}

func (p *exec06Port) port(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
	if p.fail != nil {
		return p.fail
	}
	var call exec06Call
	switch request.Command.Backend() {
	case command.CompiledBackendPostgreSQL:
		sqlText, args, err := request.Command.PostgreSQLParameters()
		if err != nil {
			return err
		}
		if len(request.DeclaredColumns) != 2 || request.DeclaredColumns[0] != "present" || request.DeclaredColumns[1] != "value" {
			return errors.New("fake PostgreSQL port: declared column contract must be exactly [present value]")
		}
		call = exec06Call{sql: sqlText, args: args, declared: append([]string(nil), request.DeclaredColumns...), tag: request.Command.Backend()}
	case command.CompiledBackendAQL:
		if _, _, err := request.Command.AQLParameters(); err != nil {
			return err
		}
		if len(request.DeclaredColumns) != 0 {
			return errors.New("fake port: the AQL arm must stay contract-free")
		}
		call = exec06Call{tag: request.Command.Backend()}
	default:
		return command.ErrCompiledCommandBackendNotRegistered
	}
	p.calls = append(p.calls, call)
	for index, row := range p.rows {
		if p.visitBefore != nil {
			if err := p.visitBefore(index); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(row); err != nil {
			return err
		}
	}
	return nil
}

func (p *exec06Port) counts() int { return len(p.calls) }

func exec06JSONB(t *testing.T, text string) json.RawMessage {
	t.Helper()
	if !json.Valid([]byte(text)) {
		t.Fatalf("fixture is not valid JSON: %s", text)
	}
	return json.RawMessage(text)
}

func exec06PGCommand(t *testing.T, bound int) command.CompiledCommand {
	t.Helper()
	sqlText := `WITH __loom_category_source AS (SELECT $1::jsonb AS "c0", $2::jsonb AS "c1"), ` +
		`__loom_category_rows AS (SELECT __loom_category_row."c1" AS "present", CASE WHEN __loom_category_row."c1" THEN __loom_category_row."c0" END AS "value" FROM __loom_category_source AS __loom_category_row), ` +
		`__loom_category_distinct AS (SELECT "present", "value" FROM __loom_category_rows GROUP BY "present", "value") ` +
		`SELECT "present" AS "c0", "value" AS "c1" FROM __loom_category_distinct ORDER BY "present" ASC LIMIT $3`
	compiled, err := command.NewPostgreSQLCommand(sqlText, []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterJSONB, Value: `{"gender":"female"}`},
		{Type: command.PostgreSQLParameterBoolean, Value: true},
		{Type: command.PostgreSQLParameterBigint, Value: int64(bound)},
	})
	if err != nil {
		t.Fatalf("tag PostgreSQL category command: %v", err)
	}
	return compiled
}

func exec06AQLCommand(t *testing.T) command.CompiledCommand {
	t.Helper()
	compiled, err := command.NewAQLCommand("FOR row IN Patients RETURN { present: true, value: row.gender }", map[string]any{"__loom_category_limit": 9})
	if err != nil {
		t.Fatalf("tag AQL category command: %v", err)
	}
	return compiled
}

func exec06Compiled(t *testing.T, backend command.CompiledBackend, maxValues int) compiler.CompiledCategoryScanQuery {
	t.Helper()
	var cmd command.CompiledCommand
	switch backend {
	case command.CompiledBackendPostgreSQL:
		cmd = exec06PGCommand(t, maxValues+1)
	case command.CompiledBackendAQL:
		cmd = exec06AQLCommand(t)
	default:
		t.Fatalf("test helper does not build %q commands", backend)
	}
	return compiler.CompiledCategoryScanQuery{
		Command:       cmd,
		PresentColumn: "present", ValueColumn: "value",
		Proof: compiler.CategoryScanProof{Version: 1, Output: "patients", Column: "gender", MaxValues: maxValues, PresenceTracked: true, Fingerprint: "exec06-proof"},
	}
}

func exec06Target(t *testing.T, backend command.CompiledBackend) compiler.Target {
	t.Helper()
	switch backend {
	case command.CompiledBackendPostgreSQL:
		binding, err := ir.NewPostgresStorageBinding("verify_exec06_offline")
		if err != nil {
			t.Fatalf("storage binding: %v", err)
		}
		target, err := compiler.PostgreSQLTarget(binding)
		if err != nil {
			t.Fatalf("PostgreSQL target: %v", err)
		}
		return target
	case command.CompiledBackendAQL:
		return compiler.ArangoTarget()
	default:
		t.Fatalf("test helper does not build %q targets", backend)
		return compiler.Target{}
	}
}

type exec06RefusingRegistry struct{}

func (exec06RefusingRegistry) LoadRecipe(context.Context, string) (exec.Entry, error) {
	return exec.Entry{}, errors.New("the exec06 offline battery compiles authored bundles only")
}

func (exec06RefusingRegistry) LoadRecipeVersion(context.Context, string, string) (exec.Entry, error) {
	return exec.Entry{}, errors.New("the exec06 offline battery compiles authored bundles only")
}

func exec06Landmines() dataframeexecution.Config {
	return dataframeexecution.Config{
		Registry: exec06RefusingRegistry{},
		SourceRows: func(_ context.Context, request dataframeexecution.RowsRequest, _ dataframeexecution.RowVisitor) error {
			if _, _, err := request.Command.AQLParameters(); err != nil {
				return err
			}
			return errors.New("AQL QueryRows must never run for a PostgreSQL-served category scan")
		},
		PreviewSourceRows: func(_ context.Context, request dataframeexecution.RowsRequest, _ dataframeexecution.RowVisitor) error {
			if _, _, err := request.Command.AQLParameters(); err != nil {
				return err
			}
			return errors.New("AQL PreviewQueryRows must never run for a PostgreSQL-served category scan")
		},
		PreparePreviewIndex: func(context.Context, compiler.PreviewCoveringIndexSpec) error {
			return errors.New("preparePreviewIndex must never run for a PostgreSQL-served category scan")
		},
		PreviewCoveringIndexes: func(context.Context, string) ([]dataframeexecution.PreviewCollectionIndex, error) {
			return nil, errors.New("previewCoveringIndexes must never run for a PostgreSQL-served category scan")
		},
		PreviewCollectionRevision: func(context.Context, string) (string, error) {
			return "", errors.New("previewCollectionRevision must never run for a PostgreSQL-served category scan")
		},
		PreviewCollectionScans: func(context.Context, command.CompiledCommand, string) (dataframeexecution.PreviewCollectionScanEvidence, error) {
			return dataframeexecution.PreviewCollectionScanEvidence{}, errors.New("previewCollectionScans must never run for a PostgreSQL-served category scan")
		},
		PreviewCollectionCount: func(context.Context, string) (int64, error) {
			return 0, errors.New("previewCollectionCount must never run for a PostgreSQL-served category scan")
		},
		ClickHouseQueryRows: func(context.Context, string, []string, func(map[string]any) error, ...any) error {
			return errors.New("ClickHouse callbacks must never run for a PostgreSQL-served category scan")
		},
	}
}

func exec06Engine(t *testing.T, backend command.CompiledBackend, port *exec06Port) *dataframeexecution.Engine {
	t.Helper()
	config := exec06Landmines()
	config.CompileTarget = exec06Target(t, backend)
	config.PreviewSourceRows = nil
	config.SourceRows = port.port
	engine, err := dataframeexecution.New(config)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	return engine
}

func TestExec06PGCategoryExactValuesDecode(t *testing.T) {
	port := &exec06Port{rows: []map[string]any{
		{"present": false, "value": nil},
		{"present": true, "value": exec06JSONB(t, "false")},
		{"present": true, "value": exec06JSONB(t, "null")},
		{"present": true, "value": exec06JSONB(t, "0")},
		{"present": true, "value": exec06JSONB(t, `""`)},
		{"present": true, "value": exec06JSONB(t, `"female"`)},
		{"present": true, "value": exec06JSONB(t, `"male"`)},
	}}
	engine := exec06Engine(t, command.CompiledBackendPostgreSQL, port)
	result, err := engine.ScanCategoriesCompiled(context.Background(), exec06Compiled(t, command.CompiledBackendPostgreSQL, 8))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := []dataframeexecution.CategoryValue{
		{Present: false, Value: nil},
		{Present: true, Value: false},
		{Present: true, Value: nil},
		{Present: true, Value: float64(0)},
		{Present: true, Value: ""},
		{Present: true, Value: "female"},
		{Present: true, Value: "male"},
	}
	if len(result.Values) != len(want) {
		t.Fatalf("values = %#v, want %#v", result.Values, want)
	}
	for index := range want {
		if result.Values[index] != want[index] {
			t.Fatalf("value %d = %#v, want %#v", index, result.Values[index], want[index])
		}
	}
	if !result.Complete || result.Overflow || result.ConclusiveMissing {
		t.Fatalf("status = Complete:%v Overflow:%v ConclusiveMissing:%v, want complete, not overflow, not conclusive-missing", result.Complete, result.Overflow, result.ConclusiveMissing)
	}
	if result.Proof.Fingerprint != "exec06-proof" {
		t.Fatalf("proof not echoed: %#v", result.Proof)
	}
	if port.counts() != 1 {
		t.Fatalf("port calls = %d, want exactly one bounded statement", port.counts())
	}
	call := port.calls[0]
	if call.tag != command.CompiledBackendPostgreSQL {
		t.Fatalf("tag = %q, want POSTGRESQL", call.tag)
	}
	if len(call.declared) != 2 || call.declared[0] != "present" || call.declared[1] != "value" {
		t.Fatalf("declared columns = %v, want [present value]", call.declared)
	}
	if !strings.Contains(call.sql, "LIMIT $3") || strings.Contains(call.sql, "@") {
		t.Fatalf("statement is not the positional bounded page: %s", call.sql)
	}
	if call.args[len(call.args)-1].Value != int64(9) {
		t.Fatalf("bound argument = %+v, want bigint 9 (maxValues+1)", call.args[len(call.args)-1])
	}
}

func TestExec06PGCategoryOverflowCapsPageAtBound(t *testing.T) {
	port := &exec06Port{rows: []map[string]any{
		{"present": true, "value": exec06JSONB(t, `"a"`)},
		{"present": true, "value": exec06JSONB(t, `"b"`)},
		{"present": true, "value": exec06JSONB(t, `"c"`)},
	}}
	engine := exec06Engine(t, command.CompiledBackendPostgreSQL, port)
	result, err := engine.ScanCategoriesCompiled(context.Background(), exec06Compiled(t, command.CompiledBackendPostgreSQL, 2))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(result.Values) != 2 || result.Values[0].Value != "a" || result.Values[1].Value != "b" {
		t.Fatalf("values = %#v, want the first two capped", result.Values)
	}
	if result.Complete || !result.Overflow {
		t.Fatalf("status = Complete:%v Overflow:%v, want Complete=false Overflow=true", result.Complete, result.Overflow)
	}
	if port.counts() != 1 {
		t.Fatalf("port calls = %d, want one — truncation is page-owned, no second query", port.counts())
	}
}

func TestExec06PGCategoryDecodeFailuresFailLoud(t *testing.T) {
	cases := []struct {
		name     string
		row      map[string]any
		contains string
	}{
		{"empty JSONB bytes", map[string]any{"present": true, "value": json.RawMessage(nil)}, "invalid JSONB value"},
		{"malformed JSONB bytes", map[string]any{"present": true, "value": json.RawMessage(`{oops`)}, "invalid JSONB value"},
		{"Go-native string value", map[string]any{"present": true, "value": "secret-value"}, "raw JSON bytes"},
		{"Go-native number value", map[string]any{"present": true, "value": int64(3)}, "raw JSON bytes"},
		{"missing value key", map[string]any{"present": true}, "missing value"},
		{"missing presence marker", map[string]any{"value": exec06JSONB(t, `"a"`)}, "invalid presence marker"},
		{"non-bool presence marker", map[string]any{"present": "true", "value": exec06JSONB(t, `"a"`)}, "invalid presence marker"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			port := &exec06Port{rows: []map[string]any{
				{"present": true, "value": exec06JSONB(t, `"female"`)},
				test.row,
			}}
			engine := exec06Engine(t, command.CompiledBackendPostgreSQL, port)
			result, err := engine.ScanCategoriesCompiled(context.Background(), exec06Compiled(t, command.CompiledBackendPostgreSQL, 8))
			if err == nil {
				t.Fatalf("malformed row accepted: result %#v", result)
			}
			if !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v, want it to mention %q", err, test.contains)
			}
			if strings.Contains(err.Error(), "secret-value") || strings.Contains(err.Error(), "{oops") {
				t.Fatalf("error leaked row value text: %v", err)
			}
			if result.Complete || result.Overflow || result.ConclusiveMissing || len(result.Values) != 0 {
				t.Fatalf("failed scan must carry a zero result, got %#v", result)
			}
		})
	}
}

func TestExec06PGCategoryWitnessAndForeignTagRefusedBeforePort(t *testing.T) {
	t.Run("hand-attached witness refused", func(t *testing.T) {
		port := &exec06Port{}
		engine := exec06Engine(t, command.CompiledBackendPostgreSQL, port)
		compiled := exec06Compiled(t, command.CompiledBackendPostgreSQL, 8)
		witnessCommand, err := command.NewAQLCommand("RETURN {present: false, value: null}", nil)
		if err != nil {
			t.Fatalf("tag witness: %v", err)
		}
		compiled.OverflowWitness = &compiler.CategoryOverflowWitness{Command: witnessCommand}
		result, err := engine.ScanCategoriesCompiled(context.Background(), compiled)
		if err == nil || !strings.Contains(err.Error(), "overflow witness") {
			t.Fatalf("witness-carrying PostgreSQL command = (%#v, %v), want a typed witness refusal", result, err)
		}
		if result.Complete || len(result.Values) != 0 {
			t.Fatalf("refused scan must be a zero result, got %#v", result)
		}
		if port.counts() != 0 {
			t.Fatalf("port calls = %d, want zero before any witness dispatch", port.counts())
		}
	})
	t.Run("unregistered command tag refused", func(t *testing.T) {
		port := &exec06Port{}
		engine := exec06Engine(t, command.CompiledBackendPostgreSQL, port)
		compiled := exec06Compiled(t, command.CompiledBackendPostgreSQL, 8)
		compiled.Command = command.CompiledCommand{}
		_, err := engine.ScanCategoriesCompiled(context.Background(), compiled)
		if !errors.Is(err, command.ErrCompiledCommandBackendNotRegistered) {
			t.Fatalf("zero-tag command = %v, want ErrCompiledCommandBackendNotRegistered", err)
		}
		if port.counts() != 0 {
			t.Fatalf("port calls = %d, want zero", port.counts())
		}
	})
	t.Run("AQL arm stays contract-free", func(t *testing.T) {
		port := &exec06Port{rows: []map[string]any{{"present": true, "value": "female"}}}
		engine := exec06Engine(t, command.CompiledBackendAQL, port)
		result, err := engine.ScanCategoriesCompiled(context.Background(), exec06Compiled(t, command.CompiledBackendAQL, 8))
		if err != nil {
			t.Fatalf("AQL control scan: %v", err)
		}
		if !result.Complete || len(result.Values) != 1 || result.Values[0] != (dataframeexecution.CategoryValue{Present: true, Value: "female"}) {
			t.Fatalf("AQL control result = %#v", result)
		}
		if port.counts() != 1 || port.calls[0].tag != command.CompiledBackendAQL || len(port.calls[0].declared) != 0 {
			t.Fatalf("AQL arm gained a declared contract: %#v", port.calls)
		}
	})
}

func TestExec06PGCategoryPortErrorsHonestStatuses(t *testing.T) {
	t.Run("generic port failure is a wrapped hard error", func(t *testing.T) {
		port := &exec06Port{fail: errors.New("boom")}
		engine := exec06Engine(t, command.CompiledBackendPostgreSQL, port)
		result, err := engine.ScanCategoriesCompiled(context.Background(), exec06Compiled(t, command.CompiledBackendPostgreSQL, 8))
		if err == nil || !strings.Contains(err.Error(), "execute category scan") || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("port failure = (%#v, %v), want a wrapped hard error", result, err)
		}
		if result.Complete || result.Overflow || len(result.Values) != 0 {
			t.Fatalf("failed scan must be zero-result, got %#v", result)
		}
	})
	t.Run("cancellation never yields a complete result", func(t *testing.T) {
		port := &exec06Port{rows: []map[string]any{
			{"present": true, "value": exec06JSONB(t, `"female"`)},
			{"present": true, "value": exec06JSONB(t, `"male"`)},
		}}
		engine := exec06Engine(t, command.CompiledBackendPostgreSQL, port)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		port.visitBefore = func(index int) error {
			if index == 0 {
				cancel()
			}
			return nil
		}
		result, err := engine.ScanCategoriesCompiled(ctx, exec06Compiled(t, command.CompiledBackendPostgreSQL, 8))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled scan = (%#v, %v), want an errors.Is(context.Canceled) error", result, err)
		}
		if result.Complete || result.Overflow || len(result.Values) != 0 {
			t.Fatalf("cancelled scan must be zero-result and never complete, got %#v", result)
		}
		port.visitBefore = nil
		alive, err := engine.ScanCategoriesCompiled(context.Background(), exec06Compiled(t, command.CompiledBackendPostgreSQL, 8))
		if err != nil {
			t.Fatalf("ALIVE control never converged: %v", err)
		}
		if !alive.Complete || len(alive.Values) != 2 {
			t.Fatalf("ALIVE control result = %#v", alive)
		}
	})
}

func TestExec06PGCategoryStatusInvariants(t *testing.T) {
	port := &exec06Port{rows: []map[string]any{{"present": false, "value": nil}}}
	engine := exec06Engine(t, command.CompiledBackendPostgreSQL, port)
	compiled := exec06Compiled(t, command.CompiledBackendPostgreSQL, 8)
	compiled.CategoryIndex = &compiler.PreviewCoveringIndexSpec{Collection: "Patient", Name: "exec06_sneak", Fields: []string{"payload.gender"}}
	compiled.PreviewCoveringIndex = &compiler.PreviewCoveringIndexSpec{Collection: "Patient", Name: "exec06_sneak_2", Fields: []string{"project", "payload.gender"}}
	result, err := engine.ScanCategoriesCompiled(context.Background(), compiled)
	if err != nil {
		t.Fatalf("scan with hand-attached specs: %v (Arango accelerator callbacks are armed as landmines)", err)
	}
	if result.ConclusiveMissing {
		t.Fatalf("ConclusiveMissing must be unreachable on the PostgreSQL arm")
	}
	if !result.Complete || result.Overflow || len(result.Values) != 1 || result.Values[0] != (dataframeexecution.CategoryValue{Present: false}) {
		t.Fatalf("all-absent page result = %#v, want one complete ABSENT group", result)
	}
	if port.counts() != 1 {
		t.Fatalf("port calls = %d, want one", port.counts())
	}
}

func TestExec06PGCategoryUsesPreviewPortOverride(t *testing.T) {
	preview := &exec06Port{rows: []map[string]any{{"present": true, "value": exec06JSONB(t, `"female"`)}}}
	config := exec06Landmines()
	config.CompileTarget = exec06Target(t, command.CompiledBackendPostgreSQL)
	config.SourceRows = func(context.Context, dataframeexecution.RowsRequest, dataframeexecution.RowVisitor) error {
		return errors.New("the canonical port must not serve a category scan when the preview override is installed")
	}
	config.PreviewSourceRows = preview.port
	engine, err := dataframeexecution.New(config)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	result, err := engine.ScanCategoriesCompiled(context.Background(), exec06Compiled(t, command.CompiledBackendPostgreSQL, 8))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !result.Complete || len(result.Values) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if preview.counts() != 1 {
		t.Fatalf("preview port calls = %d, want one", preview.counts())
	}
}

func exec06ConstructionBundle() recipe.Bundle {
	two := int64(2)
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "exec06_identity",
		TranslationVersion:  "exec06",
		Outputs: []recipe.Output{{
			Name: "trace", RootResourceType: "Observation", RowGrain: "observation",
			RootColumnNaming: recipe.RootColumnNamingExact,
			Fields: []recipe.Field{
				{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.valueInteger"}},
				{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
			},
			Construction: &recipe.Construction{
				Version: 1,
				SourceColumns: []recipe.StageColumn{
					{ID: "amount_id", Name: "amount"}, {ID: "status_id", Name: "status"},
				},
				Steps: []recipe.ConstructionStep{{
					ID: "derive_total", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionDeriveOp, Derive: &recipe.ConstructionDerive{
						ConstructionID: "calc_total", OutputColumnID: "total_id", Operation: recipe.DerivedAdd,
						Left:               recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "amount_id"},
						Right:              recipe.ConstructionOperand{Kind: recipe.DerivedLiteralOperand, Literal: &recipe.DerivedLiteral{Kind: recipe.NumericInteger, Integer: &two}},
						MissingInputPolicy: recipe.MissingInputPropagateNull,
					}},
					Outputs: []recipe.StageColumn{
						{ID: "amount_id", Name: "amount"}, {ID: "status_id", Name: "status"}, {ID: "total_id", Name: "total"},
					},
				}},
			},
		}},
	}
}

func exec06Resolve(t *testing.T) dataframeexecution.Resolved {
	t.Helper()
	bindings := recipe.RuntimeBindings{Project: "exec06-identity", DatasetGeneration: "generation-1"}
	plan, err := semantic.BuildRecipePlan(exec06ConstructionBundle(), bindings)
	if err != nil {
		t.Fatalf("semantic build: %v", err)
	}
	resolvedPlan, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatalf("semantic resolve: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolvedPlan, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("lower compile: %v", err)
	}
	return dataframeexecution.Resolved{Compiled: compiled}
}

func TestExec06CategoryIdentityRefusalsStayTypedAtTheCompileEntry(t *testing.T) {
	resolved := exec06Resolve(t)
	if len(resolved.Compiled.Outputs) != 1 || len(resolved.Compiled.Outputs[0].Stages) == 0 {
		t.Fatalf("fixture: resolved output carries no stage descriptors: %#v", resolved.Compiled.Outputs)
	}
	cases := []struct {
		name    string
		request dataframeexecution.CategoryScanRequest
		code    compiler.CategoryScanRefusalCode
	}{
		{"output form names an absent column",
			dataframeexecution.CategoryScanRequest{Output: "trace", Column: "nope", MaxValues: 256},
			compiler.CategoryScanColumnUnknown},
		{"stage form names an absent stage",
			dataframeexecution.CategoryScanRequest{Output: "trace", StageID: "nope_stage", ColumnID: "status_id", ValueColumnID: "amount_id", MaxValues: 256},
			compiler.CategoryScanStageUnknown},
		{"stage form names an absent category column",
			dataframeexecution.CategoryScanRequest{Output: "trace", StageID: recipe.ConstructionSourceProjectionID, ColumnID: "nope_id", ValueColumnID: "amount_id", MaxValues: 256},
			compiler.CategoryScanColumnUnknown},
		{"stage form names an absent value column",
			dataframeexecution.CategoryScanRequest{Output: "trace", StageID: recipe.ConstructionSourceProjectionID, ColumnID: "status_id", ValueColumnID: "nope_id", MaxValues: 256},
			compiler.CategoryScanColumnUnknown},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			port := &exec06Port{}
			engine := exec06Engine(t, command.CompiledBackendPostgreSQL, port)
			result, err := engine.ScanCategories(context.Background(), resolved, test.request)
			if err == nil {
				t.Fatalf("absent identity accepted: result %#v", result)
			}
			code, ok := compiler.CategoryScanRefusalCodeOf(err)
			if !ok || code != test.code {
				t.Fatalf("refusal = %v (code %q, ok %v), want typed %q from the compiler entry", err, code, ok, test.code)
			}
			if result.Complete || result.Overflow || result.ConclusiveMissing || len(result.Values) != 0 {
				t.Fatalf("refusal softened into a result page: %#v", result)
			}
			if port.counts() != 0 {
				t.Fatalf("port calls = %d, want zero — the compile entry refuses before execution", port.counts())
			}
		})
	}
	t.Run("absent output is the executor layer, not a compiler refusal", func(t *testing.T) {
		port := &exec06Port{}
		engine := exec06Engine(t, command.CompiledBackendPostgreSQL, port)
		_, err := engine.ScanCategories(context.Background(), resolved, dataframeexecution.CategoryScanRequest{Output: "nope", Column: "status", MaxValues: 256})
		if err == nil || !strings.Contains(err.Error(), "was not found") {
			t.Fatalf("output-name refusal = %v, want the executor's own not-found error", err)
		}
		if _, ok := compiler.CategoryScanRefusalCodeOf(err); ok {
			t.Fatalf("output-name lookup must stay the executor layer, got a compiler refusal code: %v", err)
		}
		if port.counts() != 0 {
			t.Fatalf("port calls = %d, want zero", port.counts())
		}
	})
	t.Run("valid source_projection stage scan proceeds", func(t *testing.T) {
		port := &exec06Port{rows: []map[string]any{{"present": true, "value": exec06JSONB(t, `"final"`)}}}
		engine := exec06Engine(t, command.CompiledBackendPostgreSQL, port)
		result, err := engine.ScanCategories(context.Background(), resolved, dataframeexecution.CategoryScanRequest{
			Output: "trace", StageID: recipe.ConstructionSourceProjectionID,
			ColumnID: "status_id", ValueColumnID: "amount_id", MaxValues: 256,
		})
		if err != nil {
			t.Fatalf("valid stage-form scan: %v", err)
		}
		if !result.Complete || len(result.Values) != 1 || result.Values[0] != (dataframeexecution.CategoryValue{Present: true, Value: "final"}) {
			t.Fatalf("stage-form result = %#v", result)
		}
		if result.Proof.Version != 2 || result.Proof.StageID != recipe.ConstructionSourceProjectionID ||
			result.Proof.ColumnID != "status_id" || result.Proof.ValueColumnID != "amount_id" {
			t.Fatalf("stage proof not bound: %#v", result.Proof)
		}
		if port.counts() != 1 || port.calls[0].tag != command.CompiledBackendPostgreSQL {
			t.Fatalf("calls = %#v, want one PostgreSQL statement", port.calls)
		}
	})
}
