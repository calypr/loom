package semantic

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

// qualifyTraversalLocals makes the traversal node's local selectors explicit
// before expression checking. Recipe selectors are intentionally concise
// ("status", "type[].coding[].display") while the semantic AST needs a
// lexical alias to distinguish them from parent/root references. Existing
// aliases remain untouched so a recipe can deliberately read a visible parent
// binding.
func qualifyTraversalLocals(input recipe.Traversal, alias string, visible map[string]scopeBinding) recipe.Traversal {
	copy := input
	copy.Fields = append([]recipe.Field(nil), input.Fields...)
	for index := range copy.Fields {
		copy.Fields[index].Expr = qualifyRecipeExpression(copy.Fields[index].Expr, alias, visible)
		copy.Fields[index].Fallbacks = append([]recipe.Expression(nil), input.Fields[index].Fallbacks...)
		for fallbackIndex := range copy.Fields[index].Fallbacks {
			copy.Fields[index].Fallbacks[fallbackIndex] = qualifyRecipeExpression(copy.Fields[index].Fallbacks[fallbackIndex], alias, visible)
		}
	}
	copy.Filters = append([]recipe.Filter(nil), input.Filters...)
	for index := range copy.Filters {
		copy.Filters[index].Select = qualifyRecipeSelector(copy.Filters[index].Select, alias, visible)
	}
	copy.Pivots = append([]recipe.Pivot(nil), input.Pivots...)
	for index := range copy.Pivots {
		copy.Pivots[index].ColumnExpr = qualifyRecipeExpression(copy.Pivots[index].ColumnExpr, alias, visible)
		copy.Pivots[index].ValueExpr = qualifyRecipeExpression(copy.Pivots[index].ValueExpr, alias, visible)
	}
	copy.Aggregates = append([]recipe.Aggregate(nil), input.Aggregates...)
	for index := range copy.Aggregates {
		if input.Aggregates[index].Expr != nil {
			expr := qualifyRecipeExpression(*input.Aggregates[index].Expr, alias, visible)
			copy.Aggregates[index].Expr = &expr
		}
		if input.Aggregates[index].Where != nil {
			where := *input.Aggregates[index].Where
			where.Select = qualifyRecipeSelector(where.Select, alias, visible)
			copy.Aggregates[index].Where = &where
		}
	}
	copy.Slices = append([]recipe.RepresentativeSlice(nil), input.Slices...)
	for index := range copy.Slices {
		if input.Slices[index].Where != nil {
			where := *input.Slices[index].Where
			where.Select = qualifyRecipeSelector(where.Select, alias, visible)
			copy.Slices[index].Where = &where
		}
		copy.Slices[index].Fields = append([]recipe.Field(nil), input.Slices[index].Fields...)
		for fieldIndex := range copy.Slices[index].Fields {
			copy.Slices[index].Fields[fieldIndex].Expr = qualifyRecipeExpression(copy.Slices[index].Fields[fieldIndex].Expr, alias, visible)
			copy.Slices[index].Fields[fieldIndex].Fallbacks = append([]recipe.Expression(nil), input.Slices[index].Fields[fieldIndex].Fallbacks...)
			for fallbackIndex := range copy.Slices[index].Fields[fieldIndex].Fallbacks {
				copy.Slices[index].Fields[fieldIndex].Fallbacks[fallbackIndex] = qualifyRecipeExpression(copy.Slices[index].Fields[fieldIndex].Fallbacks[fallbackIndex], alias, visible)
			}
		}
	}
	copy.DynamicColumns = append([]recipe.DynamicColumn(nil), input.DynamicColumns...)
	for index := range copy.DynamicColumns {
		copy.DynamicColumns[index].Source = qualifyRecipeExpression(copy.DynamicColumns[index].Source, alias, visible)
		if copy.DynamicColumns[index].Key != nil {
			key := qualifyRecipeExpression(*copy.DynamicColumns[index].Key, alias, visible)
			copy.DynamicColumns[index].Key = &key
		}
		if copy.DynamicColumns[index].Value != nil {
			value := qualifyRecipeExpression(*copy.DynamicColumns[index].Value, alias, visible)
			copy.DynamicColumns[index].Value = &value
		}
	}
	copy.ExtensionColumns = append([]recipe.ExtensionColumn(nil), input.ExtensionColumns...)
	for index := range copy.ExtensionColumns {
		copy.ExtensionColumns[index].Source = qualifyRecipeExpression(copy.ExtensionColumns[index].Source, alias, visible)
		copy.ExtensionColumns[index].Columns = append([]recipe.ExtensionColumnMapping(nil), input.ExtensionColumns[index].Columns...)
	}
	return copy
}

func qualifyRecipeExpression(input recipe.Expression, alias string, visible map[string]scopeBinding) recipe.Expression {
	copy := input
	if strings.TrimSpace(copy.Select) != "" {
		copy.Select = qualifyRecipeSelector(copy.Select, alias, visible)
	}
	copy.Args = append([]recipe.Expression(nil), input.Args...)
	for index := range copy.Args {
		copy.Args[index] = qualifyRecipeExpression(copy.Args[index], alias, visible)
	}
	return copy
}

func qualifyRecipeSelector(input, alias string, visible map[string]scopeBinding) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return trimmed
	}
	path := trimmed
	suffix := ""
	if before, after, found := strings.Cut(trimmed, " where "); found {
		path, suffix = before, " where "+after
	}
	first := strings.SplitN(strings.TrimPrefix(strings.TrimSpace(path), "."), ".", 2)[0]
	if first == "root" || first == "item" || first == alias {
		return trimmed
	}
	if _, ok := visible[first]; ok {
		return trimmed
	}
	return alias + "." + strings.TrimPrefix(strings.TrimSpace(path), ".") + suffix
}

// NormalizeRecipeMatchMode applies the persisted recipe default and returns
// the canonical semantic relationship mode. Empty and case-insensitive values
// remain OPTIONAL for version-1 recipe compatibility; unknown values fail
// before traversal lowering.
func NormalizeRecipeMatchMode(mode recipe.TraversalMatchMode) (spec.TraversalMatchMode, error) {
	if !mode.Valid() {
		return "", fmt.Errorf("unsupported traversal match mode %q", mode)
	}
	switch mode.Normalized() {
	case recipe.MatchRequired:
		return spec.TraversalMatchRequired, nil
	case recipe.MatchOptional:
		return spec.TraversalMatchOptional, nil
	default:
		return "", fmt.Errorf("unsupported traversal match mode %q", mode)
	}
}

// LowerRecipeTraversalFilters performs the closed semantic portion of one
// traversal. The caller attaches the returned filters to the child
// SemanticNode, preserving the optional/required boundary for the common
// physical lowering path.
