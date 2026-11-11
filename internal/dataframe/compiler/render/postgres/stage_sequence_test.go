package postgres

// Offline battery for pg-sql-16: the composition door (stage_sequence.go)
// renders ordered FILTER/GROUP/PIVOT/UNPIVOT/EXPAND construction stages as
// a WITH-CTE chain over typed intermediate rows. The live acceptance legs
// (L1-L9, P1-P5) live in stage_sequence_production_test.go; this file owns
// the dispatch/equivalence cells (U1), the invalid-reference cells (U2),
// the two-stage structural golden (U3), the refusal battery A2 requires
// for the converted blanket refusal (C-01..C-18 classes), and the law
// cells (U7) including the D-16a attribution verbatim law.
//
// D-16a attribution (mandatory, coordinator ruling 2026-11-11): CTE terminal re-sort over GROUP-last per coordinator ruling 2026-11-11; SQL CTE order unspecified; refines registered group total order.

import (
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

// sql16RunDeltaAudit is the 10th, outermost delta audit in the TestMain
// chain (main_test.go): pg-sql-16 owns the disjoint verify_sql16v1_*
// namespace exclusively and audits it through the parameterized runner
// (unit_normalization_test.go), composing with every sibling ticket's hook.
var (
	sql16FixturesMu      sync.Mutex
	sql16TrackedFixtures = map[string]bool{}
)

func sql16RunDeltaAudit(inner func() int) int {
	return runVerifyNamespaceDeltaAudit("verify_sql16v1_", &sql16FixturesMu, sql16TrackedFixtures, inner)
}

// sql16TrackResource registers one minted verify_sql16v1_* resource (a
// live leg's database or schema) with the package delta audit.
func sql16TrackResource(t *testing.T, name string) {
	t.Helper()
	sql16FixturesMu.Lock()
	defer sql16FixturesMu.Unlock()
	sql16TrackedFixtures[name] = true
}

// ---------------------------------------------------------------------------
// Builders: production-compiled composition shapes (zero hand-shaping of
// the sequence; mutations start from exactly these).
// ---------------------------------------------------------------------------

func sql16Compile(t *testing.T, output recipe.Output) ir.PhysicalPlan {
	t.Helper()
	bindings := recipe.RuntimeBindings{
		Project: group13Project, DatasetGeneration: group13Generation,
		AuthResourcePaths: []string{group13AuthPath},
	}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                output.Name, TranslationVersion: "test",
		Outputs: []recipe.Output{output},
	}
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
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("lower.CompileResolvedRecipePlan: %v", err)
	}
	if len(compiled.Outputs) != 1 {
		t.Fatalf("compiled outputs = %d, want 1", len(compiled.Outputs))
	}
	return compiled.Outputs[0].Plan
}

// sql16ExpandGroupPlan compiles the authored EXPAND→GROUP shape
// (lower/recipe_construction_reshape_test.go:43-95): preserve-parent
// EXPAND over root.note[].text with a public ordinal, then a keyed GROUP
// with three registered reducers.
func sql16ExpandGroupPlan(t *testing.T) ir.PhysicalPlan {
	t.Helper()
	output := recipe.Output{
		Name: "sql16_expand_group", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "tags", ColumnID: "tags_id", Expr: recipe.Expression{Select: "root.note[].text"}, ValueMode: recipe.ValueModeAll},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "status_id", Name: "status"}, {ID: "tags_id", Name: "tags"},
			},
			Steps: []recipe.ConstructionStep{
				{
					ID: "expand_tags", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionExpandOp, Expand: &recipe.ConstructionExpand{
						ConstructionID: "expand_tag_values", InputColumnID: "tags_id", OutputColumnID: "tag_id",
						OrdinalColumnID: "ordinal_id", EmptyPolicy: recipe.ExpansionPreserveParent,
					}},
					Outputs: []recipe.StageColumn{
						{ID: "status_id", Name: "status"}, {ID: "tag_id", Name: "tag"}, {ID: "ordinal_id", Name: "ordinal"},
					},
				},
				{
					ID:     "group_tags",
					Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand_tags"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
						ConstructionID: "group_tag_values",
						Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "tag_id", OutputColumnID: "grouped_tag_id"}},
						Aggregates: []recipe.ConstructionGroupAggregate{
							{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"},
							{Operation: recipe.ConstructionGroupCountNonNull, InputColumnID: "status_id", OutputColumnID: "status_count_id"},
							{Operation: recipe.ConstructionGroupCountDistinct, InputColumnID: "status_id", OutputColumnID: "status_distinct_id"},
						},
					}},
					Outputs: []recipe.StageColumn{
						{ID: "grouped_tag_id", Name: "tag"}, {ID: "rows_id", Name: "rows"},
						{ID: "status_count_id", Name: "status_count"}, {ID: "status_distinct_id", Name: "status_distinct"},
					},
				},
			},
		},
	}
	plan := sql16Compile(t, output)
	sequence := plan.StageSequence
	if sequence == nil || len(sequence.Stages) != 2 ||
		sequence.Stages[0].Kind != ir.PhysicalStageExpandOp || sequence.Stages[1].Kind != ir.PhysicalStageGroupOp {
		t.Fatalf("compiled plan is not the EXPAND→GROUP composition: %#v", sequence)
	}
	return ir.ClonePhysicalPlan(plan)
}

