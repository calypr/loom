package postgres

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

// pivot14NonCodedPivotBundle authors the single-stage NON-coded
// construction PIVOT (recipe PIVOT operation, probe C3 shape): the shape
// that must keep the merged StageSequence refusal byte-identical because
// the coded door keys on CodedCorrelation payload presence alone.
func pivot14NonCodedPivotBundle(t *testing.T, name string) recipe.Bundle {
	t.Helper()
	alpha := "alpha"
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: name, TranslationVersion: "test",
		Outputs: []recipe.Output{{
			Name: name, RootResourceType: "Observation", RowGrain: "resource",
			Fields: []recipe.Field{
				{Name: "grp", ColumnID: "grp_id", Expr: recipe.Expression{Select: "root.status"}},
				{Name: "category", ColumnID: "cat_id", Expr: recipe.Expression{Select: "root.status"}},
				{Name: "amount", ColumnID: "val_id", Expr: recipe.Expression{Select: "root.valueQuantity.value"}},
			},
			Construction: &recipe.Construction{
				Version: 1,
				SourceColumns: []recipe.StageColumn{
					{ID: "grp_id", Name: "grp"}, {ID: "cat_id", Name: "category"}, {ID: "val_id", Name: "amount"},
				},
				Steps: []recipe.ConstructionStep{{
					ID:     name + "_step",
					Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{
						Kind: recipe.ConstructionPivotOp,
						Pivot: &recipe.ConstructionPivot{
							ConstructionID:   name,
							GroupKeyIDs:      []string{"grp_id"},
							CategoryColumnID: "cat_id", ValueColumnID: "val_id",
							Categories: []recipe.ConstructionPivotCategory{
								{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &alpha}, OutputColumnID: "alpha_id"},
							},
							DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
							UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
						},
					},
					Outputs: []recipe.StageColumn{
						{ID: "grp_id", Name: "grp"}, {ID: "alpha_id", Name: "alpha", Type: "decimal", Nullable: true},
					},
				}},
			},
		}},
	}
}
