package compilation

import (
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestRecipeTableReshapePreservesAuthoredPivotAndUnpivotIdentity(t *testing.T) {
	zero := int64(0)
	pivotShape := &authoringv2.TableShape{Reshape: &authoringv2.TableReshape{
		Kind: "PIVOT",
		Pivot: &authoringv2.PivotConstruction{
			ConstructionID: "pivot_construction", GroupKeys: []string{"cohort", "site"}, CategoryColumn: "measure", ValueColumn: "value",
			Categories: []authoringv2.PivotCategory{{
				Key:    authoringv2.TableScalar{Kind: "INTEGER", Integer: &zero},
				Output: authoringv2.ColumnOutput{Column: "baseline", Label: "Baseline"},
			}},
			DuplicatePolicy: "SUM", MissingCellPolicy: "NULL", UnlistedCategoryPolicy: "EXCLUDE_WITH_EVIDENCE",
		},
	}}
	pivot, err := recipeTableReshape(pivotShape)
	if err != nil {
		t.Fatal(err)
	}
	if pivot.Kind != recipe.TableReshapeGroupedPivot || pivot.GroupedPivot == nil || pivot.Unpivot != nil {
		t.Fatalf("recipe pivot union = %#v", pivot)
	}
	if got := pivot.GroupedPivot; got.ConstructionID != "pivot_construction" || !reflect.DeepEqual(got.GroupKeys, []string{"cohort", "site"}) || got.CategoryColumn != "measure" || got.ValueColumn != "value" || got.Categories[0].Output != "baseline" || got.Categories[0].Label != "Baseline" || got.Categories[0].Key.Integer == nil || *got.Categories[0].Key.Integer != 0 || got.DuplicatePolicy != recipe.PivotDuplicateSum || got.MissingCellPolicy != recipe.PivotMissingCellNull || got.UnlistedCategoryPolicy != recipe.PivotUnlistedCategoryExcludeWithEvidence {
		t.Fatalf("recipe pivot lost authored construction: %#v", got)
	}
	semanticPlan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "reshape-translation", TranslationVersion: "test",
		Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", TableReshape: pivot}},
	}, recipe.RuntimeBindings{Project: "project"})
	if err != nil {
		t.Fatal(err)
	}
	semanticPivot := semanticPlan.Outputs[0].TableReshape.GroupedPivot
	if semanticPivot == nil || semanticPivot.ConstructionID != "pivot_construction" || semanticPivot.CategoryColumn != "measure" || semanticPivot.ValueColumn != "value" || !reflect.DeepEqual(semanticPivot.GroupKeys, []string{"cohort", "site"}) || semanticPivot.Categories[0].Key.Kind != recipe.TableScalarInteger || *semanticPivot.Categories[0].Key.Integer != 0 {
		t.Fatalf("semantic pivot lost direct keys/construction ID: %#v", semanticPlan.Outputs[0].TableReshape)
	}

	firstKey, secondKey := "first", "second"
	unpivotShape := &authoringv2.TableShape{Reshape: &authoringv2.TableReshape{
		Kind: "UNPIVOT",
		Unpivot: &authoringv2.UnpivotConstruction{
			ConstructionID: "unpivot_construction", Inputs: []authoringv2.UnpivotInput{
				{Column: "baseline", Key: authoringv2.TableScalar{Kind: "STRING", String: &firstKey}},
				{Column: "followup", Key: authoringv2.TableScalar{Kind: "STRING", String: &secondKey}},
			},
			KeyOutput:   authoringv2.ColumnOutput{Column: "visit", Label: "Visit"},
			ValueOutput: authoringv2.ColumnOutput{Column: "result", Label: "Result"}, NullRowPolicy: "PRESERVE",
		},
	}}
	unpivot, err := recipeTableReshape(unpivotShape)
	if err != nil {
		t.Fatal(err)
	}
	if unpivot.Kind != recipe.TableReshapeUnpivot || unpivot.Unpivot == nil || unpivot.GroupedPivot != nil {
		t.Fatalf("recipe unpivot union = %#v", unpivot)
	}
	semanticPlan, err = semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "reshape-translation", TranslationVersion: "test",
		Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", TableReshape: unpivot}},
	}, recipe.RuntimeBindings{Project: "project"})
	if err != nil {
		t.Fatal(err)
	}
	semanticUnpivot := semanticPlan.Outputs[0].TableReshape.Unpivot
	if semanticUnpivot == nil || semanticUnpivot.ConstructionID != "unpivot_construction" || len(semanticUnpivot.Inputs) != 2 || semanticUnpivot.Inputs[0].Column != "baseline" || semanticUnpivot.Inputs[1].Column != "followup" || semanticUnpivot.KeyOutput != "visit" || semanticUnpivot.ValueOutput != "result" || semanticUnpivot.NullRowPolicy != recipe.UnpivotNullPreserve {
		t.Fatalf("semantic unpivot lost authored construction/input order: %#v", semanticPlan.Outputs[0].TableReshape)
	}
}

