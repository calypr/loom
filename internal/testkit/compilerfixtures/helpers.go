package compilerfixtures

import (
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func BuildGenericPhysicalPlan(plan recipeplan.OutputPlan) (ir.PhysicalPlan, error) {
	return lower.BuildGenericPhysicalPlanWithPolicy(plan, recipeplan.ExecutionContext{Project: "p"}, ir.DefaultPhysicalOptimizationPolicy())
}

func BuildGenericPhysicalPlanWithContext(plan recipeplan.OutputPlan, context recipeplan.ExecutionContext) (ir.PhysicalPlan, error) {
	return lower.BuildGenericPhysicalPlanWithPolicy(plan, context, ir.DefaultPhysicalOptimizationPolicy())
}

func TestSemanticField(name string, selector spec.Selector, projection spec.ProjectionMode) recipeplan.SemanticField {
	return recipeplan.SemanticField{
		Name:       name,
		Expr:       recipeplan.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: selector.CanonicalPath()})},
		Projection: projection,
	}
}

func TestSemanticFieldWithFallback(name string, selector spec.Selector, fallback spec.Selector) recipeplan.SemanticField {
	field := TestSemanticField(name, selector, "")
	field.Fallbacks = []recipeplan.SemanticExpression{{Expression: expression.Select(expression.SelectorRef{Path: fallback.CanonicalPath()})}}
	return field
}
