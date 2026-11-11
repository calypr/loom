package postgres

// coded_pivot_test.go is the pg-sql-14 test surface for the inline
// single-stage CODED_PIVOT construction door: whole-plan door goldens
// against render/aql/table_reshape.go renderCodedPivotCategoryValues
// :338-389 and the correlation renderers of render/aql/predicates.go
// :120-207, the C3 non-coded byte-identical refusal, the C11/C12
// ignored-field goldens (Coding.version NEVER filters — ruling R2), the
// refusal register with ALIVE controls, and the live legs over the
// verify_sql14v1_* namespace: system separation, version-varied codings
// as duplicate observations, the INVALID_CHOICE_ARM position, the value
// fallback chain with the ONE trim set, and the coded reducer MISSING
// guard on the TYPED count (:267, the verbatim asymmetry against the
// scalar branch's RAW count at :230).

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	spec "github.com/calypr/loom/internal/fhir/schema"
)

// clonePlanForPivot14 deep-copies the parts of a compiled plan the coded
// door's mutations touch, so one refusal leg never leaks state into another.
func clonePlanForPivot14(plan ir.PhysicalPlan) ir.PhysicalPlan {
	clone := plan
	clone.BindVars = map[string]any{}
	for key, value := range plan.BindVars {
		clone.BindVars[key] = value
	}
	clone.Operations = append([]ir.PhysicalOperation(nil), plan.Operations...)
	if plan.StageSequence != nil {
		sequence := *plan.StageSequence
		sequence.Stages = append([]ir.PhysicalConstructionStage(nil), plan.StageSequence.Stages...)
		if sequence.Stages[0].GroupedPivot != nil {
			pivot := *sequence.Stages[0].GroupedPivot
			if pivot.CodedCorrelation != nil {
				correlation := *pivot.CodedCorrelation
				pivot.CodedCorrelation = &correlation
			}
			pivot.CodedCategories = append([]ir.PhysicalGroupedCodedPivotCategory(nil), pivot.CodedCategories...)
			pivot.GroupKeys = append([]ir.PhysicalGroupedPivotKey(nil), pivot.GroupKeys...)
			pivot.InputProjections = append([]ir.PhysicalProjection(nil), pivot.InputProjections...)
			sequence.Stages[0].GroupedPivot = &pivot
		}
		clone.StageSequence = &sequence
	}
	return clone
}

// pivot14CodedBundle authors the production CODED_PIVOT shape (probe
// P-14.3..P-14.5 attested): Observation.component[] owners,
// component[].code.coding[] keys, valueQuantity.value values.
func pivot14CodedBundle(name string, categories []recipe.ConstructionCodedPivotCategory,
	duplicate recipe.PivotDuplicatePolicy, missing recipe.PivotMissingCellPolicy,
	valuePath string, fallbacks []string, blank bool, arms []string, logical string) recipe.Bundle {
	if valuePath == "" {
		valuePath = "valueQuantity.value"
	}
	if logical == "" {
		logical = "decimal"
	}
	if len(arms) == 0 {
		arms = []string{"valueQuantity"}
	}
	if len(categories) == 0 {
		categories = []recipe.ConstructionCodedPivotCategory{
			{System: "http://loinc.org", Code: "8480-6", OutputColumnID: "systolic_id"},
			{System: "http://loinc.org", Code: "8462-4", OutputColumnID: "diastolic_id"},
		}
	}
	outputs := make([]recipe.StageColumn, 0, len(categories))
	for _, category := range categories {
		outputs = append(outputs, recipe.StageColumn{
			ID: category.OutputColumnID, Name: strings.TrimSuffix(category.OutputColumnID, "_id"),
			Type: logical, Nullable: true})
	}
	source := "Observation.component[]." + valuePath
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: name, TranslationVersion: "test",
		Outputs: []recipe.Output{{
			Name: name, RootResourceType: "Observation", RowGrain: "resource",
			Fields: []recipe.Field{{Name: "resource_id", ColumnID: "resource_id", Expr: recipe.Expression{Select: "root.id"}}},
			Construction: &recipe.Construction{
				Version:       1,
				SourceColumns: []recipe.StageColumn{{ID: "resource_id", Name: "resource_id"}},
				Steps: []recipe.ConstructionStep{{
					ID: name, Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{
						Kind: recipe.ConstructionCodedPivotOp,
						CodedPivot: &recipe.ConstructionCodedPivot{
							ConstructionID: name,
							Source: recipe.ConstructionCodedPivotSource{
								BindingID: name + "-binding", ResourceType: "Observation",
								SourcePath: source, SourceCanonical: "http://loinc.org",
								OwningScope: "component[]", KeyPath: "component[].code.coding[]", ValuePath: valuePath,
								ValueFallbacks: fallbacks, FallbackOnBlankString: blank,
								ChoiceArms: arms, LogicalType: logical,
								RuleVersion: "semantic-rule-v1", SchemaVersion: 1,
								CandidateID: name + "-candidate", NodeID: name + "-node", FieldPath: source,
								Route: []recipe.ConstructionRelatedRouteStep{},
							},
							Categories:        categories,
							DuplicatePolicy:   duplicate,
							MissingCellPolicy: missing,
						},
					},
					Outputs: outputs,
				}},
			},
		}},
	}
}

func pivot14CompileCodedBundle(t *testing.T, name string) ir.PhysicalPlan {
	t.Helper()
	return pivot14CompileBundle(t, pivot14CodedBundle(name, nil,
		recipe.PivotDuplicateSum, recipe.PivotMissingCellNull, "", nil, false, nil, ""),
		[]string{"FHIR/Observation"})
}

func pivot14CodedPivot(t *testing.T, plan ir.PhysicalPlan) *ir.PhysicalGroupedPivot {
	t.Helper()
	if plan.StageSequence == nil || len(plan.StageSequence.Stages) != 1 ||
		plan.StageSequence.Stages[0].Kind != ir.PhysicalStagePivotOp || plan.StageSequence.Stages[0].GroupedPivot == nil {
		t.Fatalf("want one inline PIVOT stage, got %+v", plan.StageSequence)
	}
	return plan.StageSequence.Stages[0].GroupedPivot
}

func pivot14CodedRefused(t *testing.T, plan ir.PhysicalPlan, label string, wantFragment string) {
	t.Helper()
	_, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err == nil {
		t.Fatalf("%s: expected typed refusal, got success", label)
	}
	if !errors.Is(err, ErrUnsupportedPostgresPlan) {
		t.Fatalf("%s: refusal not ErrUnsupportedPostgresPlan: %v", label, err)
	}
	if wantFragment != "" && !strings.Contains(err.Error(), wantFragment) {
		t.Fatalf("%s: refusal %q lacks %q", label, err.Error(), wantFragment)
	}
}

