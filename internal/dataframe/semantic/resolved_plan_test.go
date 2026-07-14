package semantic

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestResolveRecipePlanFreezesScopedDynamicColumns(t *testing.T) {
	bundle := recipe.Bundle{
		RecipeSchemaVersion: 1,
		Name:                "dynamic",
		TranslationVersion:  "test",
		Outputs: []recipe.Output{{
			Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
			Fields:         []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "id"}}},
			DynamicColumns: []recipe.DynamicColumn{{Name: "code", Source: recipe.Expression{Select: "identifier[].value"}, MaxColumns: 4}},
		}},
	}
	plan, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p", DatasetGeneration: "g"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveRecipePlan(context.Background(), plan, "scope-1", "g", func(_ context.Context, _ OutputPlan, _ SemanticDynamicMap, _ recipe.RuntimeBindings) ([]DiscoveryCandidate, error) {
		return []DiscoveryCandidate{{Key: "b", ValueType: "string"}, {Key: "a", ValueType: "integer"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	columns := resolved.ResolvedColumns["Patient:code"]
	if len(columns) != 2 || columns[0].Column.Name != "code_a" || columns[1].Column.Name != "code_b" {
		t.Fatalf("unexpected frozen columns: %#v", columns)
	}
	if resolved.ResolvedSchemaDigest == "" || resolved.ScopeDigest != "scope-1" || resolved.SourceGeneration != "g" {
		t.Fatalf("missing resolved provenance: %#v", resolved)
	}
}

func TestResolveRecipePlanRequiresDiscoveryForUnfrozenDynamicMap(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "dynamic", TranslationVersion: "test", Outputs: []recipe.Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "patient", DynamicColumns: []recipe.DynamicColumn{{Name: "code", Source: recipe.Expression{Select: "identifier[].value"}}}}}}
	plan, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveRecipePlan(context.Background(), plan, "scope", "g", nil); err == nil {
		t.Fatal("expected missing scoped discovery error")
	}
}
