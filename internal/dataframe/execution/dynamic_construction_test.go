package execution

import (
	"context"
	"fmt"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestPreviewFilterPreservesDynamicRuntimeKeysAndStillRejectsDrift(t *testing.T) {
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "dynamic-filter", TranslationVersion: "test",
		Outputs: []recipe.Output{{
			Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
			Fields:         []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
			DynamicColumns: []recipe.DynamicColumn{{Name: "identifier", Source: recipe.Expression{Select: "root.identifier[]"}, Key: &recipe.Expression{Select: "item.value"}, Columns: []string{"x"}}},
			Construction: &recipe.Construction{
				Version:       1,
				SourceColumns: []recipe.StageColumn{{ID: "id", Name: "id"}, {ID: "identifier_x", Name: "identifier_x"}},
				Steps: []recipe.ConstructionStep{{
					ID: "filter", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{ColumnID: "id", Operator: recipe.FilterExists}},
					Outputs:   []recipe.StageColumn{{ID: "id", Name: "id"}, {ID: "identifier_x", Name: "identifier_x"}},
				}},
			},
		}},
	}
	row := func(runtimeKeys []any) map[string]any {
		return map[string]any{"id": "patient-1", "identifier_x": "x", "__loom_row_id": "patient-1", "__loom_dynamic_runtime_keys": map[string]any{"identifier": runtimeKeys}}
	}
	run := func(keys []any) error {
		engine, err := New(Config{
			Registry:    invalidRecipeRegistry{},
			ScopeDigest: func(recipe.RuntimeBindings) string { return "scope" },
			QueryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
				return visit(row(keys))
			},
		})
		if err != nil {
			return err
		}
		resolved, err := engine.CompileResolvedBundle(context.Background(), bundle, recipe.RuntimeBindings{Project: "test-project"})
		if err != nil {
			return err
		}
		found := false
		for _, column := range resolved.Compiled.Outputs[0].OutputSchema {
			if column.Name == "__loom_dynamic_runtime_keys" && column.Internal && !column.Identity {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("compiled filter output dropped internal dynamic-key validation metadata")
		}
		_, err = engine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "Patient", Limit: 10}, func(value map[string]any) error {
			if _, exposed := value["__loom_dynamic_runtime_keys"]; exposed {
				return fmt.Errorf("validation metadata was exposed as public data")
			}
			return nil
		})
		return err
	}
	if err := run([]any{"x"}); err != nil {
		t.Fatalf("preview with a known runtime key failed: %v", err)
	}
	if err := run([]any{"x", "unexpected"}); err == nil {
		t.Fatal("preview with unknown runtime key succeeded")
	} else if userErr, ok := dataframeerrors.AsUserError(err); !ok || userErr.Code() != string(dataframeerrors.CodeDynamicSchemaDrift) {
		t.Fatalf("preview with unknown runtime key error = %v, want dynamic schema drift", err)
	}
}
