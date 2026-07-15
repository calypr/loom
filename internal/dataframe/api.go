// Package dataframe is Loom's stable dataframe facade.
//
// Runtime orchestration, compiler contracts, and user errors are re-exported
// from their canonical packages here so GraphQL, CLI, and conformance callers
// share one recipe/compiler contract without depending on implementation
// layout.
package dataframe

import (
	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/materialization"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/control"
	"github.com/calypr/loom/internal/dataframe/recipe/engine"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/dataframe/recipe/plan"
	"github.com/calypr/loom/internal/dataframe/runtime"
)

type (
	Service             = runtime.Service
	ServiceConfig       = runtime.ServiceConfig
	ExecuteQueryOptions = runtime.ExecuteQueryOptions
	ValidationWarning   = runtime.ValidationWarning
	ValidationResult    = runtime.ValidationResult
	ValidateRequest     = runtime.ValidateRequest
	RunRequest          = runtime.RunRequest
	Result              = runtime.Result
	QueryDiagnostics    = runtime.QueryDiagnostics
	StreamResult        = runtime.StreamResult

	CompiledQuery           = compiler.CompiledQuery
	SemanticPlan            = compiler.SemanticPlan
	SemanticNode            = compiler.SemanticNode
	SemanticField           = compiler.SemanticField
	SemanticPivot           = compiler.SemanticPivot
	SemanticAggregate       = compiler.SemanticAggregate
	SemanticSlice           = compiler.SemanticSlice
	SemanticPlanExplanation = compiler.SemanticPlanExplanation
	SemanticNodeExplanation = compiler.SemanticNodeExplanation
	SelectionSemanticSpec   = compiler.SelectionSemanticSpec
	RowGrain                = compiler.RowGrain
	ProjectionMode          = compiler.ProjectionMode
	Cardinality             = compiler.Cardinality
	RowIdentity             = compiler.RowIdentity
	TraversalMatchMode      = compiler.TraversalMatchMode
	FilterOperator          = compiler.FilterOperator
	FilterValueKind         = compiler.FilterValueKind
	ArrayQuantifier         = compiler.ArrayQuantifier
	CodeValue               = compiler.CodeValue
	FilterValue             = compiler.FilterValue
	TypedFilter             = compiler.TypedFilter
	Selector                = compiler.Selector
	SelectorStep            = compiler.SelectorStep
	ContainsFilter          = compiler.ContainsFilter

	RecipeBundle             = recipe.Bundle
	RecipeOutput             = recipe.Output
	RecipeRuntimeBindings    = recipe.RuntimeBindings
	RecipeExpression         = recipe.Expression
	RecipeRegistry           = exec.Registry
	RecipeRunner             = exec.Runner
	RecipeResult             = exec.Result
	RecipePlan               = compiler.RecipePlan
	RecipeOutputPlan         = compiler.OutputPlan
	RecipeResolvedPlan       = compiler.ResolvedRecipePlan
	RecipeResolvedColumn     = compiler.ResolvedColumn
	RecipeFrozenSchema       = plan.FrozenSchema
	RecipeDynamicSpec        = plan.DynamicSpec
	RecipeColumnCandidate    = plan.Candidate
	RecipePlanColumn         = plan.Column
	RecipeControlService     = control.Service
	RecipeValidation         = control.Validation
	RecipePreview            = control.Preview
	RecipeEngine             = engine.Engine
	RecipeEngineControl      = engine.Control
	RecipeEngineConfig       = engine.Config
	RecipeEngineResolved     = engine.Resolved
	RecipeOutputStream       = engine.OutputStream
	RecipeEngineStreamResult = engine.StreamResult
	BundleOutput             = materialization.BundleOutput
	AtomicBundleStore        = materialization.AtomicBundleStore
	AtomicBundleTx           = materialization.AtomicBundleTx
	Expression               = expression.Expression
	ExpressionType           = expression.Type
	CheckedExpression        = expression.CheckedExpression

	PhysicalOptimizationPolicy             = compiler.PhysicalOptimizationPolicy
	PhysicalOptimizationRule               = compiler.PhysicalOptimizationRule
	PhysicalOptimizationDecision           = compiler.PhysicalOptimizationDecision
	PhysicalOptimizationReport             = compiler.PhysicalOptimizationReport
	PhysicalOptimizationRuleState          = compiler.PhysicalOptimizationRuleState
	CompilerPlanDiagnostics                = compiler.CompilerPlanDiagnostics
	PhysicalTraversalDecision              = compiler.PhysicalTraversalDecision
	RichSourceReuse                        = compiler.RichSourceReuse
	RichConsumerGroup                      = compiler.RichConsumerGroup
	RenderedPhysicalPlan                   = compiler.RenderedPhysicalPlan
	PhysicalTraversalPrefix                = compiler.PhysicalTraversalPrefix
	PhysicalTraversalSubset                = compiler.PhysicalTraversalSubset
	PhysicalTraversalPrefixDecomposition   = compiler.PhysicalTraversalPrefixDecomposition
	PhysicalTraversalPrefixRejectionReason = compiler.PhysicalTraversalPrefixRejectionReason
	PhysicalTraversalPrefixError           = compiler.PhysicalTraversalPrefixError
	PhysicalPlan                           = compiler.PhysicalPlan
	PhysicalSource                         = compiler.PhysicalSource
	PhysicalOperationKind                  = compiler.PhysicalOperationKind
	PhysicalOperation                      = compiler.PhysicalOperation
	PhysicalRootScan                       = compiler.PhysicalRootScan
	PhysicalTraversalDirection             = compiler.PhysicalTraversalDirection
	PhysicalTraversalStrategy              = compiler.PhysicalTraversalStrategy
	PhysicalTraversal                      = compiler.PhysicalTraversal
	PhysicalValue                          = compiler.PhysicalValue
	PhysicalCardinality                    = compiler.PhysicalCardinality
	PhysicalNullBehavior                   = compiler.PhysicalNullBehavior
	PhysicalExpressionKind                 = compiler.PhysicalExpressionKind
	PhysicalSelectorExecutionMode          = compiler.PhysicalSelectorExecutionMode
	PhysicalExpression                     = compiler.PhysicalExpression
	PhysicalExtract                        = compiler.PhysicalExtract
	PhysicalPreparedReference              = compiler.PhysicalPreparedReference
	PhysicalPreparedSet                    = compiler.PhysicalPreparedSet
	PhysicalPreparedField                  = compiler.PhysicalPreparedField
	PhysicalAggregateOperation             = compiler.PhysicalAggregateOperation
	PhysicalAggregate                      = compiler.PhysicalAggregate
	PhysicalPivotMap                       = compiler.PhysicalPivotMap
	PhysicalSlice                          = compiler.PhysicalSlice
	PhysicalExpressionProjection           = compiler.PhysicalExpressionProjection
	PhysicalObject                         = compiler.PhysicalObject
	PhysicalSet                            = compiler.PhysicalSet
	PhysicalSetProjection                  = compiler.PhysicalSetProjection
	PhysicalSetProjectionField             = compiler.PhysicalSetProjectionField
	PhysicalSetOutputField                 = compiler.PhysicalSetOutputField
	PhysicalSetOutput                      = compiler.PhysicalSetOutput
	PhysicalSubplan                        = compiler.PhysicalSubplan
	PhysicalPredicate                      = compiler.PhysicalPredicate
	PhysicalPredicateKind                  = compiler.PhysicalPredicateKind
	PhysicalPredicateExpression            = compiler.PhysicalPredicateExpression
	PhysicalFilter                         = compiler.PhysicalFilter
	PhysicalDerivedLet                     = compiler.PhysicalDerivedLet
	PhysicalSort                           = compiler.PhysicalSort
	PhysicalLimit                          = compiler.PhysicalLimit
	PhysicalProjection                     = compiler.PhysicalProjection
	PhysicalReturn                         = compiler.PhysicalReturn
	StorageRoute                           = compiler.StorageRoute

	ErrorCode   = dataframeerrors.ErrorCode
	UserError   = dataframeerrors.UserError
	Error       = dataframeerrors.Error
	ErrorOption = dataframeerrors.ErrorOption
)