// ---------------------------------------------------------------------------
// Door goldens
// ---------------------------------------------------------------------------

func TestCodedPivot14ProductionRenders(t *testing.T) {
	plan := pivot14CompileCodedBundle(t, "p14_coded_render")
	rendered, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("production coded pivot must render through the registered door: %v", err)
	}
	sql := rendered.SQL
	for _, want := range []string{
		"WITH cp_src AS (SELECT",
		"cp_owner AS (SELECT s.cp_key,",
		"cp_cat_0 AS (",
		"cp_elem_0 AS (",
		"cp_agg_0 AS (",
		// The (system, code) match on ONE Coding item
		// (renderCodedPivotCategoryValues :360-:373): both keys are
		// jsonb strings, non-empty EXACT (untrimmed — only the
		// blank-string value fallback trims, ruling R2), equal to the
		// bound pair.
		"EXISTS (SELECT 1 FROM",
		"jsonb_typeof(",
		") = 'string'",
		"::text <> ''",
		"LEFT JOIN cp_agg_0 AS a0 ON a0.cp_key = s.cp_key",
		// The INVALID_CHOICE_ARM assert on MATCHED owners only (the
		// per-category match keeps badness as DATA — cp_bad is the
		// BOOL_OR over matched rows only, WHERE t.cp_m precedes the
		// aggregation) and raises it in the single ordered outer chain
		// (the AQL lazy category LETs force the owner-loop ASSERT with
		// the category's values, ahead of that category's cell guards:
		// table_reshape.go:384).
		"WHERE t.cp_m)",
		"BOOL_OR(cp_bad) AS cp_bad",
		"WHEN COALESCE(a0.cp_bad, false) THEN CAST('INVALID_CHOICE_ARM'",
		// FIRST-over-candidates chain, ordinal-pinned to declared order
		// (predicates.go:177), then the one-level FLATTEN of the chosen
		// candidate (table_reshape.go:385) in document order.
		"jsonb_agg(s.cp_e ORDER BY f.ord, s.ord1)",
		"ELSE jsonb_build_array(f.x) END) WITH ORDINALITY AS s(cp_e, ord1)",
		"(ARRAY_AGG(e.cp_e ORDER BY e.ord))[1] AS cp_first",
		// Terminal identity order (stage_sequence.go:399-448 parity).
		`ORDER BY '[' || '"GROUPED_PIVOT"'`,
		`COLLATE "C" ASC`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("coded statement lost %q:\n%s", want, sql)
		}
	}
	// The ordinal-pinned VALUES pick only exists on the multi-candidate
	// path (a hand-carried fallback makes candidates > 1; the recipe
	// layer's DECIMAL veto is irrelevant here because we hand-carry), so
	// its text receipt lives with a fallback-carrying plan.
	multi := pivot14CompileCodedBundle(t, "p14_coded_multi_candidate")
	multiCodedPivot := pivot14CodedPivot(t, multi)
	multiCodedPivot.CodedCorrelation.ValueFallbacks = []spec.Selector{specSelector(t, "valueString")}
	multiRendered, err := RenderPhysicalPlan(multi, mustBinding(t))
	if err != nil {
		t.Fatalf("render multi-candidate plan: %v", err)
	}
	for _, want := range []string{
		"AS c(ord, cand) WHERE ",
		"ORDER BY c.ord LIMIT 1",
	} {
		if !strings.Contains(multiRendered.SQL, want) {
			t.Fatalf("multi-candidate pick lost %q:\n%s", want, multiRendered.SQL)
		}
	}

	// Category pairs ride $n (the AQL @system/@code binds, :367/:370).
	for _, forbidden := range []string{"8480-6", "8462-4", "http://loinc.org", "proj-a", "gen-1", "FHIR/Observation"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("coded plan data leaked into SQL text (%q):\n%s", forbidden, sql)
		}
	}
	carried := 0
	for _, parameter := range rendered.Args {
		var text string
		switch value := parameter.Value.(type) {
		case string:
			text = value
		case []byte:
			text = string(value)
		}
		if strings.Contains(text, "8480-6") || strings.Contains(text, "8462-4") || strings.Contains(text, "loinc") {
			carried++
		}
	}
	if carried < 3 {
		t.Fatalf("system/code pairs must ride parameters, carried=%d\n%v", carried, rendered.Args)
	}
}

