package construction

import (
	"context"
	"time"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/semantic"
)

var (
	_ = context.Background
	_ = time.Now
	_ = explorer.ErrNotFound
	_ = authoringv2.APIVersion
	_ = capability.Snapshot{}
	_ = ports.AuthorizedCapability{}
	_ = semantic.Entry{}
	_ = fhirschema.Index{}
)

func (s *Service) Catalog(snapshot capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
	return s.catalog(snapshot, explorerID)
}

func (s *Service) GetOwner(ctx context.Context, project, explorerID string) (*explorer.Explorer, error) {
	return s.getOwner(ctx, project, explorerID)
}

func (s *Service) LookupReceipt(ctx context.Context, project, explorerID, receiptID string) (*explorer.CompilationReceipt, error) {
	return s.lookupReceipt(ctx, project, explorerID, receiptID)
}

func (s *Service) VerifyProposalReceipt(ctx context.Context, stage string, receipt *explorer.CompilationReceipt, project, explorerID, snapshotToken string, snapshot capability.Snapshot, expected *authoringv2.Workspace) (authoringv2.Workspace, error) {
	return s.verifyProposalReceipt(ctx, stage, receipt, project, explorerID, snapshotToken, snapshot, expected)
}

func (s *Service) Compile(ctx context.Context, request CompileRequest) (*explorer.CompilationReceipt, error) {
	return s.compile(ctx, request)
}

type ConstructionBaseLoader = constructionBaseLoader

func (s *Service) LoadConstructionBase(ctx context.Context, project, explorerID, snapshotToken string, draftVersion int64, draftDigest, outputID string) (Base, error) {
	return s.loadConstructionBase(ctx, project, explorerID, snapshotToken, draftVersion, draftDigest, outputID)
}

type ConstructionBaseValidation = constructionBaseValidation

type ConstructionBaseCompilation = constructionBaseCompilation

const ConstructionBaseCacheFormatVersion = constructionBaseCacheFormatVersion
const DefaultConstructionBaseCacheTTL = defaultConstructionBaseCacheTTL
const DefaultConstructionBaseCacheEntries = defaultConstructionBaseCacheEntries

type ConstructionBaseCacheKey = constructionBaseCacheKey

type ConstructionBaseCacheValue = constructionBaseCacheValue

type ConstructionBaseCache = constructionBaseCache

var NewConstructionBaseCache = newConstructionBaseCache
var CloneCachedCompilationReceipt = cloneCachedCompilationReceipt
var CloneCachedReceiptConstructionStages = cloneCachedReceiptConstructionStages

const ConstructionCategoryDiscoveryComplete = constructionCategoryDiscoveryComplete
const ConstructionCategoryDiscoveryLimitExceeded = constructionCategoryDiscoveryLimitExceeded
const ConstructionCategoryDiscoveryMissingUnsupported = constructionCategoryDiscoveryMissingUnsupported
const ConstructionCategoryDiscoveryNoValues = constructionCategoryDiscoveryNoValues

type ConstructionScanFlow = constructionScanFlow

func (s *Service) ConstructionScans() constructionScanFlow { return s.constructionScans() }

var PruneConstructionCategorySourceProjections = pruneConstructionCategorySourceProjections
var ConstructionPivotSourcePaths = constructionPivotSourcePaths
var UniqueConstructionPivotOutputName = uniqueConstructionPivotOutputName
var ConstructionProbeCategoryKey = constructionProbeCategoryKey
var ClassifyConstructionCategoryScanError = classifyConstructionCategoryScanError
var ConstructionCategoryColumn = constructionCategoryColumn
var ConstructionCategoryScalar = constructionCategoryScalar
var ConstructionCategoryLabel = constructionCategoryLabel

func (s *Service) PrepareConstructionChoiceX(ctx context.Context, project, explorerID string, authorized ports.AuthorizedCapability, identities []capability.ConstructionChoiceIdentity, workspace authoringv2.Workspace, catalogSnapshot authoringv2.CatalogSnapshot, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	return s.prepareConstructionChoice(ctx, project, explorerID, authorized, identities, workspace, catalogSnapshot, commands)
}

var FrameForConstructionChoice = frameForConstructionChoice
var FrameDefinitionForID = frameDefinitionForID