// sql16FilterStep is the registered FILTER stage step over a named input.
func sql16FilterStep(id string, inputKind recipe.ConstructionInputKind, inputStep, columnID string) recipe.ConstructionStep {
	not := "entered-in-errors"
	return recipe.ConstructionStep{
		ID: id, Inputs: []recipe.ConstructionInputRef{{Kind: inputKind, StepID: inputStep}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
			ColumnID: columnID, Operator: recipe.FilterNotEquals,
			Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &not}},
		}},
	}
}

func sql16ExpandStep(id string) recipe.ConstructionStep {
	return recipe.ConstructionStep{
		ID: id, Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "keep_rows"}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionExpandOp, Expand: &recipe.ConstructionExpand{
			ConstructionID: "expand_tag_values", InputColumnID: "tags_id", OutputColumnID: "tag_id",
			OrdinalColumnID: "ordinal_id", EmptyPolicy: recipe.ExpansionPreserveParent,
		}},
		Outputs: []recipe.StageColumn{
			{ID: "status_id", Name: "status"}, {ID: "tag_id", Name: "tag"}, {ID: "ordinal_id", Name: "ordinal"},
		},
	}
}

func sql16GroupStep(id, inputStep string) recipe.ConstructionStep {
	return recipe.ConstructionStep{
		ID: id, Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: inputStep}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
			ConstructionID: "group_tag_values",
			Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "tag_id", OutputColumnID: "grouped_tag_id"}},
			Aggregates: []recipe.ConstructionGroupAggregate{
				{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"},
				{Operation: recipe.ConstructionGroupCountNonNull, InputColumnID: "status_id", OutputColumnID: "status_count_id"},
				{Operation: recipe.ConstructionGroupCountDistinct, InputColumnID: "status_id", OutputColumnID: "status_distinct_id"},
			},
		}},
		Outputs: []recipe.StageColumn{
			{ID: "grouped_tag_id", Name: "tag"}, {ID: "rows_id", Name: "rows"},
			{ID: "status_count_id", Name: "status_count"}, {ID: "status_distinct_id", Name: "status_distinct"},
		},
	}
}

func sql16SourceFields() []recipe.Field {
	return []recipe.Field{
		{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
		{Name: "tags", ColumnID: "tags_id", Expr: recipe.Expression{Select: "root.note[].text"}, ValueMode: recipe.ValueModeAll},
	}
}

func sql16SourceColumns() []recipe.StageColumn {
	return []recipe.StageColumn{
		{ID: "status_id", Name: "status"}, {ID: "tags_id", Name: "tags"},
	}
}

// sql16ThreeStagePlans compiles the three U4 orderings, each distinguished
// by its LAST stage kind: group-last, pivot-last, expand-last.
func sql16ThreeStagePlans(t *testing.T) map[string]ir.PhysicalPlan {
	t.Helper()
	plans := map[string]ir.PhysicalPlan{}

	// T1 FILTER→EXPAND→GROUP (group-last).
	keep := sql16FilterStep("keep_rows", recipe.ConstructionSourceProjectionInput, "", "status_id")
	keep.Outputs = []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "tags_id", Name: "tags"}}
	plans["group-last"] = sql16Compile(t, recipe.Output{
		Name: "sql16_filter_expand_group", RootResourceType: "Observation", RowGrain: "observation",
		Fields: sql16SourceFields(),
		Construction: &recipe.Construction{Version: 1, SourceColumns: sql16SourceColumns(),
			Steps: []recipe.ConstructionStep{keep, sql16ExpandStep("expand_tags"), sql16GroupStep("group_tags", "expand_tags")}},
	})

	// T2 FILTER→GROUP→PIVOT (pivot-last): the pivot keys on the group's
	// key column, categorizes on a row-value ONE string, cells COUNT_ROWS.
	group := recipe.ConstructionStep{
		ID: "by_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "keep_rows"}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
			ConstructionID: "group_status_values",
			Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "status_id", OutputColumnID: "status_group_id"}},
			Aggregates:     []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
		}},
		RowValues: []recipe.ConstructionRowValue{{InputColumnID: "status_id", OutputColumnID: "tier_id", Policy: recipe.ConstructionRowValueOne}},
		Outputs: []recipe.StageColumn{
			{ID: "status_group_id", Name: "status_group", Type: "string", Nullable: true},
			{ID: "tier_id", Name: "tier", Type: "string", Nullable: true},
			{ID: "rows_id", Name: "rows", Type: "integer"},
		},
	}
	final := "final"
	pivot := recipe.ConstructionStep{
		ID: "pivot_tiers", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "by_status"}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
			ConstructionID: "tier_pivot", GroupKeyIDs: []string{"status_group_id"},
			CategoryColumnID: "tier_id", ValueColumnID: "rows_id",
			Categories: []recipe.ConstructionPivotCategory{{
				Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &final}, OutputColumnID: "final_rows_id",
			}},
			DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
		}},
		Outputs: []recipe.StageColumn{
			{ID: "status_group_id", Name: "status_group", Type: "string", Nullable: true},
			{ID: "final_rows_id", Name: "final_rows", Type: "integer", Nullable: true},
		},
	}
	keepT2 := sql16FilterStep("keep_rows", recipe.ConstructionSourceProjectionInput, "", "status_id")
	keepT2.Outputs = []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "tags_id", Name: "tags"}}
	plans["pivot-last"] = sql16Compile(t, recipe.Output{
		Name: "sql16_filter_group_pivot", RootResourceType: "Observation", RowGrain: "observation",
		Fields: sql16SourceFields(),
		Construction: &recipe.Construction{Version: 1, SourceColumns: sql16SourceColumns(),
			Steps: []recipe.ConstructionStep{keepT2, group, pivot}},
	})

	// T3 FILTER→GROUP→EXPAND (expand-last): the EXPAND consumes the
	// group's ALL row-value array column (D-16c melt-adjacent posture).
	groupT3 := recipe.ConstructionStep{
		ID: "by_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "keep_rows"}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
			ConstructionID: "group_status_values",
			Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "status_id", OutputColumnID: "status_group_id"}},
			Aggregates:     []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
		}},
		RowValues: []recipe.ConstructionRowValue{{InputColumnID: "status_id", OutputColumnID: "status_values_id", Policy: recipe.ConstructionRowValueAll}},
		Outputs: []recipe.StageColumn{
			{ID: "status_group_id", Name: "status_group", Type: "string", Nullable: true},
			{ID: "status_values_id", Name: "status_values", Type: "array"},
			{ID: "rows_id", Name: "rows", Type: "integer"},
		},
	}
	expandT3 := recipe.ConstructionStep{
		ID: "expand_values", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "by_status"}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionExpandOp, Expand: &recipe.ConstructionExpand{
			ConstructionID: "expand_status_values", InputColumnID: "status_values_id", OutputColumnID: "item_id",
			OrdinalColumnID: "ordinal_id", EmptyPolicy: recipe.ExpansionPreserveParent,
		}},
		Outputs: []recipe.StageColumn{
			{ID: "status_group_id", Name: "status_group", Type: "string", Nullable: true},
			{ID: "rows_id", Name: "rows", Type: "integer"},
			{ID: "item_id", Name: "item"}, {ID: "ordinal_id", Name: "ordinal"},
		},
	}
	plans["expand-last"] = sql16Compile(t, recipe.Output{
		Name: "sql16_filter_group_expand", RootResourceType: "Observation", RowGrain: "observation",
		Fields: sql16SourceFields(),
		Construction: &recipe.Construction{Version: 1, SourceColumns: sql16SourceColumns(),
			Steps: []recipe.ConstructionStep{keepT2, groupT3, expandT3}},
	})
	for name, plan := range plans {
		if plan.StageSequence == nil || len(plan.StageSequence.Stages) != 3 {
			t.Fatalf("%s: compiled plan is not a three-stage composition: %#v", name, plan.StageSequence)
		}
	}
	return plans
}

