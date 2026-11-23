package authoringv2

import ws "github.com/calypr/loom/internal/explorer/workspace"

const APIVersion = ws.APIVersion

type Action = ws.Action
type ActionColumn = ws.ActionColumn
type AggregateSource = ws.AggregateSource
type AggregateTransformationCapabilities = ws.AggregateTransformationCapabilities

var AggregateTransformationCapabilitiesForCapability = ws.AggregateTransformationCapabilitiesForCapability
var AggregateTransformationCapabilitiesForCatalog = ws.AggregateTransformationCapabilitiesForCatalog
var AggregateTransformationCapabilitiesForCatalogCandidates = ws.AggregateTransformationCapabilitiesForCatalogCandidates

type Artifact = ws.Artifact
type AuthoringNetwork = ws.AuthoringNetwork
type BuilderState = ws.BuilderState

var BytesDigest = ws.BytesDigest
var CanPopulateConstructionRows = ws.CanPopulateConstructionRows
var CanonicalStoredWorkspaceForMigration = ws.CanonicalStoredWorkspaceForMigration

type CatalogAvailability = ws.CatalogAvailability
type CatalogAvailabilityReadiness = ws.CatalogAvailabilityReadiness
type CatalogAvailabilitySourceReadiness = ws.CatalogAvailabilitySourceReadiness
type CatalogCandidate = ws.CatalogCandidate
type CatalogDiagnostic = ws.CatalogDiagnostic
type CatalogEdge = ws.CatalogEdge

var CatalogEdgeByID = ws.CatalogEdgeByID
var CatalogFromCapability = ws.CatalogFromCapability

const CatalogKind = ws.CatalogKind

type CatalogNode = ws.CatalogNode

var CatalogNodeByID = ws.CatalogNodeByID
var CatalogNodeForConstructionRoot = ws.CatalogNodeForConstructionRoot

type CatalogObservedField = ws.CatalogObservedField
type CatalogObservedRelationship = ws.CatalogObservedRelationship
type CatalogObservedResource = ws.CatalogObservedResource
type CatalogSnapshot = ws.CatalogSnapshot
type ChartPresentation = ws.ChartPresentation

var CloneCurrentWorkspace = ws.CloneCurrentWorkspace
var CloneFrameRoute = ws.CloneFrameRoute
var CloneWorkspace = ws.CloneWorkspace

type CodeValue = ws.CodeValue
type Column = ws.Column
type ColumnOutput = ws.ColumnOutput
type ColumnSource = ws.ColumnSource
type ColumnValueTransformationCapabilities = ws.ColumnValueTransformationCapabilities

var ColumnValueTransformationCapabilitiesForCatalog = ws.ColumnValueTransformationCapabilitiesForCatalog
var CommandGeneratedID = ws.CommandGeneratedID

type ConceptCandidate = ws.ConceptCandidate
type Construction = ws.Construction

var ConstructionCanCarrySourceProjection = ws.ConstructionCanCarrySourceProjection

type ConstructionCodedGroup = ws.ConstructionCodedGroup
type ConstructionCodedGroupSource = ws.ConstructionCodedGroupSource
type ConstructionCodedPivot = ws.ConstructionCodedPivot
type ConstructionCodedPivotCategory = ws.ConstructionCodedPivotCategory
type ConstructionCodedPivotSource = ws.ConstructionCodedPivotSource

const ConstructionColumnOperand = ws.ConstructionColumnOperand

type ConstructionCombine = ws.ConstructionCombine

const ConstructionCombineAppend = ws.ConstructionCombineAppend
const ConstructionCombineExcludeMatches = ws.ConstructionCombineExcludeMatches
const ConstructionCombineIncludeMatches = ws.ConstructionCombineIncludeMatches
const ConstructionCombineInnerJoin = ws.ConstructionCombineInnerJoin

type ConstructionCombineJoinType = ws.ConstructionCombineJoinType
type ConstructionCombineKey = ws.ConstructionCombineKey

const ConstructionCombineKeyJoin = ws.ConstructionCombineKeyJoin

type ConstructionCombineKind = ws.ConstructionCombineKind

const ConstructionCombineLeftJoin = ws.ConstructionCombineLeftJoin
const ConstructionCombineMembership = ws.ConstructionCombineMembership

type ConstructionCombineMembershipMode = ws.ConstructionCombineMembershipMode

const ConstructionCombinePreserveAllMatches = ws.ConstructionCombinePreserveAllMatches

