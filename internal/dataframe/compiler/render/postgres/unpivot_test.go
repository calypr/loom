package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// pg-sql-15 melt slice tests. Offline: the closed surface, golden SQL and
// ordered arguments, refusals, parameter discipline, and the W3 window
// contract. Live (service-gated by LOOM_TEST_POSTGRES_URL, owned namespace
// verify_sql15_*): the eight ratified sentinels with their alive-assertion
// controls — independent literal expected row multisets distinguishing
// absent, explicit JSON null, and ordinary values, policy behavior, the
// typed raise, authored key order, expanded-row windowing, preserved
// columns and the stable melt identity, and the scope controls.
// ---------------------------------------------------------------------------

const (
	unp15Project    = "sql15-proj"
	unp15Foreign    = "sql15-proj-x"
	unp15Generation = "gen-15"
	unp15Auth       = "FHIR/Patient"
	unp15SecretAuth = "FHIR/Secret"
)

// unp15Fixture is one registered melt plan in canonical form: a direct root
// scan, one melt of the two authored scalar inputs a→"first" and b→"second"
// with a preserved logical_id column, identity from the input-row logical id
// plus the project bind, and the optional stable-identity window. Every
// fixture plan is structurally valid: ir.Validate must pass (vacuous
// fixtures are killed at the plan surface, not by skipping).
type unp15Fixture struct {
	KeyType   string
	ValueType string
	Keys      []any
	Columns   []string // selected input columns, authored order
	Policy    string
	Window    bool
	Limit     int
	// Mutate adjusts the canonical plan after assembly (surface tests).
	Mutate func(plan *ir.PhysicalPlan)
}

func (f unp15Fixture) with(fallback unp15Fixture) unp15Fixture {
	if f.KeyType == "" {
		f.KeyType = fallback.KeyType
	}
	if f.ValueType == "" {
		f.ValueType = fallback.ValueType
	}
	if f.Keys == nil {
		f.Keys = fallback.Keys
	}
	if f.Columns == nil {
		f.Columns = fallback.Columns
	}
	if f.Policy == "" {
		f.Policy = fallback.Policy
	}
	return f
}

var unp15Canonical = unp15Fixture{
	KeyType:   "STRING",
	ValueType: "STRING",
	Keys:      []any{"first", "second"},
	Columns:   []string{"a", "b"},
	Policy:    "PRESERVE",
}

// unp15Plan assembles the canonical registered melt plan.
func unp15Plan(t *testing.T, fixture unp15Fixture) ir.PhysicalPlan {
	t.Helper()
	fixture = fixture.with(unp15Canonical)
	if len(fixture.Keys) != len(fixture.Columns) {
		t.Fatalf("fixture keys %d != columns %d", len(fixture.Keys), len(fixture.Columns))
	}
	inputs := make([]ir.PhysicalUnpivotInput, 0, len(fixture.Columns))
	for index, column := range fixture.Columns {
		inputs = append(inputs, ir.PhysicalUnpivotInput{
			Column:     column,
			KeyBindKey: fmt.Sprintf("reshape_unpivot_key_%d", index),
			KeyKind:    fixture.KeyType,
			ValueKind:  fixture.ValueType,
		})
	}
	projections := []ir.PhysicalProjection{
		{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		{Name: "a", Value: ir.PhysicalValue{Variable: "root", Path: []string{"a"}}},
		{Name: "b", Value: ir.PhysicalValue{Variable: "root", Path: []string{"b"}}},
	}
	preserved := make([]ir.PhysicalUnpivotOutput, 0, 1)
	for _, projection := range projections {
		drop := projection.Name == unpivotRowIDColumn
		for _, input := range inputs {
			if input.Column == projection.Name {
				drop = true
			}
		}
		if !drop {
			preserved = append(preserved, ir.PhysicalUnpivotOutput{InputColumn: projection.Name, OutputColumn: projection.Name})
		}
	}
	unpivot := &ir.PhysicalUnpivot{
		ConstructionID:    "cfg-15",
		InputRowVariable:  "__loom_reshape_unpivot_input_0",
		SlotVariable:      "__loom_reshape_unpivot_slot_0",
		OutputRowVariable: "__loom_reshape_unpivot_output_0",
		InputProjections:  projections,
		Inputs:            inputs,
		PreservedOutputs:  preserved,
		KeyOutput:         "measure",
		KeyType:           fixture.KeyType,
		ValueOutput:       "amount",
		ValueType:         fixture.ValueType,
		IdentityParts: []ir.PhysicalUnpivotIdentityPart{
			{Name: unpivotRowIDColumn, Value: ir.PhysicalValue{Variable: "__loom_reshape_unpivot_input_0", Path: []string{"logical_id"}}},
			{Name: "project", Value: ir.PhysicalValue{BindKey: "project"}},
		},
		ConstructionIDBindKey: "reshape_construction",
		NullRowPolicy:         fixture.Policy,
	}
	outputVariable := unpivot.OutputRowVariable
	returns := []ir.PhysicalProjection{
		{Name: "measure", Value: ir.PhysicalValue{Variable: outputVariable, Path: []string{"measure"}}},
		{Name: "amount", Value: ir.PhysicalValue{Variable: outputVariable, Path: []string{"amount"}}},
		{Name: "logical_id", Value: ir.PhysicalValue{Variable: outputVariable, Path: []string{"logical_id"}}},
		{Name: unpivotRowIDColumn, Hidden: true, Value: ir.PhysicalValue{Variable: outputVariable, Path: []string{unpivotRowIDColumn}}},
	}
	operations := []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalUnpivotOp, Unpivot: unpivot},
	}
	bindVars := map[string]any{
		"root_collection":                  "Patient",
		"project":                          unp15Project,
		"dataset_generation":               unp15Generation,
		"auth_resource_paths":              []string{unp15Auth},
		"auth_resource_paths_unrestricted": false,
		"scope_allowed":                    true,
		"reshape_construction":             "cfg-15",
	}
	for index, key := range fixture.Keys {
		bindVars[fmt.Sprintf("reshape_unpivot_key_%d", index)] = key
	}
	if fixture.Window {
		operations = append(operations,
			ir.PhysicalOperation{Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{Keys: []ir.PhysicalValue{
				{Variable: outputVariable, Path: []string{unpivotRowIDColumn}},
			}}},
		)
		if fixture.Limit > 0 {
			bindVars["unpivot_limit"] = fixture.Limit
			operations = append(operations, ir.PhysicalOperation{Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: "unpivot_limit"}})
		}
	}
	operations = append(operations, ir.PhysicalOperation{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: returns}})
	plan := ir.PhysicalPlan{Version: 1, Engine: ir.PhysicalEngineAQL, BindVars: bindVars, Operations: operations}
	if fixture.Mutate != nil {
		fixture.Mutate(&plan)
	}
	return plan
}

