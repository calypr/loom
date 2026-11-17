package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	spec "github.com/calypr/loom/internal/fhir/schema"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	pivot14Project    = "proj-a"
	pivot14Generation = "gen-1"
	pivot14AuthPath   = "FHIR/Patient"
	pivot14Prefix     = "verify_sql14v1_"
)

func pivot14CompileBundle(t *testing.T, bundle recipe.Bundle, authPaths []string) ir.PhysicalPlan {
	t.Helper()
	bindings := recipe.RuntimeBindings{
		Project: pivot14Project, DatasetGeneration: pivot14Generation,
		AuthResourcePaths: authPaths,
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
	output := compiled.Outputs[0]
	if output.OptimizedPlan != nil {
		return *output.OptimizedPlan
	}
	return output.Plan
}

func pivot14ScalarBundle(name string, filters []recipe.Filter, duplicate recipe.PivotDuplicatePolicy,
	missing recipe.PivotMissingCellPolicy, unlisted recipe.PivotUnlistedCategoryPolicy) recipe.Bundle {
	zero := int64(0)
	one := int64(1)
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: name, TranslationVersion: "test",
		Outputs: []recipe.Output{{
			Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Filters: filters,
			Fields: []recipe.Field{
				{Name: "grp", Expr: recipe.Expression{Select: "gender"}},
				{Name: "category", Expr: recipe.Expression{Select: "multipleBirthInteger"}},
				{Name: "value", Expr: recipe.Expression{Select: "multipleBirthInteger"}},
			},
			TableReshape: &recipe.TableReshape{
				Kind: recipe.TableReshapeGroupedPivot,
				GroupedPivot: &recipe.GroupedPivot{
					ConstructionID: name, GroupKeys: []string{"grp"},
					CategoryColumn: "category", ValueColumn: "value",
					Categories: []recipe.GroupedPivotCategory{
						{Key: recipe.TableScalar{Kind: recipe.TableScalarInteger, Integer: &zero}, Output: "birth_zero", Label: "Zero"},
						{Key: recipe.TableScalar{Kind: recipe.TableScalarInteger, Integer: &one}, Output: "birth_one", Label: "One"},
					},
					DuplicatePolicy: duplicate, MissingCellPolicy: missing,
					UnlistedCategoryPolicy: unlisted,
				},
			},
		}},
	}
}

func pivot14FemaleFilter() []recipe.Filter {
	female := "female"
	return []recipe.Filter{{Select: "gender", Operator: recipe.FilterEquals,
		Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &female}}}}
}

type pivot14ScalarConfig struct {
	keyless         bool
	duplicate       string
	missing         string
	unlisted        string
	window          bool
	presence        string
	categoryOmit    bool
	extractorValue  bool
	categories      []ir.PhysicalGroupedPivotCategory
	categoryType    string
	valueType       string
	constructionID  string
	categoryBinds   map[string]any
	returnDriftName string
}

func pivot14ScalarPlan(t *testing.T, cfg pivot14ScalarConfig) ir.PhysicalPlan {
	t.Helper()
	duplicate := cfg.duplicate
	if duplicate == "" {
		duplicate = "ERROR"
	}
	missing := cfg.missing
	if missing == "" {
		missing = "NULL"
	}
	unlisted := cfg.unlisted
	if unlisted == "" {
		unlisted = "ERROR"
	}
	categories := cfg.categories
	if categories == nil {
		categories = []ir.PhysicalGroupedPivotCategory{
			{Output: "cell_alpha", MatchKind: ir.PhysicalPivotCategoryValueMatch, ValueBindKey: "cat_alpha", ValueKind: "STRING"},
			{Output: "cell_beta", MatchKind: ir.PhysicalPivotCategoryValueMatch, ValueBindKey: "cat_beta", ValueKind: "STRING"},
			{Output: "cell_null", MatchKind: ir.PhysicalPivotCategoryNullMatch},
		}
		if cfg.presence != "" {
			categories = append(categories,
				ir.PhysicalGroupedPivotCategory{Output: "cell_missing", MatchKind: ir.PhysicalPivotCategoryMissingMatch})
		}
	}
	categoryType := cfg.categoryType
	if categoryType == "" {
		categoryType = "STRING"
	}
	valueType := cfg.valueType
	if valueType == "" {
		valueType = "DECIMAL"
	}
	constructionID := cfg.constructionID
	if constructionID == "" {
		constructionID = "p14-hand-construct"
	}
	bindVars := map[string]any{
		"root_collection":                  "Patient",
		"project":                          pivot14Project,
		"dataset_generation":               pivot14Generation,
		"auth_resource_paths":              []string{pivot14AuthPath},
		"auth_resource_paths_unrestricted": false,
		"scope_allowed":                    true,
		"p14_construction_id":              constructionID,
		"cat_alpha":                        "alpha",
		"cat_beta":                         "beta",
		"limit":                            2,
	}
	for key, value := range cfg.categoryBinds {
		bindVars[key] = value
	}
	presence := &ir.PhysicalProjectionPresence{
		Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
		Paths:  [][]string{{"cProp"}},
	}
	categoryProjection := ir.PhysicalProjection{
		Name:  "ccol",
		Value: ir.PhysicalValue{Variable: "root", Path: []string{"cProp"}},
	}
	categoryPresenceColumn := ""
	switch cfg.presence {
	case "contract":
		categoryProjection.Presence = presence
		categoryPresenceColumn = "cProp_present"
	case "from-input":
	}
	valueProjection := ir.PhysicalProjection{
		Name:  "vcol",
		Value: ir.PhysicalValue{Variable: "root", Path: []string{"vProp"}},
	}
	if cfg.extractorValue {
		valueProjection = ir.PhysicalProjection{
			Name: "vcol",
			Expression: &ir.PhysicalExpression{
				Kind:         ir.PhysicalExtractExpression,
				Cardinality:  ir.PhysicalScalarCardinality,
				NullBehavior: ir.PhysicalPreserveNull,
				Extract: &ir.PhysicalExtract{
					Source:       ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
					ResourceType: "Patient",
					Selector:     specSelector(t, "multipleBirthInteger"),
				},
			},
		}
	}
	pivot := &ir.PhysicalGroupedPivot{
		ConstructionID:        constructionID,
		ConstructionIDBindKey: "p14_construction_id",
		InputRowVariable:      "irow",
		GroupRowsVariable:     "grow",
		OutputRowVariable:     "prow",
		InputProjections: []ir.PhysicalProjection{
			{Name: "gcol", Value: ir.PhysicalValue{Variable: "root", Path: []string{"gProp"}}},
			categoryProjection,
			valueProjection,
		},
		CategoryColumn:         "ccol",
		CategoryType:           categoryType,
		ValueColumn:            "vcol",
		ValueType:              valueType,
		Categories:             categories,
		DuplicatePolicy:        duplicate,
		MissingCellPolicy:      missing,
		UnlistedCategoryPolicy: unlisted,
	}
	if cfg.presence == "contract" {
		pivot.CategoryPresence = presence
		pivot.CategoryPresenceColumn = categoryPresenceColumn
	}
	if cfg.presence == "from-input" {
		pivot.CategoryPresenceFromInput = true
		pivot.CategoryPresenceColumn = "cpresent"
		pivot.InputProjections = append(pivot.InputProjections, ir.PhysicalProjection{
			Name:  "cpresent",
			Value: ir.PhysicalValue{Variable: "root", Path: []string{"cPresent"}},
		})
	}
	if cfg.categoryOmit {
		pivot.InputProjections[1].Expression = &ir.PhysicalExpression{
			Kind:         ir.PhysicalExtractExpression,
			Cardinality:  ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalOmitNulls,
			Extract: &ir.PhysicalExtract{
				Source:       ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
				ResourceType: "Patient",
				Selector:     specSelector(t, "gender"),
			},
		}
		pivot.InputProjections[1].Value = ir.PhysicalValue{}
		pivot.InputProjections[1].Presence = nil
	}
	if unlisted == "EXCLUDE_WITH_EVIDENCE" {
		pivot.UnlistedEvidenceColumn = "unlisted_n"
	}
	var groupKeys []ir.PhysicalGroupedPivotKey
	if !cfg.keyless {
		groupKeys = append(groupKeys, ir.PhysicalGroupedPivotKey{Column: "gcol", Variable: "g0", Kind: "STRING", Output: "grp"})
	}
	pivot.GroupKeys = groupKeys

	returnNames := []string{}
	if !cfg.keyless {
		returnNames = append(returnNames, "grp")
	}
	for _, category := range categories {
		returnNames = append(returnNames, category.Output)
	}
	if unlisted == "EXCLUDE_WITH_EVIDENCE" {
		returnNames = append(returnNames, "unlisted_n")
	}
	returnNames = append(returnNames, "__loom_row_id")
	projections := make([]ir.PhysicalProjection, 0, len(returnNames))
	for _, name := range returnNames {
		if cfg.returnDriftName != "" && name == cfg.returnDriftName {
			projections = append(projections, ir.PhysicalProjection{
				Name:  name,
				Value: ir.PhysicalValue{Variable: "prow", Path: []string{"drifted_" + name}},
			})
			continue
		}
		projections = append(projections, ir.PhysicalProjection{
			Name:  name,
			Value: ir.PhysicalValue{Variable: "prow", Path: []string{name}},
		})
	}

	operations := []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalGroupedPivotOp, GroupedPivot: pivot},
	}
	if cfg.window {
		operations = append(operations,
			ir.PhysicalOperation{Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{
				Keys: []ir.PhysicalValue{{Variable: "prow", Path: []string{"__loom_row_id"}}}}},
			ir.PhysicalOperation{Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: "limit"}})
	}
	operations = append(operations, ir.PhysicalOperation{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: projections}})
	return ir.PhysicalPlan{Version: 1, Engine: ir.PhysicalEngineAQL, BindVars: bindVars, Operations: operations}
}

