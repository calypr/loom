package expression

import (
	"encoding/json"
	"strings"
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

func TestFromRecipeNormalizesPersistedCastTarget(t *testing.T) {
	input := recipe.Expression{Call: "cast", Args: []recipe.Expression{
		{Literal: json.RawMessage(`"7"`)},
		{Literal: json.RawMessage(`"integer"`)},
	}}
	expr, err := FromRecipe(input)
	if err != nil {
		t.Fatal(err)
	}
	if expr.Call == nil || len(expr.Call.Args) != 1 {
		t.Fatalf("cast args = %#v, want one value argument", expr.Call)
	}
	if expr.Call.Target == nil || *expr.Call.Target != (Type{Kind: KindInteger, Cardinality: RequiredOne}) {
		t.Fatalf("cast target = %#v, want integer/required_one", expr.Call.Target)
	}
	checked, err := expr.Check(TypeContext{})
	if err != nil {
		t.Fatalf("check normalized cast: %v", err)
	}
	if checked.Type != (Type{Kind: KindInteger, Cardinality: RequiredOne}) {
		t.Fatalf("cast type = %s, want integer/required_one", checked.Type)
	}
}

func TestFromRecipeRejectsInvalidPersistedCastTargets(t *testing.T) {
	tests := []struct {
		name   string
		target recipe.Expression
		want   string
	}{
		{
			name:   "nonliteral",
			target: recipe.Expression{Select: "root.target"},
			want:   "cast target must be a literal",
		},
		{
			name:   "malformed JSON",
			target: recipe.Expression{Literal: json.RawMessage(`{"`)},
			want:   "cast target literal is invalid JSON",
		},
		{
			name:   "nonstring",
			target: recipe.Expression{Literal: json.RawMessage(`123`)},
			want:   "cast target must be a string",
		},
		{
			name:   "unsupported",
			target: recipe.Expression{Literal: json.RawMessage(`"timestamp"`)},
			want:   "unsupported cast target",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := recipe.Expression{Call: "cast", Args: []recipe.Expression{
				{Literal: json.RawMessage(`"7"`)},
				test.target,
			}}
			expr, err := FromRecipe(input)
			if err == nil {
				t.Fatal("invalid cast target unexpectedly converted")
			}
			if expr != (Expression{}) {
				t.Fatalf("invalid cast target returned AST: %#v", expr)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestFromRecipeRejectsMixedLiteralArrayWithoutPanic(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("mixed literal array panicked: %v", recovered)
		}
	}()

	_, err := FromRecipe(recipe.Expression{Literal: json.RawMessage(`[1,true]`)})
	if err == nil {
		t.Fatal("mixed literal array unexpectedly converted")
	}
	if !strings.Contains(err.Error(), "mixed") {
		t.Fatalf("mixed literal array error = %v, want mixed-kind validation", err)
	}
}
