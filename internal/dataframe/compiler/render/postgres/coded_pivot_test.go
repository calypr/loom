package postgres

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
		"EXISTS (SELECT 1 FROM",
		"jsonb_typeof(",
		") = 'string'",
		"::text <> ''",
		"LEFT JOIN cp_agg_0 AS a0 ON a0.cp_key = s.cp_key",
		"WHERE t.cp_m)",
		"BOOL_OR(cp_bad) AS cp_bad",
		"WHEN COALESCE(a0.cp_bad, false) THEN CAST('INVALID_CHOICE_ARM'",
		"jsonb_agg(s.cp_e ORDER BY f.ord, s.ord1)",
		"ELSE jsonb_build_array(f.x) END) WITH ORDINALITY AS s(cp_e, ord1)",
		"(ARRAY_AGG(e.cp_e ORDER BY e.ord))[1] AS cp_first",
		`ORDER BY '[' || '"GROUPED_PIVOT"'`,
		`COLLATE "C" ASC`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("coded statement lost %q:\n%s", want, sql)
		}
	}
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
		out[row[0]] = row
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
	if got := out["r2"][systolic]; got != "<null>" && got != "null" {
		t.Fatalf("same code across systems must not match; r2 systolic = %q", got)
	}
	if got := out["r3"][systolic]; got != "15" {
		t.Fatalf("version-varied codings must be duplicate observations (SUM 15), got %q — silent version filtering would return 10 or 5", got)
	}
	if got := out["r4"][hostile]; got != "7" {
		t.Fatalf("hostile category pair must match byte-exactly, got %q", got)
	}
	if got := out["r1"][systolic]; got != "120" {
		t.Fatalf("r1 systolic = %q, want 120", got)
	}

	for _, forbidden := range []string{hostileCode, "EVIL"} {
		if strings.Contains(rendered.SQL, forbidden) {
			t.Fatalf("hostile category pair leaked into SQL text:\n%s", rendered.SQL)
		}
	}
}