func TestCodedPivot14GuardOrderAndMissingBranches(t *testing.T) {
	// The reducer branch conditions MISSING on the TYPED count — the
	// verbatim asymmetry against the ordinary (scalar) branch's RAW count
	// (table_reshape.go:267 vs :230). Diff receipt asserted here: the
	// missing guard reads cp_tcnt, while the scalar ERROR branch reads the
	// raw COUNT(*) FILTER (TestPivot14GoldenErrorBranch). The ALIVE
	// controls are the paired live legs:
	// TestCodedPivot14LiveReducerMissingTypedCount (raises here) vs
	// TestPivot14LiveScalarMissingRawCountNoRaise (must NOT raise there).
	plan := pivot14CompileCodedBundle(t, "p14_coded_missing_guard")
	plan.StageSequence.Stages[0].GroupedPivot.MissingCellPolicy = "ERROR"
	plan.StageSequence.Stages[0].GroupedPivot.DuplicatePolicy = "SUM"
	rendered, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	sql := rendered.SQL
	sumErrorChainSQL := sql
	if !strings.Contains(sql, "COALESCE(a0.cp_tcnt, 0) = 0 THEN CAST('TABLE_PIVOT_CELL_MISSING'") {
		t.Fatalf("coded reducer MISSING guard must read the TYPED count (:267):\n%s", sql)
	}
	if strings.Contains(sql, "COALESCE(a0.cp_cnt, 0) = 0 THEN CAST('TABLE_PIVOT_CELL_MISSING'") {
		t.Fatalf("coded reducer branch must NOT read the raw count for MISSING (:267 vs :230):\n%s", sql)
	}

	// The ERROR branch guards on the RAW count as well
	// (table_reshape.go:252-258): cardinality cp_cnt > 1.
	errPlan := pivot14CompileCodedBundle(t, "p14_coded_error_branch")
	errPlan.StageSequence.Stages[0].GroupedPivot.DuplicatePolicy = "ERROR"
	rendered, err = RenderPhysicalPlan(errPlan, mustBinding(t))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	sql = rendered.SQL
	if !strings.Contains(sql, "COALESCE(a0.cp_cnt, 0) > 1 THEN CAST('TABLE_PIVOT_CELL_CARDINALITY'") {
		t.Fatalf("coded ERROR branch cardinality guard must read the RAW count:\n%s", sql)
	}
	if strings.Contains(sql, "ELSE to_jsonb(a0.cp_sum)") {
		t.Fatalf("coded ERROR branch must not enumerate typed reducers:\n%s", sql)
	}

	// The choice-arm guard is DATA-qualified by the match (cp_bad is the
	// BOOL_OR over matched rows only — WHERE t.cp_m precedes it, so an
	// UNMATCHED owner carrying an unsupported arm can never raise it,
	// predicates.go :185-207 at the :377 position) and raised ONLY in
	// the single ordered outer chain, never inside the per-category CTE
	// where CTE materialization would outrank the AQL lazy-LET order.
	armRendered, err := RenderPhysicalPlan(pivot14CompileCodedBundle(t, "p14_coded_arm_guard"), mustBinding(t))
	if err != nil {
		t.Fatalf("render arm guard plan: %v", err)
	}
	if strings.Contains(armRendered.SQL, "CASE WHEN t.cp_m AND t.cp_bad THEN CAST('INVALID_CHOICE_ARM'") {
		t.Fatalf("choice-arm raise must not live in the per-category CTE (unstable cross-CTE order):\n%s", armRendered.SQL)
	}
	if !strings.Contains(armRendered.SQL, "BOOL_OR(cp_bad) AS cp_bad") ||
		!strings.Contains(armRendered.SQL, "WHEN COALESCE(a0.cp_bad, false) THEN CAST('INVALID_CHOICE_ARM'") ||
		!strings.Contains(armRendered.SQL, "WHEN COALESCE(a1.cp_bad, false) THEN CAST('INVALID_CHOICE_ARM'") {
		t.Fatalf("choice-arm guard must ride the outer chain per category:\n%s", armRendered.SQL)
	}
	// Chain order receipts on the SUM+ERROR plan (sql): category order and
	// within-category order — choice-arm, then that category's missing,
	// then that category's value-type — mirror the AQL projection forcing
	// each category's owner-loop ASSERT with its values LET.
	type term struct {
		frag  string
		label string
	}
	chain := []term{
		{"WHEN COALESCE(a0.cp_bad, false) THEN CAST('INVALID_CHOICE_ARM'", "cat0 choice-arm"},
		{"WHEN COALESCE(a0.cp_tcnt, 0) = 0 THEN CAST('TABLE_PIVOT_CELL_MISSING'", "cat0 missing"},
		{"a0.cp_mm, false) THEN CAST('TABLE_PIVOT_VALUE_TYPE_MISMATCH'", "cat0 value-type"},
		{"WHEN COALESCE(a1.cp_bad, false) THEN CAST('INVALID_CHOICE_ARM'", "cat1 choice-arm"},
		{"WHEN COALESCE(a1.cp_tcnt, 0) = 0 THEN CAST('TABLE_PIVOT_CELL_MISSING'", "cat1 missing"},
		{"a1.cp_mm, false) THEN CAST('TABLE_PIVOT_VALUE_TYPE_MISMATCH'", "cat1 value-type"},
	}
	previous := -1
	for _, entry := range chain {
		at := strings.Index(sumErrorChainSQL, entry.frag)
		if at < 0 {
			t.Fatalf("guard chain lost %s:\n%s", entry.label, sumErrorChainSQL)
		}
		if at < previous {
			t.Fatalf("guard chain order drifted at %s (AQL lazy-LET order):\n%s", entry.label, sumErrorChainSQL)
		}
		previous = at
	}
}

