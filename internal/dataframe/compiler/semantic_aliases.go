package compiler

import "github.com/calypr/loom/internal/dataframe/semantic"

type (
	RecipePlan              = semantic.RecipePlan
	OutputPlan              = semantic.OutputPlan
	SemanticExpression      = semantic.SemanticExpression
	SemanticProjection      = semantic.SemanticProjection
	SemanticDynamicMap      = semantic.SemanticDynamicMap
	RecipePlanExplanation   = semantic.RecipePlanExplanation
	OutputPlanExplanation   = semantic.OutputPlanExplanation
	ExpressionExplanation   = semantic.ExpressionExplanation
	ExpansionExplanation    = semantic.ExpansionExplanation
	ResolvedColumn          = semantic.ResolvedColumn
	ResolvedRecipePlan      = semantic.ResolvedRecipePlan
	SemanticPlan            = semantic.SemanticPlan
	SemanticNode            = semantic.SemanticNode
	SemanticField           = semantic.SemanticField
	SemanticPivot           = semantic.SemanticPivot
	SemanticAggregate       = semantic.SemanticAggregate
	SemanticSlice           = semantic.SemanticSlice
	SemanticPlanExplanation = semantic.SemanticPlanExplanation
	SemanticNodeExplanation = semantic.SemanticNodeExplanation
	SelectionSemanticSpec   = semantic.SelectionSemanticSpec
)

const MaxSemanticTraversalDepth = semantic.MaxSemanticTraversalDepth

var (
	BuildRecipePlan        = semantic.BuildRecipePlan
	ResolveRecipePlan      = semantic.ResolveRecipePlan
	ValidateSemanticGraph  = semantic.ValidateSemanticGraph
	NormalizeSelectionPlan = semantic.NormalizeSelectionPlan
	ResolveSemanticField   = semantic.ResolveSemanticField
)
