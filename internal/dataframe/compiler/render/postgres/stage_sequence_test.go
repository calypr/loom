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
	"regexp"
	"strconv"
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
		// ir :2632-2637 re-read at the door: the named row identity must
		// be internal AND identity-marked. (ir counts no identity columns,
		// so the door has no identity-count cell either — the removed
		// identities>1 arm had no ir counterpart, PR #137 review.)
		{"row-identity-not-identity-marked", func(p *ir.PhysicalPlan) {
			last := &p.StageSequence.Stages[len(p.StageSequence.Stages)-1]
			for i := range last.OutputColumns {
				if last.OutputColumns[i].Name == last.RowIdentityColumn {
					last.OutputColumns[i].Identity = false
				}
			}
		}, "must be internal and identity-marked"},
		{"row-identity-not-internal", func(p *ir.PhysicalPlan) {
			last := &p.StageSequence.Stages[len(p.StageSequence.Stages)-1]
			for i := range last.OutputColumns {
				if last.OutputColumns[i].Name == last.RowIdentityColumn {
					last.OutputColumns[i].Internal = false
				}
			}
		}, "must be internal and identity-marked"},
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

// ---------------------------------------------------------------------------
// Delivery-record RED-capable pins for the live-found source repairs
// (coordinator order 2026-11-11): each repair gets a named offline cell that
// fails on the pre-repair shape, not merely passes on the fixed one. The
// root id/_key sidecar repair is pinned by TestSQL16KeySelectorSidecar; the
// SET slice-column survival by TestSQL16SETSliceSourceColumn; the three
// shapes below close the rest of the register.
// ---------------------------------------------------------------------------

func TestSQL16ArraySelectorIteratePathBind(t *testing.T) {
	// Live-found repair (commit 57a6680f): the guarded gather reads the
	// array AT the iterated selector step, INCLUSIVE of its own field
	// (root.note[].text gathers doc.note — the AQL FLATTEN twin iterates
	// doc.note, not doc). The pre-repair slice excluded the iterate step,
	// so the gather read the raw payload with no path bind: jsonb_typeof
	// over the whole document silently gathered nothing. Pinning the bind
	// VALUE (not just the #> shape, which U3 already holds) is what kills
	// both the exclusive slice and an over-inclusive slice.
	rendered := sql16Render(t, sql16ExpandGroupPlan(t))
	sql := rendered.SQL
	guard := regexp.MustCompile(`jsonb_typeof\(r0\."payload" #> \$(\d+)::text\[\]\) = 'array'`).FindStringSubmatch(sql)
	gather := regexp.MustCompile(`FROM jsonb_array_elements\(r0\."payload" #> \$(\d+)::text\[\]\) AS e`).FindStringSubmatch(sql)
	if guard == nil || gather == nil {
		t.Fatalf("the guarded gather lost its path-bound payload read:\n%s", sql)
	}
	if guard[1] != gather[1] {
		t.Fatalf("the typeof guard and the elements gather read different paths (guard $%s, gather $%s):\n%s", guard[1], gather[1], sql)
	}
	pathIndex, err := strconv.Atoi(guard[1])
	if err != nil || pathIndex < 1 || pathIndex > len(rendered.Args) {
		t.Fatalf("gather path parameter $%s is not a minted bind", guard[1])
	}
	if bound, ok := rendered.Args[pathIndex-1].Value.(string); !ok || bound != "{note}" {
		t.Fatalf("the gather path binds %v, want {note} (the iterated step's own field, inclusive)", rendered.Args[pathIndex-1].Value)
	}
	tail := regexp.MustCompile(`SELECT jsonb_agg\(e #> \$(\d+)::text\[\]\)`).FindStringSubmatch(sql)
	if tail == nil {
		t.Fatalf("the gather lost its leaf tail:\n%s", sql)
	}
	tailIndex, err := strconv.Atoi(tail[1])
	if err != nil || tailIndex < 1 || tailIndex > len(rendered.Args) {
		t.Fatalf("leaf tail parameter $%s is not a minted bind", tail[1])
	}
	if bound, ok := rendered.Args[tailIndex-1].Value.(string); !ok || bound != "{text}" {
		t.Fatalf("the leaf tail binds %v, want {text}", rendered.Args[tailIndex-1].Value)
	}
	// The pre-repair shape exactly: the gather must never read the raw
	// payload without the path.
	if strings.Contains(sql, `jsonb_typeof(r0."payload") = 'array'`) {
		t.Fatalf("the gather regressed to the pathless raw-payload read:\n%s", sql)
	}
}