func TestCodedPivot14LiveInvalidChoiceArmRaise(t *testing.T) {
	svc := pivot14LiveService(t)
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
	primary := pivot14CodedBundle("p14_live_fallback", []recipe.ConstructionCodedPivotCategory{
		{System: "http://loinc.org", Code: "8480-6", OutputColumnID: "systolic_id"},
	}, recipe.PivotDuplicateSum, recipe.PivotMissingCellNull,
		"valueQuantity.value", nil, false, []string{"valueQuantity"}, "decimal")
	plan := pivot14CompileBundle(t, primary, []string{"FHIR/Observation"})
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
	svc := pivot14LiveService(t)
	pivot14SeedObservation(t, svc, "m2", json.RawMessage(`{"component":[{"code":{"coding":[{"system":"http://loinc.org","code":"8480-6"}]},"valueQuantity":{"value":null}}]}`))
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
	svc := pivot14LiveService(t)
	pivot14SeedObservation(t, svc, "fl1", json.RawMessage(
		`{"component":[{"code":{"coding":[{"system":"http://loinc.org","code":"8480-6"}]},`+
			`"valueQuantity":{"value":[7,3]}}]}`))

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
	svc := pivot14LiveService(t)
	pivot14SeedObservation(t, svc, "g0", map[string]any{
		"component": []map[string]any{
			pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8480-6", "")}, nil, nil),
		},
	})
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

func TestPivot14ProductionCodedLive(t *testing.T) {
	svc := pivot14LiveService(t)
	pivot14SeedObservation(t, svc, "q1", map[string]any{
		"component": []map[string]any{
			pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8480-6", "")}, 120, nil),
		},
	})
	pivot14SeedObservation(t, svc, "q2", map[string]any{
		"component": []map[string]any{
			pivot14Component([]map[string]any{pivot14Coding("http://snomed.info/sct", "8480-6", "")}, 999, nil),
			pivot14Component([]map[string]any{pivot14Coding("http://snomed.info/sct", "8462-4", "")}, 998, nil),
		},
	})
	pivot14SeedObservation(t, svc, "q3", map[string]any{
		"component": []map[string]any{
			pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8480-6", "1")}, 10, nil),
			pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8480-6", "2")}, 5, nil),
		},
	})
	pivot14SeedObservation(t, svc, "q4", map[string]any{
		"component": []map[string]any{
			pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8462-4", "")}, 3, nil),
			pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8462-4", "")}, 4, nil),
		},
	})
	pivot14SeedObservation(t, svc, "q5", map[string]any{
		"component": []map[string]any{
			pivot14Component([]map[string]any{pivot14Coding("http://example.org", "9999-9", "")}, 1, nil),
		},
	})
	bundle := pivot14CodedBundle("p14_prod_coded", nil,
		recipe.PivotDuplicateSum, recipe.PivotMissingCellNull, "", nil, false, nil, "")
	plan := pivot14CompileBundle(t, bundle, []string{"FHIR/Observation"})
	rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render production coded plan: %v", err)
	}
	if strings.Contains(rendered.SQL, "8480-6") || strings.Contains(rendered.SQL, "http://loinc.org") {
		t.Fatalf("category literals leaked into SQL text:\n%s", rendered.SQL)
	}
	carried := 0
	for _, parameter := range rendered.Args {
		if text := fmt.Sprintf("%s", parameter.Value); strings.Contains(text, "8480-6") || strings.Contains(text, "8462-4") {
			carried++
		}
	}
	if carried < 2 {
		t.Fatalf("category pairs must ride parameters: %v", rendered.Args)
	}
	keyIndex := pivot14ColIndex(t, rendered, "_key")
	systolic := pivot14ColIndex(t, rendered, "systolic")
	diastolic := pivot14ColIndex(t, rendered, "diastolic")
	identityIndex := pivot14ColIndex(t, rendered, "__loom_row_id")
	rows := svc.runRendered(t, rendered)
	byRoot := map[string][]string{}
	for _, row := range rows {
		byRoot[row[keyIndex]] = row
	}
	if len(byRoot) != 5 {
		t.Fatalf("production coded roots = %v, want all five", rows)
	}
	expectations := []struct {
		root                string
		systolic, diastolic string
	}{
		{"q1", "120", "<null>"},
		{"q2", "<null>", "<null>"},
		{"q3", "15", "<null>"},
		{"q4", "<null>", "7"},
		{"q5", "<null>", "<null>"},
	}
	for _, want := range expectations {
		row, ok := byRoot[want.root]
		if !ok {
			t.Fatalf("root %s missing: %v", want.root, rows)
		}
		if row[systolic] != want.systolic && !(want.systolic == "<null>" && pivot14CellIsNull(row[systolic])) {
			t.Fatalf("%s systolic = %q, want %s", want.root, row[systolic], want.systolic)
		}
		if row[diastolic] != want.diastolic && !(want.diastolic == "<null>" && pivot14CellIsNull(row[diastolic])) {
			t.Fatalf("%s diastolic = %q, want %s", want.root, row[diastolic], want.diastolic)
		}
		wantIdentity := `["GROUPED_PIVOT","p14_prod_coded",["STRING","` + want.root + `"]]`
		if got := row[identityIndex]; got != wantIdentity {
			t.Fatalf("%s identity = %q, want %s", want.root, got, wantIdentity)
		}
	}
}

