package compiler

import "github.com/calypr/loom/internal/dataframe/semantic"

type (
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
	BuildSemanticPlan      = semantic.BuildSemanticPlan
	ValidateSemanticGraph  = semantic.ValidateSemanticGraph
	NormalizeSelectionPlan = semantic.NormalizeSelectionPlan
	ResolveSemanticField   = semantic.ResolveSemanticField
)
