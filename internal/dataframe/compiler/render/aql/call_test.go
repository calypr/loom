package aql

import (
	"strings"
	"testing"
)

func scalarLiteral(key string) PhysicalExpression {
	return PhysicalExpression{
		Kind:         PhysicalLiteralExpression,
		Cardinality:  PhysicalScalarCardinality,
		NullBehavior: PhysicalPreserveNull,
		Literal:      &PhysicalLiteral{BindKey: key},
	}
}

func callExpression(name string, args ...PhysicalExpression) PhysicalExpression {
	return PhysicalExpression{
		Kind:         PhysicalCallExpression,
		Cardinality:  PhysicalScalarCardinality,
		NullBehavior: PhysicalPreserveNull,
		Call:         &PhysicalCall{Name: name, Args: args},
	}
}

func TestRenderGenericPhysicalCallsParameterizeLiterals(t *testing.T) {
	renderer := &physicalPlanRenderer{
		bindVars:       map[string]any{"left": "A", "right": "B", "delimiter": ","},
		collectionKeys: map[string]struct{}{},
		setVariables:   map[string]string{},
		reservedVars:   map[string]struct{}{},
	}
	concat := callExpression("concat", scalarLiteral("left"), scalarLiteral("right"))
	got, err := renderer.renderExpression(concat)
	if err != nil {
		t.Fatal(err)
	}
	if got != "CONCAT(@left, @right)" {
		t.Fatalf("concat = %q", got)
	}
	join := callExpression("join", callExpression("all", scalarLiteral("left")), scalarLiteral("delimiter"))
	got, err = renderer.renderExpression(join)
	if err != nil {
		t.Fatal(err)
	}
	if got != "JOIN([@left], @delimiter)" {
		t.Fatalf("join = %q", got)
	}
	if strings.Contains(got, "A") || strings.Contains(got, "B") {
		t.Fatalf("literal values leaked into AQL: %q", got)
	}
}

func TestRenderGenericPhysicalCaseUsesBoundNull(t *testing.T) {
	renderer := &physicalPlanRenderer{
		bindVars:       map[string]any{"condition": true, "value": "yes"},
		collectionKeys: map[string]struct{}{},
		setVariables:   map[string]string{},
		reservedVars:   map[string]struct{}{},
	}
	call := callExpression("case", scalarLiteral("condition"), scalarLiteral("value"))
	got, err := renderer.renderExpression(call)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "@condition") || !strings.Contains(got, "@value") {
		t.Fatalf("case did not preserve bind-backed args: %q", got)
	}
	if !strings.Contains(got, "@__loom_physical_call_null") {
		t.Fatalf("case without ELSE did not bind null: %q", got)
	}
	if renderer.bindVars["__loom_physical_call_null"] != nil {
		t.Fatalf("case null bind is not nil: %#v", renderer.bindVars)
	}
}

func TestRenderGenericPhysicalUUIDUsesExactPostQueryMarker(t *testing.T) {
	renderer := &physicalPlanRenderer{
		bindVars:       map[string]any{"namespace": "ns", "name": "name"},
		collectionKeys: map[string]struct{}{},
		setVariables:   map[string]string{},
		reservedVars:   map[string]struct{}{},
	}
	uuid := callExpression("uuid5", scalarLiteral("namespace"), scalarLiteral("name"))
	got, err := renderer.renderExpression(uuid)
	if err != nil || !strings.Contains(got, "__loom_exact_uuid_operation") || !strings.Contains(got, "uuid5") {
		t.Fatalf("uuid5 exact marker was not rendered: query=%q err=%v", got, err)
	}
	uuid3 := callExpression("uuid3", scalarLiteral("namespace"), scalarLiteral("name"))
	got, err = renderer.renderExpression(uuid3)
	if err != nil || !strings.Contains(got, "__loom_exact_uuid_operation") || !strings.Contains(got, "uuid3") {
		t.Fatalf("uuid3 exact marker was not rendered: query=%q err=%v", got, err)
	}
	got, err = renderer.renderExpression(callExpression("sanitize_name", scalarLiteral("name")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "REGEX_REPLACE(TO_STRING(@name)") {
		t.Fatalf("sanitize_name was not lowered: %q", got)
	}
}

func TestRenderNestedUUIDUsesRecursivePostQueryCallMarker(t *testing.T) {
	renderer := &physicalPlanRenderer{
		bindVars:       map[string]any{"namespace": "ns", "name": "name", "suffix": "-suffix"},
		collectionKeys: map[string]struct{}{},
		setVariables:   map[string]string{},
		reservedVars:   map[string]struct{}{},
	}
	nested := callExpression("concat", callExpression("uuid5", scalarLiteral("namespace"), scalarLiteral("name")), scalarLiteral("suffix"))
	got, err := renderer.renderExpression(nested)
	if err != nil || !strings.Contains(got, "__loom_postquery_call") || !strings.Contains(got, "concat") {
		t.Fatalf("nested UUID marker was not rendered: query=%q err=%v", got, err)
	}
}

func TestPhysicalPlanValidatesLiteralAndCallPayloads(t *testing.T) {
	valid := callExpression("concat", scalarLiteral("left"), scalarLiteral("right"))
	plan := PhysicalPlan{
		Version:  1,
		BindVars: map[string]any{"root_collection": "Patient", "left": "A", "right": "B"},
		Operations: []PhysicalOperation{
			{Kind: PhysicalRootScanOp, RootScan: &PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: PhysicalReturnOp, Return: &PhysicalReturn{Projections: []PhysicalProjection{{Name: "value", Expression: &valid}}}},
		},
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("valid literal/call plan rejected: %v", err)
	}
	missing := scalarLiteral("missing")
	plan.Operations[1].Return.Projections[0].Expression = &missing
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "bind key \"missing\"") {
		t.Fatalf("missing literal bind was accepted: %v", err)
	}
	unsupported := callExpression("not_a_recipe_operator", scalarLiteral("left"))
	plan.Operations[1].Return.Projections[0].Expression = &unsupported
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported call") {
		t.Fatalf("unsupported call was accepted: %v", err)
	}
}
