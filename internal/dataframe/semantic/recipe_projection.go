package semantic

// This file owns the recipe-to-semantic projection boundary.  Recipe fields
// are deliberately normalized here, before physical lowering, so selector
// fallbacks continue to use the same typed extraction contract as recipes
// fields.  In particular, this code must not choose a renderer or construct
// backend expressions.

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

// buildRecipeProjection checks one recipe field in the supplied lexical scope
// and converts it to the canonical semantic field consumed by generic
// lowering.
// The function is intentionally kept separate from BuildRecipePlan so the
// root and traversal builders can use exactly the same fallback/value-mode
// rules, including slice fields.
//
// Schema-version-one fallbacks are selector-only. Their checked expression
// forms remain attached to the field; selector metadata is derived only by
// the physical lowerer when it needs schema-specialized extraction.
func normalizeRecipeProjection(field recipe.Field, scope scopeFrame, path string) (SemanticField, error) {
	if strings.TrimSpace(field.Name) == "" {
		return SemanticField{}, fmt.Errorf("%s.name is required", path)
	}
	primary, err := scope.expression(field.Expr, path+".expr")
	if err != nil {
		return SemanticField{}, err
	}

	fallbacks := make([]SemanticExpression, 0, len(field.Fallbacks))
	for index, input := range field.Fallbacks {
		fallback, err := scope.expression(input, fmt.Sprintf("%s.fallbacks[%d]", path, index))
		if err != nil {
			return SemanticField{}, err
		}
		if fallback.Expression.Selector == nil {
			return SemanticField{}, fmt.Errorf("%s.fallbacks[%d] must be a selector expression", path, index)
		}
		fallbacks = append(fallbacks, fallback)
	}

	// A selector primary plus selector fallbacks is the optimized physical
	// representation. Keep only the checked expressions here; selectors are
	// derived from them by ResolveSemanticField at the physical boundary.
	if primary.Expression.Selector != nil {
		cardinality := spec.CardinalityOptionalOne
		if primary.Type.Cardinality == expression.Many {
			cardinality = spec.CardinalityMany
		}
		for _, fallback := range fallbacks {
			fallbackSelector, selectorErr := recipeSelector(fallback.Expression)
			if selectorErr != nil {
				return SemanticField{}, fmt.Errorf("%s fallback: %w", path, selectorErr)
			}
			repeated, _, cardinalityErr := spec.SelectorCardinality(resourceTypeForScope(scope, fallback.Expression), fallbackSelector)
			if cardinalityErr != nil {
				return SemanticField{}, fmt.Errorf("%s fallback: %w", path, cardinalityErr)
			}
			if repeated {
				cardinality = spec.CardinalityMany
			}
		}
		projection, _, err := projectionForValueMode(string(field.ValueMode), cardinality)
		if err != nil {
			return SemanticField{}, fmt.Errorf("%s: %w", path, err)
		}
		fieldSemantic := SemanticField{
			Name: field.Name, FieldRef: field.FieldRef, Expr: primary,
			Fallbacks: fallbacks, Projection: projection, Discovered: field.Discovered,
		}
		if err := validateRecipeProjectionTypes(primary.Type, fallbacks); err != nil {
			return SemanticField{}, fmt.Errorf("%s: %w", path, err)
		}
		selection, err := ResolveSemanticField(resourceTypeForScope(scope, primary.Expression), selectorContext(primary.Expression), 0, fieldSemantic)
		if err != nil {
			// ResolveSemanticField needs the resource type owning the selector. A
			// malformed/unknown context is already rejected by scope.expression;
			// retain its useful diagnostic here rather than silently weakening the
			// cardinality contract.
			return SemanticField{}, fmt.Errorf("%s: %w", path, err)
		}
		projected := primary
		switch selection.Projection {
		case spec.ProjectionFirst:
			// FIRST over a repeated selector can produce no value, but it can
			// never produce the source array. Carry that effective cardinality
			// into the publication schema while retaining the selector AST for
			// optimized PhysicalExtract lowering.
			projected.Type.Cardinality = expression.OptionalOne
		case spec.ProjectionArray, spec.ProjectionDistinctArray:
			projected.Type.Cardinality = expression.Many
		case spec.ProjectionScalar:
		default:
			return SemanticField{}, fmt.Errorf("%s: unsupported selector projection %q", path, selection.Projection)
		}
		fieldSemantic.Expr = projected
		fieldSemantic.Projection = selection.Projection
		return fieldSemantic, nil
	}

	if len(fallbacks) != 0 {
		return SemanticField{}, fmt.Errorf("%s.fallbacks require a selector primary expression", path)
	}
	projection, _, err := projectionForValueMode(string(field.ValueMode), semanticCardinality(primary.Type))
	if err != nil {
		return SemanticField{}, fmt.Errorf("%s: %w", path, err)
	}
	projected, err := applyRecipeValueMode(scope, primary, field.ValueMode)
	if err != nil {
		return SemanticField{}, fmt.Errorf("%s: %w", path, err)
	}
	return SemanticField{Name: field.Name, FieldRef: field.FieldRef, Expr: projected, Projection: projection, Discovered: field.Discovered}, nil
}

