package command_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
)

func okPlan(t *testing.T, sql string, args []command.PostgreSQLParameter, columns []string) command.RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := command.NewRenderedPostgresPhysicalPlan(sql, args, columns)
	if err != nil {
		t.Fatalf("NewRenderedPostgresPhysicalPlan(%q): %v", sql, err)
	}
	return rendered
}

func TestRenderedPostgresPlanRequiresSQLAndRejectsAQLBindSyntax(t *testing.T) {
	if _, err := command.NewRenderedPostgresPhysicalPlan("   ", nil, nil); err == nil {
		t.Fatal("blank SQL accepted")
	}
	for _, sql := range []string{
		`SELECT @value`,
		`FOR d IN @@collection RETURN d`,
		`SELECT $1 -- @@ sneaky`,
	} {
		_, err := command.NewRenderedPostgresPhysicalPlan(sql, []command.PostgreSQLParameter{{Type: command.PostgreSQLParameterText, Value: "x"}}, nil)
		if !errors.Is(err, command.ErrRenderedPostgresAQLBindSyntax) {
			t.Errorf("SQL %q error = %v, want ErrRenderedPostgresAQLBindSyntax", sql, err)
		}
	}
}

func TestRenderedPostgresPlanPlaceholderContract(t *testing.T) {
	text := func(v string) command.PostgreSQLParameter {
		return command.PostgreSQLParameter{Type: command.PostgreSQLParameterText, Value: v}
	}
	okPlan(t, `SELECT $1, $1 WHERE $2 = $1`, []command.PostgreSQLParameter{text("a"), text("b")}, nil)

	for _, tc := range []struct {
		name string
		sql  string
		args []command.PostgreSQLParameter
	}{
		{"out of range", `SELECT $3`, []command.PostgreSQLParameter{text("a")}},
		{"gap skips a number", `SELECT $1, $3`, []command.PostgreSQLParameter{text("a"), text("b"), text("c")}},
		{"unreferenced tail argument", `SELECT $1`, []command.PostgreSQLParameter{text("a"), text("b")}},
		{"zero placeholder", `SELECT $0`, []command.PostgreSQLParameter{text("a")}},
		{"arguments without placeholders", `SELECT 1`, []command.PostgreSQLParameter{text("a")}},
	} {
		if _, err := command.NewRenderedPostgresPhysicalPlan(tc.sql, tc.args, nil); !errors.Is(err, command.ErrRenderedPostgresPlaceholderMismatch) {
			t.Errorf("%s: error = %v, want ErrRenderedPostgresPlaceholderMismatch", tc.name, err)
		}
	}
	okPlan(t, `SELECT resource_seq FROM "s"."fhir_resource"`, nil, nil)
	if _, err := command.NewRenderedPostgresPhysicalPlan(`SELECT $1`, nil, nil); !errors.Is(err, command.ErrRenderedPostgresPlaceholderMismatch) {
		t.Fatalf("placeholder without arguments: error = %v", err)
	}
}

func TestRenderedPostgresPlanArgumentValidationMatchesCommandContract(t *testing.T) {
	for _, args := range [][]command.PostgreSQLParameter{
		{{Type: "money", Value: "1.00"}},
		{{Type: command.PostgreSQLParameterText, Value: nil}},
		{{Type: command.PostgreSQLParameterNull, Value: "x"}},
	} {
		if _, err := command.NewRenderedPostgresPhysicalPlan(`SELECT $1`, args, nil); err == nil {
			t.Fatalf("malformed arguments accepted: %#v", args)
		}
	}
	okPlan(t, `SELECT $1`, []command.PostgreSQLParameter{{Type: command.PostgreSQLParameterNull}}, nil)
	okPlan(t, `SELECT $1`, []command.PostgreSQLParameter{{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage("null")}}, nil)
}

func TestRenderedPostgresPlanCopiesAndValidatesOutputColumns(t *testing.T) {
	args := []command.PostgreSQLParameter{{Type: command.PostgreSQLParameterText, Value: "x"}}
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
		if _, err := command.NewRenderedPostgresPhysicalPlan(`SELECT $1`, args, columns); err == nil {
			t.Fatalf("blank output column accepted: %v", columns)
		}
	}
}

func TestRenderedPostgresPlanSQLCarriesOnlyIdentifiersAndPlaceholders(t *testing.T) {
	sql := `SELECT r0."logical_id" AS "c0" FROM "owner_schema"."fhir_resource" AS r0` +
		` WHERE r0."project" = $1 AND r0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($2::jsonb))`
	hostile := `x'; DROP TABLE fhir_resource; --`
	rendered := okPlan(t, sql, []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: hostile},
		{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`["` + hostile + `"]`)},
	}, []string{"logical_id"})
	if strings.Contains(rendered.SQL, hostile) || strings.Contains(rendered.SQL, "DROP") {
		t.Fatalf("value leaked into SQL: %s", rendered.SQL)
	}
}
