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

// selectorModeContext is the complete physical context needed to classify a
// selector. Source/cardinality/null behavior are intentionally carried here
// even though the current schema proof only needs resource type and selector:
// keeping them at this boundary prevents individual frontends from making a
// mode decision with incomplete physical context and leaves one place for
// future cardinality-sensitive proofs.
type selectorModeContext struct {
	ResourceType string
	Selector     Selector
	Fallbacks    []Selector
	Source       PhysicalValue
	Cardinality  PhysicalCardinality
	NullBehavior PhysicalNullBehavior
	Metadata     fhirschema.TerminalScalarMetadata
	MetadataOK   bool
}

func selectorExecutionMode(resourceType string, selector Selector, fallbacks ...Selector) PhysicalSelectorExecutionMode {
	return selectorExecutionModeWithContext(selectorModeContext{
		ResourceType: resourceType,
		Selector:     selector,
		Fallbacks:    fallbacks,
		Cardinality:  PhysicalScalarCardinality,
		NullBehavior: PhysicalPreserveNull,
	})
}

// selectorExecutionModeForExpression is the shared selector classifier used
// by expression frontends. It resolves schema metadata once and passes the
// complete physical expression context through the same proof used by
// generic field lowering.
func selectorExecutionModeForExpression(resourceType string, selector Selector, fallbacks []Selector, source PhysicalValue, cardinality PhysicalCardinality, nullBehavior PhysicalNullBehavior) PhysicalSelectorExecutionMode {
	return selectorExecutionModeWithContext(selectorModeContext{
		ResourceType: resourceType,
		Selector:     selector,
		Fallbacks:    fallbacks,
		Source:       source,
		Cardinality:  cardinality,
		NullBehavior: nullBehavior,
	})
}

func selectorExecutionModeWithContext(input selectorModeContext) PhysicalSelectorExecutionMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOOM_PHYSICAL_RULE_TYPED_SELECTORS"))) {
	case "off", "0", "false", "disabled":
		return PhysicalSelectorGeneric
	}
	if len(input.Fallbacks) != 0 || input.Selector.Filter != nil {
		return PhysicalSelectorGeneric
	}
	metadata, ok := input.Metadata, input.MetadataOK
	if !ok {
		metadata, ok = fhirschema.ResolveTerminalScalarMetadata(input.ResourceType, input.Selector.CanonicalPath())
	}
	if !ok {
		return PhysicalSelectorGeneric
	}
	if selectorHasNoArrays(input.Selector) && !metadata.Repeated {
		return PhysicalSelectorDirectScalar
	}
	if selectorHasIteratedArray(input.Selector) && metadata.Repeated {
		return PhysicalSelectorConditionalArray
	}
	return PhysicalSelectorGeneric
}
