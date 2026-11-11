package postgres

// Production-path and live acceptance legs for the pg-sql-16 composed
// construction stage sequence (stage_sequence.go), plan §§9-10.
//
// The offline battery (stage_sequence_test.go) pins SQL shape; this file
// executes it. Every composition here is production-compiled
// (semantic.BuildRecipePlan → ResolveRecipePlan →
// lower.CompileResolvedRecipePlan via sql16Compile/sql16ThreeStagePlans);
// hand-shaping appears only in named single-point mutations documented at
// the cell. Live legs are never-skip: with LOOM_TEST_POSTGRES_URL set a
// broken environment fails through svc.fail, never a silent pass
// ("mocks and skipped tests do not establish parity"). Service-less lanes
// keep green by the package-shared testPostgresURL skip, which establishes
// nothing; the ticket acceptance run must be LIVE (report-pinned).
//
// Live inventory (plan §10): L1 EXPAND→GROUP after-every-stage rows,
// typed columns, independent identity; L2 three-stage exact after every
// stage (×3 orderings — also P2/P3 production-compiled legs); L3 staged
// PIVOT coexisting with the single-stage pivot door in one owned schema;
// L6/P5 source-region SET over a production child-set recipe; L7
// tail-index parity against the banked o1 oracle receipts (G1 emitter);
// L9 preview LIMIT, guard raises (ASSERT tags via 22P02), and the
// TestMain delta audit (census) over the verify_sql16v1_* namespace.
// L4/L5 (semantic key, route adapter) land with the worker arms; L8/S10
// are discharged by the D-16f fail-closed battery pins — the composition
// correlation carries no registered comparison surface, so no live arm
// exists to run (coordinator-ratified divergence register).

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	control "github.com/calypr/loom/internal/backends/postgres/control"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/jackc/pgx/v5/pgconn"
)

// sql16LiveService is one owned isolated database + schema per live cell
// under the disjoint verify_sql16v1_* namespace, registered with the
// TestMain delta audit (exact-name drops are the convergence proof).
func sql16LiveService(t *testing.T) *pgir02Service {
	t.Helper()
	svc := newOwnedService(t, 0, "verify_sql16v1")
	var database string
	if err := svc.iso.QueryRow(svc.ctx, "SELECT current_database()").Scan(&database); err != nil {
		svc.fail("track owned database", err)
	}
	sql16TrackResource(t, database)
	sql16TrackResource(t, svc.schema)
	return svc
}

// sql16SeedObservation inserts one Observation under the bindings the
// production compile carries (group13 project/generation/auth path).
func sql16SeedObservation(t *testing.T, svc *pgir02Service, logicalID string, payload map[string]any) {
	t.Helper()
	svc.insertResource(t, svc.schema, group13Project, group13Generation, "Observation", logicalID, group13AuthPath, payload)
}

func sql16DriverArgs(rendered command.RenderedPostgresPhysicalPlan) []any {
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}
	return args
}

// sql16ThroughStage returns the after-every-stage prefix: stages 0..through
// only, with FinalColumns/FinalStageID/FinalRowIdentity re-derived from
// the new terminal stage (the door's chain re-read still governs).
func sql16ThroughStage(t *testing.T, plan ir.PhysicalPlan, through int) ir.PhysicalPlan {
	t.Helper()
	clone := ir.ClonePhysicalPlan(plan)
	if clone.StageSequence == nil || through >= len(clone.StageSequence.Stages) {
		t.Fatalf("prefix %d out of range", through)
	}
	stages := clone.StageSequence.Stages[:through+1]
	last := stages[len(stages)-1]
	clone.StageSequence.Stages = stages
	clone.StageSequence.FinalColumns = append([]ir.PhysicalStageColumn(nil), last.OutputColumns...)
	clone.StageSequence.FinalStageID = last.ID
	clone.StageSequence.FinalRowIdentity = last.RowIdentityColumn
	return clone
}