const (
	MaxSemanticTraversalDepth                  = compiler.MaxSemanticTraversalDepth
	RowGrainResource                           = compiler.RowGrainResource
	RowGrainPatient                            = compiler.RowGrainPatient
	RowGrainSpecimen                           = compiler.RowGrainSpecimen
	RowGrainFile                               = compiler.RowGrainFile
	RowGrainDiagnosis                          = compiler.RowGrainDiagnosis
	RowGrainObservation                        = compiler.RowGrainObservation
	RowGrainStudyEnrollment                    = compiler.RowGrainStudyEnrollment
	ProjectionScalar                           = compiler.ProjectionScalar
	ProjectionFirst                            = compiler.ProjectionFirst
	ProjectionArray                            = compiler.ProjectionArray
	ProjectionDistinctArray                    = compiler.ProjectionDistinctArray
	ProjectionAggregate                        = compiler.ProjectionAggregate
	ProjectionPivot                            = compiler.ProjectionPivot
	ProjectionExplode                          = compiler.ProjectionExplode
	CardinalityRequiredOne                     = compiler.CardinalityRequiredOne
	CardinalityOptionalOne                     = compiler.CardinalityOptionalOne
	CardinalityMany                            = compiler.CardinalityMany
	CardinalityUnknownObservedMany             = compiler.CardinalityUnknownObservedMany
	TraversalMatchOptional                     = compiler.TraversalMatchOptional
	TraversalMatchRequired                     = compiler.TraversalMatchRequired
	FilterEquals                               = compiler.FilterEquals
	FilterNotEquals                            = compiler.FilterNotEquals
	FilterIn                                   = compiler.FilterIn
	FilterExists                               = compiler.FilterExists
	FilterMissing                              = compiler.FilterMissing
	FilterContains                             = compiler.FilterContains
	FilterGreaterThan                          = compiler.FilterGreaterThan
	FilterGreaterEq                            = compiler.FilterGreaterEq
	FilterLessThan                             = compiler.FilterLessThan
	FilterLessEq                               = compiler.FilterLessEq
	FilterString                               = compiler.FilterString
	FilterCode                                 = compiler.FilterCode
	FilterBoolean                              = compiler.FilterBoolean
	FilterInteger                              = compiler.FilterInteger
	FilterDecimal                              = compiler.FilterDecimal
	FilterDate                                 = compiler.FilterDate
	FilterDateTime                             = compiler.FilterDateTime
	QuantifierAny                              = compiler.QuantifierAny
	QuantifierAll                              = compiler.QuantifierAll
	QuantifierNone                             = compiler.QuantifierNone
	PhysicalTraversalNative                    = compiler.PhysicalTraversalNative
	PhysicalTraversalEndpointLookup            = compiler.PhysicalTraversalEndpointLookup
	PhysicalOutbound                           = compiler.PhysicalOutbound
	PhysicalInbound                            = compiler.PhysicalInbound
	PhysicalAny                                = compiler.PhysicalAny
	PhysicalRootScanOp                         = compiler.PhysicalRootScanOp
	PhysicalTraversalOp                        = compiler.PhysicalTraversalOp
	PhysicalFilterOp                           = compiler.PhysicalFilterOp
	PhysicalDerivedLetOp                       = compiler.PhysicalDerivedLetOp
	PhysicalSetOp                              = compiler.PhysicalSetOp
	PhysicalSortOp                             = compiler.PhysicalSortOp
	PhysicalLimitOp                            = compiler.PhysicalLimitOp
	PhysicalReturnOp                           = compiler.PhysicalReturnOp
	PhysicalScalarCardinality                  = compiler.PhysicalScalarCardinality
	PhysicalArrayCardinality                   = compiler.PhysicalArrayCardinality
	PhysicalObjectCardinality                  = compiler.PhysicalObjectCardinality
	PhysicalPreserveNull                       = compiler.PhysicalPreserveNull
	PhysicalOmitNulls                          = compiler.PhysicalOmitNulls
	PhysicalEmptyOnNull                        = compiler.PhysicalEmptyOnNull
	PhysicalValueExpression                    = compiler.PhysicalValueExpression
	PhysicalExtractExpression                  = compiler.PhysicalExtractExpression
	PhysicalAggregateExpression                = compiler.PhysicalAggregateExpression
	PhysicalPivotExpression                    = compiler.PhysicalPivotExpression
	PhysicalSliceExpression                    = compiler.PhysicalSliceExpression
	PhysicalObjectExpression                   = compiler.PhysicalObjectExpression
	PhysicalSelectorGeneric                    = compiler.PhysicalSelectorGeneric
	PhysicalSelectorDirectScalar               = compiler.PhysicalSelectorDirectScalar
	PhysicalSelectorConditionalArray           = compiler.PhysicalSelectorConditionalArray
	PhysicalCountAggregate                     = compiler.PhysicalCountAggregate
	PhysicalCountDistinctAggregate             = compiler.PhysicalCountDistinctAggregate
	PhysicalExistsAggregate                    = compiler.PhysicalExistsAggregate
	PhysicalDistinctValuesAggregate            = compiler.PhysicalDistinctValuesAggregate
	PhysicalMinAggregate                       = compiler.PhysicalMinAggregate
	PhysicalMaxAggregate                       = compiler.PhysicalMaxAggregate
	PhysicalFirstAggregate                     = compiler.PhysicalFirstAggregate
	PhysicalSetGraphIDField                    = compiler.PhysicalSetGraphIDField
	PhysicalSetKeyField                        = compiler.PhysicalSetKeyField
	PhysicalSetIDField                         = compiler.PhysicalSetIDField
	PhysicalSetResourceTypeField               = compiler.PhysicalSetResourceTypeField
	PhysicalSetPayloadField                    = compiler.PhysicalSetPayloadField
	PhysicalComparisonPredicate                = compiler.PhysicalComparisonPredicate
	PhysicalAllPredicate                       = compiler.PhysicalAllPredicate
	PhysicalAnyPredicate                       = compiler.PhysicalAnyPredicate
	PhysicalNotPredicate                       = compiler.PhysicalNotPredicate
	PhysicalExistsPredicate                    = compiler.PhysicalExistsPredicate
	PhysicalPrefixNotOptionalSet               = compiler.PhysicalPrefixNotOptionalSet
	PhysicalPrefixSharedSubset                 = compiler.PhysicalPrefixSharedSubset
	PhysicalPrefixInvalidCapture               = compiler.PhysicalPrefixInvalidCapture
	PhysicalPrefixMissingTraversal             = compiler.PhysicalPrefixMissingTraversal
	PhysicalPrefixUnsupportedDirection         = compiler.PhysicalPrefixUnsupportedDirection
	PhysicalPrefixInvalidRoute                 = compiler.PhysicalPrefixInvalidRoute
	PhysicalPrefixInvalidScope                 = compiler.PhysicalPrefixInvalidScope
	PhysicalPrefixInvalidTarget                = compiler.PhysicalPrefixInvalidTarget
	PhysicalOptimizationRuleTraversalSharing   = compiler.PhysicalOptimizationRuleTraversalSharing
	PhysicalOptimizationRulePreparedSelectors  = compiler.PhysicalOptimizationRulePreparedSelectors
	PhysicalOptimizationRuleNestedSharing      = compiler.PhysicalOptimizationRuleNestedSharing
	PhysicalOptimizationRuleRichConsumerFusion = compiler.PhysicalOptimizationRuleRichConsumerFusion
	PhysicalOptimizationRuleCompactProjection  = compiler.PhysicalOptimizationRuleCompactProjection
	PhysicalOptimizationRuleEndpointTraversal  = compiler.PhysicalOptimizationRuleEndpointTraversal
	OptimizerRuleFilterPushdown                = compiler.OptimizerRuleFilterPushdown
	OptimizerRuleTraversalSharing              = compiler.OptimizerRuleTraversalSharing
	OptimizerRuleRelationshipSemiJoin          = compiler.OptimizerRuleRelationshipSemiJoin
	CodeProjectRequired                        = dataframeerrors.CodeProjectRequired
	CodeRootResourceTypeRequired               = dataframeerrors.CodeRootResourceTypeRequired
	CodeUnauthorizedProject                    = dataframeerrors.CodeUnauthorizedProject
	CodeUnknownField                           = dataframeerrors.CodeUnknownField
	CodeFieldNotPopulated                      = dataframeerrors.CodeFieldNotPopulated
	CodeInvalidTraversal                       = dataframeerrors.CodeInvalidTraversal
	CodeUnsafeTraversalRoute                   = dataframeerrors.CodeUnsafeTraversalRoute
	CodeInvalidFilter                          = dataframeerrors.CodeInvalidFilter
	CodeUnboundedPivot                         = dataframeerrors.CodeUnboundedPivot
	CodeInvalidPivotColumn                     = dataframeerrors.CodeInvalidPivotColumn
	CodeInvalidSlice                           = dataframeerrors.CodeInvalidSlice
	CodePlanTooExpensive                       = dataframeerrors.CodePlanTooExpensive
	CodeInvalidCursor                          = dataframeerrors.CodeInvalidCursor
	CodeStaleCursor                            = dataframeerrors.CodeStaleCursor
	CodeDatasetGenerationChanged               = dataframeerrors.CodeDatasetGenerationChanged
	CodeUnsupportedExportFormat                = dataframeerrors.CodeUnsupportedExportFormat
	CodeClientCanceled                         = dataframeerrors.CodeClientCanceled
	CodeBackendUnavailable                     = dataframeerrors.CodeBackendUnavailable
	CodeInternalError                          = dataframeerrors.CodeInternalError
)