func TestCodedPivot14IgnoredFieldsByteIdentical(t *testing.T) {
	// C11/C12 (ruling R2): Coding.version NEVER filters; UnitSelector,
	// ValueScope, SelectionScope, VersionSelector are ignored by the
	// ground truth (renderCodedPivotCategoryValues reads values from the
	// owner regardless, table_reshape.go:360/:364). Goldens pin the
	// rendering byte-identical with and without them.
	base := pivot14CompileCodedBundle(t, "p14_coded_ignored")
	rendered, err := RenderPhysicalPlan(base, mustBinding(t))
	if err != nil {
		t.Fatalf("baseline render: %v", err)
	}
	type variant struct {
		name  string
		drift func(*ir.PhysicalCorrelation, map[string]any)
	}
	variants := []variant{
		{"value-scope", func(c *ir.PhysicalCorrelation, _ map[string]any) { c.ValueScope = fhirschema.CorrelatedValueScopeOwner }},
		{"selection-scope", func(c *ir.PhysicalCorrelation, _ map[string]any) {
			c.SelectionScope = fhirschema.CorrelatedSelectionScopeExact
		}},
		{"unit-selector", func(c *ir.PhysicalCorrelation, _ map[string]any) {
			c.UnitSelector = &spec.Selector{Steps: []spec.SelectorStep{{Field: "valueQuantity"}, {Field: "unit"}}}
		}},
		// ValueScope CODING and a hand-carried VersionSelector are beyond
		// even the ir boundary for a pivot correlation carrying owner
		// choice arms (physical_validation_refs.go :392) — the
		// unreachable-state refusals are pinned below instead.
	}
	for _, test := range variants {
		clone := clonePlanForPivot14(base)
		test.drift(clone.StageSequence.Stages[0].GroupedPivot.CodedCorrelation, clone.BindVars)
		renderedVariant, err := RenderPhysicalPlan(clone, mustBinding(t))
		if err != nil {
			t.Fatalf("variant %s: ir or renderer rejected the ignored field: %v", test.name, err)
		}
		if renderedVariant.SQL != rendered.SQL {
			t.Fatalf("ignored field %s changed the rendering (R2 violation — the ground truth must read byte-identically):\nbase: %s\nvariant: %s", test.name, rendered.SQL, renderedVariant.SQL)
		}
	}

	// A hand-carried version selector is refused at the ir boundary for a
	// pivot carrying owner choice arms (physical_validation_refs.go
	// :289/:392): the state is unreachable even before the renderer's own
	// ignore-everything stance (ruling R2) can be exercised — pinned so a
	// future ir relaxation is noticed here.
	carrier := clonePlanForPivot14(base)
	carrier.StageSequence.Stages[0].GroupedPivot.CodedCorrelation.VersionSelector =
		&spec.Selector{Steps: []spec.SelectorStep{{Field: "code"}, {Field: "coding"}, {Field: "version"}}}
	carrier.StageSequence.Stages[0].GroupedPivot.CodedCorrelation.VersionBindKey = "p14_version_bind"
	carrier.BindVars["p14_version_bind"] = "3"
	if _, err := RenderPhysicalPlan(carrier, mustBinding(t)); err == nil ||
		!strings.Contains(err.Error(), "version") {
		t.Fatalf("ir must refuse the unreachable hand-carried version selector state: %v", err)
	}
	codingScope := clonePlanForPivot14(base)
	codingScope.StageSequence.Stages[0].GroupedPivot.CodedCorrelation.ValueScope = fhirschema.CorrelatedValueScopeCoding
	if _, err := RenderPhysicalPlan(codingScope, mustBinding(t)); err == nil ||
		!strings.Contains(err.Error(), "CODING correlation value scope does not support owner choice arms") {
		t.Fatalf("ir must refuse CODING value scope on the pivot's owner choice arms: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Refusal register — every refusal with an ALIVE control
// ---------------------------------------------------------------------------

func TestCodedPivot14RefusalRegister(t *testing.T) {
	cases := []struct {
		name         string
		mutate       func(*ir.PhysicalPlan)
		wantFragment string
	}{
		{"OneInputRowPerGroup false", func(p *ir.PhysicalPlan) {
			pivot14CodedPivot(t, *p).OneInputRowPerGroup = false
		}, "OneInputRowPerGroup"},
		{"non-IGNORE unlisted policy", func(p *ir.PhysicalPlan) {
			pivot14CodedPivot(t, *p).UnlistedCategoryPolicy = "ERROR"
		}, "IGNORE"},
		{"row values (pg-sql-13 divergence)", func(p *ir.PhysicalPlan) {
			pivot14CodedPivot(t, *p).RowValues = []ir.PhysicalStageRowValue{{InputColumn: "resource_id", Output: "rv", Variable: "rv0", Policy: "ALL", InputKind: "STRING"}}
		}, "pg-sql-13"},
		{"extension URL selectors (C10)", func(p *ir.PhysicalPlan) {
			pivot14CodedPivot(t, *p).CodedCorrelation.ExtensionURLSelectors = []spec.Selector{{Steps: []spec.SelectorStep{{Field: "url"}}}}
		}, "extension-URL"},
		// The (system, code) match renders ONE property test on the
		// coding item; the AQL oracle would evaluate the whole selector
		// chain (renderCorrelationScalar, predicates.go:151-157), and ir
		// admits these forms for EXACT scope
		// (physical_validation_refs.go:420-425), so the door refuses
		// everything outside the single plain step instead of silently
		// matching nothing. ALIVE control: the goldens render the
		// single-step pair (TestCodedPivot14ProductionRenders).
		{"multi-step system selector", func(p *ir.PhysicalPlan) {
			correlation := pivot14CodedPivot(t, *p).CodedCorrelation
			correlation.SystemSelector.Steps = append([]spec.SelectorStep(nil), correlation.SystemSelector.Steps...)
			correlation.SystemSelector.Steps = append(correlation.SystemSelector.Steps, spec.SelectorStep{Field: "extension"})
		}, "system selector must be one plain property step"},
		{"indexed code selector", func(p *ir.PhysicalPlan) {
			correlation := pivot14CodedPivot(t, *p).CodedCorrelation
			zero := 0
			correlation.CodeSelector.Steps = append([]spec.SelectorStep(nil), correlation.CodeSelector.Steps...)
			correlation.CodeSelector.Steps[0].Index = &zero
		}, "code selector must be one plain property step"},
		{"CONTAINS-filtered system selector", func(p *ir.PhysicalPlan) {
			correlation := pivot14CodedPivot(t, *p).CodedCorrelation
			correlation.SystemSelector.Filter = &spec.ContainsFilter{Field: "system", Needle: "loinc"}
		}, "system selector must be one plain property step"},
		{"duplicate system/code pair", func(p *ir.PhysicalPlan) {
			categories := pivot14CodedPivot(t, *p).CodedCategories
			categories[1].CodeBindKey = categories[0].CodeBindKey
		}, "duplicates a bound system/code pair"},
		{"blank category bind", func(p *ir.PhysicalPlan) {
			categories := pivot14CodedPivot(t, *p).CodedCategories
			p.BindVars[categories[0].CodeBindKey] = "  "
		}, "exact non-empty string"},
		{"group key not the hidden root _key", func(p *ir.PhysicalPlan) {
			pivot14CodedPivot(t, *p).GroupKeys[0].Kind = "INTEGER"
		}, "hidden root _key"},
		{"input projection pair drift", func(p *ir.PhysicalPlan) {
			pivot := pivot14CodedPivot(t, *p)
			pivot.InputProjections = pivot.InputProjections[:1]
		}, "only the direct root key and payload"},
		{"correlation source drift", func(p *ir.PhysicalPlan) {
			pivot14CodedPivot(t, *p).CodedCorrelation.Source.Path = []string{"other"}
		}, "staged payload"},
		{"terminal Pivot window", func(p *ir.PhysicalPlan) {
			p.StageSequence.PreviewTerminalPivotWindow = true
		}, "terminal Pivot window"},
		{"plan-level preview window", func(p *ir.PhysicalPlan) {
			p.PreviewSourceWindowByRootID = true
		}, "plan-level preview source window"},
		{"active root-id window plus limit (R3)", func(p *ir.PhysicalPlan) {
			p.StageSequence.PreviewSourceWindowByRootID = true
			p.StageSequence.PreviewLimitBindKey = "p14_preview_limit"
			p.BindVars["p14_preview_limit"] = 10
		}, "refused, never silently rendered"},
		{"non-positive preview limit", func(p *ir.PhysicalPlan) {
			p.StageSequence.PreviewSourceWindowByRootID = false
			p.StageSequence.PreviewLimitBindKey = "p14_preview_limit"
			p.BindVars["p14_preview_limit"] = 0
		}, "positive int"},
		{"stage carries a second payload", func(p *ir.PhysicalPlan) {
			p.StageSequence.Stages[0].Group = &ir.PhysicalStageGroup{}
		}, "second payload"},
		{"stage not final", func(p *ir.PhysicalPlan) {
			p.StageSequence.FinalStageID = "someone-else"
		}, "must be final"},
		{"cell-trace return", func(p *ir.PhysicalPlan) {
			p.StageSequence.CellTraceReturn = &ir.PhysicalCellTraceReturn{}
		}, "pg-sql-23 through pg-sql-26"},
		{"source-region SET refused (C6)", func(p *ir.PhysicalPlan) {
			p.Operations = append(p.Operations[:1],
				ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Set: &ir.PhysicalSet{Variable: "s"}},
				p.Operations[1])
		}, "SET is a pg-sql-16 capability gap"},
		{"source-region EXPRESSION_LET refused (C6)", func(p *ir.PhysicalPlan) {
			p.Operations = append(p.Operations[:1],
				ir.PhysicalOperation{Kind: ir.PhysicalExpressionLetOp, ExpressionLet: &ir.PhysicalExpressionLet{Variable: "e"}},
				p.Operations[1])
		}, "EXPRESSION_LET is a pg-sql-16 capability gap"},
		{"coexisting traversal refused", func(p *ir.PhysicalPlan) {
			p.Operations = append(p.Operations[:1],
				ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{
					SourceVariable: "root", TargetVariable: "t", Direction: ir.PhysicalOutbound, EdgeLabelBindKey: "edge"}},
				p.Operations[1])
		}, "not part of the registered coded-pivot source surface"},
	}
	for index, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// ALIVE control: the unmutated production coded plan renders.
			control := pivot14CompileCodedBundle(t, fmt.Sprintf("p14_coded_control_%02d", index))
			if _, err := RenderPhysicalPlan(control, mustBinding(t)); err != nil {
				t.Fatalf("ALIVE control refused before any mutation: %v", err)
			}
			plan := pivot14CompileCodedBundle(t, fmt.Sprintf("p14_coded_refused_%02d", index))
			test.mutate(&plan)
			pivot14CodedRefused(t, plan, test.name, test.wantFragment)
		})
	}
}