// ---------------------------------------------------------------------------
// U4: three-stage goldens, one per last-stage kind.
// ---------------------------------------------------------------------------

func TestSQL16ThreeStageComposedRenders(t *testing.T) {
	for _, cell := range []struct {
		name       string
		lastKind   ir.PhysicalStageOperationKind
		terminalBy string
	}{
		{"group-last", ir.PhysicalStageGroupOp, `ORDER BY s."c0" COLLATE "C" ASC NULLS FIRST, s."c4" COLLATE "C" ASC`},
		{"pivot-last", ir.PhysicalStagePivotOp, `COLLATE "C" ASC`},
		{"expand-last", ir.PhysicalStageExpandOp, `ORDER BY s."c4" COLLATE "C" ASC`},
	} {
		t.Run(cell.name, func(t *testing.T) {
			plans := sql16ThreeStagePlans(t)
			plan, ok := plans[cell.name]
			if !ok {
				t.Fatalf("no %s triple", cell.name)
			}
			last := plan.StageSequence.Stages[2]
			if last.Kind != cell.lastKind {
				t.Fatalf("%s: last stage kind = %q, want %q", cell.name, last.Kind, cell.lastKind)
			}
			rendered := sql16Render(t, plan)
			sql := rendered.SQL
			for _, fragment := range []string{
				"WITH __loom_src AS (",
				"__loom_stage_0 AS (", "__loom_stage_1 AS (", "__loom_stage_2 AS (",
				"FROM __loom_src AS", "FROM __loom_stage_2 AS s",
				cell.terminalBy,
			} {
				if !strings.Contains(sql, fragment) {
					t.Fatalf("%s: composed SQL lost fragment %q:\n%s", cell.name, fragment, sql)
				}
			}
			// Chain wiring: stage N reads exactly the prior relation
			// (S1 killer); the terminal reads stage 2.
			for _, relation := range []string{"__loom_src", "__loom_stage_0", "__loom_stage_1", "__loom_stage_2"} {
				if strings.Count(sql, "FROM "+relation) < 1 {
					t.Fatalf("%s: relation %s is never read:\n%s", cell.name, relation, sql)
				}
			}
			if strings.Contains(sql, "__loom_stage_3") {
				t.Fatalf("%s: phantom stage relation:\n%s", cell.name, sql)
			}
			// Final schema equals the terminal SELECT columns.
			if strings.Join(rendered.OutputColumns, ",") != strings.Join(finalColumnNames(plan), ",") {
				t.Fatalf("%s: OutputColumns %v != FinalColumns %v", cell.name,
					rendered.OutputColumns, finalColumnNames(plan))
			}
		})
	}
}

func finalColumnNames(plan ir.PhysicalPlan) []string {
	var names []string
	for _, column := range plan.StageSequence.FinalColumns {
		names = append(names, column.Name)
	}
	return names
}

// ---------------------------------------------------------------------------
// U5: per-stage schema-contract cells.
// ---------------------------------------------------------------------------

