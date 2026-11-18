package compilation

import (
	"fmt"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

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
