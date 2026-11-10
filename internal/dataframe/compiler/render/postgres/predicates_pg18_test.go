package postgres

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// pg-sql-05 real PostgreSQL 18 acceptance (service-gated by
// LOOM_TEST_POSTGRES_URL). Every case runs against a real server through the
// shared owned-service harness under the ticket-owned verify_sql05_* prefix:
// one uniquely owned isolated database and schema per test, literal rows
// stored through the merged pg-store repositories, the rendered statement
// executed with its ordered driver arguments, and comparison against literal
// expected rows plus a hand-written direct-SQL witness. Independent rows
// mean each fixture row exists to answer exactly one matrix cell, and every
// cell names its expected row set. A skipped required case is a blocker for
// this ticket's acceptance, never a pass.
// TestZZNoLeftoverVerifySQL05Resources audits that nothing survives.
// ---------------------------------------------------------------------------

const (
	sql05Prefix    = "verify_sql05_it"
	sql05Project   = "verify_sql05-proj"
	sql05Gen       = "gen-1"
	sql05Auth      = "FHIR/Patient"
	sql05RowPrefix = "verify_sql05-"
)

func newSQL05Service(t *testing.T) *pgir02Service {
	t.Helper()
	return newOwnedService(t, 0, sql05Prefix)
}

// sql05Plan is the scoped restricted-auth read over one resource type for
// this ticket's owned project and generation.
func sql05Plan(resourceType string) ir.PhysicalPlan {
	return ir.PhysicalPlan{
		Version: 1,
		Engine:  ir.PhysicalEngineAQL,
		BindVars: map[string]any{
			"root_collection":                  resourceType,
			"project":                          sql05Project,
			"dataset_generation":               sql05Gen,
			"auth_resource_paths":              []string{sql05Auth},
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

func sql05Insert(t *testing.T, svc *pgir02Service, logicalID string, payload any) {
	t.Helper()
	svc.insertResource(t, svc.schema, sql05Project, sql05Gen, "Patient", sql05RowPrefix+logicalID, sql05Auth, payload)
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
	rendered := renderOK(t, plan)
	if strings.Contains(rendered.SQL, sql05Project) || strings.Contains(rendered.SQL, sql05RowPrefix) {
		t.Fatalf("plan data leaked into SQL: %s", rendered.SQL)
	}
	return svc.runRendered(t, rendered)
}

// ---------------------------------------------------------------------------
// Independent rows: equality / inequality / range over string, code, date,
// and decimal filter values, plus explicit stored JSON null and missing.
// ---------------------------------------------------------------------------

func TestPG18ScalarFilterIndependentRows(t *testing.T) {
	svc := newSQL05Service(t)

	// One row per stored state; every cell below names exactly the rows
	// that must answer, so each expectation is independent.
	sql05Insert(t, svc, "str-final", map[string]any{"status": "final"})
	sql05Insert(t, svc, "str-amended", map[string]any{"status": "amended"})
	sql05Insert(t, svc, "str-null", map[string]any{"status": nil})
	sql05Insert(t, svc, "str-missing", map[string]any{"unrelated": 1})
	sql05Insert(t, svc, "score-seven", map[string]any{"score": 7})
	sql05Insert(t, svc, "score-decimal", map[string]any{"score": 2.5})
	sql05Insert(t, svc, "score-missing", map[string]any{"unrelated": 2})
	sql05Insert(t, svc, "date-march", map[string]any{"observed": "2024-03-15"})
	sql05Insert(t, svc, "date-january", map[string]any{"observed": "2024-01-01"})
	sql05Insert(t, svc, "date-partial", map[string]any{"observed": "2024"})
	sql05Insert(t, svc, "date-missing", map[string]any{"unrelated": 3})
	sql05Insert(t, svc, "instant-late", map[string]any{"recorded": "2024-03-15T10:20:30Z"})
	sql05Insert(t, svc, "instant-early", map[string]any{"recorded": "2024-03-15T09:00:00+02:00"})
	sql05Insert(t, svc, "instant-offset-twin", map[string]any{"recorded": "2024-03-15T12:20:30+02:00"})

	storedDateWitness := func(field string) string {
		extraction := `(payload #> '{` + field + `}'::text[])`
		text := `((` + extraction + `) #>> '{}')`
		return "CASE" +
			` WHEN jsonb_typeof(` + extraction + `) = 'string' AND ` + text + ` ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$'` +
			` THEN ((` + text + ` || 'T00:00:00+00:00')::timestamptz)` +
			` WHEN jsonb_typeof(` + extraction + `) = 'string' AND ` + text + ` ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}(:[0-9]{2}([.,][0-9]+)?)?(Z|[+-][0-9]{2}:[0-9]{2})$'` +
			` THEN (` + text + `)::timestamptz` +
			` END`
	}

	cases := []struct {
		label    string
		operator string
		field    string
		kind     spec.FilterValueKind
		bindKey  string
		bind     any
		want     [][]string
		witness  string // direct-SQL predicate with literals inline
	}{
		{
			label: "string equality", operator: "EQUALS", field: "status",
			kind: spec.FilterString, bindKey: "status_value", bind: "final",
			want:    sql05Rows("str-final"),
			witness: `payload #> '{status}'::text[] = '"final"'::jsonb`,
		},
		{
			label: "string inequality keeps null and missing", operator: "NOT_EQUALS", field: "status",
			kind: spec.FilterString, bindKey: "status_value", bind: "final",
			want:    sql05Rows("str-amended", "str-null", "str-missing"),
			witness: `NOT (payload #> '{status}'::text[] = '"final"'::jsonb)`,
		},
		{
			label: "explicit null equality is null-or-missing", operator: "EQUALS", field: "status",
			bindKey: "null_value", bind: json.RawMessage("null"),
			want:    sql05Rows("str-null", "str-missing"),
			witness: `payload #> '{status}'::text[] IS NULL OR payload #> '{status}'::text[] = 'null'::jsonb`,
		},
		{
			label: "code equality matches the stored code", operator: "EQUALS", field: "status",
			kind: spec.FilterCode, bindKey: "status_value", bind: "amended",
			want:    sql05Rows("str-amended"),
			witness: `payload #> '{status}'::text[] = '"amended"'::jsonb`,
		},
		{
			label: "missing treats stored JSON null as absent", operator: "MISSING", field: "status",
			want:    sql05Rows("str-null", "str-missing"),
			witness: `payload #> '{status}'::text[] IS NULL OR payload #> '{status}'::text[] = 'null'::jsonb`,
		},
		{
			label: "exists keeps present values only", operator: "EXISTS", field: "status",
			want:    sql05Rows("str-final", "str-amended"),
			witness: `COALESCE(jsonb_typeof(payload #> '{status}'::text[]), 'null') <> 'null'`,
		},
		{
			label: "decimal equality", operator: "EQUALS", field: "score",
			kind: spec.FilterDecimal, bindKey: "score_value", bind: 2.5,
			want:    sql05Rows("score-decimal"),
			witness: `payload #> '{score}'::text[] = '2.5'::jsonb`,
		},
		{
			label: "integer range lower bound", operator: "GTE", field: "score",
			kind: spec.FilterInteger, bindKey: "score_value", bind: int64(5),
			want:    sql05Rows("score-seven"),
			witness: `(payload #> '{score}'::text[]) >= '5'::jsonb`,
		},
		{
			label: "decimal range upper bound inclusive", operator: "LTE", field: "score",
			kind: spec.FilterDecimal, bindKey: "score_value", bind: 7.0,
			want:    sql05Rows("score-seven", "score-decimal"),
			witness: `(payload #> '{score}'::text[]) <= '7'::jsonb`,
		},
		{
			label: "date range orders full dates and excludes the never-comparable partial", operator: "GTE", field: "observed",
			kind: spec.FilterDate, bindKey: "date_value", bind: "2024-02-01",
			want:    sql05Rows("date-march"),
			witness: storedDateWitness("observed") + ` >= '2024-02-01T00:00:00+00:00'::timestamptz`,
		},
		{
			label: "date-time equality stays raw text (AQL parity)", operator: "EQUALS", field: "recorded",
			kind: spec.FilterDateTime, bindKey: "when_value", bind: "2024-03-15T10:20:30Z",
			want:    sql05Rows("instant-late"),
			witness: `payload #> '{recorded}'::text[] = '"2024-03-15T10:20:30Z"'::jsonb`,
		},
		{
			label: "instant range orders across offsets", operator: "GTE", field: "recorded",
			kind: spec.FilterDateTime, bindKey: "when_value", bind: "2024-03-15T09:00:00Z",
			want:    sql05Rows("instant-late", "instant-offset-twin"),
			witness: storedDateWitness("recorded") + ` >= '2024-03-15T09:00:00+00:00'::timestamptz`,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.label, func(t *testing.T) {
			plan := sql05Plan("Patient")
			if testCase.bind != nil {
				plan.BindVars[testCase.bindKey] = testCase.bind
			}
			plan = pgsq05AddFilter(plan, pgsq05FilterByPredicate(
				sql05DirectLeaf(testCase.field, testCase.operator, testCase.bindKey, testCase.kind, testCase.bind != nil)))
			sql05Compare(t, testCase.label, sql05RenderedSelect(t, svc, plan), testCase.want)
			witness := svc.witness(t,
				`SELECT logical_id FROM "`+svc.schema+`"."fhir_resource"`+
					` WHERE project=$1 AND dataset_generation=$2 AND resource_type='Patient' AND (`+testCase.witness+`)`+
					` ORDER BY logical_id, resource_seq`, sql05Project, sql05Gen)
			sql05Compare(t, testCase.label+" witness", witness, testCase.want)
		})
	}
}

// ---------------------------------------------------------------------------
// Selector-extraction comparisons: quantifier truth tables over the
// null-filtered 0..1-item multiset, element non-unpacking, IN lists.
// ---------------------------------------------------------------------------

func TestPG18SelectorQuantifierTruthTables(t *testing.T) {
	svc := newSQL05Service(t)
	sql05Insert(t, svc, "g-female", map[string]any{"gender": "female"})
	sql05Insert(t, svc, "g-male", map[string]any{"gender": "male"})
	sql05Insert(t, svc, "g-null", map[string]any{"gender": nil})
	sql05Insert(t, svc, "g-missing", map[string]any{"unrelated": 1})
	sql05Insert(t, svc, "tags-ab", map[string]any{"tags": []any{"a", "b"}})
	sql05Insert(t, svc, "tags-null-a", map[string]any{"tags": []any{nil, "a"}})

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
			quant: spec.QuantifierNone, bindKey: "g", bind: "female", want: sql05Rows("g-male", "g-null", "g-missing")},
		{label: "ALL requires the matching item", selector: "gender", operator: "EQUALS",
			quant: spec.QuantifierAll, bindKey: "g", bind: "male", want: sql05Rows("g-male")},
		{label: "ANY equality ignores JSON null as absent", selector: "gender", operator: "EQUALS",
			quant: spec.QuantifierAny, bindKey: "g", bind: "nope", want: nil},
		{label: "array elements are never quantified individually", selector: "tags", operator: "EQUALS",
			quant: spec.QuantifierAny, bindKey: "t", bind: "a", want: nil},
		{label: "NONE over arrays proves the whole item is compared", selector: "tags", operator: "EQUALS",
			quant: spec.QuantifierNone, bindKey: "t", bind: "a",
			want: sql05Rows("g-female", "g-male", "g-null", "g-missing", "tags-ab", "tags-null-a")},
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
	sql05Compare(t, "selector EXISTS", sql05RenderedSelect(t, svc, existsPlan), sql05Rows("g-female", "g-male"))

	missingPlan := sql05Plan("Patient")
	missingPlan = pgsq05AddFilter(missingPlan, selectorFilter("gender", "MISSING", "", "", false))
	sql05Compare(t, "selector MISSING", sql05RenderedSelect(t, svc, missingPlan), sql05Rows("g-null", "g-missing"))
}

// ---------------------------------------------------------------------------
// Boolean predicates: AND / OR / NOT truth table over independent rows.
// ---------------------------------------------------------------------------

func TestPG18BooleanPredicateTruthTable(t *testing.T) {
	svc := newSQL05Service(t)
	sql05Insert(t, svc, "tt-tt", map[string]any{"alpha": true, "beta": true})
	sql05Insert(t, svc, "tt-tf", map[string]any{"alpha": true, "beta": false})
	sql05Insert(t, svc, "tt-ft", map[string]any{"alpha": false, "beta": true})
	sql05Insert(t, svc, "tt-ff", map[string]any{"alpha": false, "beta": false})
	sql05Insert(t, svc, "tt-null", map[string]any{"alpha": nil, "beta": true})
	sql05Insert(t, svc, "tt-missing", map[string]any{"unrelated": 1})

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
			want:    sql05Rows("tt-tt", "tt-tf", "tt-ft", "tt-null")},
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
			want:    sql05Rows("tt-tf", "tt-ft", "tt-null")},
	}

	for _, testCase := range cases {
		t.Run(testCase.label, func(t *testing.T) {
			plan := sql05Plan("Patient")
			plan.BindVars["alpha_value"] = true
			plan.BindVars["beta_value"] = true
			plan = pgsq05AddFilter(plan, pgsq05FilterByExpression(testCase.build(leaf("alpha", "alpha_value"), leaf("beta", "beta_value"))))
			sql05Compare(t, testCase.label, sql05RenderedSelect(t, svc, plan), testCase.want)
			witness := svc.witness(t,
				`SELECT logical_id FROM "`+svc.schema+`"."fhir_resource"`+
					` WHERE project=$1 AND dataset_generation=$2 AND resource_type='Patient' AND (`+testCase.witness+`)`+
					` ORDER BY logical_id, resource_seq`, sql05Project, sql05Gen)
			sql05Compare(t, testCase.label+" witness", witness, testCase.want)
		})
	}

	// NOT over the payload equality keeps the missing-key row (AQL reads
	// the missing-key comparison false, so NOT is true — the COALESCE
	// guard is what reproduces that in SQL) and the JSON-null row.
	notPlan := sql05Plan("Patient")
	notPlan.BindVars["alpha_value"] = true
	notPlan = pgsq05AddFilter(notPlan, pgsq05FilterByExpression(not(leaf("alpha", "alpha_value"))))
	sql05Compare(t, "NOT keeps null and missing rows", sql05RenderedSelect(t, svc, notPlan),
		sql05Rows("tt-ft", "tt-ff", "tt-null", "tt-missing"))
}