func pivot14Render(t *testing.T, plan ir.PhysicalPlan) string {
	t.Helper()
	rendered, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	return rendered.SQL
}

func pivot14Refused(t *testing.T, plan ir.PhysicalPlan, label string, wantFragment string) {
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

func TestPivot14ProductionScalarPivotRenders(t *testing.T) {
	plan := pivot14CompileBundle(t, pivot14ScalarBundle("p14_prod_render", nil,
		recipe.PivotDuplicateSum, recipe.PivotMissingCellNull, recipe.PivotUnlistedCategoryExcludeWithEvidence),
		[]string{pivot14AuthPath})
	rendered, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("production scalar pivot must render through the registered door: %v", err)
	}
	sql := rendered.SQL
	for _, want := range []string{
		"WITH pv_src AS (SELECT",
		"FROM pv_src AS p",
		"GROUP BY",
		`ORDER BY`,
		`COLLATE "C" ASC`,
		"jsonb_typeof(",
		"::double precision)",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("production statement lost %q:\n%s", want, sql)
		}
	}
	for _, forbidden := range []string{"proj-a", "gen-1", "FHIR/Patient", "female"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("plan data leaked into statement text (%q):\n%s", forbidden, sql)
		}
	}
	if len(rendered.Args) == 0 {
		t.Fatal("production pivot statement carries no parameters")
	}
}

func TestPivot14GoldenScalarStatement(t *testing.T) {
	sql := pivot14Render(t, pivot14ScalarPlan(t, pivot14ScalarConfig{
		duplicate: "SUM", missing: "NULL", unlisted: "ERROR", presence: "contract",
	}))
	unlistedFirst := strings.Index(sql, `CAST('TABLE_PIVOT_UNLISTED_CATEGORY'`)
	cardinalityAfter := strings.Index(sql, `CAST('TABLE_PIVOT_VALUE_TYPE_MISMATCH'`)
	if unlistedFirst < 0 || cardinalityAfter < unlistedFirst {
		t.Fatalf("guard chain order drifted (unlisted must precede per-category guards):\n%s", sql)
	}
	for _, want := range []string{
		"COUNT(*) FILTER (WHERE NOT COALESCE((",
		"THEN CAST('TABLE_PIVOT_UNLISTED_CATEGORY'",
		"CASE WHEN COUNT(*) FILTER (WHERE",
		"THEN 'null'::jsonb ELSE to_jsonb(SUM(CASE WHEN jsonb_typeof(",
		") = 'number' THEN ",
		"::double precision END) FILTER (WHERE",
		"jsonb_typeof(",
		"THEN CAST('TABLE_PIVOT_VALUE_TYPE_MISMATCH'",
		"jsonb_typeof(",
		"= 'string'",
		"::jsonb)",
		`'[' || '"GROUPED_PIVOT"' || ',' || to_jsonb(`,
		`|| ',' || '[' || to_jsonb(`,
		`|| ']' || ']'`,
		"GROUP BY p.",
		`COLLATE "C" ASC`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("golden statement lost %q:\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "TABLE_PIVOT_CELL_MISSING") {
		t.Fatalf("NULL missing policy must not emit the missing guard:\n%s", sql)
	}
	if !strings.Contains(sql, "NOT (p.") {
		t.Fatalf("MISSING category must render NOT (present):\n%s", sql)
	}
}

func TestPivot14GoldenErrorBranch(t *testing.T) {
	sql := pivot14Render(t, pivot14ScalarPlan(t, pivot14ScalarConfig{
		duplicate: "ERROR", missing: "ERROR", unlisted: "ERROR",
	}))
	for _, want := range []string{
		"(ARRAY_AGG(p.",
		") FILTER (WHERE",
		")[1]",
		"COALESCE(",
		"THEN CAST('TABLE_PIVOT_CELL_MISSING'",
		"THEN CAST('TABLE_PIVOT_CELL_CARDINALITY'",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("ERROR-branch golden lost %q:\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "ELSE to_jsonb(") {
		t.Fatalf("ERROR branch must not enumerate typed reducer values:\n%s", sql)
	}
}

func TestPivot14GoldenWindowClauses(t *testing.T) {
	sql := pivot14Render(t, pivot14ScalarPlan(t, pivot14ScalarConfig{
		duplicate: "MAX", window: true,
	}))
	if !strings.Contains(sql, `COLLATE "C" ASC LIMIT $`) {
		t.Fatalf("window leg lost the identity order + bound LIMIT:\n%s", sql)
	}
	if strings.Contains(sql, "LIMIT 2") {
		t.Fatalf("LIMIT must stay parameterized:\n%s", sql)
	}
}

func TestPivot14GoldenNoWindowPerKeyOrder(t *testing.T) {
	sql := pivot14Render(t, pivot14ScalarPlan(t, pivot14ScalarConfig{duplicate: "MIN"}))
	for _, want := range []string{
		"CASE WHEN p.",
		"= 'boolean' THEN 1",
		"= 'number' THEN 2",
		"= 'string' THEN 3",
		"ELSE 4 END) ASC",
		"::double precision) ASC",
		`COLLATE "C") ASC`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("per-key order lost %q:\n%s", want, sql)
		}
	}
}

func TestPivot14GoldenPresenceForms(t *testing.T) {
	sql := pivot14Render(t, pivot14ScalarPlan(t, pivot14ScalarConfig{
		duplicate: "ERROR", presence: "contract",
		categories: []ir.PhysicalGroupedPivotCategory{
			{Output: "cell_alpha", MatchKind: ir.PhysicalPivotCategoryValueMatch, ValueBindKey: "cat_alpha", ValueKind: "STRING"},
			{Output: "cell_missing", MatchKind: ir.PhysicalPivotCategoryMissingMatch},
		},
	}))
	if !strings.Contains(sql, "THEN 'true'::jsonb ELSE 'false'::jsonb END") {
		t.Fatalf("presence contract lost the tri-state proof read:\n%s", sql)
	}
	if !strings.Contains(sql, " = 'true'::jsonb") {
		t.Fatalf("presence comparison drifted:\n%s", sql)
	}

	sql = pivot14Render(t, pivot14ScalarPlan(t, pivot14ScalarConfig{
		duplicate: "ERROR", presence: "from-input",
		categories: []ir.PhysicalGroupedPivotCategory{
			{Output: "cell_alpha", MatchKind: ir.PhysicalPivotCategoryValueMatch, ValueBindKey: "cat_alpha", ValueKind: "STRING"},
			{Output: "cell_missing", MatchKind: ir.PhysicalPivotCategoryMissingMatch},
		},
	}))
	if strings.Contains(sql, "THEN 'true'::jsonb ELSE 'false'::jsonb END") {
		t.Fatalf("input-carried presence must not recompute the proof:\n%s", sql)
	}
	if !strings.Contains(sql, "COALESCE(") || !strings.Contains(sql, " = 'true'::jsonb, false)") {
		t.Fatalf("input-carried presence must compare the companion bit two-valued:\n%s", sql)
	}

	sql = pivot14Render(t, pivot14ScalarPlan(t, pivot14ScalarConfig{
		duplicate: "ERROR", categoryOmit: true,
		categories: []ir.PhysicalGroupedPivotCategory{
			{Output: "cell_null", MatchKind: ir.PhysicalPivotCategoryNullMatch},
		},
	}))
	if !strings.Contains(sql, "IS NOT NULL") || !strings.Contains(sql, "= 'null'::jsonb") {
		t.Fatalf("null-omission category read drifted:\n%s", sql)
	}
}

func TestPivot14HiddenAuthGroupKeyHandIR(t *testing.T) {
	plan := pivot14ScalarPlan(t, pivot14ScalarConfig{duplicate: "ERROR"})
	pivot := plan.Operations[1].GroupedPivot
	pivot.GroupKeys = append(pivot.GroupKeys, ir.PhysicalGroupedPivotKey{
		Column: "authcol", Variable: "authg", Kind: "STRING", Output: "authcol", Hidden: true})
	pivot.InputProjections = append(pivot.InputProjections, ir.PhysicalProjection{
		Name:   "authcol",
		Hidden: true,
		Value:  ir.PhysicalValue{Variable: "root", Path: []string{"auth_resource_path"}},
	})
	plan.Operations[1].GroupedPivot = pivot
	returnOp := plan.Operations[len(plan.Operations)-1]
	returnOp.Return.Projections = append(returnOp.Return.Projections, ir.PhysicalProjection{
		Name:  "authcol",
		Value: ir.PhysicalValue{Variable: "prow", Path: []string{"authcol"}},
	})
	plan.Operations[len(plan.Operations)-1] = returnOp
	sql := pivot14Render(t, plan)
	if !strings.Contains(sql, "auth_resource_path") {
		t.Fatalf("hidden auth group key lost its read:\n%s", sql)
	}
	if strings.Contains(sql, "GROUP BY p.") && !strings.Contains(sql, "auth") {
		t.Fatalf("hidden auth key missing from the group:\n%s", sql)
	}
}

func TestPivot14HostileCategoryValuesStayParameterized(t *testing.T) {
	hostile := map[string]any{
		"cat_alpha": "a\"b',c]}}\"{[ -- /* x' || $$ $$",
	}
	plan := pivot14ScalarPlan(t, pivot14ScalarConfig{
		duplicate:      "ERROR",
		categories:     []ir.PhysicalGroupedPivotCategory{{Output: "cell_alpha", MatchKind: ir.PhysicalPivotCategoryValueMatch, ValueBindKey: "cat_alpha", ValueKind: "STRING"}},
		constructionID: `p14",["EVIL",1]`,
		categoryBinds:  hostile,
	})
	rendered, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("hostile category render: %v", err)
	}
	sql := rendered.SQL
	for _, forbidden := range []string{"a\"b'", "EVIL", "--", "$$"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("hostile data leaked into SQL text (%q):\n%s", forbidden, sql)
		}
	}
	carried := 0
	for _, parameter := range rendered.Args {
		text := fmt.Sprintf("%s", parameter.Value)
		if strings.Contains(text, "EVIL") || strings.Contains(text, "$$") {
			carried++
		}
	}
	if carried < 2 {
		t.Fatalf("hostile construction ID/category must ride two parameters, got %d:\n%v", carried, rendered.Args)
	}
}