// ---------------------------------------------------------------------------
// Live legs
// ---------------------------------------------------------------------------

func pivot14SeedObservation(t *testing.T, svc *pgir02Service, logicalID string, payload any) {
	t.Helper()
	svc.insertResource(t, svc.schema, pivot14Project, pivot14Generation, "Observation", logicalID, "FHIR/Observation", payload)
}

func pivot14Coding(system, code, version string) map[string]any {
	coding := map[string]any{"system": system, "code": code}
	if version != "" {
		coding["version"] = version
	}
	return coding
}

func pivot14Component(codings []map[string]any, value any, valueString *string) map[string]any {
	component := map[string]any{
		"code": map[string]any{"coding": codings},
	}
	if value != nil {
		component["valueQuantity"] = map[string]any{"value": value}
	}
	if valueString != nil {
		component["valueString"] = *valueString
	}
	return component
}

func TestCodedPivot14LiveSystemSeparationAndVersionDuplication(t *testing.T) {
	svc := pivot14LiveService(t)
	// r1: matched loinc/8480-6 owner, value 120.
	// r2: SAME code, different system (snomed) — must NOT match.
	// r3: two matched owners differing only in Coding.version — ruling R2:
	//     version NEVER filters, so these are DUPLICATE observations
	//     (SUM = 10+5 = 15), never a silent drop.
	// r4: hostile category pair with quotes/commas/brackets/JSON
	//     fragments, matched exactly.
	hostileCode := `8462-4",["EVIL",1]`
	pivot14SeedObservation(t, svc, "r1", map[string]any{
		"component": []map[string]any{pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8480-6", "")}, 120, nil)},
	})
	pivot14SeedObservation(t, svc, "r2", map[string]any{
		"component": []map[string]any{pivot14Component([]map[string]any{pivot14Coding("http://snomed.info/sct", "8480-6", "")}, 999, nil)},
	})
	pivot14SeedObservation(t, svc, "r3", map[string]any{
		"component": []map[string]any{
			pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8480-6", "1")}, 10, nil),
			pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8480-6", "2")}, 5, nil),
		},
	})
	pivot14SeedObservation(t, svc, "r4", map[string]any{
		"component": []map[string]any{pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", hostileCode, "")}, 7, nil)},
	})
	// Non-vacuity: the discriminating payloads are really stored.
	rows := svc.witness(t, `SELECT logical_id FROM "`+svc.schema+`"."fhir_resource`+
		`" WHERE project = $1 AND dataset_generation = $2 AND logical_id = 'r3'`+
		` AND payload @> '{"component":[{"code":{"coding":[{"system":"http://loinc.org","code":"8480-6","version":"1"}]},"valueQuantity":{"value":10}}]}'::jsonb`+
		` AND payload @> '{"component":[{"code":{"coding":[{"system":"http://loinc.org","code":"8480-6","version":"2"}]},"valueQuantity":{"value":5}}]}'::jsonb`,
		pivot14Project, pivot14Generation)
	if len(rows) != 1 {
		t.Fatalf("version-varied fixture is vacuous (both version-bearing components must be stored): %v", rows)
	}

	plan := pivot14CompileBundle(t, pivot14CodedBundle("p14_live_coded", []recipe.ConstructionCodedPivotCategory{
		{System: "http://loinc.org", Code: "8480-6", OutputColumnID: "systolic_id"},
		{System: "http://loinc.org", Code: hostileCode, OutputColumnID: "hostile_id"},
	}, recipe.PivotDuplicateSum, recipe.PivotMissingCellNull, "", nil, false, nil, ""), []string{"FHIR/Observation"})
	rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render live coded plan: %v", err)
	}
	out := map[string][]string{}
	for _, row := range svc.runRendered(t, rendered) {
		out[row[0]] = row // c0 is the hidden root _key (logical id)
	}
	systolic, hostile := -1, -1
	for index, name := range rendered.OutputColumns {
		switch name {
		case "systolic":
			systolic = index
		case "hostile":
			hostile = index
		}
	}
	if systolic < 0 || hostile < 0 {
		t.Fatalf("category columns missing: %v", rendered.OutputColumns)
	}
	// System separation: the snomed-coded row contributes nothing.
	if got := out["r2"][systolic]; got != "<null>" && got != "null" {
		t.Fatalf("same code across systems must not match; r2 systolic = %q", got)
	}
	// Version-varied codings are duplicate observations (R2): SUM 10+5.
	if got := out["r3"][systolic]; got != "15" {
		t.Fatalf("version-varied codings must be duplicate observations (SUM 15), got %q — silent version filtering would return 10 or 5", got)
	}
	// Hostile pair matched byte-exactly through $n.
	if got := out["r4"][hostile]; got != "7" {
		t.Fatalf("hostile category pair must match byte-exactly, got %q", got)
	}
	if got := out["r1"][systolic]; got != "120" {
		t.Fatalf("r1 systolic = %q, want 120", got)
	}

	// Identity is the JSON-compact AQL composition riding parameters: the
	// hostile pair never reaches SQL text and the identity composes from
	// to_jsonb of the bound texts.
	for _, forbidden := range []string{hostileCode, "EVIL"} {
		if strings.Contains(rendered.SQL, forbidden) {
			t.Fatalf("hostile category pair leaked into SQL text:\n%s", rendered.SQL)
		}
	}
}

func TestCodedPivot14LiveChoiceArmMatchedOnly(t *testing.T) {
	svc := pivot14LiveService(t)
	// ALIVE control first: an UNMATCHED owner carrying an unsupported
	// choice arm must not raise (the AQL ASSERT runs after the match
	// filter — predicates.go:185-207 at the :377 position).
	pivot14SeedObservation(t, svc, "c1", map[string]any{
		"component": []map[string]any{
			pivot14Component([]map[string]any{pivot14Coding("http://snomed.info/sct", "8480-6", "")}, nil, strPtr("bad-arm-unmatched")),
		},
	})
	plan := pivot14CompileBundle(t, pivot14CodedBundle("p14_live_arm_control", nil,
		recipe.PivotDuplicateSum, recipe.PivotMissingCellNull, "valueQuantity.value", nil, false, []string{"valueQuantity"}, "decimal"),
		[]string{"FHIR/Observation"})
	rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render control: %v", err)
	}
	if rows := svc.runRendered(t, rendered); len(rows) == 0 {
		t.Fatal("control root must survive")
	}

	// The matched owner with the unsupported arm raises
	// INVALID_CHOICE_ARM through the guard barrier — EXPLAIN completes
	// under BOTH plan_cache modes (plan-time safety) and execution raises
	// 22P02 naming the tag (the calls_live pattern).
	pivot14SeedObservation(t, svc, "c2", map[string]any{
		"component": []map[string]any{
			pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8480-6", "")}, nil, strPtr("bad-arm-matched")),
		},
	})
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		conn, err := svc.iso.Acquire(svc.ctx)
		if err != nil {
			svc.fail("acquire pinned connection", err)
		}
		if _, err := conn.Exec(svc.ctx, "SET plan_cache_mode = "+mode); err != nil {
			conn.Release()
			svc.fail("pin plan_cache_mode "+mode, err)
		}
		if _, err := conn.Exec(svc.ctx, "EXPLAIN "+rendered.SQL, pivot14Args(rendered)...); err != nil {
			conn.Release()
			svc.fail("plan-time EXPLAIN raised under "+mode, err)
		}
		_, err = conn.Exec(svc.ctx, rendered.SQL, pivot14Args(rendered)...)
		conn.Release()
		if err == nil {
			t.Fatalf("matched owner with an unsupported choice arm must raise under %s", mode)
		}
		if !strings.Contains(err.Error(), "INVALID_CHOICE_ARM") || !strings.Contains(err.Error(), "22P02") {
			t.Fatalf("choice-arm raise drifted under %s: %v", mode, err)
		}
	}
}