func (s *Service) ResolveConstructionChoiceSemantics(ctx context.Context, project string, authorized ports.AuthorizedCapability, identities []capability.ConstructionChoiceIdentity) (map[string]semantic.Entry, map[string]semantic.Family, string, error) {
	return s.resolveConstructionChoiceSemantics(ctx, project, authorized, identities)
}

var ResolveSemanticConstructionChoice = resolveSemanticConstructionChoice
var SemanticChoiceFieldOptionsMatch = semanticChoiceFieldOptionsMatch
var SemanticChoiceKey = semanticChoiceKey
var ConstructionChoicesMatchSnapshotX = constructionChoicesMatchSnapshot
var ResolveFieldConstructionChoice = resolveFieldConstructionChoice
var ResolveFieldConstructionChoiceFromProvenCandidate = resolveFieldConstructionChoiceFromProvenCandidate
var FieldCandidateMatchesIdentity = fieldCandidateMatchesIdentity
var UniqueCapabilityCandidate = uniqueCapabilityCandidate
var UniqueCatalogCandidate = uniqueCatalogCandidate
var ConstructionChoiceSupportsX = constructionChoiceSupports
var InvalidConstructionChoice = invalidConstructionChoice

func (s *Service) ResolveConstructionCodedPivotChoices(
	ctx context.Context,
	base Base,
	outputID string,
	candidate authoringv2.Construction,
) (authoringv2.Construction, map[string]bool, error) {
	return s.resolveConstructionCodedPivotChoices(ctx, base, outputID, candidate)
}

func (s *Service) ResolveCodedPivotCategoryChoice(
	ctx context.Context,
	base Base,
	frame capability.SemanticFrameChoiceSource,
	frameIdentity capability.ConstructionChoiceIdentity,
	route []capability.ConstructionRouteStep,
	choiceID string,
) (semantic.Entry, capability.Candidate, error) {
	return s.resolveCodedPivotCategoryChoice(ctx, base, frame, frameIdentity, route, choiceID)
}

var FrameIdentityCandidateID = frameIdentityCandidateID
var ConstructionStageSupportsCodedPivot = constructionStageSupportsCodedPivot
var CodedPivotSourceMatchesPinnedPrior = codedPivotSourceMatchesPinnedPrior
var ConstructionSourceStage = constructionSourceStage

const MaxConstructionColumnPopulationIDs = maxConstructionColumnPopulationIDs
const ColumnPopulationCacheTTL = columnPopulationCacheTTL
const ColumnPopulationCacheMaxEntries = columnPopulationCacheMaxEntries

type ColumnPopulationCacheKey = columnPopulationCacheKey

type ColumnPopulationCache = columnPopulationCache

var NewColumnPopulationCache = newColumnPopulationCache

func (s *Service) ConstructionGroupFieldChoices(
	ctx context.Context,
	base Base,
	outputID string,
	stage explorer.ReceiptConstructionStage,
	semanticChoices []ConstructionSemanticGroupChoice,
	semanticChoicesIncomplete bool,
	targetGroupStepID string,
	selectedGroupStepID string,
) ([]ConstructionGroupFieldChoice, bool, error) {
	return s.constructionGroupFieldChoices(ctx, base, outputID, stage, semanticChoices, semanticChoicesIncomplete, targetGroupStepID, selectedGroupStepID)
}

var MarkSavedGroupFieldChoices = markSavedGroupFieldChoices
var SortConstructionGroupFieldChoices = sortConstructionGroupFieldChoices

func (s *Service) ConstructionGroupRowValueChoices(
	ctx context.Context,
	base Base,
	outputID string,
	stage explorer.ReceiptConstructionStage,
	groupStepID string,
) ([]ConstructionGroupFieldChoice, error) {
	return s.constructionGroupRowValueChoices(ctx, base, outputID, stage, groupStepID)
}

func (s *Service) ResolveConstructionGroupFieldSources(
	ctx context.Context,
	base Base,
	outputID string,
	candidate authoringv2.Construction,
) (authoringv2.Construction, error) {
	return s.resolveConstructionGroupFieldSources(ctx, base, outputID, candidate)
}

