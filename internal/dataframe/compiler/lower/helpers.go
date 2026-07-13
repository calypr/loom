package lower

import (
	"os"
	"strings"

	"github.com/calypr/loom/fhirschema"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const (
	datasetGenerationBindKey = "dataset_generation"
	datasetGenerationField   = "dataset_generation"
)

func datasetGenerationBindValue(generation string) any {
	return ir.DatasetGenerationBindValue(generation)
}

func sanitizeColumnName(in string) string {
	var b strings.Builder
	for _, r := range in {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

func selectorHasNoArrays(sel Selector) bool {
	for _, step := range sel.Steps {
		if step.Iterate || step.Index != nil {
			return false
		}
	}
	return true
}

func selectorHasIteratedArray(sel Selector) bool {
	for _, step := range sel.Steps {
		if step.Iterate {
			return true
		}
	}
	return false
}

func selectorExecutionMode(resourceType string, selector Selector, fallbacks ...Selector) PhysicalSelectorExecutionMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOOM_PHYSICAL_RULE_TYPED_SELECTORS"))) {
	case "off", "0", "false", "disabled":
		return PhysicalSelectorGeneric
	}
	if len(fallbacks) != 0 || selector.Filter != nil {
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
