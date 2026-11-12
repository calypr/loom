package postgres

// Offline + live battery for the pg-sql-24 typed cell-value evidence
// terminal (cell_trace.go). The plans are production-lowered population
// plans with the evidence terminal attached in exactly the compiler's
// minting posture (compiler/cell_trace.go prepareCellTracePhysicalPlan) —
// the render package may not import the compiler (cycle), so these helpers
// are the contract mirror; the compiler-side PostgreSQL cell-trace tests
// (compiler/cell_trace_test.go) keep the real mint honest, mirroring the
// pg-sql-23 dual-track law.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	control "github.com/calypr/loom/internal/backends/postgres/control"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Fixtures: the two registered arms, lowered through the real pipeline.
// ---------------------------------------------------------------------------

func sql24ArmBOutput() recipe.Output {
	return recipe.Output{
		Name: "Patients", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "gender", Expr: recipe.Expression{Select: "root.gender"}},
		},
	}
}

func sql24ArmAOutput() recipe.Output {
	return recipe.Output{
		Name: "Patients", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact, TraversalColumnNaming: recipe.TraversalColumnNamingExact,
		Fields: []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Traversals: []recipe.Traversal{{
			Name: "subject_Patient", ToResourceType: "Condition", Alias: "condition", MatchMode: recipe.MatchOptional,
			Fields: []recipe.Field{{Name: "condition_id", Expr: recipe.Expression{Select: "id"}, ValueMode: recipe.ValueModeFirst}},
		}},
	}
}

func sql24ArmAAllOutput() recipe.Output {
	output := sql24ArmAOutput()
	output.Traversals[0].Fields[0].ValueMode = recipe.ValueModeAll
	return output
}

func sql24Lower(t *testing.T, output recipe.Output) lower.CompiledRecipeOutput {
	t.Helper()
	bindings := recipe.RuntimeBindings{Project: group13Project, DatasetGeneration: group13Generation, AuthResourcePaths: []string{group13AuthPath}}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
	if err := bundle.Validate(); err != nil {
		t.Fatalf("bundle validate: %v", err)
	}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("BuildRecipePlan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatalf("ResolveRecipePlan: %v", err)
	}
	// The registered-surface policy (stage_sequence_production_test.go:435):
	// the endpoint-lookup planner label has no PostgreSQL storage
	// counterpart on the spine, so the arm A fixture lowers the native
	// typed-identity join, as compileWithRegisteredSurfacePolicy does.
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy().
		WithRule(ir.PhysicalOptimizationRuleEndpointTraversal, false))
	if err != nil {
		t.Fatalf("lower.CompileResolvedRecipePlan: %v", err)
	}
	for _, candidate := range compiled.Outputs {
		if candidate.Name == output.Name {
			return candidate
		}
	}
	t.Fatalf("lowered outputs do not carry %q", output.Name)
	return lower.CompiledRecipeOutput{}
}

// ---------------------------------------------------------------------------
// The mint mirror: prepareCellTracePhysicalPlan posture, contract-for-
// contract (compiler/cell_trace.go:99-135, population_mapping.go:346-395,
// cell_trace.go:799-831). The compiler battery exercises the real mint.
// ---------------------------------------------------------------------------

func sql24MirrorProjectionExpression(projection ir.PhysicalProjection) (ir.PhysicalExpression, error) {
	if projection.Expression != nil {
		return ir.ClonePhysicalExpression(*projection.Expression), nil
	}
	if projection.Value.Variable == "" && projection.Value.BindKey == "" {
		return ir.PhysicalExpression{}, fmt.Errorf("final return projection has no typed expression or value")
	}
	return ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: projection.Value.Variable, BindKey: projection.Value.BindKey, Path: append([]string(nil), projection.Value.Path...)}}, nil
}

// sql24ApplyGenericWindow replicates the shared mint's first step
// (compiler/cell_trace.go withGenericPhysicalExecutionWindow(physical, 0),
// physical_optimization.go:145-218): the root SORT over root._key inserted
// at the scope window end, no LIMIT (limit=0).
func sql24ApplyGenericWindow(t *testing.T, plan ir.PhysicalPlan) ir.PhysicalPlan {
	t.Helper()
	root := plan.Operations[0].RootScan.Variable
	insertAt := ir.PhysicalScopeWindowEnd(plan.Operations, 1)
	if insertAt <= 1 || insertAt >= len(plan.Operations) {
		t.Fatalf("fixture plan does not carry a scoped root followed by RETURN or traversal")
	}
	window := ir.PhysicalOperation{
		Kind:   ir.PhysicalSortOp,
		Source: ir.PhysicalSource{SemanticNode: plan.Source.SemanticNode, ResourceType: plan.Source.ResourceType, SemanticField: "_key"},
		Sort:   &ir.PhysicalSort{Keys: []ir.PhysicalValue{{Variable: root, Path: []string{"_key"}}}},
	}
	operations := make([]ir.PhysicalOperation, 0, len(plan.Operations)+1)
	operations = append(operations, plan.Operations[:insertAt]...)
	operations = append(operations, window)
	operations = append(operations, plan.Operations[insertAt:]...)
	plan.Operations = operations
	return plan
}

