package semantic

import (
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
			DynamicColumns: []recipe.DynamicColumn{{Name: "code", Source: recipe.Expression{Select: "identifier[].value"}, MaxColumns: 4, Columns: []string{"b", "a"}}},
		}},
	}
	plan, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p", DatasetGeneration: "g"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveRecipePlan(plan, "scope-1", "g")
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

func TestResolveRecipePlanHonorsExplicitEmptyDynamicColumnPrefix(t *testing.T) {
	prefix := ""
	bundle := recipe.Bundle{
		RecipeSchemaVersion: 1,
		Name:                "dynamic",
		TranslationVersion:  "test",
		Outputs: []recipe.Output{{
			Name: "DocumentReference", RootResourceType: "DocumentReference", RowGrain: "file",
			DynamicColumns: []recipe.DynamicColumn{{
				Name: "attachment_extensions", ColumnPrefix: &prefix,
				Source: recipe.Expression{Select: "content[].attachment.extension[]"},
				Key:    &recipe.Expression{Call: "last_segment", Args: []recipe.Expression{{Select: "item.url"}}},
				Value:  &recipe.Expression{Select: "item.valueString"}, Columns: []string{"source_path"}, MaxColumns: 4,
				ColumnSourceKeys: map[string]string{"source_path": "https://example.test/source_path"},
			}},
		}},
	}
	plan, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p", DatasetGeneration: "g"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Outputs) != 1 || len(plan.Outputs[0].Root.DynamicMaps) != 1 || !plan.Outputs[0].Root.DynamicMaps[0].AllowUnknownKeys {
		t.Fatalf("fixed keyed map must ignore unrelated sibling keys: %#v", plan.Outputs)
	}
	resolved, err := ResolveRecipePlan(plan, "scope-1", "g")
	if err != nil {
		t.Fatal(err)
	}
	columns := resolved.ResolvedColumns["DocumentReference:attachment_extensions"]
	if len(columns) != 1 || columns[0].Column.Name != "source_path" || columns[0].Column.SourceKey != "https://example.test/source_path" {
		t.Fatalf("unexpected compatibility dynamic column: %#v", columns)
	}
}

func TestResolveRecipePlanRequiresDiscoveryForUnfrozenDynamicMap(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "dynamic", TranslationVersion: "test", Outputs: []recipe.Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "patient", DynamicColumns: []recipe.DynamicColumn{{Name: "code", Source: recipe.Expression{Select: "identifier[].value"}}}}}}
	plan, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveRecipePlan(plan, "scope", "g"); err == nil {
		t.Fatal("expected missing scoped discovery error")
	}
}
