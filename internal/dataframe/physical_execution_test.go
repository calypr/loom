package dataframe

import (
	"reflect"
	"strings"
	"testing"
)

func TestCompileRequestUsesPhysicalExecutionForNavigationOnlyGenericPlan(t *testing.T) {
	builder := Builder{
		Project:           "P1",
		DatasetGeneration: "generation-a",
		AuthResourcePaths: []string{"/programs/p1"},
		RootResourceType:  "Specimen",
		Traversals: []TraversalStep{{
			Label:          "subject_Specimen",
			ToResourceType: "DocumentReference",
			Alias:          "file",
		}},
	}
	physical, err := CompileRequest(builder, 7)
	if err != nil {
		t.Fatalf("CompileRequest() error = %v", err)
	}
	if got := physical.BindVars["@root_collection"]; got != "Specimen" {
		t.Fatalf("physical root collection bind = %#v", got)
	}
	if got := physical.BindVars["@traversal_1_edge_collection"]; got != "fhir_edge" {
		t.Fatalf("physical edge collection bind = %#v", got)
	}
	for _, want := range []string{
		"FOR root IN @@root_collection",
		"SORT root._key",
		"LIMIT @limit",
		"FOR node_1, edge_1 IN 1..1 INBOUND root @@traversal_1_edge_collection",
		"RETURN { [@__loom_physical_projection_0_name]: root._key }",
	} {
		if !strings.Contains(physical.Query, want) {
			t.Fatalf("physical execution query missing %q:\n%s", want, physical.Query)
		}
	}
	if got, want := strings.Index(physical.Query, "LIMIT @limit"), strings.Index(physical.Query, "LET __loom_physical_set_1"); got < 0 || want < 0 || got > want {
		t.Fatalf("root limit must occur before optional navigation materialization:\n%s", physical.Query)
	}
	if strings.Contains(physical.Query, "FOR root IN Specimen") || strings.Contains(physical.Query, "generic_file_set") {
		t.Fatalf("navigation-only request unexpectedly used lowered string renderer:\n%s", physical.Query)
	}

	lowered, err := Lower(builder)
	if err != nil {
		t.Fatalf("Lower() error = %v", err)
	}
	legacy, err := Compile(lowered, 7)
	if err != nil {
		t.Fatalf("Compile(lowered) error = %v", err)
	}
	if physical.Query == legacy.Query {
		t.Fatalf("physical execution path did not render a distinct typed plan:\n%s", physical.Query)
	}
	assertPhysicalExecutionMetadataParity(t, physical, legacy)
}

func TestCompileRequestFallsBackForGenericSelectionUntilPhysicalProjectionExists(t *testing.T) {
	compiled, err := CompileRequest(Builder{
		Project:          "P1",
		RootResourceType: "Specimen",
		Fields:           []FieldSelect{{Name: "id", Select: "id"}},
	}, 3)
	if err != nil {
		t.Fatalf("CompileRequest() error = %v", err)
	}
	if !strings.Contains(compiled.Query, "FOR root IN Specimen") || !strings.Contains(compiled.Query, "root.payload.id") {
		t.Fatalf("selection request did not use the full lowered fallback:\n%s", compiled.Query)
	}
	if strings.Contains(compiled.Query, "@@root_collection") {
		t.Fatalf("selection request was incorrectly routed through the navigation-only physical renderer:\n%s", compiled.Query)
	}
}

func TestGenericPhysicalExecutionWindowIsTypedAndOptional(t *testing.T) {
	semantic, err := BuildSemanticPlan(Builder{Project: "P1", RootResourceType: "Patient"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildGenericPhysicalPlan(semantic)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = withGenericPhysicalExecutionWindow(plan, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) < 8 || plan.Operations[5].Kind != PhysicalSortOp || plan.Operations[6].Kind != PhysicalLimitOp {
		t.Fatalf("typed execution window = %#v", plan.Operations)
	}
	if got := plan.BindVars[genericPhysicalExecutionLimitBind]; got != 2 {
		t.Fatalf("typed limit bind = %#v", got)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("windowed physical plan did not validate: %v", err)
	}

	withoutLimit, err := withGenericPhysicalExecutionWindow(buildPhysicalPlanForTest(t, semantic), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(withoutLimit.Operations) < 7 || withoutLimit.Operations[5].Kind != PhysicalSortOp || withoutLimit.Operations[6].Kind == PhysicalLimitOp {
		t.Fatalf("unbounded physical execution window = %#v", withoutLimit.Operations)
	}
}

func buildPhysicalPlanForTest(t *testing.T, semantic SemanticPlan) PhysicalPlan {
	t.Helper()
	plan, err := BuildGenericPhysicalPlan(semantic)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func assertPhysicalExecutionMetadataParity(t *testing.T, physical, lowered CompiledQuery) {
	t.Helper()
	if physical.Project != lowered.Project ||
		physical.DatasetGeneration != lowered.DatasetGeneration ||
		physical.RootResourceType != lowered.RootResourceType ||
		physical.PlanMode != lowered.PlanMode ||
		physical.PlanProfile != lowered.PlanProfile ||
		physical.NamedSetCount != lowered.NamedSetCount ||
		physical.FileSummaries != lowered.FileSummaries ||
		physical.StudyLookup != lowered.StudyLookup ||
		physical.Limit != lowered.Limit ||
		!reflect.DeepEqual(physical.AuthResourcePaths, lowered.AuthResourcePaths) ||
		!reflect.DeepEqual(physical.OptimizationRules, lowered.OptimizationRules) ||
		!reflect.DeepEqual(physical.RowIdentity, lowered.RowIdentity) ||
		!reflect.DeepEqual(physical.Columns, lowered.Columns) ||
		!reflect.DeepEqual(physical.PivotFields, lowered.PivotFields) {
		t.Fatalf("physical metadata does not match lowered renderer:\nphysical=%#v\nlowered=%#v", physical, lowered)
	}
}
