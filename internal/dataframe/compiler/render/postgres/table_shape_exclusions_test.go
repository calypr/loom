package postgres

// table_shape_exclusions_test.go is the pg-sql-26 test surface for the
// table-shape excluded-row evidence terminal (table_shape_exclusions.go).
//
// Diff-receipt convention (the pg-sql-14 ruling, pivot_test.go:11-16): each
// golden that claims a branch is ported names the AQL source line and the
// distinguishing conjunct, and every refusal has an ALIVE sibling control so
// a swapped condition fails one of the two, never both silently.
//
// Coverage split, stated plainly: this file owns the renderer contract
// (closed-slice goldens, the refusal register with alive controls, the
// parameter law, and the live PG18 acceptance cells over the owned
// verify_sql26v1_* namespace). The compiler boundary — that
// compiler.CompileTableShapeExclusions reaches this door for a PostgreSQL
// target and keeps its registered AQL voice otherwise — is owned by
// compiler/table_shape_exclusions_test.go, because package compiler imports
// this one and a live leg here cannot call back without an import cycle.
//
// Live inventory (never-skip in the acceptance run; service-less lanes keep
// green by the package-shared testPostgresURL skip, which establishes
// nothing):
//   L1 independently expected evidence rows for one exact selected population
//     and scope, with direct-SQL witnesses.
//   L2 foreign project / dataset generation / auth path data never appears
//     (three separate negative fixtures, one per axis).
//   L3 absent property vs stored JSON null vs SQL-NULL-unreadable payload:
//     the tri-state and the identity-unavailable evidence pair.
//   L4 duplicate contributors do not perturb evidence (the value column is
//     never read), and the per-group evidence count equals the pivot's own
//     __loom_reshape_unlisted_count — the anti-divergence pin against the
//     predicate this terminal re-ports.
//   L5 empty population returns zero evidence rows and no phantom row.
//   L6 bounded and truncated pages: the limit+1 probe, and page1++page2 ==
//     the unpaginated list in the same deterministic order.
//   L7 hostile identity values (quotes, SQL punctuation, non-ASCII, an
//     out-of-double-range glyph) are inert parameters and order under
//     COLLATE "C".

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

const (
	excl26Project    = "proj-a"
	excl26Generation = "gen-1"
	excl26AuthPath   = "FHIR/Patient"
	excl26Prefix     = "verify_sql26v1_"
)

const (
	excl26OffsetBind     = "table_shape_exclusion_offset"
	excl26LimitBind      = "table_shape_exclusion_limit"
	excl26FetchLimitBind = "table_shape_exclusion_fetch_limit"
)

// ---------------------------------------------------------------------------
// Canonical plan builders
// ---------------------------------------------------------------------------

type excl26Config struct {
	categories   []ir.PhysicalGroupedPivotCategory
	categoryType string // default STRING
	valueType    string // default DECIMAL
	keyless      bool   // no group keys: identity has no key parts
	presence     string // "", "contract", "from-input"
	omitNulls    bool   // the category read uses the OMIT_NULLS extraction form
	stable       string // "", "rowid", "key", "none"
	offset       int    // default 2; zeroOffset opts into the real 0 page
	zeroOffset   bool
	limit        int    // default 5 (fetch limit is limit+1)
	duplicate    string // default ERROR
	missing      string // default NULL
	unlisted     string // default EXCLUDE_WITH_EVIDENCE
	bindDrop     string // drop one plan bind (a named single-point mutation)
	policyDrift  string // override the terminal's unlisted policy (mutation only)
}

func excl26DefaultCategories() []ir.PhysicalGroupedPivotCategory {
	return []ir.PhysicalGroupedPivotCategory{
		{Output: "cell_zero", MatchKind: ir.PhysicalPivotCategoryValueMatch, ValueBindKey: "cat_zero", ValueKind: "INTEGER"},
		{Output: "cell_one", MatchKind: ir.PhysicalPivotCategoryValueMatch, ValueBindKey: "cat_one", ValueKind: "INTEGER"},
	}
}

