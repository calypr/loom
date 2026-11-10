package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

// ---------------------------------------------------------------------------
// pg-sql-05: scalar filter comparisons and boolean predicates as registered
// in predicates.go. This file pins the surface offline — byte-exact golden
// SQL, full ordered-argument literals, truth-table null-guard placement, and
// fail-closed rejection before any SQL is produced. The live matrix that
// executes the independent-row acceptance cells against real PostgreSQL 18
// lives in predicates_pg18_test.go.
// ---------------------------------------------------------------------------

const (
	pgsq05Project = "pgsq05-proj"
	pgsq05Gen     = "gen-1"
	pgsq05Path    = "FHIR/Patient"
)

// pgsq05Plan is the scoped direct root scan over one resource type with a
// positional logical-id return, matching the basePlan shape of the package.
func pgsq05Plan(resourceType string) ir.PhysicalPlan {
	return ir.PhysicalPlan{
		Version: 1,
		Engine:  ir.PhysicalEngineAQL,
		BindVars: map[string]any{
			"root_collection":                  resourceType,
			"project":                          pgsq05Project,
			"dataset_generation":               pgsq05Gen,
			"auth_resource_paths":              []string{pgsq05Path},
			"auth_resource_paths_unrestricted": false,
			"scope_allowed":                    true,
		},
		Operations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
				{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
			}}},
		},
	}
}

// pgsq05AddFilter inserts one FILTER operation after the root scan and
// before the RETURN terminal.
func pgsq05AddFilter(plan ir.PhysicalPlan, operation ir.PhysicalOperation) ir.PhysicalPlan {
	operations := make([]ir.PhysicalOperation, 0, len(plan.Operations)+1)
	operations = append(operations, plan.Operations[0])
	operations = append(operations, operation)
	operations = append(operations, plan.Operations[1:]...)
	plan.Operations = operations
	return plan
}

func pgsq05FilterByPredicate(predicate ir.PhysicalPredicate) ir.PhysicalOperation {
	return ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: predicate}}
}

func pgsq05FilterByExpression(expression ir.PhysicalPredicateExpression) ir.PhysicalOperation {
	return ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &expression}}
}

func pgsq05ComparisonLeaf(predicate ir.PhysicalPredicate) ir.PhysicalPredicateExpression {
	return ir.PhysicalPredicateExpression{Kind: ir.PhysicalComparisonPredicate, Comparison: &predicate}
}

// pgsq05SelectorPredicate builds the exact closed selector-expression
// comparison shape lowering emits (lower/typed_predicate.go
// lowerTypedPredicate): array-cardinality EMPTY_ON_NULL EXTRACT anchored at
// the root document payload with a bind-backed right value.
func pgsq05SelectorPredicate(resourceType, selectorPath, operator string,
	quantifier spec.ArrayQuantifier, kind spec.FilterValueKind, bindKey string, withRight bool) ir.PhysicalPredicate {
	predicate := ir.PhysicalPredicate{
		Operator:   operator,
		Quantifier: quantifier,
		ValueKind:  kind,
		LeftExpression: &ir.PhysicalExpression{
			Kind:         ir.PhysicalExtractExpression,
			Cardinality:  ir.PhysicalArrayCardinality,
			NullBehavior: ir.PhysicalEmptyOnNull,
			Extract: &ir.PhysicalExtract{
				Source:       ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
				ResourceType: resourceType,
				Selector:     pgsq05SelectorNoT(selectorPath),
			},
		},
	}
	if withRight {
		predicate.Right = &ir.PhysicalValue{BindKey: bindKey}
	}
	return predicate
}

func pgsq05SelectorNoT(path string) spec.Selector {
	selector, err := spec.ParseSelector(path)
	if err != nil {
		panic("fixture selector " + path + ": " + err.Error())
	}
	return selector
}

// pgsq05WhereClause extracts the WHERE portion of a rendered statement so
// goldens pin the predicate text without restating the canonical scope
// frame that scope.go owns.
func pgsq05WhereClause(t *testing.T, rendered compiler.RenderedPostgresPhysicalPlan) string {
	t.Helper()
	marker := " AND r0.\"resource_type\" = $4"
	start := strings.Index(rendered.SQL, marker)
	if start < 0 {
		t.Fatalf("statement does not carry the canonical scope frame: %s", rendered.SQL)
	}
	rest := rendered.SQL[start+len(marker):]
	end := strings.Index(rest, " ORDER BY ")
	if end < 0 {
		t.Fatalf("statement has no ORDER BY terminal: %s", rendered.SQL)
	}
	clause := rest[:end]
	if !strings.HasPrefix(clause, " AND ") {
		t.Fatalf("filter clause does not extend the WHERE list: %q", clause)
	}
	return strings.TrimPrefix(clause, " AND ")
}

// pgsq05ScopeArgs are the four canonical scope parameters every restricted
// statement binds before any filter parameters.
func pgsq05ScopeArgs(resourceType string) []compiler.PostgreSQLParameter {
	return []compiler.PostgreSQLParameter{
		{Type: compiler.PostgreSQLParameterText, Value: pgsq05Project},
		{Type: compiler.PostgreSQLParameterText, Value: pgsq05Gen},
		{Type: compiler.PostgreSQLParameterJSONB, Value: json.RawMessage(`["FHIR/Patient"]`)},
		{Type: compiler.PostgreSQLParameterText, Value: resourceType},
	}
}

func pgsq05Reject(t *testing.T, plan ir.PhysicalPlan, context string) {
	t.Helper()
	rendered, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err == nil {
		t.Fatalf("%s: render succeeded, want fail-closed rejection\nSQL: %s", context, rendered.SQL)
	}
	if rendered.SQL != "" || rendered.Args != nil || rendered.OutputColumns != nil {
		t.Fatalf("%s: rejection leaked a rendered plan: %#v", context, rendered)
	}
	if !errors.Is(err, ErrUnsupportedPostgresPlan) {
		t.Fatalf("%s: error = %v (%T), want ErrUnsupportedPostgresPlan", context, err, err)
	}
}

// pgsq05RejectAny pins "rejected before SQL" for shapes the frozen ir plan
// validator itself refuses: the error class is ir's, but no plan data may
// leak and no SQL may be produced.
func pgsq05RejectAny(t *testing.T, plan ir.PhysicalPlan, context string) {
	t.Helper()
	rendered, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err == nil {
		t.Fatalf("%s: render succeeded, want rejection\nSQL: %s", context, rendered.SQL)
	}
	if rendered.SQL != "" || rendered.Args != nil || rendered.OutputColumns != nil {
		t.Fatalf("%s: rejection leaked a rendered plan: %#v", context, rendered)
	}
}

func pgsq05JSONRaw(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal fixture value %#v: %v", value, err)
	}
	return raw
}

// ---------------------------------------------------------------------------
// Registered selector-expression comparisons: golden SQL + ordered args.
// ---------------------------------------------------------------------------

func TestRenderSelectorComparisonGoldenAnyEquality(t *testing.T) {
	plan := pgsq05Plan("Patient")
	plan.BindVars["gender_value"] = "female"
	plan = pgsq05AddFilter(plan, pgsq05FilterByPredicate(pgsq05SelectorPredicate(
		"Patient", "gender", "EQUALS", spec.QuantifierAny, spec.FilterCode, "gender_value", true)))
	rendered := renderOK(t, plan)

	wantClause := `(COALESCE(jsonb_typeof(r0."payload" #> $5::text[]), 'null') <> 'null'` +
		` AND (r0."payload" #> $5::text[]) = ($6::jsonb))`
	if got := pgsq05WhereClause(t, rendered); got != wantClause {
		t.Fatalf("ANY equality clause =\n%s\nwant\n%s", got, wantClause)
	}
	wantArgs := append(pgsq05ScopeArgs("Patient"),
		compiler.PostgreSQLParameter{Type: compiler.PostgreSQLParameterText, Value: "{gender}"},
		compiler.PostgreSQLParameter{Type: compiler.PostgreSQLParameterJSONB, Value: json.RawMessage(`"female"`)},
	)
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("ANY equality args = %#v, want %#v", rendered.Args, wantArgs)
	}
}