func TestCodedPivot14LiveValueFallbackAndTrim(t *testing.T) {
	svc := pivot14LiveService(t)
	// Fallback chain (renderCorrelationValues :159-175): the primary
	// valueQuantity.value is absent, so the valueString fallback is
	// chosen when it is non-blank; an all-blank fallback (the ONE trim
	// set) is ineligible and the cell reads null WITHOUT the value-type
	// guard.
	primary := pivot14CodedBundle("p14_live_fallback", []recipe.ConstructionCodedPivotCategory{
		{System: "http://loinc.org", Code: "8480-6", OutputColumnID: "systolic_id"},
	}, recipe.PivotDuplicateSum, recipe.PivotMissingCellNull,
		"valueQuantity.value", nil, false, []string{"valueQuantity"}, "decimal")
	plan := pivot14CompileBundle(t, primary, []string{"FHIR/Observation"})
	// The DECIMAL + valueString-arm + FallbackOnBlankString state is what
	// AQL renders, but the authored layers refuse it three times over
	// (construction_reshape.go:171 wants string values for blank-string
	// fallback; lower rejects a valueString fallback and mixed choice
	// arms under decimal),
	// so it rides hand-carried IR; ir itself places no such restriction
	// on the correlation.
	correlation := pivot14CodedPivot(t, plan).CodedCorrelation
	correlation.ValueFallbacks = []spec.Selector{specSelector(t, "valueString")}
	correlation.ChoiceArms = []string{"valueQuantity", "valueString"}
	correlation.FallbackOnBlankString = true
	systolicIndex := -1
	probeRendered, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("probe render: %v", err)
	}
	for index, name := range probeRendered.OutputColumns {
		if name == "systolic" {
			systolicIndex = index
		}
	}
	if systolicIndex < 0 {
		t.Fatalf("systolic column missing: %v", probeRendered.OutputColumns)
	}

	// f1: matched owner with no candidates at all → cnt 0, null cell.
	pivot14SeedObservation(t, svc, "f1", map[string]any{
		"component": []map[string]any{
			pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8480-6", "")}, nil, nil),
		},
	})
	rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render fallback plan: %v", err)
	}
	rows := svc.runRendered(t, rendered)
	if len(rows) != 1 {
		t.Fatalf("fallback rows = %v", rows)
	}
	if got := rows[0][systolicIndex]; got != "<null>" && got != "null" {
		t.Fatalf("owner with no candidates must read the null cell, got %q", got)
	}

	// f2: non-blank numeric STRING fallback is chosen (primary empty), its
	// item is a STRING under the DECIMAL witness → the typed enumeration
	// rejects it and the value-type guard raises 22P02 naming the tag.
	pivot14SeedObservation(t, svc, "f2", map[string]any{
		"component": []map[string]any{
			pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8480-6", "")}, nil, strPtr("12.5")),
		},
	})
	_, err = svc.iso.Exec(svc.ctx, rendered.SQL, pivot14Args(rendered)...)
	if err == nil {
		t.Fatal("string value under DECIMAL must raise the value-type guard")
	}
	if !strings.Contains(err.Error(), "TABLE_PIVOT_VALUE_TYPE_MISMATCH") {
		t.Fatalf("value-type raise drifted: %v", err)
	}

	// All-blank fallback under FallbackOnBlankString: the candidate never
	// enters the typed enumeration — no raise, null cell. The trim set
	// reaches SQL only as the ONE bound parameter (structural law; the
	// btrim render lives in the gate-file helper
	// codedGroupTrimmedNonBlank, never inline in pivot.go).
	blankSvc := pivot14LiveService(t)
	pivot14SeedObservation(t, blankSvc, "b1", map[string]any{
		"component": []map[string]any{
			pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8480-6", "")}, nil, strPtr("   ")),
		},
	})
	blankRendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, blankSvc))
	if err != nil {
		t.Fatalf("render blank plan: %v", err)
	}
	blankRows := blankSvc.runRendered(t, blankRendered)
	if len(blankRows) != 1 || (blankRows[0][systolicIndex] != "<null>" && blankRows[0][systolicIndex] != "null") {
		t.Fatalf("all-blank fallback must be ineligible and read null, got %v", blankRows)
	}
	// The trim predicate renders (through the gate-file helper) with the
	// ONE trim set as a bound parameter — never an inlined literal.
	if !strings.Contains(blankRendered.SQL, "btrim(") ||
		strings.Contains(blankRendered.SQL, "btrim(b.x #>> '{}', ' '") {
		t.Fatalf("the trim must render with the ONE set as a bound parameter:\n%s", blankRendered.SQL)
	}
	carriedTrim := false
	for _, parameter := range blankRendered.Args {
		if text, ok := parameter.Value.(string); ok && text == " " {
			carriedTrim = true
		}
	}
	if !carriedTrim {
		t.Fatalf("the ONE trim set must ride as a bound parameter:\n%v", blankRendered.Args)
	}
}

