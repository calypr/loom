package compiler

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestBuildPhysicalPlanUsesOutputPlanDirectly(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias:        "root",
		ResourceType: "Patient",
		Children: []semantic.SemanticNode{{
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
	selector, err := spec.ParseSelector("gender")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias:        "root",
		ResourceType: "Patient",
		Fields:       []semantic.SemanticField{testSemanticField("gender", selector, "")},
	},
	})
	if err != nil {
		t.Fatalf("BuildPhysicalPlan() error = %v", err)
	}
	projections := plan.Operations[len(plan.Operations)-1].Return.Projections
	if len(projections) != 2 || projections[1].Expression == nil || projections[1].Expression.Kind != ir.PhysicalExtractExpression {
		t.Fatalf("root selection was not lowered to EXTRACT projection: %#v", projections)
	}
}