func TestRenderSelectorComparisonEmptyQuantifierIsAny(t *testing.T) {
	plan := pgsq05Plan("Patient")
	plan.BindVars["gender_value"] = "female"
	plan = pgsq05AddFilter(plan, pgsq05FilterByPredicate(pgsq05SelectorPredicate(
		"Patient", "gender", "EQUALS", "", spec.FilterString, "gender_value", true)))
	anyPlan := pgsq05Plan("Patient")
	anyPlan.BindVars["gender_value"] = "female"
	anyPlan = pgsq05AddFilter(anyPlan, pgsq05FilterByPredicate(pgsq05SelectorPredicate(
		"Patient", "gender", "EQUALS", spec.QuantifierAny, spec.FilterString, "gender_value", true)))
	if a, b := renderOK(t, plan).SQL, renderOK(t, anyPlan).SQL; a != b {
		t.Fatalf("empty quantifier must equal ANY:\n%s\n%s", a, b)
	}
}

func TestRenderSelectorComparisonNoneIsNegatedAny(t *testing.T) {
	plan := pgsq05Plan("Patient")
	plan.BindVars["gender_value"] = "female"
	plan = pgsq05AddFilter(plan, pgsq05FilterByPredicate(pgsq05SelectorPredicate(
		"Patient", "gender", "EQUALS", spec.QuantifierNone, spec.FilterCode, "gender_value", true)))
	rendered := renderOK(t, plan)
	wantClause := `NOT ((COALESCE(jsonb_typeof(r0."payload" #> $5::text[]), 'null') <> 'null'` +
		` AND (r0."payload" #> $5::text[]) = ($6::jsonb)))`
	if got := pgsq05WhereClause(t, rendered); got != wantClause {
		t.Fatalf("NONE clause =\n%s\nwant\n%s", got, wantClause)
	}
}

func TestRenderSelectorComparisonAllRequiresTheItem(t *testing.T) {
	// Over the null-filtered 0..1-item multiset ALL coincides with ANY
	// item-for-item but must still read false for a missing item; the
	// presence conjunct is what makes that true, so ALL and ANY share the
	// total form and NOT (ALL) stays unguarded.
	plan := pgsq05Plan("Patient")
	plan.BindVars["active_value"] = true
	plan = pgsq05AddFilter(plan, pgsq05FilterByPredicate(pgsq05SelectorPredicate(
		"Patient", "active", "EQUALS", spec.QuantifierAll, spec.FilterBoolean, "active_value", true)))
	rendered := renderOK(t, plan)
	wantClause := `(COALESCE(jsonb_typeof(r0."payload" #> $5::text[]), 'null') <> 'null'` +
		` AND (r0."payload" #> $5::text[]) = ($6::jsonb))`
	if got := pgsq05WhereClause(t, rendered); got != wantClause {
		t.Fatalf("ALL clause =\n%s\nwant\n%s", got, wantClause)
	}
	if got := rendered.Args[5]; got.Type != compiler.PostgreSQLParameterJSONB || string(got.Value.(json.RawMessage)) != "true" {
		t.Fatalf("BOOLEAN equality arg = %#v", got)
	}
	// The quantifier result is total, so NOT needs no null guard.
	negated := pgsq05Plan("Patient")
	negated.BindVars["active_value"] = true
	negated = pgsq05AddFilter(negated, pgsq05FilterByExpression(ir.PhysicalPredicateExpression{
		Kind: ir.PhysicalNotPredicate,
		Children: []ir.PhysicalPredicateExpression{pgsq05ComparisonLeaf(pgsq05SelectorPredicate(
			"Patient", "active", "EQUALS", spec.QuantifierAll, spec.FilterBoolean, "active_value", true))},
	}))
	negatedRendered := renderOK(t, negated)
	if !strings.HasPrefix(pgsq05WhereClause(t, negatedRendered), "NOT (") {
		t.Fatalf("NOT over a total quantifier comparison must stay plain:\n%s", negatedRendered.SQL)
	}
	if strings.Contains(pgsq05WhereClause(t, negatedRendered), "COALESCE(NOT") {
		t.Fatalf("total quantifier comparison must not get the null guard:\n%s", negatedRendered.SQL)
	}
}

func TestRenderSelectorComparisonNotEqualsUsesTotalDestruction(t *testing.T) {
	plan := pgsq05Plan("Patient")
	plan.BindVars["gender_value"] = "male"
	plan = pgsq05AddFilter(plan, pgsq05FilterByPredicate(pgsq05SelectorPredicate(
		"Patient", "gender", "NOT_EQUALS", spec.QuantifierAny, spec.FilterCode, "gender_value", true)))
	rendered := renderOK(t, plan)
	wantClause := `(COALESCE(jsonb_typeof(r0."payload" #> $5::text[]), 'null') <> 'null'` +
		` AND (r0."payload" #> $5::text[]) <> ($6::jsonb))`
	if got := pgsq05WhereClause(t, rendered); got != wantClause {
		t.Fatalf("NOT_EQUALS clause =\n%s\nwant\n%s", got, wantClause)
	}
}

func TestRenderSelectorComparisonExistenceKeepsMissingDistinct(t *testing.T) {
	plan := pgsq05Plan("Patient")
	plan = pgsq05AddFilter(plan, pgsq05FilterByPredicate(pgsq05SelectorPredicate(
		"Patient", "birthDate", "EXISTS", "", "", "unused", false)))
	rendered := renderOK(t, plan)
	if got := pgsq05WhereClause(t, rendered); got != `COALESCE(jsonb_typeof(r0."payload" #> $5::text[]), 'null') <> 'null'` {
		t.Fatalf("selector EXISTS clause = %s", got)
	}
	missingPlan := pgsq05Plan("Patient")
	missingPlan = pgsq05AddFilter(missingPlan, pgsq05FilterByPredicate(pgsq05SelectorPredicate(
		"Patient", "birthDate", "MISSING", "", "", "unused", false)))
	missingRendered := renderOK(t, missingPlan)
	if got := pgsq05WhereClause(t, missingRendered); got != `NOT (COALESCE(jsonb_typeof(r0."payload" #> $5::text[]), 'null') <> 'null')` {
		t.Fatalf("selector MISSING clause = %s", got)
	}
	if len(rendered.Args) != 5 || len(missingRendered.Args) != 5 {
		t.Fatalf("existence comparisons bind scope plus one path parameter only: %#v", rendered.Args)
	}
}