func unp15Binding(t *testing.T, svc *pgir02Service) ir.PostgresStorageBinding {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding(svc.schema)
	if err != nil {
		svc.fail("storage binding", err)
	}
	return binding
}

func unp15Render(t *testing.T, plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) command.RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := RenderUnpivot(plan, storage)
	if err != nil {
		t.Fatalf("RenderUnpivot: %v", err)
	}
	return rendered
}

func unp15Seed(t *testing.T, svc *pgir02Service, project, logicalID string, payload any) {
	t.Helper()
	svc.insertResource(t, svc.schema, project, unp15Generation, "Patient", logicalID, unp15Auth, payload)
}

// unp15Rows renders and executes the canonical melt, returning stringified
// rows keyed by position (c0..).
func unp15Rows(t *testing.T, svc *pgir02Service, plan ir.PhysicalPlan) [][]string {
	t.Helper()
	rendered := unp15Render(t, plan, unp15Binding(t, svc))
	return svc.runRendered(t, rendered)
}

func unp15Multiset(rows [][]string) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, strings.Join(row, "|"))
	}
	sort.Strings(out)
	return out
}

func unp15ExpectMultiset(t *testing.T, got [][]string, want []string) {
	t.Helper()
	gotSet := unp15Multiset(got)
	wantSet := append([]string(nil), want...)
	sort.Strings(wantSet)
	if !reflect.DeepEqual(gotSet, wantSet) {
		t.Fatalf("row multiset mismatch\n got %d rows %v\nwant %d rows %v", len(gotSet), gotSet, len(wantSet), wantSet)
	}
}

// unp15ExecError runs the rendered statement to completion and returns the
// first execution error. pgx surfaces query errors only at Next(), so a
// bare Query call would both miss a raise and leave the pinned connection
// undrained (pool.Close then blocks forever): every raise check here drains.
func unp15ExecError(svc *pgir02Service, rendered command.RenderedPostgresPhysicalPlan) error {
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}
	rows, err := svc.iso.Query(svc.ctx, rendered.SQL, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
	}
	return rows.Err()
}

// unp15StateProbe executes the rendered statement as a subquery and returns
// (measure, amount, state) with state ∈ {SQL_NULL, JSON_NULL, VALUE} for the
// amount column — the driver-decode collapse of SQL NULL and jsonb 'null'
// (selectors_test.go precedent) means only an in-statement probe observes
// the tri-state the melt preserves.
func unp15StateProbe(t *testing.T, svc *pgir02Service, plan ir.PhysicalPlan, amountColumn string) [][3]string {
	t.Helper()
	rendered := unp15Render(t, plan, unp15Binding(t, svc))
	amountIndex := -1
	for index, name := range rendered.OutputColumns {
		if name == amountColumn {
			amountIndex = index
		}
	}
	if amountIndex < 0 {
		t.Fatalf("output column %q not rendered", amountColumn)
	}
	keyIndex := -1
	for index, name := range rendered.OutputColumns {
		if name == "measure" {
			keyIndex = index
		}
	}
	if keyIndex < 0 {
		t.Fatalf("output column measure not rendered")
	}
	value := fmt.Sprintf("q.\"c%d\"", amountIndex)
	sql := fmt.Sprintf(
		"SELECT q.\"c%d\"::text, "+
			"CASE WHEN %s IS NULL THEN 'SQL_NULL' WHEN %s = 'null'::jsonb THEN 'JSON_NULL' ELSE 'VALUE' END, "+
			"COALESCE(%s::text, '<null>') FROM ("+rendered.SQL+") AS q",
		keyIndex, value, value, value)
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}
	rows := svc.witness(t, sql, args...)
	out := make([][3]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, [3]string{row[0], row[1], row[2]})
	}
	return out
}

// ---------------------------------------------------------------------------
// Offline: golden SQL and ordered arguments for the canonical plan.
// ---------------------------------------------------------------------------

