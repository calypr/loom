package postgres

// Offline contract cells for the pg-sql-25 category terminal: the exact
// composed statement, its parameter law, the three-state readings, the closed
// refusal surface, and sabotage cells proving each guard is load-bearing.
// Live real-PostgreSQL-18 legs are in category_scan_live_test.go.

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

const (
	sql25Marker         = "__loom_category_scan_presence"
	sql25CategoryColumn = "gender"
	sql25OtherColumn    = "status"
	sql25OfflineSchema  = "verify_sql25v1_offline"
)

// sql25CategoryProjection is the lowered shape a category column arrives in:
// one scalar-cardinality DIRECT_SCALAR EXTRACT selector over the root row
// document carrying presence metadata for its own path.
func sql25CategoryProjection(name, field string, behavior ir.PhysicalNullBehavior, override func(*ir.PhysicalExtract, *ir.PhysicalProjection)) ir.PhysicalProjection {
	source := ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}
	expression := &ir.PhysicalExpression{
		Kind:         ir.PhysicalExtractExpression,
		Cardinality:  ir.PhysicalScalarCardinality,
		NullBehavior: behavior,
		Extract: &ir.PhysicalExtract{
			Source:        source,
			ResourceType:  "Patient",
			Selector:      spec.Selector{Steps: []spec.SelectorStep{{Field: field}}},
			ExecutionMode: ir.PhysicalSelectorDirectScalar,
		},
	}
	projection := ir.PhysicalProjection{
		Name:       name,
		Expression: expression,
		Presence:   &ir.PhysicalProjectionPresence{Source: source, Paths: [][]string{{field}}},
	}
	if override != nil {
		override(expression.Extract, &projection)
	}
	return projection
}

// sql25Plan is the registered category surface: one scoped direct root scan
// plus the terminal RETURN.
func sql25Plan(projections ...ir.PhysicalProjection) ir.PhysicalPlan {
	return withReturn(scopedPlan(itProject, itGeneration, nil, true), projections...)
}

func sql25Render(t *testing.T, plan ir.PhysicalPlan, column, marker string, maxValues int) RenderedCategoryScan {
	t.Helper()
	rendered, err := RenderCategoryScan(plan, bindingFor(t, sql25OfflineSchema), column, marker, maxValues)
	if err != nil {
		t.Fatalf("RenderCategoryScan(%q): %v", column, err)
	}
	return rendered
}

// ---------------------------------------------------------------------------
// Structure: one composed statement, positional contract, parameter law.
// ---------------------------------------------------------------------------

func TestRenderCategoryScanComposesBoundedDistinctPage(t *testing.T) {
	plan := sql25Plan(
		sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil),
		sql25CategoryProjection(sql25OtherColumn, "status", ir.PhysicalPreserveNull, nil),
	)
	rendered := sql25Render(t, plan, sql25CategoryColumn, sql25Marker, 8)

	want := `WITH __loom_category_source AS (SELECT (r0."payload" #> $4::text[]) AS "c0", ` +
		`COALESCE(jsonb_typeof(r0."payload") = 'object' AND jsonb_exists(r0."payload", $5), false) AS "c1" ` +
		`FROM "` + sql25OfflineSchema + `"."fhir_resource" AS r0 ` +
		`WHERE r0."project" = $1 AND r0."dataset_generation" = $2 AND r0."resource_type" = $3 ` +
		`ORDER BY r0."resource_seq" ASC), ` +
		`__loom_category_rows AS (SELECT __loom_category_row."c1" AS "present", ` +
		`CASE WHEN __loom_category_row."c1" THEN __loom_category_row."c0" END AS "value" ` +
		`FROM __loom_category_source AS __loom_category_row), ` +
		`__loom_category_distinct AS (SELECT "present", "value" FROM __loom_category_rows ` +
		`GROUP BY "present", "value") ` +
		`SELECT "present" AS "c0", "value" AS "c1" FROM __loom_category_distinct ` +
		`ORDER BY "present" ASC, CASE WHEN "value" IS NULL THEN 2 ELSE CASE COALESCE(jsonb_typeof("value"), 'null')` +
		` WHEN 'array' THEN 0 WHEN 'boolean' THEN 1 WHEN 'null' THEN 2 WHEN 'number' THEN 3` +
		` WHEN 'object' THEN 4 WHEN 'string' THEN 5 ELSE 6 END END ASC, "value" ASC LIMIT $6`
	if rendered.Values.SQL != want {
		t.Fatalf("category page SQL\ngot:  %s\nwant: %s", rendered.Values.SQL, want)
	}
	wantArgs := []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: itProject},
		{Type: command.PostgreSQLParameterText, Value: itGeneration},
		{Type: command.PostgreSQLParameterText, Value: "Patient"},
		{Type: command.PostgreSQLParameterText, Value: "{gender}"},
		{Type: command.PostgreSQLParameterText, Value: "gender"},
		{Type: command.PostgreSQLParameterBigint, Value: int64(9)},
	}
	if !reflect.DeepEqual(rendered.Values.Args, wantArgs) {
		t.Fatalf("category page args = %#v, want %#v", rendered.Values.Args, wantArgs)
	}
	if got := rendered.Values.OutputColumns; !reflect.DeepEqual(got, []string{"present", "value"}) {
		t.Fatalf("declared output columns = %v, want [present value]", got)
	}
	if rendered.PresentColumn != CategoryScanPresentColumn || rendered.ValueColumn != CategoryScanValueColumn {
		t.Fatalf("column contract = %q/%q", rendered.PresentColumn, rendered.ValueColumn)
	}
	if rendered.PresenceMarkerColumn != sql25Marker {
		t.Fatalf("presence marker echoed = %q", rendered.PresenceMarkerColumn)
	}
	// Parameter law: AQL bind syntax never appears, and the page bound is the
	// maxValues+1 truncation probe as the final argument.
	if strings.Contains(rendered.Values.SQL, "@") {
		t.Fatalf("AQL bind syntax reached the PostgreSQL category statement: %s", rendered.Values.SQL)
	}
	if !strings.HasSuffix(rendered.Values.SQL, "LIMIT $6") {
		t.Fatalf("page bound is not the last placeholder: %s", rendered.Values.SQL)
	}
	// The category column name never becomes SQL text: it is read through the
	// bound document path, and the statement names only renderer-owned
	// aliases.
	if strings.Contains(rendered.Values.SQL, `"`+sql25CategoryColumn+`"`) || strings.Contains(rendered.Values.SQL, sql25OtherColumn) {
		t.Fatalf("a plan column name leaked into SQL text: %s", rendered.Values.SQL)
	}
}