func TestRenderSelectorComparisonInBindsTopLevelMembership(t *testing.T) {
	plan := pgsq05Plan("Patient")
	plan.BindVars["gender_values"] = []any{"male", "female"}
	plan = pgsq05AddFilter(plan, pgsq05FilterByPredicate(pgsq05SelectorPredicate(
		"Patient", "gender", "IN", spec.QuantifierAny, spec.FilterCode, "gender_values", true)))
	rendered := renderOK(t, plan)
	wantClause := `(COALESCE(jsonb_typeof(r0."payload" #> $5::text[]), 'null') <> 'null'` +
		` AND EXISTS (SELECT 1 FROM jsonb_array_elements($6::jsonb) AS loom_selector_item WHERE loom_selector_item = (r0."payload" #> $5::text[])))`
	if got := pgsq05WhereClause(t, rendered); got != wantClause {
		t.Fatalf("selector IN clause =\n%s\nwant\n%s", got, wantClause)
	}
	if got := rendered.Args[5]; got.Type != compiler.PostgreSQLParameterJSONB ||
		string(got.Value.(json.RawMessage)) != `["male","female"]` {
		t.Fatalf("selector IN arg = %#v", got)
	}

	// An empty list binds []: EXISTS reads false, matching x IN [].
	empty := pgsq05Plan("Patient")
	empty.BindVars["gender_values"] = []any{}
	empty = pgsq05AddFilter(empty, pgsq05FilterByPredicate(pgsq05SelectorPredicate(
		"Patient", "gender", "IN", spec.QuantifierAny, spec.FilterCode, "gender_values", true)))
	emptyRendered := renderOK(t, empty)
	if got := emptyRendered.Args[5]; got.Type != compiler.PostgreSQLParameterJSONB ||
		string(got.Value.(json.RawMessage)) != `[]` {
		t.Fatalf("empty selector IN arg = %#v, want []", got)
	}
}

func TestRenderSelectorComparisonTemporalOrdersByGuardedInstant(t *testing.T) {
	plan := pgsq05Plan("Patient")
	plan.BindVars["birth_value"] = "2024-03-15"
	plan = pgsq05AddFilter(plan, pgsq05FilterByPredicate(pgsq05SelectorPredicate(
		"Patient", "birthDate", "GT", spec.QuantifierAny, spec.FilterDate, "birth_value", true)))
	rendered := renderOK(t, plan)
	extraction := `r0."payload" #> $5::text[]`
	text := `((` + extraction + `) #>> '{}')`
	wantClause := `(COALESCE(jsonb_typeof(` + extraction + `), 'null') <> 'null'` +
		` AND COALESCE((CASE` +
		` WHEN jsonb_typeof(` + extraction + `) = 'string' AND ` + text + ` ~ '^([1-9][0-9]{3}-(0[13578]|1[02])-(0[1-9]|[12][0-9]|3[01])|[1-9][0-9]{3}-(0[469]|11)-(0[1-9]|[12][0-9]|30)|[1-9][0-9]{3}-02-(0[1-9]|1[0-9]|2[0-8])|(0[48]|[2468][048]|[13579][26])00-02-29|[0-9]{2}(0[48]|[2468][048]|[13579][26])-02-29)$'` +
		` THEN (` + text + ` || 'T00:00:00+00:00')::timestamptz` +
		` WHEN jsonb_typeof(` + extraction + `) = 'string' AND ` + text + ` ~ '^([1-9][0-9]{3}-(0[13578]|1[02])-(0[1-9]|[12][0-9]|3[01])|[1-9][0-9]{3}-(0[469]|11)-(0[1-9]|[12][0-9]|30)|[1-9][0-9]{3}-02-(0[1-9]|1[0-9]|2[0-8])|(0[48]|[2468][048]|[13579][26])00-02-29|[0-9]{2}(0[48]|[2468][048]|[13579][26])-02-29)T(0[0-9]|1[0-9]|2[0-3]):[0-5][0-9](:[0-5][0-9]([.][0-9]+)?)?(Z|[+-]((0[0-9]|1[0-4]):[0-5][0-9]|15:[0-5][0-9]))$'` +
		` THEN ` + text + `::timestamptz` +
		` WHEN jsonb_typeof(` + extraction + `) = 'string' AND ` + text + ` ~ '^([1-9][0-9]{3}-(0[13578]|1[02])-(0[1-9]|[12][0-9]|3[01])|[1-9][0-9]{3}-(0[469]|11)-(0[1-9]|[12][0-9]|30)|[1-9][0-9]{3}-02-(0[1-9]|1[0-9]|2[0-8])|(0[48]|[2468][048]|[13579][26])00-02-29|[0-9]{2}(0[48]|[2468][048]|[13579][26])-02-29)T(0[0-9]|1[0-9]|2[0-3]):[0-5][0-9](:[0-5][0-9]([.][0-9]+)?)?$'` +
		` THEN (` + text + ` || '+00:00')::timestamptz` +
		` END) > $6, false))`
	if got := pgsq05WhereClause(t, rendered); got != wantClause {
		t.Fatalf("DATE GT clause =\n%s\nwant\n%s", got, wantClause)
	}
	arg := rendered.Args[5]
	if arg.Type != compiler.PostgreSQLParameterTimestamptz {
		t.Fatalf("DATE literal arg type = %q, want timestamptz", arg.Type)
	}
	if stamp, ok := arg.Value.(time.Time); !ok || !stamp.Equal(time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("DATE literal arg = %#v, want 2024-03-15T00:00:00Z", arg.Value)
	}
}

func TestRenderSelectorComparisonDateTimeLiteralMustBeFullRFC3339(t *testing.T) {
	good := pgsq05Plan("Patient")
	good.BindVars["when_value"] = "2024-03-15T10:20:30+02:00"
	good = pgsq05AddFilter(good, pgsq05FilterByPredicate(pgsq05SelectorPredicate(
		"Patient", "birthDate", "GTE", spec.QuantifierAny, spec.FilterDateTime, "when_value", true)))
	rendered := renderOK(t, good)
	arg := rendered.Args[5]
	if arg.Type != compiler.PostgreSQLParameterTimestamptz {
		t.Fatalf("DATE_TIME arg type = %q", arg.Type)
	}
	want := time.Date(2024, 3, 15, 8, 20, 30, 0, time.UTC)
	if stamp, ok := arg.Value.(time.Time); !ok || !stamp.Equal(want) {
		t.Fatalf("DATE_TIME arg = %#v, want %s", arg.Value, want)
	}

	for _, literal := range []string{"2024", "2024-03", "2024-03-15", "not-a-date"} {
		plan := pgsq05Plan("Patient")
		plan.BindVars["when_value"] = literal
		plan = pgsq05AddFilter(plan, pgsq05FilterByPredicate(pgsq05SelectorPredicate(
			"Patient", "birthDate", "LT", spec.QuantifierAny, spec.FilterDateTime, "when_value", true)))
		if _, err := RenderPhysicalPlan(plan, mustBinding(t)); err == nil {
			t.Fatalf("DATE_TIME literal %q accepted, want fail-closed rejection", literal)
		}
	}
}

func TestRenderSelectorComparisonIntegerAndDecimalBindTypedScalars(t *testing.T) {
	// The renderer does not consult the FHIR schema for value kinds (spec
	// + lowering own that pairing); what is pinned here is the bind-type
	// discipline: INTEGER accepts only int64 and DECIMAL only float64.
	plan := pgsq05Plan("Patient")
	plan.BindVars["score_value"] = int64(7)
	plan = pgsq05AddFilter(plan, pgsq05FilterByPredicate(pgsq05SelectorPredicate(
		"Patient", "gender", "GTE", spec.QuantifierAny, spec.FilterInteger, "score_value", true)))
	rendered := renderOK(t, plan)
	if got := rendered.Args[5]; got.Type != compiler.PostgreSQLParameterJSONB ||
		string(got.Value.(json.RawMessage)) != "7" {
		t.Fatalf("INTEGER arg = %#v", got)
	}
	for _, mismatched := range []any{float64(7), "7", 7} {
		plan := pgsq05Plan("Patient")
		plan.BindVars["score_value"] = mismatched
		plan = pgsq05AddFilter(plan, pgsq05FilterByPredicate(pgsq05SelectorPredicate(
			"Patient", "gender", "GTE", spec.QuantifierAny, spec.FilterInteger, "score_value", true)))
		if _, err := RenderPhysicalPlan(plan, mustBinding(t)); err == nil {
			t.Fatalf("INTEGER bind %#v accepted, want rejection", mismatched)
		}
	}

	decimalPlan := pgsq05Plan("Patient")
	decimalPlan.BindVars["score_value"] = 2.5
	decimalPlan = pgsq05AddFilter(decimalPlan, pgsq05FilterByPredicate(pgsq05SelectorPredicate(
		"Patient", "gender", "LTE", spec.QuantifierAny, spec.FilterDecimal, "score_value", true)))
	decimalRendered := renderOK(t, decimalPlan)
	if got := decimalRendered.Args[5]; got.Type != compiler.PostgreSQLParameterJSONB ||
		string(got.Value.(json.RawMessage)) != "2.5" {
		t.Fatalf("DECIMAL arg = %#v", got)
	}
}