func TestSQL16RowValuePolicyStorageClasses(t *testing.T) {
	// Live-found repair (commit 57a6680f, jsonbOutputs): the group emitter
	// materializes an ALL row value as to_jsonb(ARRAY_AGG(...)) — jsonb —
	// while the ONE policy collapses through MIN(...) and stays a SQL
	// scalar, and the stage FACT carries that class so a later stage reads
	// each through its own storage class. Marking a ONE row value jsonb
	// made a later stage read it with a document operator and abort at
	// analysis (42883, live-found); this cell is the offline RED twin.
	keepPolicy := sql16FilterStep("keep_rows", recipe.ConstructionSourceProjectionInput, "", "status_id")
	keepPolicy.Outputs = []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "tags_id", Name: "tags"}}
	groupOutputs := []recipe.StageColumn{
		{ID: "status_key_id", Name: "status_key"}, {ID: "rows_id", Name: "rows"},
		{ID: "tags_all_id", Name: "tags_all"}, {ID: "status_one_id", Name: "status_one"},
	}
	filterOne := sql16FilterStep("filter_one", recipe.ConstructionStepOutputInput, "group_status", "status_one_id")
	filterOne.Outputs = groupOutputs
	plan := sql16Compile(t, recipe.Output{
		Name: "sql16_policy_storage", RootResourceType: "Observation", RowGrain: "observation",
		Fields: sql16SourceFields(),
		Construction: &recipe.Construction{Version: 1, SourceColumns: sql16SourceColumns(),
			Steps: []recipe.ConstructionStep{
				keepPolicy,
				{
					ID: "group_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "keep_rows"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
						ConstructionID: "group_status_values",
						Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "status_id", OutputColumnID: "status_key_id"}},
						Aggregates: []recipe.ConstructionGroupAggregate{
							{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"},
						},
					}},
					RowValues: []recipe.ConstructionRowValue{
						{InputColumnID: "status_id", OutputColumnID: "tags_all_id", Policy: recipe.ConstructionRowValueAll},
						{InputColumnID: "status_id", OutputColumnID: "status_one_id", Policy: recipe.ConstructionRowValueOne},
					},
					Outputs: []recipe.StageColumn{
						{ID: "status_key_id", Name: "status_key"}, {ID: "rows_id", Name: "rows"},
						{ID: "tags_all_id", Name: "tags_all"}, {ID: "status_one_id", Name: "status_one"},
					},
				},
				filterOne,
			},
		},
	})
	rendered := sql16Render(t, plan)
	sql := rendered.SQL
	alias := func(marker string) string {
		match := regexp.MustCompile(regexp.QuoteMeta(marker) + ` AS "(c\d+)"`).FindStringSubmatch(sql)
		if match == nil {
			t.Fatalf("%s never materialized into a stage column:\n%s", marker, sql)
		}
		return match[1]
	}
	allAlias := alias("__loom_g1_rv0")
	oneAlias := alias("__loom_g1_rv1")
	for _, fragment := range []string{
		"to_jsonb(COALESCE(ARRAY_AGG(DISTINCT", // the ALL policy emitter (jsonb)
		"(CASE WHEN COUNT(DISTINCT",            // the ONE policy at-most-one guard
		"THEN MIN(",                            // the ONE policy scalar collapse
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("the group region lost the row-value policy fragment %q:\n%s", fragment, sql)
		}
	}
	// The downstream stage reads each fact through its OWN storage class:
	// the ALL column through a document path, the ONE column bare.
	if !strings.Contains(sql, `s."`+allAlias+`" #> $`) {
		t.Fatalf("the ALL row value fact lost its jsonb downstream read:\n%s", sql)
	}
	if strings.Contains(sql, `s."`+oneAlias+`" #> `) {
		t.Fatalf("the ONE row value fact was marked jsonb (the 42883 class):\n%s", sql)
	}
	if !strings.Contains(sql, `s."`+oneAlias+`" AS "`) {
		t.Fatalf("the ONE row value fact was not bare-passthrough:\n%s", sql)
	}
	if !strings.Contains(sql, `WHERE s."`+oneAlias+`" IS DISTINCT FROM $`) ||
		strings.Contains(sql, `"`+oneAlias+`" IS DISTINCT FROM (`) {
		t.Fatalf("the downstream filter did not compare the ONE column as a scalar:\n%s", sql)
	}
	door, err := renderStageSequencePhysicalPlan(ir.ClonePhysicalPlan(plan), mustBinding(t))
	if err != nil {
		t.Fatalf("direct door: %v", err)
	}
	if door.SQL != sql {
		t.Fatalf("door SQL drifted from the entry render")
	}
}