func TestSQL16StageSchemaContract(t *testing.T) {
	plans := sql16ThreeStagePlans(t)
	for name, base := range plans {
		t.Run(name, func(t *testing.T) {
			plan := ir.ClonePhysicalPlan(base)
			sequence := plan.StageSequence
			for index, stage := range sequence.Stages {
				wantInput := sequence.SourceStageID
				if index > 0 {
					wantInput = sequence.Stages[index-1].ID
				}
				if stage.InputStageID != wantInput {
					t.Fatalf("stage %d: InputStageID = %q, want %q (adjacent chain)", index, stage.InputStageID, wantInput)
				}
				identityHidden := false
				for _, projection := range stage.OutputProjections {
					if projection.Name == stage.RowIdentityColumn {
						identityHidden = projection.Hidden
					}
				}
				if !identityHidden {
					t.Fatalf("stage %d: row identity column %q is not the hidden projection", index, stage.RowIdentityColumn)
				}
			}
			// InputColumns of stage N carry every public column of N-1;
			// the only permitted extras are the internal scalar identity
			// pass-throughs the lowering stamps (_key, hidden).
			for index := 1; index < len(sequence.Stages); index++ {
				in := map[string]ir.PhysicalStageColumn{}
				for _, column := range sequence.Stages[index].InputColumns {
					in[column.Name] = column
				}
				for _, column := range publicColumnNames(sequence.Stages[index-1].OutputColumns) {
					if _, ok := in[column]; !ok {
						t.Fatalf("stage %d: prior public column %q is not an input", index, column)
					}
				}
				for _, column := range sequence.Stages[index].InputColumns {
					if !column.Internal && column.Name != "_key" {
						if nameIn(column.Name, publicColumnNames(sequence.Stages[index-1].OutputColumns)) {
							continue
						}
						t.Fatalf("stage %d: input column %q is neither prior-public nor internal", index, column.Name)
					}
				}
			}
			// Type-skew sabotage: renaming the group input column type
			// must die at the door (sameStageColumns law).
			skewed := ir.ClonePhysicalPlan(base)
			if skewed.StageSequence.Stages[1].InputColumns[0].Kind != "" {
				skewed.StageSequence.Stages[1].InputColumns[0].Kind = "INTEGER"
			} else {
				skewed.StageSequence.Stages[1].InputColumns[0].Kind = "__INVENTED__"
			}
			sql16RequireRefusal(t, skewed, "input schema differs")
		})
	}
}

func nameIn(name string, names []string) bool {
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}

func publicColumnNames(columns []ir.PhysicalStageColumn) []string {
	var names []string
	for _, column := range columns {
		if !column.Internal {
			names = append(names, column.Name)
		}
	}
	return names
}

func columnNames(columns []ir.PhysicalStageColumn) []string {
	var names []string
	for _, column := range columns {
		names = append(names, column.Name)
	}
	return names
}

// sql16Render is the entry-door render (the dispatcher must claim the plan
// through the composition door, not any inline slice).
func sql16Render(t *testing.T, plan ir.PhysicalPlan) command.RenderedPostgresPhysicalPlan {
	t.Helper()
	if !stageSequenceCompositionMentions(plan) {
		t.Fatalf("plan does not mention the composition surface: %#v", plan.StageSequence)
	}
	rendered, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("composition render: %v", err)
	}
	return rendered
}

func sql16RequireRefusal(t *testing.T, plan ir.PhysicalPlan, want string) {
	t.Helper()
	_, entryErr := RenderPhysicalPlan(plan, mustBinding(t))
	// Refusals come from the door (typed ErrUnsupportedPostgresPlan) or
	// from the ir schema gate the door runs (validate physical plan: …);
	// both are typed, never silent (requireGroup13Refusal convention).
	if entryErr == nil || (!strings.Contains(entryErr.Error(), ErrUnsupportedPostgresPlan.Error()) &&
		!strings.HasPrefix(entryErr.Error(), "validate physical plan:")) ||
		!strings.Contains(entryErr.Error(), want) {
		t.Fatalf("entry refusal = %v, want typed refusal containing %q", entryErr, want)
	}
	// Door-level equality: the composition door speaks the identical
	// message (entry==dispatch, U1 law).
	_, doorErr := renderStageSequencePhysicalPlan(plan, mustBinding(t))
	if doorErr == nil || doorErr.Error() != entryErr.Error() {
		t.Fatalf("door refused differently: entry=%v door=%v", entryErr, doorErr)
	}
}

// ---------------------------------------------------------------------------
// U3: two-stage structural golden (offline shape of the WITH chain).
// ---------------------------------------------------------------------------

func TestSQL16TwoStageComposedRender(t *testing.T) {
	rendered := sql16Render(t, sql16ExpandGroupPlan(t))
	sql := rendered.SQL
	for _, fragment := range []string{
		"WITH __loom_src AS (",
		"__loom_stage_0 AS (",
		"__loom_stage_1 AS (",
		"FROM __loom_src AS",
		"FROM __loom_stage_0)",
		"FROM __loom_stage_1 AS s",
		`AS "c0"`,
		`AS "c1"`,
		`AS "c2"`,
		`AS "c3"`,
		"jsonb_array_elements(",
		// The MANY array selector gathers over the iterated step's own
		// array (root.note[].text gathers doc.note, the AQL FLATTEN twin):
		// the guarded jsonb_typeof AND the jsonb_array_elements must both
		// read the payload through the prefix path, never the raw payload.
		`jsonb_typeof(r0."payload" #> $`,
		`jsonb_array_elements(r0."payload" #> $`,
		"WITH ORDINALITY",
		"ORDER BY",
		`COLLATE "C"`,
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("composed SQL lost fragment %q:\n%s", fragment, sql)
		}
	}
	// Chain shape: the group stage must read the expand relation, and the
	// terminal the last stage — each relation exactly once as a FROM
	// source, never skipped (S1 kills a wrong-relation wiring).
	if strings.Count(sql, "FROM __loom_src") != 1 ||
		strings.Count(sql, "FROM __loom_stage_0") != 1 ||
		strings.Count(sql, "FROM __loom_stage_1") != 1 {
		t.Fatalf("CTE chain relations wrong (each must be read exactly once):\n%s", sql)
	}
	if got, want := rendered.OutputColumns,
		[]string{"tag", "rows", "status_count", "status_distinct", "__loom_row_id"}; len(got) != len(want) {
		t.Fatalf("output columns = %v, want %v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("output column %d = %q, want %q", i, got[i], want[i])
			}
		}
	}
	// Population isolation: no root-contributor set column may ride the
	// composed statement (validateStageColumnClass law).
	if strings.Contains(sql, "__loom_root_contributor") {
		t.Fatalf("population-bound contributor column leaked into the composition:\n%s", sql)
	}
}