func TestCodedPivot14SameCodeDistinctSystemAliveControl(t *testing.T) {
	svc := pivot14LiveService(t)
	pivot14SeedObservation(t, svc, "sd1", map[string]any{
		"component": []map[string]any{
			pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8480-6", "")}, 120, nil),
		},
	})
	pivot14SeedObservation(t, svc, "sd2", map[string]any{
		"component": []map[string]any{
			pivot14Component([]map[string]any{pivot14Coding("http://snomed.info/sct", "8480-6", "")}, 999, nil),
			pivot14Component([]map[string]any{pivot14Coding("http://snomed.info/sct", "8462-4", "")}, 998, nil),
		},
	})
	plan := pivot14CompileBundle(t, pivot14CodedBundle("p14_sd_system", nil,
		recipe.PivotDuplicateSum, recipe.PivotMissingCellNull, "", nil, false, nil, ""),
		[]string{"FHIR/Observation"})
	rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render system-separation plan: %v", err)
	}
	rows := svc.runRendered(t, rendered)
	byRoot := map[string][]string{}
	keyIndex := pivot14ColIndex(t, rendered, "_key")
	for _, row := range rows {
		byRoot[row[keyIndex]] = row
	}
	if len(byRoot) != 2 {
		t.Fatalf("system-separation roots = %v", rows)
	}
	if got := byRoot["sd2"][pivot14ColIndex(t, rendered, "systolic")]; !pivot14CellIsNull(got) {
		t.Fatalf("foreign-system 8480-6 must never match (got %q — a system-drop would read 999)", got)
	}
	if got := byRoot["sd2"][pivot14ColIndex(t, rendered, "diastolic")]; !pivot14CellIsNull(got) {
		t.Fatalf("foreign-system 8462-4 must never match (got %q — a code-only match would read 998)", got)
	}
	if got := byRoot["sd1"][pivot14ColIndex(t, rendered, "systolic")]; got != "120" {
		t.Fatalf("loinc 8480-6 must match (got %q)", got)
	}
}

func TestCodedPivot14DuplicateObservationAliveControl(t *testing.T) {
	bundleSum := pivot14CodedBundle("p14_dup_sum",
		[]recipe.ConstructionCodedPivotCategory{
			{System: "http://loinc.org", Code: "8480-6", OutputColumnID: "systolic_id"},
		}, recipe.PivotDuplicateSum, recipe.PivotMissingCellNull, "", nil, false, nil, "")
	dupPayload := map[string]any{
		"component": []map[string]any{
			pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8480-6", "1")}, 7, nil),
			pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8480-6", "2")}, 7, nil),
		},
	}
	sumSvc := pivot14LiveService(t)
	pivot14SeedObservation(t, sumSvc, "dup1", dupPayload)
	sumPlan := pivot14CompileBundle(t, bundleSum, []string{"FHIR/Observation"})
	sumRendered, err := RenderPhysicalPlan(sumPlan, pivot14Binding(t, sumSvc))
	if err != nil {
		t.Fatalf("render duplicate-observation SUM plan: %v", err)
	}
	sumRows := sumSvc.runRendered(t, sumRendered)
	if len(sumRows) != 1 {
		t.Fatalf("duplicate-observation SUM rows = %v", sumRows)
	}
	if got := sumRows[0][pivot14ColIndex(t, sumRendered, "systolic")]; got != "14" {
		t.Fatalf("duplicate observations must double-count (SUM 7+7=14), got %q (DISTINCT collapse reads 7)", got)
	}

	errSvc := pivot14LiveService(t)
	pivot14SeedObservation(t, errSvc, "dup1", dupPayload)
	bundleError := pivot14CodedBundle("p14_dup_card",
		[]recipe.ConstructionCodedPivotCategory{
			{System: "http://loinc.org", Code: "8480-6", OutputColumnID: "systolic_id"},
		}, recipe.PivotDuplicateError, recipe.PivotMissingCellNull, "", nil, false, nil, "")
	cardPlan := pivot14CompileBundle(t, bundleError, []string{"FHIR/Observation"})
	cardRendered, err := RenderPhysicalPlan(cardPlan, pivot14Binding(t, errSvc))
	if err != nil {
		t.Fatalf("render duplicate-observation cardinality plan: %v", err)
	}
	pivot14ExpectGuardRaise(t, errSvc, cardRendered, pivotCardinalityTag)
}

