package spec

import (
	"fmt"
	"strings"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

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

func SelectorCardinality(resourceType string, selector Selector) (bool, []string, error) {
	return selectorCardinality(resourceType, selector)
}