func TestCodedPivot14LiveReducerMissingTypedCount(t *testing.T) {
	// Coded-branch MISSING raise on reachable data plus the NULL sibling.
	// The §7 raw-vs-typed ALIVE asymmetry receipt for THIS branch is the
	// textual guard in TestCodedPivot14GuardOrderAndMissingBranches, not
	// a runtime discriminator: the AQL selector drops a JSON null final
	// member (selectors.go:343 FILTER __value != null) and pivotSelector-
	// Chain mirrors that, so a candidate array can never hold a JSON null
	// — cp_cnt == cp_tcnt on every reachable row and the :267-vs-:230
	// swap hides only in unreachable states. The runtime twins that DO
	// discriminate are the scalar pair (TestPivot14LiveScalarMissingRaw
	// CountNoRaise raises on raw>0/typed=0 null members, which the scalar
	// FIRST list keeps) and the textual receipts here and in
	// TestPivot14GoldenErrorBranch.
	svc := pivot14LiveService(t)
	// ONLY the null-candidate state (no matched-owner-with-no-candidates
	// row here: that owner has raw count 0 too and would raise MISSING
	// under either count, masking the raw-vs-typed swap this leg is the
	// ALIVE control for). valueQuantity present with a null value —
	// jsonb stores the candidate array as [null].
	pivot14SeedObservation(t, svc, "m2", json.RawMessage(`{"component":[{"code":{"coding":[{"system":"http://loinc.org","code":"8480-6"}]},"valueQuantity":{"value":null}}]}`))
	// A SINGLE category: with the default two categories the never-matched
	// diastolic term raises MISSING on its own and masks the raw-vs-typed
	// count swap (the §7 ALIVE asymmetry control must flip on this leg
	// alone).
	codedCategories := []recipe.ConstructionCodedPivotCategory{
		{System: "http://loinc.org", Code: "8480-6", OutputColumnID: "systolic_id"},
	}
	plan := pivot14CompileBundle(t, pivot14CodedBundle("p14_live_missing_typed", codedCategories,
		recipe.PivotDuplicateSum, recipe.PivotMissingCellError, "", nil, false, nil, ""),
		[]string{"FHIR/Observation"})
	rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render missing plan: %v", err)
	}
	_, err = svc.iso.Exec(svc.ctx, rendered.SQL, pivot14Args(rendered)...)
	t.Logf("raise-phase err: %v\nSQL: %s", err, rendered.SQL)
	if err == nil {
		t.Fatalf("typed count 0 with raw count > 0 must raise the MISSING guard at the coded branch (:267):\n%s", rendered.SQL)
	}
	if !strings.Contains(err.Error(), "TABLE_PIVOT_CELL_MISSING") || !strings.Contains(err.Error(), "22P02") {
		t.Fatalf("coded MISSING raise drifted: %v", err)
	}
	// NULL missing sibling: identical data reads the null cell.
	nullPlan := pivot14CompileBundle(t, pivot14CodedBundle("p14_live_missing_null", codedCategories,
		recipe.PivotDuplicateSum, recipe.PivotMissingCellNull, "", nil, false, nil, ""),
		[]string{"FHIR/Observation"})
	nullRendered, err := RenderPhysicalPlan(nullPlan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render null-missing plan: %v", err)
	}
	rows := svc.runRendered(t, nullRendered)
	if len(rows) != 1 || rows[0][0] != "m2" {
		t.Fatalf("the raw-count-bearing root must survive under NULL missing, got %v", rows)
	}
	if rows[0][1] != "<null>" && rows[0][1] != "null" {
		t.Fatalf("null-only candidate cells must read JSON null, got %v", rows)
	}
}

