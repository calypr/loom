package compiler

import "github.com/calypr/loom/internal/dataframe/semantic"

type (
	RecipePlan              = semantic.RecipePlan
	OutputPlan              = semantic.OutputPlan
	SemanticExpression      = semantic.SemanticExpression
	SemanticProjection      = semantic.SemanticProjection
	SemanticExpansion       = semantic.SemanticExpansion
	SemanticDynamicMap      = semantic.SemanticDynamicMap
	RecipePlanExplanation   = semantic.RecipePlanExplanation
	OutputPlanExplanation   = semantic.OutputPlanExplanation
	ExpressionExplanation   = semantic.ExpressionExplanation
	ExpansionExplanation    = semantic.ExpansionExplanation
	ResolvedColumn          = semantic.ResolvedColumn
	ResolvedRecipePlan      = semantic.ResolvedRecipePlan
	DiscoveryCandidate      = semantic.DiscoveryCandidate
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
	BuildRecipePlan            = semantic.BuildRecipePlan
	BuildRecipePlanFromBuilder = semantic.BuildRecipePlanFromBuilder
	ResolveRecipePlan          = semantic.ResolveRecipePlan
	BuildSemanticPlan          = semantic.BuildSemanticPlan
	ValidateSemanticGraph      = semantic.ValidateSemanticGraph
	NormalizeSelectionPlan     = semantic.NormalizeSelectionPlan
	ResolveSemanticField       = semantic.ResolveSemanticField
)
