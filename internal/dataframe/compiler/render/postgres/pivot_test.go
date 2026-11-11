package postgres

// pivot_test.go is the pg-sql-14 test surface for the scalar GROUPED_PIVOT
// slice: closed-slice goldens (branch-for-branch against
// render/aql/table_reshape.go renderGroupedTablePivot :28-336 with the diff
// receipts cited at each asserted branch), the refusal register with ALIVE
// controls, spine-path untouchedness, hostile-data parameterization, and the
// production-compiled offline/live acceptance legs over the
// verify_sql14v1_* namespace tracked by the TestMain delta audit.
//
// Diff-receipt convention (§7, coordinator ruling): each golden that claims
// a branch is ported verbatim names the AQL source line and the distinguishing
// conjunct, and a live control leg proves the assertion is ALIVE — the
// opposite branch (the one this renderer must NOT take) is exercised in the
// sibling coded_pivot_test.go leg so a swapped raw/typed missing-condition
// fails one of the two, never both silently.

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

// ---------------------------------------------------------------------------
// Production compile helpers (recipe → semantic → lower, default policy)
// ---------------------------------------------------------------------------

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

// pivot14ScalarBundle authors the discriminating table-reshape pivot over
// Patient: group by gender, pivot multipleBirthInteger against itself
// (frozen INTEGER categories 0 and 1), DECIMAL-free plain reducer policy.
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

// ---------------------------------------------------------------------------
// Hand-built canonical plan (renderer-level goldens; ir-legal shapes the
// producers never emit, e.g. the compiler-level hidden auth group key)
// ---------------------------------------------------------------------------

type pivot14ScalarConfig struct {
	keyless         bool
	duplicate       string
	missing         string
	unlisted        string // "ERROR" or "EXCLUDE_WITH_EVIDENCE"
	window          bool   // reshaped SORT + LIMIT
	presence        string // "", "contract", "from-input" (with a MISSING category when set)
	categoryOmit    bool   // category input projection omits nulls
	extractorValue  bool   // value input projection is a selector extraction
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
			// A MISSING category requires a preserved category presence
			// contract (ir re-read; the renderer refuses without one).
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
		// nothing on the category projection; the companion carries it
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

// ---------------------------------------------------------------------------
// Production acceptance: the registered slice renders real planner output
// ---------------------------------------------------------------------------

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
	// Frozen categories are parameters, never SQL text (the AQL @bind
	// discipline): the compiled INTEGER category values 0/1 must not appear
	// as literal comparisons in the statement, and the auth-scope data never
	// reaches SQL text.
	for _, forbidden := range []string{"proj-a", "gen-1", "FHIR/Patient", "female"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("plan data leaked into statement text (%q):\n%s", forbidden, sql)
		}
	}
	if len(rendered.Args) == 0 {
		t.Fatal("production pivot statement carries no parameters")
	}
}

// ---------------------------------------------------------------------------
// Goldens: hand-built canonical statement, branch-for-branch
// ---------------------------------------------------------------------------