// ---------------------------------------------------------------------------
// Hostile selector values stay inert data.
// ---------------------------------------------------------------------------

func TestPG18SelectorHostileValuesAreInert(t *testing.T) {
	svc := newSQL05Service(t)
	hostile := `x'; DROP TABLE fhir_resource; --`
	sql05Insert(t, svc, "hostile", map[string]any{"note": hostile})
	sql05Insert(t, svc, "plain", map[string]any{"note": "plain"})

	plan := sql05Plan("Patient")
	plan.BindVars["note_value"] = hostile
	plan = pgsq05AddFilter(plan, pgsq05FilterByPredicate(pgsq05SelectorPredicate(
		"Patient", "note", "EQUALS", spec.QuantifierAny, spec.FilterString, "note_value", true)))
	rendered := renderOK(t, plan)
	if strings.Contains(rendered.SQL, "DROP") || strings.Contains(rendered.SQL, hostile) {
		t.Fatalf("hostile value reached statement text: %s", rendered.SQL)
	}
	sql05Compare(t, "hostile selector equality", svc.runRendered(t, rendered), sql05Rows("hostile"))
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
	sql05Insert(t, svc, "recent", map[string]any{"note": "r"})

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
	if got := svc.runRendered(t, renderOK(t, recorded("GTE", "2099-01-01"))); len(got) != 0 {
		t.Fatalf("recorded_at GTE 2099 rows = %v, want none", got)
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