// ---------------------------------------------------------------------------
// Registered selector shape: everything outside it fails closed.
// ---------------------------------------------------------------------------

func TestRenderSelectorComparisonFailsClosedOutsideClosedShape(t *testing.T) {
	// Repeated step.
	iterPlan := pgsq05Plan("Patient")
	iterPlan.BindVars["v"] = "x"
	iterPlan = pgsq05AddFilter(iterPlan, pgsq05FilterByPredicate(pgsq05SelectorPredicate(
		"Patient", "link[].other.reference", "EQUALS", spec.QuantifierAny, spec.FilterString, "v", true)))
	pgsq05Reject(t, iterPlan, "repeated selector step")

	// Fallbacks.
	fallback := pgsq05SelectorPredicate("Patient", "gender", "EQUALS", spec.QuantifierAny, spec.FilterString, "v", true)
	fallback.LeftExpression.Extract.Fallbacks = []ir.PhysicalSelectorFallback{{
		Source:       ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
		ResourceType: "Patient",
		Selector:     pgsq05SelectorNoT("active"),
	}}
	fallbackPlan := pgsq05Plan("Patient")
	fallbackPlan.BindVars["v"] = "x"
	fallbackPlan = pgsq05AddFilter(fallbackPlan, pgsq05FilterByPredicate(fallback))
	pgsq05Reject(t, fallbackPlan, "selector fallback")

	// DISTINCT.
	distinct := pgsq05SelectorPredicate("Patient", "gender", "EQUALS", spec.QuantifierAny, spec.FilterString, "v", true)
	distinct.LeftExpression.Extract.Distinct = true
	distinctPlan := pgsq05Plan("Patient")
	distinctPlan.BindVars["v"] = "x"
	distinctPlan = pgsq05AddFilter(distinctPlan, pgsq05FilterByPredicate(distinct))
	pgsq05Reject(t, distinctPlan, "selector DISTINCT")

	// Non-EMPTY_ON_NULL behavior.
	behavior := pgsq05SelectorPredicate("Patient", "gender", "EQUALS", spec.QuantifierAny, spec.FilterString, "v", true)
	behavior.LeftExpression.NullBehavior = ir.PhysicalPreserveNull
	behaviorPlan := pgsq05Plan("Patient")
	behaviorPlan.BindVars["v"] = "x"
	behaviorPlan = pgsq05AddFilter(behaviorPlan, pgsq05FilterByPredicate(behavior))
	pgsq05Reject(t, behaviorPlan, "selector null behavior outside registered table")

	// Resource type contradicting the scanned row.
	contradict := pgsq05SelectorPredicate("Observation", "status", "EQUALS", spec.QuantifierAny, spec.FilterString, "v", true)
	contradictPlan := pgsq05Plan("Patient")
	contradictPlan.BindVars["v"] = "x"
	contradictPlan = pgsq05AddFilter(contradictPlan, pgsq05FilterByPredicate(contradict))
	pgsq05Reject(t, contradictPlan, "selector resource type contradiction")

	// Source not anchored at the document payload.
	source := pgsq05SelectorPredicate("Patient", "gender", "EQUALS", spec.QuantifierAny, spec.FilterString, "v", true)
	source.LeftExpression.Extract.Source = ir.PhysicalValue{Variable: "root"}
	sourcePlan := pgsq05Plan("Patient")
	sourcePlan.BindVars["v"] = "x"
	sourcePlan = pgsq05AddFilter(sourcePlan, pgsq05FilterByPredicate(source))
	pgsq05Reject(t, sourcePlan, "selector source without payload anchor")

	// Null-literal right value constants the 0..1-item comparison.
	nullRight := pgsq05SelectorPredicate("Patient", "gender", "EQUALS", spec.QuantifierAny, spec.FilterString, "v", true)
	nullRightPlan := pgsq05Plan("Patient")
	nullRightPlan.BindVars["v"] = json.RawMessage("null")
	nullRightPlan = pgsq05AddFilter(nullRightPlan, pgsq05FilterByPredicate(nullRight))
	pgsq05Reject(t, nullRightPlan, "selector comparison against a null literal")

	// Operator/kind incompatibility inside the selector form.
	ordering := pgsq05SelectorPredicate("Patient", "gender", "GT", spec.QuantifierAny, spec.FilterString, "v", true)
	orderingPlan := pgsq05Plan("Patient")
	orderingPlan.BindVars["v"] = "a"
	orderingPlan = pgsq05AddFilter(orderingPlan, pgsq05FilterByPredicate(ordering))
	pgsq05Reject(t, orderingPlan, "GT over STRING kind in selector form")

	// Existence carrying a right value (the frozen ir validator refuses
	// the shape outright).
	existing := pgsq05SelectorPredicate("Patient", "gender", "EXISTS", "", "", "v", true)
	existingPlan := pgsq05Plan("Patient")
	existingPlan.BindVars["v"] = "a"
	existingPlan = pgsq05AddFilter(existingPlan, pgsq05FilterByPredicate(existing))
	pgsq05RejectAny(t, existingPlan, "selector EXISTS carrying a right value")

	// IN with a non-list bind.
	inBind := pgsq05SelectorPredicate("Patient", "gender", "IN", spec.QuantifierAny, spec.FilterString, "v", true)
	inPlan := pgsq05Plan("Patient")
	inPlan.BindVars["v"] = "a"
	inPlan = pgsq05AddFilter(inPlan, pgsq05FilterByPredicate(inBind))
	pgsq05Reject(t, inPlan, "selector IN with a non-list bind")

	// CONTAINS_TEXT keeps its registered gap (same as the direct form).
	contains := pgsq05SelectorPredicate("Patient", "gender", "CONTAINS_TEXT", spec.QuantifierAny, spec.FilterString, "v", true)
	containsPlan := pgsq05Plan("Patient")
	containsPlan.BindVars["v"] = "a"
	containsPlan = pgsq05AddFilter(containsPlan, pgsq05FilterByPredicate(contains))
	pgsq05Reject(t, containsPlan, "selector CONTAINS_TEXT stays a gap")

	// A quantifier on the direct value form stays refused (pg-sql-02).
	quantified := ir.PhysicalPredicate{
		Operator:   "EQUALS",
		Quantifier: spec.QuantifierAny,
		Left:       ir.PhysicalValue{Variable: "root", Path: []string{"status"}},
		Right:      &ir.PhysicalValue{BindKey: "v"},
	}
	quantifiedPlan := pgsq05Plan("Patient")
	quantifiedPlan.BindVars["v"] = "x"
	quantifiedPlan = pgsq05AddFilter(quantifiedPlan, pgsq05FilterByPredicate(quantified))
	pgsq05Reject(t, quantifiedPlan, "quantifier on the direct value form")

	// Correlated predicates stay refused (the frozen ir validator rejects
	// an incomplete correlation before the renderer is reached).
	correlated := pgsq05Plan("Patient")
	correlated = pgsq05AddFilter(correlated, pgsq05FilterByPredicate(ir.PhysicalPredicate{
		Operator:    "EXISTS",
		Correlation: &ir.PhysicalCorrelation{ResourceType: "Patient"},
	}))
	pgsq05RejectAny(t, correlated, "correlated predicate")

	// Unknown operators are rejected before SQL (ir owns the operator
	// alphabet; LIKE is not in it).
	unknown := pgsq05Plan("Patient")
	unknown.BindVars["v"] = "x"
	unknown = pgsq05AddFilter(unknown, pgsq05FilterByPredicate(ir.PhysicalPredicate{
		Operator: "LIKE",
		Left:     ir.PhysicalValue{Variable: "root", Path: []string{"status"}},
		Right:    &ir.PhysicalValue{BindKey: "v"},
	}))
	pgsq05RejectAny(t, unknown, "LIKE operator")
}