// ---------------------------------------------------------------------------
// Independent identity builders (group13Identity pattern): the expected
// _key strings are assembled from the fixture literals and the plan's
// registered construction IDs — never from the rendered SQL.
// ---------------------------------------------------------------------------

func sql16GroupIdentity(constructionID, keyColumnID, keyJSON string) string {
	return fmt.Sprintf(`[["construction",%q],["operation","GROUP"],[%q,%s]]`,
		constructionID, keyColumnID, keyJSON)
}

func sql16ExpandIdentity(constructionID, inputIdentityJSON, ordinal string) string {
	return fmt.Sprintf(`[["input",%s],["construction",%q],["ordinal",%s]]`,
		inputIdentityJSON, constructionID, ordinal)
}

func sql16PivotIdentity(constructionID, kind, keyJSON string) string {
	return fmt.Sprintf(`["GROUPED_PIVOT",%q,[%q,%s]]`, constructionID, kind, keyJSON)
}

func sql16JSONString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// sql16ExpectRows renders the plan against the owned schema and compares
// the full result to literal rows (never recomputed from the renderer).
func sql16ExpectRows(t *testing.T, svc *pgir02Service, plan ir.PhysicalPlan, want [][]string) {
	t.Helper()
	rendered, err := renderPhysicalPlanForSchema(plan, svc.schema)
	if err != nil {
		svc.fail("render plan", err)
	}
	got := svc.runRendered(t, rendered)
	if len(got) != len(want) {
		t.Fatalf("rows = %d %v, want %d %v\nSQL: %s", len(got), got, len(want), want, rendered.SQL)
	}
	for index := range want {
		if strings.Join(got[index], "|") != strings.Join(want[index], "|") {
			t.Fatalf("row %d = %v, want %v\nSQL: %s", index, got[index], want[index], rendered.SQL)
		}
	}
}

func renderPhysicalPlanForSchema(plan ir.PhysicalPlan, schema string) (command.RenderedPostgresPhysicalPlan, error) {
	binding, err := ir.NewPostgresStorageBinding(schema)
	if err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return RenderPhysicalPlan(plan, binding)
}

// sql16ProbeTypes wraps a rendered statement and probes the SQL type of
// every output column through pg_typeof (the typed-column half of the
// after-every-stage contract; the driver side is runRendered's scan types).
func sql16ProbeTypes(t *testing.T, svc *pgir02Service, plan ir.PhysicalPlan) []string {
	t.Helper()
	rendered, err := renderPhysicalPlanForSchema(plan, svc.schema)
	if err != nil {
		svc.fail("render plan", err)
	}
	cells := make([]string, 0, len(rendered.OutputColumns))
	for index := range rendered.OutputColumns {
		cells = append(cells, fmt.Sprintf(`pg_typeof("c%d")`, index))
	}
	probe := "SELECT " + strings.Join(cells, ", ") + " FROM (" + rendered.SQL + ") AS __probe LIMIT 1"
	rows := svc.witness(t, probe, sql16DriverArgs(rendered)...)
	if len(rows) != 1 {
		t.Fatalf("type probe returned %d rows: %v", len(rows), rows)
	}
	return rows[0]
}

// ---------------------------------------------------------------------------
// L1 / P1 — EXPAND→GROUP: exact rows, typed columns, and independent
// identity after EVERY stage (plan §11 first criterion).
// ---------------------------------------------------------------------------

