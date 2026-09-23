package eval

import (
	"errors"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestEvaluateRowSetExtractPreservesFalsyValuesAndHiddenKey(t *testing.T) {
	selector := mustSelector(t, "identifier[].value")
	extract := ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull,
		Extract: &ir.PhysicalExtract{
			Source: ir.PhysicalValue{Variable: "child_set_1"}, ResourceType: "Observation", Selector: selector,
			ExecutionMode: ir.PhysicalSelectorConditionalArray,
		},
	}
	plan := planWithSet(t, []ir.PhysicalProjection{
		{Name: "_key", Hidden: true, Value: ir.PhysicalValue{Variable: "root", Path: []string{"_key"}}},
		{Name: "values", Expression: &extract},
	})
	program, err := Compile(plan)
	if err != nil {
		t.Fatal(err)
	}
	if got := program.SourceSetVariables(); !reflect.DeepEqual(got, []string{"child_set_1"}) {
		t.Fatalf("source sets = %v, want only terminal child_set_1", got)
	}

	got, err := program.EvaluateRow(map[string]any{
		"root": map[string]any{"_key": "root-1", "payload": map[string]any{}},
		"child_set_1": []any{
			map[string]any{"_key": "child-1", "payload": map[string]any{"identifier": []any{
				map[string]any{"value": false},
				map[string]any{"value": 0},
				map[string]any{"value": ""},
				map[string]any{"value": nil},
				map[string]any{},
			}}},
			map[string]any{"_key": "child-2", "payload": map[string]any{"identifier": []any{
				map[string]any{"value": "kept"},
			}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["_key"] != "root-1" {
		t.Fatalf("hidden key = %#v, want root-1", got["_key"])
	}
	want := []any{false, 0, "", "kept"}
	if !reflect.DeepEqual(got["values"], want) {
		t.Fatalf("set extract = %#v, want %#v", got["values"], want)
	}
}

func TestEvaluateRowKeyedMapUsesSortedUniqueStringValues(t *testing.T) {
	source := ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalPreserveNull,
		Extract: &ir.PhysicalExtract{
			Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}, ResourceType: "Observation",
			Selector: mustSelector(t, "identifier[]"), ExecutionMode: ir.PhysicalSelectorConditionalArray,
		},
	}
	keyed := ir.PhysicalExpression{
		Kind: ir.PhysicalKeyedMapExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull,
		KeyedMap: &ir.PhysicalKeyedMap{
			Source: source, ItemVariable: "identifier_item",
			ItemKey: physicalValueExpr("identifier_item", "system"), ItemValue: physicalValueExpr("identifier_item", "value"),
			Reduction: ir.PhysicalMapFirstSorted, FlattenSource: true,
		},
	}
	plan := rootPlan([]ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{{Name: "identifiers", Expression: &keyed}}}},
	})
	program, err := Compile(plan)
	if err != nil {
		t.Fatal(err)
	}
	got, err := program.EvaluateRow(map[string]any{"root": map[string]any{"payload": map[string]any{"identifier": []any{
		map[string]any{"system": "id", "value": "z"},
		map[string]any{"system": "id", "value": "a"},
		map[string]any{"system": "id", "value": "z"},
		map[string]any{"system": "missing-value"},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"id": "a"}
	if !reflect.DeepEqual(got["identifiers"], want) {
		t.Fatalf("keyed map = %#v, want %#v", got["identifiers"], want)
	}
}

func TestEvaluateRowCorrelatedPivotAndObjectLookupNullModes(t *testing.T) {
	plan := correlatedPivotPlan(t, "FIRST", "string", "string", "display")
	plan.Operations[2].Return.Projections = append(plan.Operations[2].Return.Projections,
		ir.PhysicalProjection{Name: "nil_value", Expression: objectLookup("root", "nil_key", ir.PhysicalPreserveNull)},
		ir.PhysicalProjection{Name: "false_value", Expression: objectLookup("root", "false_key", ir.PhysicalPreserveNull)},
		ir.PhysicalProjection{Name: "zero_value", Expression: objectLookup("root", "zero_key", ir.PhysicalPreserveNull)},
		ir.PhysicalProjection{Name: "empty_value", Expression: objectLookup("root", "empty_key", ir.PhysicalPreserveNull)},
		ir.PhysicalProjection{Name: "missing_empty", Expression: objectLookup("root", "missing_key", ir.PhysicalEmptyOnNull)},
	)
	plan.BindVars["nil_key"] = "nil"
	plan.BindVars["false_key"] = "false"
	plan.BindVars["zero_key"] = "zero"
	plan.BindVars["empty_key"] = "empty"
	plan.BindVars["missing_key"] = "missing"
	program, err := Compile(plan)
	if err != nil {
		t.Fatal(err)
	}
	got, err := program.EvaluateRow(map[string]any{"root": map[string]any{
		"payload": map[string]any{"category": []any{
			map[string]any{"coding": []any{
				map[string]any{"system": "urn:test", "code": "code-1", "display": "z"},
				map[string]any{"system": "urn:test", "code": "code-1", "display": "a"},
			}},
		}},
		"nil": nil, "false": false, "zero": 0, "empty": "",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got["pivot"], map[string]any{"code-1": "a"}) {
		t.Fatalf("pivot = %#v, want lexical FIRST", got["pivot"])
	}
	for field, want := range map[string]any{"nil_value": nil, "false_value": false, "zero_value": 0, "empty_value": ""} {
		if actual, ok := got[field]; !ok || !reflect.DeepEqual(actual, want) {
			t.Errorf("%s = %#v (present %v), want %#v", field, actual, ok, want)
		}
	}
	if got["missing_empty"] == nil || !reflect.DeepEqual(got["missing_empty"], []any{}) {
		t.Fatalf("EMPTY_ON_NULL lookup = %#v, want empty array", got["missing_empty"])
	}
}

func TestCompileRejectsPivotModesOutsideExactOrderingSubset(t *testing.T) {
	tests := []struct {
		name      string
		mode      string
		logical   string
		primitive string
		selector  string
	}{
		{name: "distinct requires sorted unique ordering", mode: "DISTINCT", logical: "string", primitive: "string", selector: "display"},
		{name: "boolean first requires backend sort ordering", mode: "FIRST", logical: "boolean", primitive: "boolean", selector: "userSelected"},
		{name: "unknown first primitive requires backend sort ordering", mode: "FIRST", logical: "unknown", selector: "display"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := correlatedPivotPlan(t, test.mode, test.logical, test.primitive, test.selector)
			if _, err := Compile(plan); !errors.Is(err, ErrUnsupportedPlan) {
				t.Fatalf("Compile() error = %v, want fail-closed ErrUnsupportedPlan", err)
			}
			if Eligible(plan) {
				t.Fatal("Eligible() accepted pivot semantics that require AQL fallback")
			}
		})
	}
}

func correlatedPivotPlan(t *testing.T, mode, logicalType, primitive, valueSelector string) ir.PhysicalPlan {
	t.Helper()
	source := ir.PhysicalValue{Variable: "root"}
	correlation := ir.PhysicalCorrelation{
		Source: source, ResourceType: "Observation", OwnerResource: "Observation", KeyResource: "Coding",
		KeySelector: mustSelector(t, "category[].coding[]"), SystemSelector: mustSelector(t, "system"), CodeSelector: mustSelector(t, "code"),
		ValueScope: ir.PhysicalCorrelationValueKeyItem, ValueSelector: mustSelector(t, valueSelector),
		LogicalType: logicalType, ValuePrimitive: primitive, SystemBindKey: "system", CodeBindKey: "code_bind",
	}
	pivot := ir.PhysicalExpression{
		Kind: ir.PhysicalPivotExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull,
		Pivot: &ir.PhysicalPivotMap{
			Source: source, ResourceType: "Observation", Correlation: &correlation, ColumnsBindKey: "columns", ProjectionMode: mode,
		},
	}
	plan := rootPlan([]ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalExpressionLetOp, ExpressionLet: &ir.PhysicalExpressionLet{Variable: "pivot", Expression: pivot}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{{Name: "pivot", Value: ir.PhysicalValue{Variable: "pivot"}}}}},
	})
	plan.BindVars["system"] = "urn:test"
	plan.BindVars["code_bind"] = "code-1"
	plan.BindVars["columns"] = []string{"code-1"}
	return plan
}

func TestCompileRejectsUnsupportedExpression(t *testing.T) {
	unsupported := ir.PhysicalExpression{
		Kind: ir.PhysicalCallExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
		Call: &ir.PhysicalCall{Name: "unknown"},
	}
	plan := rootPlan([]ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{{Name: "unsupported", Expression: &unsupported}}}},
	})
	if _, err := Compile(plan); err == nil {
		t.Fatal("Compile() unexpectedly accepted an unsupported expression")
	}
}