var BindOneGroupRelatedRoute = bindOneGroupRelatedRoute
var GroupChoiceMatchesInputContext = groupChoiceMatchesInputContext
var ConstructionStepHasGroupFieldSource = constructionStepHasGroupFieldSource
var GroupStepHasRowValueFieldSource = groupStepHasRowValueFieldSource
var GroupRowValueSourceForOutput = groupRowValueSourceForOutput
var ConstructionGroupInputStageID = constructionGroupInputStageID
var ReceiptConstructionStageByID = receiptConstructionStageByID
var GroupFieldSourceForOutput = groupFieldSourceForOutput
var GroupFieldChoiceMatchesBinding = groupFieldChoiceMatchesBinding
var ConstructionGroupFieldBinding = constructionGroupFieldBinding
var SameConstructionGroupFieldBinding = sameConstructionGroupFieldBinding
var ConstructionGroupFieldOwnerRoute = constructionGroupFieldOwnerRoute
var CompiledGroupRouteMatches = compiledGroupRouteMatches
var GroupFieldPathForOwner = groupFieldPathForOwner
var GroupConsumedFieldBoundaries = groupConsumedFieldBoundaries

type ConstructionGroupRelatedAnchor = constructionGroupRelatedAnchor

var ConstructionGroupRelatedAnchorForStage = constructionGroupRelatedAnchorForStage
var PopulatedGroupScalarCandidate = populatedGroupScalarCandidate
var ObservedGroupCodingSelector = observedGroupCodingSelector
var StripGroupFieldPathPrefix = stripGroupFieldPathPrefix
var GroupSourceSelector = groupSourceSelector
var EqualGroupFieldRoutes = equalGroupFieldRoutes
var GroupFieldScopeLabel = groupFieldScopeLabel
var MatchedPublicGroupFieldColumn = matchedPublicGroupFieldColumn

type ObservedRelatedGroupCandidate = observedRelatedGroupCandidate

type RelatedCodingSelector = relatedCodingSelector

func (s *Service) AppendRelatedGroupFieldChoices(
	ctx context.Context,
	base Base,
	stage explorer.ReceiptConstructionStage,
	fieldContext *explorer.ReceiptConstructionGroupFieldSourceContext,
	index *fhirschema.Index,
	choicesByID map[string]ConstructionGroupFieldChoice,
	targetGroupStepID string,
	selectedGroupStepID string,
) (bool, error) {
	return s.appendRelatedGroupFieldChoices(ctx, base, stage, fieldContext, index, choicesByID, targetGroupStepID, selectedGroupStepID)
}

type SavedRelatedGroupRoute = savedRelatedGroupRoute

var SavedRelatedGroupRoutes = savedRelatedGroupRoutes
var ConstructionGroupRelatedRouteKey = constructionGroupRelatedRouteKey
var FirstProvenRelatedGroupRoute = firstProvenRelatedGroupRoute
var GroupFieldCompilerContext = groupFieldCompilerContext
var ResolveRelatedGroupSource = resolveRelatedGroupSource
var GroupFieldSourceChoiceFromRelated = groupFieldSourceChoiceFromRelated
var RelatedCodingRowChoice = relatedCodingRowChoice
var ObservedRelatedCodingSelectors = observedRelatedCodingSelectors
var ConstructionGroupRelatedRouteLabel = constructionGroupRelatedRouteLabel
var RelatedCodingFieldLabel = relatedCodingFieldLabel

type ConstructionRowSources = constructionRowSources

func (s *Service) ConstructionSources() constructionRowSources { return s.constructionSources() }

var ConstructionGroupSourceCandidate = constructionGroupSourceCandidate
var CanonicalRowPath = canonicalRowPath
var FhirNonGroupableSourcePath = fhirNonGroupableSourcePath
var FhirNonGroupableGroupFieldPath = fhirNonGroupableGroupFieldPath
var ContainsSourceProjectionMode = containsSourceProjectionMode
var ConstructionGroupFieldLabel = constructionGroupFieldLabel

func (s *Service) ResolveConstructionPivotDiscoveries(
	ctx context.Context,
	base Base,
	request ConstructionProposalRequest,
	candidate authoringv2.Construction,
) (authoringv2.Construction, map[string]bool, error) {
	return s.resolveConstructionPivotDiscoveries(ctx, base, request, candidate)
}

var ConstructionPivotDiscoveryOutcomeError = constructionPivotDiscoveryOutcomeError
var ConstructionPivotDiscoveryEvidence = constructionPivotDiscoveryEvidence
var ConstructionUnresolvedPivotDiscovery = constructionUnresolvedPivotDiscovery
var ValidateConstructionPivotSourceSelections = validateConstructionPivotSourceSelections
var PivotSourceReasons = pivotSourceReasons

