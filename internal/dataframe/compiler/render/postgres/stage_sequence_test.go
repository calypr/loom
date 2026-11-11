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
		}, "the related-occurrence Group adapter is a pg-sql-16 capability gap (L-13.5"},
		{"group-arm-coding-key", func(p *ir.PhysicalPlan) {
			p.StageSequence.Stages[1].Group.CodingKey = &ir.PhysicalStageGroupCodingKey{}
		}, "CodingKey group keys are refused at the composition payload gate"},
		{"group-arm-root-contributor", func(p *ir.PhysicalPlan) {
			p.StageSequence.Stages[1].Group.RootContributorInputColumn = "status"
		}, "the root-contributor set is production-unreachable"},
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
