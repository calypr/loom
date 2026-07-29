package ir

import "testing"

func unnestValueExpression(variable string) PhysicalExpression {
	return PhysicalExpression{
		Kind:         PhysicalValueExpression,
		Cardinality:  PhysicalArrayCardinality,
		NullBehavior: PhysicalEmptyOnNull,
		Value:        &PhysicalValue{Variable: variable, Path: []string{"payload"}},
	}
}

func unnestPlan(mode PhysicalUnnestJoinMode) PhysicalPlan {
	return PhysicalPlan{
		Version: 1,
		BindVars: map[string]any{
			"collection": "Patient",
		},
		Operations: []PhysicalOperation{
			{Kind: PhysicalRootScanOp, RootScan: &PhysicalRootScan{Variable: "root", CollectionBindKey: "collection"}},
			{Kind: PhysicalUnnestOp, Unnest: &PhysicalUnnest{
				InputVariable: "root", OutputVariable: "item", Ordinality: "item_index",
				Expression: unnestValueExpression("root"), JoinMode: mode,
			}},
			{Kind: PhysicalReturnOp, Return: &PhysicalReturn{Projections: []PhysicalProjection{{
				Name: "item", Expression: &PhysicalExpression{
					Kind: PhysicalValueExpression, Cardinality: PhysicalScalarCardinality,
					NullBehavior: PhysicalPreserveNull, Value: &PhysicalValue{Variable: "item"},
				},
			}}}},
		},
	}
}

func TestPhysicalUnnestValidatesInnerAndOuterPlans(t *testing.T) {
	for _, mode := range []PhysicalUnnestJoinMode{PhysicalUnnestInner, PhysicalUnnestOuter} {
		plan := unnestPlan(mode)
		if err := plan.Validate(); err != nil {
			t.Fatalf("mode %s: %v", mode, err)
		}
	}
}

func TestPhysicalUnnestRejectsInvalidScopeAndCardinality(t *testing.T) {
	tests := []struct {
		name string
		edit func(*PhysicalPlan)
		want string
	}{
		{"source out of scope", func(plan *PhysicalPlan) { plan.Operations[1].Unnest.InputVariable = "future" }, "out of scope"},
		{"scalar source", func(plan *PhysicalPlan) {
			plan.Operations[1].Unnest.Expression.Cardinality = PhysicalScalarCardinality
		}, "array-valued"},
		{"shadowed output", func(plan *PhysicalPlan) { plan.Operations[1].Unnest.OutputVariable = "root" }, "shadows"},
		{"unsafe ordinality", func(plan *PhysicalPlan) { plan.Operations[1].Unnest.Ordinality = "item.index" }, "unsafe"},
		{"unknown mode", func(plan *PhysicalPlan) { plan.Operations[1].Unnest.JoinMode = "CROSS" }, "unsupported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := unnestPlan(PhysicalUnnestInner)
			test.edit(&plan)
			if err := plan.Validate(); err == nil || !contains(err.Error(), test.want) {
				t.Fatalf("Validate() = %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestPhysicalUnnestCanBeNestedInSubplan(t *testing.T) {
	plan := unnestPlan(PhysicalUnnestInner)
	plan.Operations[1] = PhysicalOperation{Kind: PhysicalSetOp, Set: &PhysicalSet{
		Variable: "items",
		Subplan: PhysicalSubplan{
			Captures: []string{"root"},
			Operations: []PhysicalOperation{{Kind: PhysicalUnnestOp, Unnest: &PhysicalUnnest{
				InputVariable: "root", OutputVariable: "item", Expression: unnestValueExpression("root"), JoinMode: PhysicalUnnestInner,
			}}},
			Return: PhysicalExpression{Kind: PhysicalValueExpression, Cardinality: PhysicalObjectCardinality,
				NullBehavior: PhysicalPreserveNull, Value: &PhysicalValue{Variable: "item"}},
		},
	}}
	plan.Operations[2].Return.Projections[0].Expression.Value.Variable = "items"
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestClonePhysicalUnnestClonesExpression(t *testing.T) {
	plan := unnestPlan(PhysicalUnnestOuter)
	copy := ClonePhysicalPlan(plan)
	copy.Operations[1].Unnest.Expression.Value.Path[0] = "changed"
	if got := plan.Operations[1].Unnest.Expression.Value.Path[0]; got != "payload" {
		t.Fatalf("clone mutated original unnest expression path: %q", got)
	}
}

func contains(value, want string) bool {
	for i := 0; i+len(want) <= len(value); i++ {
		if value[i:i+len(want)] == want {
			return true
		}
	}
	return false
}