func TestPivot14SpinePathUntouched(t *testing.T) {
	if sql := pivot14Render(t, pivot14ScalarPlan(t, pivot14ScalarConfig{})); sql == "" {
		t.Fatal("canonical pivot rendered empty")
	}
	nonCoded := pivot14CompileBundle(t, pivot14NonCodedPivotBundle(t, "p14_spine_noncoded"), []string{"FHIR/Observation"})
	if nonCoded.StageSequence == nil || nonCoded.StageSequence.Stages[0].GroupedPivot == nil ||
		nonCoded.StageSequence.Stages[0].GroupedPivot.CodedCorrelation != nil {
		t.Fatalf("non-coded construction pivot shape drifted: %+v", nonCoded.StageSequence)
	}
	rendered, err := RenderPhysicalPlan(nonCoded, mustBinding(t))
	if err != nil {
		t.Fatalf("staged non-coded pivot must render through the composition door (G4): %v", err)
	}
	if !strings.Contains(rendered.SQL, "WITH ") || !strings.Contains(rendered.SQL, "__loom_") {
		t.Fatalf("staged non-coded pivot did not render a composed CTE chain:\n%s", rendered.SQL)
	}
	windowed := basePlan()
	windowed.PreviewSourceWindowByRootID = true
	if _, err := RenderPhysicalPlan(windowed, mustBinding(t)); err == nil ||
		!strings.Contains(err.Error(), "preview source windowing is not yet implemented for PostgreSQL") {
		t.Fatalf("plan-level window refusal drifted: %v", err)
	}
}

func TestPivot14ScalarRefusalRegister(t *testing.T) {
	cases := []struct {
		name         string
		mutate       func(*ir.PhysicalPlan)
		wantFragment string
	}{
		{"stage sequence coexistence refused by the group door", func(p *ir.PhysicalPlan) {
			p.StageSequence = &ir.PhysicalStageSequence{Stages: []ir.PhysicalConstructionStage{{Kind: ir.PhysicalStageGroupOp}}}
		}, "registered group slice"},
		{"stage sequence coexistence refused by the pivot slice", func(p *ir.PhysicalPlan) {
			p.StageSequence = &ir.PhysicalStageSequence{Stages: []ir.PhysicalConstructionStage{{Kind: ir.PhysicalStageFilterOp}}}
		}, "stage sequence"},
		{"plan-level preview window refused at the door", func(p *ir.PhysicalPlan) {
			p.PreviewSourceWindowByRootID = true
		}, "pg-sql-16"},
		{"second root scan", func(p *ir.PhysicalPlan) {
			p.Operations = append(p.Operations[:1],
				ir.PhysicalOperation{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
				p.Operations[1])
		}, "ROOT_SCAN"},
		{"traversal coexistence", func(p *ir.PhysicalPlan) {
			p.Operations = append(p.Operations[:1],
				ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{
					SourceVariable: "root", TargetVariable: "t", Direction: ir.PhysicalOutbound, EdgeLabelBindKey: "edge"}},
				p.Operations[1])
		}, "cannot coexist with GROUPED_PIVOT"},
		{"filter after pivot", func(p *ir.PhysicalPlan) {
			p.Operations = append(p.Operations[:len(p.Operations)-1],
				ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "IS_NULL"}}},
				p.Operations[len(p.Operations)-1])
		}, "FILTER must precede the pivot"},
		{"second pivot", func(p *ir.PhysicalPlan) {
			clone := *p.Operations[1].GroupedPivot
			p.Operations = append(p.Operations[:2],
				ir.PhysicalOperation{Kind: ir.PhysicalGroupedPivotOp, GroupedPivot: &clone},
				p.Operations[2])
		}, "exactly one reshape"},
		{"window sort drift", func(p *ir.PhysicalPlan) {
			p.Operations = append(p.Operations[:2],
				ir.PhysicalOperation{Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{
					Keys: []ir.PhysicalValue{{Variable: "prow", Path: []string{"other"}}}}},
				p.Operations[2])
		}, "must sort on prow.__loom_row_id exactly"},
		{"limit without sort", func(p *ir.PhysicalPlan) {
			p.Operations = append(p.Operations[:2],
				ir.PhysicalOperation{Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: "limit"}},
				p.Operations[2])
		}, "requires the stable row identity SORT"},
		{"non-positive limit", func(p *ir.PhysicalPlan) {
			p.Operations = append(p.Operations[:2],
				ir.PhysicalOperation{Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{
					Keys: []ir.PhysicalValue{{Variable: "prow", Path: []string{"__loom_row_id"}}}}},
				ir.PhysicalOperation{Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: "bad_limit"}},
				p.Operations[2])
			p.BindVars["bad_limit"] = 0
		}, "positive int"},
		{"missing leading ROOT_SCAN", func(p *ir.PhysicalPlan) {
			p.Operations = p.Operations[1:]
		}, "ROOT_SCAN"},
		{"ROOT_SCAN not first", func(p *ir.PhysicalPlan) {
			p.Operations = append([]ir.PhysicalOperation{{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{
				Predicate: ir.PhysicalPredicate{Operator: "IS_NULL"}}}}, p.Operations...)
		}, "ROOT_SCAN"},
		{"population root", func(p *ir.PhysicalPlan) {
			p.Operations[0].RootScan.Population = &ir.PhysicalPopulationRootSource{}
		}, "population"},
		{"coded fields on the spine op", func(p *ir.PhysicalPlan) {
			p.Operations[1].GroupedPivot.CodedCorrelation = &ir.PhysicalCorrelation{}
		}, "coded Pivot fields"},
		{"row values", func(p *ir.PhysicalPlan) {
			p.Operations[1].GroupedPivot.RowValues = []ir.PhysicalStageRowValue{{InputColumn: "gcol", Output: "rv", Variable: "rv0", Policy: "ALL", InputKind: "STRING"}}
		}, "pg-sql-13"},
		{"root contributor columns", func(p *ir.PhysicalPlan) {
			p.Operations[1].GroupedPivot.RootContributorVariable = "rc"
		}, "root-contributor"},
		{"unregistered duplicate policy", func(p *ir.PhysicalPlan) {
			p.Operations[1].GroupedPivot.DuplicatePolicy = "MEAN"
		}, "duplicate policy"},
		{"IGNORE unlisted on the ordinary pivot", func(p *ir.PhysicalPlan) {
			p.Operations[1].GroupedPivot.UnlistedCategoryPolicy = "IGNORE"
			p.Operations[1].GroupedPivot.UnlistedEvidenceColumn = ""
		}, "IGNORE is coded-only"},
		{"category kind disagreement", func(p *ir.PhysicalPlan) {
			p.Operations[1].GroupedPivot.Categories[0].ValueKind = "INTEGER"
		}, "disagrees with the frozen category type"},
		{"category bind wrong type", func(p *ir.PhysicalPlan) {
			p.BindVars["cat_alpha"] = 7
		}, "must be a string"},
		{"missing category value bind", func(p *ir.PhysicalPlan) {
			delete(p.BindVars, "cat_alpha")
		}, "missing"},
		{"evidence column without EXCLUDE", func(p *ir.PhysicalPlan) {
			p.Operations[1].GroupedPivot.UnlistedEvidenceColumn = "evidence_x"
		}, "EXCLUDE_WITH_EVIDENCE"},
		{"consumed projection missing", func(p *ir.PhysicalPlan) {
			p.Operations[1].GroupedPivot.InputProjections = p.Operations[1].GroupedPivot.InputProjections[:1]
		}, "no input projection carries"},
		{"consumed projection twice", func(p *ir.PhysicalPlan) {
			dup := p.Operations[1].GroupedPivot.InputProjections[1]
			p.Operations[1].GroupedPivot.InputProjections = append(p.Operations[1].GroupedPivot.InputProjections, dup)
		}, "projected twice"},
		{"return outside the registered names", func(p *ir.PhysicalPlan) {
			returnOp := p.Operations[len(p.Operations)-1]
			returnOp.Return.Projections = append(returnOp.Return.Projections, ir.PhysicalProjection{
				Name: "not_a_pivot_output", Value: ir.PhysicalValue{Variable: "prow", Path: []string{"not_a_pivot_output"}}})
			p.Operations[len(p.Operations)-1] = returnOp
		}, "not a registered pivot output"},
		{"return path drift", func(p *ir.PhysicalPlan) {
			returnOp := p.Operations[len(p.Operations)-1]
			returnOp.Return.Projections[0].Value.Path = []string{"drifted"}
			p.Operations[len(p.Operations)-1] = returnOp
		}, "single-name read"},
		{"hostile category type", func(p *ir.PhysicalPlan) {
			p.Operations[1].GroupedPivot.ValueType = "OBJECT"
		}, "table scalar kind"},
		{"reducer on non-numeric value type", func(p *ir.PhysicalPlan) {
			p.Operations[1].GroupedPivot.DuplicatePolicy = "SUM"
			p.Operations[1].GroupedPivot.ValueType = "STRING"
		}, "numeric value column"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			control := pivot14ScalarPlan(t, pivot14ScalarConfig{})
			if _, err := RenderPhysicalPlan(control, mustBinding(t)); err != nil {
				t.Fatalf("ALIVE control refused before any mutation: %v", err)
			}
			plan := pivot14ScalarPlan(t, pivot14ScalarConfig{})
			test.mutate(&plan)
			pivot14Refused(t, plan, test.name, test.wantFragment)
		})
	}
}

