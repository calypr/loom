package dataframe

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/fhirschema"
)

type Selector = fhirschema.Selector
type SelectorStep = fhirschema.SelectorStep
type ContainsFilter = fhirschema.ContainsFilter

func ParseSelector(input string) (Selector, error) {
	return fhirschema.ParseSelector(input)
}

func selectorStepText(step SelectorStep) string {
	switch {
	case step.Iterate:
		return step.Field + "[]"
	case step.Index != nil:
		return fmt.Sprintf("%s[%d]", step.Field, *step.Index)
	default:
		return step.Field
	}
}

func sanitizeColumnName(in string) string {
	var b strings.Builder
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

func isSafeAQLFieldIdentifier(in string) bool {
	if in == "" {
		return false
	}
	for index, r := range in {
		if index == 0 {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && r != '_' {
				return false
			}
			continue
		}
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func quoteKey(key string) string {
	data, _ := json.Marshal(key)
	return string(data)
}