func TestSQL16LiveExpandGroupAfterEveryStage(t *testing.T) {
	svc := sql16LiveService(t)
	sql16SeedObservation(t, svc, "obs-1", map[string]any{"status": "final", "note": []any{
		map[string]any{"text": "a"}, map[string]any{"text": "b"},
	}})
	sql16SeedObservation(t, svc, "obs-2", map[string]any{"status": "entered-in-errors", "note": []any{
		map[string]any{"text": "b"},
	}})
	sql16SeedObservation(t, svc, "obs-3", map[string]any{"status": "final", "note": []any{}})
	sql16SeedObservation(t, svc, "obs-4", map[string]any{"status": "final"})
	plan := sql16ExpandGroupPlan(t)

	// After stage 0 (EXPAND, preserve-parent): five rows, ordinals from the
	// LATERAL WITH ORDINALITY, identity = input source identity (the
	// logical_id the source CTE materializes) + construction + ordinal.
	expand := sql16ThroughStage(t, plan, 0)
	sql16ExpectRows(t, svc, expand, [][]string{
		{"final", "a", "0", "obs-1", sql16ExpandIdentity("expand_tag_values", sql16JSONString("obs-1"), "0")},
		{"final", "b", "1", "obs-1", sql16ExpandIdentity("expand_tag_values", sql16JSONString("obs-1"), "1")},
		{"entered-in-errors", "b", "0", "obs-2", sql16ExpandIdentity("expand_tag_values", sql16JSONString("obs-2"), "0")},
		{"final", "<null>", "<null>", "obs-3", sql16ExpandIdentity("expand_tag_values", sql16JSONString("obs-3"), "null")},
		{"final", "<null>", "<null>", "obs-4", sql16ExpandIdentity("expand_tag_values", sql16JSONString("obs-4"), "null")},
	})
	// Intermediate stage columns keep the jsonb document class (registered
	// path reads); only the group key/reducers and identity scalars and the
	// carried source identity cross as SQL scalars.
	if got := sql16ProbeTypes(t, svc, expand); strings.Join(got, "|") != "jsonb|jsonb|bigint|text|text" {
		t.Fatalf("expand stage SQL types = %v, want jsonb|jsonb|bigint|text|text", got)
	}

	// Terminal (GROUP): NULLS FIRST key order under COLLATE "C", identity
	// built independently from the plan's construction ID.
	sql16ExpectRows(t, svc, plan, [][]string{
		{"<null>", "2", "2", "1", sql16GroupIdentity("group_tag_values", "grouped_tag_id", "null")},
		{"a", "1", "1", "1", sql16GroupIdentity("group_tag_values", "grouped_tag_id", `"a"`)},
		{"b", "2", "2", "2", sql16GroupIdentity("group_tag_values", "grouped_tag_id", `"b"`)},
	})
	if got := sql16ProbeTypes(t, svc, plan); strings.Join(got, "|") != "text|bigint|bigint|bigint|text" {
		t.Fatalf("group stage SQL types = %v, want text|bigint|bigint|bigint|text", got)
	}
}

// ---------------------------------------------------------------------------
// L2 / P2 / P3 — three-stage exact rows after every stage, all three
// registered orderings, each production-compiled with zero hand-shaping.
// ---------------------------------------------------------------------------