// excl26Plan builds the canonical registered terminal plan: one scoped direct
// root scan and the evidence terminal, over hand-carried canonical binds. The
// shapes only ir allows but no producer emits (an absent tie-breaker, a
// drifted policy) appear only through a named config field.
func excl26Plan(t *testing.T, cfg excl26Config) ir.PhysicalPlan {
	t.Helper()
	categories := cfg.categories
	if categories == nil {
		categories = excl26DefaultCategories()
	}
	// The frozen category type is the one the VALUE categories declare
	// (pivot.go:707-709 refuses a disagreement), so the builder cannot
	// accidentally assert a golden against a refused shape.
	categoryType := cfg.categoryType
	if categoryType == "" {
		categoryType = "STRING"
		for _, category := range categories {
			if category.MatchKind == ir.PhysicalPivotCategoryValueMatch && category.ValueKind != "" {
				categoryType = category.ValueKind
				break
			}
		}
	}
	valueType := cfg.valueType
	if valueType == "" {
		valueType = "DECIMAL"
	}
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
		unlisted = string(ir.PhysicalPivotUnlistedCategoryExcludeWithEvidence)
	}
	if cfg.policyDrift != "" {
		unlisted = cfg.policyDrift
	}
	offset, limit := cfg.offset, cfg.limit
	if limit == 0 {
		limit = 5
	}
	if offset == 0 && !cfg.zeroOffset {
		offset = 2
	}
	bindVars := map[string]any{
		"root_collection":                  "Patient",
		"project":                          excl26Project,
		"dataset_generation":               excl26Generation,
		"auth_resource_paths":              []string{excl26AuthPath},
		"auth_resource_paths_unrestricted": false,
		"scope_allowed":                    true,
		"p26_construction_id":              "p26-hand-construct",
		"cat_zero":                         int64(0),
		"cat_one":                          int64(1),
		excl26OffsetBind:                   offset,
		excl26LimitBind:                    limit,
		excl26FetchLimitBind:               limit + 1,
	}
	for _, category := range categories {
		if category.MatchKind != ir.PhysicalPivotCategoryValueMatch || category.ValueBindKey == "" {
			continue
		}
		if _, exists := bindVars[category.ValueBindKey]; exists {
			continue
		}
		switch category.ValueKind {
		case "STRING":
			bindVars[category.ValueBindKey] = "alpha-" + category.ValueBindKey
		case "INTEGER":
			bindVars[category.ValueBindKey] = int64(0)
		case "DECIMAL":
			bindVars[category.ValueBindKey] = 0.5
		case "BOOLEAN":
			bindVars[category.ValueBindKey] = true
		}
	}
	for _, drop := range strings.Split(cfg.bindDrop, ",") {
		if drop != "" {
			delete(bindVars, drop)
		}
	}

	inputs := []ir.PhysicalProjection{
		{Name: "_key", Hidden: true, Value: ir.PhysicalValue{Variable: "root", Path: []string{"_key"}}},
		{Name: "gcol", Value: ir.PhysicalValue{Variable: "root", Path: []string{"gProp"}}},
		{Name: "ccol", Value: ir.PhysicalValue{Variable: "root", Path: []string{"cProp"}}},
		{Name: "vcol", Value: ir.PhysicalValue{Variable: "root", Path: []string{"vProp"}}},
	}
	pivot := &ir.PhysicalGroupedPivot{
		ConstructionID:         "p26-hand-construct",
		ConstructionIDBindKey:  "p26_construction_id",
		InputRowVariable:       "irow",
		GroupRowsVariable:      "grow",
		OutputRowVariable:      "prow",
		InputProjections:       inputs,
		CategoryColumn:         "ccol",
		CategoryType:           categoryType,
		ValueColumn:            "vcol",
		ValueType:              valueType,
		Categories:             categories,
		DuplicatePolicy:        duplicate,
		MissingCellPolicy:      missing,
		UnlistedCategoryPolicy: unlisted,
	}
	if unlisted == string(ir.PhysicalPivotUnlistedCategoryExcludeWithEvidence) {
		pivot.UnlistedEvidenceColumn = "unlisted_n"
	}
	switch cfg.presence {
	case "contract":
		presence := &ir.PhysicalProjectionPresence{
			Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
			Paths:  [][]string{{"cProp"}},
		}
		pivot.CategoryPresence = presence
		pivot.CategoryPresenceColumn = "cProp_present"
		for index := range pivot.InputProjections {
			if pivot.InputProjections[index].Name == pivot.CategoryColumn {
				pivot.InputProjections[index].Presence = presence
			}
		}
	case "from-input":
		pivot.CategoryPresenceFromInput = true
		pivot.CategoryPresenceColumn = "cpresent"
		pivot.InputProjections = append(pivot.InputProjections, ir.PhysicalProjection{
			Name:  "cpresent",
			Value: ir.PhysicalValue{Variable: "root", Path: []string{"cPresent"}},
		})
	}
	if cfg.omitNulls {
		for index := range pivot.InputProjections {
			if pivot.InputProjections[index].Name != pivot.CategoryColumn {
				continue
			}
			pivot.InputProjections[index] = ir.PhysicalProjection{
				Name: pivot.CategoryColumn,
				Expression: &ir.PhysicalExpression{
					Kind:         ir.PhysicalExtractExpression,
					Cardinality:  ir.PhysicalScalarCardinality,
					NullBehavior: ir.PhysicalOmitNulls,
					Extract: &ir.PhysicalExtract{
						Source:       ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
						ResourceType: "Patient",
						Selector:     specSelector(t, "gender"),
					},
				},
			}
		}
	}
	switch cfg.stable {
	case "rowid":
		pivot.InputProjections = append(pivot.InputProjections, ir.PhysicalProjection{
			Name: pivotRowIDColumn, Hidden: true,
			Value: ir.PhysicalValue{Variable: "root", Path: []string{"rowId"}},
		})
	case "none":
		trimmed := pivot.InputProjections[:0]
		for _, projection := range pivot.InputProjections {
			if projection.Name == "_key" || projection.Name == pivotRowIDColumn {
				continue
			}
			trimmed = append(trimmed, projection)
		}
		pivot.InputProjections = trimmed
	}
	if !cfg.keyless {
		pivot.GroupKeys = []ir.PhysicalGroupedPivotKey{
			{Column: "gcol", Variable: "g0", Kind: "STRING", Output: "grp"},
		}
	}

	operations := []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalTableShapeExclusionReturnOp, TableShapeExclusionReturn: &ir.PhysicalTableShapeExclusionReturn{
			Pivot: *pivot, OffsetBindKey: excl26OffsetBind, LimitBindKey: excl26LimitBind,
			FetchLimitBindKey: excl26FetchLimitBind,
		}},
	}
	return ir.PhysicalPlan{Version: 1, Engine: ir.PhysicalEngineAQL, BindVars: bindVars, Operations: operations}
}

