package expression

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestFromRecipeBuildsTypedAST(t *testing.T) {
	input := recipe.Expression{Call: "reference_id", Args: []recipe.Expression{{Select: "root.subject.reference"}}}
	expr, err := FromRecipe(input)
	if err != nil {
		t.Fatal(err)
	}
	if expr.Kind != CallNode || expr.Call == nil || expr.Call.Name != "reference_id" || expr.Call.Args[0].Selector.Context != "root" {
		t.Fatalf("unexpected expression: %#v", expr)
	}
	ctx := TypeContext{Selectors: map[string]Type{"root.subject.reference": Type{Kind: KindString, Cardinality: OptionalOne}}}
	checked, err := expr.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if checked.Type.Kind != KindString {
		t.Fatalf("type = %s", checked.Type)
	}
}