func TestRenderCategoryScanWitnessSharesTheDistinctRelation(t *testing.T) {
	plan := sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil))
	rendered := sql25Render(t, plan, sql25CategoryColumn, sql25Marker, 8)
	witness := rendered.Witness
	// The witness outer SELECT carries no ORDER BY of its own: only the
	// spine's deterministic inner resource_seq order survives, which cannot
	// change a row count or presence bit.
	if outer := witness.SQL[strings.Index(witness.SQL, categoryDistinctCTE):]; strings.Contains(outer, "ORDER BY") {
		t.Fatalf("the bounded confirmation must not sort its page: %s", witness.SQL)
	}
	if !strings.Contains(witness.SQL, `ORDER BY r0."resource_seq" ASC`) {
		t.Fatalf("the inner deterministic spine order is missing: %s", witness.SQL)
	}
	if !strings.HasSuffix(witness.SQL, "LIMIT $6") {
		t.Fatalf("witness bound is not the last placeholder: %s", witness.SQL)
	}
	last := witness.Args[len(witness.Args)-1]
	if want := (command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(8*4 + 1)}); last != want {
		t.Fatalf("witness bound = %#v, want %#v", last, want)
	}
	if !reflect.DeepEqual(witness.OutputColumns, []string{"present", "value"}) {
		t.Fatalf("witness declares %v", witness.OutputColumns)
	}
	// The confirmation and the page agree on what a (present, value) group is:
	// every clause but the ORDER BY/LIMIT pair is shared verbatim.
	page := rendered.Values.SQL
	pageHead, witnessHead := page[:strings.Index(page, " SELECT ")], witness.SQL[:strings.Index(witness.SQL, " SELECT ")]
	if pageHead != witnessHead {
		t.Fatalf("witness distinct relation differs from the page:\n page: %s\nwitness: %s", pageHead, witnessHead)
	}
}

func TestRenderCategoryScanIsDeterministic(t *testing.T) {
	plan := sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil))
	binding := bindingFor(t, sql25OfflineSchema)
	first, err := RenderCategoryScan(plan, binding, sql25CategoryColumn, sql25Marker, 64)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		again, err := RenderCategoryScan(plan, binding, sql25CategoryColumn, sql25Marker, 64)
		if err != nil {
			t.Fatal(err)
		}
		if again.Values.SQL != first.Values.SQL || again.Witness.SQL != first.Witness.SQL {
			t.Fatalf("render %d was not byte-identical", attempt)
		}
		if !reflect.DeepEqual(again.Values.Args, first.Values.Args) || !reflect.DeepEqual(again.Witness.Args, first.Witness.Args) {
			t.Fatalf("render %d was not argument-identical", attempt)
		}
	}
	if plan.Operations[len(plan.Operations)-1].Return.Projections[0].Name != sql25CategoryColumn ||
		len(plan.Operations[len(plan.Operations)-1].Return.Projections) != 1 {
		t.Fatalf("the caller's plan was mutated: %v", plan.Operations[len(plan.Operations)-1].Return.Projections)
	}
}