// ---------------------------------------------------------------------------
// U2: invalid chain-reference cells (acceptance clause 2). Every cell is
// ONE mutation of the compiled canonical plan; the canonical render is the
// ALIVE control.
// ---------------------------------------------------------------------------

func TestSQL16ChainRefusals(t *testing.T) {
	base := sql16ExpandGroupPlan(t)
	if _, err := RenderPhysicalPlan(ir.ClonePhysicalPlan(base), mustBinding(t)); err != nil {
		t.Fatalf("ALIVE control: the canonical composition must render: %v", err)
	}
	for _, cell := range []struct {
		name   string
		mutate func(*ir.PhysicalPlan)
		want   string
	}{
		{"zero-stages", func(p *ir.PhysicalPlan) { p.StageSequence.Stages = nil },
			"at least one construction stage"},
		{"stage-id-empty", func(p *ir.PhysicalPlan) { p.StageSequence.Stages[0].ID = "" },
			"ID is empty, untrimmed, or duplicated (L-16.1)"},
		{"non-adjacent-input", func(p *ir.PhysicalPlan) {
			p.StageSequence.Stages[1].InputStageID = p.StageSequence.SourceStageID
		}, "does not reference prior stage"},
		{"self-input", func(p *ir.PhysicalPlan) {
			p.StageSequence.Stages[1].InputStageID = p.StageSequence.Stages[1].ID
		}, "does not reference prior stage"},
		{"final-stage-wrong", func(p *ir.PhysicalPlan) {
			p.StageSequence.FinalStageID = p.StageSequence.Stages[0].ID
		}, "final stage ID"},
		{"final-identity-drift", func(p *ir.PhysicalPlan) {
			p.StageSequence.FinalRowIdentity = "__loom_other_id"
		}, "final row identity"},
		{"group-input-unknown-column", func(p *ir.PhysicalPlan) {
			p.StageSequence.Stages[1].Group.Keys[0].InputColumn = "no_such_column"
		}, "is not a public scalar input"}, // the ir schema gate speaks first (same law, typed)
	} {
		t.Run(cell.name, func(t *testing.T) {
			plan := ir.ClonePhysicalPlan(base)
			cell.mutate(&plan)
			sql16RequireRefusal(t, plan, cell.want)
		})
	}
}

// ---------------------------------------------------------------------------
// A2 receipt battery: every shape that formerly hit the blanket refusal
// receives a typed refusal or a render. One probe per class, zero silent
// paths, and (U1) the door speaks the identical message.
// ---------------------------------------------------------------------------