func TestUnpivot15GoldenStatement(t *testing.T) {
	plan := unp15Plan(t, unp15Fixture{})
	if err := plan.Validate(); err != nil {
		t.Fatalf("canonical melt plan invalid: %v", err)
	}
	binding := unp15OfflineBinding(t)
	first := unp15Render(t, plan, binding)
	second := unp15Render(t, plan, binding)
	if first.SQL != second.SQL || !reflect.DeepEqual(first.Args, second.Args) {
		t.Fatal("melt render is not deterministic")
	}
	if strings.Contains(first.SQL, "@") {
		t.Fatalf("melt SQL carries AQL bind syntax: %s", first.SQL)
	}
	// Golden captured from the renderer and hand-verified against the AQL
	// emitter contract (render/aql/table_reshape.go:502-620): authored-order
	// VALUES slots (key jsonb, payload extraction, ordinal), scope conjuncts
	// first, the typeof witness CASE, then the melt-order tail. The live
	// S6 witness additionally pins the identity text against hand-written
	// direct SQL, so the emitter is never cross-verified only against
	// itself.
	wantSQL := `SELECT (x1.slot_key) AS "c0", (x1.slot_value) AS "c1", (r0."logical_id") AS "c2", ` +
		`('[' || '[' || to_jsonb($9::text) || ',' || COALESCE(to_jsonb(r0."logical_id"), 'null'::jsonb)::text || ']' || ',' || ` +
		`'[' || to_jsonb($10::text) || ',' || to_jsonb($11::text)::text || ']' || ',' || ` +
		`'[' || to_jsonb($12::text) || ',' || '"TABLE_UNPIVOT"' || ']' || ',' || ` +
		`'[' || '"STRING"' || ',' || x1.slot_key::text || ']' || ']') AS "c3" ` +
		`FROM "sql15_it_binding"."fhir_resource" AS r0 ` +
		`CROSS JOIN LATERAL (VALUES ($7::jsonb, to_jsonb((r0."payload" #> $5::text[])), 1), ($8::jsonb, to_jsonb((r0."payload" #> $6::text[])), 2)) AS x1(slot_key, slot_value, slot_ord) ` +
		`WHERE r0."project" = $1 AND r0."dataset_generation" = $2 AND r0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb)) ` +
		`AND r0."resource_type" = $4 AND (CASE WHEN (x1.slot_value IS NOT NULL AND x1.slot_value <> 'null'::jsonb AND jsonb_typeof(x1.slot_value) <> 'string') ` +
		`THEN CAST('TABLE_UNPIVOT_VALUE_TYPE_MISMATCH' || COALESCE(current_setting('loom.explorer_guard_barrier', true), '') AS integer) ELSE 1 END) = 1 ` +
		`ORDER BY r0."resource_seq" ASC, x1.slot_ord ASC`
	if first.SQL != wantSQL {
		t.Fatalf("golden SQL drift:\n got %s\nwant %s", first.SQL, wantSQL)
	}
	wantArgs := []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: unp15Project},
		{Type: command.PostgreSQLParameterText, Value: unp15Generation},
		{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`["FHIR/Patient"]`)},
		{Type: command.PostgreSQLParameterText, Value: "Patient"},
		{Type: command.PostgreSQLParameterText, Value: "{a}"},
		{Type: command.PostgreSQLParameterText, Value: "{b}"},
		{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`"first"`)},
		{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`"second"`)},
		{Type: command.PostgreSQLParameterText, Value: "__loom_row_id"},
		{Type: command.PostgreSQLParameterText, Value: "project"},
		{Type: command.PostgreSQLParameterText, Value: unp15Project},
		{Type: command.PostgreSQLParameterText, Value: "cfg-15"},
	}
	if !reflect.DeepEqual(first.Args, wantArgs) {
		t.Fatalf("golden args = %#v, want %#v", first.Args, wantArgs)
	}
	wantColumns := []string{"measure", "amount", "logical_id", "__loom_row_id"}
	if !reflect.DeepEqual(first.OutputColumns, wantColumns) {
		t.Fatalf("output columns = %v, want %v", first.OutputColumns, wantColumns)
	}
}

func unp15OfflineBinding(t *testing.T) ir.PostgresStorageBinding {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding("sql15_it_binding")
	if err != nil {
		t.Fatalf("storage binding: %v", err)
	}
	return binding
}

// ---------------------------------------------------------------------------
// Offline: closed-surface refusals. Every mutation must be refused by BOTH
// entry points with the same typed capability error (the full-backstop
// witness, unnest.go's TestUnnestDispatchGateIsFullBackstop precedent).
// ---------------------------------------------------------------------------

