package semantic

import (
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

// PrimarySelector derives the schema selector used by the physical compiler
// from the checked primary expression. Semantic plans keep expressions as
// their single source of truth so diagnostics, type information, and
// lowering cannot observe different selector values.
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

// FallbackSelectors derives the ordered selector fallback chain from checked
// semantic expressions. Fallbacks are selector-only in the current recipe
// contract; rejecting anything else here keeps that constraint at the
// semantic/physical boundary instead of relying on a parallel selector list.
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

// SelectionSemanticSpec is the compiler-ready meaning of one field selection.
// It deliberately contains selectors and schema facts, not rendered AQL.
type SelectionSemanticSpec struct {
	Alias         string
	NodeAlias     string
	ResourceType  string
	FieldRef      string
	Selector      spec.Selector
	Fallbacks     []spec.Selector
	Cardinality   spec.Cardinality
	Projection    spec.ProjectionMode
	LegacyAuto    bool
	RepeatedPaths []string
}

// ResolveSemanticField resolves a single field and all fallback selectors.
func ResolveSemanticField(resourceType, nodeAlias string, index int, field SemanticField) (SelectionSemanticSpec, error) {
	if !fhirschema.HasResource(resourceType) {
		return SelectionSemanticSpec{}, fmt.Errorf("field %q: resource type %q is not in the active FHIR schema", field.Name, resourceType)
	}
	selector, err := field.PrimarySelector()
	if err != nil {
		return SelectionSemanticSpec{}, err
	}
	fallbacks, err := field.FallbackSelectors()
	if err != nil {
		return SelectionSemanticSpec{}, err
	}
	repeated, paths, err := spec.SelectorCardinality(resourceType, selector)
	if err != nil {
		return SelectionSemanticSpec{}, fmt.Errorf("field %q: %w", field.Name, err)
	}
	for fallbackIndex, fallback := range fallbacks {
		fallbackRepeated, fallbackPaths, err := spec.SelectorCardinality(resourceType, fallback)
		if err != nil {
			return SelectionSemanticSpec{}, fmt.Errorf("field %q fallback %d: %w", field.Name, fallbackIndex, err)
		}
		repeated = repeated || fallbackRepeated
		paths = append(paths, fallbackPaths...)
	}
	paths = sortedUniqueStrings(paths)
	cardinality := spec.CardinalityOptionalOne
	if repeated {
		cardinality = spec.CardinalityMany
	}
	projection := field.Projection
	legacyAuto := false
	if projection == "" {
		// Direct semantic graph callers may omit Projection. Preserve the
		// historical AUTO behavior for that transport-neutral input while
		// recipe plans record the resolved projection explicitly.
		projection, legacyAuto, err = projectionForValueMode("", cardinality)
		if err != nil {
			return SelectionSemanticSpec{}, fmt.Errorf("field %q: %w", field.Name, err)
		}
	} else if err := projection.Validate(); err != nil {
		return SelectionSemanticSpec{}, fmt.Errorf("field %q: %w", field.Name, err)
	}
	if err := spec.ValidateProjection(cardinality, projection); err != nil {
		return SelectionSemanticSpec{}, fmt.Errorf("field %q: %w", field.Name, err)
	}
	name := strings.TrimSpace(field.Name)
	if name == "" {
		name = fmt.Sprintf("field_%d", index+1)
	}
	return SelectionSemanticSpec{
		Alias: nodeAlias + "." + name, NodeAlias: nodeAlias,
		ResourceType: resourceType, FieldRef: field.FieldRef,
		Selector: selector, Fallbacks: fallbacks,
		Cardinality: cardinality, Projection: projection, LegacyAuto: legacyAuto,
		RepeatedPaths: paths,
	}, nil
}

func projectionForValueMode(valueMode string, cardinality spec.Cardinality) (spec.ProjectionMode, bool, error) {
	switch strings.ToUpper(strings.TrimSpace(valueMode)) {
	case "", "AUTO":
		if cardinality.AllowsMany() {
			// Legacy AUTO selected FIRST for an array-bearing selector. Preserve
			// that behavior as an explicit semantic decision, not compiler magic.
			return spec.ProjectionFirst, true, nil
		}
		return spec.ProjectionScalar, true, nil
	case "FIRST":
		return spec.ProjectionFirst, false, nil
	case "ALL":
		return spec.ProjectionArray, false, nil
	case "DISTINCT":
		return spec.ProjectionDistinctArray, false, nil
	default:
		return "", false, fmt.Errorf("unsupported value mode %q", valueMode)
	}
}

func sortedUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