func TestSQL16RefusalBattery(t *testing.T) {
	base := sql16ExpandGroupPlan(t)
	if _, err := RenderPhysicalPlan(ir.ClonePhysicalPlan(base), mustBinding(t)); err != nil {
		t.Fatalf("ALIVE control: the canonical composition must render: %v", err)
	}
	for _, cell := range []struct {
		name   string
		mutate func(*ir.PhysicalPlan)
		want   string
	}{
		{"C-10 cohort-group", func(p *ir.PhysicalPlan) {
			p.StageSequence.Stages[1].Kind = ir.PhysicalStageCohortGroupOp
		}, "COHORT_GROUP stage (index 1) is not part of the registered composition surface"},
		{"C-11 related-source", func(p *ir.PhysicalPlan) {
			p.StageSequence.Stages[1].Kind = ir.PhysicalStageRelatedSourceOp
		}, "the RELATED_SOURCE stage is a related-route capability and is refused in composition"},
		{"C-13 coded-group voice", func(p *ir.PhysicalPlan) {
			p.StageSequence.Stages[1].Kind = ir.PhysicalStageCodedGroupOp
		}, "the registered coded-group slice admits exactly one CODED_GROUP construction stage; multi-stage composition is a pg-sql-16 capability gap"},
		{"C-14 coded pivot trailing voice", func(p *ir.PhysicalPlan) {
			p.StageSequence.Stages[1].GroupedPivot = &ir.PhysicalGroupedPivot{
				CodedCorrelation: &ir.PhysicalCorrelation{},
			}
		}, "violates the ir rule that the coded Pivot must be the first stage over the direct source projection"},
		{"C-16 lineage return", func(p *ir.PhysicalPlan) {
			p.StageSequence.RowLineageReturn = &ir.PhysicalRowLineageReturn{}
		}, "row-lineage, cell-trace, and population-mapping returns have no PostgreSQL compiled route"},
		{"C-17 sequence window", func(p *ir.PhysicalPlan) {
			p.StageSequence.PreviewSourceWindowByRootID = true
		}, "preview source windowing and the terminal Pivot window are pg-sql-22/23 terminal windowing contracts"},
		{"C-17 plan window", func(p *ir.PhysicalPlan) {
			p.PreviewSourceWindowByRootID = true
		}, "preview source windowing and the terminal Pivot window are pg-sql-22/23 terminal windowing contracts"},
		{"C-18 kind payload mismatch", func(p *ir.PhysicalPlan) {
			p.StageSequence.Stages[1].Group = nil
		}, "re-read at the door"},
		{"preview limit non-positive", func(p *ir.PhysicalPlan) {
			p.StageSequence.PreviewLimitBindKey = "limit"
			p.BindVars["limit"] = 0
		}, "composition preview limit bind"},
		{"group-arm-semantic", func(p *ir.PhysicalPlan) {
			p.StageSequence.Stages[1].Group.SemanticKey = &ir.PhysicalStageGroupSemanticKey{}
		}, "the semantic family Group key is a registered named gap (L-13.3"},
		{"group-arm-related-occurrence", func(p *ir.PhysicalPlan) {
			p.StageSequence.Stages[1].Group.RelatedOccurrence = &ir.PhysicalStageGroupRelatedOccurrenceAdapter{}
		}, "unreferenced related route adapter"}, // G3(c) CLOSED the door refusal: the adapter now reaches the dispatch arm, and the ir bound-field gate (physical_stage_validation.go:2143) speaks first; behind it validateStageRelatedOccurrenceAdapter is the fail-closed renderer re-read (TestSQL16RelatedOccurrenceRefusals pins its voices)
		{"group-arm-coding-key", func(p *ir.PhysicalPlan) {
			p.StageSequence.Stages[1].Group.CodingKey = &ir.PhysicalStageGroupCodingKey{}
		}, "CodingKey group keys are refused at the composition payload gate"},
		{"group-arm-root-contributor", func(p *ir.PhysicalPlan) {
			p.StageSequence.Stages[1].Group.RootContributorInputColumn = "status"
		}, "the root-contributor set is production-unreachable"},
		// D-16f pins: the composition carries no registered correlated-
		// comparison surface (predicates.go:276 twin); every correlation
		// shape must die typed, never silently.
		{"D-16f correlated filter predicate", func(p *ir.PhysicalPlan) {
			last := len(p.Operations) - 1
			filterOp := ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{
				Predicate: ir.PhysicalPredicate{
					Operator: "EXISTS",
					Correlation: &ir.PhysicalCorrelation{
						Source:         ir.PhysicalValue{Variable: p.Operations[0].RootScan.Variable, Path: []string{"payload"}},
						ResourceType:   "Observation",
						OwnerResource:  "CodeableConcept",
						KeyResource:    "Coding",
						KeyIsArray:     true,
						KeySelector:    spec.Selector{Steps: []spec.SelectorStep{{Field: "coding", Iterate: true}}},
						ValueScope:     fhirschema.CorrelatedValueScopeCoding,
						SystemSelector: spec.Selector{Steps: []spec.SelectorStep{{Field: "system"}}},
						CodeSelector:   spec.Selector{Steps: []spec.SelectorStep{{Field: "code"}}},
						ValueSelector:  spec.Selector{Steps: []spec.SelectorStep{{Field: "display"}}},
						LogicalType:    "string",
						SystemBindKey:  "corrsys",
						CodeBindKey:    "corrcode",
					},
				},
			}}
			ops := append([]ir.PhysicalOperation{}, p.Operations[:last]...)
			p.Operations = append(append(ops, filterOp), p.Operations[last])
			p.BindVars["corrsys"] = "http://loinc.org"
			p.BindVars["corrcode"] = "systolic"
		}, "correlated predicate"},
		{"D-16f version selector without bind", func(p *ir.PhysicalPlan) {
			last := len(p.Operations) - 1
			filterOp := ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{
				Predicate: ir.PhysicalPredicate{
					Operator: "EXISTS",
					Correlation: &ir.PhysicalCorrelation{
						Source:          ir.PhysicalValue{Variable: p.Operations[0].RootScan.Variable, Path: []string{"payload"}},
						ResourceType:    "Observation",
						OwnerResource:   "CodeableConcept",
						KeyResource:     "Coding",
						KeyIsArray:      true,
						KeySelector:     spec.Selector{Steps: []spec.SelectorStep{{Field: "coding", Iterate: true}}},
						ValueScope:      fhirschema.CorrelatedValueScopeCoding,
						SystemSelector:  spec.Selector{Steps: []spec.SelectorStep{{Field: "system"}}},
						CodeSelector:    spec.Selector{Steps: []spec.SelectorStep{{Field: "code"}}},
						ValueSelector:   spec.Selector{Steps: []spec.SelectorStep{{Field: "display"}}},
						LogicalType:     "string",
						VersionSelector: &spec.Selector{Steps: []spec.SelectorStep{{Field: "version"}}},
					},
				},
			}}
			ops := append([]ir.PhysicalOperation{}, p.Operations[:last]...)
			p.Operations = append(append(ops, filterOp), p.Operations[last])
		}, "correlation version bind key is required"},
		{"auth path bind", func(p *ir.PhysicalPlan) {
			p.StageSequence.OutputAuthResourcePathBindKey = "auth_path"
		}, "composite Combine prefix authorization path is not registered"},
	} {
		t.Run(cell.name, func(t *testing.T) {
			plan := ir.ClonePhysicalPlan(base)
			cell.mutate(&plan)
			sql16RequireRefusal(t, plan, cell.want)
		})
	}
}