func TestCodedPivot14OwnerZeroItemAliveControl(t *testing.T) {
	mixed := map[string]any{
		"component": []map[string]any{
			pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8480-6", "")}, nil, nil),
			pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8480-6", "")}, 5, nil),
		},
	}
	zeroRoot := map[string]any{
		"component": []map[string]any{
			pivot14Component([]map[string]any{pivot14Coding("http://example.org", "9999-9", "")}, 1, nil),
		},
	}
	single := []recipe.ConstructionCodedPivotCategory{
		{System: "http://loinc.org", Code: "8480-6", OutputColumnID: "systolic_id"},
	}

	zeroSvc := pivot14LiveService(t)
	pivot14SeedObservation(t, zeroSvc, "z1", mixed)
	zeroPlan := pivot14CompileBundle(t, pivot14CodedBundle("p14_zero_item", single,
		recipe.PivotDuplicateError, recipe.PivotMissingCellNull, "", nil, false, nil, ""),
		[]string{"FHIR/Observation"})
	zeroRendered, err := RenderPhysicalPlan(zeroPlan, pivot14Binding(t, zeroSvc))
	if err != nil {
		t.Fatalf("render zero-item plan: %v", err)
	}
	zeroRows := zeroSvc.runRendered(t, zeroRendered)
	if len(zeroRows) != 1 || zeroRows[0][pivot14ColIndex(t, zeroRendered, "systolic")] != "5" {
		t.Fatalf("the empty-candidate owner must contribute nothing (cell 5, no cardinality raise), got %v", zeroRows)
	}

	nullSvc := pivot14LiveService(t)
	pivot14SeedObservation(t, nullSvc, "z2", zeroRoot)
	nullPlan := pivot14CompileBundle(t, pivot14CodedBundle("p14_zero_null", single,
		recipe.PivotDuplicateSum, recipe.PivotMissingCellNull, "", nil, false, nil, ""),
		[]string{"FHIR/Observation"})
	nullRendered, err := RenderPhysicalPlan(nullPlan, pivot14Binding(t, nullSvc))
	if err != nil {
		t.Fatalf("render zero-coding NULL plan: %v", err)
	}
	nullRows := nullSvc.runRendered(t, nullRendered)
	if len(nullRows) != 1 || !pivot14CellIsNull(nullRows[0][pivot14ColIndex(t, nullRendered, "systolic")]) {
		t.Fatalf("zero-coding root must read the NULL-policy cell, got %v", nullRows)
	}

	raiseSvc := pivot14LiveService(t)
	pivot14SeedObservation(t, raiseSvc, "z2", zeroRoot)
	raisePlan := pivot14CompileBundle(t, pivot14CodedBundle("p14_zero_raise", single,
		recipe.PivotDuplicateSum, recipe.PivotMissingCellError, "", nil, false, nil, ""),
		[]string{"FHIR/Observation"})
	raiseRendered, err := RenderPhysicalPlan(raisePlan, pivot14Binding(t, raiseSvc))
	if err != nil {
		t.Fatalf("render zero-coding ERROR plan: %v", err)
	}
	pivot14ExpectGuardRaise(t, raiseSvc, raiseRendered, pivotCellMissingTag)
}

func TestCodedPivot14FallbackBlankAliveControl(t *testing.T) {
	blankBundle := pivot14CodedBundle("p14_fb_blank",
		[]recipe.ConstructionCodedPivotCategory{
			{System: "http://loinc.org", Code: "8480-6", OutputColumnID: "systolic_id"},
		}, recipe.PivotDuplicateSum, recipe.PivotMissingCellNull, "valueQuantity.value", nil, false, []string{"valueQuantity"}, "decimal")
	blankPlan := pivot14CompileBundle(t, blankBundle, []string{"FHIR/Observation"})
	correlation := pivot14CodedPivot(t, blankPlan).CodedCorrelation
	correlation.ValueFallbacks = []spec.Selector{specSelector(t, "valueString")}
	correlation.ChoiceArms = []string{"valueQuantity", "valueString"}
	correlation.FallbackOnBlankString = true

	chosenSvc := pivot14LiveService(t)
	pivot14SeedObservation(t, chosenSvc, "fb1", map[string]any{
		"component": []map[string]any{
			pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8480-6", "")}, nil, strPtr("8.5")),
		},
	})
	chosenRendered, err := RenderPhysicalPlan(blankPlan, pivot14Binding(t, chosenSvc))
	if err != nil {
		t.Fatalf("render fallback-chosen plan: %v", err)
	}
	pivot14ExpectGuardRaise(t, chosenSvc, chosenRendered, pivotValueTypeTag)

	nullSvc := pivot14LiveService(t)
	pivot14SeedObservation(t, nullSvc, "fb2", map[string]any{
		"component": []map[string]any{
			pivot14Component([]map[string]any{pivot14Coding("http://loinc.org", "8480-6", "")}, nil, strPtr("   ")),
		},
	})
	nullRendered, err := RenderPhysicalPlan(blankPlan, pivot14Binding(t, nullSvc))
	if err != nil {
		t.Fatalf("render fallback-blank plan: %v", err)
	}
	rows := nullSvc.runRendered(t, nullRendered)
	if len(rows) != 1 || !pivot14CellIsNull(rows[0][pivot14ColIndex(t, nullRendered, "systolic")]) {
		t.Fatalf("the all-blank fallback must be ineligible and read null without a raise, got %v", rows)
	}
}

