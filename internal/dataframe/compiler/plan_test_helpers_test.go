package compiler

import (
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func buildGenericPhysicalPlan(plan semantic.OutputPlan) (ir.PhysicalPlan, error) {
	return lower.BuildGenericPhysicalPlanWithPolicy(plan, semantic.ExecutionContext{Project: "p"}, ir.DefaultPhysicalOptimizationPolicy())
}

func buildGenericPhysicalPlanWithContext(plan semantic.OutputPlan, context semantic.ExecutionContext) (ir.PhysicalPlan, error) {
	return lower.BuildGenericPhysicalPlanWithPolicy(plan, context, ir.DefaultPhysicalOptimizationPolicy())
}

func testSemanticField(name string, selector spec.Selector, projection spec.ProjectionMode) semantic.SemanticField {
	return semantic.SemanticField{
		Name:       name,
		Expr:       semantic.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: selector.CanonicalPath()})},
		Projection: projection,
	}
}

func testSemanticFieldWithFallback(name string, selector spec.Selector, fallback spec.Selector) semantic.SemanticField {
	field := testSemanticField(name, selector, "")
	field.Fallbacks = []semantic.SemanticExpression{{Expression: expression.Select(expression.SelectorRef{Path: fallback.CanonicalPath()})}}
	return field
}
