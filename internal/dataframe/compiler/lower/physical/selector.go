package physical

import (
	"os"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

type selectorModeContext struct {
	ResourceType string
	Selector     spec.Selector
	Fallbacks    []spec.Selector
	Metadata     fhirschema.TerminalScalarMetadata
	MetadataOK   bool
}

func SanitizeColumnName(in string) string {
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

func SelectorExecutionMode(resourceType string, selector spec.Selector, fallbacks ...spec.Selector) ir.PhysicalSelectorExecutionMode {
	return selectorExecutionModeWithContext(selectorModeContext{
		ResourceType: resourceType,
		Selector:     selector,
		Fallbacks:    fallbacks,
	})
}

func SelectorExecutionModeForExpression(resourceType string, selector spec.Selector, fallbacks []spec.Selector) ir.PhysicalSelectorExecutionMode {
	return selectorExecutionModeWithContext(selectorModeContext{
		ResourceType: resourceType,
		Selector:     selector,
		Fallbacks:    fallbacks,
	})
}

func selectorExecutionModeWithContext(input selectorModeContext) ir.PhysicalSelectorExecutionMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOOM_PHYSICAL_RULE_TYPED_SELECTORS"))) {
	case "off", "0", "false", "disabled":
		return ir.PhysicalSelectorGeneric
	}
	if len(input.Fallbacks) != 0 || input.Selector.Filter != nil {
		return ir.PhysicalSelectorGeneric
	}
	metadata, ok := input.Metadata, input.MetadataOK
	if !ok {
		metadata, ok = fhirschema.ResolveTerminalScalarMetadata(input.ResourceType, input.Selector.CanonicalPath())
	}
	if !ok {
		return ir.PhysicalSelectorGeneric
	}
	if input.Selector.HasNoArrays() && !metadata.Repeated {
		return ir.PhysicalSelectorDirectScalar
	}
	if input.Selector.HasIteratedArray() && metadata.Repeated {
		return ir.PhysicalSelectorConditionalArray
	}
	return ir.PhysicalSelectorGeneric
}
