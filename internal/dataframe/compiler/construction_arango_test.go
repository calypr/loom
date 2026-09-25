package compiler

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestConstructionPivotDeriveFilterUnpivotRowsAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}

	project := "loom_construction_chain_" + uuid.NewString()
	generation := "generation-construction-chain"
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := client.ExecuteAQL(cleanupCtx,
			"FOR document IN Observation FILTER document.project == @project REMOVE document IN Observation",
			map[string]any{"project": project},
		); err != nil {
			t.Errorf("remove construction fixtures: %v", err)
		}
	}()
	documents := make([]json.RawMessage, 0, len(reshapeOracleSourceRows))
	for _, row := range reshapeOracleSourceRows {
		payload := map[string]any{
			"id": row.SourceID, "resourceType": "Observation", "status": *row.Group.Text.String,
			"valueInteger": *row.NumericCategory.Integer, "valueString": row.Text,
			"code":         map[string]any{"text": *row.StringCategory.String},
			"valueBoolean": row.Flag, "issued": row.Unrelated,
		}
		if row.Number != nil {
			payload["valueQuantity"] = map[string]any{"value": *row.Number}
		} else {
			payload["valueQuantity"] = map[string]any{"value": nil}
		}
		document, marshalErr := json.Marshal(map[string]any{
			"_key": project + "_" + row.SourceID, "id": row.SourceID, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Observation", "payload": payload,
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		documents = append(documents, document)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", documents, false, "document"); err != nil {
		t.Fatalf("insert construction fixture: %v", err)
	}

	output := constructionOracleOutput()
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "construction-oracle", TranslationVersion: "test", Outputs: []recipe.Output{output}}
	bindings := recipe.RuntimeBindings{Project: project, DatasetGeneration: generation}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, project, generation)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	query, err := CompileRecipeOutputWithPolicy(compiled.Outputs[0], bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rows := executeReshapeOracleQuery(t, ctx, client, query)
	if len(rows) != 2 {
		t.Fatalf("construction chain rows = %#v, want alpha and beta rows from the one qualifying intermediate group", rows)
	}
	wantAmounts := map[string]float64{"alpha": 7, "beta": 3.5}
	rowIDs := map[string]bool{}
	for _, row := range rows {
		measure, ok := row["measure"].(string)
		if !ok {
			t.Fatalf("unpivot key = %#v, want string", row["measure"])
		}
		wantAmount, ok := wantAmounts[measure]
		if !ok || !constructionNumericEqual(row["amount"], wantAmount) {
			t.Errorf("unpivot amount for %q = %#v, want %v", measure, row["amount"], wantAmount)
		}
		if row["group_text"] != "final" || !constructionNumericEqual(row["group_number"], 1) || !constructionNumericEqual(row["total"], 10.5) {
			t.Errorf("intermediate group/derive values = %#v, want final / 1 / 10.5", row)
		}
		id, ok := row["__loom_row_id"].(string)
		if !ok || id == "" || rowIDs[id] {
			t.Errorf("unpivot row identity is missing or duplicated: %#v", row["__loom_row_id"])
		}
		rowIDs[id] = true
	}
	if len(rowIDs) != 2 {
		t.Fatalf("unpivot identities = %#v, want two distinct rows", rowIDs)
	}
}

func constructionOracleOutput() recipe.Output {
	stringAlpha, stringBeta := "alpha", "beta"
	minimumTotal := 10.0
	return recipe.Output{
		Name: "construction_oracle", RootResourceType: "Observation", RowGrain: "observation", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "group_text", ColumnID: "group_text_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "group_number", ColumnID: "group_number_id", Expr: recipe.Expression{Select: "root.valueInteger"}},
			{Name: "category", ColumnID: "category_id", Expr: recipe.Expression{Select: "root.code.text"}},
			{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.valueQuantity.value"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "group_text_id", Name: "group_text"}, {ID: "group_number_id", Name: "group_number"},
				{ID: "category_id", Name: "category"}, {ID: "amount_id", Name: "amount"},
			},
			Steps: []recipe.ConstructionStep{
				{
					ID: "pivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
						ConstructionID: "oracle_pivot", GroupKeyIDs: []string{"group_text_id", "group_number_id"},
						CategoryColumnID: "category_id", ValueColumnID: "amount_id",
						Categories: []recipe.ConstructionPivotCategory{
							{Key: reshapeOracleString(stringAlpha), OutputColumnID: "alpha_id"},
							{Key: reshapeOracleString(stringBeta), OutputColumnID: "beta_id"},
						},
						DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
						UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
					}},
					Outputs: []recipe.StageColumn{
						{ID: "group_text_id", Name: "group_text"}, {ID: "group_number_id", Name: "group_number"},
						{ID: "alpha_id", Name: "alpha"}, {ID: "beta_id", Name: "beta"},
					},
				},
				{
					ID: "derive", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "pivot"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionDeriveOp, Derive: &recipe.ConstructionDerive{
						ConstructionID: "oracle_total", OutputColumnID: "total_id", Operation: recipe.DerivedAdd,
						Left:               recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "alpha_id"},
						Right:              recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "beta_id"},
						MissingInputPolicy: recipe.MissingInputPropagateNull,
					}},
					Outputs: []recipe.StageColumn{
						{ID: "group_text_id", Name: "group_text"}, {ID: "group_number_id", Name: "group_number"},
						{ID: "alpha_id", Name: "alpha"}, {ID: "beta_id", Name: "beta"}, {ID: "total_id", Name: "total"},
					},
				},
				{
					ID: "filter", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "derive"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "total_id", Operator: recipe.FilterGreaterEq,
						Values: []recipe.FilterValue{{Kind: recipe.FilterDecimal, Decimal: &minimumTotal}},
					}},
					Outputs: []recipe.StageColumn{
						{ID: "group_text_id", Name: "group_text"}, {ID: "group_number_id", Name: "group_number"},
						{ID: "alpha_id", Name: "alpha"}, {ID: "beta_id", Name: "beta"}, {ID: "total_id", Name: "total"},
					},
				},
				{
					ID: "unpivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "filter"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionUnpivotOp, Unpivot: &recipe.ConstructionUnpivot{
						ConstructionID: "oracle_unpivot", Inputs: []recipe.ConstructionUnpivotInput{
							{ColumnID: "alpha_id", Key: reshapeOracleString(stringAlpha)},
							{ColumnID: "beta_id", Key: reshapeOracleString(stringBeta)},
						}, KeyOutputColumnID: "measure_id", ValueOutputColumnID: "amount_output_id", NullRowPolicy: recipe.UnpivotNullPreserve,
					}},
					Outputs: []recipe.StageColumn{
						{ID: "group_text_id", Name: "group_text"}, {ID: "group_number_id", Name: "group_number"},
						{ID: "total_id", Name: "total"}, {ID: "measure_id", Name: "measure"}, {ID: "amount_output_id", Name: "amount"},
					},
				},
			},
		},
	}
}

func constructionNumericEqual(value any, want float64) bool {
	switch got := value.(type) {
	case float64:
		return math.Abs(got-want) < 1e-9
	case int64:
		return float64(got) == want
	case int:
		return float64(got) == want
	default:
		return false
	}
}
