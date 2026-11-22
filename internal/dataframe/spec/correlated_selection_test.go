package spec

import (
	fhirbinding "github.com/calypr/loom/internal/fhir/binding"
	"testing"
)

func TestCorrelatedCategoryFilterRequiresExplicitScope(t *testing.T) {
	binding := &fhirbinding.CorrelatedBinding{
		KeyPath: "code.coding[]", SystemPath: "system", CodePath: "code", VersionPath: "version",
		ValueScope: fhirbinding.CorrelatedValueScopeCoding, SelectionScope: fhirbinding.CorrelatedSelectionScopeSystemVersion,
		ValuePath: "display", ValueFallback: []string{"code"}, LogicalType: "string",
	}
	filter := TypedFilter{
		FieldRef: "Condition.code", Selector: "code.coding[].code", FieldKind: FilterCode,
		Repeated: true, Quantifier: QuantifierAny, Operator: FilterEquals, Correlation: binding,
		Values: []FilterValue{{Kind: FilterCode, Code: &CodeValue{System: "urn:test"}}},
	}
	if err := ValidateTypedFilterForResource("Condition", filter); err != nil {
		t.Fatalf("explicit category filter rejected: %v", err)
	}
	if err := filter.Values[0].Validate(); err == nil {
		t.Fatal("standalone blank CODE unexpectedly became a wildcard")
	}
	binding.SelectionScope = ""
	if err := ValidateTypedFilterForResource("Condition", filter); err == nil {
		t.Fatal("legacy exact filter accepted a blank code")
	}
	binding.SelectionScope = fhirbinding.CorrelatedSelectionScopeSystemVersion
	filter.Values[0].Code.Code = "normal"
	if err := ValidateTypedFilterForResource("Condition", filter); err == nil {
		t.Fatal("category filter accepted a selected exact code")
	}
}