// ---------------------------------------------------------------------------
// Direct value form: typed filter values.
// ---------------------------------------------------------------------------

func TestRenderDirectTypedColumnTemporalLiterals(t *testing.T) {
	plan := pgsq05Plan("Patient")
	plan.BindVars["at_value"] = "2024-03-15"
	plan = pgsq05AddFilter(plan, pgsq05FilterByPredicate(ir.PhysicalPredicate{
		Operator:  "GTE",
		ValueKind: spec.FilterDate,
		Left:      ir.PhysicalValue{Variable: "root", Path: []string{"recorded_at"}},
		Right:     &ir.PhysicalValue{BindKey: "at_value"},
	}))
	rendered := renderOK(t, plan)
	if got := pgsq05WhereClause(t, rendered); got != `r0."recorded_at" >= $5` {
		t.Fatalf("recorded_at GTE clause = %s", got)
	}
	arg := rendered.Args[4]
	if arg.Type != compiler.PostgreSQLParameterTimestamptz {
		t.Fatalf("recorded_at literal arg type = %q", arg.Type)
	}
	if stamp, ok := arg.Value.(time.Time); !ok || !stamp.Equal(time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("recorded_at literal arg = %#v", arg.Value)
	}

	// Partial literals and kind contradictions never reach SQL.
	for _, bad := range []struct {
		kind  spec.FilterValueKind
		value any
	}{
		{spec.FilterDate, "2024-03"},
		{spec.FilterDate, "15/03/2024"},
		{spec.FilterDateTime, "2024-03-15"},
		{spec.FilterDateTime, int64(5)},
		{spec.FilterString, "2024-03-15T10:20:30Z"},
	} {
		plan := pgsq05Plan("Patient")
		plan.BindVars["at_value"] = bad.value
		plan = pgsq05AddFilter(plan, pgsq05FilterByPredicate(ir.PhysicalPredicate{
			Operator:  "GTE",
			ValueKind: bad.kind,
			Left:      ir.PhysicalValue{Variable: "root", Path: []string{"recorded_at"}},
			Right:     &ir.PhysicalValue{BindKey: "at_value"},
		}))
		if _, err := RenderPhysicalPlan(plan, mustBinding(t)); err == nil {
			t.Fatalf("recorded_at literal %v kind %q accepted, want rejection", bad.value, bad.kind)
		}
	}

	// A time.Time bind keeps the pg-sql-02 reading.
	timePlan := pgsq05Plan("Patient")
	timePlan.BindVars["at_value"] = time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	timePlan = pgsq05AddFilter(timePlan, pgsq05FilterByPredicate(ir.PhysicalPredicate{
		Operator: "EQUALS",
		Left:     ir.PhysicalValue{Variable: "root", Path: []string{"recorded_at"}},
		Right:    &ir.PhysicalValue{BindKey: "at_value"},
	}))
	if got := pgsq05WhereClause(t, renderOK(t, timePlan)); got != `r0."recorded_at" = $5` {
		t.Fatalf("recorded_at time.Time clause = %s", got)
	}
}

func TestRenderDirectTypedColumnsRejectNontextKinds(t *testing.T) {
	plan := pgsq05Plan("Patient")
	plan.BindVars["v"] = int64(7)
	plan = pgsq05AddFilter(plan, pgsq05FilterByPredicate(ir.PhysicalPredicate{
		Operator:  "EQUALS",
		ValueKind: spec.FilterInteger,
		Left:      ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}},
		Right:     &ir.PhysicalValue{BindKey: "v"},
	}))
	pgsq05Reject(t, plan, "INTEGER kind over a text column")
}

func TestRenderDirectTypedPayloadKinds(t *testing.T) {
	cases := []struct {
		name  string
		kind  spec.FilterValueKind
		value any
		raw   string
	}{
		{"string", spec.FilterString, "active", `"active"`},
		{"code", spec.FilterCode, "final", `"final"`},
		{"boolean", spec.FilterBoolean, true, "true"},
		{"integer", spec.FilterInteger, int64(7), "7"},
		{"decimal", spec.FilterDecimal, 2.5, "2.5"},
		{"date equality stays raw text", spec.FilterDate, "2024-03-15", `"2024-03-15"`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			plan := pgsq05Plan("Patient")
			plan.BindVars["v"] = testCase.value
			plan = pgsq05AddFilter(plan, pgsq05FilterByPredicate(ir.PhysicalPredicate{
				Operator:  "EQUALS",
				ValueKind: testCase.kind,
				Left:      ir.PhysicalValue{Variable: "root", Path: []string{"note"}},
				Right:     &ir.PhysicalValue{BindKey: "v"},
			}))
			rendered := renderOK(t, plan)
			wantClause := `(r0."payload" #> $5::text[]) = ($6::jsonb)`
			if got := pgsq05WhereClause(t, rendered); got != wantClause {
				t.Fatalf("clause = %s, want %s", got, wantClause)
			}
			if got := rendered.Args[5]; got.Type != compiler.PostgreSQLParameterJSONB ||
				string(got.Value.(json.RawMessage)) != testCase.raw {
				t.Fatalf("arg = %#v, want jsonb %s", got, testCase.raw)
			}
			// Go nil is not an explicit null literal: only raw JSON null is.
			nilPlan := pgsq05Plan("Patient")
			nilPlan.BindVars["v"] = nil
			nilPlan = pgsq05AddFilter(nilPlan, pgsq05FilterByPredicate(ir.PhysicalPredicate{
				Operator:  "EQUALS",
				ValueKind: testCase.kind,
				Left:      ir.PhysicalValue{Variable: "root", Path: []string{"note"}},
				Right:     &ir.PhysicalValue{BindKey: "v"},
			}))
			if _, err := RenderPhysicalPlan(nilPlan, mustBinding(t)); err == nil {
				t.Fatalf("Go nil bind accepted for kind %q, want rejection", testCase.kind)
			}
		})
	}
}

func TestRenderDirectTypedPayloadKindMismatchesRejected(t *testing.T) {
	mismatches := []struct {
		kind  spec.FilterValueKind
		value any
	}{
		{spec.FilterString, int64(1)},
		{spec.FilterBoolean, "true"},
		{spec.FilterInteger, float64(1)},
		{spec.FilterDecimal, "1.5"},
		{spec.FilterDate, int64(1)},
	}
	for _, mismatch := range mismatches {
		plan := pgsq05Plan("Patient")
		plan.BindVars["v"] = mismatch.value
		plan = pgsq05AddFilter(plan, pgsq05FilterByPredicate(ir.PhysicalPredicate{
			Operator:  "EQUALS",
			ValueKind: mismatch.kind,
			Left:      ir.PhysicalValue{Variable: "root", Path: []string{"note"}},
			Right:     &ir.PhysicalValue{BindKey: "v"},
		}))
		if _, err := RenderPhysicalPlan(plan, mustBinding(t)); err == nil {
			t.Fatalf("kind %q with bind %#v accepted, want rejection", mismatch.kind, mismatch.value)
		}
	}
}