type RowSourceSkipClass = rowSourceSkipClass

const RowSourceSkipObserved = rowSourceSkipObserved
const RowSourceSkipType = rowSourceSkipType
const RowSourceSkipRoute = rowSourceSkipRoute
const RowSourceSkipVersion = rowSourceSkipVersion
const RowSourceSkipInventory = rowSourceSkipInventory

type RowSourceEnumeration = rowSourceEnumeration

type RowSourceLaneReasons = rowSourceLaneReasons

var RowSourceInventoryFailure = rowSourceInventoryFailure
var RowSourceCandidateSkipClass = rowSourceCandidateSkipClass
var RowSourceChoiceIsStaleVersion = rowSourceChoiceIsStaleVersion
var ConstructionRootProjectionCanReachStage = constructionRootProjectionCanReachStage
var ConstructionReceiptStage = constructionReceiptStage
var ConstructionStageSupportsRelatedField = constructionStageSupportsRelatedField
var ConstructionFHIRScalarType = constructionFHIRScalarType
var ConstructionOwnedRootProjectionColumnID = constructionOwnedRootProjectionColumnID
var ConstructionOwnedRelatedFieldColumnID = constructionOwnedRelatedFieldColumnID
var ConstructionPivotInputStageID = constructionPivotInputStageID
var ConstructionStepInputStageID = constructionStepInputStageID
var ConstructionStepIndex = constructionStepIndex
var ConstructionPivotSourceHelperID = constructionPivotSourceHelperID
var PreserveExistingPivotSourceTable = preserveExistingPivotSourceTable
var ConstructionFHIRPathLabel = constructionFHIRPathLabel
var ConstructionFieldDisplayLabel = constructionFieldDisplayLabel

type ResolvedConstructionPivotSource = resolvedConstructionPivotSource

var PreserveConstructionOwnedInputs = preserveConstructionOwnedInputs
var ConstructionOwnerInputsClearedFromCandidate = constructionOwnerInputsClearedFromCandidate
var ConstructionRootFieldAlreadyProjected = constructionRootFieldAlreadyProjected
var ConstructionStepCarriesSourceLane = constructionStepCarriesSourceLane
var ClearConstructionPivotInputs = clearConstructionPivotInputs
var ConstructionOwnerAnchorStageID = constructionOwnerAnchorStageID
var ConstructionRefStageID = constructionRefStageID
var ConstructionAuthoringStageColumns = constructionAuthoringStageColumns
var AppendStageColumnIfAbsent = appendStageColumnIfAbsent
var ConstructionPivotSourceOutputName = constructionPivotSourceOutputName
var HasPivotSourceProjection = hasPivotSourceProjection
var HasNewRootPivotSource = hasNewRootPivotSource
var UniqueRowSourceCandidate = uniqueRowSourceCandidate
var SourceCandidate = sourceCandidate
var ValidatePivotSourceColumns = validatePivotSourceColumns
var ImportedSourceChoiceUnavailable = importedSourceChoiceUnavailable
var ImportedSourceRootChoiceInventoryMissing = importedSourceRootChoiceInventoryMissing
var ConstructionNodeOccurrences = constructionNodeOccurrences
var RunConstructionCapabilityBuilders = runConstructionCapabilityBuilders
var ConstructionSourceProjectionAvailabilityX = constructionSourceProjectionAvailability
var ConstructionWorkspaceInputs = constructionWorkspaceInputs
var ConstructionCapabilitiesWorkspaceX = constructionCapabilitiesWorkspace
var WorkspaceOutputConsumerClosure = workspaceOutputConsumerClosure
var ConstructionCombineCompatibilityKeys = constructionCombineCompatibilityKeys
var AnalyzeConstructionCandidateWithCascade = analyzeConstructionCandidateWithCascade
var ConstructionRelatedExpandTargetChanged = constructionRelatedExpandTargetChanged
var ConstructionStepsToRemoveForMissingInputs = constructionStepsToRemoveForMissingInputs
var ConstructionImpactForRemovedRelatedExpandAnchors = constructionImpactForRemovedRelatedExpandAnchors
var ReauthorizeConstructionRelatedField = reauthorizeConstructionRelatedField
var ConstructionCodedGroupChoices = constructionCodedGroupChoices
var ReauthorizeConstructionCodedGroup = reauthorizeConstructionCodedGroup
var ConstructionStageSupportsCodedGroup = constructionStageSupportsCodedGroup
var RelatedFieldInputStageID = relatedFieldInputStageID
var ReauthorizeConstructionRelatedExpand = reauthorizeConstructionRelatedExpand
var ReauthorizeConstructionRelatedEligibility = reauthorizeConstructionRelatedEligibility
var RelatedExpandInputStageID = relatedExpandInputStageID
var ReauthorizeRelatedExpandContributor = reauthorizeRelatedExpandContributor
var ReauthorizeConstructionRelatedSource = reauthorizeConstructionRelatedSource
var ConstructionDocumentIndexX = constructionDocumentIndex
var ConstructionWorkspaceCandidateDigest = constructionWorkspaceCandidateDigest