func sql24AttachTrace(t *testing.T, output lower.CompiledRecipeOutput, column string, offset, limit int) ir.PhysicalPlan {
	t.Helper()
	plan := sql24ApplyGenericWindow(t, ir.ClonePhysicalPlan(output.Plan))
	terminalIndex := -1
	for index, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalReturnOp && operation.Return != nil {
			terminalIndex = index
		}
	}
	if terminalIndex < 0 {
		t.Fatalf("fixture plan has no RETURN terminal")
	}
	projections := map[string]ir.PhysicalProjection{}
	for _, projection := range plan.Operations[terminalIndex].Return.Projections {
		projections[projection.Name] = projection
	}
	identityParts := make([]ir.PhysicalPopulationMappingIdentityPart, 0, len(output.RowIdentity.Fields))
	for _, field := range output.RowIdentity.Fields {
		if projection, ok := projections[field]; ok {
			expression, err := sql24MirrorProjectionExpression(projection)
			if err != nil {
				t.Fatalf("identity field %q: %v", field, err)
			}
			identityParts = append(identityParts, ir.PhysicalPopulationMappingIdentityPart{Name: field, Expression: expression})
			continue
		}
		if _, bound := plan.BindVars[field]; bound {
			identityParts = append(identityParts, ir.PhysicalPopulationMappingIdentityPart{Name: field,
				Expression: ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{BindKey: field}}})
			continue
		}
		t.Fatalf("identity field %q is not present in the final return projection or compiler bind vars", field)
	}
	var explicit *ir.PhysicalExpression
	if projection, ok := projections["__loom_row_id"]; ok {
		expression, err := sql24MirrorProjectionExpression(projection)
		if err != nil {
			t.Fatalf("explicit row identity: %v", err)
		}
		explicit = &expression
	}
	wanted, ok := projections[column]
	if !ok || wanted.Hidden {
		t.Fatalf("column %q is not a public output projection", column)
	}
	value, err := sql24MirrorProjectionExpression(wanted)
	if err != nil {
		t.Fatalf("trace column %q: %v", column, err)
	}
	var contribution *ir.PhysicalCellTraceContribution
	if value.Kind == ir.PhysicalValueExpression && value.Value != nil && value.Value.Variable != "" && len(value.Value.Path) == 1 {
		for _, operation := range plan.Operations {
			if operation.Kind != ir.PhysicalSetOp || operation.Set == nil || operation.Set.Reduction == nil ||
				operation.Set.Reduction.Variable != value.Value.Variable {
				continue
			}
			for _, field := range operation.Set.Reduction.Fields {
				if field.Name != value.Value.Path[0] {
					continue
				}
				contribution = &ir.PhysicalCellTraceContribution{
					SetVariable: operation.Set.Reduction.SourceSetVariable,
					ValueField:  field.SourceField,
					Lossy:       field.Mode == ir.PhysicalSetReductionFirst,
				}
			}
		}
	}
	plan.BindVars["cell_trace_offset"] = offset
	plan.BindVars["cell_trace_limit"] = limit
	plan.BindVars["cell_trace_fetch_limit"] = limit + 1
	plan.Operations[terminalIndex] = ir.PhysicalOperation{
		Kind: ir.PhysicalCellTraceReturnOp, Source: plan.Operations[terminalIndex].Source,
		CellTraceReturn: &ir.PhysicalCellTraceReturn{
			Value: value, Contribution: contribution, IdentityParts: identityParts, ExplicitIdentity: explicit,
			OffsetBindKey: "cell_trace_offset", LimitBindKey: "cell_trace_limit", FetchLimitBindKey: "cell_trace_fetch_limit",
		},
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("plan with the cell-trace terminal must validate: %v", err)
	}
	return plan
}

func sql24ArmBPlan(t *testing.T, column string, offset, limit int) ir.PhysicalPlan {
	t.Helper()
	output := sql24Lower(t, sql24ArmBOutput())
	return sql24AttachTrace(t, output, column, offset, limit)
}

func sql24ArmAPlan(t *testing.T, all bool, offset, limit int) ir.PhysicalPlan {
	t.Helper()
	output := sql24Lower(t, sql24ArmAOutput())
	if all {
		output = sql24Lower(t, sql24ArmAAllOutput())
	}
	return sql24AttachTrace(t, output, "condition_id", offset, limit)
}

func sql24Render(t *testing.T, plan ir.PhysicalPlan) command.RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	return rendered
}

// ---------------------------------------------------------------------------
// Offline: the six-column contract on both arms.
// ---------------------------------------------------------------------------

func sql24WantColumns(identityColumn string) []string {
	return []string{
		ir.PhysicalCellTraceValueField, ir.PhysicalCellTraceContributionsField, identityColumn,
		ir.PhysicalCellTraceStatusField, ir.PhysicalCellTraceHasMoreField, ir.PhysicalCellTraceOmissionField,
	}
}

func sql24ContractCells(t *testing.T, rendered command.RenderedPostgresPhysicalPlan) map[string]string {
	t.Helper()
	cells := map[string]string{}
	for _, column := range rendered.OutputColumns {
		marker := ` AS "` + column + `"`
		if !strings.Contains(rendered.SQL, marker) {
			t.Fatalf("statement does not publish %q as a named column:\n%s", column, rendered.SQL)
		}
	}
	return cells
}