func TestRenderDirectTypedPayloadRangeUsesTemporalGuards(t *testing.T) {
	plan := pgsq05Plan("Patient")
	plan.BindVars["v"] = "2024-03-15T10:20:30Z"
	plan = pgsq05AddFilter(plan, pgsq05FilterByPredicate(ir.PhysicalPredicate{
		Operator:  "LT",
		ValueKind: spec.FilterDateTime,
		Left:      ir.PhysicalValue{Variable: "root", Path: []string{"note"}},
		Right:     &ir.PhysicalValue{BindKey: "v"},
	}))
	rendered := renderOK(t, plan)
	clause := pgsq05WhereClause(t, rendered)
	if !strings.Contains(clause, "::timestamptz") || !strings.Contains(clause, "END) < $6") {
		t.Fatalf("DATE_TIME payload LT must compare guarded instants:\n%s", clause)
	}
	if rendered.Args[5].Type != compiler.PostgreSQLParameterTimestamptz {
		t.Fatalf("DATE_TIME payload arg = %#v", rendered.Args[5])
	}
	// NOT must be guarded: the guarded stored side reads NULL for missing
	// and never-comparable stored strings.
	negated := pgsq05Plan("Patient")
	negated.BindVars["v"] = "2024-03-15T10:20:30Z"
	negated = pgsq05AddFilter(negated, pgsq05FilterByExpression(ir.PhysicalPredicateExpression{
		Kind: ir.PhysicalNotPredicate,
		Children: []ir.PhysicalPredicateExpression{pgsq05ComparisonLeaf(ir.PhysicalPredicate{
			Operator:  "LT",
			ValueKind: spec.FilterDateTime,
			Left:      ir.PhysicalValue{Variable: "root", Path: []string{"note"}},
			Right:     &ir.PhysicalValue{BindKey: "v"},
		})},
	}))
	negatedRendered := renderOK(t, negated)
	if !strings.HasPrefix(pgsq05WhereClause(t, negatedRendered), "COALESCE(NOT (") {
		t.Fatalf("NOT over a three-valued temporal comparison must be guarded:\n%s", negatedRendered.SQL)
	}

	// Ordering over an incompatible kind is rejected before SQL.
	stringOrder := pgsq05Plan("Patient")
	stringOrder.BindVars["v"] = "b"
	stringOrder = pgsq05AddFilter(stringOrder, pgsq05FilterByPredicate(ir.PhysicalPredicate{
		Operator:  "GT",
		ValueKind: spec.FilterString,
		Left:      ir.PhysicalValue{Variable: "root", Path: []string{"note"}},
		Right:     &ir.PhysicalValue{BindKey: "v"},
	}))
	pgsq05Reject(t, stringOrder, "GT over STRING kind")
}

// ---------------------------------------------------------------------------
// Boolean predicates: AND / OR / NOT truth-table plumbing.
// ---------------------------------------------------------------------------

func pgsq05PayloadLeaf(bindKey string) ir.PhysicalPredicate {
	return ir.PhysicalPredicate{
		Operator: "EQUALS",
		Left:     ir.PhysicalValue{Variable: "root", Path: []string{"status"}},
		Right:    &ir.PhysicalValue{BindKey: bindKey},
	}
}

func pgsq05ColumnLeaf(bindKey string) ir.PhysicalPredicate {
	return ir.PhysicalPredicate{
		Operator: "EQUALS",
		Left:     ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}},
		Right:    &ir.PhysicalValue{BindKey: bindKey},
	}
}

func TestRenderOrPredicateGoldenAndNullGuardPlacement(t *testing.T) {
	plan := pgsq05Plan("Patient")
	plan.BindVars["a"] = "x"
	plan.BindVars["b"] = "y"
	plan = pgsq05AddFilter(plan, pgsq05FilterByExpression(ir.PhysicalPredicateExpression{
		Kind: ir.PhysicalAnyPredicate,
		Children: []ir.PhysicalPredicateExpression{
			pgsq05ComparisonLeaf(pgsq05PayloadLeaf("a")),
			pgsq05ComparisonLeaf(pgsq05PayloadLeaf("b")),
		},
	}))
	rendered := renderOK(t, plan)
	// Both leaves resolve the same document path, so the whole path binds
	// once and only the two values take separate parameters.
	wantClause := `((r0."payload" #> $5::text[]) = ($6::jsonb)) OR ((r0."payload" #> $5::text[]) = ($7::jsonb))`
	if got := pgsq05WhereClause(t, rendered); got != wantClause {
		t.Fatalf("OR clause =\n%s\nwant\n%s", got, wantClause)
	}

	// Two-valued leaves keep NOT plain.
	twoValued := pgsq05Plan("Patient")
	twoValued.BindVars["a"] = "x"
	twoValued.BindVars["b"] = "y"
	twoValued = pgsq05AddFilter(twoValued, pgsq05FilterByExpression(ir.PhysicalPredicateExpression{
		Kind: ir.PhysicalNotPredicate,
		Children: []ir.PhysicalPredicateExpression{{
			Kind: ir.PhysicalAnyPredicate,
			Children: []ir.PhysicalPredicateExpression{
				pgsq05ComparisonLeaf(pgsq05ColumnLeaf("a")),
				pgsq05ComparisonLeaf(pgsq05ColumnLeaf("b")),
			},
		}},
	}))
	if got := pgsq05WhereClause(t, renderOK(t, twoValued)); !strings.HasPrefix(got, "NOT ((") {
		t.Fatalf("NOT over a two-valued OR must stay plain:\n%s", got)
	}

	// One three-valued leaf makes the disjunction three-valued.
	mixed := pgsq05Plan("Patient")
	mixed.BindVars["a"] = "x"
	mixed.BindVars["b"] = "y"
	mixed = pgsq05AddFilter(mixed, pgsq05FilterByExpression(ir.PhysicalPredicateExpression{
		Kind: ir.PhysicalNotPredicate,
		Children: []ir.PhysicalPredicateExpression{{
			Kind: ir.PhysicalAnyPredicate,
			Children: []ir.PhysicalPredicateExpression{
				pgsq05ComparisonLeaf(pgsq05ColumnLeaf("a")),
				pgsq05ComparisonLeaf(pgsq05PayloadLeaf("b")),
			},
		}},
	}))
	if got := pgsq05WhereClause(t, renderOK(t, mixed)); !strings.HasPrefix(got, "COALESCE(NOT (") {
		t.Fatalf("NOT over a mixed OR must be guarded:\n%s", got)
	}
}