func TestRecipeTableReshapePreservesNullAndMissingPivotKeysThroughSemanticPlan(t *testing.T) {
	shape := &authoringv2.TableShape{Reshape: &authoringv2.TableReshape{
		Kind: "PIVOT",
		Pivot: &authoringv2.PivotConstruction{
			ConstructionID: "pivot_sentinels", GroupKeys: []string{"cohort"}, CategoryColumn: "measure", ValueColumn: "value",
			Categories: []authoringv2.PivotCategory{
				{Key: authoringv2.TableScalar{Kind: authoringv2.TableScalarNull}, Output: authoringv2.ColumnOutput{Column: "null_category", Label: "Null"}},
				{Key: authoringv2.TableScalar{Kind: authoringv2.TableScalarMissing}, Output: authoringv2.ColumnOutput{Column: "missing_category", Label: "Missing"}},
			},
			DuplicatePolicy: "ERROR", MissingCellPolicy: "NULL", UnlistedCategoryPolicy: "ERROR",
		},
	}}

	reshape, err := recipeTableReshape(shape)
	if err != nil {
		t.Fatal(err)
	}
	wantKinds := []recipe.TableScalarKind{recipe.TableScalarNull, recipe.TableScalarMissing}
	recipePivot := reshape.GroupedPivot
	if recipePivot == nil || len(recipePivot.Categories) != len(wantKinds) {
		t.Fatalf("recipe pivot categories = %#v, want %d sentinels", recipePivot, len(wantKinds))
	}
	for index, wantKind := range wantKinds {
		key := recipePivot.Categories[index].Key
		if key.Kind != wantKind || key.String != nil || key.Integer != nil || key.Decimal != nil || key.Boolean != nil {
			t.Errorf("recipe category %d key = %#v, want payload-free %s", index, key, wantKind)
		}
	}

	plan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "reshape-sentinels", TranslationVersion: "test",
		Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", TableReshape: reshape}},
	}, recipe.RuntimeBindings{Project: "project"})
	if err != nil {
		t.Fatal(err)
	}
	semanticPivot := plan.Outputs[0].TableReshape.GroupedPivot
	if semanticPivot == nil || len(semanticPivot.Categories) != len(wantKinds) {
		t.Fatalf("semantic pivot categories = %#v, want %d sentinels", semanticPivot, len(wantKinds))
	}
	for index, wantKind := range wantKinds {
		key := semanticPivot.Categories[index].Key
		if key.Kind != wantKind || key.String != nil || key.Integer != nil || key.Decimal != nil || key.Boolean != nil {
			t.Errorf("semantic category %d key = %#v, want payload-free %s", index, key, wantKind)
		}
	}
}

func TestRecipeTableReshapeRejectsNullAndMissingUnpivotKeys(t *testing.T) {
	for _, kind := range []authoringv2.TableScalarKind{authoringv2.TableScalarNull, authoringv2.TableScalarMissing} {
		t.Run(string(kind), func(t *testing.T) {
			shape := &authoringv2.TableShape{Reshape: &authoringv2.TableReshape{
				Kind: "UNPIVOT",
				Unpivot: &authoringv2.UnpivotConstruction{
					ConstructionID: "unpivot_sentinel",
					Inputs:         []authoringv2.UnpivotInput{{Column: "value", Key: authoringv2.TableScalar{Kind: kind}}},
					KeyOutput:      authoringv2.ColumnOutput{Column: "name", Label: "Name"},
					ValueOutput:    authoringv2.ColumnOutput{Column: "result", Label: "Result"},
					NullRowPolicy:  "DROP",
				},
			}}
			if _, err := recipeTableReshape(shape); err == nil {
				t.Fatalf("%s unpivot key was accepted", kind)
			}
		})
	}
}
