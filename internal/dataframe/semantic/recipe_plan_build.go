package semantic

// This file is the single semantic boundary for persisted recipes and the
// existing GraphQL dataframe request. It deliberately stops before physical
// lowering: no collection, AQL, SQL, or backend implementation detail belongs
// in these types.

import (
	"fmt"
	"strings"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func BuildRecipePlan(bundle recipe.Bundle, bindings recipe.RuntimeBindings) (RecipePlan, error) {
	if bundle.Fragments != nil {
		expanded, err := bundle.ExpandFragments()
		if err != nil {
			return RecipePlan{}, err
		}
		bundle = expanded
	}
	if err := bundle.Validate(); err != nil {
		return RecipePlan{}, err
	}
	digest, err := bundle.Digest()
	if err != nil {
		return RecipePlan{}, err
	}
	plan := RecipePlan{Version: 1, RecipeDigest: digest, TranslationVersion: bundle.TranslationVersion, Bindings: cloneBindings(bindings), Outputs: make([]OutputPlan, 0, len(bundle.Outputs))}
	for index, output := range bundle.Outputs {
		compiled, err := buildRecipeOutput(output, bindings)
		if err != nil {
			return RecipePlan{}, fmt.Errorf("outputs[%d] %s: %w", index, output.Name, err)
		}
		plan.Outputs = append(plan.Outputs, compiled)
	}
	return plan, nil
}

type scopeBinding struct {
	ResourceType string
	Prefix       string
	// ExpandedItem means this alias denotes one element of a repeated
	// collection. The prefix retains the array path for schema resolution,
	// but that collection's cardinality must not leak into expressions scoped
	// to the item.
	ExpandedItem bool
}

type scopeFrame struct {
	aliases map[string]scopeBinding
}

func newRootScope(resourceType string) scopeFrame {
	return scopeFrame{aliases: map[string]scopeBinding{"root": {ResourceType: resourceType}}}
}

func (s scopeFrame) child(alias string, binding scopeBinding) (scopeFrame, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return scopeFrame{}, fmt.Errorf("alias is required")
	}
	if _, exists := s.aliases[alias]; exists {
		return scopeFrame{}, fmt.Errorf("alias %q shadows an existing lexical binding", alias)
	}
	child := scopeFrame{aliases: make(map[string]scopeBinding, len(s.aliases)+1)}
	for name, value := range s.aliases {
		child.aliases[name] = value
	}
	child.aliases[alias] = binding
	return child, nil
}

func (s scopeFrame) expression(input recipe.Expression, path string) (SemanticExpression, error) {
	expr, err := expression.FromRecipeInContexts(input, keys(s.aliases))
	if err != nil {
		return SemanticExpression{}, fmt.Errorf("%s: %w", path, err)
	}
	checked, err := expr.Check(expression.TypeContext{Resolve: s.resolve})
	if err != nil {
		return SemanticExpression{}, fmt.Errorf("%s: %w", path, err)
	}
	context := ""
	if expr.Selector != nil {
		context = expr.Selector.Context
	} else if expr.Document != nil {
		context = strings.TrimSpace(expr.Document.Context)
		if context == "" {
			context = "root"
		}
		if _, ok := s.aliases[context]; !ok {
			return SemanticExpression{}, fmt.Errorf("%s: document context %q is not in scope", path, context)
		}
	}
	return SemanticExpression{Expression: checked.Expression, Type: checked.Type, SourcePath: path, Context: context}, nil
}

func (s scopeFrame) resolve(ref expression.SelectorRef) (expression.Type, error) {
	alias := ref.Context
	if alias == "" {
		alias = "root"
	}
	binding, ok := s.aliases[alias]
	if !ok {
		return expression.Type{}, fmt.Errorf("selector context %q is not in scope", alias)
	}
	path := strings.TrimPrefix(strings.TrimSpace(ref.Path), ".")
	if path == "" {
		return expression.Type{}, fmt.Errorf("selector path is empty")
	}
	fullPath := path
	if binding.Prefix != "" {
		fullPath = binding.Prefix + "." + path
	}
	selector, err := spec.ParseSelector(fullPath)
	if err != nil {
		return expression.Type{}, err
	}
	canonicalPath := selector.CanonicalPath()
	// An unqualified dotted selector may be either a nested root field or an
	// alias-qualified selector. If the generated schema cannot resolve it as a
	// root path, report the lexical alias error before the lower-level schema
	// diagnostic so recipe authors get an actionable scope failure.
	if ref.Context == "" {
		if parts := strings.SplitN(path, ".", 2); len(parts) == 2 {
			if _, visible := s.aliases[parts[0]]; !visible {
				if _, resolved := fhirschema.ResolveTerminalScalarMetadata(binding.ResourceType, canonicalPath); !resolved {
					return expression.Type{}, fmt.Errorf("selector context %q is not in scope", parts[0])
				}
			}
		}
	}
	repeated, _, err := spec.SelectorCardinality(binding.ResourceType, selector)
	if err != nil {
		if ref.Context == "" && strings.Contains(path, ".") {
			return expression.Type{}, fmt.Errorf("selector context or path %q is undefined: %w", path, err)
		}
		return expression.Type{}, err
	}
	metadata, ok := fhirschema.ResolveTerminalScalarMetadata(binding.ResourceType, fullPath)
	if !ok {
		return expression.Type{}, fmt.Errorf("selector path %q is not in the active FHIR schema", fullPath)
	}
	kind := primitiveKind(metadata.Primitive)
	if kind == expression.KindObject {
		// A repeated object is a valid expansion source even though it has no
		// terminal scalar metadata.
		repeated = repeated || metadata.Repeated
	}
	if binding.ExpandedItem {
		// The expansion itself is the row-shaping operation. Any [] markers in
		// the item-relative selector still represent a many-valued expression,
		// while the repeated prefix used to reach the item does not.
		repeated = strings.Contains(path, "[]")
	}
	cardinality := expression.OptionalOne
	if repeated {
		cardinality = expression.Many
	}
	return expression.Type{Kind: kind, Cardinality: cardinality}, nil
}

func primitiveKind(kind fhirschema.PrimitiveKind) expression.ValueKind {
	switch kind {
	case fhirschema.PrimitiveBoolean:
		return expression.KindBoolean
	case fhirschema.PrimitiveInteger:
		return expression.KindInteger
	case fhirschema.PrimitiveDecimal:
		return expression.KindDecimal
	case fhirschema.PrimitiveDate:
		return expression.KindDate
	case fhirschema.PrimitiveDateTime:
		return expression.KindDateTime
	case fhirschema.PrimitiveString:
		return expression.KindString
	default:
		return expression.KindObject
	}
}