func TestCellTracePostgresRootExtractContract(t *testing.T) {
	plan := sql24ArmBPlan(t, "gender", 2, 5)
	rendered := sql24Render(t, plan)
	want := sql24WantColumns(ir.PhysicalCellTraceIdentityPartsField)
	if strings.Join(rendered.OutputColumns, ",") != strings.Join(want, ",") {
		t.Fatalf("output columns = %v, want %v", rendered.OutputColumns, want)
	}
	sql := rendered.SQL
	for _, fragment := range []string{
		`#> $5::text[]) AS "__loom_trace_value"`,
		`jsonb_build_object('resourceType',`, `'resourceId',`,
		`ELSE '[]'::jsonb END AS "__loom_trace_contributions"`,
		`jsonb_build_array(to_jsonb($1::text), r0."logical_id") AS "__loom_trace_identity_parts"`,
		`= 0 THEN 'NO_MATCH' WHEN`, `THEN 'RECORDED_NULL' ELSE 'VALUE' END AS "__loom_trace_status"`,
		`false AS "__loom_trace_has_more"`,
		`$6::text AS "__loom_trace_omission"`,
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("arm B statement missing %q:\n%s", fragment, sql)
		}
	}
	if strings.Contains(sql, "OFFSET") || strings.Contains(sql, "LIMIT") {
		t.Fatalf("arm B has provable zero-or-one bounds (D-24b), no page cap may appear:\n%s", sql)
	}
	if strings.Contains(sql, "'AMBIGUOUS'") {
		t.Fatalf("arm B is never lossy; no AMBIGUOUS branch may appear:\n%s", sql)
	}
	if !strings.Contains(sql, `ORDER BY r0."logical_id" ASC, r0."resource_seq" ASC`) {
		t.Fatalf("arm B must keep the stable execution-window order:\n%s", sql)
	}
	sql24ContractCells(t, rendered)
}

func TestCellTracePostgresReducedSetContract(t *testing.T) {
	plan := sql24ArmAPlan(t, false, 5, 10)
	rendered := sql24Render(t, plan)
	want := sql24WantColumns(ir.PhysicalCellTraceIdentityPartsField)
	if strings.Join(rendered.OutputColumns, ",") != strings.Join(want, ",") {
		t.Fatalf("output columns = %v, want %v", rendered.OutputColumns, want)
	}
	sql := rendered.SQL
	for _, fragment := range []string{
		`AS "__loom_trace_value"`,
		`jsonb_agg(jsonb_build_object('resourceType', capped.c_rt, 'resourceId', capped.c_lid, 'value', capped.c_v) ORDER BY capped.c_lid, capped.c_ord)`,
		`OFFSET $10 LIMIT $11) AS capped`,
		`ORDER BY capped.c_lid, capped.c_ord LIMIT $12) AS capped), '[]'::jsonb) AS "__loom_trace_contributions"`,
		`COUNT(*) > $12 FROM`, `AS __loom_t_more) AS "__loom_trace_has_more"`,
		`LIMIT 2) AS __loom_t_status) = 0 THEN 'NO_MATCH'`,
		`> 1 THEN 'AMBIGUOUS'`,
		`ELSE 'VALUE' END AS "__loom_trace_status"`,
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("arm A statement missing %q:\n%s", fragment, sql)
		}
	}
	// The registered page binds are positional bigints in registration
	// order — offset, fetch-limit (the page cap), then limit (the published
	// slice): plan data never crosses anywhere but a parameter.
	if got := fmt.Sprint(rendered.Args[9].Value, rendered.Args[10].Value, rendered.Args[11].Value); got != "5 11 10" {
		t.Fatalf("page bind args = %s, want \"5 11 10\": %v", got, rendered.Args)
	}
	if !strings.Contains(sql, `WITH ORDINALITY`) || !strings.Contains(sql, `SELECT DISTINCT`) {
		t.Fatalf("arm A must reuse the spine's registered DISTINCT-item + WITH ORDINALITY relation:\n%s", sql)
	}
}

// The reduced-set contributor page must be the byte-twin of the spine's own
// reduction reading: the item relation, slot, and total order the preview's
// reduced column used appear verbatim in the contributor page.
func TestCellTracePostgresReducedSetIsSpineTwin(t *testing.T) {
	plan := sql24ArmAPlan(t, true, 0, 3)
	rendered := sql24Render(t, plan)
	sql := rendered.SQL
	valueEnd := strings.Index(sql, ` AS "__loom_trace_value"`)
	if valueEnd < 0 {
		t.Fatalf("no value column:\n%s", sql)
	}
	valueText := sql[:valueEnd]
	for _, fragment := range []string{
		`"fhir_edge_occurrence" AS e1`, `WITH ORDINALITY AS s`,
		`ORDER BY i`, `.ord`, `SELECT DISTINCT`,
	} {
		if !strings.Contains(valueText, fragment) || !strings.Contains(sql[valueEnd:], fragment) {
			t.Fatalf("twin fragment %q must appear in both the value column and the contributor page:\n%s", fragment, sql)
		}
	}
}

// Entry==dispatch: the RenderPhysicalPlan door and a direct door call render
// identical SQL and args for both registered arms.
func TestCellTracePostgresEntryEqualsDispatch(t *testing.T) {
	for _, plan := range []ir.PhysicalPlan{sql24ArmBPlan(t, "gender", 2, 5), sql24ArmAPlan(t, false, 5, 10)} {
		throughEntry := sql24Render(t, ir.ClonePhysicalPlan(plan))
		direct, err := renderCellTracePhysicalPlan(ir.ClonePhysicalPlan(plan), mustBinding(t))
		if err != nil {
			t.Fatalf("direct door: %v", err)
		}
		if direct.SQL != throughEntry.SQL || fmt.Sprint(direct.Args) != fmt.Sprint(throughEntry.Args) {
			t.Fatalf("door drifted from the entry render")
		}
	}
}