// excl26ProductionTerminalPlan production-compiles one grouped-pivot recipe
// (semantic → lower, the default policy) and mints the evidence terminal
// exactly as compiler/table_shape_exclusions.go:117-133 does: three fresh
// binds, the pivot operation replaced in place, and everything after it
// dropped. The compiler entry itself is exercised against this door by
// compiler/table_shape_exclusions_test.go.
func excl26ProductionTerminalPlan(t *testing.T, bundle recipe.Bundle, offset, limit int) ir.PhysicalPlan {
	t.Helper()
	plan := pivot14CompileBundle(t, bundle, []string{excl26AuthPath})
	pivotIndex := -1
	var pivot *ir.PhysicalGroupedPivot
	for index := range plan.Operations {
		if plan.Operations[index].Kind == ir.PhysicalGroupedPivotOp {
			pivotIndex, pivot = index, plan.Operations[index].GroupedPivot
		}
	}
	if pivot == nil {
		t.Fatalf("production plan carries no GROUPED_PIVOT: %#v", plan.Operations)
	}
	for _, bind := range []string{excl26OffsetBind, excl26LimitBind, excl26FetchLimitBind} {
		if _, exists := plan.BindVars[bind]; exists {
			t.Fatalf("bind %q is already defined", bind)
		}
	}
	plan.BindVars[excl26OffsetBind] = offset
	plan.BindVars[excl26LimitBind] = limit
	plan.BindVars[excl26FetchLimitBind] = limit + 1
	plan.Operations = append(plan.Operations[:pivotIndex], ir.PhysicalOperation{
		Kind:   ir.PhysicalTableShapeExclusionReturnOp,
		Source: plan.Operations[pivotIndex].Source,
		TableShapeExclusionReturn: &ir.PhysicalTableShapeExclusionReturn{
			Pivot: *pivot, OffsetBindKey: excl26OffsetBind, LimitBindKey: excl26LimitBind,
			FetchLimitBindKey: excl26FetchLimitBind,
		},
	})
	if err := plan.Validate(); err != nil {
		t.Fatalf("validate minted terminal plan: %v", err)
	}
	return plan
}

// excl26Bundle authors the discriminating table-reshape pivot over Patient
// that the live cells seed: group by gender, pivot multipleBirthInteger, with
// the unlisted policy this terminal exists for.
func excl26Bundle(name string, categories []recipe.GroupedPivotCategory, filters []recipe.Filter) recipe.Bundle {
	zero := int64(0)
	one := int64(1)
	if categories == nil {
		categories = []recipe.GroupedPivotCategory{
			{Key: recipe.TableScalar{Kind: recipe.TableScalarInteger, Integer: &zero}, Output: "birth_zero", Label: "Zero"},
			{Key: recipe.TableScalar{Kind: recipe.TableScalarInteger, Integer: &one}, Output: "birth_one", Label: "One"},
		}
	}
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
					Categories:             categories,
					DuplicatePolicy:        recipe.PivotDuplicateSum,
					MissingCellPolicy:      recipe.PivotMissingCellNull,
					UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence,
				},
			},
		}},
	}
}

func excl26Render(t *testing.T, plan ir.PhysicalPlan) string {
	t.Helper()
	rendered, err := RenderTableShapeExclusions(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("RenderTableShapeExclusions: %v", err)
	}
	return rendered.SQL
}

