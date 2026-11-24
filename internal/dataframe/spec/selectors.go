package spec

import (
	"fmt"
	"strings"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

type Selector = fhirschema.Selector
type SelectorStep = fhirschema.SelectorStep
type ContainsFilter = fhirschema.ContainsFilter

func ParseSelector(input string) (Selector, error) {
	return fhirschema.ParseSelector(input)
}

func ParseDirectScalarSelector(input string) ([]string, error) {
	if strings.TrimSpace(input) == "" || strings.TrimSpace(input) != input {
		return nil, fmt.Errorf("selector must be exact and non-empty")
	}
	selector, err := ParseSelector(input)
	if err != nil || selector.Filter != nil || len(selector.Steps) == 0 {
		return nil, fmt.Errorf("selector is not a direct scalar property path")
	}
	segments := make([]string, 0, len(selector.Steps))
	for index, step := range selector.Steps {
		if step.Iterate || step.Index != nil || !ValidFieldPathSegment(step.Field) {
			return nil, fmt.Errorf("selector step %d is not a direct scalar property", index)
		}
		segments = append(segments, step.Field)
	}
	return segments, nil
}

func ValidFieldPathSegment(segment string) bool {
	if segment == "" || (segment[0] < 'a' || segment[0] > 'z') && (segment[0] < 'A' || segment[0] > 'Z') {
		return false
	}
	for index := 1; index < len(segment); index++ {
		character := segment[index]
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
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

func SelectorCardinality(resourceType string, selector Selector) (bool, []string, error) {
	return selectorCardinality(resourceType, selector)
}

func PathHasPrefix(path, prefix []string) bool {
	if len(path) < len(prefix) {
		return false
	}
	for index, segment := range prefix {
		if path[index] != segment {
			return false
		}
	}
	return true
}