// Head placement (coordinator ruling 2026-11-12): a plan that carries the
// cell-trace terminal AND an unregistered path/graph operation is refused
// fail-closed by this slice with the typed unsupported-plan error — it must
// never fall through to the graph door or silently drop the trace columns.
func TestCellTracePostgresHeadDoorClaimsEveryTerminalBearingPlan(t *testing.T) {
	plan := sql24ArmBPlan(t, "gender", 0, 5)
	last := len(plan.Operations) - 1
	plan.Operations = append(plan.Operations[:last],
		ir.PhysicalOperation{Kind: ir.PhysicalGraphReturnOp}, plan.Operations[last])
	_, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err == nil || !strings.Contains(err.Error(), "not part of the cell-trace render surface") {
		t.Fatalf("terminal-carrying plan must refuse in the cell-trace slice, got: %v", err)
	}
	if !errors.Is(err, ErrUnsupportedPostgresPlan) {
		t.Fatalf("refusal must be the typed unsupported-plan error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Offline: the refusal register. Every shape outside the two arms refuses
// with a named typed error before any rendering can speak.
// ---------------------------------------------------------------------------

func sql24Refuse(t *testing.T, plan ir.PhysicalPlan, want string) {
	t.Helper()
	_, err := renderCellTracePhysicalPlan(plan, mustBinding(t))
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("refusal = %v, want error naming %q", err, want)
	}
	if !errors.Is(err, ErrUnsupportedPostgresPlan) {
		t.Fatalf("refusal %v is not the typed unsupported-plan error", err)
	}
}

func TestCellTracePostgresSurfaceRefusals(t *testing.T) {
	base := sql24ArmBPlan(t, "gender", 2, 5)
	armA := sql24ArmAPlan(t, false, 5, 10)

	composed := ir.ClonePhysicalPlan(base)
	composed.StageSequence = &ir.PhysicalStageSequence{SourceStageID: "s"}
	sql24Refuse(t, composed, "requires an inline plan")

	construction := ir.ClonePhysicalPlan(base)
	construction.Operations[len(construction.Operations)-1].CellTraceReturn.Construction = &ir.PhysicalCellTraceConstruction{}
	sql24Refuse(t, construction, "composition slice's terminal wave")

	reshape := ir.ClonePhysicalPlan(base)
	reshape.Operations[len(reshape.Operations)-1].CellTraceReturn.Reshape = &ir.PhysicalCellTraceReshape{}
	sql24Refuse(t, reshape, "pivot and unpivot terminal surfaces")

	omission := ir.ClonePhysicalPlan(base)
	omission.Operations[len(omission.Operations)-1].CellTraceReturn.OmissionCode = "TABLE_SHAPE_OMITTED_ROW"
	sql24Refuse(t, omission, "mints no omission code")

	bothIdentities := ir.ClonePhysicalPlan(base)
	extract := ir.PhysicalExpression{Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
		Extract: &ir.PhysicalExtract{Source: ir.PhysicalValue{Variable: "root"}, Selector: spec.Selector{Steps: []spec.SelectorStep{{Field: "id"}}}}}
	both := bothIdentities.Operations[len(bothIdentities.Operations)-1].CellTraceReturn
	both.ExplicitIdentity = &extract
	sql24Refuse(t, bothIdentities, "both an explicit identity and identity parts")

	neither := ir.ClonePhysicalPlan(base)
	neither.Operations[len(neither.Operations)-1].CellTraceReturn.IdentityParts = nil
	sql24Refuse(t, neither, "neither an explicit identity nor identity parts")

	badBind := ir.ClonePhysicalPlan(base)
	badBind.BindVars["cell_trace_offset"] = "5"
	sql24Refuse(t, badBind, "offset bind")

	badLookahead := ir.ClonePhysicalPlan(base)
	badLookahead.BindVars["cell_trace_fetch_limit"] = 7
	sql24Refuse(t, badLookahead, "limit+1 lookahead")

	negativeOffset := ir.ClonePhysicalPlan(base)
	negativeOffset.BindVars["cell_trace_offset"] = -1
	sql24Refuse(t, negativeOffset, "non-negative int")

	missingBindKeys := ir.ClonePhysicalPlan(base)
	missingBindKeys.Operations[len(missingBindKeys.Operations)-1].CellTraceReturn.LimitBindKey = ""
	sql24Refuse(t, missingBindKeys, "offset, limit, and fetch-limit bind keys")

	aggregateValue := ir.ClonePhysicalPlan(base)
	aggregateValue.Operations[len(aggregateValue.Operations)-1].CellTraceReturn.Value = ir.PhysicalExpression{Kind: ir.PhysicalAggregateExpression, Cardinality: ir.PhysicalScalarCardinality}
	sql24Refuse(t, aggregateValue, "aggregate, owner-records, call-recode, and prepared-extract values")

	prepared := ir.ClonePhysicalPlan(base)
	prepared.Operations[len(prepared.Operations)-1].CellTraceReturn.Value.Extract.Prepared = &ir.PhysicalPreparedReference{SetVariable: "s", Field: "f"}
	sql24Refuse(t, prepared, "prepared-extract values register with their terminal surfaces")

	arrayCardinality := ir.ClonePhysicalPlan(base)
	arrayCardinality.Operations[len(arrayCardinality.Operations)-1].CellTraceReturn.Value.Cardinality = ir.PhysicalArrayCardinality
	sql24Refuse(t, arrayCardinality, "SCALAR cardinality only")

	foreignExtract := ir.ClonePhysicalPlan(base)
	foreignExtract.Operations[len(foreignExtract.Operations)-1].CellTraceReturn.Value.Extract.Source.Variable = "somebody_elses_row"
	sql24Refuse(t, foreignExtract, "must read the root row")

	setMismatch := ir.ClonePhysicalPlan(armA)
	setMismatch.Operations[len(setMismatch.Operations)-1].CellTraceReturn.Contribution.SetVariable = "unregistered_set"
	sql24Refuse(t, setMismatch, "self-sourced child-set reduction shape")

	fieldMismatch := ir.ClonePhysicalPlan(armA)
	fieldMismatch.Operations[len(fieldMismatch.Operations)-1].CellTraceReturn.Contribution.ValueField = "not_a_slot"
	sql24Refuse(t, fieldMismatch, "disagrees with reduction field")

	lossyMismatch := ir.ClonePhysicalPlan(armA)
	lossyMismatch.Operations[len(lossyMismatch.Operations)-1].CellTraceReturn.Contribution.Lossy = !lossyMismatch.Operations[len(lossyMismatch.Operations)-1].CellTraceReturn.Contribution.Lossy
	sql24Refuse(t, lossyMismatch, "lossy=")

	valueNotReductionRead := ir.ClonePhysicalPlan(armA)
	valueNotReductionRead.Operations[len(valueNotReductionRead.Operations)-1].CellTraceReturn.Value.Value.Path = []string{"deeper", "path"}
	sql24Refuse(t, valueNotReductionRead, "one field of the reduction variable")

	secondTerminal := ir.ClonePhysicalPlan(base)
	secondTerminal.Operations = append(secondTerminal.Operations[:1], append([]ir.PhysicalOperation{ir.ClonePhysicalOperation(secondTerminal.Operations[len(secondTerminal.Operations)-1])}, secondTerminal.Operations[1:]...)...)
	sql24Refuse(t, secondTerminal, "at most one cell-trace terminal")

	survivingReturn := sql24ArmBPlan(t, "gender", 2, 5)
	survivingReturn.Operations = append(survivingReturn.Operations[:len(survivingReturn.Operations)-1],
		ir.PhysicalOperation{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{}},
		survivingReturn.Operations[len(survivingReturn.Operations)-1])
	sql24Refuse(t, survivingReturn, "not part of the cell-trace render surface")

	traversalOp := ir.ClonePhysicalPlan(armA)
	traversalOp.Operations = append(traversalOp.Operations[:1], append([]ir.PhysicalOperation{ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{}}}, traversalOp.Operations[1:]...)...)
	sql24Refuse(t, traversalOp, "not part of the cell-trace render surface")

	companion := ir.ClonePhysicalPlan(base)
	companion.Operations = append(companion.Operations, ir.PhysicalOperation{Kind: ir.PhysicalPopulationMappingReturnOp, PopulationMappingReturn: &ir.PhysicalPopulationMappingReturn{}})
	sql24Refuse(t, companion, "companion terminal keeps its registered surface voice")

	terminalNotLast := ir.ClonePhysicalPlan(base)
	terminalNotLast.Operations = append(terminalNotLast.Operations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{}})
	sql24Refuse(t, terminalNotLast, "final operation")
}

