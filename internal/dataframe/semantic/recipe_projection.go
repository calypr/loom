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
// and converts it to the semantic projection consumed by generic lowering.
// The function is intentionally kept separate from BuildRecipePlan so the
// root and traversal builders can use exactly the same fallback/value-mode
// rules, including slice fields.
//
// Schema-version-one fallbacks are selector-only.  Selector-only fields retain
// SemanticField.Selector/Fallbacks, which lets the physical lowerer preserve
// typed selector specialization and prepared fallback reuse.  A non-selector
// field is still checked through the generic expression AST; its explicit
// value mode is represented by an AST call rather than renderer branching.

type normalizedRecipeProjection struct {
	projection SemanticProjection
	field      SemanticField
}

func normalizeRecipeProjection(field recipe.Field, scope scopeFrame, path string) (normalizedRecipeProjection, error) {
	if strings.TrimSpace(field.Name) == "" {
		return normalizedRecipeProjection{}, fmt.Errorf("%s.name is required", path)
	}
	primary, err := scope.expression(field.Expr, path+".expr")
	if err != nil {
		return normalizedRecipeProjection{}, err
	}

	fallbacks := make([]SemanticExpression, 0, len(field.Fallbacks))
	for index, input := range field.Fallbacks {
		fallback, err := scope.expression(input, fmt.Sprintf("%s.fallbacks[%d]", path, index))
		if err != nil {
			return normalizedRecipeProjection{}, err
		}
		if fallback.Expression.Selector == nil {
			return normalizedRecipeProjection{}, fmt.Errorf("%s.fallbacks[%d] must be a selector expression", path, index)
		}
		fallbacks = append(fallbacks, fallback)
	}

	// A selector primary plus selector fallbacks is the optimized physical
	// representation.  Keep the checked expression as the projection source,
	// while copying selectors into SemanticField below for generic lowering.
	if primary.Expression.Selector != nil {
		selector, err := recipeSelector(primary.Expression)
		if err != nil {
			return normalizedRecipeProjection{}, fmt.Errorf("%s: %w", path, err)
		}
		fallbackSelectors := make([]Selector, 0, len(fallbacks))
		for index, fallback := range fallbacks {
			parsed, err := recipeSelector(fallback.Expression)
			if err != nil {
				return normalizedRecipeProjection{}, fmt.Errorf("%s.fallbacks[%d]: %w", path, index, err)
			}
			fallbackSelectors = append(fallbackSelectors, parsed)
		}
		fieldSemantic := SemanticField{
			Name: field.Name, Selector: selector,
			Fallbacks: fallbackSelectors, ValueMode: string(field.ValueMode),
			Expr: &primary.Expression, ExprType: primary.Type, SourcePath: primary.SourcePath,
		}
		if err := validateRecipeProjectionTypes(primary.Type, fallbacks); err != nil {
			return normalizedRecipeProjection{}, fmt.Errorf("%s: %w", path, err)
		}
		if _, err := ResolveSemanticField(resourceTypeForScope(scope, primary.Expression), selectorContext(primary.Expression), 0, fieldSemantic); err != nil {
			// ResolveSemanticField needs the resource type owning the selector. A
			// malformed/unknown context is already rejected by scope.expression;
			// retain its useful diagnostic here rather than silently weakening the
			// cardinality contract.
			return normalizedRecipeProjection{}, fmt.Errorf("%s: %w", path, err)
		}
		return normalizedRecipeProjection{projection: SemanticProjection{
			Name: field.Name, ValueMode: string(field.ValueMode), Expr: primary,
		}, field: fieldSemantic}, nil
	}

	if len(fallbacks) != 0 {
		return normalizedRecipeProjection{}, fmt.Errorf("%s.fallbacks require a selector primary expression", path)
	}
	projected, err := applyRecipeValueMode(scope, primary, field.ValueMode)
	if err != nil {
		return normalizedRecipeProjection{}, fmt.Errorf("%s: %w", path, err)
	}
	return normalizedRecipeProjection{projection: SemanticProjection{Name: field.Name, ValueMode: string(field.ValueMode), Expr: projected}, field: SemanticField{
		Name: field.Name, ValueMode: string(field.ValueMode), Expr: &projected.Expression, ExprType: projected.Type, SourcePath: projected.SourcePath,
	}}, nil
}

// recipeProjectionField returns the canonical selector-bearing SemanticField
// for a projection.  For non-selector expressions it returns a field whose
// Expr is the checked expression; physical expression lowering owns that
// case, while selector lowering remains schema-backed.
func recipeProjectionField(field recipe.Field, scope scopeFrame, path string) (SemanticField, error) {
	result, err := normalizeRecipeProjection(field, scope, path)
	if err != nil {
		return SemanticField{}, err
	}
	return result.field, nil
}

// recipeSelector converts a checked expression selector to the schema selector
// used by the existing semantic and physical extraction contracts.
func recipeSelector(input expression.Expression) (Selector, error) {
	if input.Selector == nil {
		return Selector{}, fmt.Errorf("expression is not a selector")
	}
	selector, err := spec.ParseSelector(input.Selector.Path)
	if err != nil {
		return Selector{}, err
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