func TestUnpivot15SurfaceRefusalsAreFullBackstop(t *testing.T) {
	cases := []struct {
		name    string
		fixture unp15Fixture
		needle  string
		// rawValidate marks mutations that break whole-plan IR validity
		// (unknown bind keys): both entries then honestly reject with the
		// shared plan.Validate error, not a capability error. Non-renderable
		// on both entries stays required, and the messages stay equal
		// because RenderUnpivot delegates to RenderPhysicalPlan.
		rawValidate bool
		// siblingGate marks a coexisting op whose own registered slice owns
		// the plan first: core dispatch hoists validateUnnestPlanSurface
		// ahead of the melt gate (unnest.go's contract, not edited here),
		// so message equality only holds when the melt gate is dispatch's
		// first refusal. Both entries must still reject, typed.
		siblingGate bool
	}{
		{"missing-payload", unp15Fixture{Mutate: func(plan *ir.PhysicalPlan) { plan.Operations[1].Unpivot = nil }}, "payload is missing", false, false},
		{"second-melt", unp15Fixture{Mutate: func(plan *ir.PhysicalPlan) {
			melt := *plan.Operations[1].Unpivot
			melt.OutputRowVariable = "second_output"
			plan.Operations = append(plan.Operations[:2], append([]ir.PhysicalOperation{{Kind: ir.PhysicalUnpivotOp, Unpivot: &melt}}, plan.Operations[2:]...)...)
		}}, "second table reshape|multiple table reshapes", false, false},
		{"coexisting-filter", unp15Fixture{Mutate: func(plan *ir.PhysicalPlan) {
			filter := &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator:  "EQUALS",
				Left:      ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}},
				Right:     &ir.PhysicalValue{BindKey: "compare_value"},
				ValueKind: spec.FilterString,
			}}
			plan.Operations = append(plan.Operations[:1], append([]ir.PhysicalOperation{{Kind: ir.PhysicalFilterOp, Filter: filter}}, plan.Operations[1:]...)...)
			plan.BindVars["compare_value"] = "x"
		}}, "cannot coexist with UNPIVOT", false, false},
		{"coexisting-unnest", unp15Fixture{Mutate: func(plan *ir.PhysicalPlan) {
			plan.Operations = append(plan.Operations[:1], append([]ir.PhysicalOperation{{Kind: ir.PhysicalUnnestOp, Unnest: &ir.PhysicalUnnest{}}}, plan.Operations[1:]...)...)
		}}, "cannot coexist with UNPIVOT", false, true},
		{"melt-before-scan", unp15Fixture{Mutate: func(plan *ir.PhysicalPlan) {
			plan.Operations[0], plan.Operations[1] = plan.Operations[1], plan.Operations[0]
		}}, "cannot run before the rows it melts", false, false},
		{"melt-after-window", unp15Fixture{Window: true, Limit: 3, Mutate: func(plan *ir.PhysicalPlan) {
			melt := plan.Operations[1]
			plan.Operations = append(plan.Operations[:1], plan.Operations[2:]...)
			plan.Operations = append(plan.Operations[:len(plan.Operations)-1], append([]ir.PhysicalOperation{melt}, plan.Operations[len(plan.Operations)-1])...)
		}}, "SORT must follow the melt", false, false},
		{"sort-wrong-key", unp15Fixture{Window: true, Mutate: func(plan *ir.PhysicalPlan) {
			plan.Operations[2].Sort.Keys[0] = ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}
		}}, "must sort on __loom_reshape_unpivot_output_0.__loom_row_id exactly", false, false},
		{"sort-two-keys", unp15Fixture{Window: true, Mutate: func(plan *ir.PhysicalPlan) {
			out := plan.Operations[1].Unpivot.OutputRowVariable
			plan.Operations[2].Sort.Keys = append(plan.Operations[2].Sort.Keys, ir.PhysicalValue{Variable: out, Path: []string{unpivotRowIDColumn}})
		}}, "exactly the stable row identity key", false, false},
		{"limit-without-sort", unp15Fixture{Mutate: func(plan *ir.PhysicalPlan) {
			plan.BindVars["unpivot_limit"] = 3
			limit := ir.PhysicalOperation{Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: "unpivot_limit"}}
			returnOp := plan.Operations[len(plan.Operations)-1]
			plan.Operations = append(plan.Operations[:2], append([]ir.PhysicalOperation{limit, returnOp}, plan.Operations[2:len(plan.Operations)-1]...)...)
		}}, "requires the stable row identity SORT|LIMIT requires|precede the execution window", false, false},
		{"unknown-policy", unp15Fixture{Policy: "SOMETHING"}, "unsupported null-row policy", false, false},
		{"unknown-value-type", unp15Fixture{ValueType: "BLOB", Keys: []any{"first", "second"}}, "outside the closed scalar registry", false, false},
		{"key-kind-mismatch", unp15Fixture{Mutate: func(plan *ir.PhysicalPlan) {
			plan.Operations[1].Unpivot.Inputs[0].KeyKind = "INTEGER"
		}}, "canonical key type", false, false},
		{"construction-mismatch", unp15Fixture{Mutate: func(plan *ir.PhysicalPlan) {
			plan.BindVars["reshape_construction"] = "other"
		}}, "does not match construction metadata", false, false},
		{"identity-missing-column", unp15Fixture{Mutate: func(plan *ir.PhysicalPlan) {
			plan.Operations[1].Unpivot.IdentityParts[0].Value.Path = []string{"nope"}
		}}, "not an input projection|missing", false, false},
		{"identity-unknown-bind", unp15Fixture{Mutate: func(plan *ir.PhysicalPlan) {
			plan.Operations[1].Unpivot.IdentityParts[1].Value.BindKey = "no_such_bind"
		}}, "is not defined", true, false},
		{"return-unknown-name", unp15Fixture{Mutate: func(plan *ir.PhysicalPlan) {
			out := plan.Operations[len(plan.Operations)-1].Return
			out.Projections[2].Value.Path = []string{"nope"}
		}}, "not a registered melt output", false, false},
		{"return-other-variable", unp15Fixture{Mutate: func(plan *ir.PhysicalPlan) {
			out := plan.Operations[len(plan.Operations)-1].Return
			out.Projections[2].Value.Variable = "root"
		}}, "reads only the melt output variable", false, false},
		{"return-multi-path", unp15Fixture{Mutate: func(plan *ir.PhysicalPlan) {
			out := plan.Operations[len(plan.Operations)-1].Return
			out.Projections[0].Value.Path = []string{"measure", "nested"}
		}}, "exactly one name|single name", false, false},
		{"return-binding-value", unp15Fixture{Mutate: func(plan *ir.PhysicalPlan) {
			out := plan.Operations[len(plan.Operations)-1].Return
			out.Projections[0].Value = ir.PhysicalValue{BindKey: "reshape_construction"}
		}}, "not a bind value|bind value", false, false},
		{"return-expression-form", unp15Fixture{Mutate: func(plan *ir.PhysicalPlan) {
			out := plan.Operations[len(plan.Operations)-1].Return
			value := ir.PhysicalValue{Variable: "__loom_reshape_unpivot_output_0", Path: []string{"measure"}}
			out.Projections[0].Expression = &ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Value: &value}
			out.Projections[0].Value = ir.PhysicalValue{}
		}}, "direct value|expression kind", false, false},
		{"population-root", unp15Fixture{Mutate: func(plan *ir.PhysicalPlan) {
			plan.Operations[0].RootScan.Population = &ir.PhysicalPopulationRootSource{}
		}}, "population, cohort, and candidate-seed roots", false, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			plan := unp15Plan(t, testCase.fixture)
			_, entryErr := RenderUnpivot(plan, unp15OfflineBinding(t))
			if entryErr == nil {
				t.Fatalf("RenderUnpivot accepted a %s plan", testCase.name)
			}
			if !testCase.rawValidate && !errors.Is(entryErr, ErrUnsupportedPostgresPlan) {
				t.Fatalf("RenderUnpivot %s error = %v, want ErrUnsupportedPostgresPlan", testCase.name, entryErr)
			}
			_, dispatchErr := RenderPhysicalPlan(plan, unp15OfflineBinding(t))
			if dispatchErr == nil {
				t.Fatalf("RenderPhysicalPlan accepted a %s plan", testCase.name)
			}
			if !testCase.rawValidate && !errors.Is(dispatchErr, ErrUnsupportedPostgresPlan) {
				t.Fatalf("RenderPhysicalPlan %s error = %v, want ErrUnsupportedPostgresPlan", testCase.name, dispatchErr)
			}
			if !testCase.siblingGate && entryErr.Error() != dispatchErr.Error() {
				t.Fatalf("backstop message drift:\n entry    %v\n dispatch %v", entryErr, dispatchErr)
			}
			t.Logf("refusal: %v", entryErr)
			if !regexNeedle(entryErr.Error(), testCase.needle) {
				t.Fatalf("%s refusal = %q, want needle %q", testCase.name, entryErr, testCase.needle)
			}
		})
	}
}

// needle helper: alternation-aware substring check.
func regexNeedle(haystack, needle string) bool {
	for _, alternative := range strings.Split(needle, "|") {
		if strings.Contains(haystack, alternative) {
			return true
		}
	}
	return false
}