// The door's claim test must not reach any plan that predates it: every
// plan without the terminal op renders byte-identically to the doors that
// claimed it before this slice existed (the graph plan below is claimed by
// the graph door exactly as before — a successful render with zero cell
// trace columns is the observable).
func TestCellTracePostgresDoorClaimsNothingElse(t *testing.T) {
	if cellTraceTerminalMentions(sql24ArmBPlan(t, "gender", 0, 5)) != true {
		t.Fatal("a terminal-carrying inline plan must be claimed")
	}
	withoutTerminal := ir.ClonePhysicalPlan(sql24Lower(t, sql24ArmBOutput()).Plan)
	if cellTraceTerminalMentions(withoutTerminal) {
		t.Fatal("a plan without the terminal op must not be claimed")
	}
	rendered, err := RenderPhysicalPlan(withoutTerminal, mustBinding(t))
	if err != nil {
		t.Fatalf("a RETURN-terminal plan must keep its pre-slice render path: %v", err)
	}
	for _, column := range rendered.OutputColumns {
		if strings.HasPrefix(column, "__loom_trace_") {
			t.Fatalf("unclaimed plan leaked a trace column %q", column)
		}
	}
}

// ---------------------------------------------------------------------------
// Live: owned verify_sql24v1_* fixtures. Zero-skip law: with
// LOOM_TEST_POSTGRES_URL set, every live cell must run or fail — never
// skip. Without the URL the whole group is inert (the sibling pattern).
// ---------------------------------------------------------------------------

var (
	sql24FixturesMu      sync.Mutex
	sql24TrackedFixtures = map[string]bool{}
)

func sql24TrackFixtures(t *testing.T, svc *pgir02Service) {
	t.Helper()
	var database string
	if err := svc.iso.QueryRow(svc.ctx, "SELECT current_database()").Scan(&database); err != nil {
		svc.fail("track owned database", err)
	}
	sql24FixturesMu.Lock()
	defer sql24FixturesMu.Unlock()
	sql24TrackedFixtures[database] = true
	sql24TrackedFixtures[svc.schema] = true
}