func TestPivot14GoldenScalarStatement(t *testing.T) {
	sql := pivot14Render(t, pivot14ScalarPlan(t, pivot14ScalarConfig{
		duplicate: "SUM", missing: "NULL", unlisted: "ERROR", presence: "contract",
	}))
	// Guard chain order — unlisted guard FIRST (table_reshape.go:224/:228
	// render the unlisted ASSERT before the per-category guards), then the
	// per-category guards in category order. The reducer branch conditions
	// MISSING on the RAW count (:230) and the value-type ASSERT rides the
	// typed enumeration (:215/:252). Diff receipt: the ERROR branch's
	// cardinality guard is the raw count and its cell is the null-
	// preserving FIRST (TestPivot14GoldenErrorBranch below); the two
	// branches' missing conditions differ exactly in the count they read
	// (rawCount = 0 here vs the coded typedCount = 0 — the verbatim
	// asymmetry, :230 vs :267; the ALIVE controls are the paired live
	// legs TestPivot14LiveScalarReducerMissingRawCount vs the coded twin
	// TestCodedPivot14LiveReducerMissingTypedCount).
	unlistedFirst := strings.Index(sql, `CAST('TABLE_PIVOT_UNLISTED_CATEGORY'`)
	cardinalityAfter := strings.Index(sql, `CAST('TABLE_PIVOT_VALUE_TYPE_MISMATCH'`)
	if unlistedFirst < 0 || cardinalityAfter < unlistedFirst {
		t.Fatalf("guard chain order drifted (unlisted must precede per-category guards):\n%s", sql)
	}
	for _, want := range []string{
		// Unlisted guard: COUNT(*) FILTER (WHERE NOT (listed)) > 0.
		"COUNT(*) FILTER (WHERE NOT (",
		"THEN CAST('TABLE_PIVOT_UNLISTED_CATEGORY'",
		// Reducer cell: typed-count-conditioned null else to_jsonb(REDUCER
		// (typed text)::double precision) FILTER typed (IEEE double
		// reducers, ruling R4 — architecturally identical to pg-sql-13).
		"CASE WHEN COUNT(*) FILTER (WHERE",
		"THEN 'null'::jsonb ELSE to_jsonb(SUM(",
		"::double precision) FILTER (WHERE",
		// Value-type mismatch witness over the typed enumeration.
		"jsonb_typeof(",
		"THEN CAST('TABLE_PIVOT_VALUE_TYPE_MISMATCH'",
		// Category match: presence-gated typed equality against the frozen
		// jsonb parameter (the AQL __category == @value, :196-:208).
		"jsonb_typeof(",
		"= 'string'",
		"::jsonb)",
		// Identity composition (table_shape_exclusion.go:137-150).
		`'[' || '"GROUPED_PIVOT"' || ',' || to_jsonb(`,
		`|| ',' || '[' || to_jsonb(`,
		`|| ']' || ']'`,
		// Uniform GROUP BY over the key slots; final identity order.
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
	// The NULL category reads AQL-null over the tri-state (missing property
	// OR stored JSON null) gated by presence (table_reshape.go:200-203);
	// the MISSING category is the negated presence (NOT (present)).
	if !strings.Contains(sql, "NOT (p.") {
		t.Fatalf("MISSING category must render NOT (present):\n%s", sql)
	}
}

func TestPivot14GoldenErrorBranch(t *testing.T) {
	sql := pivot14Render(t, pivot14ScalarPlan(t, pivot14ScalarConfig{
		duplicate: "ERROR", missing: "ERROR", unlisted: "ERROR",
	}))
	// ERROR branch diff receipts (table_reshape.go:245-262): the cell is
	// the null-preserving FIRST over the RAW matched list
	// (ARRAY_AGG(...)[1] COALESCE 'null'), the cardinality guard is the
	// RAW count > 1, and the MISSING guard is the RAW count = 0.
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
	// The keyless GROUP BY () arm stays defense in depth: ir is the
	// authoritative gate and refuses a keyless pivot ("input projections,
	// group keys, and categories are required",
	// ir/physical_table_shape.go validatePhysicalGroupedPivot).
	sql := pivot14Render(t, pivot14ScalarPlan(t, pivot14ScalarConfig{
		duplicate: "MAX", window: true,
	}))
	// Window leg: with the reshaped SORT the rows order by the stable
	// identity under COLLATE "C" (the D-19a bound) and the LIMIT crosses
	// SQL only as a $n parameter.
	if !strings.Contains(sql, `COLLATE "C" ASC LIMIT $`) {
		t.Fatalf("window leg lost the identity order + bound LIMIT:\n%s", sql)
	}
	if strings.Contains(sql, "LIMIT 2") {
		t.Fatalf("LIMIT must stay parameterized:\n%s", sql)
	}
}

func TestPivot14GoldenNoWindowPerKeyOrder(t *testing.T) {
	sql := pivot14Render(t, pivot14ScalarPlan(t, pivot14ScalarConfig{duplicate: "MIN"}))
	// Without the reshaped SORT the AQL emits per-key ASC over the Arango
	// type rank (table_reshape.go:138-140): rank CASE (null<bool<number
	// <string), then within-type terms, identity last as the registered
	// deterministic tie-break.
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
	// Contract form: OR of the propertyPresenceExpression proofs as the
	// jsonb 'true'/'false' reading, compared to 'true'::jsonb.
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

	// FromInput form: the companion projection IS the presence bit.
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
	if !strings.Contains(sql, " = 'true'::jsonb") {
		t.Fatalf("input-carried presence must compare the companion bit:\n%s", sql)
	}

	// OMIT (null-omission) category behavior: present = raw slot IS NOT
	// NULL, and the NULL category reads only the stored-null state
	// (null-omission reports stored null as absent-matching-null, the
	// registered mapping at pivot.go).
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
	// appendAuthResourcePathProjection fires only at the compiler level
	// (compiler/recipe_execution.go:100, recipe_paging.go:77), so the
	// hidden auth group key is covered here by hand IR: the key must be
	// grouped and observable through the RETURN.
	plan := pivot14ScalarPlan(t, pivot14ScalarConfig{duplicate: "ERROR"})
	pivot := plan.Operations[1].GroupedPivot
	pivot.GroupKeys = append(pivot.GroupKeys, ir.PhysicalGroupedPivotKey{
		Column: "authcol", Variable: "authg", Kind: "STRING", Output: "authcol", Hidden: true})
	// The auth column reads the row's auth_resource_path column (the
	// compiler mutator appends this projection; hand IR carries it).
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
	// §7: adversarial category values — quotes, commas, brackets, JSON
	// fragments — must ride $n to a byte-identical identity, never SQL
	// text (the AQL @bind parity, table_reshape.go:196/:373).
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

// ---------------------------------------------------------------------------
// Refusal register — every refusal with an ALIVE control
// ---------------------------------------------------------------------------

func TestPivot14SpinePathUntouched(t *testing.T) {
	// ALIVE control pair for the whole register: the canonical plan renders,
	// and the plan with the registered canonical FILTER arms renders too —
	// every refusal below mutates one fact away from exactly such a plan.
	if sql := pivot14Render(t, pivot14ScalarPlan(t, pivot14ScalarConfig{})); sql == "" {
		t.Fatal("canonical pivot rendered empty")
	}
	// The default-arm refusals for pivot-free plans are byte-identical:
	// the merged StageSequence refusal and the plan-level window refusal
	// must keep their exact pre-ticket texts (C3).
	// C3: a single-stage NON-coded construction PIVOT (the authored
	// recipe PIVOT operation) never enters the coded door — the merged
	// StageSequence refusal keeps its exact pre-ticket text.
	nonCoded := pivot14CompileBundle(t, pivot14NonCodedPivotBundle(t, "p14_spine_noncoded"), []string{"FHIR/Observation"})
	if nonCoded.StageSequence == nil || nonCoded.StageSequence.Stages[0].GroupedPivot == nil ||
		nonCoded.StageSequence.Stages[0].GroupedPivot.CodedCorrelation != nil {
		t.Fatalf("non-coded construction pivot shape drifted: %+v", nonCoded.StageSequence)
	}
	if _, err := RenderPhysicalPlan(nonCoded, mustBinding(t)); err == nil ||
		!strings.Contains(err.Error(), "staged row-operator sequences are not yet implemented for PostgreSQL") {
		t.Fatalf("merged StageSequence refusal drifted: %v", err)
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
		{"stage sequence coexistence", func(p *ir.PhysicalPlan) {
			p.StageSequence = &ir.PhysicalStageSequence{Stages: []ir.PhysicalConstructionStage{{Kind: ir.PhysicalStageGroupOp}}}
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
			// ALIVE control: the unmutated canonical plan renders.
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
	// Probe P-14.2 ruling: an authored output FILTER renders at AQL and
	// must refuse here with the named pg-sql-16 divergence — never
	// silently rendered. ALIVE control: the identical bundle without the
	// filter renders.
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

// ---------------------------------------------------------------------------
// Live legs — verify_sql14v1_* namespace, TestMain delta audit
// ---------------------------------------------------------------------------

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

// pivot14RunDeltaAudit wraps the composed audit chain (pg-sql-14 is the
// outermost hook) over the disjoint verify_sql14v1_* namespace: every
// resource born during this run must be tracked by a live test here, and
// none may survive at all.
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

// pivot14SeedOracle seeds the discriminating stored states for the scalar
// oracle: one group with a single value, one with stored JSON null under a
// matched category, one with duplicates, one with an unlisted category, one
// with the category absent (MISSING state), and a second group.
func pivot14SeedOracle(t *testing.T, svc *pgir02Service) {
	t.Helper()
	pivot14Seed(t, svc, "o1", json.RawMessage(`{"gender":"female","multipleBirthInteger":0}`))
	pivot14Seed(t, svc, "o2", json.RawMessage(`{"gender":"female","multipleBirthInteger":1}`))
	pivot14Seed(t, svc, "o3", json.RawMessage(`{"gender":"female","multipleBirthInteger":0}`))
	pivot14Seed(t, svc, "o4", json.RawMessage(`{"gender":"female","multipleBirthInteger":2}`))
	pivot14Seed(t, svc, "o5", json.RawMessage(`{"gender":"female"}`))
	pivot14Seed(t, svc, "o6", json.RawMessage(`{"gender":"female","multipleBirthInteger":null}`))
	pivot14Seed(t, svc, "o7", json.RawMessage(`{"gender":"male","multipleBirthInteger":1}`))
	// Non-vacuity: the discriminating payloads are really stored.
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

	// SUM reducer: female group — category 0 matches o1 and o3 (SUM = 0+0
	// = 0), category 1 matches o2 (1). The unlisted category 2 (o4), the
	// absent category (o5), and the stored JSON null (o6) contribute to
	// neither cell under EXCLUDE_WITH_EVIDENCE.
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
	// Locate the group position from the compiled RETURN names.
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
	// Cell readings come back as JSON text: category 0 → 0, category 1 → 1.
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
	// ALIVE control (scalar side of the :230 vs :267 asymmetry): the
	// ordinary reducer branch conditions MISSING on the RAW matched count.
	// The o6 row matched category… category values here are INTEGER 0/1 and
	// the value column reads the same property, so a matched row always
	// carries a number — the discriminating state is built by the NULL
	// category: category… INTEGER 0/1 cannot be null, so the raw-vs-typed
	// divergence is exercised on the coded twin leg; here the control
	// proves the branch that DOES exist: with DuplicatePolicy=ERROR,
	// duplicate category-0 rows (o1+o3) raise TABLE_PIVOT_CELL_CARDINALITY
	// (raw count > 1, :252-258) and EXPLAIN completes under BOTH
	// plan_cache modes (plan-time safety, the calls_live precedent), while
	// the SUM sibling of the identical data never raises.
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
	// ALIVE control (scalar side of the :230 vs :267 asymmetry): the
	// ordinary reducer branch conditions MISSING on the RAW matched count
	// (table_reshape.go:230). The g1 row matches the alpha category while
	// its value is stored JSON null: raw count 1, typed count 0. With
	// SUM + MissingCellPolicy=ERROR the scalar branch must NOT raise
	// (the typed-count-conditioned coded branch raises on the analogous
	// data — TestCodedPivot14LiveReducerMissingTypedCount); the cell
	// reads JSON null. Swapping the counts in either renderer fails one
	// leg.
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

	// Guard ALIVENESS: a group whose alpha raw matched count is 0 (the
	// row is unlisted and excluded with evidence) must raise
	// TABLE_PIVOT_CELL_MISSING under the same policies.
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

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func TestPivot14LiveWindowOrderAndLimit(t *testing.T) {
	svc := pivot14LiveService(t)
	// One group-key row per state, each row belonging to exactly one
	// category, so the ERROR duplicate policy stays untriggered:
	// alpha-value (w1), stored JSON null (w2), absent category (w3), and
	// a second group (w4).
	pivot14Seed(t, svc, "w1", json.RawMessage(`{"gProp":"g1","cProp":"alpha","vProp":1}`))
	pivot14Seed(t, svc, "w2", json.RawMessage(`{"gProp":"g1","cProp":null,"vProp":2}`))
	pivot14Seed(t, svc, "w3", json.RawMessage(`{"gProp":"g1","vProp":3}`))
	pivot14Seed(t, svc, "w4", json.RawMessage(`{"gProp":"G2","cProp":"alpha","vProp":10}`))

	// Full window leg: SORT over {OutputRowVariable, __loom_row_id} then
	// LIMIT — rows order by the identity under COLLATE "C" (the D-19a
	// bound), the JSON-compact ASCII order of ["GROUPED_PIVOT",cid,
	// ["STRING",kv]], and the bound LIMIT keeps the prefix. The keys
	// g1/G2 are discriminating: the database collation (en_US.utf8)
	// reads case-insensitively (g1 first) while COLLATE "C" is byte
	// order ("G2" first), so dropping COLLATE "C" flips this leg.
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
	// Cell readings: the null-preserving FIRST over each matched raw list.
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

	// Bound LIMIT prefix: with LIMIT 1 only the first identity (the
	// COLLATE "C" leader, G2) survives.
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
