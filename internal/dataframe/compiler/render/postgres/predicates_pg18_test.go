package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// pg-sql-05 real PostgreSQL 18 acceptance (service-gated by
// LOOM_TEST_POSTGRES_URL). Every case runs against a real server through the
// shared owned-service harness under the ticket-owned verify_sql05_* prefix:
// one uniquely owned isolated database and schema per test, literal rows
// stored through the merged pg-store repositories, the rendered statement
// executed with its ordered driver arguments, and comparison against literal
// expected rows plus a hand-written direct-SQL witness. Fixture dimensions
// are isolated by resource type so each matrix cell names exactly the rows
// that must answer; rows are inserted in logical_id order, matching the
// witness ORDER BY. A skipped required case is a blocker for this ticket's
// acceptance, never a pass. TestZZNoLeftoverVerifySQL05Resources audits that
// nothing survives.
// ---------------------------------------------------------------------------

const (
	sql05Prefix    = "verify_sql05_it"
	sql05Project   = "verify_sql05-proj"
	sql05Gen       = "gen-1"
	sql05RowPrefix = "verify_sql05-"
)

func newSQL05Service(t *testing.T) *pgir02Service {
	t.Helper()
	return newOwnedService(t, 0, sql05Prefix)
}

// sql05Plan is the scoped restricted-auth read over one resource type for
// this ticket's owned project and generation. Each fixture dimension uses
// its own resource type so a filter over one dimension's keys can never be
// answered by another dimension's rows.
func sql05Plan(resourceType string) ir.PhysicalPlan {
	return ir.PhysicalPlan{
		Version: 1,
		Engine:  ir.PhysicalEngineAQL,
		BindVars: map[string]any{
			"root_collection":                  resourceType,
			"project":                          sql05Project,
			"dataset_generation":               sql05Gen,
			"auth_resource_paths":              []string{"FHIR/" + resourceType},
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

func sql05Insert(t *testing.T, svc *pgir02Service, resourceType, logicalID string, payload any) {
	t.Helper()
	svc.insertResource(t, svc.schema, sql05Project, sql05Gen, resourceType, sql05RowPrefix+logicalID, "FHIR/"+resourceType, payload)
}

func sql05Rows(logicalIDs ...string) [][]string {
	rows := make([][]string, 0, len(logicalIDs))
	for _, id := range logicalIDs {
		rows = append(rows, []string{sql05RowPrefix + id})
	}
	return rows
}

func sql05Compare(t *testing.T, label string, got [][]string, want [][]string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s rows = %v, want %v", label, got, want)
	}
}

func sql05DirectLeaf(field, operator, bindKey string, kind spec.FilterValueKind, withRight bool) ir.PhysicalPredicate {
	predicate := ir.PhysicalPredicate{
		Operator:  operator,
		ValueKind: kind,
		Left:      ir.PhysicalValue{Variable: "root", Path: []string{field}},
	}
	if withRight {
		predicate.Right = &ir.PhysicalValue{BindKey: bindKey}
	}
	return predicate
}

func sql05RenderedSelect(t *testing.T, svc *pgir02Service, plan ir.PhysicalPlan) [][]string {
	t.Helper()
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	if strings.Contains(rendered.SQL, sql05Project) || strings.Contains(rendered.SQL, sql05RowPrefix) {
		t.Fatalf("plan data leaked into SQL: %s", rendered.SQL)
	}
	return svc.runRendered(t, rendered)
}

func sql05WitnessRows(t *testing.T, svc *pgir02Service, resourceType, predicate string) [][]string {
	t.Helper()
	return svc.witness(t,
		`SELECT logical_id FROM "`+svc.schema+`"."fhir_resource"`+
			` WHERE project=$1 AND dataset_generation=$2 AND resource_type=$3 AND (`+predicate+`)`+
			` ORDER BY logical_id, resource_seq`, sql05Project, sql05Gen, resourceType)
}

// ---------------------------------------------------------------------------
// Independent rows: equality / inequality / range over string, code, date,
// and decimal filter values, plus explicit stored JSON null and missing.
// Each dimension (status / score / observed / recorded) lives in its own
// resource type, inserted in logical_id order.
// ---------------------------------------------------------------------------

func TestPG18ScalarFilterIndependentRows(t *testing.T) {
	svc := newSQL05Service(t)

	sql05Insert(t, svc, "Patient", "str-amended", map[string]any{"status": "amended"})
	sql05Insert(t, svc, "Patient", "str-final", map[string]any{"status": "final"})
	sql05Insert(t, svc, "Patient", "str-missing", map[string]any{"unrelated": 1})
	sql05Insert(t, svc, "Patient", "str-null", map[string]any{"status": nil})
	sql05Insert(t, svc, "Observation", "score-decimal", map[string]any{"score": 2.5})
	sql05Insert(t, svc, "Observation", "score-missing", map[string]any{"unrelated": 2})
	sql05Insert(t, svc, "Observation", "score-seven", map[string]any{"score": 7})
	// String under a numeric kind: jsonb type order would place it below
	// every number and over-select under LTE/LT against AQL; the type
	// guard must exclude it from both range cells.
	sql05Insert(t, svc, "Observation", "score-string", map[string]any{"score": "seven"})
	// Invalid-calendar and leap-day controls: the guards must exclude
	// these without ever casting them (a cast would abort the statement),
	// while a true leap day orders normally.
	sql05Insert(t, svc, "Condition", "date-feb29-leap", map[string]any{"observed": "2024-02-29"})
	sql05Insert(t, svc, "Condition", "date-feb29-nonleap", map[string]any{"observed": "2023-02-29"})
	sql05Insert(t, svc, "Condition", "date-invalid-day", map[string]any{"observed": "2024-02-30"})
	sql05Insert(t, svc, "Condition", "date-invalid-month", map[string]any{"observed": "2024-13-01"})
	sql05Insert(t, svc, "Condition", "date-january", map[string]any{"observed": "2024-01-01"})
	sql05Insert(t, svc, "Condition", "date-march", map[string]any{"observed": "2024-03-15"})
	sql05Insert(t, svc, "Condition", "date-missing", map[string]any{"unrelated": 3})
	// The named divergence witness row: a partial stored date that AQL's
	// DATE_TIMESTAMP would coerce but the renderer's guards read as
	// never-comparable (contract header, predicates.go).
	sql05Insert(t, svc, "Condition", "date-partial", map[string]any{"observed": "2024"})
	sql05Insert(t, svc, "Condition", "date-string", map[string]any{"observed": "not-a-date"})
	// Out-of-range time and offset shapes: digit-valid, cast-fatal.
	sql05Insert(t, svc, "Encounter", "instant-bad-offset", map[string]any{"recorded": "2024-03-15T10:20:30+99:99"})
	sql05Insert(t, svc, "Encounter", "instant-bad-time", map[string]any{"recorded": "2024-01-01T25:70:99Z"})
	sql05Insert(t, svc, "Encounter", "instant-early", map[string]any{"recorded": "2024-03-15T09:00:00+02:00"})
	sql05Insert(t, svc, "Encounter", "instant-late", map[string]any{"recorded": "2024-03-15T10:20:30Z"})
	sql05Insert(t, svc, "Encounter", "instant-offset-twin", map[string]any{"recorded": "2024-03-15T12:20:30+02:00"})

	storedDateWitness := func(field string) string {
		extraction := `(payload #> '{` + field + `}'::text[])`
		text := `((` + extraction + `) #>> '{}')`
		return "CASE" +
			` WHEN jsonb_typeof(` + extraction + `) = 'string' AND ` + text + ` ~ '^([1-9][0-9]{3}-(0[13578]|1[02])-(0[1-9]|[12][0-9]|3[01])|[1-9][0-9]{3}-(0[469]|11)-(0[1-9]|[12][0-9]|30)|[1-9][0-9]{3}-02-(0[1-9]|1[0-9]|2[0-8])|(0[48]|[2468][048]|[13579][26])00-02-29|[0-9]{2}(0[48]|[2468][048]|[13579][26])-02-29)$'` +
			` THEN ((` + text + ` || 'T00:00:00+00:00')::timestamptz)` +
			` WHEN jsonb_typeof(` + extraction + `) = 'string' AND ` + text + ` ~ '^([1-9][0-9]{3}-(0[13578]|1[02])-(0[1-9]|[12][0-9]|3[01])|[1-9][0-9]{3}-(0[469]|11)-(0[1-9]|[12][0-9]|30)|[1-9][0-9]{3}-02-(0[1-9]|1[0-9]|2[0-8])|(0[48]|[2468][048]|[13579][26])00-02-29|[0-9]{2}(0[48]|[2468][048]|[13579][26])-02-29)T(0[0-9]|1[0-9]|2[0-3]):[0-5][0-9](:[0-5][0-9]([.][0-9]+)?)?(Z|[+-]((0[0-9]|1[0-4]):[0-5][0-9]|15:[0-5][0-9]))$'` +
			` THEN (` + text + `)::timestamptz` +
			` WHEN jsonb_typeof(` + extraction + `) = 'string' AND ` + text + ` ~ '^([1-9][0-9]{3}-(0[13578]|1[02])-(0[1-9]|[12][0-9]|3[01])|[1-9][0-9]{3}-(0[469]|11)-(0[1-9]|[12][0-9]|30)|[1-9][0-9]{3}-02-(0[1-9]|1[0-9]|2[0-8])|(0[48]|[2468][048]|[13579][26])00-02-29|[0-9]{2}(0[48]|[2468][048]|[13579][26])-02-29)T(0[0-9]|1[0-9]|2[0-3]):[0-5][0-9](:[0-5][0-9]([.][0-9]+)?)?$'` +
			` THEN ((` + text + ` || '+00:00')::timestamptz)` +
			` END`
	}

	cases := []struct {
		label        string
		resourceType string
		operator     string
		field        string
		kind         spec.FilterValueKind
		bindKey      string
		bind         any
		want         [][]string
		witness      string // direct-SQL predicate with literals inline
	}{
		{
			label: "string equality", resourceType: "Patient", operator: "EQUALS", field: "status",
			kind: spec.FilterString, bindKey: "status_value", bind: "final",
			want:    sql05Rows("str-final"),
			witness: `payload #> '{status}'::text[] = '"final"'::jsonb`,
		},
		{
			label: "string inequality keeps null and missing", resourceType: "Patient", operator: "NOT_EQUALS", field: "status",
			kind: spec.FilterString, bindKey: "status_value", bind: "final",
			want:    sql05Rows("str-amended", "str-missing", "str-null"),
			witness: `payload #> '{status}'::text[] IS DISTINCT FROM '"final"'::jsonb`,
		},
		{
			label: "explicit null equality is null-or-missing", resourceType: "Patient", operator: "EQUALS", field: "status",
			bindKey: "null_value", bind: json.RawMessage("null"),
			want:    sql05Rows("str-missing", "str-null"),
			witness: `payload #> '{status}'::text[] IS NULL OR payload #> '{status}'::text[] = 'null'::jsonb`,
		},
		{
			label: "code equality matches the stored code", resourceType: "Patient", operator: "EQUALS", field: "status",
			kind: spec.FilterCode, bindKey: "status_value", bind: "amended",
			want:    sql05Rows("str-amended"),
			witness: `payload #> '{status}'::text[] = '"amended"'::jsonb`,
		},
		{
			label: "missing treats stored JSON null as absent", resourceType: "Patient", operator: "MISSING", field: "status",
			want:    sql05Rows("str-missing", "str-null"),
			witness: `payload #> '{status}'::text[] IS NULL OR payload #> '{status}'::text[] = 'null'::jsonb`,
		},
		{
			label: "exists keeps present values only", resourceType: "Patient", operator: "EXISTS", field: "status",
			want:    sql05Rows("str-amended", "str-final"),
			witness: `COALESCE(jsonb_typeof(payload #> '{status}'::text[]), 'null') <> 'null'`,
		},
		{
			label: "decimal equality", resourceType: "Observation", operator: "EQUALS", field: "score",
			kind: spec.FilterDecimal, bindKey: "score_value", bind: 2.5,
			want:    sql05Rows("score-decimal"),
			witness: `payload #> '{score}'::text[] = '2.5'::jsonb`,
		},
		{
			label: "integer range lower bound", resourceType: "Observation", operator: "GTE", field: "score",
			kind: spec.FilterInteger, bindKey: "score_value", bind: int64(5),
			want:    sql05Rows("score-seven"),
			witness: `jsonb_typeof((payload #> '{score}'::text[])) = 'number' AND ((payload #> '{score}'::text[])) >= '5'::jsonb`,
		},
		{
			label: "decimal range upper bound inclusive", resourceType: "Observation", operator: "LTE", field: "score",
			kind: spec.FilterDecimal, bindKey: "score_value", bind: 7.0,
			want:    sql05Rows("score-decimal", "score-seven"),
			witness: `jsonb_typeof((payload #> '{score}'::text[])) = 'number' AND ((payload #> '{score}'::text[])) <= '7'::jsonb`,
		},
		{
			label: "date range orders full and leap dates and excludes never-comparable partials and invalid calendars", resourceType: "Condition", operator: "GTE", field: "observed",
			kind: spec.FilterDate, bindKey: "date_value", bind: "2024-02-01",
			want:    sql05Rows("date-feb29-leap", "date-march"),
			witness: storedDateWitness("observed") + ` >= '2024-02-01T00:00:00+00:00'::timestamptz`,
		},
		{
			label: "payload-path date ordering upper bound excludes never-comparable strings", resourceType: "Condition", operator: "LTE", field: "observed",
			kind: spec.FilterDate, bindKey: "date_value", bind: "2024-02-01",
			want:    sql05Rows("date-january"),
			witness: storedDateWitness("observed") + ` <= '2024-02-01T00:00:00+00:00'::timestamptz`,
		},
		{
			label: "date-time equality stays raw text (AQL parity)", resourceType: "Encounter", operator: "EQUALS", field: "recorded",
			kind: spec.FilterDateTime, bindKey: "when_value", bind: "2024-03-15T10:20:30Z",
			want:    sql05Rows("instant-late"),
			witness: `payload #> '{recorded}'::text[] = '"2024-03-15T10:20:30Z"'::jsonb`,
		},
		{
			label: "instant range orders across offsets", resourceType: "Encounter", operator: "GTE", field: "recorded",
			kind: spec.FilterDateTime, bindKey: "when_value", bind: "2024-03-15T09:00:00Z",
			want:    sql05Rows("instant-late", "instant-offset-twin"),
			witness: storedDateWitness("recorded") + ` >= '2024-03-15T09:00:00+00:00'::timestamptz`,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.label, func(t *testing.T) {
			plan := sql05Plan(testCase.resourceType)
			if testCase.bind != nil {
				plan.BindVars[testCase.bindKey] = testCase.bind
			}
			plan = pgsq05AddFilter(plan, pgsq05FilterByPredicate(
				sql05DirectLeaf(testCase.field, testCase.operator, testCase.bindKey, testCase.kind, testCase.bind != nil)))
			sql05Compare(t, testCase.label, sql05RenderedSelect(t, svc, plan), testCase.want)
			sql05Compare(t, testCase.label+" witness", sql05WitnessRows(t, svc, testCase.resourceType, testCase.witness), testCase.want)
		})
	}
}

// ---------------------------------------------------------------------------
// Selector-extraction comparisons: quantifier truth tables over the
// null-filtered 0..1-item multiset, element non-unpacking, IN lists.
// gender selects Patient.gender; identifier selects the whole array item.
// ---------------------------------------------------------------------------

func TestPG18SelectorQuantifierTruthTables(t *testing.T) {
	svc := newSQL05Service(t)
	// The array-valued rows store raw JSON arrays under a non-repeating
	// FHIR field: repeating fields require [] in the selector, which is an
	// iterate step — still a typed refusal under the v1 contract.
	sql05Insert(t, svc, "Patient", "g-array-ab", map[string]any{"gender": []any{"a", "b"}})
	sql05Insert(t, svc, "Patient", "g-array-null-a", map[string]any{"gender": []any{nil, "a"}})
	sql05Insert(t, svc, "Patient", "g-female", map[string]any{"gender": "female"})
	sql05Insert(t, svc, "Patient", "g-male", map[string]any{"gender": "male"})
	sql05Insert(t, svc, "Patient", "g-missing", map[string]any{"unrelated": 1})
	sql05Insert(t, svc, "Patient", "g-null", map[string]any{"gender": nil})

	allRows := sql05Rows("g-array-ab", "g-array-null-a", "g-female", "g-male", "g-missing", "g-null")

	selectorFilter := func(selectorPath, operator, bindKey string, quantifier spec.ArrayQuantifier, withRight bool) ir.PhysicalOperation {
		return pgsq05FilterByPredicate(pgsq05SelectorPredicate(
			"Patient", selectorPath, operator, quantifier, spec.FilterString, bindKey, withRight))
	}

	cases := []struct {
		label    string
		selector string
		operator string
		quant    spec.ArrayQuantifier
		bindKey  string
		bind     any
		want     [][]string
	}{
		{label: "ANY matches the present item", selector: "gender", operator: "EQUALS",
			quant: spec.QuantifierAny, bindKey: "g", bind: "female", want: sql05Rows("g-female")},
		{label: "NONE keeps male, JSON null, and missing alike", selector: "gender", operator: "EQUALS",
			quant: spec.QuantifierNone, bindKey: "g", bind: "female",
			want: sql05Rows("g-array-ab", "g-array-null-a", "g-male", "g-missing", "g-null")},
		{label: "ALL requires the matching item", selector: "gender", operator: "EQUALS",
			quant: spec.QuantifierAll, bindKey: "g", bind: "male", want: sql05Rows("g-male")},
		{label: "ANY equality ignores JSON null as absent", selector: "gender", operator: "EQUALS",
			quant: spec.QuantifierAny, bindKey: "g", bind: "nope", want: nil},
		{label: "array elements are never quantified individually", selector: "gender", operator: "EQUALS",
			quant: spec.QuantifierAny, bindKey: "t", bind: "a", want: nil},
		{label: "NONE over arrays proves the whole item is compared", selector: "gender", operator: "EQUALS",
			quant: spec.QuantifierNone, bindKey: "t", bind: "a", want: allRows},
		{label: "IN binds top-level list membership", selector: "gender", operator: "IN",
			quant: spec.QuantifierAny, bindKey: "gs", bind: []any{"male", "female"}, want: sql05Rows("g-female", "g-male")},
		{label: "empty IN list selects nobody", selector: "gender", operator: "IN",
			quant: spec.QuantifierAny, bindKey: "gs", bind: []any{}, want: nil},
	}

	for _, testCase := range cases {
		t.Run(testCase.label, func(t *testing.T) {
			plan := sql05Plan("Patient")
			if testCase.bind != nil {
				plan.BindVars[testCase.bindKey] = testCase.bind
			}
			plan = pgsq05AddFilter(plan, selectorFilter(testCase.selector, testCase.operator, testCase.bindKey, testCase.quant, testCase.bind != nil))
			sql05Compare(t, testCase.label, sql05RenderedSelect(t, svc, plan), testCase.want)
		})
	}

	// Selector existence separates present items from JSON null/missing —
	// the quantifier-side reading of the three-state contract.
	existsPlan := sql05Plan("Patient")
	existsPlan = pgsq05AddFilter(existsPlan, selectorFilter("gender", "EXISTS", "", "", false))
	sql05Compare(t, "selector EXISTS", sql05RenderedSelect(t, svc, existsPlan), sql05Rows("g-array-ab", "g-array-null-a", "g-female", "g-male"))

	missingPlan := sql05Plan("Patient")
	missingPlan = pgsq05AddFilter(missingPlan, selectorFilter("gender", "MISSING", "", "", false))
	sql05Compare(t, "selector MISSING", sql05RenderedSelect(t, svc, missingPlan), sql05Rows("g-missing", "g-null"))
}

// ---------------------------------------------------------------------------
// Boolean predicates: AND / OR / NOT truth table over independent rows.
// ---------------------------------------------------------------------------

func TestPG18BooleanPredicateTruthTable(t *testing.T) {
	svc := newSQL05Service(t)
	sql05Insert(t, svc, "Patient", "tt-ff", map[string]any{"alpha": false, "beta": false})
	sql05Insert(t, svc, "Patient", "tt-ft", map[string]any{"alpha": false, "beta": true})
	sql05Insert(t, svc, "Patient", "tt-missing", map[string]any{"unrelated": 1})
	sql05Insert(t, svc, "Patient", "tt-null", map[string]any{"alpha": nil, "beta": true})
	sql05Insert(t, svc, "Patient", "tt-tf", map[string]any{"alpha": true, "beta": false})
	sql05Insert(t, svc, "Patient", "tt-tt", map[string]any{"alpha": true, "beta": true})

	leaf := func(field, bindKey string) ir.PhysicalPredicateExpression {
		return pgsq05ComparisonLeaf(ir.PhysicalPredicate{
			Operator: "EQUALS",
			Left:     ir.PhysicalValue{Variable: "root", Path: []string{field}},
			Right:    &ir.PhysicalValue{BindKey: bindKey},
		})
	}
	all := func(children ...ir.PhysicalPredicateExpression) ir.PhysicalPredicateExpression {
		return ir.PhysicalPredicateExpression{Kind: ir.PhysicalAllPredicate, Children: children}
	}
	any := func(children ...ir.PhysicalPredicateExpression) ir.PhysicalPredicateExpression {
		return ir.PhysicalPredicateExpression{Kind: ir.PhysicalAnyPredicate, Children: children}
	}
	not := func(child ir.PhysicalPredicateExpression) ir.PhysicalPredicateExpression {
		return ir.PhysicalPredicateExpression{Kind: ir.PhysicalNotPredicate, Children: []ir.PhysicalPredicateExpression{child}}
	}

	const alphaW = `COALESCE(jsonb_typeof(payload #> '{alpha}'::text[]), 'null') <> 'null' AND (payload #> '{alpha}'::text[]) = 'true'::jsonb`
	const betaW = `COALESCE(jsonb_typeof(payload #> '{beta}'::text[]), 'null') <> 'null' AND (payload #> '{beta}'::text[]) = 'true'::jsonb`

	cases := []struct {
		label   string
		build   func(a, b ir.PhysicalPredicateExpression) ir.PhysicalPredicateExpression
		witness string
		want    [][]string
	}{
		{label: "AND keeps only the true-true row",
			build:   func(a, b ir.PhysicalPredicateExpression) ir.PhysicalPredicateExpression { return all(a, b) },
			witness: `(` + alphaW + `) AND (` + betaW + `)`,
			want:    sql05Rows("tt-tt")},
		{label: "OR selects every row with a true side",
			build:   func(a, b ir.PhysicalPredicateExpression) ir.PhysicalPredicateExpression { return any(a, b) },
			witness: `(` + alphaW + `) OR (` + betaW + `)`,
			want:    sql05Rows("tt-ft", "tt-null", "tt-tf", "tt-tt")},
		{label: "NOR excludes every row with a true side",
			build: func(a, b ir.PhysicalPredicateExpression) ir.PhysicalPredicateExpression {
				return not(any(a, b))
			},
			witness: `NOT ((` + alphaW + `) OR (` + betaW + `))`,
			want:    sql05Rows("tt-ff", "tt-missing")},
		{label: "AND with negated beta",
			build: func(a, b ir.PhysicalPredicateExpression) ir.PhysicalPredicateExpression {
				return all(a, not(b))
			},
			witness: `(` + alphaW + `) AND (NOT (` + betaW + `))`,
			want:    sql05Rows("tt-tf")},
		{label: "exclusive-or pattern",
			build: func(a, b ir.PhysicalPredicateExpression) ir.PhysicalPredicateExpression {
				return any(all(a, not(b)), all(not(a), b))
			},
			witness: `((` + alphaW + `) AND (NOT (` + betaW + `))) OR ((NOT (` + alphaW + `)) AND (` + betaW + `))`,
			want:    sql05Rows("tt-ft", "tt-null", "tt-tf")},
	}

	for _, testCase := range cases {
		t.Run(testCase.label, func(t *testing.T) {
			plan := sql05Plan("Patient")
			plan.BindVars["alpha_value"] = true
			plan.BindVars["beta_value"] = true
			plan = pgsq05AddFilter(plan, pgsq05FilterByExpression(testCase.build(leaf("alpha", "alpha_value"), leaf("beta", "beta_value"))))
			sql05Compare(t, testCase.label, sql05RenderedSelect(t, svc, plan), testCase.want)
			sql05Compare(t, testCase.label+" witness", sql05WitnessRows(t, svc, "Patient", testCase.witness), testCase.want)
		})
	}

	// NOT over the payload equality keeps the missing-key row (AQL reads
	// the missing-key comparison false, so NOT is true — the COALESCE
	// guard is what reproduces that in SQL) and the JSON-null row.
	notPlan := sql05Plan("Patient")
	notPlan.BindVars["alpha_value"] = true
	notPlan = pgsq05AddFilter(notPlan, pgsq05FilterByExpression(not(leaf("alpha", "alpha_value"))))
	sql05Compare(t, "NOT keeps null and missing rows", sql05RenderedSelect(t, svc, notPlan),
		sql05Rows("tt-ff", "tt-ft", "tt-missing", "tt-null"))
}

// ---------------------------------------------------------------------------
// Hostile selector values stay inert data.
// ---------------------------------------------------------------------------

func TestPG18SelectorHostileValuesAreInert(t *testing.T) {
	svc := newSQL05Service(t)
	hostile := `x'; DROP TABLE fhir_resource; --`
	sql05Insert(t, svc, "Patient", "h-plain", map[string]any{"gender": "plain"})
	sql05Insert(t, svc, "Patient", "h-value", map[string]any{"gender": hostile})

	plan := sql05Plan("Patient")
	plan.BindVars["gender_value"] = hostile
	plan = pgsq05AddFilter(plan, pgsq05FilterByPredicate(pgsq05SelectorPredicate(
		"Patient", "gender", "EQUALS", spec.QuantifierAny, spec.FilterString, "gender_value", true)))
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	if strings.Contains(rendered.SQL, "DROP") || strings.Contains(rendered.SQL, hostile) {
		t.Fatalf("hostile value reached statement text: %s", rendered.SQL)
	}
	sql05Compare(t, "hostile selector equality", svc.runRendered(t, rendered), sql05Rows("h-value"))
	for _, table := range []string{"fhir_resource", "fhir_edge_occurrence"} {
		survived := svc.witness(t, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2`, svc.schema, table)
		if len(survived) != 1 || survived[0][0] != "1" {
			t.Fatalf("%s table was affected by a bound selector filter value", table)
		}
	}
}

// ---------------------------------------------------------------------------
// Typed column comparison over recorded_at with a full-precision literal.
// ---------------------------------------------------------------------------

func TestPG18RecordedAtTypedDateLiteral(t *testing.T) {
	svc := newSQL05Service(t)
	sql05Insert(t, svc, "Patient", "recent", map[string]any{"resourceType": "Patient"})

	recorded := func(operator, literal string) ir.PhysicalPlan {
		plan := sql05Plan("Patient")
		plan.BindVars["at_value"] = literal
		return pgsq05AddFilter(plan, pgsq05FilterByPredicate(ir.PhysicalPredicate{
			Operator:  operator,
			ValueKind: spec.FilterDate,
			Left:      ir.PhysicalValue{Variable: "root", Path: []string{"recorded_at"}},
			Right:     &ir.PhysicalValue{BindKey: "at_value"},
		}))
	}

	if got := sql05RenderedSelect(t, svc, recorded("GTE", "2020-01-01")); !reflect.DeepEqual(got, sql05Rows("recent")) {
		t.Fatalf("recorded_at GTE 2020 rows = %v", got)
	}
	if got := sql05RenderedSelect(t, svc, recorded("LT", "2099-01-01")); !reflect.DeepEqual(got, sql05Rows("recent")) {
		t.Fatalf("recorded_at LT 2099 rows = %v", got)
	}
	if got := sql05RenderedSelect(t, svc, recorded("GTE", "2099-01-01")); len(got) != 0 {
		t.Fatalf("recorded_at GTE 2099 rows = %v, want none", got)
	}
}

// ---------------------------------------------------------------------------
// F7/F8 boundary sweep. Expected kept/excluded sets are derived in-test
// from calendar rules by sql05ArmOrderableAtOrAfter2020 — an independent
// implementation (Go stdlib calendar parsing plus one explicit
// displacement bound) that shares no code with the renderer's regexes —
// not from captured strings. The property under test is that the rendered
// statement NEVER raises: every out-of-range stored shape must read
// never-comparable and be excluded.
//
// Empirical anchors (read-only VALUES casts against the live service
// before the fix): '2024-03-15T10:20:30+16:00', '-16:00', '+23:59' and
// '-23:59' each raised ERROR: time zone displacement out of range,
// SQLSTATE 22009; '2024-03-15T10:20:30,5' and '2024-03-15T10:20:30,5Z'
// raised ERROR: invalid input syntax for type timestamp with time zone,
// SQLSTATE 22007; control casts of '+15:59', '-15:59', '+14:00' and
// '30.5Z' succeeded. Those hand-run observations are not
// left as claims nobody re-checks:
// TestPG18TemporalGuardExcludedSideProbes re-runs exactly those casts with
// direct SQL on every live run and requires each documented SQLSTATE, and
// the oracle reads the same bound through sql05MaxAdmittedOffsetSeconds.
// If the server ever disagrees with this record, the guard pattern must be
// widened, not the assertion relaxed — over-exclusion is a live divergence
// from AQL, not a safe default.
// ---------------------------------------------------------------------------

// sql05MaxAdmittedOffsetSeconds is the largest UTC displacement, in
// seconds, that a stored instant may carry before PostgreSQL refuses the
// timestamptz cast with SQLSTATE 22009 — measured sixty seconds beyond
// this value by TestPG18TemporalGuardExcludedSideProbes, which re-witnesses
// the boundary on every live run. The renderer's offset regex and the
// oracle below share this one figure through that test rather than each
// asserting it independently.
const sql05MaxAdmittedOffsetSeconds = 15*3600 + 59*60

// TestPG18TemporalGuardExcludedSideProbes is the excluded-side witness for
// the guard boundary. The sweep proves the guard excludes out-of-range
// shapes; only these direct casts prove PostgreSQL would have rejected
// them — without that proof, an over-tight pattern would silently drop
// orderable stored instants that AQL DATE_TIMESTAMP folds and compares.
// Each shape is cast in its own statement over a literal value (no schema,
// no rows, read-only) and must either raise the documented SQLSTATE or
// cast cleanly. If any assertion here ever disagrees with real server
// behavior, widen the guard pattern; do not relax the assertion.
func TestPG18TemporalGuardExcludedSideProbes(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("LOOM_TEST_POSTGRES_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), raw)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	probes := []struct {
		value  string
		expect string // SQLSTATE that must be raised; "" means must cast
	}{
		{"2024-03-15T10:20:30+16:00", "22009"},
		{"2024-03-15T10:20:30-16:00", "22009"},
		{"2024-03-15T10:20:30+23:59", "22009"},
		{"2024-03-15T10:20:30-23:59", "22009"},
		{"2024-03-15T10:20:30,5", "22007"},
		{"2024-03-15T10:20:30,5Z", "22007"},
		{"2024-03-15T10:20:30+15:59", ""},
		{"2024-03-15T10:20:30-15:59", ""},
		{"2024-03-15T10:20:30+14:00", ""},
		{"2024-03-15T10:20:30.5Z", ""},
	}
	for _, p := range probes {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err := pool.Exec(ctx, "SELECT $1::text::timestamptz", p.value)
		cancel()
		if p.expect == "" {
			if err != nil {
				t.Errorf("control cast of %q failed: %v", p.value, err)
			}
			continue
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) {
			if err == nil {
				t.Errorf("cast of excluded shape %q succeeded: widen the guard pattern, do not relax this assertion", p.value)
			} else {
				t.Errorf("cast of %q: want SQLSTATE %s, got error %v", p.value, p.expect, err)
			}
			continue
		}
		if pgErr.Code != p.expect {
			t.Errorf("cast of %q: want SQLSTATE %s, got %s (%s)", p.value, p.expect, pgErr.Code, pgErr.Message)
		}
	}
}

// sql05ArmOrderableAtOrAfter2020 decides, independently of the renderer,
// whether a stored string is inside the space the guard may cast AND
// orders at or after the filter instant 2020-01-01T00:00:00Z. The Go
// stdlib owns the calendar (proleptic leap rules, month lengths, 0-23
// hours, 0-59 minutes/seconds, '.'-only fractional seconds); PostgreSQL's
// empirically admitted displacement range is applied explicitly.
func sql05ArmOrderableAtOrAfter2020(value string) bool {
	bound := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if strings.Contains(value, ",") {
		// PostgreSQL raises SQLSTATE 22007 on comma decimals; the guard
		// must never cast them.
		return false
	}
	if day, err := time.Parse("2006-01-02", value); err == nil {
		return !day.UTC().Before(bound)
	}
	for _, layout := range []string{
		time.RFC3339Nano,                // seconds + offset
		"2006-01-02T15:04Z07:00",        // minute precision + offset
		"2006-01-02T15:04:05.999999999", // seconds, no offset (guard appends +00:00)
		"2006-01-02T15:04",              // minute precision, no offset
	} {
		instant, err := time.Parse(layout, value)
		if err != nil {
			continue
		}
		_, offsetSeconds := instant.Zone()
		if offsetSeconds > sql05MaxAdmittedOffsetSeconds ||
			offsetSeconds < -sql05MaxAdmittedOffsetSeconds {
			// SQLSTATE 22009 beyond the probe-witnessed bound: never
			// cast, never-comparable.
			return false
		}
		return !instant.UTC().Before(bound)
	}
	return false
}

func TestPG18TemporalGuardBoundarySweep(t *testing.T) {
	svc := newSQL05Service(t)

	// (row, stored recorded value, rule under test). Kept/excluded is NOT
	// stored here: sql05ArmOrderableAtOrAfter2020 derives it per arm.
	arms := []struct {
		id    string
		value string
		rule  string
	}{
		{"b-1400", "2024-03-15T10:20:30+14:00", "offset inside admitted range"},
		{"b-1559", "2024-03-15T10:20:30+15:59", "offset admitted upper bound"},
		{"b-1600", "2024-03-15T10:20:30+16:00", "offset beyond 15:59 (22009)"},
		{"b-1900feb29", "1900-02-29", "century non-leap"},
		{"b-2000feb29", "2000-02-29", "century leap, before the filter bound"},
		{"b-2359", "2024-03-15T10:20:30+23:59", "offset beyond 15:59 (22009)"},
		{"b-2400feb29", "2400-02-29", "century leap orders"},
		{"b-apr31", "2024-04-31", "30-day month"},
		{"b-badtime", "2024-01-01T25:70:99Z", "time fields out of range"},
		{"b-comma-sec", "2024-03-15T10:20:30,5", "comma decimal (22007)"},
		{"b-comma-z", "2024-03-15T10:20:30,5Z", "comma decimal (22007)"},
		{"b-dec31", "2024-12-31", "31-day month"},
		{"b-dot-off", "2024-03-15T10:20:30.5+02:00", "dot fraction with offset"},
		{"b-dot-z", "2024-03-15T10:20:30.5Z", "dot fraction"},
		{"b-feb29leap", "2024-02-29", "leap day orders"},
		{"b-h24", "2024-03-15T24:00:00Z", "hour out of range"},
		{"b-jun30", "2024-06-30", "30-day month ok"},
		{"b-m1600", "2024-03-15T10:20:30-16:00", "negative offset beyond (22009)"},
		{"b-m2359", "2024-03-15T10:20:30-23:59", "negative offset beyond (22009)"},
		{"b-may31", "2024-05-31", "31-day month"},
		{"b-min", "2024-03-15T10:20Z", "minute precision"},
		{"b-nooff", "2024-03-15T10:20", "minute precision, no offset"},
		{"b-nov30", "2024-11-30", "30-day month ok"},
		{"b-sec60", "2024-03-15T10:20:60Z", "leap second excluded"},
		{"b-sep31", "2024-09-31", "30-day month"},
		{"b-yr0", "0000-01-01", "year 0000 never cast (also before bound)"},
	}
	var want [][]string
	for _, a := range arms {
		sql05Insert(t, svc, "Encounter", a.id, map[string]any{"recorded": a.value})
		if sql05ArmOrderableAtOrAfter2020(a.value) {
			want = append(want, []string{sql05RowPrefix + a.id})
		}
	}

	plan := sql05Plan("Encounter")
	plan.BindVars["when_value"] = "2020-01-01T00:00:00Z"
	plan = pgsq05AddFilter(plan, pgsq05FilterByPredicate(ir.PhysicalPredicate{
		Operator:  "GTE",
		ValueKind: spec.FilterDateTime,
		Left:      ir.PhysicalValue{Variable: "root", Path: []string{"recorded"}},
		Right:     &ir.PhysicalValue{BindKey: "when_value"},
	}))
	// A raise on any arm surfaces here as a fatal statement error.
	sql05Compare(t, "temporal guard boundary sweep", sql05RenderedSelect(t, svc, plan), want)
}

// TestSQL05TemporalGuardOracleSelfCheck pins the independent oracle's own
// decisions offline, so a broken oracle cannot silently weaken the live
// sweep (which is service-gated and would merely skip).
func TestSQL05TemporalGuardOracleSelfCheck(t *testing.T) {
	keep := map[string]bool{
		"2024-03-15T10:20:30+14:00":   true,
		"2024-03-15T10:20:30+15:59":   true,
		"2024-03-15T10:20:30.5+02:00": true,
		"2024-03-15T10:20:30.5Z":      true,
		"2024-03-15T10:20Z":           true,
		"2024-03-15T10:20":            true,
		"2400-02-29":                  true,
		"2024-02-29":                  true,
		"2024-12-31":                  true,
		"2024-05-31":                  true,
		"2024-06-30":                  true,
		"2024-11-30":                  true,
		"2024-03-15T10:20:30+16:00":   false,
		"2024-03-15T10:20:30-16:00":   false,
		"2024-03-15T10:20:30+23:59":   false,
		"2024-03-15T10:20:30-23:59":   false,
		"2024-03-15T10:20:30,5":       false,
		"2024-03-15T10:20:30,5Z":      false,
		"2024-01-01T25:70:99Z":        false,
		"2024-03-15T24:00:00Z":        false,
		"2024-03-15T10:20:60Z":        false,
		"1900-02-29":                  false,
		"2000-02-29":                  false, // valid but before the bound
		"2024-04-31":                  false,
		"2024-09-31":                  false,
		"0000-01-01":                  false,
		"2024":                        false,
	}
	for value, want := range keep {
		if got := sql05ArmOrderableAtOrAfter2020(value); got != want {
			t.Errorf("oracle(%q) = %v, want %v", value, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// Zero-leftover audit for this ticket's owned resources.
// ---------------------------------------------------------------------------

func TestZZNoLeftoverVerifySQL05Resources(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		// No service was used; nothing can have been created.
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close()
	var databases int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname LIKE 'verify_sql05_%'").Scan(&databases); err != nil {
		t.Fatalf("count databases: %v", err)
	}
	if databases != 0 {
		t.Fatalf("%d verify_sql05_* databases still exist", databases)
	}
	var schemas int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'verify_sql05_%'").Scan(&schemas); err != nil {
		t.Fatalf("count schemas: %v", err)
	}
	if schemas != 0 {
		t.Fatalf("%d verify_sql05_* schemas still exist in the service database", schemas)
	}
}