func (s *Service) PrepareConstructionProposalX(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, snapshot capability.Snapshot, current authoringv2.Workspace, commands []authoringv2.Command, sinkFactory PreviewSinkFactory) ([]authoringv2.Command, *explorer.CompilationReceipt, *AcceptedConstruction, error) {
	return s.prepareConstructionProposal(ctx, project, explorerID, request, snapshot, current, commands, sinkFactory)
}

var SameConstructionDocument = sameConstructionDocument
var ConstructionHasGroupFieldSource = constructionHasGroupFieldSource
var CheckConstructionProposalResultX = checkConstructionProposalResult

func (s *Service) VerifyConstructionProposalPivotCategoriesExcept(
	ctx context.Context,
	request ConstructionProposalRequest,
	candidate authoringv2.Construction,
	base *authoringv2.Construction,
	skip map[string]bool,
) ([]ConstructionProposalDiagnostic, error) {
	return s.verifyConstructionProposalPivotCategoriesExcept(ctx, request, candidate, base, skip)
}

var ConstructionProposalPivotProbeDiagnostic = constructionProposalPivotProbeDiagnostic
var ConstructionProposalCategoryKey = constructionProposalCategoryKey
var ConstructionPivotLabelOnlyEdit = constructionPivotLabelOnlyEdit

type RelatedExpandContributorChoiceCursor = relatedExpandContributorChoiceCursor

type RelatedFieldChoiceCursor = relatedFieldChoiceCursor

type ConstructionRouteFlow = constructionRouteFlow

func (s *Service) ConstructionRoutes() constructionRouteFlow { return s.constructionRoutes() }

var RelatedContributorCandidateCardinality = relatedContributorCandidateCardinality
var RelatedContributorCandidateExecutable = relatedContributorCandidateExecutable
var RelatedContributorRepeatedBoundariesMatch = relatedContributorRepeatedBoundariesMatch
var RelatedContributorLogicalTypeMatches = relatedContributorLogicalTypeMatches
var RelatedFieldPathExecutable = relatedFieldPathExecutable
var RelatedFieldLogicalTypeExecutable = relatedFieldLogicalTypeExecutable
var EncodeRelatedFieldChoiceCursor = encodeRelatedFieldChoiceCursor
var DecodeRelatedFieldChoiceCursor = decodeRelatedFieldChoiceCursor
var EncodeRelatedExpandContributorChoiceCursor = encodeRelatedExpandContributorChoiceCursor
var DecodeRelatedExpandContributorChoiceCursor = decodeRelatedExpandContributorChoiceCursor

type SavedRelatedExpandCursor = savedRelatedExpandCursor

var PageSavedRelatedExpandRoutes = pageSavedRelatedExpandRoutes
var SavedRelatedExpandRoutes = savedRelatedExpandRoutes
var ConstructionStageSupportsRelatedExpand = constructionStageSupportsRelatedExpand

type ResolvedRelatedExpandAnchor = resolvedRelatedExpandAnchor

var ResolveRelatedExpandAnchor = resolveRelatedExpandAnchor
var ConstructionRouteHasCompilerProof = constructionRouteHasCompilerProof
var ConstructionRouteHasCompilerProofCached = constructionRouteHasCompilerProofCached
var ProveConstructionCandidateX = proveConstructionCandidate
var ConstructionCompilerScopeX = constructionCompilerScope
var ConstructionChoiceIdentityMatchesX = constructionChoiceIdentityMatches
var ConstructionRouteForOccurrenceX = constructionRouteForOccurrence
var ConstructionSearchSourceKey = constructionSearchSourceKey