func TestSQL16LiveThreeStageAfterEveryStage(t *testing.T) {
	svc := sql16LiveService(t)
	sql16SeedObservation(t, svc, "obs-1", map[string]any{"status": "final", "note": []any{
		map[string]any{"text": "a"}, map[string]any{"text": "b"},
	}})
	sql16SeedObservation(t, svc, "obs-2", map[string]any{"status": "entered-in-errors", "note": []any{
		map[string]any{"text": "b"},
	}})
	sql16SeedObservation(t, svc, "obs-3", map[string]any{"status": "final", "note": []any{}})
	sql16SeedObservation(t, svc, "obs-4", map[string]any{"status": "final"})
	plans := sql16ThreeStagePlans(t)

	filtered := [][]string{
		{"final", "[a b]", "obs-1"},
		{"final", "[]", "obs-3"},
		{"final", "[]", "obs-4"},
	}

	// P2: FILTER→EXPAND→GROUP.
	t1 := plans["group-last"]
	sql16ExpectRows(t, svc, sql16ThroughStage(t, t1, 0), filtered)
	t1Expand := [][]string{
		{"final", "a", "0", "obs-1", sql16ExpandIdentity("expand_tag_values", sql16JSONString("obs-1"), "0")},
		{"final", "b", "1", "obs-1", sql16ExpandIdentity("expand_tag_values", sql16JSONString("obs-1"), "1")},
		{"final", "<null>", "<null>", "obs-3", sql16ExpandIdentity("expand_tag_values", sql16JSONString("obs-3"), "null")},
		{"final", "<null>", "<null>", "obs-4", sql16ExpandIdentity("expand_tag_values", sql16JSONString("obs-4"), "null")},
	}
	sql16ExpectRows(t, svc, sql16ThroughStage(t, t1, 1), t1Expand)
	sql16ExpectRows(t, svc, t1, [][]string{
		{"<null>", "2", "2", "1", sql16GroupIdentity("group_tag_values", "grouped_tag_id", "null")},
		{"a", "1", "1", "1", sql16GroupIdentity("group_tag_values", "grouped_tag_id", `"a"`)},
		{"b", "1", "1", "1", sql16GroupIdentity("group_tag_values", "grouped_tag_id", `"b"`)},
	})

	// P3: FILTER→GROUP→staged PIVOT (row-value ONE feeds the category).
	t2 := plans["pivot-last"]
	sql16ExpectRows(t, svc, sql16ThroughStage(t, t2, 0), filtered)
	groupIdentity := sql16GroupIdentity("group_status_values", "status_group_id", `"final"`)
	sql16ExpectRows(t, svc, sql16ThroughStage(t, t2, 1), [][]string{
		{"final", "final", "3", groupIdentity},
	})
	sql16ExpectRows(t, svc, t2, [][]string{
		{"final", "3", sql16PivotIdentity("tier_pivot", "STRING", `"final"`)},
	})

	// T3: FILTER→GROUP→EXPAND over the ALL row-value array (D-16c posture).
	t3 := plans["expand-last"]
	sql16ExpectRows(t, svc, sql16ThroughStage(t, t3, 0), filtered)
	sql16ExpectRows(t, svc, sql16ThroughStage(t, t3, 1), [][]string{
		{"final", "[final]", "3", groupIdentity},
	})
	sql16ExpectRows(t, svc, t3, [][]string{
		{"final", "3", "final", "0",
			sql16ExpandIdentity("expand_status_values", sql16JSONString(groupIdentity), "0")},
	})
}

// ---------------------------------------------------------------------------
// L3 — staged PIVOT coexisting with the single-stage pivot door in one
// owned schema: both registered render paths execute side by side.
// ---------------------------------------------------------------------------

