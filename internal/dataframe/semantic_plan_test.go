package dataframe

import "testing"

func TestBuildSemanticPlanPreservesNestedFHIRSelections(t *testing.T) {
	plan, err := BuildSemanticPlan(Builder{
		Project:           "P1",
		AuthResourcePaths: []string{"pathA"},
		RootResourceType:  "Patient",
		Fields: []FieldSelect{{
			Name:            "case_id",
			FieldRef:        "Patient.identifier_value",
			Select:          `identifier[].value where system contains "case_id"`,
			FallbackSelects: []string{"id"},
			ValueMode:       "FIRST",
		}},
		Traversals: []TraversalStep{{
			Label:          "subject_Patient",
			ToResourceType: "Observation",
			Alias:          "observation",
			Pivots: []PivotSelect{{
				Name:         "values",
				ColumnSelect: "code.coding[].display",
				ValueSelect:  "valueQuantity.value",
				Columns:      []string{"Hemoglobin"},
			}},
			Aggregates: []AggregateSelect{{Name: "count", Operation: "COUNT"}},
			Traversals: []TraversalStep{{
				Label:          "subject_Observation",
				ToResourceType: "DocumentReference",
				Alias:          "document_reference",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("BuildSemanticPlan() error = %v", err)
	}
	if plan.Version != 1 || plan.Root.ResourceType != "Patient" {
		t.Fatalf("unexpected plan root: %#v", plan)
	}
	if plan.RowIdentity == nil || plan.RowIdentity.Grain != RowGrainPatient {
		t.Fatalf("expected inferred Patient row identity, got %#v", plan.RowIdentity)
	}
	if len(plan.Root.Fields) != 1 || plan.Root.Fields[0].Selector.CanonicalPath() != "identifier[].value" {
		t.Fatalf("root field not parsed: %#v", plan.Root.Fields)
	}
	if len(plan.Root.Children) != 1 || plan.Root.Children[0].Alias != "observation" {
		t.Fatalf("child not preserved: %#v", plan.Root.Children)
	}
	if len(plan.Root.Children[0].Pivots) != 1 || plan.Root.Children[0].Pivots[0].ColumnSelector.CanonicalPath() != "code.coding[].display" {
		t.Fatalf("pivot not parsed: %#v", plan.Root.Children[0].Pivots)
	}
	got := plan.Explain()
	if len(got.Nodes) != 3 || got.Nodes[1].ParentAlias != "root" || got.Nodes[2].ParentAlias != "observation" {
		t.Fatalf("unexpected explanation: %#v", got)
	}
}

func TestBuildSemanticPlanRejectsBadSelector(t *testing.T) {
	_, err := BuildSemanticPlan(Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		Fields:           []FieldSelect{{Name: "bad", Select: "identifier[nope]"}},
	})
	if err == nil {
		t.Fatal("expected bad selector error")
	}
}

func TestBuildSemanticPlanUsesGenericIdentityForGeneratedNonProductRoot(t *testing.T) {
	plan, err := BuildSemanticPlan(Builder{Project: "P1", RootResourceType: "Organization"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.RowIdentity == nil || plan.RowIdentity.Grain != RowGrainResource {
		t.Fatalf("generic root identity = %#v", plan.RowIdentity)
	}
}

func TestBuildSemanticPlanRejectsCrossGrainRootBeforeLowering(t *testing.T) {
	_, err := BuildSemanticPlan(Builder{
		Project: "P1", RootResourceType: "Patient", RowGrain: RowGrainSpecimen,
	})
	if err == nil {
		t.Fatal("cross-grain semantic plan unexpectedly succeeded")
	}
}

func TestCompileRequestCarriesStableRowIdentity(t *testing.T) {
	compiled, err := CompileRequest(Builder{Project: "P1", RootResourceType: "Specimen"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.RowIdentity == nil || compiled.RowIdentity.Grain != RowGrainSpecimen {
		t.Fatalf("compiled identity = %#v", compiled.RowIdentity)
	}
}
