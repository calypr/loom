package compiler

import (
	"fmt"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func livenessTestPlan(t *testing.T) ir.PhysicalPlan {
	t.Helper()
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{Alias: "root", ResourceType: "Patient", Fields: []recipeplan.SemanticField{{
		Name: "id", FieldRef: "Patient.id", Expr: recipeplan.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "id"})}, Projection: spec.ProjectionScalar,
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func selectorExpression(variable, field string) ir.PhysicalExpression {
	return ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: variable, Path: []string{"payload", field}}}
}

func insertExpressionLet(plan *ir.PhysicalPlan, name string, expr ir.PhysicalExpression) {
	index := len(plan.Operations) - 1
	op := ir.PhysicalOperation{Kind: ir.PhysicalExpressionLetOp, ExpressionLet: &ir.PhysicalExpressionLet{Variable: name, Expression: expr}}
	plan.Operations = append(plan.Operations, ir.PhysicalOperation{})
	copy(plan.Operations[index+1:], plan.Operations[index:])
	plan.Operations[index] = op
}

func operationNames(operations []ir.PhysicalOperation) map[string]bool {
	got := map[string]bool{}
	for _, op := range operations {
		if op.ExpressionLet != nil {
			got[op.ExpressionLet.Variable] = true
		}
	}
	return got
}

func TestPrunePreviewPartitionPlanDropsUnusedPureLetChain(t *testing.T) {
	plan := livenessTestPlan(t)
	root := plan.Operations[0].RootScan.Variable
	for i := 0; i < 1000; i++ {
		insertExpressionLet(&plan, fmt.Sprintf("unused_%04d", i), selectorExpression(root, "id"))
	}
	before := len(plan.Operations)
	pruned, ok := prunePreviewPartitionPlan(plan)
	if !ok {
		t.Fatal("valid source row plan was not analyzable")
	}
	if len(pruned.Operations) != before-1000 {
		t.Fatalf("operation count=%d, want %d", len(pruned.Operations), before-1000)
	}
	if len(plan.Operations) != before {
		t.Fatal("input plan was mutated")
	}
	if err := pruned.Validate(); err != nil {
		t.Fatalf("pruned plan invalid: %v", err)
	}
}

func TestPrunePreviewPartitionPlanKeepsTransitiveLiveLets(t *testing.T) {
	plan := livenessTestPlan(t)
	root := plan.Operations[0].RootScan.Variable
	insertExpressionLet(&plan, "first", selectorExpression(root, "id"))
	insertExpressionLet(&plan, "second", selectorExpression("first", "value"))
	terminal := plan.Operations[len(plan.Operations)-1].Return
	terminal.Projections = append(terminal.Projections, ir.PhysicalProjection{Name: "live_value", Value: ir.PhysicalValue{Variable: "second"}})
	pruned, ok := prunePreviewPartitionPlan(plan)
	if !ok {
		t.Fatal("valid source row plan was not analyzable")
	}
	if names := operationNames(pruned.Operations); !names["first"] || !names["second"] {
		t.Fatalf("live producers removed: %v", names)
	}
}

func TestPrunePreviewPartitionPlanKeepsMayFailProducerAndMandatoryFilters(t *testing.T) {
	plan := livenessTestPlan(t)
	mayFail := ir.PhysicalExpression{Kind: ir.PhysicalCallExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Call: &ir.PhysicalCall{Name: "assert", Args: []ir.PhysicalExpression{
		{Kind: ir.PhysicalLiteralExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Literal: &ir.PhysicalLiteral{BindKey: "assert_condition"}},
		{Kind: ir.PhysicalLiteralExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Literal: &ir.PhysicalLiteral{BindKey: "assert_message"}},
	}}}
	plan.BindVars["assert_condition"] = true
	plan.BindVars["assert_message"] = "bad"
	insertExpressionLet(&plan, "may_fail", mayFail)
	beforeFilters := 0
	for _, op := range plan.Operations {
		if op.Kind == ir.PhysicalFilterOp {
			beforeFilters++
		}
	}
	pruned, ok := prunePreviewPartitionPlan(plan)
	if !ok {
		t.Fatal("valid source row plan was not analyzable")
	}
	if !operationNames(pruned.Operations)["may_fail"] {
		t.Fatal("unused may-fail producer was removed")
	}
	afterFilters := 0
	for _, op := range pruned.Operations {
		if op.Kind == ir.PhysicalFilterOp {
			afterFilters++
		}
	}
	if afterFilters != beforeFilters {
		t.Fatalf("filters changed: %d -> %d", beforeFilters, afterFilters)
	}
}

func TestPrunePreviewPartitionPlanKeepsComplexProducerButPrunesSafeSiblings(t *testing.T) {
	plan := livenessTestPlan(t)
	root := plan.Operations[0].RootScan.Variable
	complex := ir.PhysicalExpression{
		Kind: ir.PhysicalCallExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Call: &ir.PhysicalCall{Name: "concat", Args: []ir.PhysicalExpression{
			selectorExpression(root, "id"),
			{Kind: ir.PhysicalLiteralExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Literal: &ir.PhysicalLiteral{BindKey: "suffix"}},
		}},
	}
	plan.BindVars["suffix"] = "-x"
	insertExpressionLet(&plan, "complex", complex)
	insertExpressionLet(&plan, "simple_unused", selectorExpression(root, "id"))
	pruned, ok := prunePreviewPartitionPlan(plan)
	if !ok {
		t.Fatal("valid plan with a supported complex expression was not analyzable")
	}
	names := operationNames(pruned.Operations)
	if !names["complex"] {
		t.Fatal("complex producer was removed")
	}
	if names["simple_unused"] {
		t.Fatal("independent safe producer was retained")
	}
}

func TestPrunePreviewPartitionPlanDeclinesUnsupportedShape(t *testing.T) {
	plan := livenessTestPlan(t)
	plan.StageSequence = &ir.PhysicalStageSequence{}
	if _, ok := prunePreviewPartitionPlan(plan); ok {
		t.Fatal("stage sequence unexpectedly accepted")
	}
}