func TestSQL16LiveStagedPivotCoexistence(t *testing.T) {
	svc := sql16LiveService(t)
	sql16SeedObservation(t, svc, "obs-1", map[string]any{"status": "final", "valueString": "x"})
	sql16SeedObservation(t, svc, "obs-3", map[string]any{"status": "final", "valueString": "y"})
	sql16SeedObservation(t, svc, "obs-4", map[string]any{"status": "final", "valueString": "z"})

	// The single-stage PIVOT goes through the registered inline door
	// (C-06), not the composition: one PIVOT step over the direct source.
	// Keys class, category status, value id (the three slot roles must be
	// pairwise distinct by the registered pivot contract).
	final := "final"
	single := sql16Compile(t, recipe.Output{
		Name: "sql16_single_pivot", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "class", ColumnID: "class_id", Expr: recipe.Expression{Select: "root.valueString"}},
			{Name: "rid", ColumnID: "rid_id", Expr: recipe.Expression{Select: "root.id"}},
		},
		Construction: &recipe.Construction{Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "status_id", Name: "status"}, {ID: "class_id", Name: "class"}, {ID: "rid_id", Name: "rid"},
			},
			Steps: []recipe.ConstructionStep{{
				ID: "pivot_classes", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
					ConstructionID: "class_pivot", GroupKeyIDs: []string{"class_id"},
					CategoryColumnID: "status_id", ValueColumnID: "rid_id",
					Categories: []recipe.ConstructionPivotCategory{
						{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &final}, OutputColumnID: "final_id"},
					},
					DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
					UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
				}},
				Outputs: []recipe.StageColumn{
					{ID: "class_id", Name: "class", Type: "string", Nullable: true},
					{ID: "final_id", Name: "final_cell", Type: "string", Nullable: true},
				},
			}}},
	})
	// The single-step PIVOT reaches the composition door's single-stage
	// claim (one stage; the C-06 receipt), while GROUP/CODED_GROUP/CODED_
	// PIVOT keep their untouched inline doors (C-07..C-09 goldens, offline
	// battery). Coexistence here: both pivot render paths mint CTE names
	// in one owned schema without collision and both execute.
	if single.StageSequence == nil || len(single.StageSequence.Stages) != 1 {
		t.Fatalf("single-step pivot compiled without a one-stage sequence: %#v", single.StageSequence)
	}
	singleRendered := mustRender(t, single, svc.schema)
	if !strings.Contains(singleRendered.SQL, "__loom_src AS") {
		t.Fatalf("single-stage pivot claim did not render the composition spine:\n%s", singleRendered.SQL)
	}
	sql16ExpectRows(t, svc, single, [][]string{
		{"x", "obs-1", sql16PivotIdentity("class_pivot", "STRING", `"x"`)},
		{"y", "obs-3", sql16PivotIdentity("class_pivot", "STRING", `"y"`)},
		{"z", "obs-4", sql16PivotIdentity("class_pivot", "STRING", `"z"`)},
	})

	// The composed staged PIVOT executes in the same owned schema.
	t2 := sql16ThreeStagePlans(t)["pivot-last"]
	sql16SeedObservation(t, svc, "obs-2", map[string]any{"status": "entered-in-errors", "note": []any{}})
	sql16ExpectRows(t, svc, t2, [][]string{
		{"final", "3", sql16PivotIdentity("tier_pivot", "STRING", `"final"`)},
	})
}

// ---------------------------------------------------------------------------
// L6 / P5 — the source-region SET arm (L-13.2 closure) over a production
// child-set recipe: the SET is lowering-minted, the composition renders
// it, and its removal would be the L-19.4 silent-drop class.
// ---------------------------------------------------------------------------

func TestSQL16LiveSourceRegionSET(t *testing.T) {
	svc := sql16LiveService(t)
	sql16SeedObservation(t, svc, "obs-1", map[string]any{"status": "final"})
	sql16SeedObservation(t, svc, "obs-2", map[string]any{"status": "entered-in-errors"})
	svc.insertResource(t, svc.schema, group13Project, group13Generation, "Patient", "pat-1", "FHIR/Patient",
		map[string]any{"gender": "female", "active": true})
	svc.insertEdge(t, control.EdgeOccurrenceRow{
		Project: group13Project, DatasetGeneration: group13Generation, AuthResourcePath: group13AuthPath,
		Label: "subject_Patient", SourceResourceType: "Observation", SourceLogicalID: "obs-1",
		TargetResourceType: "Patient", TargetLogicalID: "pat-1", SourcePath: "subject", SourceOrdinal: 0,
	})

	bundle := sql16SETCompositionBundle()

	plan, err := compileWithRegisteredSurfacePolicy(t, bundle)
	if err != nil {
		t.Fatalf("production compile: %v", err)
	}
	if plan.StageSequence == nil || len(plan.StageSequence.Stages) != 1 {
		t.Fatalf("expected a one-stage composition over the SET source, got %#v", plan.StageSequence)
	}
	sawSet, windowOpened := false, false
	for _, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalSetOp:
			if windowOpened {
				t.Fatalf("SET must precede the execution window (door law): %#v", plan.Operations)
			}
			sawSet = true
		case ir.PhysicalSortOp, ir.PhysicalLimitOp, ir.PhysicalReturnOp:
			windowOpened = true
		}
	}
	if !sawSet {
		t.Fatalf("production lowering emitted no child-set SET: %#v", plan.Operations)
	}
	// The SET arm is load-bearing: the correlated child set survives
	// rendering (its slice column arrives populated for the row that owns
	// the edge, empty for the one that does not — here only the kept row
	// is compared) and the FILTER stage converges to the literal row.
	rendered, err := renderPhysicalPlanForSchema(plan, svc.schema)
	if err != nil {
		svc.fail("render SET composition", err)
	}
	rows := svc.runRendered(t, rendered)
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want exactly the kept final row\nSQL: %s", rows, rendered.SQL)
	}
	if rows[0][0] != "final" {
		t.Fatalf("status cell = %q, want final", rows[0][0])
	}
	if !strings.Contains(rows[0][1], "female") || !strings.Contains(rows[0][1], "gender") {
		t.Fatalf("slice column %q lost the child-set witness (gender female)\nSQL: %s", rows[0][1], rendered.SQL)
	}
	if rows[0][len(rows[0])-1] != "obs-1" {
		t.Fatalf("pass-through identity = %q, want obs-1", rows[0][len(rows[0])-1])
	}
}