type ConstructionCombineProjection = ws.ConstructionCombineProjection
type ConstructionCombineRightMatchPolicy = ws.ConstructionCombineRightMatchPolicy
type ConstructionDependencyIssue = ws.ConstructionDependencyIssue
type ConstructionDerive = ws.ConstructionDerive

const ConstructionDerivedAdd = ws.ConstructionDerivedAdd
const ConstructionDerivedDivide = ws.ConstructionDerivedDivide
const ConstructionDerivedMultiply = ws.ConstructionDerivedMultiply

type ConstructionDerivedOperation = ws.ConstructionDerivedOperation

const ConstructionDerivedSubtract = ws.ConstructionDerivedSubtract
const ConstructionDivisionByZeroError = ws.ConstructionDivisionByZeroError
const ConstructionDivisionByZeroNull = ws.ConstructionDivisionByZeroNull

type ConstructionDivisionByZeroPolicy = ws.ConstructionDivisionByZeroPolicy
type ConstructionExpand = ws.ConstructionExpand

const ConstructionExpandEmptyError = ws.ConstructionExpandEmptyError
const ConstructionExpandEmptyExclude = ws.ConstructionExpandEmptyExclude

type ConstructionExpandEmptyPolicy = ws.ConstructionExpandEmptyPolicy

const ConstructionExpandEmptyPreserveParent = ws.ConstructionExpandEmptyPreserveParent
const ConstructionExpandedFieldCardinalityMany = ws.ConstructionExpandedFieldCardinalityMany

type ConstructionExpandedFieldSource = ws.ConstructionExpandedFieldSource
type ConstructionFilter = ws.ConstructionFilter

const ConstructionFilterBoolean = ws.ConstructionFilterBoolean
const ConstructionFilterCode = ws.ConstructionFilterCode
const ConstructionFilterContains = ws.ConstructionFilterContains
const ConstructionFilterDate = ws.ConstructionFilterDate
const ConstructionFilterDateTime = ws.ConstructionFilterDateTime
const ConstructionFilterDecimal = ws.ConstructionFilterDecimal
const ConstructionFilterEquals = ws.ConstructionFilterEquals
const ConstructionFilterExists = ws.ConstructionFilterExists
const ConstructionFilterGreaterEq = ws.ConstructionFilterGreaterEq
const ConstructionFilterGreaterThan = ws.ConstructionFilterGreaterThan
const ConstructionFilterIn = ws.ConstructionFilterIn
const ConstructionFilterInteger = ws.ConstructionFilterInteger
const ConstructionFilterLessEq = ws.ConstructionFilterLessEq
const ConstructionFilterLessThan = ws.ConstructionFilterLessThan
const ConstructionFilterMissing = ws.ConstructionFilterMissing
const ConstructionFilterNotEquals = ws.ConstructionFilterNotEquals

type ConstructionFilterOperator = ws.ConstructionFilterOperator

const ConstructionFilterString = ws.ConstructionFilterString

type ConstructionFilterValueKind = ws.ConstructionFilterValueKind
type ConstructionGroup = ws.ConstructionGroup
type ConstructionGroupAggregate = ws.ConstructionGroupAggregate
type ConstructionGroupAggregateOp = ws.ConstructionGroupAggregateOp

const ConstructionGroupCountDistinct = ws.ConstructionGroupCountDistinct
const ConstructionGroupCountNonNull = ws.ConstructionGroupCountNonNull
const ConstructionGroupCountRows = ws.ConstructionGroupCountRows

type ConstructionGroupFieldCurrentStage = ws.ConstructionGroupFieldCurrentStage
type ConstructionGroupFieldRelatedOccurrence = ws.ConstructionGroupFieldRelatedOccurrence
type ConstructionGroupFieldRelatedRouteStep = ws.ConstructionGroupFieldRelatedRouteStep
type ConstructionGroupFieldSource = ws.ConstructionGroupFieldSource
type ConstructionGroupFieldSourceInput = ws.ConstructionGroupFieldSourceInput
type ConstructionGroupKey = ws.ConstructionGroupKey

const ConstructionGroupMean = ws.ConstructionGroupMean
const ConstructionGroupMissingKeyError = ws.ConstructionGroupMissingKeyError
const ConstructionGroupMissingKeyExclude = ws.ConstructionGroupMissingKeyExclude
const ConstructionGroupMissingKeyGroup = ws.ConstructionGroupMissingKeyGroup