func planWithSet(t *testing.T, projections []ir.PhysicalProjection) ir.PhysicalPlan {
	t.Helper()
	plan := rootPlan([]ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalSetOp, Set: &ir.PhysicalSet{
			Variable: "child_set_1", // Empty Kind is the captured legacy NODE_SET default.
			Subplan: ir.PhysicalSubplan{
				Captures:   []string{"root"},
				Operations: []ir.PhysicalOperation{{Kind: ir.PhysicalCollectionScanOp, CollectionScan: &ir.PhysicalCollectionScan{Variable: "child", CollectionBindKey: "child_collection"}}},
				Return:     physicalValueExpr("child", ""),
			},
		}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: projections}},
	})
	plan.BindVars["child_collection"] = "Observation"
	if err := plan.Validate(); err != nil {
		t.Fatalf("synthetic plan is invalid: %v", err)
	}
	return plan
}

func rootPlan(operations []ir.PhysicalOperation) ir.PhysicalPlan {
	return ir.PhysicalPlan{Version: 1, BindVars: map[string]any{"root_collection": "Observation"}, Operations: operations}
}

func physicalValueExpr(variable, path string) ir.PhysicalExpression {
	value := ir.PhysicalValue{Variable: variable}
	if path != "" {
		value.Path = []string{path}
	}
	return ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &value}
}

func objectLookup(variable, bind string, behavior ir.PhysicalNullBehavior) *ir.PhysicalExpression {
	return &ir.PhysicalExpression{
		Kind: ir.PhysicalObjectLookupExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: behavior,
		ObjectLookup: &ir.PhysicalObjectLookup{ObjectVariable: variable, KeyBindKey: bind},
	}
}

func mustSelector(t *testing.T, path string) spec.Selector {
	t.Helper()
	selector, err := spec.ParseSelector(path)
	if err != nil {
		t.Fatalf("ParseSelector(%q): %v", path, err)
	}
	return selector
}
