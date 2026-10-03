package compilation

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func recipeTableReshape(shape *authoringv2.TableShape) (*recipe.TableReshape, error) {
	if shape == nil || shape.Reshape == nil {
		return nil, nil
	}
	switch shape.Reshape.Kind {
	case "PIVOT":
		if shape.Reshape.Pivot == nil || shape.Reshape.Unpivot != nil {
			return nil, fmt.Errorf("pivot reshape requires exactly one pivot payload")
		}
		authored := shape.Reshape.Pivot
		pivot := &recipe.GroupedPivot{
			ConstructionID: string(authored.ConstructionID), GroupKeys: append([]string(nil), authored.GroupKeys...),
			CategoryColumn: authored.CategoryColumn, ValueColumn: authored.ValueColumn,
			DuplicatePolicy:        recipe.PivotDuplicatePolicy(authored.DuplicatePolicy),
			MissingCellPolicy:      recipe.PivotMissingCellPolicy(authored.MissingCellPolicy),
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryPolicy(authored.UnlistedCategoryPolicy),
			Categories:             make([]recipe.GroupedPivotCategory, 0, len(authored.Categories)),
		}
		for _, category := range authored.Categories {
			key, err := recipeTableScalar(category.Key, tableScalarPivotCategoryKey)
			if err != nil {
				return nil, fmt.Errorf("pivot category %q: %w", category.Output.Column, err)
			}
			pivot.Categories = append(pivot.Categories, recipe.GroupedPivotCategory{Key: key, Output: category.Output.Column, Label: category.Output.Label})
		}
		return &recipe.TableReshape{Kind: recipe.TableReshapeGroupedPivot, GroupedPivot: pivot}, nil
	case "UNPIVOT":
		if shape.Reshape.Unpivot == nil || shape.Reshape.Pivot != nil {
			return nil, fmt.Errorf("unpivot reshape requires exactly one unpivot payload")
		}
		authored := shape.Reshape.Unpivot
		unpivot := &recipe.Unpivot{
			ConstructionID: string(authored.ConstructionID),
			KeyOutput:      authored.KeyOutput.Column, KeyLabel: authored.KeyOutput.Label,
			ValueOutput: authored.ValueOutput.Column, ValueLabel: authored.ValueOutput.Label,
			NullRowPolicy: recipe.UnpivotNullRowPolicy(authored.NullRowPolicy),
			Inputs:        make([]recipe.UnpivotInput, 0, len(authored.Inputs)),
		}
		for _, input := range authored.Inputs {
			key, err := recipeTableScalar(input.Key, tableScalarUnpivotKey)
			if err != nil {
				return nil, fmt.Errorf("unpivot input %q: %w", input.Column, err)
			}
			unpivot.Inputs = append(unpivot.Inputs, recipe.UnpivotInput{Column: input.Column, Key: key})
		}
		return &recipe.TableReshape{Kind: recipe.TableReshapeUnpivot, Unpivot: unpivot}, nil
	default:
		return nil, fmt.Errorf("unsupported reshape kind %q", shape.Reshape.Kind)
	}
}

type tableScalarContext uint8

const (
	tableScalarPivotCategoryKey tableScalarContext = iota
	tableScalarUnpivotKey
)

func recipeTableScalar(value authoringv2.TableScalar, context tableScalarContext) (recipe.TableScalar, error) {
	var err error
	switch context {
	case tableScalarPivotCategoryKey:
		err = value.ValidatePivotCategoryKey()
	case tableScalarUnpivotKey:
		err = value.ValidateConcreteValue()
	default:
		return recipe.TableScalar{}, fmt.Errorf("unsupported table scalar context %d", context)
	}
	if err != nil {
		return recipe.TableScalar{}, err
	}
	return recipe.TableScalar{
		Kind: recipe.TableScalarKind(value.Kind), String: value.String,
		Integer: value.Integer, Decimal: value.Decimal, Boolean: value.Boolean,
	}, nil
}
