package dataframe

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/calypr/loom/fhirschema"
)

// SelectionSemanticSpec is the compiler-ready meaning of one field selection.
// It deliberately contains selectors and schema facts, not rendered AQL.
type SelectionSemanticSpec struct {
	Alias         string
	NodeAlias     string
	ResourceType  string
	FieldRef      string
	Selector      Selector
	Fallbacks     []Selector
	Cardinality   Cardinality
	Projection    ProjectionMode
	LegacyAuto    bool
	RepeatedPaths []string
}

// NormalizeSelectionPlan resolves every semantic field against the active
// generated FHIR metadata and returns stable alias-sorted selections.
func NormalizeSelectionPlan(plan SemanticPlan) ([]SelectionSemanticSpec, error) {
	out := make([]SelectionSemanticSpec, 0)
	nodeAliases := map[string]struct{}{}
	selectionAliases := map[string]struct{}{}
	var walk func(SemanticNode) error
	walk = func(node SemanticNode) error {
		if strings.TrimSpace(node.Alias) == "" {
			return fmt.Errorf("semantic node for %s has no alias", node.ResourceType)
		}
		if _, exists := nodeAliases[node.Alias]; exists {
			return fmt.Errorf("semantic node alias %q is duplicated", node.Alias)
		}
		nodeAliases[node.Alias] = struct{}{}
		for index, field := range node.Fields {
			spec, err := ResolveSemanticField(node.ResourceType, node.Alias, index, field)
			if err != nil {
				return err
			}
			if _, exists := selectionAliases[spec.Alias]; exists {
				return fmt.Errorf("selection alias %q is duplicated", spec.Alias)
			}
			selectionAliases[spec.Alias] = struct{}{}
			out = append(out, spec)
		}
		for _, child := range node.Children {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(plan.Root); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out, nil
}

// ResolveSemanticField resolves a single field and all fallback selectors.
func ResolveSemanticField(resourceType, nodeAlias string, index int, field SemanticField) (SelectionSemanticSpec, error) {
	if !fhirschema.HasResource(resourceType) {
		return SelectionSemanticSpec{}, fmt.Errorf("field %q: resource type %q is not in the active FHIR schema", field.Name, resourceType)
	}
	repeated, paths, err := selectorCardinality(resourceType, field.Selector)
	if err != nil {
		return SelectionSemanticSpec{}, fmt.Errorf("field %q: %w", field.Name, err)
	}
	for fallbackIndex, fallback := range field.Fallbacks {
		fallbackRepeated, fallbackPaths, err := selectorCardinality(resourceType, fallback)
		if err != nil {
			return SelectionSemanticSpec{}, fmt.Errorf("field %q fallback %d: %w", field.Name, fallbackIndex, err)
		}
		repeated = repeated || fallbackRepeated
		paths = append(paths, fallbackPaths...)
	}
	paths = sortedUniqueStrings(paths)
	cardinality := CardinalityOptionalOne
	if repeated {
		cardinality = CardinalityMany
	}
	projection, legacyAuto, err := projectionForValueMode(field.ValueMode, cardinality)
	if err != nil {
		return SelectionSemanticSpec{}, fmt.Errorf("field %q: %w", field.Name, err)
	}
	if err := ValidateProjection(cardinality, projection); err != nil {
		return SelectionSemanticSpec{}, fmt.Errorf("field %q: %w", field.Name, err)
	}
	name := strings.TrimSpace(field.Name)
	if name == "" {
		name = fmt.Sprintf("field_%d", index+1)
	}
	return SelectionSemanticSpec{
		Alias: nodeAlias + "." + name, NodeAlias: nodeAlias,
		ResourceType: resourceType, FieldRef: field.FieldRef,
		Selector: field.Selector, Fallbacks: append([]Selector(nil), field.Fallbacks...),
		Cardinality: cardinality, Projection: projection, LegacyAuto: legacyAuto,
		RepeatedPaths: paths,
	}, nil
}

func projectionForValueMode(valueMode string, cardinality Cardinality) (ProjectionMode, bool, error) {
	switch strings.ToUpper(strings.TrimSpace(valueMode)) {
	case "", "AUTO":
		if cardinality.AllowsMany() {
			// Legacy AUTO selected FIRST for an array-bearing selector. Preserve
			// that behavior as an explicit semantic decision, not compiler magic.
			return ProjectionFirst, true, nil
		}
		return ProjectionScalar, true, nil
	case "FIRST":
		return ProjectionFirst, false, nil
	case "ALL":
		return ProjectionArray, false, nil
	case "DISTINCT":
		return ProjectionDistinctArray, false, nil
	default:
		return "", false, fmt.Errorf("unsupported value mode %q", valueMode)
	}
}

func isKnownValueMode(valueMode string) bool {
	switch strings.ToUpper(strings.TrimSpace(valueMode)) {
	case "", "AUTO", "FIRST", "ALL", "DISTINCT":
		return true
	default:
		return false
	}
}

func selectorCardinality(resourceType string, selector Selector) (bool, []string, error) {
	if len(selector.Steps) == 0 {
		return false, nil, fmt.Errorf("selector is required")
	}
	metadataParts := make([]string, 0, len(selector.Steps))
	repeatedPaths := make([]string, 0)
	for _, step := range selector.Steps {
		part := step.Field
		probeParts := append(append([]string(nil), metadataParts...), part)
		probe := strings.Join(probeParts, ".")
		semantics, ok := fhirschema.ResolveFieldSemantics(resourceType, probe)
		if !ok {
			return false, nil, fmt.Errorf("selector path %q is not in the active FHIR schema", selector.CanonicalPath())
		}
		if semantics.Kind == fhirschema.FieldKindArray {
			if !step.Iterate && step.Index == nil {
				return false, nil, fmt.Errorf("selector path %q crosses repeated field %q without [] or an explicit index", selector.CanonicalPath(), strings.Join(probeParts, "."))
			}
			metadataParts = append(metadataParts, part+"[]")
			if step.Index == nil {
				repeatedPaths = append(repeatedPaths, strings.Join(metadataParts, "."))
			}
		} else {
			metadataParts = append(metadataParts, part)
		}
	}
	return len(repeatedPaths) > 0, repeatedPaths, nil
}

func selectorExecutionMode(resourceType string, selector Selector, fallbacks ...Selector) PhysicalSelectorExecutionMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOOM_PHYSICAL_RULE_TYPED_SELECTORS"))) {
	case "off", "0", "false", "disabled":
		return PhysicalSelectorGeneric
	}
	if len(fallbacks) != 0 {
		return PhysicalSelectorGeneric
	}
	if selector.Filter != nil {
		return PhysicalSelectorGeneric
	}
	metadata, ok := fhirschema.ResolveTerminalScalarMetadata(resourceType, selector.CanonicalPath())
	if !ok {
		return PhysicalSelectorGeneric
	}
	if selectorHasNoArrays(selector) && !metadata.Repeated {
		return PhysicalSelectorDirectScalar
	}
	if selectorHasIteratedArray(selector) && metadata.Repeated {
		return PhysicalSelectorConditionalArray
	}
	return PhysicalSelectorGeneric
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