func TestSQL16SETSliceSilentDropCounterfactual(t *testing.T) {
	// The slice-column-shaped silent-drop probe (coordinator order
	// 2026-11-11): the registered child-set arm is the ONLY landing for a
	// SET-sourced slice column — TestSQL16SETSliceSourceColumn pins the
	// survival render, this cell pins the complement. A slice whose source
	// variable the source region never registered as a child set must die
	// TYPED at every door (the scope gate the door runs speaks first, the
	// registered convention of the ir-gate battery cells; behind it the
	// materializer's composite refusal stands), never render the row minus
	// the column (the L-19.4 silent-drop law in its probe form).
	plan, err := compileWithRegisteredSurfacePolicy(t, sql16SETCompositionBundle())
	if err != nil {
		t.Fatalf("production compile: %v", err)
	}
	last := len(plan.Operations) - 1
	mutated := false
	for i := range plan.Operations[last].Return.Projections {
		projection := &plan.Operations[last].Return.Projections[i]
		if projection.Expression != nil && projection.Expression.Kind == ir.PhysicalSliceExpression &&
			projection.Expression.Slice != nil {
			projection.Expression.Slice.Source.Variable = "__loom_unregistered_set_var"
			mutated = true
		}
	}
	if !mutated {
		t.Fatal("the bundle carries no slice projection to probe")
	}
	sql16RequireRefusal(t, plan, "is out of scope")
}

