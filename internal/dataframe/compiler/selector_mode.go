package compiler

import (
	"os"
	"strings"

	"github.com/calypr/loom/fhirschema"
)

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

// selectorExecutionMode is a physical lowering choice. Keeping it in the
// compiler facade during migration prevents semantic planning from depending
// on physical IR types; it will move with the lowerer in the next extraction.
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