// TestSQL16CodedVoicesByteEqual proves the A3 ruling: the composition's
// L-16.2 refusal is byte-equal to the coded-group door's own multi-stage
// voice, so dispatch-level and door-level refusals can never drift (the
// #132 voice law). The coded door's len(Stages) != 1 arm fires before any
// payload read, so marking the compiled stages coded-kind is a faithful
// probe of the door's registered text.
func TestSQL16CodedVoicesByteEqual(t *testing.T) {
	doorPlan := sql16ExpandGroupPlan(t)
	doorPlan.StageSequence.Stages[0].Kind = ir.PhysicalStageCodedGroupOp
	_, doorErr := renderCodedGroupPhysicalPlan(doorPlan, mustBinding(t))
	if doorErr == nil || !strings.Contains(doorErr.Error(),
		"the registered coded-group slice admits exactly one CODED_GROUP construction stage") {
		t.Fatalf("coded-group door voice drifted: %v", doorErr)
	}
	composed := sql16ExpandGroupPlan(t)
	composed.StageSequence.Stages[1].Kind = ir.PhysicalStageCodedGroupOp
	_, compositionErr := renderStageSequencePhysicalPlan(composed, mustBinding(t))
	if compositionErr == nil {
		t.Fatal("composition must refuse a CODED_GROUP stage")
	}
	if !strings.Contains(compositionErr.Error(),
		"the registered coded-group slice admits exactly one CODED_GROUP construction stage; multi-stage composition is a pg-sql-16 capability gap") {
		t.Fatalf("composition voice drifted from the coded-group door:\ncoded=%q\ncomposition=%q", doorErr, compositionErr)
	}
}

// ---------------------------------------------------------------------------
// U7 law cells.
// ---------------------------------------------------------------------------

func TestSQL16LawCells(t *testing.T) {
	// D-16a attribution must be present and verbatim in the owning source.
	source, err := os.ReadFile("stage_sequence.go")
	if err != nil {
		t.Fatalf("read stage_sequence.go: %v", err)
	}
	const attribution = "CTE terminal re-sort over GROUP-last per coordinator ruling 2026-11-11; SQL CTE order unspecified; refines registered group total order"
	if !strings.Contains(string(source), attribution) {
		t.Fatal("stage_sequence.go lost the mandatory D-16a attribution line")
	}
	ownSource, err := os.ReadFile("stage_sequence_test.go")
	if err != nil {
		t.Fatalf("read stage_sequence_test.go: %v", err)
	}
	if !strings.Contains(string(ownSource), attribution) {
		t.Fatal("the terminal-order test register lost the verbatim D-16a attribution")
	}
	// The registered CTE vocabulary is closed: only __loom_src,
	// __loom_stage_N, and slice-owned suffixes may name relations.
	rendered := sql16Render(t, sql16ExpandGroupPlan(t))
	for _, forbidden := range []string{"; ", "DROP ", "DELETE ", "INSERT ", "UPDATE ", "SET search_path"} {
		if strings.Contains(strings.ToUpper(rendered.SQL), strings.ToUpper(forbidden)) {
			t.Fatalf("forbidden fragment %q in composed SQL:\n%s", forbidden, rendered.SQL)
		}
	}
	// Preview-limit binding stays a typed refusal for non-int binds
	// (the A4 twin of stage_sequence.go:452-453).
	plan := sql16ExpandGroupPlan(t)
	plan.StageSequence.PreviewLimitBindKey = "limit"
	plan.BindVars = map[string]any{"limit": "7"}
	_, err = RenderPhysicalPlan(plan, mustBinding(t))
	if err == nil || !strings.Contains(err.Error(), "must be a positive int") {
		t.Fatalf("string-bound preview limit must be a typed refusal, got %v", err)
	}
	if plan.BindVars == nil {
		plan.BindVars = map[string]any{}
	}
	plan.BindVars["limit"] = 0
	if _, err = RenderPhysicalPlan(plan, mustBinding(t)); err == nil ||
		!strings.Contains(err.Error(), "must be a positive int") {
		t.Fatalf("zero preview limit must be a typed refusal, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Canonical-key selector arm (the traversal.go:490-501 canonicalization
// twin at the composition source region): a stored FHIR document never
// carries an "id"/"_key" property, so a root single-step id/_key selector
// reads the logical_id sidecar and its presence companion reads sidecar
// existence. The payload-path form silently renders SQL NULL.
// ---------------------------------------------------------------------------

func TestSQL16KeySelectorSidecar(t *testing.T) {
	final := "final"
	plan := sql16Compile(t, recipe.Output{
		Name: "sql16_key_sidecar", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "rid", ColumnID: "rid_id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "val", ColumnID: "val_id", Expr: recipe.Expression{Select: "root.valueString"}},
		},
		Construction: &recipe.Construction{Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "status_id", Name: "status"}, {ID: "rid_id", Name: "rid"}, {ID: "val_id", Name: "val"},
			},
			Steps: []recipe.ConstructionStep{{
				ID: "pivot_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
					ConstructionID: "status_pivot", GroupKeyIDs: []string{"val_id"},
					CategoryColumnID: "status_id", ValueColumnID: "rid_id",
					Categories: []recipe.ConstructionPivotCategory{
						{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &final}, OutputColumnID: "final_id"},
					},
					DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
					UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
				}},
				Outputs: []recipe.StageColumn{
					{ID: "val_id", Name: "val", Type: "string", Nullable: true},
					{ID: "final_id", Name: "final_cell", Type: "string", Nullable: true},
				},
			}}},
	})
	if plan.StageSequence == nil || len(plan.StageSequence.Stages) != 1 {
		t.Fatalf("plan is not a one-stage composition: %#v", plan.StageSequence)
	}
	// The single-stage claim (C-06) reaches the same door through the
	// dispatch spine: the entry render, not sql16Render's composition
	// mention check.
	rendered, err := RenderPhysicalPlan(ir.ClonePhysicalPlan(plan), mustBinding(t))
	if err != nil {
		t.Fatalf("entry render: %v", err)
	}
	sql := rendered.SQL
	// The key column is a single exact sidecar read (the payload-path
	// form is impossible with this pin, and the column alias is emitted
	// exactly once).
	// The key column is the sidecar read; the payload-path form (which
	// would silently render SQL NULL) must not appear for it anywhere.
	if !strings.Contains(sql, `r0."logical_id" AS "c1"`) {
		t.Fatalf("key selector did not render the logical_id sidecar read:\n%s", sql)
	}
	// The scalar fact reaches the pivot source region as a single wrap,
	// never a jsonb path re-read.
	if !strings.Contains(sql, `to_jsonb(s."c1")`) {
		t.Fatalf("scalar key fact was re-read as a jsonb path:\n%s", sql)
	}
	// Door-level equality (U1 law): the composition door renders the
	// identical statement.
	door, err := renderStageSequencePhysicalPlan(ir.ClonePhysicalPlan(plan), mustBinding(t)) //nolint:govet // fresh shadow of the entry-render error is intentional
	if err != nil {
		t.Fatalf("direct door: %v", err)
	}
	if door.SQL != sql {
		t.Fatalf("door SQL drifted from the entry render")
	}
}