func TestPivot14ProductionFilterRendersAtAQLDivergence(t *testing.T) {
	control := pivot14CompileBundle(t, pivot14ScalarBundle("p14_filter_control", nil,
		recipe.PivotDuplicateSum, recipe.PivotMissingCellNull, recipe.PivotUnlistedCategoryError), []string{pivot14AuthPath})
	if _, err := RenderPhysicalPlan(control, mustBinding(t)); err != nil {
		t.Fatalf("ALIVE control (no filter) refused: %v", err)
	}
	plan := pivot14CompileBundle(t, pivot14ScalarBundle("p14_filter_divergence", pivot14FemaleFilter(),
		recipe.PivotDuplicateSum, recipe.PivotMissingCellNull, recipe.PivotUnlistedCategoryError), []string{pivot14AuthPath})
	_, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err == nil {
		t.Fatal("authored FILTER must refuse (probe P-14.2 renders-at-AQL divergence)")
	}
	if !errors.Is(err, ErrUnsupportedPostgresPlan) || !strings.Contains(err.Error(), "pg-sql-16") {
		t.Fatalf("FILTER divergence refusal drifted: %v", err)
	}
}

var (
	pivot14FixturesMu      sync.Mutex
	pivot14TrackedFixtures = map[string]bool{}
)

func pivot14LiveService(t *testing.T) *pgir02Service {
	t.Helper()
	svc := newOwnedService(t, 0, "verify_sql14v1")
	var database string
	if err := svc.iso.QueryRow(svc.ctx, "SELECT current_database()").Scan(&database); err != nil {
		svc.fail("track owned database", err)
	}
	pivot14FixturesMu.Lock()
	defer pivot14FixturesMu.Unlock()
	pivot14TrackedFixtures[database] = true
	pivot14TrackedFixtures[svc.schema] = true
	return svc
}

func pivot14Binding(t *testing.T, svc *pgir02Service) ir.PostgresStorageBinding {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding(svc.schema)
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	return binding
}

func pivot14Seed(t *testing.T, svc *pgir02Service, logicalID string, payload any) {
	t.Helper()
	svc.insertResource(t, svc.schema, pivot14Project, pivot14Generation, "Patient", logicalID, pivot14AuthPath, payload)
}

func pivot14SnapshotOwnedResources(ctx context.Context, raw string) ([]string, error) {
	pool, err := pgxpool.New(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()
	rows, err := pool.Query(ctx,
		`SELECT datname FROM pg_database WHERE datname LIKE 'verify_sql14v1_%'`+
			` UNION ALL SELECT nspname FROM pg_namespace WHERE nspname LIKE 'verify_sql14v1_%'`)
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

func pivot14RunDeltaAudit(inner func() int) int {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		return inner()
	}
	beforeList, err := pivot14SnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-14 delta audit: snapshot before run: %v\n", err)
		return inner()
	}
	if len(beforeList) > 0 {
		fmt.Fprintf(os.Stderr, "pg-sql-14 delta audit: pre-snapshot found %d surviving verify_sql14v1_* resources before this run: %v\n", len(beforeList), beforeList)
		return 1
	}
	before := make(map[string]bool, len(beforeList))
	for _, name := range beforeList {
		before[name] = true
	}
	code := inner()
	after, err := pivot14SnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-14 delta audit: snapshot after run: %v\n", err)
		code = 1
	}
	pivot14FixturesMu.Lock()
	defer pivot14FixturesMu.Unlock()
	for _, name := range after {
		if before[name] {
			continue
		}
		if pivot14TrackedFixtures[name] {
			fmt.Fprintf(os.Stderr, "pg-sql-14 delta audit: %q was created and tracked by this run and still survives\n", name)
			code = 1
			continue
		}
		fmt.Fprintf(os.Stderr, "pg-sql-14 delta audit: note — %q was born during this run but is not owned by it (sibling lane?)\n", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	survivors, err := pivot14SnapshotOwnedResources(ctx, raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-14 delta audit: survivor scan: %v\n", err)
		code = 1
	} else if len(survivors) > 0 {
		fmt.Fprintf(os.Stderr, "pg-sql-14 delta audit: %d verify_sql14v1_* resources survive: %v\n", len(survivors), survivors)
		code = 1
	}
	return code
}

func TestZZNoLeftoverPivot14Resources(t *testing.T) {
	if err := auditNoLeftoverVerifyNamespace(pivot14Prefix); err != nil {
		t.Fatalf("zero-leftover census: %v", err)
	}
}

func pivot14SeedOracle(t *testing.T, svc *pgir02Service) {
	t.Helper()
	pivot14Seed(t, svc, "o1", json.RawMessage(`{"gender":"female","multipleBirthInteger":0}`))
	pivot14Seed(t, svc, "o2", json.RawMessage(`{"gender":"female","multipleBirthInteger":1}`))
	pivot14Seed(t, svc, "o3", json.RawMessage(`{"gender":"female","multipleBirthInteger":0}`))
	pivot14Seed(t, svc, "o4", json.RawMessage(`{"gender":"female","multipleBirthInteger":2}`))
	pivot14Seed(t, svc, "o5", json.RawMessage(`{"gender":"female"}`))
	pivot14Seed(t, svc, "o6", json.RawMessage(`{"gender":"female","multipleBirthInteger":null}`))
	pivot14Seed(t, svc, "o7", json.RawMessage(`{"gender":"male","multipleBirthInteger":1}`))
	for _, state := range []struct{ id, payload string }{
		{"o4", `{"gender":"female","multipleBirthInteger":2}`},
		{"o5", `{"gender":"female"}`},
		{"o6", `{"gender":"female","multipleBirthInteger":null}`},
	} {
		rows := svc.witness(t, fmt.Sprintf(
			`SELECT logical_id FROM %s.fhir_resource WHERE logical_id = $1 AND project = $2 AND dataset_generation = $3 AND payload = $4::jsonb`,
			`"`+svc.schema+`"`), state.id, pivot14Project, pivot14Generation, state.payload)
		if len(rows) != 1 {
			t.Fatalf("stored state %s is not exactly %s — the fixture must not be vacuous", state.id, state.payload)
		}
	}
}

func TestPivot14LiveScalarOracleMatrix(t *testing.T) {
	svc := pivot14LiveService(t)
	pivot14SeedOracle(t, svc)

	plan := pivot14CompileBundle(t, pivot14ScalarBundle("p14_live_sum", nil,
		recipe.PivotDuplicateSum, recipe.PivotMissingCellNull, recipe.PivotUnlistedCategoryExcludeWithEvidence),
		[]string{pivot14AuthPath})
	rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render live SUM plan: %v", err)
	}
	rows := svc.runRendered(t, rendered)
	if len(rows) != 2 {
		t.Fatalf("live rows = %v, want the female and male groups", rows)
	}
	var groupIndex int
	for index, name := range rendered.OutputColumns {
		if name == "grp" {
			groupIndex = index
		}
	}
	byGroup := map[string][]string{}
	for _, row := range rows {
		byGroup[row[groupIndex]] = row
	}
	if _, ok := byGroup["female"]; !ok {
		t.Fatalf("female group missing (identity/group key drifted): %v", rows)
	}
	var birthZero, birthOne int
	for index, name := range rendered.OutputColumns {
		switch name {
		case "birth_zero":
			birthZero = index
		case "birth_one":
			birthOne = index
		}
	}
	if got := byGroup["female"][birthZero]; got != "0" {
		t.Fatalf("SUM over the two category-0 rows = %q, want 0", got)
	}
	if got := byGroup["female"][birthOne]; got != "1" {
		t.Fatalf("SUM over the single category-1 row = %q, want 1", got)
	}
	if got := byGroup["male"][birthZero]; got != "<null>" && got != "null" {
		t.Fatalf("male group has no category-0 rows; the cell must read JSON null, got %q", got)
	}
}