func TestUnpivot15GoldenWindowAndPolicyClauses(t *testing.T) {
	binding := unp15OfflineBinding(t)

	windowed := unp15Render(t, unp15Plan(t, unp15Fixture{Window: true, Limit: 4}), binding)
	const marker = " ORDER BY "
	orderIndex := strings.Index(windowed.SQL, marker)
	if orderIndex < 0 {
		t.Fatalf("windowed statement carries no ORDER BY:\n%s", windowed.SQL)
	}
	order := windowed.SQL[orderIndex+len(marker):]
	// The identity sort term is renderer-composed; pin its tail and the
	// witness ordinals that prove the W3 windowOrder contract.
	if !strings.Contains(order, ") ASC, r0.\"resource_seq\" ASC, x1.slot_ord ASC LIMIT $13") {
		t.Fatalf("windowed ORDER BY/LIMIT = %q, want identity then resource_seq then slot_ord then LIMIT $13", order)
	}

	dropped := unp15Render(t, unp15Plan(t, unp15Fixture{Policy: "DROP"}), binding)
	if !strings.Contains(dropped.SQL, "WHERE") || !strings.Contains(dropped.SQL,
		"AND (x1.slot_value IS NOT NULL AND x1.slot_value <> 'null'::jsonb)") {
		t.Fatalf("DROP conjunct missing:\n%s", dropped.SQL)
	}
	if !strings.Contains(dropped.SQL, "THEN CAST('TABLE_UNPIVOT_VALUE_TYPE_MISMATCH' || COALESCE(current_setting('loom.explorer_guard_barrier', true), '') AS integer) ELSE 1 END) = 1") {
		t.Fatalf("value-type witness missing or unfolded (guard barrier required):\n%s", dropped.SQL)
	}
	if strings.Contains(dropped.SQL, "loom.explorer_guard_barrier\") = ") {
		t.Fatalf("barrier GUC leaked as a filter conjunct:\n%s", dropped.SQL)
	}
}

func TestUnpivot15KeyKindEncodingsAndBounds(t *testing.T) {
	binding := unp15OfflineBinding(t)
	kindFixture := func(kind string, keys ...any) unp15Fixture {
		return unp15Fixture{KeyType: kind, ValueType: "STRING", Keys: keys}
	}
	for _, testCase := range []struct {
		name string
		fix  unp15Fixture
		want string // JSON spelling expected as one of the JSONB args
	}{
		{"string", kindFixture("STRING", "first", "second"), `"first"`},
		{"integer", kindFixture("INTEGER", int64(7), int64(8)), "7"},
		{"boolean", kindFixture("BOOLEAN", true, false), "true"},
		{"decimal", kindFixture("DECIMAL", 1.5, 2.25), "1.5"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			rendered := unp15Render(t, unp15Plan(t, testCase.fix), binding)
			found := false
			for _, argument := range rendered.Args {
				if argument.Type == command.PostgreSQLParameterJSONB && strings.Contains(string(argument.Value.(json.RawMessage)), testCase.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("key JSON %q absent from %#v", testCase.want, rendered.Args)
			}
		})
	}
	for _, testCase := range []struct {
		name string
		fix  unp15Fixture
	}{
		{"integer-above-2^53", unp15Fixture{KeyType: "INTEGER", ValueType: "STRING", Keys: []any{int64(9007199254740993), int64(8)}}},
		{"integer-below-2^53", unp15Fixture{KeyType: "INTEGER", ValueType: "STRING", Keys: []any{int64(-9007199254740993), int64(8)}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			plan := unp15Plan(t, testCase.fix)
			if err := plan.Validate(); err != nil {
				t.Fatalf("fixture must reach the renderer: %v", err)
			}
			_, err := RenderUnpivot(plan, binding)
			if !errors.Is(err, ErrUnsupportedPostgresPlan) || !strings.Contains(err.Error(), "round-trip bound") {
				t.Fatalf("bound refusal = %v, want typed round-trip bound refusal", err)
			}
		})
	}
}

func TestUnpivot15HostilePlanDataStaysParameterized(t *testing.T) {
	hostile := "x'); DROP TABLE t; --\"'@bind #> {evil}"
	plan := unp15Plan(t, unp15Fixture{Keys: []any{hostile, "second"}})
	if err := plan.Validate(); err != nil {
		t.Fatalf("hostile fixture must be structurally valid: %v", err)
	}
	rendered := unp15Render(t, plan, unp15OfflineBinding(t))
	if strings.Contains(rendered.SQL, "DROP TABLE") || strings.Contains(rendered.SQL, "evil") {
		t.Fatalf("hostile plan data reached SQL text:\n%s", rendered.SQL)
	}
	carried := false
	for _, argument := range rendered.Args {
		switch value := argument.Value.(type) {
		case string:
			if strings.Contains(value, "DROP TABLE") {
				carried = true
			}
		case json.RawMessage:
			if strings.Contains(string(value), "DROP TABLE") {
				carried = true
			}
		}
	}
	if !carried {
		t.Fatal("hostile key never reached the parameter list; it was dropped, not bound")
	}
}

func TestUnpivot15SpinePathUntouched(t *testing.T) {
	// A plain spine plan (no melt) still renders through the untouched
	// renderReturn/classification path: the melt routing is inert when
	// unpivotOutputs stayed nil.
	plan := scopedPlan(unp15Project, unp15Generation, []string{unp15Auth}, false)
	plan.Operations = append(plan.Operations, ir.PhysicalOperation{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
		{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
	}}})
	rendered, err := RenderPhysicalPlan(plan, unp15OfflineBinding(t))
	if err != nil {
		t.Fatalf("spine render: %v", err)
	}
	if !strings.Contains(rendered.SQL, `AS "c0"`) || strings.Contains(rendered.SQL, "CROSS JOIN LATERAL (VALUES") {
		t.Fatalf("spine statement mutated by the melt slice:\n%s", rendered.SQL)
	}
}

// ---------------------------------------------------------------------------
// Live: PostgreSQL 18 sentinels S1-S8 (service-gated, owned verify_sql15_*).
// ---------------------------------------------------------------------------

