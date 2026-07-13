package dataframe

import (
	"strings"
	"testing"
)

func TestTypedSelectorModesUseSchemaProvenShapes(t *testing.T) {
	semantic, err := BuildSemanticPlan(Builder{Project: "p", RootResourceType: "Patient", Fields: []FieldSelect{{Name: "gender", Select: "gender"}}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildGenericPhysicalPlanWithPolicy(semantic, DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderPhysicalPlan(withSelectorModeTestWindow(t, plan))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "root.payload.gender") {
		t.Fatalf("direct scalar selector did not lower to a direct expression:\n%s", rendered.Query)
	}
	semantic, err = BuildSemanticPlan(Builder{Project: "p", RootResourceType: "Condition", Fields: []FieldSelect{{Name: "diagnosis", Select: "code.coding[].display", ValueMode: "ALL"}}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err = BuildGenericPhysicalPlanWithPolicy(semantic, DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err = RenderPhysicalPlan(withSelectorModeTestWindow(t, plan))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered.Query, "FOR __root IN [root.payload]") {
		t.Fatalf("conditional selector retained singleton root enumeration:\n%s", rendered.Query)
	}
	if !strings.Contains(rendered.Query, "FOR __loom_selector_1 IN (__loom_selector_0.coding ? __loom_selector_0.coding : [])") {
		t.Fatalf("conditional repeated selector did not use guarded array iteration:\n%s", rendered.Query)
	}
}

func TestTypedSelectorModeFallsBackForPredicatesAndFallbacks(t *testing.T) {
	semantic, err := BuildSemanticPlan(Builder{
		Project: "p", RootResourceType: "Patient",
		Fields: []FieldSelect{{Name: "case_id", Select: `identifier[].value where system contains "case_id"`, ValueMode: "FIRST"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildGenericPhysicalPlanWithPolicy(semantic, DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range plan.Operations {
		if operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Expression == nil || projection.Expression.Extract == nil {
				continue
			}
			if projection.Expression.Extract.ExecutionMode != PhysicalSelectorGeneric {
				t.Fatalf("predicate selector unexpectedly specialized: %#v", projection.Expression.Extract.ExecutionMode)
			}
		}
	}
}

func TestTypedConditionalSelectorPreservesScalarCardinality(t *testing.T) {
	semantic, err := BuildSemanticPlan(Builder{Project: "p", RootResourceType: "Patient", Fields: []FieldSelect{{Name: "identifier", Select: "identifier[].value", ValueMode: "FIRST"}}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildGenericPhysicalPlanWithPolicy(semantic, DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderPhysicalPlan(withSelectorModeTestWindow(t, plan))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "FIRST((") || !strings.Contains(rendered.Query, "FOR __loom_selector_0 IN (root.payload.identifier ? root.payload.identifier : [])") {
		t.Fatalf("repeated scalar selector lost FIRST/cardinality boundary:\n%s", rendered.Query)
	}
}

func withSelectorModeTestWindow(t *testing.T, plan PhysicalPlan) PhysicalPlan {
	t.Helper()
	windowed, err := withGenericPhysicalExecutionWindow(plan, 25)
	if err != nil {
		t.Fatal(err)
	}
	return windowed
}