// sql16SETCompositionBundle builds the authored optional-traversal
// representative-slice shape whose resolved source carries the SET slice
// column occ_patient__representatives (object, many) beside the public
// status read, with a one-stage FILTER pass-through (L6/P5 live legs and
// the offline slice-arm pin share this exact authored shape).
func sql16SETCompositionBundle() recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "sql16_source_region_set", TranslationVersion: "test",
		Outputs: []recipe.Output{{
			Name: "set_composition", RootResourceType: "Observation", RowGrain: "resource",
			Fields: []recipe.Field{{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}}},
			Traversals: []recipe.Traversal{{
				Name: "subject_Patient", Alias: "occ_patient", ToResourceType: "Patient", MatchMode: recipe.MatchOptional,
				Slices: []recipe.RepresentativeSlice{{
					Name: "representatives", Limit: 1,
					Fields: []recipe.Field{
						{Name: "gender", Expr: recipe.Expression{Select: "occ_patient.gender"}},
						{Name: "active", Expr: recipe.Expression{Select: "occ_patient.active"}},
					},
				}},
			}},
			// The resolved typed source emits BOTH public columns — the
			// direct status read and the child-set slice column
			// occ_patient__representatives (object, many) — and the
			// construction source schema must match it exactly; the
			// FILTER stage passes the slice column through untouched.
			Construction: &recipe.Construction{Version: 1,
				SourceColumns: []recipe.StageColumn{
					{ID: "status_id", Name: "status"},
					{ID: "reps_id", Name: "occ_patient__representatives", Type: "array"},
				},
				Steps: []recipe.ConstructionStep{{
					ID:     "keep_status",
					Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "status_id", Operator: recipe.FilterEquals,
						Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: func() *string { v := "final"; return &v }()}},
					}},
					Outputs: []recipe.StageColumn{
						{ID: "status_id", Name: "status"},
						{ID: "reps_id", Name: "occ_patient__representatives", Type: "array", Nullable: true},
					},
				}}},
		}},
	}
}

// ---------------------------------------------------------------------------
// SET slice-column source arm: the representative-slice composite of a
// correlated child set renders through the registered child-set slice
// contract (objects.go setBodies arm), materialized as the correlated
// edge-join jsonb_agg in the composition source region — the group
// materializer's composite refusal must not be reached (the L-19.4
// silent-drop law inverted: the SET column survives rendering).
// ---------------------------------------------------------------------------

func TestSQL16SETSliceSourceColumn(t *testing.T) {
	plan, err := compileWithRegisteredSurfacePolicy(t, sql16SETCompositionBundle())
	if err != nil {
		t.Fatalf("production compile: %v", err)
	}
	if plan.StageSequence == nil || len(plan.StageSequence.Stages) != 1 {
		t.Fatalf("not a one-stage composition: %#v", plan.StageSequence)
	}
	rendered, err := RenderPhysicalPlan(ir.ClonePhysicalPlan(plan), mustBinding(t))
	if err != nil {
		t.Fatalf("entry render: %v", err)
	}
	sql := rendered.SQL
	for _, fragment := range []string{
		`fhir_edge_occurrence" AS e1`,              // the correlated child-set hop
		`e1."source_logical_id" = r0."logical_id"`, // correlated to the parent row
		`jsonb_build_object(`,                      // the slice item's fields
		`COALESCE(jsonb_agg(sl2."item" ORDER BY`,   // document-order gather, EMPTY_ON_NULL posture
		`AS "c1"`,                                  // the slice column slot
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("SET slice column lost fragment %q:\n%s", fragment, sql)
		}
	}
	door, err := renderStageSequencePhysicalPlan(ir.ClonePhysicalPlan(plan), mustBinding(t))
	if err != nil {
		t.Fatalf("direct door: %v", err)
	}
	if door.SQL != sql {
		t.Fatalf("door SQL drifted from the entry render")
	}
}