type ConstructionGroupMissingKeyPolicy = ws.ConstructionGroupMissingKeyPolicy
type ConstructionGroupSemanticCategory = ws.ConstructionGroupSemanticCategory

const ConstructionGroupSum = ws.ConstructionGroupSum

type ConstructionID = ws.ConstructionID
type ConstructionImpact = ws.ConstructionImpact
type ConstructionInputKind = ws.ConstructionInputKind
type ConstructionInputRef = ws.ConstructionInputRef

const ConstructionInputSourceProjection = ws.ConstructionInputSourceProjection
const ConstructionInputStepOutput = ws.ConstructionInputStepOutput
const ConstructionInputTableRevision = ws.ConstructionInputTableRevision
const ConstructionInputWorkspaceOutput = ws.ConstructionInputWorkspaceOutput

type ConstructionLiteral = ws.ConstructionLiteral

const ConstructionLiteralOperand = ws.ConstructionLiteralOperand
const ConstructionMissingInputError = ws.ConstructionMissingInputError

type ConstructionMissingInputPolicy = ws.ConstructionMissingInputPolicy

const ConstructionMissingInputPropagateNull = ws.ConstructionMissingInputPropagateNull
const ConstructionNumericDecimal = ws.ConstructionNumericDecimal
const ConstructionNumericInteger = ws.ConstructionNumericInteger

type ConstructionNumericKind = ws.ConstructionNumericKind
type ConstructionOperand = ws.ConstructionOperand
type ConstructionOperandKind = ws.ConstructionOperandKind
type ConstructionOperation = ws.ConstructionOperation

const ConstructionOperationCodedGroup = ws.ConstructionOperationCodedGroup
const ConstructionOperationCodedPivot = ws.ConstructionOperationCodedPivot
const ConstructionOperationCombine = ws.ConstructionOperationCombine
const ConstructionOperationDerive = ws.ConstructionOperationDerive
const ConstructionOperationExpand = ws.ConstructionOperationExpand
const ConstructionOperationFilter = ws.ConstructionOperationFilter
const ConstructionOperationGroup = ws.ConstructionOperationGroup

type ConstructionOperationKind = ws.ConstructionOperationKind

const ConstructionOperationPivot = ws.ConstructionOperationPivot
const ConstructionOperationRelatedEligibility = ws.ConstructionOperationRelatedEligibility
const ConstructionOperationRelatedExpand = ws.ConstructionOperationRelatedExpand
const ConstructionOperationRelatedField = ws.ConstructionOperationRelatedField
const ConstructionOperationRelatedSource = ws.ConstructionOperationRelatedSource
const ConstructionOperationUnpivot = ws.ConstructionOperationUnpivot

type ConstructionPivot = ws.ConstructionPivot
type ConstructionPivotCategory = ws.ConstructionPivotCategory
type ConstructionPivotDiscovery = ws.ConstructionPivotDiscovery
type ConstructionPivotDiscoveryCategory = ws.ConstructionPivotDiscoveryCategory

const ConstructionPivotDuplicateError = ws.ConstructionPivotDuplicateError
const ConstructionPivotDuplicateMax = ws.ConstructionPivotDuplicateMax
const ConstructionPivotDuplicateMin = ws.ConstructionPivotDuplicateMin

type ConstructionPivotDuplicatePolicy = ws.ConstructionPivotDuplicatePolicy

const ConstructionPivotDuplicateSum = ws.ConstructionPivotDuplicateSum

type ConstructionPivotMissingCellPolicy = ws.ConstructionPivotMissingCellPolicy

const ConstructionPivotMissingError = ws.ConstructionPivotMissingError
const ConstructionPivotMissingNull = ws.ConstructionPivotMissingNull

type ConstructionPivotUnlistedCategoryPolicy = ws.ConstructionPivotUnlistedCategoryPolicy

const ConstructionPivotUnlistedError = ws.ConstructionPivotUnlistedError
const ConstructionPivotUnlistedExcludeWithEvidence = ws.ConstructionPivotUnlistedExcludeWithEvidence
const ConstructionRelatedAllMatches = ws.ConstructionRelatedAllMatches

type ConstructionRelatedContributorRule = ws.ConstructionRelatedContributorRule
type ConstructionRelatedEligibility = ws.ConstructionRelatedEligibility

