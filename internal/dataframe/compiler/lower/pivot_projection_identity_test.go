package lower_test

import (
	"context"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"testing"
)

func TestNativePivotKeyIdentityMatchesActualCompiledProjectionNames(t *testing.T) {
	keys := []string{"http://loinc.org|1234-5", "value / unsafe"}
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
					if column.SourceOrigin == nil || column.SourceOrigin.SourceKind != "PIVOT" {
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
