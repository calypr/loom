package dataframe

import "testing"

func TestBuildPhysicalPlanUsesSemanticPlanDirectly(t *testing.T) {
	plan, err := BuildPhysicalPlan(SemanticPlan{
		Version: 1,
		Project: "project-1",
		Root: SemanticNode{
			Alias:        "root",
			ResourceType: "Patient",
			Children: []SemanticNode{{
				Alias:        "specimen",
				ResourceType: "Specimen",
				EdgeLabel:    "subject_Patient",
			}},
		},
	})
	if err != nil {
		t.Fatalf("BuildPhysicalPlan() error = %v", err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("physical plan validation: %v", err)
	}
}

func TestBuildPhysicalPlanLowersRootSelection(t *testing.T) {
	selector, err := ParseSelector("gender")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPhysicalPlan(SemanticPlan{
		Version: 1,
		Project: "project-1",
		Root: SemanticNode{
			Alias:        "root",
			ResourceType: "Patient",
			Fields: []SemanticField{{
				Name:     "gender",
				FieldRef: "gender",
				Selector: selector,
			}},
		},
	})
	if err != nil {
		t.Fatalf("BuildPhysicalPlan() error = %v", err)
	}
	projections := plan.Operations[len(plan.Operations)-1].Return.Projections
	if len(projections) != 2 || projections[1].Expression == nil || projections[1].Expression.Kind != PhysicalExtractExpression {
		t.Fatalf("root selection was not lowered to EXTRACT projection: %#v", projections)
	}
}