var (
	NewService                          = runtime.NewService
	ExecuteQueryRows                    = runtime.ExecuteQueryRows
	ExplainCompiledQuery                = runtime.ExplainCompiledQuery
	ProfileCompiledQuery                = runtime.ProfileCompiledQuery
	DefaultPhysicalOptimizationPolicy   = compiler.DefaultPhysicalOptimizationPolicy
	ValidateSemanticGraph               = compiler.ValidateSemanticGraph
	BuildPhysicalPlan                   = compiler.BuildPhysicalPlan
	BuildPhysicalPlanWithPolicy         = compiler.BuildPhysicalPlanWithPolicy
	BuildGenericPhysicalPlan            = compiler.BuildGenericPhysicalPlan
	BuildGenericPhysicalPlanWithPolicy  = compiler.BuildGenericPhysicalPlanWithPolicy
	OptimizePhysicalPlan                = compiler.OptimizePhysicalPlan
	OptimizePhysicalPlanWithPolicy      = compiler.OptimizePhysicalPlanWithPolicy
	RenderPhysicalPlan                  = compiler.RenderPhysicalPlan
	ParseSelector                       = compiler.ParseSelector
	ParseRecipe                         = recipe.Parse
	BuildRecipePlan                     = compiler.BuildRecipePlan
	CompileResolvedRecipePlanWithPolicy = compiler.CompileResolvedRecipePlanWithPolicy
	ResolveRecipePlan                   = compiler.ResolveRecipePlan
	NewRecipeControlService             = func(registry control.Registry) control.Service {
		return control.Service{Registry: registry}
	}
	NewRecipeEngine                  = engine.New
	PublishRecipeBundle              = materialization.PublishBundle
	NewRecipeRegistry                = exec.NewRegistry
	ValidateTypedFilterForResource   = compiler.ValidateTypedFilterForResource
	NormalizeSelectionPlan           = compiler.NormalizeSelectionPlan
	ResolveSemanticField             = compiler.ResolveSemanticField
	InferRowGrain                    = compiler.InferRowGrain
	RootResourceForGrain             = compiler.RootResourceForGrain
	ValidateRootGrain                = compiler.ValidateRootGrain
	DefaultRowIdentity               = compiler.DefaultRowIdentity
	ValidateProjection               = compiler.ValidateProjection
	OperatorSupportsKind             = compiler.OperatorSupportsKind
	ValidateGenericPhysicalPlanScope = compiler.ValidateGenericPhysicalPlanScope
	DecomposePhysicalTraversalPrefix = compiler.DecomposePhysicalTraversalPrefix
	ResolveStorageRoute              = compiler.ResolveStorageRoute
	AsUserError                      = dataframeerrors.AsUserError
	Normalize                        = dataframeerrors.Normalize
	PublicMessage                    = dataframeerrors.PublicMessage
	NewError                         = dataframeerrors.NewError
	Wrap                             = dataframeerrors.Wrap
	WithFieldPath                    = dataframeerrors.WithFieldPath
	WithDetails                      = dataframeerrors.WithDetails
	WithRetryable                    = dataframeerrors.WithRetryable
	WithCause                        = dataframeerrors.WithCause
	IsUserCorrectable                = dataframeerrors.IsUserCorrectable
	IsRetryableCode                  = dataframeerrors.IsRetryableCode
	IsOperatorFailure                = dataframeerrors.IsOperatorFailure
	Errorf                           = dataframeerrors.Errorf
	AllErrorCodes                    = dataframeerrors.AllErrorCodes
	ErrBackendUnavailable            = dataframeerrors.ErrBackendUnavailable
	ErrClientCanceled                = dataframeerrors.ErrClientCanceled
)