// compileWithRegisteredSurfacePolicy runs the production lowering with the
// endpoint-traversal rule ablated exactly as the registered PostgreSQL
// surface requires (obj11RegisteredSurfacePolicy, sanctioned single-rule
// switch); everything else is production default.
func compileWithRegisteredSurfacePolicy(t *testing.T, bundle recipe.Bundle) (ir.PhysicalPlan, error) {
	t.Helper()
	bindings := recipe.RuntimeBindings{
		Project: group13Project, DatasetGeneration: group13Generation,
		AuthResourcePaths: []string{group13AuthPath, "FHIR/Patient"},
	}
	if err := bundle.Validate(); err != nil {
		return ir.PhysicalPlan{}, err
	}
	semPlan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		return ir.PhysicalPlan{}, err
	}
	resolved, err := semantic.ResolveRecipePlan(semPlan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		return ir.PhysicalPlan{}, err
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy().
		WithRule(ir.PhysicalOptimizationRuleEndpointTraversal, false))
	if err != nil {
		return ir.PhysicalPlan{}, err
	}
	if len(compiled.Outputs) != 1 {
		return ir.PhysicalPlan{}, fmt.Errorf("compiled outputs = %d, want 1", len(compiled.Outputs))
	}
	output := compiled.Outputs[0]
	if output.OptimizedPlan != nil {
		return *output.OptimizedPlan, nil
	}
	return output.Plan, nil
}

// ---------------------------------------------------------------------------
// L7 — tail-index parity against the banked o1 oracle receipts: the G1
// emitter (documentPathStepsFromExpression) must read the LAST item for a
// bound negative index, exactly as the ArangoDB wire receipts attest.
// ---------------------------------------------------------------------------

func TestSQL16LiveTailIndexParity(t *testing.T) {
	svc := sql16LiveService(t)
	// The last component carries the only UCUM/cm reading: index 0 drops
	// empty (non-conforming unit), index -1 must read it — the oracle-
	// attested tail branch of the registered CASE, never an out-of-range
	// null and never an in-range alias.
	payload := map[string]any{"status": "final", "component": []any{
		map[string]any{"valueQuantity": map[string]any{"value": 7, "system": "urn:not-ucum", "code": "cm"}},
		map[string]any{"valueQuantity": map[string]any{"value": 9, "system": sql21UCUM, "code": "cm"}},
	}}
	svc.insertResource(t, svc.schema, "sql16-tail", sql21Gen, "Observation", "sql16-tail", sql21Path, payload)

	tail := sql21IndexedComponentProjection(t, -1)
	plan := sql21LivePlan("sql16-tail", "root", tail)
	rows := svc.runRendered(t, mustRender(t, plan, svc.schema))
	if len(rows) != 1 || rows[0][0] != "[9]" {
		t.Fatalf("tail read = %v, want [9] (o1 receipt: bound negative index reads the last array item)", rows)
	}
	rendered := mustRender(t, plan, svc.schema)
	for _, fragment := range []string{"jsonb_array_length(", "< 0 THEN", "-> ("} {
		if !strings.Contains(rendered.SQL, fragment) {
			t.Fatalf("tail-arm SQL lost fragment %q:\n%s", fragment, rendered.SQL)
		}
	}
}