func TestCodedPivot14TypedCountMissingRaiseControl(t *testing.T) {
	single := []recipe.ConstructionCodedPivotCategory{
		{System: "http://loinc.org", Code: "8480-6", OutputColumnID: "systolic_id"},
	}
	nullSplice := json.RawMessage(`{"component":[{"code":{"coding":[{"system":"http://loinc.org","code":"8480-6"}]},"valueQuantity":{"value":[null]}}]}`)

	raiseSvc := pivot14LiveService(t)
	pivot14SeedObservation(t, raiseSvc, "tc1", nullSplice)
	raisePlan := pivot14CompileBundle(t, pivot14CodedBundle("p14_typed_raise", single,
		recipe.PivotDuplicateSum, recipe.PivotMissingCellError, "", nil, false, nil, ""),
		[]string{"FHIR/Observation"})
	raiseRendered, err := RenderPhysicalPlan(raisePlan, pivot14Binding(t, raiseSvc))
	if err != nil {
		t.Fatalf("render typed-count raise plan: %v", err)
	}
	pivot14ExpectGuardRaise(t, raiseSvc, raiseRendered, pivotCellMissingTag)

	nullSvc := pivot14LiveService(t)
	pivot14SeedObservation(t, nullSvc, "tc1", nullSplice)
	nullPlan := pivot14CompileBundle(t, pivot14CodedBundle("p14_typed_null", single,
		recipe.PivotDuplicateSum, recipe.PivotMissingCellNull, "", nil, false, nil, ""),
		[]string{"FHIR/Observation"})
	nullRendered, err := RenderPhysicalPlan(nullPlan, pivot14Binding(t, nullSvc))
	if err != nil {
		t.Fatalf("render typed-count NULL plan: %v", err)
	}
	rows := nullSvc.runRendered(t, nullRendered)
	if len(rows) != 1 || !pivot14CellIsNull(rows[0][pivot14ColIndex(t, nullRendered, "systolic")]) {
		t.Fatalf("typed-count 0 under NULL policy must read the null cell (raw count 1 must not raise), got %v", rows)
	}
}

func TestCodedPivot14LiveValueTypeRaise(t *testing.T) {
	for _, value := range []string{"abc", "12.5"} {
		t.Run("value_"+value, func(t *testing.T) {
			svc := pivot14LiveService(t)
			pivot14SeedObservation(t, svc, "cv1", json.RawMessage(
				`{"component":[{"code":{"coding":[{"system":"http://loinc.org","code":"8480-6"}]},"valueQuantity":{"value":"`+value+`"}}]}`))
			plan := pivot14CompileBundle(t, pivot14CodedBundle("p14_coded_valuetype",
				[]recipe.ConstructionCodedPivotCategory{
					{System: "http://loinc.org", Code: "8480-6", OutputColumnID: "systolic_id"},
				}, recipe.PivotDuplicateSum, recipe.PivotMissingCellNull, "", nil, false, nil, ""),
				[]string{"FHIR/Observation"})
			rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
			if err != nil {
				t.Fatalf("render coded value-type plan: %v", err)
			}
			pivot14ExpectGuardRaise(t, svc, rendered, pivotValueTypeTag)
		})
	}

	cleanSvc := pivot14LiveService(t)
	pivot14SeedObservation(t, cleanSvc, "cv0", json.RawMessage(
		`{"component":[{"code":{"coding":[{"system":"http://loinc.org","code":"8480-6"}]},"valueQuantity":{"value":6}}]}`))
	cleanPlan := pivot14CompileBundle(t, pivot14CodedBundle("p14_coded_clean",
		[]recipe.ConstructionCodedPivotCategory{
			{System: "http://loinc.org", Code: "8480-6", OutputColumnID: "systolic_id"},
		}, recipe.PivotDuplicateSum, recipe.PivotMissingCellNull, "", nil, false, nil, ""),
		[]string{"FHIR/Observation"})
	cleanRendered, err := RenderPhysicalPlan(cleanPlan, pivot14Binding(t, cleanSvc))
	if err != nil {
		t.Fatalf("render coded clean twin: %v", err)
	}
	rows := cleanSvc.runRendered(t, cleanRendered)
	if len(rows) != 1 || rows[0][pivot14ColIndex(t, cleanRendered, "systolic")] != "6" {
		t.Fatalf("clean coded data must read SUM 6 without a raise, got %v", rows)
	}
}