func sql24SnapshotOwnedResources(ctx context.Context, raw string) ([]string, error) {
	pool, err := pgxpool.New(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()
	rows, err := pool.Query(ctx,
		`SELECT datname FROM pg_database WHERE datname LIKE 'verify_sql24v1_%'`+
			` UNION ALL SELECT nspname FROM pg_namespace WHERE nspname LIKE 'verify_sql24v1_%'`)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan: %v", err)
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// sql24RunDeltaAudit wraps the composed audit chain with a delta-scoped
// audit over the disjoint verify_sql24v1_* namespace (the group13/pivot14/
// sql16/sql23 pattern): every resource born during the run and tracked by
// a live test here must not survive the run, and the namespace starts
// empty.
func sql24RunDeltaAudit(inner func() int) int {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		return inner()
	}
	beforeList, err := sql24SnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-24 delta audit: snapshot before run: %v\n", err)
		return inner()
	}
	if len(beforeList) > 0 {
		fmt.Fprintf(os.Stderr, "pg-sql-24 delta audit: pre-snapshot found %d surviving verify_sql24v1_* resources before this run: %v\n", len(beforeList), beforeList)
		return 1
	}
	before := make(map[string]bool, len(beforeList))
	for _, name := range beforeList {
		before[name] = true
	}
	code := inner()
	after, err := sql24SnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-24 delta audit: snapshot after run: %v\n", err)
		code = 1
	}
	sql24FixturesMu.Lock()
	defer sql24FixturesMu.Unlock()
	for _, name := range after {
		if before[name] {
			continue
		}
		if sql24TrackedFixtures[name] {
			fmt.Fprintf(os.Stderr, "pg-sql-24 delta audit: %q was created and tracked by this run and still survives\n", name)
			code = 1
			continue
		}
		fmt.Fprintf(os.Stderr, "pg-sql-24 delta audit: note — %q was born during this run but is not owned by it (sibling lane?)\n", name)
	}
	var survivors []string
	for _, name := range after {
		if !before[name] && sql24TrackedFixtures[name] {
			survivors = append(survivors, name)
		}
	}
	if len(survivors) > 0 {
		fmt.Fprintf(os.Stderr, "pg-sql-24 delta audit: %d verify_sql24v1_* resources survive: %v\n", len(survivors), survivors)
		code = 1
	}
	return code
}

func newSQL24Service(t *testing.T) *pgir02Service {
	t.Helper()
	return newOwnedService(t, 0, "verify_sql24v1")
}

func sql24Insert(t *testing.T, svc *pgir02Service, resourceType, logicalID string, payload map[string]any) {
	t.Helper()
	svc.insertResource(t, svc.schema, group13Project, group13Generation, resourceType, logicalID, group13AuthPath, payload)
}

func sql24Edge(t *testing.T, svc *pgir02Service, conditionID, patientID, sourcePath string) {
	t.Helper()
	svc.insertEdge(t, control.EdgeOccurrenceRow{
		Project: group13Project, DatasetGeneration: group13Generation, AuthResourcePath: group13AuthPath,
		Label: "subject_Patient", SourceResourceType: "Condition", SourceLogicalID: conditionID,
		TargetResourceType: "Patient", TargetLogicalID: patientID, SourcePath: sourcePath,
	})
}

// sql24RunPage executes the compiled contributor page and returns the six
// contract columns as published text (the sql23RunPage cast: the runner
// wraps the compiled statement unchanged and casts the contract columns to
// text, so the cell the driver reports is the JSON PostgreSQL published).
type sql24PageRow struct {
	value, contributions, identity, status, hasMore, omission string
}

func sql24RunPage(t *testing.T, svc *pgir02Service, plan ir.PhysicalPlan) []sql24PageRow {
	t.Helper()
	rendered := group13RenderSvc(t, svc, plan)
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}
	rows, err := svc.iso.Query(svc.ctx,
		`SELECT w."__loom_trace_value"::text, w."__loom_trace_contributions"::text,`+
			` w."__loom_trace_identity_parts"::text, w."__loom_trace_status"::text,`+
			` w."__loom_trace_has_more"::text, w."__loom_trace_omission"::text FROM (`+rendered.SQL+`) AS w`, args...)
	if err != nil {
		t.Fatalf("execute contributor page: %v", err)
	}
	defer rows.Close()
	var out []sql24PageRow
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			t.Fatalf("read contributor page row: %v", err)
		}
		if len(values) != 6 {
			t.Fatalf("contributor page row must carry the six contract columns: %v", values)
		}
		out = append(out, sql24PageRow{
			value: stringifyCell(values[0]), contributions: stringifyCell(values[1]),
			identity: stringifyCell(values[2]), status: stringifyCell(values[3]),
			hasMore: stringifyCell(values[4]), omission: stringifyCell(values[5]),
		})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("finish contributor page: %v", err)
	}
	return out
}

// sql24RunTextColumns executes a rendered statement with every column cast
// to text, so the cells the driver reports are exactly the values
// PostgreSQL published (the sql23RunPage cast law, applied to previews so
// trace cells and preview cells are compared same-to-same).
func sql24RunTextColumns(t *testing.T, svc *pgir02Service, rendered command.RenderedPostgresPhysicalPlan, columns int) [][]string {
	t.Helper()
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}
	selected := make([]string, columns)
	for index := range selected {
		selected[index] = fmt.Sprintf(`w."c%d"::text`, index)
	}
	rows, err := svc.iso.Query(svc.ctx, `SELECT `+strings.Join(selected, ", ")+` FROM (`+rendered.SQL+`) AS w`, args...)
	if err != nil {
		t.Fatalf("execute rendered statement (text cast): %v", err)
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			t.Fatalf("read row: %v", err)
		}
		row := make([]string, len(values))
		for index, value := range values {
			row[index] = stringifyCell(value)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("finish rows: %v", err)
	}
	return out
}