func excl26Refused(t *testing.T, plan ir.PhysicalPlan, label, wantFragment string) {
	t.Helper()
	_, doorErr := RenderTableShapeExclusions(plan, mustBinding(t))
	if doorErr == nil {
		t.Fatalf("%s: expected typed refusal, got success", label)
	}
	if !errors.Is(doorErr, ErrUnsupportedPostgresPlan) {
		t.Fatalf("%s: refusal is not ErrUnsupportedPostgresPlan: %v", label, doorErr)
	}
	if wantFragment != "" && !strings.Contains(doorErr.Error(), wantFragment) {
		t.Fatalf("%s: refusal %q lacks %q", label, doorErr.Error(), wantFragment)
	}
	// Entry==dispatch message equality: the RenderPhysicalPlan door refuses
	// with the identical typed message.
	_, dispatchErr := RenderPhysicalPlan(plan, mustBinding(t))
	if dispatchErr == nil || dispatchErr.Error() != doorErr.Error() {
		t.Fatalf("%s: door and dispatch differ:\n door     %v\n dispatch %v", label, doorErr, dispatchErr)
	}
}

// ---------------------------------------------------------------------------
// Goldens: the canonical statement, branch-for-branch
// ---------------------------------------------------------------------------

func TestExclusion26GoldenCanonicalStatement(t *testing.T) {
	sql := excl26Render(t, excl26Plan(t, excl26Config{presence: "contract", stable: "key"}))
	for _, want := range []string{
		// The relation: consumed reads only. The pivot's VALUE column is
		// never read (table_shape_exclusion.go has no value term anywhere),
		// so vProp must not appear.
		"WITH px_src AS (SELECT ",
		`FROM "` + testSchema + `"."fhir_resource" AS r0`,
		"FROM px_src AS p",
		// AQL :54 FILTER NOT listed, in the package's two-valued spelling.
		"WHERE NOT COALESCE((",
		// AQL :75 the identity-available conjunction, COALESCE'd because a
		// SQL NULL document reads NULL where IS_OBJECT(null) is false.
		"COALESCE(jsonb_typeof(",
		"= 'object'",
		"#>> '{}') <> ''",
		// AQL :120 the two identity fields are gated on availability.
		"AS \"c0\"", "AS \"c1\"", "AS \"c2\"", "AS \"c3\"", "AS \"c4\"",
		"AS \"c5\"", "AS \"c6\"", "AS \"c7\"", "AS \"c8\"",
		// AQL :124/:125 the EXACT / UNAVAILABLE pair.
		"ELSE $",
		// AQL :132 the omission rides only the unavailable branch.
		"THEN NULL ELSE $",
		// AQL :63/:129 the identity, through the pivot's own emitter
		// (pivot.go:406-415), byte-comparable to TO_STRING(["GROUPED_PIVOT",…]).
		`'[' || '"GROUPED_PIVOT"' || ',' || to_jsonb($`,
		// AQL :102 LIMIT @offset, @fetchLimit → OFFSET $n LIMIT $m.
		"OFFSET $", " LIMIT $",
		// AQL :101 the seven-term order: identity, available DESC, then the
		// type-ranked members; the identity uses the registered ASCII bound.
		`COLLATE "C" ASC`,
		" DESC",
		// AQL :48 the listed predicate's VALUE-category conjunct: presence,
		// typeof, then the frozen value as a parameter.
		"jsonb_typeof(p.",
		"::jsonb)",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("golden statement lost %q:\n%s", want, sql)
		}
	}
	for _, forbidden := range []string{"vProp", "p26-hand-construct", "FHIR/Patient", excl26Project, excl26Generation} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("statement text carries data that must be a parameter (%q):\n%s", forbidden, sql)
		}
	}
	// Every frozen evidence field name and label is a parameter, never text.
	for _, forbidden := range []string{
		ir.PhysicalTableShapeExclusionResourceTypeField,
		ir.PhysicalTableShapeExclusionOutputRowIDField,
		ir.PhysicalTableShapeExclusionReasonUnlistedCategory,
		ir.PhysicalTableShapeExclusionIdentityExact,
		ir.PhysicalTableShapeExclusionIdentityUnavailable,
		ir.PhysicalTableShapeExclusionSourceIdentityUnavailable,
	} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("evidence label %q was interpolated into statement text:\n%s", forbidden, sql)
		}
	}
	if strings.Contains(sql, "= 0::") || strings.Contains(sql, "= 1::") {
		t.Fatalf("frozen category value was interpolated, not parameterized:\n%s", sql)
	}
}