func TestCodedPivotIRRowValuesAliveControl(t *testing.T) {
	base := pivot14CompileCodedBundle(t, "p14_ir_rowvalues")
	clone := clonePlanForPivot14(base)
	pivot14CodedPivot(t, clone).RowValues = []ir.PhysicalStageRowValue{
		{InputColumn: "resource_id", Output: "rv", Variable: "rv0", Policy: "ALL", InputKind: "STRING"},
	}
	pivot14CodedRefused(t, clone, "RowValues (ir-accepted, PG-refused) names pg-sql-13", "pg-sql-13")
}

func TestCodedPivotIRSetAliveControl(t *testing.T) {
	coded := pivot14CompileCodedBundle(t, "p14_ir_set")
	clone := clonePlanForPivot14(coded)
	operations := make([]ir.PhysicalOperation, 0, len(clone.Operations)+1)
	operations = append(operations, clone.Operations[0])
	operations = append(operations, ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Set: &ir.PhysicalSet{Variable: "s"}})
	operations = append(operations, clone.Operations[1:]...)
	clone.Operations = operations
	pivot14CodedRefused(t, clone, "source-region SET (ir-accepted, PG-refused)", "SET is a pg-sql-16 capability gap")

	scalar := pivot14ScalarPlan(t, pivot14ScalarConfig{})
	scalarOps := make([]ir.PhysicalOperation, 0, len(scalar.Operations)+1)
	scalarOps = append(scalarOps, scalar.Operations[0])
	scalarOps = append(scalarOps, ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Set: &ir.PhysicalSet{Variable: "s"}})
	scalarOps = append(scalarOps, scalar.Operations[1:]...)
	scalar.Operations = scalarOps
	pivot14Refused(t, scalar, "scalar-door SET coexistence", "cannot coexist with GROUPED_PIVOT")
}

func TestCodedPivotIRExpressionLetAliveControl(t *testing.T) {
	coded := pivot14CompileCodedBundle(t, "p14_ir_let")
	clone := clonePlanForPivot14(coded)
	operations := make([]ir.PhysicalOperation, 0, len(clone.Operations)+1)
	operations = append(operations, clone.Operations[0])
	operations = append(operations, ir.PhysicalOperation{Kind: ir.PhysicalExpressionLetOp,
		ExpressionLet: &ir.PhysicalExpressionLet{Variable: "e"}})
	operations = append(operations, clone.Operations[1:]...)
	clone.Operations = operations
	pivot14CodedRefused(t, clone, "source-region EXPRESSION_LET (ir-accepted, PG-refused)", "EXPRESSION_LET is a pg-sql-16 capability gap")

	scalar := pivot14ScalarPlan(t, pivot14ScalarConfig{})
	scalarOps := make([]ir.PhysicalOperation, 0, len(scalar.Operations)+1)
	scalarOps = append(scalarOps, scalar.Operations[0])
	scalarOps = append(scalarOps, ir.PhysicalOperation{Kind: ir.PhysicalExpressionLetOp,
		ExpressionLet: &ir.PhysicalExpressionLet{Variable: "e"}})
	scalarOps = append(scalarOps, scalar.Operations[1:]...)
	scalar.Operations = scalarOps
	pivot14Refused(t, scalar, "scalar-door EXPRESSION_LET coexistence", "cannot coexist with GROUPED_PIVOT")
}