func (s *Service) CurrentWorkspaceX(ctx context.Context, project, explorerID string) (authoringv2.Workspace, error) {
	return s.currentWorkspace(ctx, project, explorerID)
}

const ConstructionSemanticGroupChoiceLimit = constructionSemanticGroupChoiceLimit
const ConstructionSemanticGroupChoicePageLimit = constructionSemanticGroupChoicePageLimit

func (s *Service) ConstructionSemanticGroupChoices(
	ctx context.Context,
	base Base,
	outputID string,
	stage explorer.ReceiptConstructionStage,
) ([]ConstructionSemanticGroupChoice, bool, error) {
	return s.constructionSemanticGroupChoices(ctx, base, outputID, stage)
}

var ConstructionStageSupportsGroup = constructionStageSupportsGroup
var SemanticGroupFamilyReady = semanticGroupFamilyReady
var ConstructionSemanticGroupCodingPath = constructionSemanticGroupCodingPath
var ConstructionSemanticGroupCodingPathSet = constructionSemanticGroupCodingPathSet

func (s *Service) ResolveConstructionGroupSemanticCategories(
	ctx context.Context,
	base Base,
	outputID string,
	candidate authoringv2.Construction,
) (authoringv2.Construction, error) {
	return s.resolveConstructionGroupSemanticCategories(ctx, base, outputID, candidate)
}

var SemanticBindingForGroupKey = semanticBindingForGroupKey

func (s *Service) ResolveConstructionSemanticGroupChoice(
	ctx context.Context,
	base Base,
	outputID, stageID, choiceID string,
) (authoringv2.ConstructionGroupSemanticCategory, error) {
	return s.resolveConstructionSemanticGroupChoice(ctx, base, outputID, stageID, choiceID)
}

func (s *Service) ReauthorizeConstructionSemanticGroupBinding(
	ctx context.Context,
	base Base,
	binding authoringv2.ConstructionGroupSemanticCategory,
) error {
	return s.reauthorizeConstructionSemanticGroupBinding(ctx, base, binding)
}

func (s *Service) ReauthorizeConstructionSemanticGroupFamily(
	ctx context.Context,
	base Base,
	bindingID, familyID, sourceKind, resourceType, sourcePath, system, version, ruleVersion string,
) (semantic.Family, error) {
	return s.reauthorizeConstructionSemanticGroupFamily(ctx, base, bindingID, familyID, sourceKind, resourceType, sourcePath, system, version, ruleVersion)
}

var ConstructionGroupSemanticBinding = constructionGroupSemanticBinding

type SemanticInventoryRouteCandidate = semanticInventoryRouteCandidate

type SemanticCandidateRouteProof = semanticCandidateRouteProof

var SemanticInventoryChoicesForOutput = semanticInventoryChoicesForOutput
var SemanticInventoryReachableNodes = semanticInventoryReachableNodes
var SemanticCandidateRouteProofCacheKey = semanticCandidateRouteProofCacheKey
var SemanticInventoryConstructionChoiceForRoute = semanticInventoryConstructionChoiceForRoute
var SemanticInventoryEntryForFamily = semanticInventoryEntryForFamily
var SemanticCategoryConstructionChoiceForRoute = semanticCategoryConstructionChoiceForRoute
var SemanticCategoryLabel = semanticCategoryLabel
var SemanticIdentifierLabel = semanticIdentifierLabel
var SemanticSystemTailLabel = semanticSystemTailLabel
var SemanticHumanizeLabel = semanticHumanizeLabel
var SemanticEntryMatchesFrame = semanticEntryMatchesFrame
var SemanticEntryMatchesFrameFamily = semanticEntryMatchesFrameFamily
var SemanticFrameFamilyMatchesPinnedPrior = semanticFrameFamilyMatchesPinnedPrior
var SemanticEntryForFrameFamily = semanticEntryForFrameFamily
var IsPriorPrimaryOnlySemanticFrameFamily = isPriorPrimaryOnlySemanticFrameFamily
var SemanticEntrySourceMatchesFrameFamily = semanticEntrySourceMatchesFrameFamily
var ProveOwnerRecordsForRoute = proveOwnerRecordsForRoute
var SemanticConstructionCandidate = semanticConstructionCandidate
var SemanticCandidateWithFallback = semanticCandidateWithFallback
var SemanticObservationValuePath = semanticObservationValuePath
var CanonicalInventoryPath = canonicalInventoryPath
var CompilerProofUnavailable = compilerProofUnavailable
var SemanticInventoryContextToken = semanticInventoryContextToken

