package lower

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestLowerCorrelatedBindingPreservesCodingValueScope(t *testing.T) {
	binding := fhirschema.CorrelatedBinding{
		OwnerPath: "component[]", KeyPath: "component[].code.coding[]",
		ValueScope: fhirschema.CorrelatedValueScopeCoding, SystemPath: "system", CodePath: "code",
		VersionPath: "version", ValuePath: "display", ValueFallback: []string{"code"}, FallbackOnBlankString: true, LogicalType: "string",
	}
	correlation, err := LowerCorrelatedBindingWithVersion("Observation", binding, ir.PhysicalValue{Variable: "root"}, "system", "code", "version")
	if err != nil {
		t.Fatal(err)
	}
	if correlation.ValueScope != fhirschema.CorrelatedValueScopeCoding || !correlation.KeyIsArray || correlation.VersionSelector == nil || correlation.VersionSelector.CanonicalPath() != "version" || correlation.VersionBindKey != "version" || correlation.ValueSelector.CanonicalPath() != "display" ||
		len(correlation.ValueFallbacks) != 1 || correlation.ValueFallbacks[0].CanonicalPath() != "code" || !correlation.FallbackOnBlankString {
		t.Fatalf("lowered correlation = %#v", correlation)
	}
}

func TestLowerCorrelatedBindingPreservesSingletonCodingKeyShape(t *testing.T) {
	binding := fhirschema.CorrelatedBinding{
		OwnerPath: "input[]", KeyPath: "input[].valueCoding", ValueScope: fhirschema.CorrelatedValueScopeCoding,
		SystemPath: "system", CodePath: "code", VersionPath: "version", ValuePath: "display", LogicalType: "string",
	}
	correlation, err := LowerCorrelatedBindingWithVersion("Task", binding, ir.PhysicalValue{Variable: "root"}, "system", "code", "version")
	if err != nil {
		t.Fatal(err)
	}
	if correlation.OwnerResource != "TaskInput" || correlation.KeyResource != "Coding" || correlation.KeyIsArray || correlation.KeySelector.CanonicalPath() != "valueCoding" {
		t.Fatalf("lowered singleton Coding correlation = %#v", correlation)
	}
}

func TestLowerCorrelatedBindingLeavesLegacyVersionUnset(t *testing.T) {
	binding := fhirschema.CorrelatedBinding{
		OwnerPath: "component[]", KeyPath: "component[].code.coding[]",
		SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value", LogicalType: "decimal",
	}
	correlation, err := LowerCorrelatedBinding("Observation", binding, ir.PhysicalValue{Variable: "root"}, "system", "code")
	if err != nil {
		t.Fatal(err)
	}
	if !correlation.KeyIsArray || correlation.VersionSelector != nil || correlation.VersionBindKey != "" {
		t.Fatalf("legacy correlation unexpectedly has version matching: %#v", correlation)
	}
}
