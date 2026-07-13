package aql

import (
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

type (
	PhysicalPlan                  = ir.PhysicalPlan
	PhysicalSource                = ir.PhysicalSource
	PhysicalOperationKind         = ir.PhysicalOperationKind
	PhysicalOperation             = ir.PhysicalOperation
	PhysicalRootScan              = ir.PhysicalRootScan
	PhysicalTraversalDirection    = ir.PhysicalTraversalDirection
	PhysicalTraversalStrategy     = ir.PhysicalTraversalStrategy
	PhysicalTraversal             = ir.PhysicalTraversal
	PhysicalValue                 = ir.PhysicalValue
	PhysicalCardinality           = ir.PhysicalCardinality
	PhysicalNullBehavior          = ir.PhysicalNullBehavior
	PhysicalExpressionKind        = ir.PhysicalExpressionKind
	PhysicalSelectorExecutionMode = ir.PhysicalSelectorExecutionMode
	PhysicalExpression            = ir.PhysicalExpression
	PhysicalExtract               = ir.PhysicalExtract
	PhysicalPreparedReference     = ir.PhysicalPreparedReference
	PhysicalPreparedSet           = ir.PhysicalPreparedSet
	PhysicalPreparedField         = ir.PhysicalPreparedField
	PhysicalAggregateOperation    = ir.PhysicalAggregateOperation
	PhysicalAggregate             = ir.PhysicalAggregate
	PhysicalPivotMap              = ir.PhysicalPivotMap
	PhysicalSlice                 = ir.PhysicalSlice
	PhysicalExpressionProjection  = ir.PhysicalExpressionProjection
	PhysicalObject                = ir.PhysicalObject
	PhysicalSet                   = ir.PhysicalSet
	PhysicalSetProjection         = ir.PhysicalSetProjection
	PhysicalSetProjectionField    = ir.PhysicalSetProjectionField
	PhysicalSetOutputField        = ir.PhysicalSetOutputField
	PhysicalSetOutput             = ir.PhysicalSetOutput
	PhysicalSubplan               = ir.PhysicalSubplan
	PhysicalPredicate             = ir.PhysicalPredicate
	PhysicalPredicateKind         = ir.PhysicalPredicateKind
	PhysicalPredicateExpression   = ir.PhysicalPredicateExpression
	PhysicalFilter                = ir.PhysicalFilter
	PhysicalDerivedLet            = ir.PhysicalDerivedLet
	PhysicalSort                  = ir.PhysicalSort
	PhysicalLimit                 = ir.PhysicalLimit
	PhysicalProjection            = ir.PhysicalProjection
	PhysicalReturn                = ir.PhysicalReturn
	Selector                      = spec.Selector
	SelectorStep                  = spec.SelectorStep
	FilterValue                   = spec.FilterValue
	FilterValueKind               = spec.FilterValueKind
)

const (
	FilterString   = spec.FilterString
	FilterCode     = spec.FilterCode
	FilterBoolean  = spec.FilterBoolean
	FilterInteger  = spec.FilterInteger
	FilterDecimal  = spec.FilterDecimal
	FilterDate     = spec.FilterDate
	FilterDateTime = spec.FilterDateTime
	QuantifierAny  = spec.QuantifierAny
	QuantifierAll  = spec.QuantifierAll
	QuantifierNone = spec.QuantifierNone
)

const (
	PhysicalRootScanOp               = ir.PhysicalRootScanOp
	PhysicalTraversalOp              = ir.PhysicalTraversalOp
	PhysicalFilterOp                 = ir.PhysicalFilterOp
	PhysicalDerivedLetOp             = ir.PhysicalDerivedLetOp
	PhysicalSetOp                    = ir.PhysicalSetOp
	PhysicalSortOp                   = ir.PhysicalSortOp
	PhysicalLimitOp                  = ir.PhysicalLimitOp
	PhysicalReturnOp                 = ir.PhysicalReturnOp
	PhysicalInbound                  = ir.PhysicalInbound
	PhysicalOutbound                 = ir.PhysicalOutbound
	PhysicalTraversalNative          = ir.PhysicalTraversalNative
	PhysicalTraversalEndpointLookup  = ir.PhysicalTraversalEndpointLookup
	PhysicalScalarCardinality        = ir.PhysicalScalarCardinality
	PhysicalArrayCardinality         = ir.PhysicalArrayCardinality
	PhysicalObjectCardinality        = ir.PhysicalObjectCardinality
	PhysicalPreserveNull             = ir.PhysicalPreserveNull
	PhysicalOmitNulls                = ir.PhysicalOmitNulls
	PhysicalEmptyOnNull              = ir.PhysicalEmptyOnNull
	PhysicalValueExpression          = ir.PhysicalValueExpression
	PhysicalExtractExpression        = ir.PhysicalExtractExpression
	PhysicalAggregateExpression      = ir.PhysicalAggregateExpression
	PhysicalPivotExpression          = ir.PhysicalPivotExpression
	PhysicalSliceExpression          = ir.PhysicalSliceExpression
	PhysicalObjectExpression         = ir.PhysicalObjectExpression
	PhysicalSelectorGeneric          = ir.PhysicalSelectorGeneric
	PhysicalSelectorDirectScalar     = ir.PhysicalSelectorDirectScalar
	PhysicalSelectorConditionalArray = ir.PhysicalSelectorConditionalArray
	PhysicalCountAggregate           = ir.PhysicalCountAggregate
	PhysicalCountDistinctAggregate   = ir.PhysicalCountDistinctAggregate
	PhysicalExistsAggregate          = ir.PhysicalExistsAggregate
	PhysicalDistinctValuesAggregate  = ir.PhysicalDistinctValuesAggregate
	PhysicalMinAggregate             = ir.PhysicalMinAggregate
	PhysicalMaxAggregate             = ir.PhysicalMaxAggregate
	PhysicalFirstAggregate           = ir.PhysicalFirstAggregate
	PhysicalSetGraphIDField          = ir.PhysicalSetGraphIDField
	PhysicalSetKeyField              = ir.PhysicalSetKeyField
	PhysicalSetIDField               = ir.PhysicalSetIDField
	PhysicalSetResourceTypeField     = ir.PhysicalSetResourceTypeField
	PhysicalSetPayloadField          = ir.PhysicalSetPayloadField
	PhysicalComparisonPredicate      = ir.PhysicalComparisonPredicate
	PhysicalAllPredicate             = ir.PhysicalAllPredicate
	PhysicalAnyPredicate             = ir.PhysicalAnyPredicate
	PhysicalNotPredicate             = ir.PhysicalNotPredicate
	PhysicalExistsPredicate          = ir.PhysicalExistsPredicate
)

const (
	genericPhysicalExecutionLimitBind = "limit"
	datasetGenerationBindKey          = "dataset_generation"
	datasetGenerationField            = "dataset_generation"
)

var ValidateGenericPhysicalPlanScope = ir.ValidateGenericPhysicalPlanScope