// TestSQL16RelatedOccurrenceDoorExtend pins the ir :65 group-extend mirror
// (groupInputColumnsExtendSource, stage_sequence.go) for the PRODUCTION
// lowering shape the P4 leg exercises: lower.CompileResolvedRecipePlan emits
// the RelatedOccurrence GROUP with InputColumns = prior schema ++ the
// adapter-internal columns (a stage EXTENSION), and ir
// physicalGroupInputColumnsExtendSource (physical_stage_validation.go:2659)
// accepts that shape. Before the mirror was made byte-parallel the door
// implemented a name-subset law instead and refused the production plan
// ("input schema differs from preceding stage output") while ir accepted it.
// Cells drive validateStageSequenceChain directly (the door the P4 leg
// reaches); the arm must be an EXACT set, not a superset.
func TestSQL16RelatedOccurrenceDoorExtend(t *testing.T) {
	// ir-valid identity flags (physical_stage_validation.go:2632-2637):
	// the named row identities must be internal AND identity-marked —
	// _key for the source/prior schemas, occurrence for the stage output.
	prior := []ir.PhysicalStageColumn{
		{ID: "key_id", Name: "_key", Kind: "string", Cardinality: "required_one", Internal: true, Identity: true},
		{ID: "status_id", Name: "status", Kind: "string", Cardinality: "optional_one"},
	}
	adapter := &ir.PhysicalStageGroupRelatedOccurrenceAdapter{
		ScalarProjections:      []ir.PhysicalStageGroupRelatedProjection{{InputColumn: "related_status", ResultField: "status"}},
		CodingMembershipColumn: "membership",
		OccurrenceKeyColumn:    "occurrence",
	}
	extension := func() []ir.PhysicalStageColumn {
		return append(append([]ir.PhysicalStageColumn{}, prior...),
			ir.PhysicalStageColumn{ID: "related_status", Name: "related_status", Kind: "string", Cardinality: "optional_one", Internal: true},
			ir.PhysicalStageColumn{ID: "membership", Name: "membership", Kind: "object", Cardinality: "optional_one", Internal: true},
			ir.PhysicalStageColumn{ID: "occurrence", Name: "occurrence", Kind: "object", Cardinality: "required_one", Internal: true},
		)
	}
	output := []ir.PhysicalStageColumn{
		{ID: "key_id", Name: "_key", Kind: "string", Cardinality: "required_one", Internal: true, Identity: true},
		{ID: "status_id", Name: "status", Kind: "string", Cardinality: "optional_one"},
		{ID: "occurrence", Name: "occurrence", Kind: "object", Cardinality: "required_one", Internal: true, Identity: true},
	}
	chain := func(inputColumns []ir.PhysicalStageColumn, withAdapter bool) ir.PhysicalPlan {
		group := &ir.PhysicalStageGroup{ConstructionIDBindKey: "ro_cid"}
		if withAdapter {
			group.RelatedOccurrence = adapter
		}
		stage := ir.PhysicalConstructionStage{
			ID: "group_related", InputStageID: "source_projection", Kind: ir.PhysicalStageGroupOp,
			InputRowVariable: "inputrow", OutputRowVariable: "group_row",
			InputColumns: inputColumns, OutputColumns: output, RowIdentityColumn: "occurrence",
			Group: group,
		}
		return ir.PhysicalPlan{
			Operations: []ir.PhysicalOperation{{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{
				Projections: []ir.PhysicalProjection{{Name: "_key"}, {Name: "status"}},
			}}},
			StageSequence: &ir.PhysicalStageSequence{
				SourceStageID: "source_projection", SourceRowIdentity: "_key", SourceColumns: prior,
				Stages:           []ir.PhysicalConstructionStage{stage},
				FinalStageID:     "group_related",
				FinalRowIdentity: "occurrence",
				FinalColumns:     output,
			},
		}
	}
	t.Run("extend-shape-passes", func(t *testing.T) {
		if err := validateStageSequenceChain(chain(extension(), true)); err != nil {
			t.Fatalf("the production extension shape must pass the door mirror (ir :2659 twin): %v", err)
		}
	})
	t.Run("renamed-extension-refuses", func(t *testing.T) {
		columns := extension()
		columns[2].Name = "membershp"
		if err := validateStageSequenceChain(chain(columns, true)); err == nil ||
			!strings.Contains(err.Error(), "input schema differs from preceding stage output") {
			t.Fatalf("renamed adapter column must die at the door: %v", err)
		}
	})
	t.Run("fourth-extension-refuses", func(t *testing.T) {
		columns := append(extension(), ir.PhysicalStageColumn{ID: "extra", Name: "extra", Kind: "object", Cardinality: "optional_one", Internal: true})
		if err := validateStageSequenceChain(chain(columns, true)); err == nil ||
			!strings.Contains(err.Error(), "input schema differs from preceding stage output") {
			t.Fatalf("a fourth extension column proves the arm is exact-set, not superset: %v", err)
		}
	})
	t.Run("non-internal-extension-refuses", func(t *testing.T) {
		columns := extension()
		columns[3].Internal = false
		if err := validateStageSequenceChain(chain(columns, true)); err == nil ||
			!strings.Contains(err.Error(), "input schema differs from preceding stage output") {
			t.Fatalf("an adapter column that is not Internal must die at the door: %v", err)
		}
	})
	t.Run("id-drift-extension-refuses", func(t *testing.T) {
		columns := extension()
		columns[2].ID = "membership_alias"
		if err := validateStageSequenceChain(chain(columns, true)); err == nil ||
			!strings.Contains(err.Error(), "input schema differs from preceding stage output") {
			t.Fatalf("extension columns carry ID==Name (sidecar law); drift must die at the door: %v", err)
		}
	})
	t.Run("prefix-drift-refuses", func(t *testing.T) {
		columns := extension()
		columns[1].Kind = "required_one"
		if err := validateStageSequenceChain(chain(columns, true)); err == nil ||
			!strings.Contains(err.Error(), "input schema differs from preceding stage output") {
			t.Fatalf("the prior prefix must be schema-identical: %v", err)
		}
	})
	t.Run("plain-group-unknown-column-refuses", func(t *testing.T) {
		// The plain-GROUP arm is unchanged: a name outside the prior schema
		// still dies (no RelatedOccurrence extension is invented).
		columns := append(append([]ir.PhysicalStageColumn{}, prior...),
			ir.PhysicalStageColumn{ID: "ghost", Name: "ghost", Kind: "object", Cardinality: "optional_one", Internal: true})
		if err := validateStageSequenceChain(chain(columns, false)); err == nil ||
			!strings.Contains(err.Error(), "input schema differs from preceding stage output") {
			t.Fatalf("plain GROUP may not extend the prior schema: %v", err)
		}
	})
	t.Run("plain-group-reordered-refuses", func(t *testing.T) {
		// ir-wins ruling 2026-11-11: the extend rule is RelatedOccurrence-
		// only; ir :65 accepts a plain GROUP InputColumns ONLY through
		// samePhysicalStageColumns, which is positional. The removed
		// name-subset plain arm accepted this reordered name-bijection.
		reordered := []ir.PhysicalStageColumn{prior[1], prior[0]}
		if err := validateStageSequenceChain(chain(reordered, false)); err == nil ||
			!strings.Contains(err.Error(), "input schema differs from preceding stage output") {
			t.Fatalf("a reordered plain GROUP input must die at the door (ir :65 is positional): %v", err)
		}
	})
	t.Run("anchor-drift-input-refuses", func(t *testing.T) {
		// The sameStageColumns field set mirrors ir exactly: ir compares
		// RelatedRecordAnchor with reflect.DeepEqual — input drift in the
		// anchor must die at the door.
		columns := append([]ir.PhysicalStageColumn{}, prior...)
		columns[0].RelatedRecordAnchor = &ir.PhysicalStageRelatedRecordAnchor{ResourceType: "Patient"}
		if err := validateStageSequenceChain(chain(columns, false)); err == nil ||
			!strings.Contains(err.Error(), "input schema differs from preceding stage output") {
			t.Fatalf("RelatedRecordAnchor drift must die at the door (ir DeepEqual leg): %v", err)
		}
	})
	t.Run("presence-companion-drift-tolerated", func(t *testing.T) {
		// The ir-wins side of the same receipt: ir.samePhysicalStageColumns
		// does NOT compare PresenceCompanionName; the door dropped its
		// unregistered extra comparison. Pinning the tolerance so a future
		// hardening is a deliberate ir-changing decision, not door drift.
		columns := append([]ir.PhysicalStageColumn{}, prior...)
		columns[1].PresenceCompanionName = "status_present"
		if err := validateStageSequenceChain(chain(columns, false)); err != nil {
			t.Fatalf("PresenceCompanionName drift must pass (ir does not compare it): %v", err)
		}
	})
	t.Run("empty-projection-input-column-refuses", func(t *testing.T) {
		// ir :2662-2666 REJECTS an empty ScalarProjections[].InputColumn
		// outright (not skip): the extension then cannot name the column.
		broken := &ir.PhysicalStageGroupRelatedOccurrenceAdapter{
			ScalarProjections:   []ir.PhysicalStageGroupRelatedProjection{{InputColumn: "", ResultField: "status"}},
			OccurrenceKeyColumn: "occurrence",
		}
		group := &ir.PhysicalStageGroup{ConstructionIDBindKey: "ro_cid", RelatedOccurrence: broken}
		stages := chain(extension(), true).StageSequence
		stages.Stages[0].Group = group
		if err := validateStageSequenceChain(ir.PhysicalPlan{
			Operations:    chain(extension(), true).Operations,
			StageSequence: stages,
		}); err == nil || !strings.Contains(err.Error(), "input schema differs from preceding stage output") {
			t.Fatalf("empty projection InputColumn must refuse (ir rejects, not skips): %v", err)
		}
	})
	t.Run("empty-coding-names-tolerated", func(t *testing.T) {
		// ir :2670-2682 skips an empty CodingMembershipColumn/
		// OccurrenceKeyColumn as absent: a no-Coding adapter extends by
		// exactly the scalar projection set.
		noCoding := &ir.PhysicalStageGroupRelatedOccurrenceAdapter{
			ScalarProjections: []ir.PhysicalStageGroupRelatedProjection{{InputColumn: "related_status", ResultField: "status"}},
		}
		columns := append(append([]ir.PhysicalStageColumn{}, prior...),
			ir.PhysicalStageColumn{ID: "related_status", Name: "related_status", Kind: "string", Cardinality: "optional_one", Internal: true})
		group := &ir.PhysicalStageGroup{ConstructionIDBindKey: "ro_cid", RelatedOccurrence: noCoding}
		base := chain(columns, true)
		base.StageSequence.Stages[0].Group = group
		if err := validateStageSequenceChain(base); err != nil {
			t.Fatalf("empty Coding column names must be treated as absent (ir skip arms): %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// All-kinds P4 sweep (coordinator ruling 2026-11-11): every composition-
// claimed stage kind gets ONE production-lowered-through-door cell
// (recipe -> semantic.BuildRecipePlan -> semantic.ResolveRecipePlan ->
// lower.CompileResolvedRecipePlan -> door + entry, zero hand-shaping).
// The hand-parked sequence.SourceColumns blind spot produced two real
// door/ir drifts through the production channel (door extend rule, hidden
// _key arm); every other claimed kind is assumed to hide the same until
// its cell exists. If a cell reveals drift: mirror ir, RED-first, pin.
// ---------------------------------------------------------------------------

// sql16SweepOutput authors the recipe for one composition-claimed kind.
// Each shape is copied from a proven lowering-channel precedent
// (T-bundle FILTER/GROUP/EXPAND, the registered pivot cell, the lower
// unpivot cell, the construction-oracle derive cell) so the sweep tests
// the DOOR, not the authoring.
func sql16SweepOutput(t *testing.T, kind ir.PhysicalStageOperationKind) recipe.Output {
	t.Helper()
	base := func(name string, fields []recipe.Field, columns []recipe.StageColumn) recipe.Output {
		return recipe.Output{
			Name: "sql16_sweep_" + name, RootResourceType: "Observation", RowGrain: "observation",
			Fields:       fields,
			Construction: &recipe.Construction{Version: 1, SourceColumns: columns},
		}
	}
	numeric := func() ([]recipe.Field, []recipe.StageColumn) {
		return []recipe.Field{
				{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
				{Name: "score", ColumnID: "score_id", Expr: recipe.Expression{Select: "root.valueInteger"}},
				{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.valueQuantity.value"}},
			}, []recipe.StageColumn{
				{ID: "status_id", Name: "status"}, {ID: "score_id", Name: "score"}, {ID: "amount_id", Name: "amount"},
			}
	}
	switch kind {
	case ir.PhysicalStageFilterOp:
		output := base("filter", sql16SourceFields(), sql16SourceColumns())
		step := sql16FilterStep("keep_rows", recipe.ConstructionSourceProjectionInput, "", "status_id")
		step.Outputs = []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "tags_id", Name: "tags"}}
		output.Construction.Steps = []recipe.ConstructionStep{step}
		return output
	case ir.PhysicalStageGroupOp:
		// Scalar-only source fields: an array-selector source RETURN is the
		// registered composition refusal (selector extraction is SCALAR-
		// only); this cell must exercise the GROUP kind, not that refusal.
		groupFields, groupColumns := numeric()
		_ = groupFields
		output := base("group", groupFields, groupColumns)
		output.Construction.Steps = []recipe.ConstructionStep{{
			ID: "group_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
				ConstructionID: "group_status_values",
				Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "status_id", OutputColumnID: "status_key_id"}},
				Aggregates: []recipe.ConstructionGroupAggregate{
					{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"},
				},
			}},
			Outputs: []recipe.StageColumn{
				{ID: "status_key_id", Name: "status_key"}, {ID: "rows_id", Name: "rows"},
			},
		}}
		return output
	case ir.PhysicalStageExpandOp:
		output := base("expand", sql16SourceFields(), sql16SourceColumns())
		output.Construction.Steps = []recipe.ConstructionStep{{
			ID: "expand_tags", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionExpandOp, Expand: &recipe.ConstructionExpand{
				ConstructionID: "expand_tag_values", InputColumnID: "tags_id",
				OutputColumnID: "tag_id", OrdinalColumnID: "ordinal_id",
				EmptyPolicy: recipe.ExpansionPreserveParent,
			}},
			Outputs: []recipe.StageColumn{
				{ID: "status_id", Name: "status"}, {ID: "tag_id", Name: "tag"}, {ID: "ordinal_id", Name: "ordinal"},
			},
		}}
		return output
	case ir.PhysicalStageDeriveOp:
		fields, columns := numeric()
		output := base("derive", fields, columns)
		output.Construction.Steps = []recipe.ConstructionStep{{
			ID: "derive_total", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionDeriveOp, Derive: &recipe.ConstructionDerive{
				ConstructionID: "derive_total_sum", OutputColumnID: "total_id", Operation: recipe.DerivedAdd,
				Left:               recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "score_id"},
				Right:              recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "amount_id"},
				MissingInputPolicy: recipe.MissingInputPropagateNull,
			}},
			Outputs: []recipe.StageColumn{
				{ID: "status_id", Name: "status"}, {ID: "score_id", Name: "score"},
				{ID: "amount_id", Name: "amount"}, {ID: "total_id", Name: "total"},
			},
		}}
		return output
	case ir.PhysicalStageUnpivotOp:
		fields, columns := numeric()
		output := base("unpivot", fields, columns)
		keyScore, keyAmount := "score", "amount"
		output.Construction.Steps = []recipe.ConstructionStep{{
			ID: "unpivot_measures", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionUnpivotOp, Unpivot: &recipe.ConstructionUnpivot{
				ConstructionID: "unpivot_measures_values",
				Inputs: []recipe.ConstructionUnpivotInput{
					{ColumnID: "score_id", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &keyScore}},
					{ColumnID: "amount_id", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &keyAmount}},
				},
				KeyOutputColumnID: "measure_id", ValueOutputColumnID: "measure_value_id",
				NullRowPolicy: recipe.UnpivotNullDrop,
			}},
			Outputs: []recipe.StageColumn{
				{ID: "status_id", Name: "status"}, {ID: "measure_id", Name: "measure"},
				{ID: "measure_value_id", Name: "measure_value"},
			},
		}}
		return output
	case ir.PhysicalStagePivotOp:
		final := "final"
		return recipe.Output{
			Name: "sql16_sweep_pivot", RootResourceType: "Observation", RowGrain: "observation",
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
				}},
			},
		}
	}
	t.Fatalf("unregistered sweep kind %q", string(kind))
	return recipe.Output{}
}

