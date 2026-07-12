package dataframe

import "testing"

func TestNormalizeShapingPlanDeterministicSpecs(t *testing.T) {
	selector, _ := ParseSelector("code.coding[].code")
	value, _ := ParseSelector("valueQuantity.value")
	plan := SemanticPlan{Root: SemanticNode{
		Alias: "root", ResourceType: "Observation",
		Aggregates: []SemanticAggregate{
			{Name: "values", Operation: "DISTINCT_VALUES", Selector: &value},
			{Name: "", Operation: "COUNT"},
		},
		Pivots: []SemanticPivot{{
			Name: "labs", ColumnSelector: selector, ValueSelector: value,
			Columns: []string{"weight", "height"}, Family: "observation",
		}},
	}}
	got, err := NormalizeShapingPlan(plan, testShapingDefaults())
	if err != nil {
		t.Fatalf("NormalizeShapingPlan: %v", err)
	}
	if len(got.Aggregates) != 2 || got.Aggregates[0].Alias != "root.aggregate_2" || got.Aggregates[1].Alias != "root.values" {
		t.Fatalf("unexpected deterministic aggregates: %#v", got.Aggregates)
	}
	if len(got.Pivots) != 1 || got.Pivots[0].Alias != "root.labs" {
		t.Fatalf("unexpected pivots: %#v", got.Pivots)
	}
	if got.Pivots[0].Config.CardinalityPolicy != PivotRequestedColumns || got.Pivots[0].Config.MaxColumns != 2 {
		t.Fatalf("unexpected pivot cardinality: %#v", got.Pivots[0].Config)
	}
}

func TestNormalizeShapingPlanUsesBoundedDiscovery(t *testing.T) {
	key, _ := ParseSelector("code")
	value, _ := ParseSelector("value")
	plan := SemanticPlan{Root: SemanticNode{Alias: "root", ResourceType: "Observation", Pivots: []SemanticPivot{{ColumnSelector: key, ValueSelector: value}}}}
	got, err := NormalizeShapingPlan(plan, testShapingDefaults())
	if err != nil {
		t.Fatalf("NormalizeShapingPlan: %v", err)
	}
	if got.Pivots[0].Config.CardinalityPolicy != PivotBoundedDiscovery || got.Pivots[0].Config.MaxColumns != 64 {
		t.Fatalf("unexpected discovery policy: %#v", got.Pivots[0].Config)
	}
}

func TestNormalizeShapingPlanRejectsBadRequests(t *testing.T) {
	validDefaults := testShapingDefaults()
	tests := []struct {
		name string
		plan SemanticPlan
		defs ShapingDefaults
	}{
		{"invalid defaults", SemanticPlan{Root: SemanticNode{Alias: "root", ResourceType: "Patient"}}, ShapingDefaults{}},
		{"missing node alias", SemanticPlan{Root: SemanticNode{ResourceType: "Patient"}}, validDefaults},
		{"duplicate node alias", SemanticPlan{Root: SemanticNode{Alias: "root", ResourceType: "Patient", Children: []SemanticNode{{Alias: "root", ResourceType: "Specimen", EdgeLabel: "subject"}}}}, validDefaults},
		{"unknown relationship", SemanticPlan{Root: SemanticNode{Alias: "root", ResourceType: "Patient", Children: []SemanticNode{{Alias: "child", ResourceType: "Specimen", EdgeLabel: "not-a-real-edge"}}}}, validDefaults},
		{"unsupported aggregate", SemanticPlan{Root: SemanticNode{Alias: "root", ResourceType: "Patient", Aggregates: []SemanticAggregate{{Operation: "MEDIAN"}}}}, validDefaults},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NormalizeShapingPlan(test.plan, test.defs); err == nil {
				t.Fatal("invalid shaping request unexpectedly succeeded")
			}
		})
	}
}

func testShapingDefaults() ShapingDefaults {
	return ShapingDefaults{
		NullPolicy: NullIgnore, PivotDuplicatePolicy: DuplicateDistinctArray,
		SparsePivotPolicy: SparsePivotNull, MaxDiscoveredPivotCols: 64,
	}
}
