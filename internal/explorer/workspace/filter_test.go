package workspace

import "testing"

func TestConstructionFilterValueCountDiagnostics(t *testing.T) {
	text := "x"
	tests := []struct {
		name   string
		filter *ConstructionFilter
		want   string
	}{
		{
			name:   "exists rejects values",
			filter: &ConstructionFilter{ColumnID: "value", Operator: ConstructionFilterExists, Values: []FilterValue{{Kind: ConstructionFilterString, String: &text}}},
			want:   "EXISTS does not accept values",
		},
		{
			name:   "in requires a value",
			filter: &ConstructionFilter{ColumnID: "value", Operator: ConstructionFilterIn},
			want:   "IN requires at least one value",
		},
		{
			name:   "equals requires one value",
			filter: &ConstructionFilter{ColumnID: "value", Operator: ConstructionFilterEquals},
			want:   "EQUALS requires exactly one value",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			step := ConstructionStep{Operation: ConstructionOperation{Filter: test.filter}}
			err := validateConstructionFilter(step, map[string]StageColumn{"value": {ID: "value"}}, nil)
			if err == nil || err.Error() != test.want {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestConstructionFilterRetainsWorkspaceKindValidation(t *testing.T) {
	integer := int64(3)
	step := ConstructionStep{Operation: ConstructionOperation{Filter: &ConstructionFilter{
		ColumnID: "value", Operator: ConstructionFilterContains,
		Values: []FilterValue{{Kind: ConstructionFilterInteger, Integer: &integer}},
	}}}
	if err := validateConstructionFilter(step, map[string]StageColumn{"value": {ID: "value"}}, nil); err != nil {
		t.Fatalf("workspace filter kind validation changed: %v", err)
	}
}