// ---------------------------------------------------------------------------
// Three-state readings: the presence bit stays total and the value column
// keeps the null_semantics.go behavior forms.
// ---------------------------------------------------------------------------

func TestRenderCategoryScanPreservesBehaviorFormsBesideATotalPresenceBit(t *testing.T) {
	cases := []struct {
		name        string
		behavior    ir.PhysicalNullBehavior
		wantValue   string
		rejectValue string
	}{
		{"preserve null", ir.PhysicalPreserveNull, `(r0."payload" #> $4::text[]) AS "c0"`, `NULLIF(`},
		{"omit nulls", ir.PhysicalOmitNulls, `NULLIF((r0."payload" #> $4::text[]), 'null'::jsonb) AS "c0"`, ``},
		{"empty on null", ir.PhysicalEmptyOnNull,
			`COALESCE(NULLIF((r0."payload" #> $4::text[]), 'null'::jsonb), '""'::jsonb) AS "c0"`, ``},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			rendered := sql25Render(t, sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", test.behavior, nil)),
				sql25CategoryColumn, sql25Marker, 4)
			sql := rendered.Values.SQL
			if !strings.Contains(sql, test.wantValue) {
				t.Fatalf("value form %q missing:\n%s", test.wantValue, sql)
			}
			if test.rejectValue != "" && strings.Contains(sql, test.rejectValue) {
				t.Fatalf("unexpected value form %q:\n%s", test.rejectValue, sql)
			}
			// The presence proof is behavior-independent, total, and never a
			// constant: it is the jsonb_exists descent over the same path.
			const presence = `COALESCE(jsonb_typeof(r0."payload") = 'object' AND jsonb_exists(r0."payload", $5), false) AS "c1"`
			if !strings.Contains(sql, presence) {
				t.Fatalf("presence proof form missing:\n%s", sql)
			}
			// Absent contributes NULL, not the behavior's substitute value:
			// the CASE keeps the presence bit decisive for the value column.
			if !strings.Contains(sql, `CASE WHEN __loom_category_row."c1" THEN __loom_category_row."c0" END AS "value"`) {
				t.Fatalf("value is not gated by presence:\n%s", sql)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The closed refusal surface.
// ---------------------------------------------------------------------------

func TestRenderCategoryScanRefusals(t *testing.T) {
	withOperation := func(plan ir.PhysicalPlan, operation ir.PhysicalOperation) ir.PhysicalPlan {
		operations := append([]ir.PhysicalOperation(nil), plan.Operations...)
		operations = append(operations[:len(operations)-1], operation, operations[len(operations)-1])
		plan.Operations = operations
		return plan
	}
	planWithLimit := withOperation(sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil)),
		ir.PhysicalOperation{Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: "limit"}})
	planWithUnnest := withOperation(sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil)),
		ir.PhysicalOperation{Kind: ir.PhysicalUnnestOp, Unnest: &ir.PhysicalUnnest{
			OutputVariable: "item", HasItemVariable: "item_has",
			EmptyPolicy: ir.PhysicalUnnestPreserveParent,
		}})
	planWithTraversal := withOperation(sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil)),
		ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{
			SourceVariable: "root", TargetVariable: "obs", EdgeVariable: "edge",
			Direction: ir.PhysicalOutbound, EdgeCollectionBindKey: "edges", EdgeLabelBindKey: "edge_type",
		}})
	planWithTwoSorts := sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil))
	sortOp := ir.PhysicalOperation{Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{
		Keys: []ir.PhysicalValue{{Variable: "root", Path: []string{"_key"}}}}}
	planWithTwoSorts.Operations = append(planWithTwoSorts.Operations[:1], append([]ir.PhysicalOperation{sortOp, sortOp}, planWithTwoSorts.Operations[1:]...)...)
	planWithStageSequence := sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil))
	planWithStageSequence.StageSequence = &ir.PhysicalStageSequence{SourceStageID: "source_projection"}
	planWithDuplicateCategory := sql25Plan(
		sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil),
		sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil))
	hiddenCategory := sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil)
	hiddenCategory.Hidden = true
	planWithHiddenCategory := sql25Plan(hiddenCategory)
	planWithMarkerCollision := sql25Plan(
		sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil),
		sql25CategoryProjection(sql25Marker, "status", ir.PhysicalPreserveNull, nil))
	planWithoutCategory := sql25Plan(sql25CategoryProjection(sql25OtherColumn, "status", ir.PhysicalPreserveNull, nil))
	planWithoutReturn := scopedPlan(itProject, itGeneration, nil, true)
	planWithReturnlessTail := sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil))
	planWithReturnlessTail.Operations = planWithReturnlessTail.Operations[:len(planWithReturnlessTail.Operations)-1]
	foreignPresence := sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil)
	foreignPresence.Presence = &ir.PhysicalProjectionPresence{
		Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}, Paths: [][]string{{"maritalStatus"}}}
	planWithForeignPresence := sql25Plan(foreignPresence)
	multiPathPresence := sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil)
	multiPathPresence.Presence = &ir.PhysicalProjectionPresence{
		Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}, Paths: [][]string{{"gender"}, {"maritalStatus"}}}
	planWithMultiPathPresence := sql25Plan(multiPathPresence)
	nested := sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, func(extract *ir.PhysicalExtract, _ *ir.PhysicalProjection) {
		extract.Selector = spec.Selector{Steps: []spec.SelectorStep{{Field: "maritalStatus"}, {Field: "coding"}}}
		extract.Prepared = nil
	})
	nested.Presence = &ir.PhysicalProjectionPresence{
		Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}, Paths: [][]string{{"maritalStatus", "coding"}}}
	planWithNestedSelector := sql25Plan(nested)

	cases := []struct {
		name    string
		plan    ir.PhysicalPlan
		column  string
		marker  string
		bound   int
		denials []string
	}{
		{"empty column", sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil)),
			"  ", sql25Marker, 8, []string{"names the column"}},
		{"column equals marker", sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil)),
			sql25Marker, sql25Marker, 8, []string{"distinct non-blank name"}},
		{"blank marker", sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil)),
			sql25CategoryColumn, " ", 8, []string{"distinct non-blank name"}},
		{"zero bound", sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil)),
			sql25CategoryColumn, sql25Marker, 0, []string{"outside the registered 1..256 bound"}},
		{"oversized bound", sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil)),
			sql25CategoryColumn, sql25Marker, 257, []string{"outside the registered 1..256 bound"}},
		{"missing category", planWithoutCategory, sql25CategoryColumn, sql25Marker, 8,
			[]string{"does not project the category column"}},
		{"duplicate category", planWithDuplicateCategory, sql25CategoryColumn, sql25Marker, 8,
			[]string{"projected exactly once"}},
		{"hidden category", planWithHiddenCategory, sql25CategoryColumn, sql25Marker, 8,
			[]string{"hidden executor-side metadata"}},
		{"marker collision", planWithMarkerCollision, sql25CategoryColumn, sql25Marker, 8,
			[]string{"collides with the renderer-owned presence marker"}},
		{"no return", planWithoutReturn, sql25CategoryColumn, sql25Marker, 8,
			[]string{"requires a terminal RETURN"}},
		{"return not terminal", planWithReturnlessTail, sql25CategoryColumn, sql25Marker, 8,
			[]string{"terminal RETURN"}},
		{"composed stage sequence", planWithStageSequence, sql25CategoryColumn, sql25Marker, 8,
			[]string{"composed construction stages are not registered"}},
		{"plan-carried limit", planWithLimit, sql25CategoryColumn, sql25Marker, 8,
			[]string{"would bound the page before the distinct"}},
		{"unnest", planWithUnnest, sql25CategoryColumn, sql25Marker, 8,
			[]string{"outside the registered PostgreSQL category surface"}},
		{"traversal", planWithTraversal, sql25CategoryColumn, sql25Marker, 8,
			[]string{"outside the registered PostgreSQL category surface"}},
		{"two sorts", planWithTwoSorts, sql25CategoryColumn, sql25Marker, 8,
			[]string{"at most one SORT"}},
		{"foreign presence claim", planWithForeignPresence, sql25CategoryColumn, sql25Marker, 8,
			[]string{"presence metadata must prove the selector's own path"}},
		{"multi-path presence claim", planWithMultiPathPresence, sql25CategoryColumn, sql25Marker, 8,
			[]string{"presence metadata must prove the selector's own path"}},
		{"direct value read", sql25Plan(ir.PhysicalProjection{
			Name: sql25CategoryColumn, Value: ir.PhysicalValue{Variable: "root", Path: []string{"gender"}}}),
			sql25CategoryColumn, sql25Marker, 8,
			[]string{"scalar-cardinality EXTRACT selector"}},
		{"generic selector mode", sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull,
			func(extract *ir.PhysicalExtract, _ *ir.PhysicalProjection) {
				extract.ExecutionMode = ir.PhysicalSelectorGeneric
			})), sql25CategoryColumn, sql25Marker, 8,
			[]string{"execution mode"}},
		{"conditional-array selector mode", sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull,
			func(extract *ir.PhysicalExtract, _ *ir.PhysicalProjection) {
				extract.ExecutionMode = ir.PhysicalSelectorConditionalArray
			})), sql25CategoryColumn, sql25Marker, 8,
			[]string{"execution mode"}},
		{"array cardinality", sql25Plan(func() ir.PhysicalProjection {
			projection := sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil)
			projection.Expression.Cardinality = ir.PhysicalArrayCardinality
			return projection
		}()), sql25CategoryColumn, sql25Marker, 8, []string{"scalar-cardinality EXTRACT selector"}},
		{"selector fallback", sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull,
			func(extract *ir.PhysicalExtract, _ *ir.PhysicalProjection) {
				extract.Fallbacks = []ir.PhysicalSelectorFallback{{
					Source:       ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
					ResourceType: "Patient",
					Selector:     spec.Selector{Steps: []spec.SelectorStep{{Field: "maritalStatus"}}},
				}}
			})), sql25CategoryColumn, sql25Marker, 8,
			[]string{"outside the registered single-path direct scalar form"}},
		{"distinct selector", sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull,
			func(extract *ir.PhysicalExtract, _ *ir.PhysicalProjection) { extract.Distinct = true })),
			sql25CategoryColumn, sql25Marker, 8, []string{"outside the registered single-path direct scalar form"}},
		{"selector filter", sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull,
			func(extract *ir.PhysicalExtract, _ *ir.PhysicalProjection) {
				extract.Selector = spec.Selector{Steps: []spec.SelectorStep{{Field: "gender"}},
					Filter: &spec.ContainsFilter{Field: "system", Needle: "urn:loinc"}}
			})), sql25CategoryColumn, sql25Marker, 8,
			[]string{"outside the registered single-path direct scalar form"}},
		{"indexed step", sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull,
			func(extract *ir.PhysicalExtract, _ *ir.PhysicalProjection) {
				index := 0
				extract.Selector = spec.Selector{Steps: []spec.SelectorStep{{Field: "gender", Index: &index}}}
				extract.ExecutionMode = ir.PhysicalSelectorGeneric
				extract.Prepared = nil
			})), sql25CategoryColumn, sql25Marker, 8,
			[]string{"execution mode"}},
		{"iterated step", sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull,
			func(extract *ir.PhysicalExtract, _ *ir.PhysicalProjection) {
				extract.Selector = spec.Selector{Steps: []spec.SelectorStep{{Field: "gender", Iterate: true}}}
				extract.ExecutionMode = ir.PhysicalSelectorGeneric
			})), sql25CategoryColumn, sql25Marker, 8,
			[]string{"execution mode"}},
		{"empty selector", sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull,
			func(extract *ir.PhysicalExtract, _ *ir.PhysicalProjection) {
				extract.Selector = spec.Selector{}
			})), sql25CategoryColumn, sql25Marker, 8,
			[]string{"empty selector"}},
		{"bind-backed selector source", sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull,
			func(extract *ir.PhysicalExtract, _ *ir.PhysicalProjection) {
				extract.Source = ir.PhysicalValue{BindKey: "document"}
			})), sql25CategoryColumn, sql25Marker, 8,
			[]string{`must extract from the row document at ["payload"]`}},
		{"nested document source", sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull,
			func(extract *ir.PhysicalExtract, _ *ir.PhysicalProjection) {
				extract.Source = ir.PhysicalValue{Variable: "root", Path: []string{"payload", "meta"}}
			})), sql25CategoryColumn, sql25Marker, 8,
			[]string{`must extract from the row document at ["payload"]`}},
		{"nested selector path", planWithNestedSelector, sql25CategoryColumn, sql25Marker, 8,
			[]string{"crosses repeated field"}},
		{"foreign selector resource type", sql25Plan(sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull,
			func(extract *ir.PhysicalExtract, _ *ir.PhysicalProjection) { extract.ResourceType = "Observation" })),
			sql25CategoryColumn, sql25Marker, 8, []string{"is not in the active FHIR schema"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := RenderCategoryScan(test.plan, bindingFor(t, sql25OfflineSchema), test.column, test.marker, test.bound)
			if err == nil {
				t.Fatalf("expected a refusal")
			}
			if !errors.Is(err, ErrUnsupportedPostgresPlan) && !strings.Contains(err.Error(), "validate physical plan") {
				t.Fatalf("refusal must carry the typed unsupported-plan error or the spine validator's typed plan error: %v", err)
			}
			if strings.Contains(strings.ToLower(err.Error()), "aql") {
				t.Fatalf("refusal must not offer an AQL fallback: %v", err)
			}
			for _, denial := range test.denials {
				if !strings.Contains(err.Error(), denial) {
					t.Fatalf("refusal must name %q: %v", denial, err)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Sabotage cells: each guard fails on a near-miss and stays silent only on
// the shape it exists for.
// ---------------------------------------------------------------------------

func TestRenderCategoryScanGuardsAreLoadBearing(t *testing.T) {
	category := sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil)
	binding := bindingFor(t, sql25OfflineSchema)

	t.Run("a sorted plan loses its inner order but keeps a total page order", func(t *testing.T) {
		sorted := sql25Plan(category)
		sorted.Operations = append(sorted.Operations[:1], append([]ir.PhysicalOperation{{
			Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{Keys: []ir.PhysicalValue{{Variable: "root", Path: []string{"logical_id"}}}},
		}}, sorted.Operations[1:]...)...)
		rendered := sql25Render(t, sorted, sql25CategoryColumn, sql25Marker, 8)
		if strings.Contains(rendered.Values.SQL, `ORDER BY r0."logical_id"`) {
			t.Fatalf("the plan sort survived into the inner read:\n%s", rendered.Values.SQL)
		}
		for _, key := range []string{`ORDER BY "present" ASC`, `END END ASC, "value" ASC`} {
			if !strings.Contains(rendered.Values.SQL, key) {
				t.Fatalf("page order key %q missing:\n%s", key, rendered.Values.SQL)
			}
		}
	})

	t.Run("a marker collision refuses while a free marker renders", func(t *testing.T) {
		colliding := sql25Plan(category, sql25CategoryProjection(sql25Marker, "status", ir.PhysicalPreserveNull, nil))
		if _, err := RenderCategoryScan(colliding, binding, sql25CategoryColumn, sql25Marker, 8); err == nil {
			t.Fatal("a projection named like the presence marker must refuse")
		}
		free := sql25Plan(category, sql25CategoryProjection("status", "status", ir.PhysicalPreserveNull, nil))
		if _, err := RenderCategoryScan(free, binding, sql25CategoryColumn, sql25Marker, 8); err != nil {
			t.Fatalf("an uncollided marker must render: %v", err)
		}
	})

	t.Run("presence metadata is checked, not ignored", func(t *testing.T) {
		agreeing := sql25Plan(category)
		if _, err := RenderCategoryScan(agreeing, binding, sql25CategoryColumn, sql25Marker, 8); err != nil {
			t.Fatalf("the lowered shape must render: %v", err)
		}
		mismatched := category
		mismatched.Presence = &ir.PhysicalProjectionPresence{
			Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}, Paths: [][]string{{"activationDate"}}}
		if _, err := RenderCategoryScan(sql25Plan(mismatched), binding, sql25CategoryColumn, sql25Marker, 8); err == nil {
			t.Fatal("a presence claim for another path must refuse")
		}
	})

	t.Run("the presence column is a real proof, not a constant", func(t *testing.T) {
		rendered := sql25Render(t, sql25Plan(category), sql25CategoryColumn, sql25Marker, 8)
		for _, forbidden := range []string{`true AS "c1"`, `false AS "c1"`, `NULL AS "c1"`} {
			if strings.Contains(rendered.Values.SQL, forbidden) {
				t.Fatalf("presence column degenerated to %q:\n%s", forbidden, rendered.Values.SQL)
			}
		}
		if !strings.Contains(rendered.Values.SQL, "jsonb_exists(r0.\"payload\", $5)") {
			t.Fatalf("presence proof is not the bound-key descent:\n%s", rendered.Values.SQL)
		}
		if rendered.Values.Args[4] != (command.PostgreSQLParameter{Type: command.PostgreSQLParameterText, Value: "gender"}) {
			t.Fatalf("presence key argument = %#v", rendered.Values.Args[4])
		}
	})

	t.Run("an untrusted binding fails before any plan work", func(t *testing.T) {
		if _, err := RenderCategoryScan(sql25Plan(category), ir.PostgresStorageBinding{}, sql25CategoryColumn, sql25Marker, 8); err == nil {
			t.Fatal("the zero storage binding must refuse")
		} else if !strings.Contains(err.Error(), "postgres") {
			t.Fatalf("binding refusal = %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// Owned-namespace delta audit (pg-sql-25): the live legs own
// verify_sql25v1_* exclusively; the hook wraps the TestMain chain.
// ---------------------------------------------------------------------------

var (
	sql25FixturesMu      sync.Mutex
	sql25TrackedFixtures = map[string]bool{}
)

// sql25RunDeltaAudit is the outermost delta audit in the TestMain chain
// (main_test.go): pg-sql-25 owns the disjoint verify_sql25v1_* namespace
// exclusively and audits it through the parameterized runner.
func sql25RunDeltaAudit(inner func() int) int {
	return runVerifyNamespaceDeltaAudit("verify_sql25v1_", &sql25FixturesMu, sql25TrackedFixtures, inner)
}

// ---------------------------------------------------------------------------
// Live real-PostgreSQL-18 legs (owned namespace verify_sql25v1_*). The legs
// follow the package-wide live convention: the shared testPostgresURL gate
// skips when LOOM_TEST_POSTGRES_URL is absent (no CI job carries it for this
// package — the convention every merged sibling's live legs follow), and
// when the URL is present every leg is required: newOwnedService fails
// loudly on any service or fixture fault and the TestMain delta audit fails
// on a lost snapshot, so a configured run can never pass on a skip. The
// ticket's required integration is established by running this battery
// against a real PostgreSQL 18 service with zero skips, recorded in the PR.
// Fixtures ride the shared owned-service lifecycle; every expectation is a
// literal row set or a direct-SQL witness computed independently of the
// renderer.
// ---------------------------------------------------------------------------

func sortRows(rows [][]string) {
	sort.Slice(rows, func(left, right int) bool {
		for column := range rows[left] {
			if rows[left][column] != rows[right][column] {
				return rows[left][column] < rows[right][column]
			}
		}
		return false
	})
}

func sql25LiveService(t *testing.T) (*pgir02Service, ir.PostgresStorageBinding) {
	t.Helper()
	testPostgresURL(t)
	svc := newOwnedService(t, 0, "verify_sql25v1")
	return svc, bindingFor(t, svc.schema)
}

func sql25PatientPlan(authPaths ...string) ir.PhysicalPlan {
	return withReturn(scopedPlan(itProject, itGeneration, authPaths, false),
		sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalPreserveNull, nil))
}

func sql25RenderLive(t *testing.T, plan ir.PhysicalPlan, binding ir.PostgresStorageBinding, maxValues int) RenderedCategoryScan {
	t.Helper()
	rendered, err := RenderCategoryScan(plan, binding, sql25CategoryColumn, sql25Marker, maxValues)
	if err != nil {
		t.Fatalf("live RenderCategoryScan: %v", err)
	}
	return rendered
}

func TestCategoryScanLiveExactRowsScopeAndThreeStates(t *testing.T) {
	svc, binding := sql25LiveService(t)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-female-1", "FHIR/Patient", map[string]any{"gender": "female"})
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-female-2", "FHIR/Patient", map[string]any{"gender": "female"})
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-male", "FHIR/Patient", map[string]any{"gender": "male"})
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-absent", "FHIR/Patient", map[string]any{"active": true})
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-null", "FHIR/Patient", map[string]any{"gender": nil})
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-empty", "FHIR/Patient", map[string]any{"gender": ""})
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-false", "FHIR/Patient", map[string]any{"gender": false})
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-zero", "FHIR/Patient", map[string]any{"gender": 0})
	// Decoys: foreign project, foreign generation, foreign auth path, and a
	// foreign resource type, each carrying a gender that must never appear.
	svc.insertResource(t, svc.schema, "sql25-foreign-project", itGeneration, "Patient", "p-decoy-project", "FHIR/Patient", map[string]any{"gender": "decoy-project"})
	svc.insertResource(t, svc.schema, itProject, "sql25-foreign-generation", "Patient", "p-decoy-generation", "FHIR/Patient", map[string]any{"gender": "decoy-generation"})
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-decoy-auth", "FHIR/Encounter", map[string]any{"gender": "decoy-auth"})
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Observation", "o-decoy-type", "FHIR/Observation", map[string]any{"gender": "decoy-type"})

	rendered := sql25RenderLive(t, sql25PatientPlan("FHIR/Patient"), binding, 8)
	got := svc.runRendered(t, rendered.Values)
	want := [][]string{
		// pgx decodes jsonb scalars into Go values, so the stringified page
		// carries decoded text; the direct-SQL witness below compares the
		// raw jsonb forms, including the SQL-NULL/JSON-null distinction.
		{"false", "<null>"}, // ABSENT: presence false, value SQL NULL
		{"true", "false"},   // stored JSON false survives
		{"true", "<null>"},  // stored JSON null, separated from ABSENT by presence
		{"true", "0"},       // falsy numbers survive
		{"true", ""},        // empty string survives
		{"true", "female"},  // duplicate contributors collapse to one group
		{"true", "male"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("category page rows\ngot:  %v\nwant: %v\nsql: %s", got, want, rendered.Values.SQL)
	}
	for _, forbidden := range []string{"decoy-project", "decoy-generation", "decoy-auth", "decoy-type"} {
		for _, row := range got {
			if strings.Contains(row[1], forbidden) {
				t.Fatalf("foreign-scope data leaked into the page: %v", row)
			}
		}
	}

	// Independent direct-SQL witness: the distinct (present, value) multiset
	// recomputed from the stored documents without the renderer's SQL.
	direct := svc.witness(t, `
		SELECT present::text,
		       CASE WHEN value IS NULL THEN '<sql-null>'
		            WHEN jsonb_typeof(value) = 'null' THEN '<json-null>'
		            ELSE value #>> '{}' END
		FROM (
			SELECT COALESCE(jsonb_typeof(payload) = 'object' AND jsonb_exists(payload, 'gender'), false) AS present,
			       CASE WHEN COALESCE(jsonb_typeof(payload) = 'object' AND jsonb_exists(payload, 'gender'), false)
			            THEN payload #> ARRAY['gender'] END AS value
			FROM "`+svc.schema+`"."fhir_resource"
			WHERE project = $1 AND dataset_generation = $2 AND resource_type = 'Patient'
			  AND auth_resource_path = ANY($3::text[])
		) grouped GROUP BY present, value`,
		itProject, itGeneration, []string{"FHIR/Patient"})
	wantDirect := [][]string{
		{"false", "<sql-null>"},
		{"true", "false"}, {"true", "<json-null>"}, {"true", "0"},
		{"true", ""}, {"true", "female"}, {"true", "male"},
	}
	sortedDirect := append([][]string(nil), direct...)
	sortRows(sortedDirect)
	sortRows(wantDirect)
	if !reflect.DeepEqual(sortedDirect, wantDirect) {
		t.Fatalf("direct-SQL witness\ngot:  %v\nwant: %v", sortedDirect, wantDirect)
	}

	// The bounded confirmation agrees with the page: same group count, and
	// re-running the page is row-identical (deterministic page order).
	confirmation := svc.runRendered(t, rendered.Witness)
	if len(confirmation) != len(want) {
		t.Fatalf("witness row count = %d, want %d: %v", len(confirmation), len(want), confirmation)
	}
	again := svc.runRendered(t, rendered.Values)
	if !reflect.DeepEqual(again, got) {
		t.Fatalf("page order was not deterministic:\nfirst:  %v\nsecond: %v", got, again)
	}
}

func TestCategoryScanLiveEmptyPopulationHasNoFabricatedAggregateRow(t *testing.T) {
	svc, binding := sql25LiveService(t)
	svc.insertResource(t, svc.schema, "sql25-empty-observer", itGeneration, "Patient", "p-visible", "FHIR/Patient", map[string]any{"gender": "female"})

	rendered := sql25RenderLive(t, sql25PatientPlan("FHIR/Patient"), binding, 8)
	if got := svc.runRendered(t, rendered.Values); len(got) != 0 {
		t.Fatalf("an empty population must return no rows, never a fabricated aggregate row: %v", got)
	}
	if got := svc.runRendered(t, rendered.Witness); len(got) != 0 {
		t.Fatalf("the bounded confirmation must also be empty: %v", got)
	}
	// The same schema does produce rows once the population is in scope.
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-in-scope", "FHIR/Patient", map[string]any{"gender": "female"})
	if got := svc.runRendered(t, rendered.Values); !reflect.DeepEqual(got, [][]string{{"true", "female"}}) {
		t.Fatalf("in-scope population produced %v", got)
	}
}

func TestCategoryScanLiveBoundedPageDetectsTruncation(t *testing.T) {
	svc, binding := sql25LiveService(t)
	for index, gender := range []string{"a", "b", "c", "d"} {
		svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", fmt.Sprintf("p-%d", index), "FHIR/Patient", map[string]any{"gender": gender})
	}
	rendered := sql25RenderLive(t, sql25PatientPlan("FHIR/Patient"), binding, 2)
	rows := svc.runRendered(t, rendered.Values)
	// maxValues+1 rows: the executor sees more than the page can hold and
	// reports truncation without a second query.
	if len(rows) != 3 {
		t.Fatalf("truncated page = %d rows, want maxValues+1=3: %v", len(rows), rows)
	}
	untruncated := sql25RenderLive(t, sql25PatientPlan("FHIR/Patient"), binding, 4)
	if rows := svc.runRendered(t, untruncated.Values); len(rows) != 4 {
		t.Fatalf("untruncated page = %d rows, want 4: %v", len(rows), rows)
	}
}

func TestCategoryScanLiveOmitNullsReadsJSONNullAsSQLNull(t *testing.T) {
	svc, binding := sql25LiveService(t)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-null", "FHIR/Patient", map[string]any{"gender": nil})
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", "p-absent", "FHIR/Patient", map[string]any{"active": true})
	plan := withReturn(scopedPlan(itProject, itGeneration, []string{"FHIR/Patient"}, false),
		sql25CategoryProjection(sql25CategoryColumn, "gender", ir.PhysicalOmitNulls, nil))
	rendered := sql25RenderLive(t, plan, binding, 8)
	got := svc.runRendered(t, rendered.Values)
	want := [][]string{{"false", "<null>"}, {"true", "<null>"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("omit-nulls readings\ngot:  %v\nwant: %v", got, want)
	}
}
