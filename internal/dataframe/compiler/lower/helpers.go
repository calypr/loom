package lower

import (
	"os"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
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

func selectorHasNoArrays(sel spec.Selector) bool {
	for _, step := range sel.Steps {
		if step.Iterate || step.Index != nil {
			return false
		}
	}
	return true
}

func selectorHasIteratedArray(sel spec.Selector) bool {
	for _, step := range sel.Steps {
		if step.Iterate {
			return true
		}
	}
	return false
}

type selectorModeContext struct {
	ResourceType string
	Selector     spec.Selector
	Fallbacks    []spec.Selector
	Metadata     fhirschema.TerminalScalarMetadata
	MetadataOK   bool
}

func selectorExecutionMode(resourceType string, selector spec.Selector, fallbacks ...spec.Selector) ir.PhysicalSelectorExecutionMode {
	return selectorExecutionModeWithContext(selectorModeContext{
		ResourceType: resourceType,
		Selector:     selector,
		Fallbacks:    fallbacks,
	})
}

// selectorExecutionModeForExpression is the shared selector classifier used
// by expression frontends. It resolves schema metadata once and passes the
// complete physical expression context through the same proof used by
// generic field lowering.
func selectorExecutionModeForExpression(resourceType string, selector spec.Selector, fallbacks []spec.Selector) ir.PhysicalSelectorExecutionMode {
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
	if selectorHasNoArrays(input.Selector) && !metadata.Repeated {
		return ir.PhysicalSelectorDirectScalar
	}
	if selectorHasIteratedArray(input.Selector) && metadata.Repeated {
		return ir.PhysicalSelectorConditionalArray
	}
	return ir.PhysicalSelectorGeneric
}