func TestRenderNestedTruthTableShapes(t *testing.T) {
	// NOT(AND(payloadLeaf, columnLeaf)) — the AND inherits the three-
	// valued leaf, so negation is guarded; nesting keeps per-part parens.
	plan := pgsq05Plan("Patient")
	plan.BindVars["a"] = "x"
	plan.BindVars["b"] = "y"
	plan = pgsq05AddFilter(plan, pgsq05FilterByExpression(ir.PhysicalPredicateExpression{
		Kind: ir.PhysicalNotPredicate,
		Children: []ir.PhysicalPredicateExpression{{
			Kind: ir.PhysicalAllPredicate,
			Children: []ir.PhysicalPredicateExpression{
				pgsq05ComparisonLeaf(pgsq05PayloadLeaf("a")),
				pgsq05ComparisonLeaf(pgsq05ColumnLeaf("b")),
			},
		}},
	}))
	got := pgsq05WhereClause(t, renderOK(t, plan))
	want := `COALESCE(NOT (((r0."payload" #> $5::text[]) = ($6::jsonb)) AND (r0."logical_id" = $7)), true)`
	if got != want {
		t.Fatalf("nested clause =\n%s\nwant\n%s", got, want)
	}

	// OR inside AND inside NOT.
	deep := pgsq05Plan("Patient")
	deep.BindVars["a"] = "x"
	deep.BindVars["b"] = "y"
	deep.BindVars["c"] = "z"
	deep = pgsq05AddFilter(deep, pgsq05FilterByExpression(ir.PhysicalPredicateExpression{
		Kind: ir.PhysicalAllPredicate,
		Children: []ir.PhysicalPredicateExpression{
			{
				Kind: ir.PhysicalAnyPredicate,
				Children: []ir.PhysicalPredicateExpression{
					pgsq05ComparisonLeaf(pgsq05ColumnLeaf("a")),
					pgsq05ComparisonLeaf(pgsq05ColumnLeaf("b")),
				},
			},
			{Kind: ir.PhysicalNotPredicate, Children: []ir.PhysicalPredicateExpression{
				pgsq05ComparisonLeaf(pgsq05PayloadLeaf("c")),
			}},
		},
	}))
	deepRendered := renderOK(t, deep)
	clause := pgsq05WhereClause(t, deepRendered)
	wantDeep := `((r0."logical_id" = $5) OR (r0."logical_id" = $6))` +
		` AND (COALESCE(NOT ((r0."payload" #> $7::text[]) = ($8::jsonb)), true))`
	if clause != wantDeep {
		t.Fatalf("deep clause =\n%s\nwant\n%s", clause, wantDeep)
	}
}

func TestRenderBooleanConnectiveShapeRejections(t *testing.T) {
	// The frozen ir validator owns predicate-tree well-formedness, so the
	// renderer's own guards are defense in depth; what is pinned here is
	// that every malformed tree is refused before any SQL exists.
	emptyAny := pgsq05Plan("Patient")
	emptyAny = pgsq05AddFilter(emptyAny, pgsq05FilterByExpression(ir.PhysicalPredicateExpression{Kind: ir.PhysicalAnyPredicate}))
	pgsq05RejectAny(t, emptyAny, "ANY without children")

	emptyAll := pgsq05Plan("Patient")
	emptyAll = pgsq05AddFilter(emptyAll, pgsq05FilterByExpression(ir.PhysicalPredicateExpression{Kind: ir.PhysicalAllPredicate}))
	pgsq05RejectAny(t, emptyAll, "ALL without children")

	twoChildNot := pgsq05Plan("Patient")
	twoChildNot.BindVars["a"] = "x"
	twoChildNot.BindVars["b"] = "y"
	twoChildNot = pgsq05AddFilter(twoChildNot, pgsq05FilterByExpression(ir.PhysicalPredicateExpression{
		Kind: ir.PhysicalNotPredicate,
		Children: []ir.PhysicalPredicateExpression{
			pgsq05ComparisonLeaf(pgsq05ColumnLeaf("a")),
			pgsq05ComparisonLeaf(pgsq05ColumnLeaf("b")),
		},
	}))
	pgsq05RejectAny(t, twoChildNot, "NOT with two children")

	kindlessComparison := pgsq05Plan("Patient")
	kindlessComparison = pgsq05AddFilter(kindlessComparison, pgsq05FilterByExpression(ir.PhysicalPredicateExpression{
		Kind: ir.PhysicalComparisonPredicate,
	}))
	pgsq05RejectAny(t, kindlessComparison, "COMPARISON without payload")
}

// ---------------------------------------------------------------------------
// Freeze checks over the widened surface: determinism and leak-freedom.
// ---------------------------------------------------------------------------

func TestRenderWidenedSurfaceIsDeterministicAndLeakFree(t *testing.T) {
	build := func() ir.PhysicalPlan {
		plan := pgsq05Plan("Patient")
		plan.BindVars["gender_value"] = "female"
		plan.BindVars["birth_value"] = "2024-03-15"
		plan.BindVars["id_value"] = "p-1"
		plan = pgsq05AddFilter(plan, pgsq05FilterByExpression(ir.PhysicalPredicateExpression{
			Kind: ir.PhysicalAnyPredicate,
			Children: []ir.PhysicalPredicateExpression{
				pgsq05ComparisonLeaf(pgsq05SelectorPredicate(
					"Patient", "gender", "EQUALS", spec.QuantifierAny, spec.FilterCode, "gender_value", true)),
				{Kind: ir.PhysicalNotPredicate, Children: []ir.PhysicalPredicateExpression{
					pgsq05ComparisonLeaf(pgsq05SelectorPredicate(
						"Patient", "birthDate", "GTE", spec.QuantifierAny, spec.FilterDate, "birth_value", true)),
				}},
				pgsq05ComparisonLeaf(pgsq05ColumnLeaf("id_value")),
			},
		}))
		return plan
	}
	first := renderOK(t, build())
	second := renderOK(t, build())
	if first.SQL != second.SQL {
		t.Fatalf("render is not deterministic:\n%s\n%s", first.SQL, second.SQL)
	}
	if !reflect.DeepEqual(first.Args, second.Args) {
		t.Fatalf("arguments are not deterministic:\n%#v\n%#v", first.Args, second.Args)
	}
	for _, leaked := range []string{"female", "2024-03-15", "p-1", pgsq05Project} {
		if strings.Contains(first.SQL, leaked) {
			t.Fatalf("plan data %q leaked into SQL: %s", leaked, first.SQL)
		}
	}
}

// ---------------------------------------------------------------------------
// Review findings: numeric ordering type guard and IN list kind discipline.
// ---------------------------------------------------------------------------

func TestRenderNumericOrderingRequiresStoredNumber(t *testing.T) {
	// jsonb orders strings below numbers across types, the reverse of
	// ArangoDB; both forms must refuse to order a non-numeric stored
	// item instead of silently using jsonb type order.
	selectorPlan := pgsq05Plan("Patient")
	selectorPlan.BindVars["score_value"] = int64(5)
	selectorPlan = pgsq05AddFilter(selectorPlan, pgsq05FilterByPredicate(pgsq05SelectorPredicate(
		"Patient", "gender", "GTE", spec.QuantifierAny, spec.FilterInteger, "score_value", true)))
	selectorClause := pgsq05WhereClause(t, renderOK(t, selectorPlan))
	wantSelector := `COALESCE(jsonb_typeof(r0."payload" #> $5::text[]), 'null') <> 'null' AND ` +
		`COALESCE(jsonb_typeof(r0."payload" #> $5::text[]) = 'number' AND ` +
		`(r0."payload" #> $5::text[]) >= ($6::jsonb), false)`
	if !strings.Contains(selectorClause, wantSelector) {
		t.Fatalf("numeric selector ordering clause =\n%s\nwant substring\n%s", selectorClause, wantSelector)
	}

	directPlan := pgsq05Plan("Patient")
	directPlan.BindVars["score_value"] = 2.5
	directPlan = pgsq05AddFilter(directPlan, pgsq05FilterByPredicate(ir.PhysicalPredicate{
		Operator:  "LTE",
		ValueKind: spec.FilterDecimal,
		Left:      ir.PhysicalValue{Variable: "root", Path: []string{"payload", "score"}},
		Right:     &ir.PhysicalValue{BindKey: "score_value"},
	}))
	directClause := pgsq05WhereClause(t, renderOK(t, directPlan))
	wantDirect := `(jsonb_typeof((r0."payload" #> $5::text[])) = 'number' AND ` +
		`(r0."payload" #> $5::text[]) <= ($6::jsonb))`
	if !strings.Contains(directClause, wantDirect) {
		t.Fatalf("numeric direct ordering clause =\n%s\nwant substring\n%s", directClause, wantDirect)
	}
}

