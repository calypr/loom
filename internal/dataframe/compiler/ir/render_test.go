package ir_test

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
)

func TestPhysicalPlanValidateRejectsRecursiveObjectExpression(t *testing.T) {
	plan, err := buildGenericPhysicalPlanWithContext(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{Alias: "root", ResourceType: "Patient"}}, recipeplan.ExecutionContext{Project: "project-1", AuthResourcePaths: []string{"/programs/p1"}})
	if err != nil {
		t.Fatal(err)
	}
	object := &ir.PhysicalObject{}
	cycle := ir.PhysicalExpression{Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull, Object: object}
	object.Fields = []ir.PhysicalExpressionProjection{{Name: "self", Expression: cycle}}
	plan.Operations[len(plan.Operations)-1].Return.Projections = []ir.PhysicalProjection{{Name: "row", Expression: &cycle}}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "recursive cycle") {
		t.Fatalf("Validate() error = %v; want recursive object cycle rejection", err)
	}
}
