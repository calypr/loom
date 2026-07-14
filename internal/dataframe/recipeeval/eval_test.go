package recipeeval

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestEvaluateExpansionAndGenericCalls(t *testing.T) {
	out := recipe.Output{
		Name:             "members",
		RootResourceType: "Group",
		RowGrain:         "member",
		Expand:           &recipe.Expansion{From: recipe.Expression{Select: "root.member"}, As: "member"},
		Identity:         &recipe.Identity{Name: "id", Expr: recipe.Expression{Call: "reference_id", Args: []recipe.Expression{{Select: "member.reference"}}}},
		Fields: []recipe.Field{
			{Name: "group_id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "name", Expr: recipe.Expression{Call: "coalesce", Args: []recipe.Expression{{Select: "member.display"}, {Literal: []byte(`"unknown"`)}}}},
		},
	}
	root := map[string]any{"id": "g1", "member": []any{
		map[string]any{"reference": "Patient/p1", "display": "Alice"},
		map[string]any{"reference": "Patient/p2"},
	}}
	rows, err := EvaluateOutput(out, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0]["id"] != "p1" || rows[1]["id"] != "p2" || rows[1]["name"] != "unknown" {
		t.Fatalf("unexpected rows: %#v", rows)
	}
}

func TestEvaluateTraversalAndDynamicColumns(t *testing.T) {
	out := recipe.Output{
		Name: "root", RootResourceType: "Root", RowGrain: "root", CollisionPolicy: "overwrite",
		Fields:         []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Traversals:     []recipe.Traversal{{Name: "child", ToResourceType: "Child", Alias: "child", Fields: []recipe.Field{{Name: "child_value", Expr: recipe.Expression{Select: "child.value"}}}}},
		DynamicColumns: []recipe.DynamicColumn{{Name: "code", Source: recipe.Expression{Select: "root.codes"}, Key: ptr(recipe.Expression{Select: "item.key"}), Value: ptr(recipe.Expression{Select: "item.value"})}},
	}
	root := map[string]any{"id": "r1", "codes": []any{map[string]any{"key": "A-1", "value": 3}}}
	rows, err := EvaluateOutput(out, root, func(_ map[string]any, _ recipe.Traversal) ([]map[string]any, error) {
		return []map[string]any{{"value": "ok"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["child_value"] != "ok" || rows[0]["code_A_1"] != int64(3) && rows[0]["code_A_1"] != 3 {
		t.Fatalf("unexpected row: %#v", rows)
	}
}

func ptr(v recipe.Expression) *recipe.Expression { return &v }