func TestRenderRejectsKindMismatchedSelectorInList(t *testing.T) {
	// Each IN element must carry the canonical Go type of the declared
	// kind; the []string shorthand belongs to the string-carrying kinds.
	inPlan := func(kind spec.FilterValueKind, bind any) ir.PhysicalPlan {
		plan := pgsq05Plan("Patient")
		plan.BindVars["list"] = bind
		return pgsq05AddFilter(plan, pgsq05FilterByPredicate(pgsq05SelectorPredicate(
			"Patient", "gender", "IN", spec.QuantifierAny, kind, "list", true)))
	}
	pgsq05Reject(t, inPlan(spec.FilterInteger, []string{"1"}), "INTEGER kind IN with a []string bind")
	pgsq05Reject(t, inPlan(spec.FilterBoolean, []any{"true"}), "BOOLEAN kind IN with string elements")
	pgsq05Reject(t, inPlan(spec.FilterDecimal, []any{int64(1)}), "DECIMAL kind IN with int64 elements")
	pgsq05Reject(t, inPlan(spec.FilterString, []any{int64(1)}), "STRING kind IN with int64 elements")

	// The canonical types render: INTEGER accepts []any of int64.
	plan := inPlan(spec.FilterInteger, []any{int64(1), int64(2)})
	rendered := renderOK(t, plan)
	if got := rendered.Args[5]; got.Type != compiler.PostgreSQLParameterJSONB ||
		string(got.Value.(json.RawMessage)) != "[1,2]" {
		t.Fatalf("INTEGER IN arg = %#v", got)
	}
}

func TestRenderRejectsStringKindOrderingInAllThreeForms(t *testing.T) {
	// OperatorSupportsKind is re-read at the renderer boundary, so a
	// hand-built plan cannot order a STRING kind in any registered form.
	columnPlan := pgsq05Plan("Patient")
	columnPlan.BindVars["v"] = "Patient"
	columnPlan = pgsq05AddFilter(columnPlan, pgsq05FilterByPredicate(ir.PhysicalPredicate{
		Operator:  "GT",
		ValueKind: spec.FilterString,
		Left:      ir.PhysicalValue{Variable: "root", Path: []string{"resource_type"}},
		Right:     &ir.PhysicalValue{BindKey: "v"},
	}))
	pgsq05Reject(t, columnPlan, "GT over STRING kind on a column")

	directPlan := pgsq05Plan("Patient")
	directPlan.BindVars["v"] = "a"
	directPlan = pgsq05AddFilter(directPlan, pgsq05FilterByPredicate(ir.PhysicalPredicate{
		Operator:  "GTE",
		ValueKind: spec.FilterString,
		Left:      ir.PhysicalValue{Variable: "root", Path: []string{"payload", "status"}},
		Right:     &ir.PhysicalValue{BindKey: "v"},
	}))
	pgsq05Reject(t, directPlan, "GTE over STRING kind on a payload path")

	selectorPlan := pgsq05Plan("Patient")
	selectorPlan.BindVars["v"] = "a"
	selectorPlan = pgsq05AddFilter(selectorPlan, pgsq05FilterByPredicate(pgsq05SelectorPredicate(
		"Patient", "gender", "LT", spec.QuantifierAny, spec.FilterString, "v", true)))
	pgsq05Reject(t, selectorPlan, "LT over STRING kind in selector form")
}

// ---------------------------------------------------------------------------
// Real unit arms for the temporal guard patterns (independent of the
// captured goldens, which encode the pattern strings and therefore cannot
// catch a pattern bug). These execute the constants themselves; the live
// boundary sweep executes them through PostgreSQL's ~ operator and the
// real ::timestamptz casts.
// ---------------------------------------------------------------------------

func TestRenderTemporalGuardPatternUnitArms(t *testing.T) {
	date := regexp.MustCompile(temporalStoredDatePattern)
	instant := regexp.MustCompile(temporalStoredInstantPattern)
	noOffset := regexp.MustCompile(temporalStoredNoOffsetPattern)

	type arm struct {
		value string
		date  bool
		inst  bool
		noOff bool
	}
	arms := []arm{
		// Century-leap arms: div-by-400 keeps Feb 29; div-by-100-not-400
		// and year 0000 never match.
		arm{"2000-02-29", true, false, false}, {"2400-02-29", true, false, false},
		arm{"1600-02-29", true, false, false}, {"1900-02-29", false, false, false},
		arm{"2100-02-29", false, false, false}, {"0000-02-29", false, false, false},
		arm{"0000-01-01", false, false, false},
		// Leap/non-leap Feb for ordinary years.
		arm{"2024-02-29", true, false, false}, {"2023-02-29", false, false, false},
		arm{"2024-02-28", true, false, false}, {"2024-02-30", false, false, false},
	}
	// Every month against calendar truth (days 01, max, max+1).
	maxDay := map[string]int{"01": 31, "02": 28, "03": 31, "04": 30, "05": 31, "06": 30,
		"07": 31, "08": 31, "09": 30, "10": 31, "11": 30, "12": 31}
	for month, max := range maxDay {
		arms = append(arms,
			arm{"2023-" + month + "-01", true, false, false},
			arm{fmt.Sprintf("2023-%s-%02d", month, max), true, false, false},
			arm{fmt.Sprintf("2023-%s-%02d", month, max+1), false, false, false},
			arm{"2023-" + month + "-00", false, false, false},
			arm{"2023-" + month + "-32", false, false, false},
		)
	}
	arms = append(arms,
		// Time bounds.
		arm{"2024-03-15T23:59:59Z", false, true, false},
		arm{"2024-03-15T24:00:00Z", false, false, false},
		arm{"2024-03-15T23:60:00Z", false, false, false},
		arm{"2024-03-15T23:59:60Z", false, false, false}, // leap second excluded, as before
		arm{"2024-03-15T10:20Z", false, true, false},
		// Offset bounds: PostgreSQL admits ±15:59 (SQLSTATE 22009 beyond).
		arm{"2024-03-15T10:20:30+00:00", false, true, false},
		arm{"2024-03-15T10:20:30+14:00", false, true, false},
		arm{"2024-03-15T10:20:30+15:59", false, true, false},
		arm{"2024-03-15T10:20:30-15:59", false, true, false},
		arm{"2024-03-15T10:20:30+16:00", false, false, false},
		arm{"2024-03-15T10:20:30-16:00", false, false, false},
		arm{"2024-03-15T10:20:30+23:59", false, false, false},
		arm{"2024-03-15T10:20:30-23:59", false, false, false},
		arm{"2024-03-15T10:20:30+15:60", false, false, false},
		// Fractional seconds: '.' only (SQLSTATE 22007 on comma).
		arm{"2024-03-15T10:20:30.5Z", false, true, false},
		arm{"2024-03-15T10:20:30,5Z", false, false, false},
		arm{"2024-03-15T10:20:30,5", false, false, false},
		arm{"2024-03-15T10:20:30.5+02:00", false, true, false},
		arm{"2024-03-15T10:20:30,5+02:00", false, false, false},
		// No-offset instant branch.
		arm{"2024-03-15T10:20:30", false, false, true},
		arm{"2024-03-15T10:20", false, false, true},
		arm{"2024-03-15T24:00:00", false, false, false},
		// Garbage shapes never match any branch.
		arm{"2024", false, false, false},
		arm{"not-a-date", false, false, false},
		arm{"2024-03-15 10:20:30Z", false, false, false},
	)

	for _, a := range arms {
		if got := date.MatchString(a.value); got != a.date {
			t.Errorf("date pattern %q = %v, want %v", a.value, got, a.date)
		}
		if got := instant.MatchString(a.value); got != a.inst {
			t.Errorf("instant pattern %q = %v, want %v", a.value, got, a.inst)
		}
		if got := noOffset.MatchString(a.value); got != a.noOff {
			t.Errorf("no-offset pattern %q = %v, want %v", a.value, got, a.noOff)
		}
	}
}
