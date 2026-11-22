package lower

import (
	fhirbinding "github.com/calypr/loom/internal/fhir/binding"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestLowerCorrelatedBindingPreservesCodingValueScope(t *testing.T) {
	binding := fhirbinding.CorrelatedBinding{
		OwnerPath: "component[]", KeyPath: "component[].code.coding[]",
		ValueScope: fhirbinding.CorrelatedValueScopeCoding, SystemPath: "system", CodePath: "code",
		VersionPath: "version", ValuePath: "display", ValueFallback: []string{"code"}, FallbackOnBlankString: true, LogicalType: "string",
	}
	correlation, err := LowerCorrelatedBindingWithVersion("Observation", binding, ir.PhysicalValue{Variable: "root"}, "system", "code", "version")
	if err != nil {
		t.Fatal(err)
	}
	if correlation.ValueScope != fhirbinding.CorrelatedValueScopeCoding || !correlation.KeyIsArray || correlation.VersionSelector == nil || correlation.VersionSelector.CanonicalPath() != "version" || correlation.VersionBindKey != "version" || correlation.ValueSelector.CanonicalPath() != "display" ||
		len(correlation.ValueFallbacks) != 1 || correlation.ValueFallbacks[0].CanonicalPath() != "code" || !correlation.FallbackOnBlankString {
		t.Fatalf("lowered correlation = %#v", correlation)
	}
}

func TestLowerCorrelatedBindingPreservesSingletonCodingKeyShape(t *testing.T) {
	binding := fhirbinding.CorrelatedBinding{
		OwnerPath: "input[]", KeyPath: "input[].valueCoding", ValueScope: fhirbinding.CorrelatedValueScopeCoding,
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
	binding := fhirbinding.CorrelatedBinding{
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

func TestLowerCorrelatedBindingPreservesExplicitSystemVersionSelection(t *testing.T) {
	binding := fhirbinding.CorrelatedBinding{
		KeyPath: "code.coding[]", VersionPath: "version", ValueScope: fhirbinding.CorrelatedValueScopeCoding,
		SelectionScope: fhirbinding.CorrelatedSelectionScopeSystemVersion,
		SystemPath:     "system", CodePath: "code", ValuePath: "display", ValueFallback: []string{"code"}, LogicalType: "string",
	}
	correlation, err := LowerCorrelatedBindingWithVersion("Condition", binding, ir.PhysicalValue{Variable: "root"}, "system", "", "version")
	if err != nil {
		t.Fatal(err)
	}
	if correlation.SelectionScope != fhirbinding.CorrelatedSelectionScopeSystemVersion || correlation.CodeBindKey != "" || correlation.VersionBindKey != "version" {
		t.Fatalf("physical category = %#v", correlation)
	}
	if _, err := LowerCorrelatedBindingWithVersion("Condition", binding, ir.PhysicalValue{Variable: "root"}, "system", "code", "version"); err == nil {
		t.Fatal("category binding accepted an unused code bind")
	}
	physical := ir.PhysicalPlan{BindVars: map[string]any{}}
	if _, err := LowerCorrelatedPredicateWithIdentity(&physical, "Condition", binding, ir.PhysicalValue{Variable: "root"}, spec.CodeValue{System: "urn:test"}, ""); err != nil {
		t.Fatal(err)
	}
	if len(physical.BindVars) != 2 || physical.BindVars["correlated_version_root"] != "" {
		t.Fatalf("category predicate binds = %#v", physical.BindVars)
	}
}
