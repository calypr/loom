package compilation

import (
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"reflect"
	"testing"
)

func constructionRecipePlanForScalarTest(t *testing.T, operation authoringv2.ConstructionOperation, sources, outputs []recipe.StageColumn) *recipeplan.RecipePlan {
	t.Helper()
	mapped, err := recipeConstructionOperation(operation)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := recipeplan.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "construction-translation",
		TranslationVersion:  "test",
		Outputs: []recipe.Output{
			{
				Name:             "patients",
				RootResourceType: "Patient",
				RowGrain:         "patient",
				Construction: &recipe.Construction{
					Version:       authoringv2.ConstructionVersion,
					SourceColumns: sources,
					Steps: []recipe.ConstructionStep{
						{
							ID:        "operation",
							Inputs:    []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
							Operation: mapped,
							Outputs:   outputs,
						},
					},
				},
			},
		},
	}, recipe.RuntimeBindings{Project: "project"})
	if err != nil {
		t.Fatal(err)
	}
	return &plan
}
func TestRecipeConstructionPreservesAuthoredPivotAndUnpivotIdentity(t *testing.T) {
	zero := int64(0)
	operation := authoringv2.ConstructionOperation{
		Kind: authoringv2.ConstructionOperationPivot,
		Pivot: &authoringv2.ConstructionPivot{
			ConstructionID:   "pivot_construction",
			GroupKeyIDs:      []string{"cohort_id", "site_id"},
			CategoryColumnID: "measure_id",
			ValueColumnID:    "value_id",
			Categories: []authoringv2.ConstructionPivotCategory{
				{
					Key:            authoringv2.TableScalar{Kind: authoringv2.TableScalarInteger, Integer: &zero},
					OutputColumnID: "baseline_id",
				},
			},
			DuplicatePolicy:        authoringv2.ConstructionPivotDuplicateSum,
			MissingCellPolicy:      authoringv2.ConstructionPivotMissingNull,
			UnlistedCategoryPolicy: authoringv2.ConstructionPivotUnlistedExcludeWithEvidence,
		},
	}
	sources := []recipe.StageColumn{
		{ID: "cohort_id", Name: "cohort", Label: "Cohort", Type: "string"},
		{ID: "site_id", Name: "site", Label: "Site", Type: "string"},
		{ID: "measure_id", Name: "measure", Label: "Measure", Type: "integer"},
		{ID: "value_id", Name: "value", Label: "Value", Type: "integer"},
	}
	outputs := []recipe.StageColumn{sources[0], sources[1], {ID: "baseline_id", Name: "baseline", Label: "Baseline", Type: "integer"}}
	plan := constructionRecipePlanForScalarTest(t, operation, sources, outputs)
	steps := plan.Outputs[0].Construction.Steps
	if len(steps) != 1 || steps[0].Operation.Pivot == nil || steps[0].Operation.Unpivot != nil {
		t.Fatalf("semantic construction union=%#v", steps)
	}
	pivot := steps[0].Operation.Pivot
	if pivot.ConstructionID != "pivot_construction" || !reflect.DeepEqual(pivot.GroupKeyIDs, []string{"cohort_id", "site_id"}) || pivot.CategoryColumnID != "measure_id" || pivot.ValueColumnID != "value_id" || len(pivot.Categories) != 1 || pivot.Categories[0].OutputColumnID != "baseline_id" || pivot.Categories[0].Key.Kind != recipe.TableScalarInteger || pivot.Categories[0].Key.Integer == nil || *pivot.Categories[0].Key.Integer != 0 || pivot.DuplicatePolicy != recipe.PivotDuplicateSum || pivot.MissingCellPolicy != recipe.PivotMissingCellNull || pivot.UnlistedCategoryPolicy != recipe.PivotUnlistedCategoryExcludeWithEvidence {
		t.Fatalf("semantic pivot lost authored identity/key/policies: %#v", pivot)
	}
	if !reflect.DeepEqual(steps[0].Outputs, outputs) {
		t.Fatalf("pivot names/labels changed: %#v", steps[0].Outputs)
	}
	firstKey, secondKey := "first", "second"
	unpivotOperation := authoringv2.ConstructionOperation{
		Kind: authoringv2.ConstructionOperationUnpivot,
		Unpivot: &authoringv2.ConstructionUnpivot{
			ConstructionID: "unpivot_construction",
			Inputs: []authoringv2.ConstructionUnpivotInput{
				{
					ColumnID: "baseline_id",
					Key:      authoringv2.TableScalar{Kind: authoringv2.TableScalarString, String: &firstKey},
				},
				{
					ColumnID: "followup_id",
					Key:      authoringv2.TableScalar{Kind: authoringv2.TableScalarString, String: &secondKey},
				},
			},
			KeyOutputColumnID:   "visit_id",
			ValueOutputColumnID: "result_id",
			NullRowPolicy:       authoringv2.ConstructionUnpivotPreserve,
		},
	}
	unpivotSources := []recipe.StageColumn{
		{ID: "baseline_id", Name: "baseline", Label: "Baseline", Type: "integer"},
		{ID: "followup_id", Name: "followup", Label: "Followup", Type: "integer"},
	}
	unpivotOutputs := []recipe.StageColumn{
		{ID: "visit_id", Name: "visit", Label: "Visit", Type: "string"},
		{ID: "result_id", Name: "result", Label: "Result", Type: "integer"},
	}
	plan = constructionRecipePlanForScalarTest(t, unpivotOperation, unpivotSources, unpivotOutputs)
	steps = plan.Outputs[0].Construction.Steps
	unpivot := steps[0].Operation.Unpivot
	if unpivot == nil || unpivot.ConstructionID != "unpivot_construction" || len(unpivot.Inputs) != 2 || unpivot.Inputs[0].ColumnID != "baseline_id" || unpivot.Inputs[1].ColumnID != "followup_id" || unpivot.Inputs[0].Key.String == nil || *unpivot.Inputs[0].Key.String != "first" || unpivot.Inputs[1].Key.String == nil || *unpivot.Inputs[1].Key.String != "second" || unpivot.KeyOutputColumnID != "visit_id" || unpivot.ValueOutputColumnID != "result_id" || unpivot.NullRowPolicy != recipe.UnpivotNullPreserve || !reflect.DeepEqual(steps[0].Outputs, unpivotOutputs) {
		t.Fatalf("semantic unpivot lost authored construction/input order/output labels: %#v", steps)
	}

}
func TestRecipeConstructionPreservesNullAndMissingPivotKeysThroughSemanticPlan(t *testing.T) {
	operation := authoringv2.ConstructionOperation{
		Kind: authoringv2.ConstructionOperationPivot,
		Pivot: &authoringv2.ConstructionPivot{
			ConstructionID:   "pivot_sentinels",
			GroupKeyIDs:      []string{"cohort_id"},
			CategoryColumnID: "measure_id",
			ValueColumnID:    "value_id",
			Categories: []authoringv2.ConstructionPivotCategory{
				{Key: authoringv2.TableScalar{Kind: authoringv2.TableScalarNull}, OutputColumnID: "null_id"},
				{Key: authoringv2.TableScalar{Kind: authoringv2.TableScalarMissing}, OutputColumnID: "missing_id"},
			},
			DuplicatePolicy:        authoringv2.ConstructionPivotDuplicateError,
			MissingCellPolicy:      authoringv2.ConstructionPivotMissingNull,
			UnlistedCategoryPolicy: authoringv2.ConstructionPivotUnlistedError,
		},
	}
	sources := []recipe.StageColumn{
		{ID: "cohort_id", Name: "cohort", Label: "Cohort", Type: "string"},
		{ID: "measure_id", Name: "measure", Label: "Measure", Type: "string"},
		{ID: "value_id", Name: "value", Label: "Value", Type: "integer"},
	}
	outputs := []recipe.StageColumn{
		sources[0],
		{ID: "null_id", Name: "null_category", Label: "Null", Type: "integer"},
		{ID: "missing_id", Name: "missing_category", Label: "Missing", Type: "integer"},
	}
	mapped, err := recipeConstructionOperation(operation)
	if err != nil {
		t.Fatal(err)
	}
	plan := constructionRecipePlanForScalarTest(t, operation, sources, outputs)
	for _, pivot := range []*recipe.ConstructionPivot{mapped.Pivot, plan.Outputs[0].Construction.Steps[0].Operation.Pivot} {
		want := []recipe.TableScalarKind{recipe.TableScalarNull, recipe.TableScalarMissing}
		if pivot == nil || len(pivot.Categories) != len(want) {
			t.Fatalf("pivot categories=%#v", pivot)
		}
		for i, kind := range want {
			key := pivot.Categories[i].Key
			if key.Kind != kind || key.String != nil || key.Integer != nil || key.Decimal != nil || key.Boolean != nil {
				t.Errorf("category %d=%#v, want payload-free %s", i, key, kind)
			}
		}
	}
}
func TestRecipeConstructionRejectsNullAndMissingUnpivotKeys(t *testing.T) {
	for _, kind := range []authoringv2.TableScalarKind{authoringv2.TableScalarNull, authoringv2.TableScalarMissing} {
		t.Run(string(kind), func(t *testing.T) {
			operation := authoringv2.ConstructionOperation{
				Kind: authoringv2.ConstructionOperationUnpivot,
				Unpivot: &authoringv2.ConstructionUnpivot{
					ConstructionID:      "unpivot_sentinel",
					Inputs:              []authoringv2.ConstructionUnpivotInput{{ColumnID: "value_id", Key: authoringv2.TableScalar{Kind: kind}}},
					KeyOutputColumnID:   "name_id",
					ValueOutputColumnID: "result_id",
					NullRowPolicy:       authoringv2.ConstructionUnpivotDrop,
				},
			}
			if _, err := recipeConstructionOperation(operation); err == nil {
				t.Fatalf("%s unpivot key accepted", kind)
			}
		})
	}
}
