package schema

import "testing"

func TestValidateCategoricalBindingNamespacesAndSingletonCoding(t *testing.T) {
	for _, test := range []struct {
		name      string
		binding   CategoricalBinding
		wantKey   string
		wantOwner string
	}{
		{
			name: "repeated coding",
			binding: CategoricalBinding{
				OwnerPath: "category[]", KeyPath: "category[].coding[]", SystemPath: "system",
				ValuePath: "code", ValueFallback: []string{"display"}, LogicalType: "string",
				ValuePresentation: ValuePresentationDisplayOrCode,
			},
			wantKey: "coding[]", wantOwner: "CodeableConcept",
		},
		{
			name: "singleton coding",
			binding: CategoricalBinding{
				OwnerPath: "extension[].valueUsageContext", KeyPath: "extension[].valueUsageContext.code", SystemPath: "system", ValuePath: "code",
				ValueFallback: []string{"display"}, LogicalType: "string",
				ValuePresentation: ValuePresentationDisplayOrCode,
			},
			wantKey: "code", wantOwner: "UsageContext",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resource := "Observation"
			checked, err := ValidateCategoricalBinding(resource, test.binding)
			if err != nil {
				t.Fatalf("ValidateCategoricalBinding: %v", err)
			}
			if got := checked.KeySelector.CanonicalPath(); got != test.wantKey {
				t.Fatalf("key selector = %q, want %q", got, test.wantKey)
			}
			if got := checked.OwnerResource; got != test.wantOwner {
				t.Fatalf("owner resource = %q, want %q", got, test.wantOwner)
			}
			if checked.ValuePresentation != ValuePresentationDisplayOrCode || len(checked.ValueFallbacks) != 1 || checked.ValueFallbacks[0].CanonicalPath() != "display" {
				t.Fatalf("presentation/fallback = %#v / %#v", checked.ValuePresentation, checked.ValueFallbacks)
			}
		})
	}
}

func TestValidateCategoricalBindingRejectsEmptyNamespaceAndOutOfScopeKey(t *testing.T) {
	for _, binding := range []CategoricalBinding{
		{OwnerPath: "category[]", KeyPath: "category[].coding[]", ValuePath: "code"},
		{OwnerPath: "category[]", KeyPath: "code.coding[]", SystemPath: "system", ValuePath: "code"},
	} {
		if _, err := ValidateCategoricalBinding("Observation", binding); err == nil {
			t.Fatalf("ValidateCategoricalBinding(%#v) succeeded", binding)
		}
	}
}

func TestCategoricalBindingRejectsMislabeledCodeAndType(t *testing.T) {
	base := CategoricalBinding{OwnerPath: "code", KeyPath: "code.coding[]", SystemPath: "system", ValuePath: "code", LogicalType: "string", ValuePresentation: ValuePresentationDisplayOrCode}
	for _, mutate := range []func(*CategoricalBinding){
		func(b *CategoricalBinding) { b.ValuePath = "version" },
		func(b *CategoricalBinding) { b.SystemPath = "display" },
		func(b *CategoricalBinding) { b.ValueFallback = []string{"code"} },
		func(b *CategoricalBinding) { b.ValueFallback = []string{"display", "version"} },
		func(b *CategoricalBinding) { b.LogicalType = "integer" },
	} {
		binding := base
		mutate(&binding)
		if _, err := ValidateCategoricalBinding("Condition", binding); err == nil {
			t.Fatalf("accepted mislabeled categorical binding: %#v", binding)
		}
	}
}
