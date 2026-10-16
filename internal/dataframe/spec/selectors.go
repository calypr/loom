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

// ParseDirectScalarSelector accepts only a non-repeating property path. It
// rejects array traversal, indexing, and filters so field choices can be
// lowered as one exact payload lookup with no implicit collection behavior.
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
		if step.Iterate || step.Index != nil || !directScalarPathSegment(step.Field) {
			return nil, fmt.Errorf("selector step %d is not a direct scalar property", index)
		}
		segments = append(segments, step.Field)
	}
	return segments, nil
}

func directScalarPathSegment(segment string) bool {
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