const ConstructionRelatedEligibilityAbsent = ws.ConstructionRelatedEligibilityAbsent
const ConstructionRelatedEligibilityCountAtLeast = ws.ConstructionRelatedEligibilityCountAtLeast
const ConstructionRelatedEligibilityExists = ws.ConstructionRelatedEligibilityExists

type ConstructionRelatedEligibilityMatch = ws.ConstructionRelatedEligibilityMatch
type ConstructionRelatedExpand = ws.ConstructionRelatedExpand
type ConstructionRelatedField = ws.ConstructionRelatedField
type ConstructionRelatedFieldSource = ws.ConstructionRelatedFieldSource
type ConstructionRelatedSource = ws.ConstructionRelatedSource
type ConstructionRowValue = ws.ConstructionRowValue

const ConstructionRowValueAll = ws.ConstructionRowValueAll
const ConstructionRowValueOne = ws.ConstructionRowValueOne

type ConstructionRowValuePolicy = ws.ConstructionRowValuePolicy
type ConstructionSourceProjection = ws.ConstructionSourceProjection

var ConstructionSourceProjectionName = ws.ConstructionSourceProjectionName

type ConstructionStep = ws.ConstructionStep
type ConstructionUnpivot = ws.ConstructionUnpivot

const ConstructionUnpivotDrop = ws.ConstructionUnpivotDrop

type ConstructionUnpivotInput = ws.ConstructionUnpivotInput
type ConstructionUnpivotNullPolicy = ws.ConstructionUnpivotNullPolicy

const ConstructionUnpivotPreserve = ws.ConstructionUnpivotPreserve
const ConstructionVersion = ws.ConstructionVersion

var ContainsString = ws.ContainsString

const ContributorAny = ws.ContributorAny

type ContributorCode = ws.ContributorCode

const ContributorEquals = ws.ContributorEquals
const ContributorExists = ws.ContributorExists

type ContributorOperator = ws.ContributorOperator
type ContributorPredicate = ws.ContributorPredicate
type ContributorQuantifier = ws.ContributorQuantifier

const ContributorString = ws.ContributorString

type ContributorValue = ws.ContributorValue

const ContributorValueCode = ws.ContributorValueCode

type ContributorValueKind = ws.ContributorValueKind
type ContributorWindowSource = ws.ContributorWindowSource

var ConvertStoredSourcePlan = ws.ConvertStoredSourcePlan

const CurrentSemanticsVersion = ws.CurrentSemanticsVersion

var DecodeCurrentWorkspace = ws.DecodeCurrentWorkspace
var DecodeStoredWorkspaceForMigration = ws.DecodeStoredWorkspaceForMigration
var DecodeWorkspace = ws.DecodeWorkspace
var DescribeRowIdentity = ws.DescribeRowIdentity

type Document = ws.Document

const EmptyCollectionError = ws.EmptyCollectionError
const EmptyCollectionExclude = ws.EmptyCollectionExclude

type EmptyCollectionPolicy = ws.EmptyCollectionPolicy

const EmptyCollectionPreserveParent = ws.EmptyCollectionPreserveParent

type ExpandedRows = ws.ExpandedRows
type ExplicitGroupRowValue = ws.ExplicitGroupRowValue
type ExplicitGroupSource = ws.ExplicitGroupSource
type ExplorerMetadata = ws.ExplorerMetadata
type FeatureInterpretation = ws.FeatureInterpretation
type FeatureInterpretationKind = ws.FeatureInterpretationKind

const FeatureInterpretationPinned = ws.FeatureInterpretationPinned

type FieldGroupSource = ws.FieldGroupSource
type FieldSource = ws.FieldSource
type FileActions = ws.FileActions
type FilterPresentation = ws.FilterPresentation
type FilterValue = ws.FilterValue

var FindRoute = ws.FindRoute

type FixedFilter = ws.FixedFilter
type FrameDefinition = ws.FrameDefinition

const FrameManyAll = ws.FrameManyAll
const FrameManyDistinct = ws.FrameManyDistinct
const FrameManyFirst = ws.FrameManyFirst
const FrameManyInvalidMultipleValues = ws.FrameManyInvalidMultipleValues

type FrameManyPolicy = ws.FrameManyPolicy

const FrameZeroEmptyList = ws.FrameZeroEmptyList
const FrameZeroNull = ws.FrameZeroNull

type FrameZeroPolicy = ws.FrameZeroPolicy
type GroupSource = ws.GroupSource

