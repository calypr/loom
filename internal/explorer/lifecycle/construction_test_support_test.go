package lifecycle

import (
	"context"
	"time"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/construction"
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

type Base = construction.Base
type SourceLanes = construction.SourceLanes
type CompileRequest = construction.CompileRequest
type PreviewRequestX = construction.PreviewRequest
type FrameSourceFamilyCandidate = construction.FrameSourceFamilyCandidate

type constructionBaseLoader = construction.ConstructionBaseLoader

func (s *Service) loadConstructionBase(ctx context.Context, project, explorerID, snapshotToken string, draftVersion int64, draftDigest, outputID string) (Base, error) {
	return s.syncConstructionService().LoadConstructionBase(ctx, project, explorerID, snapshotToken, draftVersion, draftDigest, outputID)
}

type constructionBaseValidation = construction.ConstructionBaseValidation
type constructionBaseCompilation = construction.ConstructionBaseCompilation

const constructionBaseCacheFormatVersion = construction.ConstructionBaseCacheFormatVersion
const defaultConstructionBaseCacheTTL = construction.DefaultConstructionBaseCacheTTL
const defaultConstructionBaseCacheEntries = construction.DefaultConstructionBaseCacheEntries

type constructionBaseCacheKey = construction.ConstructionBaseCacheKey
type constructionBaseCacheValue = construction.ConstructionBaseCacheValue
type constructionBaseCache = construction.ConstructionBaseCache

var newConstructionBaseCache = construction.NewConstructionBaseCache
var cloneCachedCompilationReceipt = construction.CloneCachedCompilationReceipt
var cloneCachedReceiptConstructionStages = construction.CloneCachedReceiptConstructionStages

const constructionCategoryDiscoveryComplete = construction.ConstructionCategoryDiscoveryComplete
const constructionCategoryDiscoveryLimitExceeded = construction.ConstructionCategoryDiscoveryLimitExceeded
const constructionCategoryDiscoveryMissingUnsupported = construction.ConstructionCategoryDiscoveryMissingUnsupported
const constructionCategoryDiscoveryNoValues = construction.ConstructionCategoryDiscoveryNoValues

type constructionScanFlow = construction.ConstructionScanFlow

func (s *Service) constructionScans() constructionScanFlow {
	return s.syncConstructionService().ConstructionScans()
}

var pruneConstructionCategorySourceProjections = construction.PruneConstructionCategorySourceProjections
var constructionPivotSourcePaths = construction.ConstructionPivotSourcePaths
var uniqueConstructionPivotOutputName = construction.UniqueConstructionPivotOutputName
var constructionProbeCategoryKey = construction.ConstructionProbeCategoryKey
var classifyConstructionCategoryScanError = construction.ClassifyConstructionCategoryScanError
var constructionCategoryColumn = construction.ConstructionCategoryColumn
var constructionCategoryScalar = construction.ConstructionCategoryScalar
var constructionCategoryLabel = construction.ConstructionCategoryLabel
var frameForConstructionChoice = construction.FrameForConstructionChoice
var frameDefinitionForID = construction.FrameDefinitionForID

func (s *Service) resolveConstructionChoiceSemantics(ctx context.Context, project string, authorized ports.AuthorizedCapability, identities []capability.ConstructionChoiceIdentity) (map[string]semantic.Entry, map[string]semantic.Family, string, error) {
	return s.syncConstructionService().ResolveConstructionChoiceSemantics(ctx, project, authorized, identities)
}

var resolveSemanticConstructionChoice = construction.ResolveSemanticConstructionChoice
var resolveFieldConstructionChoice = construction.ResolveFieldConstructionChoice
var resolveFieldConstructionChoiceFromProvenCandidate = construction.ResolveFieldConstructionChoiceFromProvenCandidate
var fieldCandidateMatchesIdentity = construction.FieldCandidateMatchesIdentity
var uniqueCatalogCandidate = construction.UniqueCatalogCandidate
var invalidConstructionChoice = construction.InvalidConstructionChoice

func (s *Service) resolveConstructionCodedPivotChoices(ctx context.Context, base Base, outputID string, candidate authoringv2.Construction) (authoringv2.Construction, map[string]bool, error) {
	return s.syncConstructionService().ResolveConstructionCodedPivotChoices(ctx, base, outputID, candidate)
}
func (s *Service) resolveCodedPivotCategoryChoice(ctx context.Context, base Base, frame capability.SemanticFrameChoiceSource, frameIdentity capability.ConstructionChoiceIdentity, route []capability.ConstructionRouteStep, choiceID string) (semantic.Entry, capability.Candidate, error) {
	return s.syncConstructionService().ResolveCodedPivotCategoryChoice(ctx, base, frame, frameIdentity, route, choiceID)
}

var frameIdentityCandidateID = construction.FrameIdentityCandidateID
var constructionStageSupportsCodedPivot = construction.ConstructionStageSupportsCodedPivot
var codedPivotSourceMatchesPinnedPrior = construction.CodedPivotSourceMatchesPinnedPrior
var constructionSourceStage = construction.ConstructionSourceStage

const maxConstructionColumnPopulationIDs = construction.MaxConstructionColumnPopulationIDs
const columnPopulationCacheTTL = construction.ColumnPopulationCacheTTL
const columnPopulationCacheMaxEntries = construction.ColumnPopulationCacheMaxEntries

type columnPopulationCacheKey = construction.ColumnPopulationCacheKey
type columnPopulationCache = construction.ColumnPopulationCache

var newColumnPopulationCache = construction.NewColumnPopulationCache

func (s *Service) constructionGroupFieldChoices(ctx context.Context, base Base, outputID string, stage explorer.ReceiptConstructionStage, semanticChoices []ConstructionSemanticGroupChoice, semanticChoicesIncomplete bool, targetGroupStepID string, selectedGroupStepID string) ([]ConstructionGroupFieldChoice, bool, error) {
	return s.syncConstructionService().ConstructionGroupFieldChoices(ctx, base, outputID, stage, semanticChoices, semanticChoicesIncomplete, targetGroupStepID, selectedGroupStepID)
}

var markSavedGroupFieldChoices = construction.MarkSavedGroupFieldChoices
var sortConstructionGroupFieldChoices = construction.SortConstructionGroupFieldChoices

func (s *Service) constructionGroupRowValueChoices(ctx context.Context, base Base, outputID string, stage explorer.ReceiptConstructionStage, groupStepID string) ([]ConstructionGroupFieldChoice, error) {
	return s.syncConstructionService().ConstructionGroupRowValueChoices(ctx, base, outputID, stage, groupStepID)
}
func (s *Service) resolveConstructionGroupFieldSources(ctx context.Context, base Base, outputID string, candidate authoringv2.Construction) (authoringv2.Construction, error) {
	return s.syncConstructionService().ResolveConstructionGroupFieldSources(ctx, base, outputID, candidate)
}

var bindOneGroupRelatedRoute = construction.BindOneGroupRelatedRoute
var groupChoiceMatchesInputContext = construction.GroupChoiceMatchesInputContext
var constructionStepHasGroupFieldSource = construction.ConstructionStepHasGroupFieldSource
var groupStepHasRowValueFieldSource = construction.GroupStepHasRowValueFieldSource
var groupRowValueSourceForOutput = construction.GroupRowValueSourceForOutput
var constructionGroupInputStageID = construction.ConstructionGroupInputStageID
var receiptConstructionStageByID = construction.ReceiptConstructionStageByID
var groupFieldSourceForOutput = construction.GroupFieldSourceForOutput
var groupFieldChoiceMatchesBinding = construction.GroupFieldChoiceMatchesBinding
var constructionGroupFieldBinding = construction.ConstructionGroupFieldBinding
var sameConstructionGroupFieldBinding = construction.SameConstructionGroupFieldBinding
var constructionGroupFieldOwnerRoute = construction.ConstructionGroupFieldOwnerRoute
var compiledGroupRouteMatches = construction.CompiledGroupRouteMatches
var groupFieldPathForOwner = construction.GroupFieldPathForOwner
var groupConsumedFieldBoundaries = construction.GroupConsumedFieldBoundaries

type constructionGroupRelatedAnchor = construction.ConstructionGroupRelatedAnchor

var constructionGroupRelatedAnchorForStage = construction.ConstructionGroupRelatedAnchorForStage
var populatedGroupScalarCandidate = construction.PopulatedGroupScalarCandidate
var observedGroupCodingSelector = construction.ObservedGroupCodingSelector
var stripGroupFieldPathPrefix = construction.StripGroupFieldPathPrefix
var groupSourceSelector = construction.GroupSourceSelector
var equalGroupFieldRoutes = construction.EqualGroupFieldRoutes
var groupFieldScopeLabel = construction.GroupFieldScopeLabel
var matchedPublicGroupFieldColumn = construction.MatchedPublicGroupFieldColumn

type observedRelatedGroupCandidate = construction.ObservedRelatedGroupCandidate
type relatedCodingSelector = construction.RelatedCodingSelector

func (s *Service) appendRelatedGroupFieldChoices(ctx context.Context, base Base, stage explorer.ReceiptConstructionStage, fieldContext *explorer.ReceiptConstructionGroupFieldSourceContext, index *fhirschema.Index, choicesByID map[string]ConstructionGroupFieldChoice, targetGroupStepID string, selectedGroupStepID string) (bool, error) {
	return s.syncConstructionService().AppendRelatedGroupFieldChoices(ctx, base, stage, fieldContext, index, choicesByID, targetGroupStepID, selectedGroupStepID)
}

type savedRelatedGroupRoute = construction.SavedRelatedGroupRoute

var savedRelatedGroupRoutes = construction.SavedRelatedGroupRoutes
var constructionGroupRelatedRouteKey = construction.ConstructionGroupRelatedRouteKey
var firstProvenRelatedGroupRoute = construction.FirstProvenRelatedGroupRoute
var groupFieldCompilerContext = construction.GroupFieldCompilerContext
var resolveRelatedGroupSource = construction.ResolveRelatedGroupSource
var groupFieldSourceChoiceFromRelated = construction.GroupFieldSourceChoiceFromRelated
var relatedCodingRowChoice = construction.RelatedCodingRowChoice
var observedRelatedCodingSelectors = construction.ObservedRelatedCodingSelectors
var constructionGroupRelatedRouteLabel = construction.ConstructionGroupRelatedRouteLabel
var relatedCodingFieldLabel = construction.RelatedCodingFieldLabel

type constructionRowSources = construction.ConstructionRowSources

func (s *Service) constructionSources() constructionRowSources {
	return s.syncConstructionService().ConstructionSources()
}

var fhirNonGroupableSourcePath = construction.FhirNonGroupableSourcePath
var fhirNonGroupableGroupFieldPath = construction.FhirNonGroupableGroupFieldPath
var constructionGroupFieldLabel = construction.ConstructionGroupFieldLabel

func (s *Service) resolveConstructionPivotDiscoveries(ctx context.Context, base Base, request ConstructionProposalRequest, candidate authoringv2.Construction) (authoringv2.Construction, map[string]bool, error) {
	return s.syncConstructionService().ResolveConstructionPivotDiscoveries(ctx, base, request, candidate)
}

var constructionPivotDiscoveryOutcomeError = construction.ConstructionPivotDiscoveryOutcomeError
var constructionPivotDiscoveryEvidence = construction.ConstructionPivotDiscoveryEvidence
var constructionUnresolvedPivotDiscovery = construction.ConstructionUnresolvedPivotDiscovery
var validateConstructionPivotSourceSelections = construction.ValidateConstructionPivotSourceSelections
var pivotSourceReasons = construction.PivotSourceReasons
var constructionStageSupportsRelatedField = construction.ConstructionStageSupportsRelatedField
var constructionFHIRScalarType = construction.ConstructionFHIRScalarType
var constructionOwnedRootProjectionColumnID = construction.ConstructionOwnedRootProjectionColumnID
var constructionOwnedRelatedFieldColumnID = construction.ConstructionOwnedRelatedFieldColumnID
var constructionPivotInputStageID = construction.ConstructionPivotInputStageID
var constructionPivotSourceHelperID = construction.ConstructionPivotSourceHelperID
var preserveExistingPivotSourceTable = construction.PreserveExistingPivotSourceTable

type resolvedConstructionPivotSource = construction.ResolvedConstructionPivotSource

var clearConstructionPivotInputs = construction.ClearConstructionPivotInputs
var constructionOwnerAnchorStageID = construction.ConstructionOwnerAnchorStageID
var constructionRefStageID = construction.ConstructionRefStageID
var constructionAuthoringStageColumns = construction.ConstructionAuthoringStageColumns
var constructionPivotSourceOutputName = construction.ConstructionPivotSourceOutputName
var hasNewRootPivotSource = construction.HasNewRootPivotSource
var sourceCandidate = construction.SourceCandidate
var validatePivotSourceColumns = construction.ValidatePivotSourceColumns
var importedSourceRootChoiceInventoryMissing = construction.ImportedSourceRootChoiceInventoryMissing
var constructionNodeOccurrences = construction.ConstructionNodeOccurrences
var runConstructionCapabilityBuilders = construction.RunConstructionCapabilityBuilders
var constructionSourceProjectionAvailability = construction.ConstructionSourceProjectionAvailabilityX
var constructionWorkspaceInputs = construction.ConstructionWorkspaceInputs
var workspaceOutputConsumerClosure = construction.WorkspaceOutputConsumerClosure
var constructionCombineCompatibilityKeys = construction.ConstructionCombineCompatibilityKeys
var analyzeConstructionCandidateWithCascade = construction.AnalyzeConstructionCandidateWithCascade
var constructionRelatedExpandTargetChanged = construction.ConstructionRelatedExpandTargetChanged
var constructionStepsToRemoveForMissingInputs = construction.ConstructionStepsToRemoveForMissingInputs
var constructionImpactForRemovedRelatedExpandAnchors = construction.ConstructionImpactForRemovedRelatedExpandAnchors
var reauthorizeConstructionRelatedField = construction.ReauthorizeConstructionRelatedField
var constructionCodedGroupChoices = construction.ConstructionCodedGroupChoices
var reauthorizeConstructionCodedGroup = construction.ReauthorizeConstructionCodedGroup
var constructionStageSupportsCodedGroup = construction.ConstructionStageSupportsCodedGroup
var relatedFieldInputStageID = construction.RelatedFieldInputStageID
var reauthorizeConstructionRelatedExpand = construction.ReauthorizeConstructionRelatedExpand
var reauthorizeConstructionRelatedEligibility = construction.ReauthorizeConstructionRelatedEligibility
var relatedExpandInputStageID = construction.RelatedExpandInputStageID
var reauthorizeRelatedExpandContributor = construction.ReauthorizeRelatedExpandContributor
var reauthorizeConstructionRelatedSource = construction.ReauthorizeConstructionRelatedSource
var constructionWorkspaceCandidateDigest = construction.ConstructionWorkspaceCandidateDigest
var sameConstructionDocument = construction.SameConstructionDocument
var constructionHasGroupFieldSource = construction.ConstructionHasGroupFieldSource

func (s *Service) verifyConstructionProposalPivotCategoriesExcept(ctx context.Context, request ConstructionProposalRequest, candidate authoringv2.Construction, base *authoringv2.Construction, skip map[string]bool) ([]ConstructionProposalDiagnostic, error) {
	return s.syncConstructionService().VerifyConstructionProposalPivotCategoriesExcept(ctx, request, candidate, base, skip)
}

var constructionProposalPivotProbeDiagnostic = construction.ConstructionProposalPivotProbeDiagnostic
var constructionProposalCategoryKey = construction.ConstructionProposalCategoryKey
var constructionPivotLabelOnlyEdit = construction.ConstructionPivotLabelOnlyEdit

type relatedExpandContributorChoiceCursor = construction.RelatedExpandContributorChoiceCursor
type relatedFieldChoiceCursor = construction.RelatedFieldChoiceCursor
type constructionRouteFlow = construction.ConstructionRouteFlow

func (s *Service) constructionRoutes() constructionRouteFlow {
	return s.syncConstructionService().ConstructionRoutes()
}

var relatedContributorCandidateCardinality = construction.RelatedContributorCandidateCardinality
var relatedContributorCandidateExecutable = construction.RelatedContributorCandidateExecutable
var relatedContributorRepeatedBoundariesMatch = construction.RelatedContributorRepeatedBoundariesMatch
var relatedContributorLogicalTypeMatches = construction.RelatedContributorLogicalTypeMatches
var relatedFieldPathExecutable = construction.RelatedFieldPathExecutable
var relatedFieldLogicalTypeExecutable = construction.RelatedFieldLogicalTypeExecutable
var encodeRelatedFieldChoiceCursor = construction.EncodeRelatedFieldChoiceCursor
var decodeRelatedFieldChoiceCursor = construction.DecodeRelatedFieldChoiceCursor
var encodeRelatedExpandContributorChoiceCursor = construction.EncodeRelatedExpandContributorChoiceCursor
var decodeRelatedExpandContributorChoiceCursor = construction.DecodeRelatedExpandContributorChoiceCursor

type savedRelatedExpandCursor = construction.SavedRelatedExpandCursor

var pageSavedRelatedExpandRoutes = construction.PageSavedRelatedExpandRoutes
var savedRelatedExpandRoutes = construction.SavedRelatedExpandRoutes
var constructionStageSupportsRelatedExpand = construction.ConstructionStageSupportsRelatedExpand

type resolvedRelatedExpandAnchor = construction.ResolvedRelatedExpandAnchor

var resolveRelatedExpandAnchor = construction.ResolveRelatedExpandAnchor
var constructionRouteHasCompilerProof = construction.ConstructionRouteHasCompilerProof
var constructionRouteHasCompilerProofCached = construction.ConstructionRouteHasCompilerProofCached
var constructionSearchSourceKey = construction.ConstructionSearchSourceKey

const constructionSemanticGroupChoiceLimit = construction.ConstructionSemanticGroupChoiceLimit
const constructionSemanticGroupChoicePageLimit = construction.ConstructionSemanticGroupChoicePageLimit

func (s *Service) constructionSemanticGroupChoices(ctx context.Context, base Base, outputID string, stage explorer.ReceiptConstructionStage) ([]ConstructionSemanticGroupChoice, bool, error) {
	return s.syncConstructionService().ConstructionSemanticGroupChoices(ctx, base, outputID, stage)
}

var constructionStageSupportsGroup = construction.ConstructionStageSupportsGroup
var semanticGroupFamilyReady = construction.SemanticGroupFamilyReady
var constructionSemanticGroupCodingPath = construction.ConstructionSemanticGroupCodingPath
var constructionSemanticGroupCodingPathSet = construction.ConstructionSemanticGroupCodingPathSet

func (s *Service) resolveConstructionGroupSemanticCategories(ctx context.Context, base Base, outputID string, candidate authoringv2.Construction) (authoringv2.Construction, error) {
	return s.syncConstructionService().ResolveConstructionGroupSemanticCategories(ctx, base, outputID, candidate)
}

var semanticBindingForGroupKey = construction.SemanticBindingForGroupKey

func (s *Service) resolveConstructionSemanticGroupChoice(ctx context.Context, base Base, outputID, stageID, choiceID string) (authoringv2.ConstructionGroupSemanticCategory, error) {
	return s.syncConstructionService().ResolveConstructionSemanticGroupChoice(ctx, base, outputID, stageID, choiceID)
}
func (s *Service) reauthorizeConstructionSemanticGroupBinding(ctx context.Context, base Base, binding authoringv2.ConstructionGroupSemanticCategory) error {
	return s.syncConstructionService().ReauthorizeConstructionSemanticGroupBinding(ctx, base, binding)
}
func (s *Service) reauthorizeConstructionSemanticGroupFamily(ctx context.Context, base Base, bindingID, familyID, sourceKind, resourceType, sourcePath, system, version, ruleVersion string) (semantic.Family, error) {
	return s.syncConstructionService().ReauthorizeConstructionSemanticGroupFamily(ctx, base, bindingID, familyID, sourceKind, resourceType, sourcePath, system, version, ruleVersion)
}

var constructionGroupSemanticBinding = construction.ConstructionGroupSemanticBinding

func (s *Service) sourceLanes() SourceLanes { return s.syncConstructionService().SourceLanesX() }

const maxConstructionChoiceProposalChoices = construction.MaxConstructionChoiceProposalChoices

var validatePivotSourceSelections = construction.ValidatePivotSourceSelections

type ttlCache[K comparable, V any] = construction.TtlCache[K, V]

type ttlCacheEntry[V any] = construction.TtlCacheEntry[V]

func newTTLCache[K comparable, V any](ttl time.Duration, maxEntries int, now func() time.Time, defaultTTL time.Duration, defaultMaxEntries int) *ttlCache[K, V] {
	return construction.NewTTLCache[K, V](ttl, maxEntries, now, defaultTTL, defaultMaxEntries)
}

func (s *Service) setConstructionBaseCache(cache *constructionBaseCache) {
	s.syncConstructionService().SetConstructionBaseCache(cache)
}
