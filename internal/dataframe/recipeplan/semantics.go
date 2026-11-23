package recipeplan

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/spec"
)

func (field SemanticField) PrimarySelector() (spec.Selector, error) {
	if field.Expr.Expression.Selector == nil {
		return spec.Selector{}, fmt.Errorf("field %q expression is not a selector", field.Name)
	}
	selector, err := spec.ParseSelector(field.Expr.Expression.Selector.Path)
	if err != nil {
		return spec.Selector{}, fmt.Errorf("field %q selector: %w", field.Name, err)
	}
	return selector, nil
}

func (field SemanticField) FallbackSelectors() ([]spec.Selector, error) {
	selectors := make([]spec.Selector, 0, len(field.Fallbacks))
	for index, fallback := range field.Fallbacks {
		if fallback.Expression.Selector == nil {
			return nil, fmt.Errorf("field %q fallback %d is not a selector", field.Name, index)
		}
		selector, err := spec.ParseSelector(fallback.Expression.Selector.Path)
		if err != nil {
			return nil, fmt.Errorf("field %q fallback %d selector: %w", field.Name, index, err)
		}
		selectors = append(selectors, selector)
	}
	return selectors, nil
}