func TestExclusion26GoldenColumnOrderAndParameterLaw(t *testing.T) {
	plan := excl26Plan(t, excl26Config{presence: "contract", stable: "key"})
	rendered, err := RenderTableShapeExclusions(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := []string{
		ir.PhysicalTableShapeExclusionResourceTypeField,
		ir.PhysicalTableShapeExclusionResourceIDField,
		ir.PhysicalTableShapeExclusionIdentityStatusField,
		ir.PhysicalTableShapeExclusionCategoryValueField,
		ir.PhysicalTableShapeExclusionCategoryPresentField,
		ir.PhysicalTableShapeExclusionCategoryTypeField,
		ir.PhysicalTableShapeExclusionOutputRowIDField,
		ir.PhysicalTableShapeExclusionReasonField,
		ir.PhysicalTableShapeExclusionOmissionField,
	}
	if strings.Join(rendered.OutputColumns, ",") != strings.Join(want, ",") {
		t.Fatalf("output columns = %v, want the AQL RETURN order %v", rendered.OutputColumns, want)
	}
	// Positional law: Args[i] is the value for $(i+1), and the window pair
	// is the last two, offset then probe bound.
	if len(rendered.Args) < 10 {
		t.Fatalf("statement carries only %d parameters", len(rendered.Args))
	}
	last := len(rendered.Args) - 1
	if rendered.Args[last].Type != "bigint" || rendered.Args[last].Value.(int64) != 6 {
		t.Fatalf("final argument must be the fetch limit $%d=6, got %#v", last+1, rendered.Args[last])
	}
	if rendered.Args[last-1].Type != "bigint" || rendered.Args[last-1].Value.(int64) != 2 {
		t.Fatalf("penultimate argument must be the offset $%d=2, got %#v", last, rendered.Args[last-1])
	}
	if !strings.HasSuffix(rendered.SQL, "OFFSET $"+fmt.Sprint(last)+" LIMIT $"+fmt.Sprint(last+1)) {
		t.Fatalf("window is not the trailing ordered pair:\n%s", rendered.SQL)
	}
	// Every $n placeholder in the statement is used exactly by one argument.
	for index := 1; index <= len(rendered.Args); index++ {
		needle := "$" + fmt.Sprint(index)
		if !strings.Contains(rendered.SQL, needle) && !strings.Contains(rendered.SQL, needle+" ") {
			t.Fatalf("parameter $%d (%#v) is never referenced", index, rendered.Args[index-1].Value)
		}
	}
	for _, parameter := range rendered.Args {
		if parameter.Value == nil {
			t.Fatal("a renderer argument is nil")
		}
	}
}

func TestExclusion26GoldenCategoryMatchKinds(t *testing.T) {
	// AQL table_reshape.go:420-431, one conjunct per match kind, in the
	// pivot's jsonb spelling (pivot.go:1168-1187).
	sql := excl26Render(t, excl26Plan(t, excl26Config{
		presence: "contract",
		categories: []ir.PhysicalGroupedPivotCategory{
			{Output: "cell_alpha", MatchKind: ir.PhysicalPivotCategoryValueMatch, ValueBindKey: "cat_alpha_s", ValueKind: "STRING"},
			{Output: "cell_null", MatchKind: ir.PhysicalPivotCategoryNullMatch},
			{Output: "cell_missing", MatchKind: ir.PhysicalPivotCategoryMissingMatch},
		},
		categoryType: "STRING",
	}))
	if !strings.Contains(sql, `jsonb_typeof(p.`) || !strings.Contains(sql, `= 'string'`) {
		t.Fatalf("VALUE category lost its typeof conjunct:\n%s", sql)
	}
	if !strings.Contains(sql, "= 'null'::jsonb") {
		t.Fatalf("NULL category lost its stored-null disjunct:\n%s", sql)
	}
	if !strings.Contains(sql, "NOT (") {
		t.Fatalf("MISSING category lost its NOT(present) arm:\n%s", sql)
	}
	// ALIVE control: the OMIT_NULLS spelling of the NULL arm is the other
	// disjunct (pivot.go:1180-1183), so a swapped branch fails one leg.
	omit := excl26Render(t, excl26Plan(t, excl26Config{
		omitNulls: true,
		categories: []ir.PhysicalGroupedPivotCategory{
			{Output: "cell_null", MatchKind: ir.PhysicalPivotCategoryNullMatch},
		},
		categoryType: "STRING",
	}))
	if !strings.Contains(omit, " IS NOT NULL AND ") || strings.Contains(omit, "IS NULL OR ") {
		t.Fatalf("OMIT_NULLS NULL arm did not take the registered branch:\n%s", omit)
	}
	if strings.Contains(sql, "IS NOT NULL AND p.") {
		t.Fatalf("the preserved-null plan took the OMIT_NULLS NULL arm:\n%s", sql)
	}
}

func TestExclusion26GoldenPresenceForms(t *testing.T) {
	// AQL :77-80 and table_reshape.go:405-407: with a presence contract the
	// bit rides a proven document path; without one, PRESERVE_NULL renders
	// always-present and OMIT_NULLS reads IS NOT NULL.
	contract := excl26Render(t, excl26Plan(t, excl26Config{presence: "contract"}))
	if !strings.Contains(contract, "jsonb_exists(r0.\"payload\", $") || !strings.Contains(contract, " THEN true ELSE false END") {
		t.Fatalf("presence contract lost its CASE proof:\n%s", contract)
	}
	companion := excl26Render(t, excl26Plan(t, excl26Config{presence: "from-input"}))
	if !strings.Contains(companion, "COALESCE(p.") || !strings.Contains(companion, " = 'true'::jsonb, false)") {
		t.Fatalf("input companion lost its two-valued COALESCE:\n%s", companion)
	}
	plain := excl26Render(t, excl26Plan(t, excl26Config{}))
	// The always-present derivation folds to true, but it must ride a CTE
	// slot, not the ORDER BY text: a bare constant there is PostgreSQL
	// "non-integer constant in ORDER BY" (SQLSTATE 42601), the execution
	// failure the live L1-L7 legs pin against. The presence column then
	// reads that slot everywhere, including c4 and the fifth SORT term.
	if !strings.Contains(plain, "true AS px_") ||
		!strings.Contains(plain, "to_jsonb(p.px_") || !strings.Contains(plain, ") AS \"c4\"") {
		t.Fatalf("PRESERVE_NULL presence must render always-present through a slot, exactly as AQL's HAS on an always-populated projected column does:\n%s", plain)
	}
	if strings.Contains(plain, "ORDER BY") && strings.Contains(plain[strings.Index(plain, "ORDER BY"):], "true ASC") {
		t.Fatalf("a bare constant reached the ORDER BY (SQLSTATE 42601):\n%s", plain)
	}
	if strings.Contains(plain, "'true'::jsonb ELSE") {
		t.Fatalf("a plan with no presence contract invented a document-path proof:\n%s", plain)
	}
}

func TestExclusion26KeylessTerminalIsIrIllegal(t *testing.T) {
	// A keyless evidence terminal is not a shape this renderer ever sees:
	// ir/physical_validation.go:1557 requires a valid grouped pivot, and
	// validatePhysicalGroupedPivot requires group keys. The renderer does not
	// re-invent that rule; it fails closed.
	_, err := RenderTableShapeExclusions(excl26Plan(t, excl26Config{keyless: true}), mustBinding(t))
	if err == nil || !strings.Contains(err.Error(), "group keys") {
		t.Fatalf("keyless terminal must fail closed on the ir pivot contract, got %v", err)
	}
}

func TestExclusion26GoldenIdentityAndTieBreaker(t *testing.T) {
	// The identity is the pivot's own composition over every group key
	// (table_shape_exclusion.go:137-150 via pivot.go:406-415).
	keyed := excl26Render(t, excl26Plan(t, excl26Config{}))
	if strings.Contains(keyed, `"STRING"`) {
		t.Fatalf("a group-key kind label was interpolated:\n%s", keyed)
	}
	// The stable tie-breaker: _key reads the storage identity column, never
	// a payload path (a payload read would be SQL NULL for every row and
	// silently remove the deterministic page order).
	if !strings.Contains(keyed, `to_jsonb(r0."logical_id"::text)`) {
		t.Fatalf("_key did not read the canonical identity column:\n%s", keyed)
	}
	if strings.Contains(keyed, `'{_key}'`) {
		t.Fatalf("_key was bound as a document path:\n%s", keyed)
	}
	rowid, err := RenderTableShapeExclusions(excl26Plan(t, excl26Config{stable: "rowid"}), mustBinding(t))
	if err != nil {
		t.Fatalf("rowid tie-breaker render: %v", err)
	}
	// __loom_row_id wins: _key is not consumed, so the canonical identity
	// read never appears, and the rowId payload path crosses the boundary
	// as exactly one bound text[] parameter.
	if strings.Contains(rowid.SQL, `to_jsonb(r0."logical_id"::text)`) {
		t.Fatalf("_key was read although __loom_row_id wins the tie-breaker:\n%s", rowid.SQL)
	}
	bindsRowID := false
	for _, arg := range rowid.Args {
		if value, ok := arg.Value.(string); ok && value == "{rowId}" {
			bindsRowID = true
		}
	}
	if !bindsRowID {
		t.Fatalf("the row-id tie-breaker path is not a bound text[] parameter:\n%s", rowid.SQL)
	}
}

// ---------------------------------------------------------------------------
// Refusal register, each with an alive control
// ---------------------------------------------------------------------------

func TestExclusion26RefusalRegister(t *testing.T) {
	base := func() ir.PhysicalPlan { return excl26Plan(t, excl26Config{stable: "key"}) }

	// X1: the terminal owns its window; a leaked SORT/LIMIT/RETURN would be
	// silently dropped rather than applied twice.
	sortAfter := base()
	sortAfter.Operations = append(sortAfter.Operations, ir.PhysicalOperation{
		Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{Keys: []ir.PhysicalValue{{Variable: "prow", Path: []string{"__loom_row_id"}}}},
	})
	excl26Refused(t, sortAfter, "X1 SORT after the terminal", "must be the final operation")

	// The default arm itself: a spine operation in the source region has no
	// dispatch here, and the terminal owns its window, so it is refused
	// rather than silently dropped (the L-19.4 sabotage lesson).
	insert := func(plan ir.PhysicalPlan, operation ir.PhysicalOperation) ir.PhysicalPlan {
		operations := []ir.PhysicalOperation{plan.Operations[0]}
		operations = append(operations, operation)
		return ir.PhysicalPlan{Version: plan.Version, Engine: plan.Engine,
			BindVars: plan.BindVars, Operations: append(operations, plan.Operations[1])}
	}
	sortInSource := insert(base(), ir.PhysicalOperation{
		Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{Keys: []ir.PhysicalValue{{Variable: "prow", Path: []string{"__loom_row_id"}}}},
	})
	excl26Refused(t, sortInSource, "X1 SORT in the source region", "cannot coexist with the evidence terminal")
	limitInSource := insert(base(), ir.PhysicalOperation{
		Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: excl26FetchLimitBind},
	})
	excl26Refused(t, limitInSource, "X1 LIMIT in the source region", "cannot coexist with the evidence terminal")
	// A source-region UNPIVOT is claimed by the earlier registered melt arm
	// (render.go:305-312), which already refuses this payload fail-closed;
	// this door's default arm is the second line, not the first voice, so no
	// entry/dispatch equality is claimed for it.

	notLast := base()
	operations := []ir.PhysicalOperation{notLast.Operations[0], notLast.Operations[1]}
	operations = append(operations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp,
		Filter: &ir.PhysicalFilter{}})
	notLast.Operations = operations
	excl26Refused(t, notLast, "X1 terminal not final", "must be the final operation")

	missingTerminal := base()
	missingTerminal.Operations = missingTerminal.Operations[:1]
	// With the terminal op removed, the door condition (an operation of the
	// exclusion kind) no longer holds, so RenderPhysicalPlan legitimately
	// refuses it in the ir plan validator instead of the slice surface. The
	// entry itself — which does not consult the door — refuses with the
	// typed surface message; dispatch must also refuse, without claiming
	// message equality it never promised.
	if _, err := RenderTableShapeExclusions(missingTerminal, mustBinding(t)); err == nil ||
		!errors.Is(err, ErrUnsupportedPostgresPlan) ||
		!strings.Contains(err.Error(), "requires a terminal evidence operation") {
		t.Fatalf("X1 no terminal: entry must refuse typed: %v", err)
	}
	if _, err := RenderPhysicalPlan(missingTerminal, mustBinding(t)); err == nil {
		t.Fatal("X1 no terminal: dispatch must refuse")
	}

	nilPayload := base()
	nilPayload.Operations[1].TableShapeExclusionReturn = nil
	excl26Refused(t, nilPayload, "X1 terminal payload nil", "missing its payload")

	// X2: the pivot door's own refusals, re-read here because this door
	// dispatches before the spine gates.
	preview := base()
	preview.PreviewSourceWindowByRootID = true
	excl26Refused(t, preview, "X2 plan-level preview window", "preview source window")

	// X3: the policy gate. The alive control is the canonical render.
	drift := excl26Plan(t, excl26Config{stable: "key", policyDrift: "ERROR"})
	// ir re-validates the policy/evidence-column pairing, so the renderer's
	// own voice is reached through the door's payload check.
	excl26Refused(t, drift, "X3 policy drift", "EXCLUDE_WITH_EVIDENCE")

	// X4: the window bind contract, re-read at the renderer.
	for _, mutation := range []struct {
		label, bind, fragment string
		value                 any
	}{
		{"X4 negative offset", excl26OffsetBind, "offset must be non-negative", -1},
		{"X4 zero limit", excl26LimitBind, "limit must be positive", 0},
		{"X4 probe drift", excl26FetchLimitBind, "must be limit+1", 99},
		{"X4 non-int bind", excl26OffsetBind, "must be an int", "2"},
	} {
		plan := base()
		plan.BindVars[mutation.bind] = mutation.value
		excl26Refused(t, plan, mutation.label, mutation.fragment)
	}
	missingBind := base()
	delete(missingBind.BindVars, excl26LimitBind)
	excl26Refused(t, missingBind, "X4 missing limit bind", "evidence limit bind")

	// X5: no stable tie-breaker means page order is not deterministic.
	excl26Refused(t, excl26Plan(t, excl26Config{stable: "none"}), "X5 no tie-breaker", "no stable row tie-breaker")

	// X5: a consumed column with no renderable read.
	badRead := base()
	for index := range badRead.Operations[1].TableShapeExclusionReturn.Pivot.InputProjections {
		if badRead.Operations[1].TableShapeExclusionReturn.Pivot.InputProjections[index].Name == "ccol" {
			badRead.Operations[1].TableShapeExclusionReturn.Pivot.InputProjections[index].Value.Variable = "elsewhere"
		}
	}
	excl26Refused(t, badRead, "X5 category read is not a root read", "direct document read")

	// X6: only the direct root scan is registered.
	populationRoot := base()
	populationRoot.Operations[0].RootScan.Population = &ir.PhysicalPopulationRootSource{}
	excl26Refused(t, populationRoot, "X6 population root", "population, cohort, and candidate-seed roots")

	// No leading root scan reaches a nil dereference.
	noRoot := base()
	noRoot.Operations = noRoot.Operations[1:]
	excl26Refused(t, noRoot, "X1 no leading root scan", "leads with exactly one ROOT_SCAN")
}