func unp15LiveService(t *testing.T) *pgir02Service {
	t.Helper()
	svc := newOwnedService(t, 0, "verify_sql15")
	unpTrackFixtures(t, svc)
	return svc
}

// unp15CanonicalPayloads seeds the four discriminating stored states across
// two source rows: p1 stores a="alpha", b="beta"; p2 stores a absent, b as
// explicit JSON null, plus p3 carrying an ordinary value to keep the
// multiset non-degenerate.
func unp15SeedMeltStates(t *testing.T, svc *pgir02Service) {
	t.Helper()
	unp15Seed(t, svc, unp15Project, "p1", json.RawMessage(`{"a": "alpha", "b": "beta"}`))
	unp15Seed(t, svc, unp15Project, "p2", json.RawMessage(`{"b": null}`))
	unp15Seed(t, svc, unp15Project, "p3", json.RawMessage(`{"a": "gamma"}`))
	// Non-vacuity: every stored state is really stored, byte-exact.
	for _, state := range []struct{ id, payload string }{
		{"p1", `{"a": "alpha", "b": "beta"}`},
		{"p2", `{"b": null}`},
		{"p3", `{"a": "gamma"}`},
	} {
		rows := svc.witness(t, fmt.Sprintf(
			`SELECT payload::text FROM %s.fhir_resource WHERE logical_id = $1 AND project = $2 AND dataset_generation = $3`,
			`"`+svc.schema+`"`), state.id, unp15Project, unp15Generation)
		if len(rows) != 1 || rows[0][0] != state.payload {
			t.Fatalf("stored state %s = %v, want exactly %s — the fixture must not be vacuous", state.id, rows, state.payload)
		}
	}
}

// S1+S2: policy matrix. PRESERVE emits every (row, slot) pair with the
// tri-state observable through probes; DROP removes exactly the null-state
// slots — both absent and explicit JSON null — matching the AQL object-read
// collapse of table_reshape.go:539-541.
func TestUnpivot15LiveNullPolicyMatrix(t *testing.T) {
	svc := unp15LiveService(t)
	unp15SeedMeltStates(t, svc)

	preserved := unp15StateProbe(t, svc, unp15Plan(t, unp15Fixture{Policy: "PRESERVE"}), "amount")
	// The probe reads the rendered columns with ::text, so jsonb fidelity
	// is visible in the literals: string keys/values keep their JSON
	// quotes and the explicit JSON null renders as the text "null" — the
	// state column, not the text, is the tri-state assertion.
	wantPreserved := [][3]string{
		{`"first"`, "SQL_NULL", "<null>"},   // p2.a absent
		{`"first"`, "VALUE", `"alpha"`},     // p1.a
		{`"first"`, "VALUE", `"gamma"`},     // p3.a
		{`"second"`, "SQL_NULL", "<null>"},  // p3.b absent
		{`"second"`, "JSON_NULL", "null"},   // p2.b explicit JSON null
		{`"second"`, "VALUE", `"beta"`},     // p1.b
	}
	sortProbes := func(rows [][3]string) {
		sort.Slice(rows, func(i, j int) bool { return rows[i][0]+rows[i][2]+rows[i][1] < rows[j][0]+rows[j][2]+rows[j][1] })
	}
	sortProbes(preserved)
	sortProbes(wantPreserved)
	if !reflect.DeepEqual(preserved, wantPreserved) {
		t.Fatalf("PRESERVE probe multiset = %v, want %v", preserved, wantPreserved)
	}

	// Both null states — absent (SQL_NULL) and explicit JSON null — are
	// dropped; the three ordinary values survive: 6 melt slots minus
	// p2.a (absent), p2.b (JSON null), and p3.b (absent).
	dropped := unp15StateProbe(t, svc, unp15Plan(t, unp15Fixture{Policy: "DROP"}), "amount")
	if len(dropped) != 3 {
		t.Fatalf("DROP rows = %v, want the 3 non-null slots", dropped)
	}
	for _, row := range dropped {
		if row[1] != "VALUE" {
			t.Fatalf("DROP kept a %s slot: %v", row[1], row)
		}
	}
}

// S3: the value-type witness raises the canonical tag as a query error,
// plan-time safety proven by the EXPLAIN pair (plain EXPLAIN plans; EXPLAIN
// (ANALYZE) executes and raises), the population.go sabotage-proof pattern
// applied to the per-row CASE.
func TestUnpivot15LiveValueTypeRaise(t *testing.T) {
	svc := unp15LiveService(t)
	unp15Seed(t, svc, unp15Project, "ok1", json.RawMessage(`{"a": "alpha", "b": "beta"}`))
	unp15Seed(t, svc, unp15Project, "bad1", json.RawMessage(`{"a": 42, "b": "beta"}`))
	rendered := unp15Render(t, unp15Plan(t, unp15Fixture{Policy: "DROP"}), unp15Binding(t, svc))
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}
	// Plain EXPLAIN: plans fine — no plan-time fold of the guarded cast.
	if _, err := svc.iso.Exec(svc.ctx, "EXPLAIN "+rendered.SQL, args...); err != nil {
		t.Fatalf("plain EXPLAIN must plan without executing the witness: %v", err)
	}
	// Execution raises the canonical tag under DROP, where an ordinary row
	// also flows: conjunct order cannot hide the violation.
	_, err := svc.iso.Exec(svc.ctx, "EXPLAIN (ANALYZE, TIMING OFF, SUMMARY OFF) "+rendered.SQL, args...)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "22P02" || !strings.Contains(pgErr.Message, unpivotValueTypeTag) {
		t.Fatalf("EXPLAIN ANALYZE raise = %v, want 22P02 naming %s", err, unpivotValueTypeTag)
	}
	t.Logf("raise under EXPLAIN ANALYZE: %s %s", pgErr.Code, pgErr.Message)
	execErr := unp15ExecError(svc, rendered)
	if execErr == nil {
		t.Fatal("wrong-typed slot executed instead of raising")
	}
	// Alive control: the same statement over only well-typed rows renders.
	unp15Seed(t, svc, unp15Foreign, "bad-other", json.RawMessage(`{"a": 42}`))
	svc.witness(t, `DELETE FROM "`+svc.schema+`".fhir_resource WHERE logical_id = 'bad1'`)
	rows := svc.runRendered(t, rendered)
	if len(rows) != 2 {
		t.Fatalf("control rows after removing the violation = %v, want the two ordinary slots", rows)
	}
}

