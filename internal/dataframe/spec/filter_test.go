package spec

import (
	"testing"

	fhirbinding "github.com/calypr/loom/internal/fhir/binding"
)

func TestFilterOperatorValueCountRules(t *testing.T) {
	tests := []struct {
		operator FilterOperator
		rule     FilterValueCountRule
	}{
		{FilterEquals, FilterValueCountExactlyOne},
		{FilterNotEquals, FilterValueCountExactlyOne},
		{FilterIn, FilterValueCountOneOrMore},
		{FilterExists, FilterValueCountNone},
		{FilterMissing, FilterValueCountNone},
		{FilterContains, FilterValueCountExactlyOne},
		{FilterGreaterThan, FilterValueCountExactlyOne},
		{FilterGreaterEq, FilterValueCountExactlyOne},
		{FilterLessThan, FilterValueCountExactlyOne},
		{FilterLessEq, FilterValueCountExactlyOne},
	}
	for _, test := range tests {
		if got := test.operator.ValueCountRule(); got != test.rule {
			t.Errorf("%s value count rule = %d, want %d", test.operator, got, test.rule)
		}
	}
	if got := FilterOperator("UNKNOWN").ValueCountRule(); got != FilterValueCountInvalid {
		t.Errorf("unknown operator value count rule = %d, want invalid", got)
	}
	for _, test := range []struct {
		rule  FilterValueCountRule
		count int
		want  bool
	}{
		{FilterValueCountExactlyOne, 0, false},
		{FilterValueCountExactlyOne, 1, true},
		{FilterValueCountExactlyOne, 2, false},
		{FilterValueCountOneOrMore, 0, false},
		{FilterValueCountOneOrMore, 1, true},
		{FilterValueCountOneOrMore, 2, true},
		{FilterValueCountNone, 0, true},
		{FilterValueCountNone, 1, false},
		{FilterValueCountInvalid, 1, false},
	} {
		if got := test.rule.Accepts(test.count); got != test.want {
			t.Errorf("rule %d accepts %d = %v, want %v", test.rule, test.count, got, test.want)
		}
	}
}

func TestTypedFilterValueCountDiagnostics(t *testing.T) {
	tests := []struct {
		name   string
		filter TypedFilter
		want   string
	}{
		{
			name:   "exists rejects values",
			filter: TypedFilter{FieldRef: "name", FieldKind: FilterString, Operator: FilterExists, Values: []FilterValue{{Kind: FilterString, String: stringPointer("x")}}},
			want:   "operator EXISTS requires 0 value(s), got 1",
		},
		{
			name:   "in requires a value",
			filter: TypedFilter{FieldRef: "name", FieldKind: FilterString, Operator: FilterIn},
			want:   "IN filter requires at least one value",
		},
		{
			name: "equals requires one value",
			filter: TypedFilter{FieldRef: "name", FieldKind: FilterString, Operator: FilterEquals, Values: []FilterValue{
				{Kind: FilterString, String: stringPointer("x")}, {Kind: FilterString, String: stringPointer("y")},
			}},
			want: "operator EQUALS requires 1 value(s), got 2",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.filter.Validate(); err == nil || err.Error() != test.want {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
}

func stringPointer(value string) *string { return &value }

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
