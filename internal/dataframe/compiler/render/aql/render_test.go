package aql

import (
	"reflect"
	"testing"
)

func TestPruneUnusedRuntimeBindVarsUsesExactAQLBindTokens(t *testing.T) {
	const prefix = "recipe_dynamic_fixed_e19cdb97c18880ab_key_"
	bindVars := map[string]any{
		prefix + "1":      "first column",
		prefix + "10":     "tenth column",
		"dynamic_table":   "regular bind with a collection name",
		"@dynamic_table":  "collection bind",
		"quoted":          "inside a string",
		"backtick_quoted": "inside a backtick identifier",
		"line_comment":    "inside a line comment",
		"block_comment":   "inside a block comment",
		"unused":          "not referenced",
	}
	query := `FILTER row.value == @recipe_dynamic_fixed_e19cdb97c18880ab_key_10
FOR table IN @@dynamic_table
LET literal = "@quoted"
// @line_comment
/* @block_comment */`
	query += "\nLET identifier = row.`@backtick_quoted`"

	got := pruneUnusedRuntimeBindVars(bindVars, query)
	want := map[string]any{
		prefix + "10":    "tenth column",
		"@dynamic_table": "collection bind",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pruneUnusedRuntimeBindVars() = %#v, want %#v", got, want)
	}
}