func TestSQL16CompositionKindSweep(t *testing.T) {
	for _, kind := range []ir.PhysicalStageOperationKind{
		ir.PhysicalStageFilterOp, ir.PhysicalStageDeriveOp, ir.PhysicalStageExpandOp,
		ir.PhysicalStageUnpivotOp, ir.PhysicalStageGroupOp, ir.PhysicalStagePivotOp,
	} {
		t.Run(string(kind), func(t *testing.T) {
			plan := sql16Compile(t, sql16SweepOutput(t, kind))
			sequence := plan.StageSequence
			if sequence == nil || len(sequence.Stages) != 1 {
				t.Fatalf("the recipe must lower to one composed stage, got %#v", sequence)
			}
			if got := sequence.Stages[0].Kind; got != kind {
				t.Fatalf("production lowering emitted kind %q, want %q (the sweep cell must exercise its kind)", got, string(kind))
			}
			if kind == ir.PhysicalStageDeriveOp {
				// Registered posture, not drift: every recipe-authored
				// DERIVE lowers its arithmetic operand (ADD/SUBTRACT/
				// MULTIPLY/DIVIDE — recipe.DerivedOperation is closed to
				// CALL forms) to a CALL LET, and the composition DERIVE
				// arm refuses CALL LETs as the L-16.6 named gap (CALL/
				// selector/unit-normalization belong to calls.go,
				// selectors.go, unit_normalization.go) — never partially
				// rendered. The sweep pins the production path speaking
				// that refusal at BOTH doors (entry==dispatch, U1 law).
				sql16RequireRefusal(t, ir.ClonePhysicalPlan(plan),
					`composition DERIVE LET of expression kind "CALL" has no registered rendering`)
				return
			}
			// Door level (dispatch): the chain re-reads must accept the
			// production-lowered shape with zero hand-shaping.
			if err := validateStageSequenceChain(ir.ClonePhysicalPlan(plan)); err != nil {
				t.Fatalf("door refuses the production-lowered %s plan (door/ir drift class): %v", string(kind), err)
			}
			// Entry level: the full entry render agrees with the door.
			rendered, err := RenderPhysicalPlan(ir.ClonePhysicalPlan(plan), mustBinding(t))
			if err != nil {
				t.Fatalf("entry refuses the production-lowered %s plan the door accepted: %v", string(kind), err)
			}
			// Structural presence: the dispatch arm must actually render
			// its registered shape, not an empty acceptance. The melt
			// contract itself is pinned in unpivot_test.go (table-reshape
			// path); these fragments pin the COMPOSITION arm wiring
			// (stage relation x melt lateral x identity output), which
			// had no test reference before the sweep.
			switch kind {
			case ir.PhysicalStageUnpivotOp:
				// The melt VALUES region over the prior relation, the
				// slot triple contract, and the TABLE_UNPIVOT identity
				// witness (the registered posture: AQL's composition
				// UNPIVOT arm is renderTableUnpivot and spells the same
				// tag, render/aql/table_reshape.go:557).
				for _, want := range []string{
					"CROSS JOIN LATERAL (VALUES",
					`AS x1(slot_key, slot_value, slot_ord)`,
					`"TABLE_UNPIVOT"`,
				} {
					if !strings.Contains(rendered.SQL, want) {
						t.Fatalf("composition melt stage missing fragment %q:\n%s", want, rendered.SQL)
					}
				}
			}
		})
	}
}