func sql24ContributorMembers(t *testing.T, cell string) []string {
	t.Helper()
	var members []struct {
		ResourceType string          `json:"resourceType"`
		ResourceID   json.RawMessage `json:"resourceId"`
		Value        json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal([]byte(cell), &members); err != nil {
		t.Fatalf("decode contributors %q: %v", cell, err)
	}
	out := make([]string, len(members))
	for i, member := range members {
		out[i] = member.ResourceType + "/" + string(member.ResourceID) + "=" + string(member.Value)
	}
	return out
}

func sql24RowBylogicalID(t *testing.T, rows []sql24PageRow, logicalID string) sql24PageRow {
	t.Helper()
	for _, row := range rows {
		if strings.Contains(row.identity, `"`+logicalID+`"`) {
			return row
		}
	}
	t.Fatalf("no traced row for %q in %v", logicalID, rows)
	return sql24PageRow{}
}

// TestCellTracePostgresLiveRootExtract pins arm B end to end: the traced
// row is its own contributor, the three status states read as published,
// hasMore is the constant false, and a foreign-scope patient never appears.
func TestCellTracePostgresLiveRootExtract(t *testing.T) {
	svc := newSQL24Service(t)
	sql24TrackFixtures(t, svc)
	sql24Insert(t, svc, "Patient", "p-1", map[string]any{"id": "p-1", "gender": "male"})
	sql24Insert(t, svc, "Patient", "p-2", map[string]any{"id": "p-2", "gender": nil})
	sql24Insert(t, svc, "Patient", "p-3", map[string]any{"id": "p-3"})
	svc.insertResource(t, svc.schema, "other-project", group13Generation, "Patient", "p-9", group13AuthPath,
		map[string]any{"id": "p-9", "gender": "male"})

	rows := sql24RunPage(t, svc, sql24ArmBPlan(t, "gender", 0, 5))
	if len(rows) != 3 {
		t.Fatalf("exactly the three scoped patients are traced: %v", rows)
	}
	male := sql24RowBylogicalID(t, rows, "p-1")
	if male.status != "VALUE" || male.value != `"male"` || male.hasMore != "false" || male.omission != "" {
		t.Fatalf("p-1 = %+v", male)
	}
	if got := sql24ContributorMembers(t, male.contributions); len(got) != 1 || got[0] != `Patient/"p-1"="male"` {
		t.Fatalf("p-1 contributors = %v, want the traced row itself", got)
	}
	if !strings.Contains(male.identity, `"`+group13Project+`"`) || !strings.Contains(male.identity, `"p-1"`) {
		t.Fatalf("p-1 identity = %s", male.identity)
	}
	recordedNull := sql24RowBylogicalID(t, rows, "p-2")
	if recordedNull.status != "NO_MATCH" || recordedNull.value != "null" {
		t.Fatalf("p-2 (JSON-null gender) = %+v", recordedNull)
	}
	if recordedNull.contributions != "[]" || recordedNull.hasMore != "false" {
		t.Fatalf("p-2 must publish zero contributors and no more pages: %+v", recordedNull)
	}
	missing := sql24RowBylogicalID(t, rows, "p-3")
	if missing.status != "NO_MATCH" || missing.value != "<null>" {
		t.Fatalf("p-3 (absent gender: SQL NULL, distinct from the recorded JSON null) = %+v", missing)
	}
}

// TestCellTracePostgresLivePreviewRoundTrip is the L1 law: the traced
// value equals the value the preview published for the same row, taken OUT
// of the executed preview, never re-derived.
func TestCellTracePostgresLivePreviewRoundTrip(t *testing.T) {
	svc := newSQL24Service(t)
	sql24TrackFixtures(t, svc)
	sql24Insert(t, svc, "Patient", "p-1", map[string]any{"id": "p-1", "gender": "male"})
	sql24Insert(t, svc, "Patient", "p-2", map[string]any{"id": "p-2", "gender": "female"})

	preview := ir.ClonePhysicalPlan(sql24Lower(t, sql24ArmBOutput()).Plan)
	rendered := group13RenderSvc(t, svc, preview)
	previewRows := sql24RunTextColumns(t, svc, rendered, len(rendered.OutputColumns))
	if len(previewRows) != 2 {
		t.Fatalf("preview must publish both scoped patients: %v", previewRows)
	}
	// The spine publishes [_key, patient_id, gender]; the identity sidecar
	// keys the map and the projection-order last column is gender.
	previewGender := map[string]string{}
	for _, row := range previewRows {
		previewGender[row[0]] = row[len(row)-1]
	}
	rows := sql24RunPage(t, svc, sql24ArmBPlan(t, "gender", 0, 5))
	for _, row := range rows {
		var identity []string
		if err := json.Unmarshal([]byte(row.identity), &identity); err != nil {
			t.Fatalf("decode identity %q: %v", row.identity, err)
		}
		logicalID := identity[len(identity)-1]
		want, ok := previewGender[logicalID]
		if !ok {
			t.Fatalf("traced row %q is not in the preview %v", logicalID, previewRows)
		}
		if row.value != want {
			t.Fatalf("trace value %q drifted from the published preview value %q for %q", row.value, want, logicalID)
		}
	}
}

// TestCellTracePostgresLiveReducedSetPages pins arm A: contributor
// membership is the preview's own reduced column, the page window bounds
// what is published while hasMore bounds the lookahead, and FIRST ties the
// compiler cannot order read AMBIGUOUS.
func TestCellTracePostgresLiveReducedSetPages(t *testing.T) {
	svc := newSQL24Service(t)
	sql24TrackFixtures(t, svc)
	sql24Insert(t, svc, "Patient", "p-1", map[string]any{"id": "p-1"})
	sql24Insert(t, svc, "Patient", "p-none", map[string]any{"id": "p-none"})
	for _, condition := range []string{"c-1", "c-2", "c-3"} {
		sql24Insert(t, svc, "Condition", condition, map[string]any{"id": condition})
		sql24Edge(t, svc, condition, "p-1", "subject")
	}

	// L1 anchor: the spine does not register the RETURN preview of this
	// shape (presence-on-direct-value), so the membership authority is the
	// registered reduced-column reading the trace itself embeds — the
	// __loom_trace_value of the ALL-mode arm, rendered through the spine's
	// OWN setReductionSublink text — and the contributor page must agree
	// with it item for item and order for order.
	all := sql24RunPage(t, svc, sql24ArmAPlan(t, true, 0, 5))
	row := sql24RowBylogicalID(t, all, "p-1")
	var reduced []string
	if err := json.Unmarshal([]byte(row.value), &reduced); err != nil {
		t.Fatalf("ALL-mode value column %q is not the published reduced array: %v", row.value, err)
	}
	if len(reduced) != 3 {
		t.Fatalf("reduced column = %v", reduced)
	}
	if row.status != "VALUE" || row.hasMore != "false" {
		t.Fatalf("p-1 all-mode page = %+v", row)
	}
	members := sql24ContributorMembers(t, row.contributions)
	if len(members) != 3 {
		t.Fatalf("contributors = %v, want the full reduced set %v", members, reduced)
	}
	for i, member := range members {
		if want := `Condition/"` + reduced[i] + `"="` + reduced[i] + `"`; member != want {
			t.Fatalf("contributor %d = %q, want %q (order must follow the published reduced column)", i, member, want)
		}
	}
	empty := sql24RowBylogicalID(t, all, "p-none")
	if empty.status != "NO_MATCH" || empty.contributions != "[]" || empty.hasMore != "false" {
		t.Fatalf("p-none (no conditions) = %+v", empty)
	}

	// FIRST over three candidates: the published value is the first item,
	// the page carries its own cap, and the compiler-unorderable tie reads
	// AMBIGUOUS while the preview still published a value (D-24a: the trace
	// agrees with THIS backend's normalization).
	first := sql24RunPage(t, svc, sql24ArmAPlan(t, false, 0, 2))
	row = sql24RowBylogicalID(t, first, "p-1")
	if row.status != "AMBIGUOUS" {
		t.Fatalf("FIRST over three candidates must read AMBIGUOUS: %+v", row)
	}
	if want := `"` + reduced[0] + `"`; row.value != want {
		t.Fatalf("FIRST value %q must be the first published item %q", row.value, want)
	}
	if got := sql24ContributorMembers(t, row.contributions); len(got) != 2 || got[0] != members[0] {
		t.Fatalf("FIRST page cap = %v, want the first two items of %v", got, members)
	}
	if row.hasMore != "true" {
		t.Fatalf("three candidates with limit 2 must report hasMore: %+v", row)
	}

	// The window moves the page but not the status: offset 2, limit 2
	// publishes only the third item and no more pages.
	last := sql24RunPage(t, svc, sql24ArmAPlan(t, true, 2, 2))
	row = sql24RowBylogicalID(t, last, "p-1")
	if got := sql24ContributorMembers(t, row.contributions); len(got) != 1 || got[0] != members[2] {
		t.Fatalf("offset page = %v, want only %v", got, members[2])
	}
	if row.hasMore != "false" {
		t.Fatalf("the bounded lookahead must report no more pages: %+v", row)
	}
}

// TestCellTracePostgresLiveScopeSabotage proves the page binds to THIS
// backend: a same-scope row born in a scratch schema the binding does not
// name must not join the contributor stream, and a real new edge must.
func TestCellTracePostgresLiveScopeSabotage(t *testing.T) {
	svc := newSQL24Service(t)
	sql24TrackFixtures(t, svc)
	sql24Insert(t, svc, "Patient", "p-1", map[string]any{"id": "p-1"})
	sql24Insert(t, svc, "Condition", "c-1", map[string]any{"id": "c-1"})
	sql24Edge(t, svc, "c-1", "p-1", "subject")

	before := sql24RunPage(t, svc, sql24ArmAPlan(t, true, 0, 5))
	if got := sql24ContributorMembers(t, sql24RowBylogicalID(t, before, "p-1").contributions); len(got) != 1 {
		t.Fatalf("baseline contributor = %v", got)
	}

	// Scratch sabotage inside this service's own databases: the binding's
	// schema never names it, so the trace must not see it.
	scratch := fmt.Sprintf("verify_sql24v1_scratch_%d", time.Now().UnixNano())
	if _, err := svc.iso.Exec(svc.ctx, `CREATE SCHEMA "`+scratch+`"`); err != nil {
		svc.fail("create scratch schema", err)
	}
	sql24FixturesMu.Lock()
	sql24TrackedFixtures[scratch] = true
	sql24FixturesMu.Unlock()
	t.Cleanup(func() {
		svc.iso.Exec(context.WithoutCancel(svc.ctx), `DROP SCHEMA IF EXISTS "`+scratch+`" CASCADE`)
	})
	if _, err := control.MigrateSchema(svc.ctx, svc.client, scratch); err != nil {
		svc.fail("migrate scratch schema", err)
	}
	svc.insertResource(t, scratch, group13Project, group13Generation, "Condition", "c-shadow", group13AuthPath,
		map[string]any{"id": "c-shadow"})

	after := sql24RunPage(t, svc, sql24ArmAPlan(t, true, 0, 5))
	if got := sql24ContributorMembers(t, sql24RowBylogicalID(t, after, "p-1").contributions); len(got) != 1 || got[0] != `Condition/"c-1"="c-1"` {
		t.Fatalf("scratch-schema shadow leaked into the contributor page: %v", got)
	}

	// Liveness: a real edge in the bound schema must change the page.
	sql24Insert(t, svc, "Condition", "c-2", map[string]any{"id": "c-2"})
	sql24Edge(t, svc, "c-2", "p-1", "subject")
	grown := sql24RunPage(t, svc, sql24ArmAPlan(t, true, 0, 5))
	if got := sql24ContributorMembers(t, sql24RowBylogicalID(t, grown, "p-1").contributions); len(got) != 2 {
		t.Fatalf("the page must follow the bound store: %v", got)
	}
}

func cloneForPreview(t *testing.T, output lower.CompiledRecipeOutput) ir.PhysicalPlan {
	t.Helper()
	return ir.ClonePhysicalPlan(output.Plan)
}
