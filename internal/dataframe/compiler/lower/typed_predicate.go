package lower

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

// lowerTypedPredicate is the one semantic-to-physical lowering boundary for a
// typed filter. It owns selector extraction and literal binding while callers
// supply a scope-specific prefix, ensuring sibling rich consumers cannot
// overwrite one another's bind variables.
func lowerTypedPredicate(physical *ir.PhysicalPlan, resourceType string, source ir.PhysicalValue, filter spec.TypedFilter, bindPrefix string) (*ir.PhysicalPredicateExpression, error) {
	if physical == nil {
		return nil, fmt.Errorf("physical plan is required")
	}
	if err := spec.ValidateTypedFilterForResource(resourceType, filter); err != nil {
		return nil, err
	}
	selector, err := spec.ParseSelector(filter.Selector)
	if err != nil {
		return nil, fmt.Errorf("filter selector %q: %w", filter.Selector, err)
	}
	leftSource := source
	if source.Variable != "" && len(source.Path) == 0 {
		leftSource = ir.PhysicalValue{Variable: source.Variable, Path: []string{"payload"}}
	}
	left := ir.PhysicalExpression{
		Kind:         ir.PhysicalExtractExpression,
		Cardinality:  ir.PhysicalArrayCardinality,
		NullBehavior: ir.PhysicalEmptyOnNull,
		Extract: &ir.PhysicalExtract{
			Source: leftSource, ResourceType: resourceType, Selector: selector,
			ExecutionMode: selectorExecutionMode(resourceType, selector),
		},
	}
	comparison := ir.PhysicalPredicate{
		Operator:       string(filter.Operator),
		Quantifier:     filter.Quantifier,
		ValueKind:      filter.FieldKind,
		LeftExpression: &left,
	}
	if filter.Operator != spec.FilterExists && filter.Operator != spec.FilterMissing {
		if len(filter.Values) == 0 {
			return nil, fmt.Errorf("filter %q has no value", filter.FieldRef)
		}
		bindPrefix = strings.TrimSpace(bindPrefix)
		if bindPrefix == "" {
			return nil, fmt.Errorf("filter %q requires a non-empty bind prefix", filter.FieldRef)
		}
		bindKey := bindPrefix + "_value"
		if filter.Operator == spec.FilterIn {
			values := make([]any, 0, len(filter.Values))
			for _, value := range filter.Values {
				literal, literalErr := filterLiteral(value)
				if literalErr != nil {
					return nil, literalErr
				}
				values = append(values, literal)
			}
			physical.BindVars[bindKey] = values
		} else {
			literal, literalErr := filterLiteral(filter.Values[0])
			if literalErr != nil {
				return nil, literalErr
			}
			physical.BindVars[bindKey] = literal
		}
		comparison.Right = &ir.PhysicalValue{BindKey: bindKey}
	}
	return &ir.PhysicalPredicateExpression{Kind: ir.PhysicalComparisonPredicate, Comparison: &comparison}, nil
}
