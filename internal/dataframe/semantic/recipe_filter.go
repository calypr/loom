package semantic

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/fhirschema"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

// LowerRecipeFilters converts the closed recipe filter wire type into the
// canonical typed-filter representation consumed by the semantic and physical
// compilers. Selectors are relative to resourceType unless they are prefixed
// with root.
func LowerRecipeFilters(resourceType string, filters []recipe.Filter) ([]TypedFilter, error) {
	return lowerRecipeFiltersForAlias(resourceType, "root", filters)
}

// LowerRecipeFiltersForAlias is the traversal-local variant of
// LowerRecipeFilters. The alias prefix is accepted for GraphQL/catalog
// round-trips, but is removed before schema resolution; the canonical typed
// filter always carries a resource-relative selector.
func LowerRecipeFiltersForAlias(resourceType, alias string, filters []recipe.Filter) ([]TypedFilter, error) {
	return lowerRecipeFiltersForAlias(resourceType, alias, filters)
}

func lowerRecipeFiltersForAlias(resourceType, alias string, filters []recipe.Filter) ([]TypedFilter, error) {
	if !fhirschema.HasResource(resourceType) {
		return nil, fmt.Errorf("filter resource type %q is not represented by the active generated FHIR schema", resourceType)
	}
	out := make([]TypedFilter, 0, len(filters))
	for index, input := range filters {
		filter, err := lowerRecipeFilterForAlias(resourceType, alias, input)
		if err != nil {
			return nil, fmt.Errorf("filters[%d]: %w", index, err)
		}
		out = append(out, filter)
	}
	return out, nil
}

func lowerRecipeFilterForAlias(resourceType, alias string, input recipe.Filter) (TypedFilter, error) {
	selectorText, err := normalizeRecipeFilterSelector(input.Select, alias)
	if err != nil {
		return TypedFilter{}, err
	}
	selector, err := spec.ParseSelector(selectorText)
	if err != nil {
		return TypedFilter{}, fmt.Errorf("filter selector %q: %w", input.Select, err)
	}
	canonical := selector.CanonicalPath()
	metadata, ok := fhirschema.ResolveTerminalScalarMetadata(resourceType, canonical)
	if !ok {
		return TypedFilter{}, fmt.Errorf("filter selector %q is not represented by generated resource type %q", canonical, resourceType)
	}
	if metadata.Primitive == fhirschema.PrimitiveUnknown {
		return TypedFilter{}, fmt.Errorf("filter selector %q does not resolve to a supported primitive", canonical)
	}
	fieldKind, err := recipeFilterKind(metadata.Primitive, canonical)
	if err != nil {
		return TypedFilter{}, err
	}
	repeated, _, err := spec.SelectorCardinality(resourceType, selector)
	if err != nil {
		return TypedFilter{}, fmt.Errorf("filter selector %q: %w", canonical, err)
	}
	// Terminal metadata includes repetition inherited from a repeated parent;
	// the cardinality helper also proves that every repeated path was explicitly
	// iterated. Keep the two facts in agreement rather than trusting the wire.
	if repeated != metadata.Repeated {
		return TypedFilter{}, fmt.Errorf("filter selector %q has inconsistent generated cardinality", canonical)
	}
	fieldRef := strings.TrimSpace(input.FieldRef)
	if fieldRef == "" {
		fieldRef = resourceType + "." + canonical
	}
	out := TypedFilter{
		FieldRef:   fieldRef,
		Selector:   canonical,
		FieldKind:  fieldKind,
		Repeated:   metadata.Repeated,
		Operator:   spec.FilterOperator(input.Operator),
		Quantifier: spec.ArrayQuantifier(input.Quantifier),
	}
	out.Values, err = recipeFilterValues(input.Values)
	if err != nil {
		return TypedFilter{}, err
	}
	if err := ValidateTypedFilterForResource(resourceType, out); err != nil {
		return TypedFilter{}, fmt.Errorf("filter %q: %w", fieldRef, err)
	}
	return out, nil
}

func normalizeRecipeFilterSelector(raw, alias string) (string, error) {
	selector, err := spec.ParseSelector(raw)
	if err != nil {
		return "", err
	}
	alias = strings.TrimSpace(alias)
	if alias == "" {
		alias = "root"
	}
	if len(selector.Steps) > 0 && !selector.Steps[0].Iterate && selector.Steps[0].Index == nil && selector.Steps[0].Field == alias {
		selector.Steps = selector.Steps[1:]
	}
	if len(selector.Steps) == 0 {
		return "", fmt.Errorf("filter selector %q has no resource-relative path", raw)
	}
	return selector.CanonicalPath(), nil
}

func recipeFilterKind(primitive fhirschema.PrimitiveKind, canonical string) (spec.FilterValueKind, error) {
	switch primitive {
	case fhirschema.PrimitiveBoolean:
		return spec.FilterBoolean, nil
	case fhirschema.PrimitiveInteger:
		return spec.FilterInteger, nil
	case fhirschema.PrimitiveDecimal:
		return spec.FilterDecimal, nil
	case fhirschema.PrimitiveDate:
		return spec.FilterDate, nil
	case fhirschema.PrimitiveDateTime:
		return spec.FilterDateTime, nil
	case fhirschema.PrimitiveString:
		// Generated metadata does not carry terminology identity separately;
		// code is safely recognized only at a terminal `.code` path. The shared
		// validator rejects system/display values until paired Coding lowering
		// exists in canonical physical IR.
		if canonical == "code" || strings.HasSuffix(canonical, ".code") {
			return spec.FilterCode, nil
		}
		return spec.FilterString, nil
	default:
		return "", fmt.Errorf("filter selector %q has unsupported primitive %q", canonical, primitive)
	}
}

func recipeFilterValues(values []recipe.FilterValue) ([]spec.FilterValue, error) {
	out := make([]spec.FilterValue, 0, len(values))
	for index, input := range values {
		value := spec.FilterValue{
			Kind:     spec.FilterValueKind(input.Kind),
			String:   input.String,
			Boolean:  input.Boolean,
			Integer:  input.Integer,
			Decimal:  input.Decimal,
			Date:     input.Date,
			DateTime: input.DateTime,
		}
		if input.Code != nil {
			value.Code = &spec.CodeValue{System: input.Code.System, Code: input.Code.Code, Display: input.Code.Display}
		}
		if err := value.Validate(); err != nil {
			return nil, fmt.Errorf("filter value %d: %w", index, err)
		}
		out = append(out, value)
	}
	return out, nil
}
