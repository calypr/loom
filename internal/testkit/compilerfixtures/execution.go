package compilerfixtures

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/model"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	store "github.com/calypr/loom/internal/store/arango"
)

func ConstructionMissingKeyPolicyOutput(policy recipe.ConstructionGroupMissingKeyPolicy) recipe.Output {
	return recipe.Output{
		Name: "construction_group_missing_key_policy", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}}},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status", Type: "string"}},
			Steps: []recipe.ConstructionStep{{
				ID: "group_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "group_status", MissingKeyPolicy: policy,
					Keys:       []recipe.ConstructionGroupKey{{InputColumnID: "status_id", OutputColumnID: "group_status_id"}},
					Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "row_count_id"}},
				}},
				Outputs: []recipe.StageColumn{{ID: "group_status_id", Name: "status", Type: "string"}, {ID: "row_count_id", Name: "rows", Type: "integer"}},
			}},
		},
	}
}

func ConstructionSummaryOutput() recipe.Output {
	return recipe.Output{
		Name: "construction_summary_oracle", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.valueQuantity.value"}},
		},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "amount_id", Name: "amount"}},
			Steps: []recipe.ConstructionStep{{
				ID: "summary", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "whole_table_summary",
					Aggregates: []recipe.ConstructionGroupAggregate{
						{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"},
						{Operation: recipe.ConstructionGroupCountNonNull, InputColumnID: "status_id", OutputColumnID: "status_count_id"},
						{Operation: recipe.ConstructionGroupCountDistinct, InputColumnID: "status_id", OutputColumnID: "status_distinct_id"},
						{Operation: recipe.ConstructionGroupSum, InputColumnID: "amount_id", OutputColumnID: "amount_sum_id"},
						{Operation: recipe.ConstructionGroupMean, InputColumnID: "amount_id", OutputColumnID: "amount_mean_id"},
					},
				}},
				Outputs: []recipe.StageColumn{
					{ID: "rows_id", Name: "rows"}, {ID: "status_count_id", Name: "status_count"},
					{ID: "status_distinct_id", Name: "status_distinct"}, {ID: "amount_sum_id", Name: "amount_sum"},
					{ID: "amount_mean_id", Name: "amount_mean"},
				},
			}},
		},
	}
}

func ConstructionCountRowsOnlyOutput() recipe.Output {
	return recipe.Output{
		Name: "construction_group_count_rows_only", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}}},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status"}},
			Steps: []recipe.ConstructionStep{{
				ID: "count_rows", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "count_rows",
					Aggregates:     []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
				}},
				Outputs: []recipe.StageColumn{{ID: "rows_id", Name: "rows", Type: "integer"}},
			}},
		},
	}
}

func OpenConstructionReshapeArango(t *testing.T) (context.Context, *store.Client) {
	t.Helper()
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}
	return ctx, client
}

func InsertConstructionReshapeRows(t *testing.T, ctx context.Context, client *store.Client, project, generation string, payloads []map[string]any) {
	t.Helper()
	documents := make([]json.RawMessage, 0, len(payloads))
	for _, payload := range payloads {
		id := payload["id"].(string)
		resource := map[string]any{
			"_key": project + "_" + id, "id": id, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Observation", "payload": payload,
		}
		if authResourcePath, ok := payload["auth_resource_path"]; ok {
			resource["auth_resource_path"] = authResourcePath
		}
		document, err := json.Marshal(resource)
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, document)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", documents, false, "document"); err != nil {
		t.Fatalf("insert construction reshape fixture: %v", err)
	}
}

func (compile OutputCompiler) ExecuteConstructionOutput(t *testing.T, ctx context.Context, client *store.Client, output recipe.Output, project, generation string) []map[string]any {
	t.Helper()
	query := compile.CompileConstructionOutputQuery(t, output, project, generation)
	return ExecuteReshapeOracleQuery(t, ctx, client, query)
}

func (compile OutputCompiler) ExecuteConstructionOutputError(t *testing.T, ctx context.Context, client *store.Client, output recipe.Output, project, generation string) error {
	t.Helper()
	query := compile.CompileConstructionOutputQuery(t, output, project, generation)
	return client.QueryRows(ctx, query.Query, 500, query.BindVars, func(map[string]any) error { return nil })
}

func (compile OutputCompiler) CompileConstructionOutputQuery(t *testing.T, output recipe.Output, project, generation string) model.CompiledQuery {
	t.Helper()
	return compile.CompileConstructionOutputQueryWithBindings(t, output, project, generation, recipe.RuntimeBindings{})
}

func (compile OutputCompiler) CompileConstructionOutputQueryWithBindings(t *testing.T, output recipe.Output, project, generation string, bindings recipe.RuntimeBindings) model.CompiledQuery {
	t.Helper()
	bindings.Project, bindings.DatasetGeneration = project, generation
	compiledOutput := LowerConstructionOutput(t, output, bindings)
	query, err := compile(compiledOutput, bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile construction query: %v", err)
	}
	return query
}

func LowerConstructionOutput(t *testing.T, output recipe.Output, bindings recipe.RuntimeBindings) lower.CompiledRecipeOutput {
	t.Helper()
	project, generation := bindings.Project, bindings.DatasetGeneration
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("build construction recipe plan: %v", err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, project, generation)
	if err != nil {
		t.Fatalf("resolve construction recipe plan: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile construction recipe: %v", err)
	}
	return compiled.Outputs[0]
}