func TestPivot14LiveScalarReducerMissingRawCount(t *testing.T) {
	svc := pivot14LiveService(t)
	pivot14SeedOracle(t, svc)
	plan := pivot14CompileBundle(t, pivot14ScalarBundle("p14_live_dup", nil,
		recipe.PivotDuplicateError, recipe.PivotMissingCellNull, recipe.PivotUnlistedCategoryExcludeWithEvidence),
		[]string{pivot14AuthPath})
	rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render live duplicate plan: %v", err)
	}
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
			t.Fatalf("duplicate category-0 rows must raise under "+mode+", got success\n%s", rendered.SQL)
		}
		if !strings.Contains(err.Error(), "TABLE_PIVOT_CELL_CARDINALITY") || !strings.Contains(err.Error(), "22P02") {
			t.Fatalf("duplicate raise drifted under %s: %v", mode, err)
		}
	}
}

func TestPivot14LiveScalarMissingRawCountNoRaise(t *testing.T) {
	svc := pivot14LiveService(t)
	pivot14Seed(t, svc, "s1", json.RawMessage(`{"gProp":"g1","cProp":"alpha","vProp":null}`))
	plan := pivot14ScalarPlan(t, pivot14ScalarConfig{
		duplicate: "SUM", missing: "ERROR", unlisted: "EXCLUDE_WITH_EVIDENCE",
		categories: []ir.PhysicalGroupedPivotCategory{
			{Output: "cell_alpha", MatchKind: ir.PhysicalPivotCategoryValueMatch, ValueBindKey: "cat_alpha", ValueKind: "STRING"},
		},
	})
	rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render scalar missing control: %v", err)
	}
	rows := svc.runRendered(t, rendered)
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want the single group", rows)
	}
	if rows[0][1] != "<null>" && rows[0][1] != "null" {
		t.Fatalf("raw>0/typed=0 must read the null cell without raising, got %v", rows[0])
	}

	missingSvc := pivot14LiveService(t)
	pivot14Seed(t, missingSvc, "s2", json.RawMessage(`{"gProp":"g2","cProp":"beta","vProp":1}`))
	missingPlan := pivot14ScalarPlan(t, pivot14ScalarConfig{
		duplicate: "SUM", missing: "ERROR", unlisted: "EXCLUDE_WITH_EVIDENCE",
		categories: []ir.PhysicalGroupedPivotCategory{
			{Output: "cell_alpha", MatchKind: ir.PhysicalPivotCategoryValueMatch, ValueBindKey: "cat_alpha", ValueKind: "STRING"},
		},
	})
	missingRendered, err := RenderPhysicalPlan(missingPlan, pivot14Binding(t, missingSvc))
	if err != nil {
		t.Fatalf("render scalar missing raise: %v", err)
	}
	_, err = missingSvc.iso.Exec(missingSvc.ctx, missingRendered.SQL, pivot14Args(missingRendered)...)
	if err == nil {
		t.Fatalf("raw count 0 must raise the scalar MISSING guard (:230):\n%s", missingRendered.SQL)
	}
	if !strings.Contains(err.Error(), "TABLE_PIVOT_CELL_MISSING") || !strings.Contains(err.Error(), "22P02") {
		t.Fatalf("scalar MISSING raise drifted: %v", err)
	}
}

func TestPivot14LiveWindowOrderAndLimit(t *testing.T) {
	svc := pivot14LiveService(t)
	pivot14Seed(t, svc, "w1", json.RawMessage(`{"gProp":"g1","cProp":"alpha","vProp":1}`))
	pivot14Seed(t, svc, "w2", json.RawMessage(`{"gProp":"g1","cProp":null,"vProp":2}`))
	pivot14Seed(t, svc, "w3", json.RawMessage(`{"gProp":"g1","vProp":3}`))
	pivot14Seed(t, svc, "w4", json.RawMessage(`{"gProp":"G2","cProp":"alpha","vProp":10}`))

	plan := pivot14ScalarPlan(t, pivot14ScalarConfig{duplicate: "ERROR", window: true, presence: "contract"})
	rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render windowed plan live: %v", err)
	}
	rows := svc.runRendered(t, rendered)
	if len(rows) != 2 {
		t.Fatalf("windowed rows = %v, want both groups under LIMIT 2", rows)
	}
	groupIndex := -1
	for index, name := range rendered.OutputColumns {
		if name == "grp" {
			groupIndex = index
		}
	}
	if groupIndex < 0 {
		t.Fatalf("group column missing from output columns: %v", rendered.OutputColumns)
	}
	if rows[0][groupIndex] != "G2" || rows[1][groupIndex] != "g1" {
		t.Fatalf("identity COLLATE \"C\" order drifted (byte order puts G2 first): %v", rows)
	}
	cellIndex := map[string]int{}
	for index, name := range rendered.OutputColumns {
		cellIndex[name] = index
	}
	if got := rows[1][cellIndex["cell_alpha"]]; got != "1" {
		t.Fatalf("g1 alpha cell = %q, want 1", got)
	}
	if got := rows[1][cellIndex["cell_null"]]; got != "2" {
		t.Fatalf("g1 null cell = %q, want 2", got)
	}
	if got := rows[1][cellIndex["cell_missing"]]; got != "3" {
		t.Fatalf("g1 missing cell = %q, want 3 (the presence contract proves cProp per row; the producer always carries it for MISSING kinds, ir/physical_table_shape.go re-read)", got)
	}

	limited := pivot14ScalarPlan(t, pivot14ScalarConfig{duplicate: "ERROR", window: true, presence: "contract"})
	limited.BindVars["limit"] = 1
	limitedRendered, err := RenderPhysicalPlan(limited, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render limited plan live: %v", err)
	}
	limitedRows := svc.runRendered(t, limitedRendered)
	if len(limitedRows) != 1 || limitedRows[0][groupIndex] != "G2" {
		t.Fatalf("LIMIT prefix drifted: %v", limitedRows)
	}
}

func pivot14Args(rendered command.RenderedPostgresPhysicalPlan) []any {
	args := make([]any, 0, len(rendered.Args))
	for _, parameter := range rendered.Args {
		args = append(args, parameter.Value)
	}
	return args
}

func specSelector(t *testing.T, fields ...string) spec.Selector {
	t.Helper()
	steps := make([]spec.SelectorStep, 0, len(fields))
	for _, field := range fields {
		steps = append(steps, spec.SelectorStep{Field: field})
	}
	return spec.Selector{Steps: steps}
}

var _ = sort.Strings

func pivot14ColIndex(t *testing.T, rendered command.RenderedPostgresPhysicalPlan, name string) int {
	t.Helper()
	for index, candidate := range rendered.OutputColumns {
		if candidate == name {
			return index
		}
	}
	t.Fatalf("output column %q missing: %v", name, rendered.OutputColumns)
	return -1
}