// ---------------------------------------------------------------------------
// L9 — preview LIMIT binding, guard raises (ASSERT tags via 22P02), and
// (via TestMain) the verify_sql16v1_* census.
// ---------------------------------------------------------------------------

func TestSQL16LivePreviewLimitAndGuardRaises(t *testing.T) {
	svc := sql16LiveService(t)

	// Preview LIMIT: the terminal LIMIT binds a bigint parameter and
	// truncates the totally-ordered result to the bound count.
	sql16SeedObservation(t, svc, "obs-1", map[string]any{"status": "final", "note": []any{
		map[string]any{"text": "a"}, map[string]any{"text": "b"},
	}})
	limited := sql16ExpandGroupPlan(t)
	limited.StageSequence.PreviewLimitBindKey = "preview_limit"
	limited.BindVars["preview_limit"] = 2
	rendered := mustRender(t, limited, svc.schema)
	rows := svc.runRendered(t, rendered)
	if len(rows) != 2 {
		t.Fatalf("preview limit rows = %v, want the first 2 of the total order", rows)
	}
	// A non-positive or non-int bind is the registered typed refusal
	// (stage_sequence.go:452-453 twin).
	bad := ir.ClonePhysicalPlan(limited)
	bad.BindVars["preview_limit"] = 0
	if _, err := renderPhysicalPlanForSchema(bad, svc.schema); err == nil ||
		!strings.Contains(err.Error(), "preview limit bind") {
		t.Fatalf("non-positive preview bind refused with %v, want the registered refusal", err)
	}

	// Guard raise: a numeric EXPAND item reaches the ASSERT barrier and
	// raises 22P02 naming the registered tag (plain under EXPLAIN first).
	sql16SeedObservation(t, svc, "obs-bad-item", map[string]any{"status": "final", "note": []any{
		map[string]any{"text": 42},
	}})
	badItem := sql16ExpandGroupPlan(t)
	badItem.BindVars["project"] = group13Project
	guarded := mustRender(t, badItem, svc.schema)
	planText := strings.Builder{}
	for _, row := range svc.witness(t, "EXPLAIN (FORMAT TEXT) "+guarded.SQL, sql16DriverArgs(guarded)...) {
		planText.WriteString(strings.Join(row, "\n"))
	}
	if !strings.Contains(planText.String(), "SubPlan") && !strings.Contains(planText.String(), "Function Scan") {
		t.Fatalf("guard subplan missing from plan (barrier folded it):\n%s", planText.String())
	}
	_, err := svc.iso.Exec(svc.ctx, guarded.SQL, sql16DriverArgs(guarded)...)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "22P02" ||
		!strings.Contains(pgErr.Message, "CONSTRUCTION_EXPAND_ITEM_TYPE_MISMATCH") {
		t.Fatalf("numeric expand item = %v, want 22P02 naming CONSTRUCTION_EXPAND_ITEM_TYPE_MISMATCH", err)
	}

	// Guard raise: a numeric GROUP key raises the key-tag barrier.
	sql16SeedObservation(t, svc, "obs-bad-key", map[string]any{"status": 42})
	badKey := sql16ThreeStagePlans(t)["pivot-last"]
	guardedKey := mustRender(t, badKey, svc.schema)
	_, err = svc.iso.Exec(svc.ctx, guardedKey.SQL, sql16DriverArgs(guardedKey)...)
	if !errors.As(err, &pgErr) || pgErr.Code != "22P02" ||
		!strings.Contains(pgErr.Message, "CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH") {
		t.Fatalf("numeric group key = %v, want 22P02 naming CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH", err)
	}
}