func semanticCardinality(input expression.Type) spec.Cardinality {
	if input.Cardinality == expression.Many {
		return spec.CardinalityMany
	}
	return spec.CardinalityOptionalOne
}

// recipeProjectionField returns the canonical selector-bearing SemanticField
// for a projection.  For non-selector expressions it returns a field whose
// Expr is the checked expression; physical expression lowering owns that
// case, while selector lowering remains schema-backed.
func recipeProjectionField(field recipe.Field, scope scopeFrame, path string) (SemanticField, error) {
	return normalizeRecipeProjection(field, scope, path)
}

// recipeSelector converts a checked expression selector to the schema selector
// used by the existing semantic and physical extraction contracts.
func recipeSelector(input expression.Expression) (spec.Selector, error) {
	if input.Selector == nil {
		return spec.Selector{}, fmt.Errorf("expression is not a selector")
	}
	selector, err := spec.ParseSelector(input.Selector.Path)
	if err != nil {
		return spec.Selector{}, err
	}
	return selector, nil
}

func selectorContext(input expression.Expression) string {
	if input.Selector == nil || strings.TrimSpace(input.Selector.Context) == "" {
		return "root"
	}
	return input.Selector.Context
}

func resourceTypeForScope(scope scopeFrame, input expression.Expression) string {
	alias := selectorContext(input)
	if binding, ok := scope.aliases[alias]; ok {
		return binding.ResourceType
	}
	return ""
}

func validateRecipeProjectionTypes(primary expression.Type, fallbacks []SemanticExpression) error {
	for index, fallback := range fallbacks {
		if fallback.Type.Kind == expression.KindNull || primary.Kind == expression.KindNull {
			continue
		}
		if primary.Kind != fallback.Type.Kind {
			return fmt.Errorf("fallback %d type %s is incompatible with primary type %s", index, fallback.Type, primary)
		}
	}
	return nil
}

// applyRecipeValueMode applies an explicit value mode to a non-selector
// expression using the typed expression AST. Selector expressions intentionally
// bypass this function so their optimized PhysicalExtract representation keeps
// fallback and selector specialization metadata.
func applyRecipeValueMode(scope scopeFrame, input SemanticExpression, mode recipe.ValueMode) (SemanticExpression, error) {
	normalized := mode.Normalized()
	if normalized == recipe.ValueModeAuto && input.Type.Cardinality != expression.Many {
		return input, nil
	}
	name := ""
	switch normalized {
	case recipe.ValueModeAuto, recipe.ValueModeFirst:
		name = "first"
	case recipe.ValueModeAll:
		name = "all"
	case recipe.ValueModeDistinct:
		name = "distinct"
	default:
		return SemanticExpression{}, fmt.Errorf("unsupported value mode %q", mode)
	}
	wrapper := expression.Function(name, input.Expression)
	checked, err := wrapper.Check(expression.TypeContext{Resolve: scope.resolve})
	if err != nil {
		return SemanticExpression{}, err
	}
	return SemanticExpression{Expression: checked.Expression, Type: checked.Type, SourcePath: input.SourcePath, Context: input.Context}, nil
}
