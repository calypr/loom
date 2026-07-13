package lower

import (
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
)

type (
	SemanticPlan                  = semantic.SemanticPlan
	SemanticNode                  = semantic.SemanticNode
	SemanticField                 = semantic.SemanticField
	SemanticPivot                 = semantic.SemanticPivot
	SemanticAggregate             = semantic.SemanticAggregate
	SemanticSlice                 = semantic.SemanticSlice
	SelectionSemanticSpec         = semantic.SelectionSemanticSpec
	Builder                       = spec.Builder
	FieldSelect                   = spec.FieldSelect
	TypedFilter                   = spec.TypedFilter
	Selector                      = spec.Selector
	FilterValue                   = spec.FilterValue
	PhysicalPlan                  = ir.PhysicalPlan
	PhysicalSource                = ir.PhysicalSource
	PhysicalOperation             = ir.PhysicalOperation
	PhysicalOperationKind         = ir.PhysicalOperationKind
	PhysicalRootScan              = ir.PhysicalRootScan
	PhysicalTraversal             = ir.PhysicalTraversal
	PhysicalTraversalDirection    = ir.PhysicalTraversalDirection
	PhysicalTraversalStrategy     = ir.PhysicalTraversalStrategy
	PhysicalValue                 = ir.PhysicalValue
	PhysicalCardinality           = ir.PhysicalCardinality
	PhysicalNullBehavior          = ir.PhysicalNullBehavior
	PhysicalExpression            = ir.PhysicalExpression
	PhysicalExtract               = ir.PhysicalExtract
	PhysicalPreparedSet           = ir.PhysicalPreparedSet
	PhysicalPreparedField         = ir.PhysicalPreparedField
	PhysicalPreparedReference     = ir.PhysicalPreparedReference
	PhysicalAggregate             = ir.PhysicalAggregate
	PhysicalPivotMap              = ir.PhysicalPivotMap
	PhysicalSlice                 = ir.PhysicalSlice
	PhysicalExpressionProjection  = ir.PhysicalExpressionProjection
	PhysicalObject                = ir.PhysicalObject
	PhysicalSet                   = ir.PhysicalSet
	PhysicalSetProjection         = ir.PhysicalSetProjection
	PhysicalSetProjectionField    = ir.PhysicalSetProjectionField
	PhysicalSetOutput             = ir.PhysicalSetOutput
	PhysicalSetOutputField        = ir.PhysicalSetOutputField
	PhysicalSubplan               = ir.PhysicalSubplan
	PhysicalPredicate             = ir.PhysicalPredicate
	PhysicalPredicateExpression   = ir.PhysicalPredicateExpression
	PhysicalFilter                = ir.PhysicalFilter
	PhysicalDerivedLet            = ir.PhysicalDerivedLet
	PhysicalProjection            = ir.PhysicalProjection
	PhysicalReturn                = ir.PhysicalReturn
	PhysicalPredicateKind         = ir.PhysicalPredicateKind
	PhysicalAggregateOperation    = ir.PhysicalAggregateOperation
	PhysicalSelectorExecutionMode = ir.PhysicalSelectorExecutionMode
	PhysicalOptimizationPolicy    = ir.PhysicalOptimizationPolicy
)

