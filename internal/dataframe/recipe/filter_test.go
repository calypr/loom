package recipe

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestFilterValueCountDiagnostics(t *testing.T) {
	tests := []struct {
		name   string
		filter Filter
		want   string
	}{
		{
			name:   "exists rejects values",
			filter: Filter{Select: "name", Operator: FilterExists, Values: []spec.FilterValue{{Kind: FilterString, String: recipeStringPointer("x")}}},
			want:   "invalid_filter_values at $.values: EXISTS and MISSING do not accept values",
		},
		{
			name:   "in requires a value",
			filter: Filter{Select: "name", Operator: FilterIn},
			want:   "invalid_filter_values at $.values: IN requires at least one value",
		},
		{
			name: "equals requires one value",
			filter: Filter{Select: "name", Operator: FilterEquals, Values: []spec.FilterValue{
				{Kind: FilterString, String: recipeStringPointer("x")}, {Kind: FilterString, String: recipeStringPointer("y")},
			}},
			want: "invalid_filter_values at $.values: operator requires exactly one value",
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

func TestFilterOperatorKindCompatibilityRemainsRecipeSpecific(t *testing.T) {
	integer := int64(3)
	filter := Filter{Select: "name", Operator: FilterContains, Values: []spec.FilterValue{{Kind: FilterInteger, Integer: &integer}}}
	if err := filter.Validate(); err == nil || err.Error() != "invalid_filter_value at $.values[0].kind: operator CONTAINS_TEXT is incompatible with INTEGER" {
		t.Fatalf("validation error = %v, want recipe kind incompatibility", err)
	}
}

func TestConstructionFilterValueCountDiagnostics(t *testing.T) {
	tests := []struct {
		name   string
		filter ConstructionFilter
		want   string
	}{
		{
			name:   "exists rejects values",
			filter: ConstructionFilter{Operator: FilterExists, Values: []spec.FilterValue{{Kind: FilterString, String: recipeStringPointer("x")}}},
			want:   "step.filter operator EXISTS does not accept values",
		},
		{
			name:   "in requires a value",
			filter: ConstructionFilter{Operator: FilterIn},
			want:   "step.filter IN requires values",
		},
		{
			name:   "equals requires one value",
			filter: ConstructionFilter{Operator: FilterEquals},
			want:   "step.filter operator EQUALS requires exactly one value",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateConstructionFilter(test.filter, "step.filter"); err == nil || err.Error() != test.want {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
}

func recipeStringPointer(value string) *string { return &value }