func pivot14ExpectGuardRaise(t *testing.T, svc *pgir02Service, rendered command.RenderedPostgresPhysicalPlan, wantTag string) {
	t.Helper()
	const sentinel = "PIVOT14FOLD42"
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
		if _, err := conn.Exec(svc.ctx, "SET loom.explorer_guard_barrier = "+quoteSQLLiteral(sentinel)); err != nil {
			conn.Release()
			svc.fail("set guard barrier under "+mode, err)
		}
		_, err = conn.Exec(svc.ctx, rendered.SQL, pivot14Args(rendered)...)
		if _, resetErr := conn.Exec(svc.ctx, "RESET loom.explorer_guard_barrier"); resetErr != nil {
			conn.Release()
			svc.fail("reset guard barrier", resetErr)
		}
		conn.Release()
		if err == nil {
			t.Fatalf("guard %s must raise under %s:\n%s", wantTag, mode, rendered.SQL)
		}
		if message := err.Error(); !strings.Contains(message, "22P02") ||
			!strings.Contains(message, wantTag) || !strings.Contains(message, sentinel) {
			t.Fatalf("guard %s raise drifted under %s: %v", wantTag, mode, err)
		}
	}
}

func pivot14CellIsNull(value string) bool {
	return value == "<null>" || value == "null"
}

func TestPivot14ProductionScalarLive(t *testing.T) {
	svc := pivot14LiveService(t)
	pivot14Seed(t, svc, "s1", json.RawMessage(`{"gender":"female","multipleBirthInteger":0}`))
	pivot14Seed(t, svc, "s2", json.RawMessage(`{"gender":"female","multipleBirthInteger":0}`))
	pivot14Seed(t, svc, "s3", json.RawMessage(`{"gender":"female","multipleBirthInteger":1}`))
	pivot14Seed(t, svc, "s4", json.RawMessage(`{"gender":"female","multipleBirthInteger":1}`))
	pivot14Seed(t, svc, "s5", json.RawMessage(`{"gender":"female","multipleBirthInteger":null}`))
	pivot14Seed(t, svc, "s6", json.RawMessage(`{"gender":"female"}`))
	pivot14Seed(t, svc, "s7", json.RawMessage(`{"gender":"female","multipleBirthInteger":2}`))
	pivot14Seed(t, svc, "s8", json.RawMessage(`{"gender":"male","multipleBirthInteger":1}`))

	cases := []struct {
		name      string
		duplicate recipe.PivotDuplicatePolicy
		birthZero string
		birthOne  string
	}{
		{"sum", recipe.PivotDuplicateSum, "0", "2"},
		{"min", recipe.PivotDuplicateMin, "0", "1"},
		{"max", recipe.PivotDuplicateMax, "0", "1"},
	}
	for _, testCase := range cases {
		bundle := pivot14ScalarBundle("p14_prod_scalar_"+testCase.name, nil,
			testCase.duplicate, recipe.PivotMissingCellNull, recipe.PivotUnlistedCategoryExcludeWithEvidence)
		plan := pivot14CompileBundle(t, bundle, []string{pivot14AuthPath})
		rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
		if err != nil {
			t.Fatalf("render %s plan: %v", testCase.name, err)
		}
		wantColumns := []string{"grp", "birth_zero", "birth_one", "__loom_reshape_unlisted_count", "__loom_row_id"}
		if len(rendered.OutputColumns) != len(wantColumns) {
			t.Fatalf("%s output columns = %v, want %v", testCase.name, rendered.OutputColumns, wantColumns)
		}
		for index, name := range wantColumns {
			if rendered.OutputColumns[index] != name {
				t.Fatalf("%s output column %d = %q, want %q", testCase.name, index, rendered.OutputColumns[index], name)
			}
		}
		rows := svc.runRendered(t, rendered)
		if len(rows) != 2 {
			t.Fatalf("%s rows = %v, want the female and male groups", testCase.name, rows)
		}
		byGroup := map[string][]string{}
		for _, row := range rows {
			byGroup[row[pivot14ColIndex(t, rendered, "grp")]] = row
		}
		female, ok := byGroup["female"]
		if !ok {
			t.Fatalf("%s female group missing: %v", testCase.name, rows)
		}
		if got := female[pivot14ColIndex(t, rendered, "birth_zero")]; got != testCase.birthZero {
			t.Fatalf("%s female birth_zero = %q, want %s", testCase.name, got, testCase.birthZero)
		}
		if got := female[pivot14ColIndex(t, rendered, "birth_one")]; got != testCase.birthOne {
			t.Fatalf("%s female birth_one = %q, want %s", testCase.name, got, testCase.birthOne)
		}
		if got := female[pivot14ColIndex(t, rendered, "__loom_reshape_unlisted_count")]; got != "3" {
			t.Fatalf("%s female unlisted evidence = %q, want 3", testCase.name, got)
		}
		male, ok := byGroup["male"]
		if !ok {
			t.Fatalf("%s male group missing: %v", testCase.name, rows)
		}
		if got := male[pivot14ColIndex(t, rendered, "birth_one")]; got != "1" {
			t.Fatalf("%s male birth_one = %q, want 1", testCase.name, got)
		}
		if got := male[pivot14ColIndex(t, rendered, "birth_zero")]; !pivot14CellIsNull(got) {
			t.Fatalf("%s male birth_zero = %q, want the null cell", testCase.name, got)
		}
		if got := male[pivot14ColIndex(t, rendered, "__loom_reshape_unlisted_count")]; got != "0" {
			t.Fatalf("%s male unlisted evidence = %q, want 0", testCase.name, got)
		}
		for _, row := range rows {
			group := row[pivot14ColIndex(t, rendered, "grp")]
			want := `["GROUPED_PIVOT","p14_prod_scalar_` + testCase.name + `",["STRING","` + group + `"]]`
			if got := row[pivot14ColIndex(t, rendered, "__loom_row_id")]; got != want {
				t.Fatalf("%s identity = %q, want %s", testCase.name, got, want)
			}
		}

		foreign := pivot14CompileBundle(t, bundle, []string{"FHIR/Encounter"})
		foreignRendered, err := RenderPhysicalPlan(foreign, pivot14Binding(t, svc))
		if err != nil {
			t.Fatalf("render foreign-auth %s plan: %v", testCase.name, err)
		}
		if rows := svc.runRendered(t, foreignRendered); len(rows) != 0 {
			t.Fatalf("%s foreign auth must yield zero rows, got %v", testCase.name, rows)
		}
	}

	bundle := pivot14ScalarBundle("p14_prod_scalar_window", nil,
		recipe.PivotDuplicateSum, recipe.PivotMissingCellNull, recipe.PivotUnlistedCategoryExcludeWithEvidence)
	plan := pivot14CompileBundle(t, bundle, []string{pivot14AuthPath})
	outputRowVariable := ""
	for _, operation := range plan.Operations {
		if operation.GroupedPivot != nil {
			outputRowVariable = operation.GroupedPivot.OutputRowVariable
		}
	}
	if outputRowVariable == "" {
		t.Fatalf("production plan lost its GROUPED_PIVOT operation")
	}
	operations := make([]ir.PhysicalOperation, 0, len(plan.Operations)+2)
	returnIndex := len(plan.Operations) - 1
	operations = append(operations, plan.Operations[:returnIndex]...)
	operations = append(operations,
		ir.PhysicalOperation{Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{
			Keys: []ir.PhysicalValue{{Variable: outputRowVariable,
				Path: []string{"__loom_row_id"}}}}},
		ir.PhysicalOperation{Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: "p14_prod_limit"}})
	operations = append(operations, plan.Operations[returnIndex])
	plan.Operations = operations
	plan.BindVars["p14_prod_limit"] = 1
	windowRendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render windowed production plan: %v", err)
	}
	windowRows := svc.runRendered(t, windowRendered)
	if len(windowRows) != 1 || windowRows[0][pivot14ColIndex(t, windowRendered, "grp")] != "female" {
		t.Fatalf("production LIMIT prefix drifted: %v", windowRows)
	}
}

func TestPivot14LiveValueTypeRaise(t *testing.T) {
	for _, value := range []string{"abc", "12.5"} {
		t.Run("value_"+value, func(t *testing.T) {
			svc := pivot14LiveService(t)
			pivot14Seed(t, svc, "vt1", json.RawMessage(`{"gProp":"g1","cProp":"alpha","vProp":"`+value+`"}`))
			plan := pivot14ScalarPlan(t, pivot14ScalarConfig{duplicate: "SUM", missing: "NULL"})
			rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
			if err != nil {
				t.Fatalf("render value-type plan: %v", err)
			}
			pivot14ExpectGuardRaise(t, svc, rendered, pivotValueTypeTag)
		})
	}

	svc := pivot14LiveService(t)
	pivot14Seed(t, svc, "vt0", json.RawMessage(`{"gProp":"g1","cProp":"alpha","vProp":4}`))
	clean := pivot14ScalarPlan(t, pivot14ScalarConfig{duplicate: "SUM", missing: "NULL"})
	cleanRendered, err := RenderPhysicalPlan(clean, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render clean value-type twin: %v", err)
	}
	rows := svc.runRendered(t, cleanRendered)
	if len(rows) != 1 || rows[0][pivot14ColIndex(t, cleanRendered, "cell_alpha")] != "4" {
		t.Fatalf("clean numeric data must read SUM 4 without a raise, got %v", rows)
	}
}