const (
	PhysicalRootScanOp                        = ir.PhysicalRootScanOp
	PhysicalTraversalOp                       = ir.PhysicalTraversalOp
	PhysicalFilterOp                          = ir.PhysicalFilterOp
	PhysicalDerivedLetOp                      = ir.PhysicalDerivedLetOp
	PhysicalSetOp                             = ir.PhysicalSetOp
	PhysicalScalarCardinality                 = ir.PhysicalScalarCardinality
	PhysicalArrayCardinality                  = ir.PhysicalArrayCardinality
	PhysicalObjectCardinality                 = ir.PhysicalObjectCardinality
	PhysicalPreserveNull                      = ir.PhysicalPreserveNull
	PhysicalOmitNulls                         = ir.PhysicalOmitNulls
	PhysicalEmptyOnNull                       = ir.PhysicalEmptyOnNull
	PhysicalValueExpression                   = ir.PhysicalValueExpression
	PhysicalExtractExpression                 = ir.PhysicalExtractExpression
	PhysicalAggregateExpression               = ir.PhysicalAggregateExpression
	PhysicalPivotExpression                   = ir.PhysicalPivotExpression
	PhysicalSliceExpression                   = ir.PhysicalSliceExpression
	PhysicalObjectExpression                  = ir.PhysicalObjectExpression
	PhysicalSelectorGeneric                   = ir.PhysicalSelectorGeneric
	PhysicalSelectorDirectScalar              = ir.PhysicalSelectorDirectScalar
	PhysicalSelectorConditionalArray          = ir.PhysicalSelectorConditionalArray
	PhysicalInbound                           = ir.PhysicalInbound
	PhysicalOutbound                          = ir.PhysicalOutbound
	PhysicalTraversalNative                   = ir.PhysicalTraversalNative
	PhysicalTraversalEndpointLookup           = ir.PhysicalTraversalEndpointLookup
	PhysicalCountAggregate                    = ir.PhysicalCountAggregate
	PhysicalCountDistinctAggregate            = ir.PhysicalCountDistinctAggregate
	PhysicalExistsAggregate                   = ir.PhysicalExistsAggregate
	PhysicalDistinctValuesAggregate           = ir.PhysicalDistinctValuesAggregate
	PhysicalMinAggregate                      = ir.PhysicalMinAggregate
	PhysicalMaxAggregate                      = ir.PhysicalMaxAggregate
	PhysicalFirstAggregate                    = ir.PhysicalFirstAggregate
	PhysicalSetGraphIDField                   = ir.PhysicalSetGraphIDField
	PhysicalSetKeyField                       = ir.PhysicalSetKeyField
	PhysicalSetIDField                        = ir.PhysicalSetIDField
	PhysicalSetResourceTypeField              = ir.PhysicalSetResourceTypeField
	PhysicalSetPayloadField                   = ir.PhysicalSetPayloadField
	PhysicalReturnOp                          = ir.PhysicalReturnOp
	PhysicalComparisonPredicate               = ir.PhysicalComparisonPredicate
	PhysicalExistsPredicate                   = ir.PhysicalExistsPredicate
	PhysicalOptimizationRuleCompactProjection = ir.PhysicalOptimizationRuleCompactProjection
	PhysicalOptimizationRuleEndpointTraversal = ir.PhysicalOptimizationRuleEndpointTraversal
	PhysicalOptimizationRulePreparedSelectors = ir.PhysicalOptimizationRulePreparedSelectors
	ProjectionArray                           = spec.ProjectionArray
	ProjectionScalar                          = spec.ProjectionScalar
	ProjectionFirst                           = spec.ProjectionFirst
	FilterEquals                              = spec.FilterEquals
	FilterIn                                  = spec.FilterIn
	FilterExists                              = spec.FilterExists
	FilterMissing                             = spec.FilterMissing
	ProjectionDistinctArray                   = spec.ProjectionDistinctArray
	FilterString                              = spec.FilterString
	FilterCode                                = spec.FilterCode
	FilterBoolean                             = spec.FilterBoolean
	FilterInteger                             = spec.FilterInteger
	FilterDecimal                             = spec.FilterDecimal
	FilterDate                                = spec.FilterDate
	FilterDateTime                            = spec.FilterDateTime
)

var (
	ValidateSemanticGraph             = semantic.ValidateSemanticGraph
	NormalizeSelectionPlan            = semantic.NormalizeSelectionPlan
	ResolveSemanticField              = semantic.ResolveSemanticField
	ValidateTypedFilterForResource    = spec.ValidateTypedFilterForResource
	ParseSelector                     = spec.ParseSelector
	ValidateGenericPhysicalPlanScope  = ir.ValidateGenericPhysicalPlanScope
	DefaultPhysicalOptimizationPolicy = ir.DefaultPhysicalOptimizationPolicy
)

func estimatePreparedSelectorWork(selectorUseCount int) (int, int, int) {
	return ir.EstimatePreparedSelectorWork(selectorUseCount)
}