const GroupSourceExplicit = ws.GroupSourceExplicit
const GroupSourceField = ws.GroupSourceField

type GroupSourceKind = ws.GroupSourceKind
type GroupedRows = ws.GroupedRows
type ImportedCatalogOutput = ws.ImportedCatalogOutput
type ImportedCatalogParameters = ws.ImportedCatalogParameters
type ImportedDynamicOutput = ws.ImportedDynamicOutput
type ImportedDynamicParameters = ws.ImportedDynamicParameters
type ImportedExpansionParameters = ws.ImportedExpansionParameters
type ImportedExtensionOutput = ws.ImportedExtensionOutput
type ImportedExtensionParameters = ws.ImportedExtensionParameters
type ImportedFieldGroupParameters = ws.ImportedFieldGroupParameters
type ImportedFieldParameters = ws.ImportedFieldParameters
type ImportedIdentityParameters = ws.ImportedIdentityParameters
type ImportedOutputParameters = ws.ImportedOutputParameters
type ImportedPivotDiscoveryParameters = ws.ImportedPivotDiscoveryParameters
type ImportedPivotOutput = ws.ImportedPivotOutput
type ImportedPivotParameters = ws.ImportedPivotParameters
type ImportedRecipeRef = ws.ImportedRecipeRef
type ImportedRelationship = ws.ImportedRelationship
type ImportedRelationshipField = ws.ImportedRelationshipField
type ImportedRelationshipMatch = ws.ImportedRelationshipMatch
type ImportedRelationshipOverlay = ws.ImportedRelationshipOverlay
type ImportedSource = ws.ImportedSource
type ImportedSourceFieldBinding = ws.ImportedSourceFieldBinding
type ImportedSourceParameters = ws.ImportedSourceParameters
type ImportedSourcePlan = ws.ImportedSourcePlan
type ImportedSourceRuleAddition = ws.ImportedSourceRuleAddition
type ImportedSourceStep = ws.ImportedSourceStep
type ImportedTraversalParameters = ws.ImportedTraversalParameters

var InferredSourceLogicalType = ws.InferredSourceLogicalType

const Kind = ws.Kind
const LifecycleNew = ws.LifecycleNew
const LifecycleReady = ws.LifecycleReady

type LookupSource = ws.LookupSource

const MaxImportedRuleColumns = ws.MaxImportedRuleColumns

var MigrateAggregateTemporalPolicy = ws.MigrateAggregateTemporalPolicy
var MigrateLegacyContributors = ws.MigrateLegacyContributors
var MigrateLosslessDefaults = ws.MigrateLosslessDefaults
var MigrateStoredTableShapeWorkspace = ws.MigrateStoredTableShapeWorkspace
var MigrateStoredWorkspace = ws.MigrateStoredWorkspace
var MigrateWorkspaceForCompilation = ws.MigrateWorkspaceForCompilation

const MissingKeyError = ws.MissingKeyError
const MissingKeyExclude = ws.MissingKeyExclude
const MissingKeyGroupAsMissing = ws.MissingKeyGroupAsMissing

type MissingKeyPolicy = ws.MissingKeyPolicy
type NetworkColumnBinding = ws.NetworkColumnBinding

var NetworkForDocument = ws.NetworkForDocument

type NetworkOccurrence = ws.NetworkOccurrence
type NetworkOperation = ws.NetworkOperation

var NewAuthoringNetwork = ws.NewAuthoringNetwork
var NewFrameDefinition = ws.NewFrameDefinition
var NewWorkspace = ws.NewWorkspace

type Output = ws.Output
type OwnerRecordsSource = ws.OwnerRecordsSource
type PinnedInterpretation = ws.PinnedInterpretation
type Population = ws.Population
type PopulationRouteStep = ws.PopulationRouteStep

var PrepareWorkspaceForCompilation = ws.PrepareWorkspaceForCompilation

type ProjectedField = ws.ProjectedField

const ProjectedFieldOwnerAuthored = ws.ProjectedFieldOwnerAuthored
const ProjectedFieldOwnerPinned = ws.ProjectedFieldOwnerPinned
const ProjectedFieldOwnerUnknown = ws.ProjectedFieldOwnerUnknown
const ProjectedFieldRemovalColumn = ws.ProjectedFieldRemovalColumn
const ProjectedFieldRemovalDisablePinned = ws.ProjectedFieldRemovalDisablePinned
const ProjectedFieldRemovalDisablePinnedOutput = ws.ProjectedFieldRemovalDisablePinnedOutput
const ProjectedFieldRemovalUnavailable = ws.ProjectedFieldRemovalUnavailable
const ProjectedFieldStatusExact = ws.ProjectedFieldStatusExact
const ProjectedFieldStatusUnknown = ws.ProjectedFieldStatusUnknown