func (s *Service) SourceLanesX() SourceLanes { return s.sourceLanes() }

func (s *Service) ConstructionFilterSourceCapabilityX(ctx context.Context, base Base, outputID, stageID string) (ConstructionFilterSourceCapability, error) {
	return s.constructionFilterSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) ResolveConstructionFilterSource(ctx context.Context, base Base, request ConstructionProposalRequest, acceptedStageID string, selection ConstructionFilterSourceSelection) (authoringv2.ConstructionSourceProjection, error) {
	return s.resolveConstructionFilterSource(ctx, base, request, acceptedStageID, selection)
}

func (s *Service) ConstructionCandidateWithFilterSources(ctx context.Context, base Base, request ConstructionProposalRequest, candidate authoringv2.Construction) (authoringv2.Construction, error) {
	return s.constructionCandidateWithFilterSources(ctx, base, request, candidate)
}

func (s *Service) ReauthorizeConstructionFilterSources(ctx context.Context, base Base, outputID string, construction authoringv2.Construction) error {
	return s.reauthorizeConstructionFilterSources(ctx, base, outputID, construction)
}

func (s *Service) ConstructionDeriveSourceCapabilityX(ctx context.Context, base Base, outputID, stageID string) (ConstructionDeriveSourceCapability, error) {
	return s.constructionDeriveSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) ResolveConstructionDeriveSource(ctx context.Context, base Base, request ConstructionProposalRequest, acceptedStageID string, selection ConstructionDeriveSourceSelection) (authoringv2.ConstructionSourceProjection, error) {
	return s.resolveConstructionDeriveSource(ctx, base, request, acceptedStageID, selection)
}

func (s *Service) ConstructionCandidateWithDeriveSources(ctx context.Context, base Base, request ConstructionProposalRequest, candidate authoringv2.Construction) (authoringv2.Construction, error) {
	return s.constructionCandidateWithDeriveSources(ctx, base, request, candidate)
}

func (s *Service) ReauthorizeConstructionDeriveSources(ctx context.Context, base Base, outputID string, construction authoringv2.Construction) error {
	return s.reauthorizeConstructionDeriveSources(ctx, base, outputID, construction)
}

func (s *Service) ConstructionExpandSourceCapabilityX(ctx context.Context, base Base, outputID, stageID string) (ConstructionExpandSourceCapability, error) {
	return s.constructionExpandSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) ResolveConstructionExpandSource(ctx context.Context, base Base, request ConstructionProposalRequest, acceptedStageID string, selection ConstructionExpandSourceSelection) (authoringv2.ConstructionSourceProjection, error) {
	return s.resolveConstructionExpandSource(ctx, base, request, acceptedStageID, selection)
}

func (s *Service) ConstructionCandidateWithExpandSources(ctx context.Context, base Base, request ConstructionProposalRequest, candidate authoringv2.Construction) (authoringv2.Construction, error) {
	return s.constructionCandidateWithExpandSources(ctx, base, request, candidate)
}

func (s *Service) ReauthorizeConstructionExpandSources(ctx context.Context, base Base, outputID string, construction authoringv2.Construction) error {
	return s.reauthorizeConstructionExpandSources(ctx, base, outputID, construction)
}

func (s *Service) ConstructionUnpivotSourceCapabilityX(ctx context.Context, base Base, outputID, stageID string) (ConstructionUnpivotSourceCapability, error) {
	return s.constructionUnpivotSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) ResolveConstructionUnpivotSource(ctx context.Context, base Base, request ConstructionProposalRequest, acceptedStageID string, selection ConstructionUnpivotSourceSelection) (authoringv2.ConstructionSourceProjection, error) {
	return s.resolveConstructionUnpivotSource(ctx, base, request, acceptedStageID, selection)
}

func (s *Service) ConstructionCandidateWithUnpivotSources(ctx context.Context, base Base, request ConstructionProposalRequest, candidate authoringv2.Construction) (authoringv2.Construction, error) {
	return s.constructionCandidateWithUnpivotSources(ctx, base, request, candidate)
}

func (s *Service) ReauthorizeConstructionUnpivotSources(ctx context.Context, base Base, outputID string, construction authoringv2.Construction) error {
	return s.reauthorizeConstructionUnpivotSources(ctx, base, outputID, construction)
}

