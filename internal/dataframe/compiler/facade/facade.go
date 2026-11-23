package facade

import (
	compilerpkg "github.com/calypr/loom/internal/dataframe/compiler"
	capability "github.com/calypr/loom/internal/dataframe/compiler/capability"
	"github.com/calypr/loom/internal/dataframe/compiler/celltrace"
	ir "github.com/calypr/loom/internal/dataframe/compiler/ir"
	lower "github.com/calypr/loom/internal/dataframe/compiler/lower"
	lowerphysical "github.com/calypr/loom/internal/dataframe/compiler/lower/physical"
	aql "github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/compiler/rowlineage"
)

type CategoryScanProof = compilerpkg.CategoryScanProof
type CategoryScanRefusal = compilerpkg.CategoryScanRefusal

var CategoryScanRefusalCodeOf = compilerpkg.CategoryScanRefusalCodeOf

const CategoryScanStageUnknown = compilerpkg.CategoryScanStageUnknown

var CompileCategoryScanStageRequiringValue = compilerpkg.CompileCategoryScanStageRequiringValue
var CompileRecipeOutputPageWithPolicy = compilerpkg.CompileRecipeOutputPageWithPolicy
var CompileRecipeOutputWithPolicy = compilerpkg.CompileRecipeOutputWithPolicy

const DefaultRowLineageLimit = rowlineage.DefaultRowLineageLimit
const MaxCategoryScanValues = compilerpkg.MaxCategoryScanValues
const MaxCellTraceContributions = celltrace.MaxCellTraceContributions
const MaxColumnPopulationBatch = compilerpkg.MaxColumnPopulationBatch
const MaxRowLineageContributors = rowlineage.MaxRowLineageContributors
const RootPageAfterKeyBind = compilerpkg.RootPageAfterKeyBind
const RootPageKeysBind = compilerpkg.RootPageKeysBind

type RowLineageCapability = rowlineage.RowLineageCapability
type RowLineageUnsupportedError = rowlineage.RowLineageUnsupportedError

var BindGroupRelatedOccurrenceSource = lower.BindGroupRelatedOccurrenceSource
var CompileResolvedRecipePlan = lower.CompileResolvedRecipePlan

type CompiledGroupFieldOwner = lower.CompiledGroupFieldOwner
type CompiledGroupFieldSourceContext = lower.CompiledGroupFieldSourceContext
type CompiledOutputColumn = lower.CompiledOutputColumn
type CompiledRecipe = lower.CompiledRecipe
type CompiledRecipeOutput = lower.CompiledRecipeOutput
type CompiledSourceOrigin = lower.CompiledSourceOrigin
type CompiledStageDescriptor = lower.CompiledStageDescriptor

var ConstructionCombineColumnType = lower.ConstructionCombineColumnType
var DescribeConstructionSourceStage = lower.DescribeConstructionSourceStage
var NativePivotColumnName = lowerphysical.NativePivotColumnName
var NativeTraversalColumnPrefix = lowerphysical.NativeTraversalColumnPrefix

type PivotReducerTypeError = lower.PivotReducerTypeError
type RelatedEligibilityAnchorError = lower.RelatedEligibilityAnchorError
type RelatedSourceAnchorError = lower.RelatedSourceAnchorError

var ResolveGroupRelatedOccurrenceSource = lower.ResolveGroupRelatedOccurrenceSource
var TableReshapeScalarKind = lower.TableReshapeScalarKind

var CanonicalExecutionPhysicalPlan = ir.CanonicalExecutionPhysicalPlan
var MarshalLegacyPhysical = ir.MarshalLegacyPhysical
var ClickHouseCombineScalarBaseType = ir.ClickHouseCombineScalarBaseType
var DefaultPhysicalOptimizationPolicy = ir.DefaultPhysicalOptimizationPolicy

const PhysicalCallExpression = ir.PhysicalCallExpression
const PhysicalCombineAppend = ir.PhysicalCombineAppend
const PhysicalCombineKeyJoin = ir.PhysicalCombineKeyJoin

type PhysicalOptimizationDecision = ir.PhysicalOptimizationDecision
type PhysicalPlan = ir.PhysicalPlan
type PhysicalProjection = ir.PhysicalProjection

const PhysicalReturnOp = ir.PhysicalReturnOp
const PhysicalUnnestOp = ir.PhysicalUnnestOp
const PreviewSourceResourceIDColumn = ir.PreviewSourceResourceIDColumn

type ResolvedClickHouseTable = ir.ResolvedClickHouseTable

type CandidateRequest = capability.CandidateRequest
type OwnerRecordsRequest = capability.OwnerRecordsRequest

var ProbeCandidate = capability.ProbeCandidate
var ProbeOwnerRecords = capability.ProbeOwnerRecords
var ProbeProjection = capability.ProbeProjection
var ProbeRoot = capability.ProbeRoot
var ProbeTraversal = capability.ProbeTraversal

type RootRequest = capability.RootRequest
type SchemaFieldDescriptor = capability.SchemaFieldDescriptor

var SchemaFieldDescriptors = capability.SchemaFieldDescriptors

type Scope = capability.Scope
type Traversal = capability.Traversal
type TraversalRequest = capability.TraversalRequest

var RenderPhysicalPlan = aql.RenderPhysicalPlan

type RenderedPhysicalPlan = aql.RenderedPhysicalPlan