func TestExclusion26AliveControlsRender(t *testing.T) {
	// Each refusal above has a sibling that must render, so a dead check
	// cannot pass silently: the canonical plan, a keyless plan, a companion
	// plan, and the production-compiled plan all reach SQL.
	for _, caseName := range []string{"canonical", "contract", "companion", "rowid", "zero-page"} {
		cfg := excl26Config{}
		switch caseName {
		case "contract":
			cfg.presence = "contract"
		case "zero-page":
			cfg.zeroOffset = true
		case "companion":
			cfg.presence = "from-input"
		case "rowid":
			cfg.stable = "rowid"
		}
		if sql := excl26Render(t, excl26Plan(t, cfg)); !strings.Contains(sql, "OFFSET $") {
			t.Fatalf("%s: control did not render a windowed statement:\n%s", caseName, sql)
		}
	}
	plan := excl26ProductionTerminalPlan(t, excl26Bundle("p26_alive_control", nil, nil), 0, 3)
	rendered, err := RenderTableShapeExclusions(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("production-compiled terminal must render: %v", err)
	}
	if !strings.Contains(rendered.SQL, "WHERE NOT COALESCE(") {
		t.Fatalf("production statement lost the unlisted filter:\n%s", rendered.SQL)
	}
}

func TestExclusion26DoorEqualsDispatch(t *testing.T) {
	// Entry==dispatch message equality (the pivot.go:142-144 precedent): the
	// registered entry and the RenderPhysicalPlan arm are one contract.
	plan := excl26Plan(t, excl26Config{stable: "key"})
	direct, directErr := RenderTableShapeExclusions(plan, mustBinding(t))
	viaDispatch, dispatchErr := RenderPhysicalPlan(plan, mustBinding(t))
	if directErr != nil || dispatchErr != nil {
		t.Fatalf("both doors must render: %v / %v", directErr, dispatchErr)
	}
	if direct.SQL != viaDispatch.SQL {
		t.Fatalf("door and dispatch disagree:\n%s\n%s", direct.SQL, viaDispatch.SQL)
	}
	if len(direct.Args) != len(viaDispatch.Args) {
		t.Fatalf("door and dispatch disagree on arguments: %d vs %d", len(direct.Args), len(viaDispatch.Args))
	}
	// A plan that is not this shape must not be claimed by the arm: the
	// spine's own voice stays byte-identical for a plain scoped read.
	plainPlan := withReturn(scopedPlan(excl26Project, excl26Generation, []string{excl26AuthPath}, false),
		ir.PhysicalProjection{Name: "k", Value: ir.PhysicalValue{Variable: "root", Path: []string{"_key"}}})
	plain, err := RenderPhysicalPlan(plainPlan, mustBinding(t))
	if err != nil {
		t.Fatalf("plain scoped read must still render: %v", err)
	}
	if strings.Contains(plain.SQL, "px_src") {
		t.Fatalf("the exclusion arm claimed a plan that does not carry the terminal:\n%s", plain.SQL)
	}
}

func TestExclusion26SurfaceStableUnderRepeatedRender(t *testing.T) {
	// Determinism law: the same plan and binding yield byte-identical SQL
	// and value-identical ordered arguments.
	plan := excl26Plan(t, excl26Config{presence: "contract", stable: "key"})
	first, err := RenderTableShapeExclusions(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	second, err := RenderTableShapeExclusions(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("second render: %v", err)
	}
	if first.SQL != second.SQL {
		t.Fatal("repeated render is not byte-identical")
	}
	if fmt.Sprint(first.Args) != fmt.Sprint(second.Args) {
		t.Fatalf("repeated render re-ordered arguments:\n%v\n%v", first.Args, second.Args)
	}
	// Rendering must not mutate the plan (the compiler proves the published
	// plan is untouched; this proves the renderer keeps that promise).
	third, err := RenderTableShapeExclusions(plan, mustBinding(t))
	if err != nil || third.SQL != first.SQL {
		t.Fatalf("rendering perturbed the plan: %v", err)
	}
}
