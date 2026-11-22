package lower_test

import (
	"context"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"testing"
)

func TestNativePivotKeyIdentityMatchesActualCompiledProjectionNames(t *testing.T) {
	keys := []string{"http://loinc.org|1234-5", "value / unsafe", "  code-a  "}
	for _, aliases := range []map[string]string{nil, {keys[0]: "exact_public_alias"}} {
		pivot := recipe.Pivot{Name: "measurements", ColumnExpr: recipe.Expression{Select: "root.code.text"}, ValueExpr: recipe.Expression{Select: "root.valueString"}, Columns: keys, ColumnAliases: aliases}
		bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "pivot-identity", TranslationVersion: "v1", Outputs: []recipe.Output{{Name: "Observation", RootResourceType: "Observation", RowGrain: "resource", Pivots: []recipe.Pivot{pivot}}}}
		engine := &dataframeexecution.Engine{}
		resolved, err := engine.ResolveBundle(context.Background(), bundle, recipe.RuntimeBindings{Project: "p"})
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range keys {
			expected := lower.NativePivotColumnName("", pivot.Name, key, aliases)
			count := 0
			for _, column := range resolved.Compiled.Outputs[0].OutputSchema {
				if column.Name == expected {
					count++
					if column.SourceOrigin == nil || column.SourceOrigin.SourceKind != "PIVOT" || column.SourceOrigin.OutputKey != key {
						t.Fatal("native helper matched a projection without pivot provenance")
					}
				}
			}
			if count != 1 {
				t.Fatalf("raw key %q maps to %d compiled projections", key, count)
			}
		}
	}
}

func TestCatalogProjectionProvenanceKeepsOperationAndOutputKeys(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "catalog-provenance", TranslationVersion: "v1", Outputs: []recipe.Output{{
		Name: "Patient", RootResourceType: "Patient", RowGrain: "resource",
		Fields: []recipe.Field{{Name: "patient_gender", FieldRef: "gender", SourceKey: "patient_fields", Discovered: true, Expr: recipe.Expression{Select: "root.gender"}}},
	}}}
	plan, err := recipeplan.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range compiled.Outputs[0].OutputSchema {
		if column.Name != "patient_gender" {
			continue
		}
		if column.SourceOrigin == nil || column.SourceOrigin.SourceKind != "CATALOG_PROJECTION" || column.SourceOrigin.SourceKey != "patient_fields" || column.SourceOrigin.OutputKey != "gender" {
			t.Fatalf("catalog provenance = %#v", column.SourceOrigin)
		}
		return
	}
	t.Fatal("compiled catalog output was not emitted")
}