func TestPivot14LiveErrorBranchRawCountParity(t *testing.T) {
	svc := pivot14LiveService(t)
	pivot14Seed(t, svc, "eb1", json.RawMessage(`{"gProp":"g1","cProp":"alpha","vProp":null}`))
	pivot14Seed(t, svc, "eb2", json.RawMessage(`{"gProp":"g1","cProp":"beta","vProp":7}`))
	pivot14Seed(t, svc, "eb3", json.RawMessage(`{"gProp":"g1","cProp":null,"vProp":4}`))
	plan := pivot14ScalarPlan(t, pivot14ScalarConfig{duplicate: "ERROR", missing: "ERROR"})
	rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render ERROR-branch raw-count parity plan: %v", err)
	}
	rows := svc.runRendered(t, rendered)
	if len(rows) != 1 {
		t.Fatalf("parity rows = %v, want the single g1 group", rows)
	}
	row := rows[0]
	if got := row[pivot14ColIndex(t, rendered, "cell_alpha")]; !pivot14CellIsNull(got) {
		t.Fatalf("alpha cell = %q, want JSON null (rawCount=1, null-preserving FIRST over the raw list — no CELL_MISSING raise, no cell collapse)", got)
	}
	if got := row[pivot14ColIndex(t, rendered, "cell_beta")]; got != "7" {
		t.Fatalf("beta cell = %q, want 7 (typed row reads normally)", got)
	}
	if got := row[pivot14ColIndex(t, rendered, "cell_null")]; got != "4" {
		t.Fatalf("null-kind cell = %q, want 4", got)
	}
}

func TestPivot14LiveErrorBranchRawCountCardinalityAlive(t *testing.T) {
	svc := pivot14LiveService(t)
	pivot14Seed(t, svc, "ec1", json.RawMessage(`{"gProp":"g1","cProp":"alpha","vProp":3}`))
	pivot14Seed(t, svc, "ec2", json.RawMessage(`{"gProp":"g1","cProp":"alpha","vProp":null}`))
	plan := pivot14ScalarPlan(t, pivot14ScalarConfig{duplicate: "ERROR", missing: "ERROR"})
	rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render ERROR-branch cardinality ALIVE plan: %v", err)
	}
	pivot14ExpectGuardRaise(t, svc, rendered, pivotCardinalityTag)
}

func TestPivot14LiveDuplicateCardinalityRaise(t *testing.T) {
	svc := pivot14LiveService(t)
	pivot14Seed(t, svc, "dc1", json.RawMessage(`{"gProp":"g1","cProp":"alpha","vProp":3}`))
	pivot14Seed(t, svc, "dc2", json.RawMessage(`{"gProp":"g1","cProp":"alpha","vProp":4}`))
	plan := pivot14ScalarPlan(t, pivot14ScalarConfig{duplicate: "ERROR", missing: "NULL"})
	rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render cardinality plan: %v", err)
	}
	pivot14ExpectGuardRaise(t, svc, rendered, pivotCardinalityTag)
}

func TestPivot14LiveMissingCellRaise(t *testing.T) {
	svc := pivot14LiveService(t)
	pivot14Seed(t, svc, "mc1", json.RawMessage(`{"gProp":"g1","cProp":"beta","vProp":5}`))
	plan := pivot14ScalarPlan(t, pivot14ScalarConfig{duplicate: "SUM", missing: "ERROR"})
	rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render missing plan: %v", err)
	}
	pivot14ExpectGuardRaise(t, svc, rendered, pivotCellMissingTag)
}

func TestPivot14LiveUnlistedRaise(t *testing.T) {
	svc := pivot14LiveService(t)
	pivot14Seed(t, svc, "ul1", json.RawMessage(`{"gProp":"g1","cProp":"zeta","vProp":5}`))
	plan := pivot14ScalarPlan(t, pivot14ScalarConfig{duplicate: "SUM", missing: "NULL", unlisted: "ERROR"})
	rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render unlisted plan: %v", err)
	}
	pivot14ExpectGuardRaise(t, svc, rendered, pivotUnlistedTag)
}

func TestPivot14LiveUnlistedEvidenceRowsExact(t *testing.T) {
	svc := pivot14LiveService(t)
	pivot14Seed(t, svc, "ev1", json.RawMessage(`{"gProp":"g1","cProp":"alpha","vProp":1}`))
	pivot14Seed(t, svc, "ev2", json.RawMessage(`{"gProp":"g1","cProp":"beta","vProp":2}`))
	pivot14Seed(t, svc, "ev3", json.RawMessage(`{"gProp":"g1","cProp":"zeta","vProp":9}`))
	pivot14Seed(t, svc, "ev4", json.RawMessage(`{"gProp":"g2","cProp":"alpha","vProp":7}`))
	plan := pivot14ScalarPlan(t, pivot14ScalarConfig{duplicate: "SUM", missing: "NULL", unlisted: "EXCLUDE_WITH_EVIDENCE"})
	rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render evidence plan: %v", err)
	}
	rows := svc.runRendered(t, rendered)
	if len(rows) != 2 {
		t.Fatalf("evidence rows = %v", rows)
	}
	byGroup := map[string][]string{}
	for _, row := range rows {
		byGroup[row[pivot14ColIndex(t, rendered, "grp")]] = row
	}
	g1, ok := byGroup["g1"]
	if !ok {
		t.Fatalf("g1 group missing: %v", rows)
	}
	if got := g1[pivot14ColIndex(t, rendered, "cell_alpha")]; got != "1" {
		t.Fatalf("g1 alpha cell = %q, want 1", got)
	}
	if got := g1[pivot14ColIndex(t, rendered, "cell_beta")]; got != "2" {
		t.Fatalf("g1 beta cell = %q, want 2", got)
	}
	if got := g1[pivot14ColIndex(t, rendered, "unlisted_n")]; got != "1" {
		t.Fatalf("g1 evidence = %q, want the kept unlisted count 1", got)
	}
	g2, ok := byGroup["g2"]
	if !ok {
		t.Fatalf("g2 group missing: %v", rows)
	}
	if got := g2[pivot14ColIndex(t, rendered, "unlisted_n")]; got != "0" {
		t.Fatalf("g2 evidence = %q, want 0", got)
	}
	if got := g2[pivot14ColIndex(t, rendered, "cell_beta")]; !pivot14CellIsNull(got) {
		t.Fatalf("g2 beta cell = %q, want the null cell", got)
	}
}

func TestPivot14LiveAbsentCompanionMissingParity(t *testing.T) {
	svc := pivot14LiveService(t)
	pivot14Seed(t, svc, "ac1", json.RawMessage(`{"gProp":"g1","cProp":"alpha","vProp":5}`))
	pivot14Seed(t, svc, "ac2", json.RawMessage(`{"gProp":"g1","cProp":"alpha","vProp":1,"cPresent":true}`))
	pivot14Seed(t, svc, "ac3", json.RawMessage(`{"gProp":"g1","cProp":"beta","vProp":2,"cPresent":true}`))
	pivot14Seed(t, svc, "ac4", json.RawMessage(`{"gProp":"g1","cProp":null,"vProp":3,"cPresent":true}`))
	plan := pivot14ScalarPlan(t, pivot14ScalarConfig{duplicate: "ERROR", missing: "ERROR", unlisted: "ERROR", presence: "from-input"})
	rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render absent-companion parity plan: %v", err)
	}
	rows := svc.runRendered(t, rendered)
	if len(rows) != 1 {
		t.Fatalf("parity rows = %v", rows)
	}
	row := rows[0]
	for name, want := range map[string]string{
		"cell_alpha":   "1",
		"cell_beta":    "2",
		"cell_null":    "3",
		"cell_missing": "5",
	} {
		if got := row[pivot14ColIndex(t, rendered, name)]; got != want {
			t.Fatalf("%s = %q, want %s (companion-absent row must land in MISSING, present-true rows keep their kinds)", name, got, want)
		}
	}
}