// S4: declared key typing and authored input order. INTEGER keys render as
// JSON numbers; the identity tail carries the first-input key kind and the
// slot keys in authored order; swapping authored order swaps the slot
// ordinals' key pairing.
func TestUnpivot15LiveKeyTypingAndAuthoredOrder(t *testing.T) {
	svc := unp15LiveService(t)
	unp15Seed(t, svc, unp15Project, "p1", json.RawMessage(`{"a": "alpha", "b": "beta"}`))
	fixture := unp15Fixture{KeyType: "INTEGER", ValueType: "STRING", Keys: []any{int64(101), int64(202)}, Window: true}
	rows := unp15Rows(t, svc, unp15Plan(t, fixture))
	// The declared key type reaches the identity: an INTEGER slot key is a
	// JSON number, and TO_STRING (table_reshape.go:543-559) renders numbers
	// unquoted, so the identity carries ["INTEGER",101], not ["INTEGER","101"].
	want := []string{
		`101|alpha|p1|[["__loom_row_id","p1"],["project","sql15-proj"],["cfg-15","TABLE_UNPIVOT"],["INTEGER",101]]`,
		`202|beta|p1|[["__loom_row_id","p1"],["project","sql15-proj"],["cfg-15","TABLE_UNPIVOT"],["INTEGER",202]]`,
	}
	unp15ExpectMultiset(t, rows, want)

	swapped := unp15Fixture{KeyType: "INTEGER", ValueType: "STRING", Keys: []any{int64(202), int64(101)}, Window: true}
	swappedRows := unp15Rows(t, svc, unp15Plan(t, swapped))
	swappedWant := []string{
		`101|beta|p1|[["__loom_row_id","p1"],["project","sql15-proj"],["cfg-15","TABLE_UNPIVOT"],["INTEGER",101]]`,
		`202|alpha|p1|[["__loom_row_id","p1"],["project","sql15-proj"],["cfg-15","TABLE_UNPIVOT"],["INTEGER",202]]`,
	}
	unp15ExpectMultiset(t, swappedRows, swappedWant)
	if reflect.DeepEqual(rows, swappedRows) {
		t.Fatal("authored order is not observable: permutation changed nothing")
	}
}

// S5: the window bounds MELTED rows, not source rows: 1 source row melts to
// 2 slots; LIMIT 1 returns 1 of the 2 melted rows while the control without
// LIMIT returns both. The control delta is the alive proof that the Limit
// node sits above the VALUES expansion, not below it.
func TestUnpivot15LiveWindowBoundsMeltedRows(t *testing.T) {
	svc := unp15LiveService(t)
	unp15Seed(t, svc, unp15Project, "p1", json.RawMessage(`{"a": "alpha", "b": "beta"}`))
	control := unp15Rows(t, svc, unp15Plan(t, unp15Fixture{Window: true}))
	if len(control) != 2 {
		t.Fatalf("unbounded window rows = %v, want 2 melted rows", control)
	}
	bounded := unp15Rows(t, svc, unp15Plan(t, unp15Fixture{Window: true, Limit: 1}))
	if len(bounded) != 1 {
		t.Fatalf("bounded window rows = %v, want exactly 1", bounded)
	}
	// Alive proof the LIMIT sits above the expansion: with 1 source row and
	// 2 slots, a source-row window would still return 2 rows (both slots of
	// the same source), while the melted window returns 1. The identity
	// order is stable across executions: run twice, compare exactly.
	repeat := unp15Rows(t, svc, unp15Plan(t, unp15Fixture{Window: true, Limit: 1}))
	if !reflect.DeepEqual(bounded, repeat) {
		t.Fatalf("bounded window is not deterministic across executions:\n%v\n%v", bounded, repeat)
	}
	rendered := unp15Render(t, unp15Plan(t, unp15Fixture{Window: true, Limit: 1}), unp15Binding(t, svc))
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}
	// The witness stringifies the JSON plan into its Go-map rendering, so
	// the nodes appear as "Node Type:Limit"/"Node Type:Values Scan" in
	// pre-order serialization: the Limit token preceding the Values Scan
	// token is the above-the-expansion proof (a below-limit expansion
	// would serialize the Values Scan first).
	planRows := svc.witness(t, "EXPLAIN (ANALYZE, FORMAT JSON) "+rendered.SQL, args...)
	if len(planRows) == 0 {
		t.Fatalf("EXPLAIN ANALYZE returned no plan rows:\n%s", planRows)
	}
	dump := planRows[0][0]
	limitAt, valuesAt := strings.Index(dump, "Node Type:Limit"), strings.Index(dump, "Node Type:Values Scan")
	if limitAt < 0 || valuesAt < 0 || limitAt > valuesAt {
		t.Fatalf("EXPLAIN ANALYZE plan lacks a Limit node above the VALUES expansion (limitAt=%d valuesAt=%d):\n%s", limitAt, valuesAt, dump)
	}
}

