package lower

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

// LowerCategoricalBinding converts a checked namespace-only Coding binding
// into the shared physical correlation shape. It deliberately has no code
// bind: code is an output value, not a dynamic feature identity.
func LowerCategoricalBinding(resourceType string, binding fhirschema.CategoricalBinding, source ir.PhysicalValue, systemBindKey string) (ir.PhysicalCorrelation, error) {
	checked, err := fhirschema.ValidateCategoricalBinding(resourceType, binding)
	if err != nil {
		return ir.PhysicalCorrelation{}, err
	}
	if strings.TrimSpace(systemBindKey) == "" {
		return ir.PhysicalCorrelation{}, fmt.Errorf("categorical binding system bind key is required")
	}
	codeSelector, err := spec.ParseSelector("code")
	if err != nil {
		return ir.PhysicalCorrelation{}, err
	}
	return ir.PhysicalCorrelation{
		Source: source, ResourceType: resourceType, OwnerResource: checked.OwnerResource,
		OwnerSelector: checked.OwnerSelector, KeyResource: checked.KeyResource,
		KeySelector: checked.KeySelector, SystemSelector: checked.SystemSelector,
		CodeSelector: codeSelector, ValueScope: ir.PhysicalCorrelationValueKeyItem,
		ValueSelector: checked.ValueSelector, ValueFallbacks: append([]spec.Selector(nil), checked.ValueFallbacks...),
		LogicalType: checked.LogicalType, ValuePrimitive: string(checked.ValuePrimitive),
		ValuePresentation: checked.ValuePresentation, ValueRepeated: checked.ValueRepeated,
		SystemBindKey: systemBindKey, NamespaceOnly: true,
	}, nil
}