func (s *Service) ReauthorizeFrameSourceChoice(ctx context.Context, project, explorerID, rowRoot, outputID, choiceID string, authorized ports.AuthorizedCapability, identity capability.ConstructionChoiceIdentity, base Base) (FrameSourceFamilyCandidate, []capability.ConstructionRouteStep, error) {
	return s.reauthorizeFrameSourceChoice(ctx, project, explorerID, rowRoot, outputID, choiceID, authorized, identity, base)
}

var Failure = failure
var FailureDetails = failureDetails
var Malformed = malformed
var Unavailable = unavailable
var Conflict = conflict
var Forbidden = forbidden
var NotFound = notFound
var Unprocessable = unprocessable
var Internal = internal
var PersistenceFailure = persistenceFailure
var ValidateAuthorizedReadScope = validateAuthorizedReadScope
var RequireExactIdentity = requireExactIdentity
var FirstNonEmpty = firstNonEmpty
var PreviewWorkspace = previewWorkspace
var VerifyPreviewReceiptIntent = verifyPreviewReceiptIntent
var ApplyAuthorizedScope = applyAuthorizedScope
var ValidateReceiptOutputContract = validateReceiptOutputContract
var ReceiptHasOutput = receiptHasOutput
var CapabilityFailure = capabilityFailure
var ReceiptCapabilityFailure = receiptCapabilityFailure
var ResolveCapabilityRouteEdge = resolveCapabilityRouteEdge
var ReauthorizeConstructionRoute = reauthorizeConstructionRoute
var ReauthorizeConstructionRouteFromAnchor = reauthorizeConstructionRouteFromAnchor
var RoutePathToOccurrence = routePathToOccurrence
var FhirIdentifierPath = fhirIdentifierPath
var ResolveRowChoiceOccurrence = resolveRowChoiceOccurrence
var ValidateResolvedRowChoice = validateResolvedRowChoice
var DocumentDigest = documentDigest
var FindSemanticOutput = findSemanticOutput
var WorkspaceValidationCode = workspaceValidationCode
var SchemaFieldCandidateID = schemaFieldCandidateID
var ResolveSchemaFieldCandidate = resolveSchemaFieldCandidate
var ValidSHA256Digest = validSHA256Digest

const MaxConstructionChoiceProposalChoices = maxConstructionChoiceProposalChoices

var ValidatePivotSourceSelections = validatePivotSourceSelections

type TtlCache[K comparable, V any] = ttlCache[K, V]

type TtlCacheEntry[V any] = ttlCacheEntry[V]

func NewTTLCache[K comparable, V any](ttl time.Duration, maxEntries int, now func() time.Time, defaultTTL time.Duration, defaultMaxEntries int) *ttlCache[K, V] {
	return newTTLCache[K, V](ttl, maxEntries, now, defaultTTL, defaultMaxEntries)
}

func (s *Service) SetConstructionBaseCache(cache *constructionBaseCache) {
	s.constructionBaseCache = cache
}

func (s constructionRowSources) ConstructionGroupSourceCapability(ctx context.Context, base Base, outputID, stageID string) (ConstructionGroupSourceCapability, error) {
	return s.constructionGroupSourceCapability(ctx, base, outputID, stageID)
}
func (s constructionRowSources) ConstructionCandidateWithGroupSource(ctx context.Context, base Base, request ConstructionProposalRequest) (authoringv2.Construction, error) {
	return s.constructionCandidateWithGroupSource(ctx, base, request)
}
func (s constructionRowSources) ResolveConstructionGroupSource(ctx context.Context, base Base, request ConstructionProposalRequest, columnID string) (authoringv2.ConstructionSourceProjection, error) {
	return s.resolveConstructionGroupSource(ctx, base, request, columnID)
}
func (s constructionRowSources) ConstructionPivotSourceCapability(ctx context.Context, base Base, outputID, stageID string) (ConstructionPivotSourceCapability, error) {
	return s.constructionPivotSourceCapability(ctx, base, outputID, stageID)
}
func (s constructionRowSources) ConstructionCandidateWithPivotSources(ctx context.Context, base Base, request ConstructionProposalRequest, candidate authoringv2.Construction) (authoringv2.Construction, map[string]bool, error) {
	return s.constructionCandidateWithPivotSources(ctx, base, request, candidate)
}

func (s relatedCodingSelector) Path() string { return s.path }