// S6: preserved columns (with a rename), stable identity match across
// executions, and the preserved logical id riding every slot.
func TestUnpivot15LivePreservedAndIdentityStability(t *testing.T) {
	svc := unp15LiveService(t)
	unp15Seed(t, svc, unp15Project, "p1", json.RawMessage(`{"a": "alpha", "b": "beta"}`))
	plan := unp15Plan(t, unp15Fixture{})
	rendered := unp15Render(t, plan, unp15Binding(t, svc))
	rows := svc.runRendered(t, rendered)
	want := []string{
		`first|alpha|p1|[["__loom_row_id","p1"],["project","sql15-proj"],["cfg-15","TABLE_UNPIVOT"],["STRING","first"]]`,
		`second|beta|p1|[["__loom_row_id","p1"],["project","sql15-proj"],["cfg-15","TABLE_UNPIVOT"],["STRING","second"]]`,
	}
	unp15ExpectMultiset(t, rows, want)
	again := svc.runRendered(t, rendered)
	if !reflect.DeepEqual(rows, again) {
		t.Fatalf("stable row identity drifted:\n%v\n%v", rows, again)
	}
	// Witness the identity against a hand-written direct-SQL expectation of
	// the AQL TO_STRING shape (independent literals, ASCII-bound contract).
	probe := svc.witness(t,
		`SELECT ('[' || '[' || '"__loom_row_id"' || ',' || '"p1"' || ']' || ',' || '[' || '"project"' || ',' || '"sql15-proj"' || ']' || ',' || `+
			`'[' || '"cfg-15"' || ',' || '"TABLE_UNPIVOT"' || ']' || ',' || '[' || '"STRING"' || ',' || '"first"' || ']' || ']')::text`)
	renderedIdentity := ""
	for _, row := range rows {
		if row[0] == "first" {
			renderedIdentity = row[3]
		}
	}
	if renderedIdentity == "" {
		t.Fatalf("first-slot row missing from %v", rows)
	}
	if probe[0][0] != renderedIdentity {
		t.Fatalf("identity witness drift:\n direct SQL %s\n rendered %s", probe[0][0], renderedIdentity)
	}
}

// S7: scope stays load-bearing through the expansion: the unauthorized auth
// path reads zero rows while the authorized control returns rows.
func TestUnpivot15LiveScopeControls(t *testing.T) {
	svc := unp15LiveService(t)
	unp15Seed(t, svc, unp15Project, "p1", json.RawMessage(`{"a": "alpha", "b": "beta"}`))
	plan := unp15Plan(t, unp15Fixture{})
	plan.BindVars["auth_resource_paths"] = []string{unp15SecretAuth}
	if rows := svc.runRendered(t, unp15Render(t, plan, unp15Binding(t, svc))); len(rows) != 0 {
		t.Fatalf("unauthorized auth path returned %v, want zero rows", rows)
	}
	// Alive control through the melt: authorized path returns both slots.
	if rows := unp15Rows(t, svc, unp15Plan(t, unp15Fixture{})); len(rows) != 2 {
		t.Fatalf("authorized control rows = %v, want 2", rows)
	}
	// Canonical deny: zero rows even with the authorized path.
	denied := unp15Plan(t, unp15Fixture{})
	denied.BindVars["scope_allowed"] = false
	if rows := svc.runRendered(t, unp15Render(t, denied, unp15Binding(t, svc))); len(rows) != 0 {
		t.Fatalf("canonical deny returned %v, want zero rows", rows)
	}
}

// S8: parameterized determinism live at the driver: same statement, same
// args, same ordered rows, and the ordered arguments pin exactly.
func TestUnpivot15LiveArgumentsPinned(t *testing.T) {
	svc := unp15LiveService(t)
	unp15Seed(t, svc, unp15Project, "p1", json.RawMessage(`{"a": "alpha", "b": "beta"}`))
	rendered := unp15Render(t, unp15Plan(t, unp15Fixture{Window: true, Limit: 1}), unp15Binding(t, svc))
	if len(rendered.Args) != 13 {
		t.Fatalf("melt argument count = %d, want the 12 canonical plus the bound limit: %#v", len(rendered.Args), rendered.Args)
	}
	if last := rendered.Args[12]; last.Type != command.PostgreSQLParameterBigint || last.Value != int64(1) {
		t.Fatalf("limit argument = %#v, want bigint 1", last)
	}
	rows := svc.runRendered(t, rendered)
	if len(rows) != 1 || rows[0][0] != "first" {
		t.Fatalf("deterministic first row = %v, want the authored-first slot", rows)
	}
}

// ---------------------------------------------------------------------------
// Live fixture hygiene: delta-scoped zero-survivor audit for verify_sql15_*
// (traversal_test.go trvRunDeltaAudit precedent), composed into TestMain by
// main_test.go. Tracked survivors fail the run; untracked late-born names
// are reported without framing a sibling lane.
// ---------------------------------------------------------------------------

var (
	unpFixturesMu      sync.Mutex
	unpTrackedFixtures = map[string]bool{}
)

func unpTrackFixtures(t *testing.T, svc *pgir02Service) {
	t.Helper()
	var database string
	if err := svc.iso.QueryRow(svc.ctx, "SELECT current_database()").Scan(&database); err != nil {
		svc.fail("track owned database", err)
	}
	unpFixturesMu.Lock()
	defer unpFixturesMu.Unlock()
	unpTrackedFixtures[database] = true
	unpTrackedFixtures[svc.schema] = true
}

func unpRunDeltaAudit(inner func() int) int {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		return inner()
	}
	beforeList, err := unpSnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-15 delta audit: snapshot before run: %v\n", err)
		return 1
	}
	before := make(map[string]bool, len(beforeList))
	for _, name := range beforeList {
		before[name] = true
	}
	code := inner()
	after, err := unpSnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-15 delta audit: snapshot after run: %v\n", err)
		code = 1
	}
	unpFixturesMu.Lock()
	defer unpFixturesMu.Unlock()
	for _, name := range after {
		if before[name] {
			continue
		}
		if unpTrackedFixtures[name] {
			fmt.Fprintf(os.Stderr, "pg-sql-15 delta audit: %q was created and tracked by this run and still survives\n", name)
			code = 1
			continue
		}
		fmt.Fprintf(os.Stderr, "pg-sql-15 delta audit: note — %q was born during this run but is not owned by it (sibling lane?)\n", name)
	}
	return code
}

func unpSnapshotOwnedResources(ctx context.Context, raw string) ([]string, error) {
	pool, err := pgxpool.New(ctx, raw)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var names []string
	for _, query := range []string{
		`SELECT datname FROM pg_database WHERE datname LIKE 'verify\_sql15\_%' ESCAPE '\'`,
		`SELECT nspname FROM pg_namespace WHERE nspname LIKE 'verify\_sql15\_%' ESCAPE '\'`,
	} {
		rows, err := pool.Query(queryCtx, query)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return nil, err
			}
			names = append(names, name)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return names, nil
}

var _ = regexp.MustCompile
