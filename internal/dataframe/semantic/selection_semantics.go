package semantic

import (
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

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
	repeated, paths, err := spec.SelectorCardinality(resourceType, field.Selector)
	if err != nil {
		return SelectionSemanticSpec{}, fmt.Errorf("field %q: %w", field.Name, err)
	}
	for fallbackIndex, fallback := range field.Fallbacks {
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
	projection, legacyAuto, err := projectionForValueMode(field.ValueMode, cardinality)
	if err != nil {
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
		Selector: field.Selector, Fallbacks: append([]spec.Selector(nil), field.Fallbacks...),
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