func TestPivot14LiveAbsentCompanionEvidenceAndAliveControls(t *testing.T) {
	svc := pivot14LiveService(t)
	pivot14Seed(t, svc, "ae1", json.RawMessage(`{"gProp":"g1","cProp":"alpha","vProp":5}`))
	pivot14Seed(t, svc, "ae2", json.RawMessage(`{"gProp":"g2","vProp":7,"cPresent":true}`))
	cats := []ir.PhysicalGroupedPivotCategory{
		{Output: "cell_alpha", MatchKind: ir.PhysicalPivotCategoryValueMatch, ValueBindKey: "cat_alpha", ValueKind: "STRING"},
		{Output: "cell_missing", MatchKind: ir.PhysicalPivotCategoryMissingMatch},
	}
	plan := pivot14ScalarPlan(t, pivot14ScalarConfig{
		duplicate: "SUM", missing: "NULL", unlisted: "EXCLUDE_WITH_EVIDENCE",
		presence: "from-input", categories: cats,
	})
	rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render evidence plan: %v", err)
	}
	rows := svc.runRendered(t, rendered)
	byGroup := map[string][]string{}
	for _, row := range rows {
		byGroup[row[pivot14ColIndex(t, rendered, "grp")]] = row
	}
	g1, ok := byGroup["g1"]
	if !ok {
		t.Fatalf("g1 missing: %v", rows)
	}
	if got := g1[pivot14ColIndex(t, rendered, "unlisted_n")]; got != "0" {
		t.Fatalf("g1 evidence = %q, want 0 (absent-companion row is LISTED as MISSING per AQL)", got)
	}
	if got := g1[pivot14ColIndex(t, rendered, "cell_missing")]; got != "5" {
		t.Fatalf("g1 missing cell = %q, want 5 (the row must count in rawCount, not be dropped)", got)
	}
	g2, ok := byGroup["g2"]
	if !ok {
		t.Fatalf("g2 missing: %v", rows)
	}
	if got := g2[pivot14ColIndex(t, rendered, "unlisted_n")]; got != "1" {
		t.Fatalf("g2 evidence = %q, want the kept unlisted count 1", got)
	}

	svc2 := pivot14LiveService(t)
	pivot14Seed(t, svc2, "al1", json.RawMessage(`{"gProp":"g1","cProp":"alpha","vProp":1,"cPresent":true}`))
	pivot14Seed(t, svc2, "al2", json.RawMessage(`{"gProp":"g1","cProp":"zeta","vProp":9,"cPresent":true}`))
	raisePlan := pivot14ScalarPlan(t, pivot14ScalarConfig{
		duplicate: "ERROR", missing: "NULL", unlisted: "ERROR",
		presence: "from-input", categories: cats,
	})
	rendered2, err := RenderPhysicalPlan(raisePlan, pivot14Binding(t, svc2))
	if err != nil {
		t.Fatalf("render unlisted ALIVE plan: %v", err)
	}
	pivot14ExpectGuardRaise(t, svc2, rendered2, pivotUnlistedTag)

	svc3 := pivot14LiveService(t)
	pivot14Seed(t, svc3, "am1", json.RawMessage(`{"gProp":"g1","cProp":"alpha","vProp":1,"cPresent":true}`))
	missingPlan := pivot14ScalarPlan(t, pivot14ScalarConfig{
		duplicate: "ERROR", missing: "ERROR", unlisted: "ERROR",
		presence: "from-input", categories: cats,
	})
	rendered3, err := RenderPhysicalPlan(missingPlan, pivot14Binding(t, svc3))
	if err != nil {
		t.Fatalf("render cell-missing ALIVE plan: %v", err)
	}
	pivot14ExpectGuardRaise(t, svc3, rendered3, pivotCellMissingTag)
}

func TestPivot14AbsentVersusNullAliveControl(t *testing.T) {
	svc := pivot14LiveService(t)
	pivot14Seed(t, svc, "an1", json.RawMessage(`{"gProp":"g1","cProp":null,"vProp":9}`))
	pivot14Seed(t, svc, "an2", json.RawMessage(`{"gProp":"g1","vProp":8}`))
	pivot14Seed(t, svc, "an3", json.RawMessage(`{"gProp":"g1","cProp":"alpha","vProp":1}`))
	plan := pivot14ScalarPlan(t, pivot14ScalarConfig{duplicate: "SUM", missing: "NULL", presence: "contract"})
	rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render absent-versus-null plan: %v", err)
	}
	rows := svc.runRendered(t, rendered)
	if len(rows) != 1 {
		t.Fatalf("absent-versus-null rows = %v, want one group", rows)
	}
	row := rows[0]
	if got := row[pivot14ColIndex(t, rendered, "cell_null")]; got != "9" {
		t.Fatalf("stored-null row must land in the NULL cell, got %q (collapse to MISSING is the plan §7 failure)", got)
	}
	if got := row[pivot14ColIndex(t, rendered, "cell_missing")]; got != "8" {
		t.Fatalf("absent row must land in the MISSING cell, got %q", got)
	}
	if got := row[pivot14ColIndex(t, rendered, "cell_alpha")]; got != "1" {
		t.Fatalf("alpha cell = %q, want 1", got)
	}
}

func TestPivot14UnlistedEvidenceAliveControl(t *testing.T) {
	svc := pivot14LiveService(t)
	pivot14Seed(t, svc, "ue1", json.RawMessage(`{"gProp":"g_ok","cProp":"alpha","vProp":1}`))
	pivot14Seed(t, svc, "ue2", json.RawMessage(`{"gProp":"g_mix","cProp":"alpha","vProp":2}`))
	pivot14Seed(t, svc, "ue3", json.RawMessage(`{"gProp":"g_mix","cProp":"zeta","vProp":5}`))
	plan := pivot14ScalarPlan(t, pivot14ScalarConfig{duplicate: "SUM", missing: "NULL", unlisted: "EXCLUDE_WITH_EVIDENCE"})
	rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render evidence control plan: %v", err)
	}
	rows := svc.runRendered(t, rendered)
	if len(rows) != 2 {
		t.Fatalf("evidence control rows = %v", rows)
	}
	byGroup := map[string][]string{}
	for _, row := range rows {
		byGroup[row[pivot14ColIndex(t, rendered, "grp")]] = row
	}
	okRow, ok := byGroup["g_ok"]
	mixRow, mix := byGroup["g_mix"]
	if !ok || !mix {
		t.Fatalf("evidence control groups missing: %v", rows)
	}
	if got := okRow[pivot14ColIndex(t, rendered, "unlisted_n")]; got != "0" {
		t.Fatalf("all-listed group evidence = %q, want 0", got)
	}
	if got := mixRow[pivot14ColIndex(t, rendered, "unlisted_n")]; got != "1" {
		t.Fatalf("mixed group evidence = %q, want 1 (0≠1 is the non-vacuity differentiator)", got)
	}
}

func TestPivot14ReducerTypedOnlyAliveControl(t *testing.T) {
	svc := pivot14LiveService(t)
	pivot14Seed(t, svc, "rt1", json.RawMessage(`{"gProp":"g1","cProp":"alpha","vProp":1}`))
	pivot14Seed(t, svc, "rt2", json.RawMessage(`{"gProp":"g1","cProp":"alpha","vProp":null}`))
	pivot14Seed(t, svc, "rt3", json.RawMessage(`{"gProp":"g1","cProp":"alpha","vProp":2}`))
	pivot14Seed(t, svc, "rt4", json.RawMessage(`{"gProp":"g1","cProp":"beta","vProp":5}`))
	pivot14Seed(t, svc, "rt5", json.RawMessage(`{"gProp":"g1","cProp":null,"vProp":6}`))
	plan := pivot14ScalarPlan(t, pivot14ScalarConfig{duplicate: "SUM", missing: "ERROR"})
	rendered, err := RenderPhysicalPlan(plan, pivot14Binding(t, svc))
	if err != nil {
		t.Fatalf("render typed-only plan: %v", err)
	}
	rows := svc.runRendered(t, rendered)
	if len(rows) != 1 {
		t.Fatalf("typed-only rows = %v, want one group", rows)
	}
	if got := rows[0][pivot14ColIndex(t, rendered, "cell_alpha")]; got != "3" {
		t.Fatalf("alpha reducer must skip JSON null (SUM 1+2=3), got %q", got)
	}

	badSvc := pivot14LiveService(t)
	pivot14Seed(t, badSvc, "rb1", json.RawMessage(`{"gProp":"g1","cProp":"alpha","vProp":1}`))
	pivot14Seed(t, badSvc, "rb2", json.RawMessage(`{"gProp":"g1","cProp":"beta","vProp":"abc"}`))
	pivot14Seed(t, badSvc, "rb3", json.RawMessage(`{"gProp":"g1","cProp":null,"vProp":6}`))
	badPlan := pivot14ScalarPlan(t, pivot14ScalarConfig{duplicate: "SUM", missing: "ERROR"})
	badRendered, err := RenderPhysicalPlan(badPlan, pivot14Binding(t, badSvc))
	if err != nil {
		t.Fatalf("render typed-guard-live plan: %v", err)
	}
	pivot14ExpectGuardRaise(t, badSvc, badRendered, pivotValueTypeTag)
}
