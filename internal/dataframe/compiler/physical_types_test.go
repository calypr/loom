package compiler

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func okPlan(t *testing.T, sql string, args []PostgreSQLParameter, columns []string) RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := NewRenderedPostgresPhysicalPlan(sql, args, columns)
	if err != nil {
		t.Fatalf("NewRenderedPostgresPhysicalPlan(%q): %v", sql, err)
	}
	return rendered
}

func TestRenderedPostgresPlanRequiresSQLAndRejectsAQLBindSyntax(t *testing.T) {
	if _, err := NewRenderedPostgresPhysicalPlan("   ", nil, nil); err == nil {
		t.Fatal("blank SQL accepted")
	}
	for _, sql := range []string{
		`SELECT @value`,
		`FOR d IN @@collection RETURN d`,
		`SELECT $1 -- @@ sneaky`,
	} {
		_, err := NewRenderedPostgresPhysicalPlan(sql, []PostgreSQLParameter{{Type: PostgreSQLParameterText, Value: "x"}}, nil)
		if !errors.Is(err, ErrRenderedPostgresAQLBindSyntax) {
			t.Errorf("SQL %q error = %v, want ErrRenderedPostgresAQLBindSyntax", sql, err)
		}
	}
}

func TestRenderedPostgresPlanPlaceholderContract(t *testing.T) {
	text := func(v string) PostgreSQLParameter {
		return PostgreSQLParameter{Type: PostgreSQLParameterText, Value: v}
	}
	// Placeholder reuse is legal; every $1..$n must appear at least once.
	okPlan(t, `SELECT $1, $1 WHERE $2 = $1`, []PostgreSQLParameter{text("a"), text("b")}, nil)

	for _, tc := range []struct {
		name string
		sql  string
		args []PostgreSQLParameter
	}{
		{"out of range", `SELECT $3`, []PostgreSQLParameter{text("a")}},
		{"gap skips a number", `SELECT $1, $3`, []PostgreSQLParameter{text("a"), text("b"), text("c")}},
		{"unreferenced tail argument", `SELECT $1`, []PostgreSQLParameter{text("a"), text("b")}},
		{"zero placeholder", `SELECT $0`, []PostgreSQLParameter{text("a")}},
		{"arguments without placeholders", `SELECT 1`, []PostgreSQLParameter{text("a")}},
	} {
		if _, err := NewRenderedPostgresPhysicalPlan(tc.sql, tc.args, nil); !errors.Is(err, ErrRenderedPostgresPlaceholderMismatch) {
			t.Errorf("%s: error = %v, want ErrRenderedPostgresPlaceholderMismatch", tc.name, err)
		}
	}
	// Zero arguments require zero placeholders.
	okPlan(t, `SELECT resource_seq FROM "s"."fhir_resource"`, nil, nil)
	if _, err := NewRenderedPostgresPhysicalPlan(`SELECT $1`, nil, nil); !errors.Is(err, ErrRenderedPostgresPlaceholderMismatch) {
		t.Fatalf("placeholder without arguments: error = %v", err)
	}
}

func TestRenderedPostgresPlanArgumentValidationMatchesCommandContract(t *testing.T) {
	for _, args := range [][]PostgreSQLParameter{
		{{Type: "money", Value: "1.00"}},
		{{Type: PostgreSQLParameterText, Value: nil}},
		{{Type: PostgreSQLParameterNull, Value: "x"}},
	} {
		if _, err := NewRenderedPostgresPhysicalPlan(`SELECT $1`, args, nil); err == nil {
			t.Fatalf("malformed arguments accepted: %#v", args)
		}
	}
	// SQL NULL is the dedicated null type with no value.
	okPlan(t, `SELECT $1`, []PostgreSQLParameter{{Type: PostgreSQLParameterNull}}, nil)
	// JSON null stays a JSONB value, distinct from SQL NULL.
	okPlan(t, `SELECT $1`, []PostgreSQLParameter{{Type: PostgreSQLParameterJSONB, Value: json.RawMessage("null")}}, nil)
}

func TestRenderedPostgresPlanCopiesAndValidatesOutputColumns(t *testing.T) {
	args := []PostgreSQLParameter{{Type: PostgreSQLParameterText, Value: "x"}}
	columns := []string{"keep me", "second"}
	rendered := okPlan(t, `SELECT $1 AS "c0", $1 AS "c1"`, args, columns)
	if !reflect.DeepEqual(rendered.OutputColumns, []string{"keep me", "second"}) {
		t.Fatalf("output columns = %v", rendered.OutputColumns)
	}
	args[0].Value = "mutated"
	columns[0] = "mutated"
	if rendered.Args[0].Value != "x" || rendered.OutputColumns[0] != "keep me" {
		t.Fatalf("constructor aliased caller data: %#v %v", rendered.Args, rendered.OutputColumns)
	}
	for _, columns := range [][]string{{"ok", "  "}, {""}} {
		if _, err := NewRenderedPostgresPhysicalPlan(`SELECT $1`, args, columns); err == nil {
			t.Fatalf("blank output column accepted: %v", columns)
		}
	}
}

func TestRenderedPostgresPlanSQLCarriesOnlyIdentifiersAndPlaceholders(t *testing.T) {
	// A representative statement from the PostgreSQL renderer path: hostile
	// values live only in the ordered arguments.
	sql := `SELECT r0."logical_id" AS "c0" FROM "owner_schema"."fhir_resource" AS r0` +
		` WHERE r0."project" = $1 AND r0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($2::jsonb))`
	hostile := `x'; DROP TABLE fhir_resource; --`
	rendered := okPlan(t, sql, []PostgreSQLParameter{
		{Type: PostgreSQLParameterText, Value: hostile},
		{Type: PostgreSQLParameterJSONB, Value: json.RawMessage(`["` + hostile + `"]`)},
	}, []string{"logical_id"})
	if strings.Contains(rendered.SQL, hostile) || strings.Contains(rendered.SQL, "DROP") {
		t.Fatalf("value leaked into SQL: %s", rendered.SQL)
	}
}