func TestCodedPivot14LiveFlattenChosenCandidate(t *testing.T) {
	// The chosen candidate is FLATTENed ONE level at exactly the layer
	// the oracle does (LET __coded_values = FLATTEN(chosen),
	// table_reshape.go:385): a value selector whose final member stores
	// an array splices that member into its items, so counts and cell
	// readings match AQL instead of treating the array as one element.
	// Without the splice: the cardinality guard stays silent (the
	// cardinality twin below passes vacuously) and the reducer raises
	// VALUE_TYPE on the array (the SUM twin fails) where AQL returns a
	// number.
	svc := pivot14LiveService(t)
	// One owner whose only value member is an array of two numbers:
	// AQL sees items [7, 3] (raw count 2), not one array element.
	pivot14SeedObservation(t, svc, "fl1", json.RawMessage(
		`{"component":[{"code":{"coding":[{"system":"http://loinc.org","code":"8480-6"}]},`+
			`"valueQuantity":{"value":[7,3]}}]}`))

	// Cardinality twin: ERROR duplicate over the two spliced items must
	// raise TABLE_PIVOT_CELL_CARDINALITY (22P02). Pre-splice this does
	// not raise at all (raw count 1).
	cardPlan := pivot14CompileBundle(t, pivot14CodedBundle("p14_live_flatten_card",
		[]recipe.ConstructionCodedPivotCategory{
			{System: "http://loinc.org", Code: "8480-6", OutputColumnID: "systolic_id"},
		}, recipe.PivotDuplicateError, recipe.PivotMissingCellNull, "", nil, false, nil, ""),
		[]string{"FHIR/Observation"})
	cardRendered, err := RenderPhysicalPlan(cardPlan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render flatten cardinality plan: %v", err)
	}
	if _, err := svc.iso.Exec(svc.ctx, cardRendered.SQL, pivot14Args(cardRendered)...); err == nil ||
		!strings.Contains(err.Error(), "TABLE_PIVOT_CELL_CARDINALITY") || !strings.Contains(err.Error(), "22P02") {
		t.Fatalf("spliced array member must reach the cardinality guard like AQL: %v", err)
	}

	// SUM twin: the reducer must return 10 where pre-splice the array
	// item would trip the value-type guard.
	sumBundle := pivot14CodedBundle("p14_live_flatten_sum",
		[]recipe.ConstructionCodedPivotCategory{
			{System: "http://loinc.org", Code: "8480-6", OutputColumnID: "systolic_id"},
		}, recipe.PivotDuplicateSum, recipe.PivotMissingCellNull, "", nil, false, nil, "")
	sumPlan := pivot14CompileBundle(t, sumBundle, []string{"FHIR/Observation"})
	probeRendered, err := RenderPhysicalPlan(sumPlan, mustBinding(t))
	if err != nil {
		t.Fatalf("probe flatten SUM plan: %v", err)
	}
	systolicIndex := -1
	for index, name := range probeRendered.OutputColumns {
		if name == "systolic" {
			systolicIndex = index
		}
	}
	if systolicIndex < 0 {
		t.Fatalf("systolic column missing: %v", probeRendered.OutputColumns)
	}
	sumRendered, err := RenderPhysicalPlan(sumPlan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render flatten SUM plan: %v", err)
	}
	rows := svc.runRendered(t, sumRendered)
	if len(rows) != 1 || rows[0][systolicIndex] != "10" {
		t.Fatalf("array member must FLATTEN into the reducer (SUM 7+3=10), got %v", rows)
	}
}

func TestCodedPivot14LiveFallbackOrdinalPrecedence(t *testing.T) {
	// FIRST-over-candidates is FIRST-in-DECLARED-order (the AQL
	// FIRST(FOR __correlation_candidate IN [a1, a2, ...] FILTER eligible
	// RETURN candidate), predicates.go:177): the VALUES pick carries an
	// explicit ordinal and ORDER BY it, so a planner change can never
	// let a later eligible fallback silently win. Here BOTH candidates
	// are eligible on one matched owner — the primary number must win.
	// (A wrong pick reads the string "999" under DECIMAL, tripping the
	// value-type guard, so the leg fails loudly rather than drifting.)
	// The DECIMAL + valueString-fallback state is refused by the recipe
	// and lower layers (see TestCodedPivot14LiveValueFallbackAndTrim),
	// so it rides hand-carried IR.
	svc := pivot14LiveService(t)
	bundle := pivot14CodedBundle("p14_live_fallback_precedence",
		[]recipe.ConstructionCodedPivotCategory{
			{System: "http://loinc.org", Code: "8480-6", OutputColumnID: "systolic_id"},
		}, recipe.PivotDuplicateSum, recipe.PivotMissingCellNull, "valueQuantity.value", nil, false, []string{"valueQuantity"}, "decimal")
	plan := pivot14CompileBundle(t, bundle, []string{"FHIR/Observation"})
	correlation := pivot14CodedPivot(t, plan).CodedCorrelation
	correlation.ValueFallbacks = []spec.Selector{specSelector(t, "valueString")}
	correlation.ChoiceArms = []string{"valueQuantity", "valueString"}
	pivot14SeedObservation(t, svc, "fp1", map[string]any{
		"component": []map[string]any{
			pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8480-6", "")}, 120, strPtr("999")),
		},
	})
	rendered, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("probe precedence plan: %v", err)
	}
	systolicIndex := -1
	for index, name := range rendered.OutputColumns {
		if name == "systolic" {
			systolicIndex = index
		}
	}
	if systolicIndex < 0 {
		t.Fatalf("systolic column missing: %v", rendered.OutputColumns)
	}
	liveRendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render precedence plan: %v", err)
	}
	rows := svc.runRendered(t, liveRendered)
	if len(rows) != 1 {
		t.Fatalf("precedence rows = %v", rows)
	}
	if rows[0][systolicIndex] != "120" {
		t.Fatalf("declared-order FIRST must pick the primary value 120, got %q", rows[0][systolicIndex])
	}
}

func TestCodedPivot14LiveCompetingGuardsCategoryOrder(t *testing.T) {
	// ALIVE control for the single ordered outer chain: category 0 is in
	// the MISSING state and category 1 carries a matched owner with an
	// unsupported choice arm. The AQL projection forces category 0's
	// LETs first: the category-0 cell guard wins with
	// TABLE_PIVOT_CELL_MISSING (table_reshape.go:384-:386 evaluates the
	// categories strictly in declared order). Raising the choice-arm
	// inside the per-category CTE let the CTE materialization pick
	// INVALID_CHOICE_ARM instead — an unstable winner PostgreSQL does
	// not even fix among the CTEs.
	svc := pivot14LiveService(t)
	// g0: matched 8480-6 owner with NO value → category 0 raw count 0.
	pivot14SeedObservation(t, svc, "g0", map[string]any{
		"component": []map[string]any{
			pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8480-6", "")}, nil, nil),
		},
	})
	// g1: matched 8462-4 owner whose only value is on an arm OUTSIDE the
	// checked binding (default arms = [valueQuantity]) → cp_bad for
	// category 1.
	pivot14SeedObservation(t, svc, "g1", map[string]any{
		"component": []map[string]any{
			pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8462-4", "")}, nil, strPtr("bad-arm")),
		},
	})
	plan := pivot14CompileBundle(t, pivot14CodedBundle("p14_live_guard_order", nil,
		recipe.PivotDuplicateError, recipe.PivotMissingCellError, "", nil, false, nil, ""),
		[]string{"FHIR/Observation"})
	rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render competing-guards plan: %v", err)
	}
	if _, err := svc.iso.Exec(svc.ctx, rendered.SQL, pivot14Args(rendered)...); err == nil ||
		!strings.Contains(err.Error(), "TABLE_PIVOT_CELL_MISSING") ||
		strings.Contains(err.Error(), "INVALID_CHOICE_ARM") {
		t.Fatalf("the declared-category-order chain must pick TABLE_PIVOT_CELL_MISSING over the later category's choice arm: %v", err)
	}
}

func strPtr(value string) *string { return &value }
