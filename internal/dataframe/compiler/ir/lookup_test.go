package ir

import "testing"

func lookupExpression() PhysicalExpression {
	value := func(path ...string) PhysicalExpression {
		return PhysicalExpression{Kind: PhysicalValueExpression, Cardinality: PhysicalScalarCardinality, NullBehavior: PhysicalPreserveNull, Value: &PhysicalValue{Variable: "dynamic_item", Path: path}}
	}
	return PhysicalExpression{
		Kind:         PhysicalLookupExpression,
		Cardinality:  PhysicalScalarCardinality,
		NullBehavior: PhysicalPreserveNull,
		Lookup: &PhysicalLookup{
			Source:       PhysicalExpression{Kind: PhysicalValueExpression, Cardinality: PhysicalArrayCardinality, NullBehavior: PhysicalPreserveNull, Value: &PhysicalValue{Variable: "root", Path: []string{"items"}}},
			ItemVariable: "dynamic_item",
			ItemKey:      value("url"),
			ItemValue:    value("value"),
			MatchBindKey: "dynamic_key",
		},
	}
}

func TestPhysicalLookupValidatesScopeAndBind(t *testing.T) {
	plan := PhysicalPlan{Version: 1, BindVars: map[string]any{"collection": "Patient", "dynamic_key": "x"}, Operations: []PhysicalOperation{
		{Kind: PhysicalRootScanOp, RootScan: &PhysicalRootScan{Variable: "root", CollectionBindKey: "collection"}},
		{Kind: PhysicalReturnOp, Return: &PhysicalReturn{Projections: []PhysicalProjection{{Name: "value", Expression: func() *PhysicalExpression { value := lookupExpression(); return &value }()}}}},
	}}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := lookupExpression()
	bad.Lookup.ItemKey.Cardinality = PhysicalArrayCardinality
	plan.Operations[1].Return.Projections[0].Expression = &bad
	if err := plan.Validate(); err == nil {
		t.Fatal("array-valued lookup key was accepted")
	}
}

func TestPhysicalLookupCloneAndFingerprint(t *testing.T) {
	original := lookupExpression()
	clone := ClonePhysicalExpression(original)
	if clone.Lookup == original.Lookup || clone.Lookup.Source.Value == original.Lookup.Source.Value {
		t.Fatal("lookup clone shares mutable payload")
	}
	left, err := PhysicalExpressionFingerprint(original)
	if err != nil {
		t.Fatal(err)
	}
	right, err := PhysicalExpressionFingerprint(clone)
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("clone fingerprint differs: %s != %s", left, right)
	}
}