var ProjectedFieldsForDocument = ws.ProjectedFieldsForDocument

type Projection = ws.Projection
type ProjectionReader = ws.ProjectionReader

var QualifySemanticPath = ws.QualifySemanticPath

type RecordRows = ws.RecordRows

var RecordsRowDefinition = ws.RecordsRowDefinition

type RelatedSelection = ws.RelatedSelection
type RelationshipRowEffect = ws.RelationshipRowEffect
type RepeatedBoundary = ws.RepeatedBoundary

var ResolveConstructionPivotDiscovery = ws.ResolveConstructionPivotDiscovery

const RootOccurrenceID = ws.RootOccurrenceID

type RouteMatchMode = ws.RouteMatchMode

const RouteMatchOptional = ws.RouteMatchOptional
const RouteMatchRequired = ws.RouteMatchRequired

type RouteNode = ws.RouteNode

var RoutePath = ws.RoutePath

type RoutePolicy = ws.RoutePolicy
type RowCountEvidence = ws.RowCountEvidence
type RowDefinition = ws.RowDefinition

const RowDefinitionExpanded = ws.RowDefinitionExpanded
const RowDefinitionGroups = ws.RowDefinitionGroups

type RowDefinitionKind = ws.RowDefinitionKind

const RowDefinitionRecords = ws.RowDefinitionRecords

type RowIdentity = ws.RowIdentity
type RowIdentityPlan = ws.RowIdentityPlan
type RowIdentityStage = ws.RowIdentityStage
type SharedFilterBinding = ws.SharedFilterBinding

const SourceAggregate = ws.SourceAggregate
const SourceCodedValue = ws.SourceCodedValue
const SourceExtensionByURL = ws.SourceExtensionByURL
const SourceField = ws.SourceField
const SourceIdentifierBySystem = ws.SourceIdentifierBySystem
const SourceImported = ws.SourceImported
const SourceOwnerRecords = ws.SourceOwnerRecords
const SourceProjectID = ws.SourceProjectID

type SourceRuleCreate = ws.SourceRuleCreate
type SourceWhere = ws.SourceWhere
type StageColumn = ws.StageColumn

var StartDocumentConstruction = ws.StartDocumentConstruction

const StateKind = ws.StateKind

type StoredWorkspaceMigration = ws.StoredWorkspaceMigration
type Tab = ws.Tab
type TablePresentation = ws.TablePresentation
type TableScalar = ws.TableScalar

const TableScalarBoolean = ws.TableScalarBoolean
const TableScalarDecimal = ws.TableScalarDecimal
const TableScalarInteger = ws.TableScalarInteger

type TableScalarKind = ws.TableScalarKind

const TableScalarMissing = ws.TableScalarMissing
const TableScalarNull = ws.TableScalarNull
const TableScalarString = ws.TableScalarString

type TemporalFieldChoice = ws.TemporalFieldChoice
type TemporalOrderingSource = ws.TemporalOrderingSource
type TemporalReductionCapabilities = ws.TemporalReductionCapabilities

const UnassignedMemberError = ws.UnassignedMemberError
const UnassignedMemberExclude = ws.UnassignedMemberExclude
const UnassignedMemberGroupAsUnassigned = ws.UnassignedMemberGroupAsUnassigned

type UnassignedMemberPolicy = ws.UnassignedMemberPolicy
type UnavailableSourceRuleOccurrence = ws.UnavailableSourceRuleOccurrence
type UnitNormalizationCapabilities = ws.UnitNormalizationCapabilities
type UnitNormalizationPolicy = ws.UnitNormalizationPolicy
type UnitNormalizationPresetCapability = ws.UnitNormalizationPresetCapability

var ValidateConstructionExpandedFieldSource = ws.ValidateConstructionExpandedFieldSource

type ValueTransformationCapability = ws.ValueTransformationCapability

var VerifyStoredWorkspaceForMigration = ws.VerifyStoredWorkspaceForMigration

type Workspace = ws.Workspace

const WorkspaceKind = ws.WorkspaceKind
