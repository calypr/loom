package semanticbrowse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	semanticarango "github.com/calypr/loom/internal/semantic/arango"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog/fieldprofile"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/calypr/loom/internal/projectid"
	"github.com/calypr/loom/internal/semantic"
)

type FieldPresentationResult interface {
	FieldPresentationResult()
}

type FieldPresentationUnavailable struct {
	Kind   FieldPresentationResultKind `json:"kind"`
	Reason FieldPresentationReason     `json:"reason"`
}

func (FieldPresentationUnavailable) FieldPresentationResult() {}

type FieldPresentationReady struct {
	Kind         FieldPresentationResultKind `json:"kind"`
	Context      FieldPresentationContext    `json:"context"`
	Decisions    []FieldPresentationDecision `json:"decisions"`
	Replacements []SemanticInventoryItem     `json:"replacements"`
}

func (FieldPresentationReady) FieldPresentationResult() {}

type FieldPresentationContext struct {
	Project                  string                             `json:"project"`
	AuthorizationScopeDigest string                             `json:"authorizationScopeDigest"`
	SnapshotToken            string                             `json:"snapshotToken"`
	DatasetGeneration        string                             `json:"datasetGeneration"`
	InventoryBuildID         string                             `json:"inventoryBuildId"`
	CoverageBuildID          string                             `json:"coverageBuildId"`
	ExplorerID               string                             `json:"explorerId"`
	OutputID                 string                             `json:"outputId"`
	DraftVersion             int64                              `json:"draftVersion"`
	DraftDigest              string                             `json:"draftDigest"`
	SourceNodeID             string                             `json:"sourceNodeId"`
	OccurrenceID             string                             `json:"occurrenceId"`
	Route                    []capability.ConstructionRouteStep `json:"route"`
	StageID                  string                             `json:"stageId,omitempty"`
	PresentationRevision     string                             `json:"presentationRevision"`
	CandidateBatchDigest     string                             `json:"candidateBatchDigest"`
}

type FieldPresentationDecision interface {
	FieldPresentationDecision()
}

type FieldPresentationKeepRaw struct {
	Kind        FieldPresentationDecisionKind `json:"kind"`
	CandidateID string                        `json:"candidateId"`
	Reason      FieldPresentationReason       `json:"reason"`
}

func (FieldPresentationKeepRaw) FieldPresentationDecision() {}

type FieldPresentationGroupUnderSemantic struct {
	Kind           FieldPresentationDecisionKind   `json:"kind"`
	CandidateID    string                          `json:"candidateId"`
	CoverageID     string                          `json:"coverageId"`
	ReplacementIDs []semanticInventoryItemIdentity `json:"replacementIds"`
	Access         FieldPresentationAccess         `json:"access"`
}

func (FieldPresentationGroupUnderSemantic) FieldPresentationDecision() {}

type fieldPresentationCurrentContext struct {
	owner          explorer.Explorer
	document       ws.Document
	sourceNodeID   string
	candidates     []capability.Candidate
	route          []capability.ConstructionRouteStep
	stageID        string
	stageSupported bool
}

type semanticPresentationReference struct {
	identity semanticInventoryItemIdentity
	key      string
	exact    *semantic.Reference
	family   *semantic.FamilyReference
}

func fieldPresentationUnavailable(reason FieldPresentationReason) FieldPresentationResult {
	return FieldPresentationUnavailable{Kind: FieldPresentationResultUnavailable, Reason: reason}
}

func fieldPresentationCandidateBatchDigest(candidates []capability.Candidate) (string, error) {
	identities := make([]struct {
		ID           string
		ResourceType string
		FieldPath    string
	}, 0, len(candidates))
	for _, candidate := range candidates {
		identities = append(identities, struct {
			ID           string
			ResourceType string
			FieldPath    string
		}{ID: candidate.ID, ResourceType: candidate.ResourceType, FieldPath: candidate.FieldPath})
	}
	raw, err := json.Marshal(identities)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func fieldPresentationCoverageReason(reason string) (FieldPresentationReason, bool) {
	value := FieldPresentationReason(reason)
	switch value {
	case FieldPresentationMissingSystem, FieldPresentationMissingCode, FieldPresentationTextOnly,
		FieldPresentationAmbiguousAnchor, FieldPresentationMixedChoice, FieldPresentationUnsupportedValueProjection,
		FieldPresentationUnsupportedVersion, FieldPresentationUnknownSchemaMember, FieldPresentationUnknownCodeClassification,
		FieldPresentationUnsupportedSelector, FieldPresentationIncompleteSource, FieldPresentationWalkFailed,
		FieldPresentationTruncatedEvidence, FieldPresentationUnrepresentedMetadata:
		return value, true
	default:
		return FieldPresentationIncompleteCoverage, false
	}
}

func semanticPresentationReferenceFromCoverage(reference fieldprofile.SemanticCoverageReference, candidate capability.Candidate, sourceKind string) (semanticPresentationReference, bool) {
	if reference.ResourceType != candidate.ResourceType || reference.CandidatePath != candidate.FieldPath {
		return semanticPresentationReference{}, false
	}
	switch reference.Kind {
	case fieldprofile.SemanticCoverageExactReference:
		if reference.ConceptID == "" || reference.BindingID == "" {
			return semanticPresentationReference{}, false
		}
		identity := ExactSemanticInventoryItemIdentity{Kind: SemanticInventoryIdentityExact, ConceptID: reference.ConceptID, BindingID: reference.BindingID}
		exact := semantic.Reference{ConceptID: reference.ConceptID, BindingID: reference.BindingID}
		return semanticPresentationReference{identity: identity, key: "EXACT\x00" + reference.BindingID + "\x00" + reference.ConceptID, exact: &exact}, true
	case fieldprofile.SemanticCoverageFamilyReference:
		family := reference.Family
		if family.FamilyID == "" || family.FamilyID != semantic.FamilyID(family) ||
			family.BindingID == "" || family.BindingID != reference.BindingID || family.SourceKind != sourceKind ||
			family.ResourceType != candidate.ResourceType || family.SourcePath == "" || family.System == "" {
			return semanticPresentationReference{}, false
		}
		identity := FamilySemanticInventoryItemIdentity{Kind: SemanticInventoryIdentityFamily, FamilyID: family.FamilyID, BindingID: family.BindingID}
		familyCopy := family
		return semanticPresentationReference{identity: identity, key: "FAMILY\x00" + family.BindingID + "\x00" + family.FamilyID, family: &familyCopy}, true
	default:
		return semanticPresentationReference{}, false
	}
}

func fieldPresentationRawCandidateChoice(
	ctx context.Context,
	authorized ports.AuthorizedCapability,
	snapshot capability.Snapshot,
	rootResourceType string,
	candidate capability.Candidate,
	route []capability.ConstructionRouteStep,
	proofCache map[string]semanticCandidateRouteProof,
) (capability.ConstructionChoice, bool, error) {
	if !candidate.Populated {
		return capability.ConstructionChoice{}, false, nil
	}
	proof, err := proveSemanticCandidateForRoute(ctx, authorized, rootResourceType, candidate, route, proofCache)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return capability.ConstructionChoice{}, false, support.Unavailable("catalog", "FIELD_PRESENTATION_CANDIDATE_PROOF_INTERRUPTED", "compiler proof for the raw field was interrupted", err)
		}
		return capability.ConstructionChoice{}, false, support.Internal("catalog", "FIELD_PRESENTATION_CANDIDATE_PROOF_FAILED", "the raw field compiler proof failed", err)
	}
	choice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, route, proof.candidate)
	if err != nil || len(choice.Options) == 0 {
		return capability.ConstructionChoice{}, false, nil
	}
	return choice, true, nil
}

func fieldPresentationRuleVisibleInCodedView(ruleHint string) bool {
	switch ruleHint {
	case semantic.RuleHintCodedValueV1, semantic.RuleHintCodedConceptV1, semantic.RuleHintCodedCategoryV1:
		return true
	default:
		return false
	}
}

func (b *Service) semanticInventoryPresentationForOutput(
	ctx context.Context,
	authorized ports.AuthorizedCapability,
	snapshot capability.Snapshot,
	document ws.Document,
	outputID, contextToken string,
	build semantic.Build,
	entries []semantic.Entry,
	families []semantic.Family,
	current fieldPresentationCurrentContext,
	request FieldPresentationRequest,
) (map[string]capability.ConstructionChoice, FieldPresentationResult, error) {
	proofCache := make(map[string]semanticCandidateRouteProof)
	choices, err := semanticInventoryChoicesForExactOccurrence(
		ctx, authorized, snapshot, document.RootResourceType, contextToken, build.BuildID,
		entries, families, request.SourceNodeID, current.route, proofCache,
	)
	if err != nil {
		return nil, nil, err
	}
	if !current.stageSupported {
		return choices, fieldPresentationUnavailable(FieldPresentationUnsupportedCurrentStage), nil
	}
	if b.semanticCoverage == nil {
		return choices, fieldPresentationUnavailable(FieldPresentationCoverageUnavailable), nil
	}
	if build.BuildID == "" || build.SourceKind == "" {
		return choices, fieldPresentationUnavailable(FieldPresentationCoverageUnavailable), nil
	}
	coveragePaths := make([]semanticarango.SemanticCoverageCandidatePath, 0, len(current.candidates))
	coveragePathKeys := make(map[string]struct{}, len(current.candidates))
	for _, candidate := range current.candidates {
		path := semanticarango.SemanticCoverageCandidatePath{ResourceType: candidate.ResourceType, CanonicalPath: candidate.FieldPath}
		key := semanticCoverageCandidatePathKey(path)
		if _, exists := coveragePathKeys[key]; exists {
			continue
		}
		coveragePathKeys[key] = struct{}{}
		coveragePaths = append(coveragePaths, path)
	}
	unrestricted := authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	lookup, err := b.semanticCoverage(ctx, semanticarango.SemanticCoverageLookupOptions{
		Project: projectid.Legacy(snapshot.Identity.Project), DatasetGeneration: snapshot.Identity.Generation,
		BuildID: build.BuildID, SchemaIdentity: snapshot.Identity.SchemaDigest, SourceKind: build.SourceKind,
		AuthResourcePaths:             append([]string(nil), authorized.Scope.AuthResourcePaths...),
		AuthResourcePathsUnrestricted: unrestricted, Candidates: coveragePaths,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return nil, nil, support.Unavailable("catalog", "FIELD_PRESENTATION_LOOKUP_INTERRUPTED", "semantic field coverage lookup was interrupted", err)
		}
		return nil, nil, support.PersistenceFailure("catalog", "semantic field coverage could not be read", err)
	}
	if lookup.State != semantic.SemanticCoverageBuildComplete && lookup.State != semantic.SemanticCoverageBuildPartial {
		reason := FieldPresentationCoverageUnavailable
		if lookup.State == semantic.SemanticCoverageBuildBuilding || lookup.State == semantic.SemanticCoverageBuildFailed {
			reason = FieldPresentationIncompleteCoverage
		}
		return choices, fieldPresentationUnavailable(reason), nil
	}
	coverageByPath := make(map[string]semanticarango.SemanticCoverageCandidateResult, len(lookup.Candidates))
	for _, candidate := range lookup.Candidates {
		key := semanticCoverageCandidatePathKey(semanticarango.SemanticCoverageCandidatePath{
			ResourceType: candidate.ResourceType, CanonicalPath: candidate.CanonicalPath,
		})
		if _, duplicate := coverageByPath[key]; duplicate {
			return choices, fieldPresentationUnavailable(FieldPresentationIncompleteCoverage), nil
		}
		coverageByPath[key] = candidate
	}
	if len(coverageByPath) != len(coveragePaths) {
		return choices, fieldPresentationUnavailable(FieldPresentationIncompleteCoverage), nil
	}
	for _, path := range coveragePaths {
		if _, found := coverageByPath[semanticCoverageCandidatePathKey(path)]; !found {
			return choices, fieldPresentationUnavailable(FieldPresentationIncompleteCoverage), nil
		}
	}
	batchDigest, err := fieldPresentationCandidateBatchDigest(current.candidates)
	if err != nil {
		return nil, nil, support.Internal("catalog", "FIELD_PRESENTATION_BATCH_IDENTITY_FAILED", "the candidate batch identity could not be encoded", err)
	}
	context := FieldPresentationContext{
		Project:                  projectid.Legacy(snapshot.Identity.Project),
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		SnapshotToken:            snapshot.Token, DatasetGeneration: snapshot.Identity.Generation,
		InventoryBuildID: build.BuildID, CoverageBuildID: build.BuildID,
		ExplorerID: current.owner.ExplorerID, OutputID: outputID,
		DraftVersion: current.owner.DraftVersion, DraftDigest: current.owner.DraftDigest,
		SourceNodeID: request.SourceNodeID, OccurrenceID: request.OccurrenceID,
		Route: capability.CloneConstructionRoute(current.route), StageID: current.stageID,
		PresentationRevision: SemanticFieldPresentationRevision, CandidateBatchDigest: batchDigest,
	}
	decisions := make([]FieldPresentationDecision, 0, len(current.candidates))
	groupReferences := make(map[string][]semanticPresentationReference, len(current.candidates))
	requestedReferences := make(map[string]semanticPresentationReference)
	for _, candidate := range current.candidates {
		if !candidate.Populated {
			decisions = append(decisions, FieldPresentationKeepRaw{Kind: FieldPresentationDecisionKeepRaw, CandidateID: candidate.ID, Reason: FieldPresentationCandidateNotPopulated})
			continue
		}
		if _, addable, addableErr := fieldPresentationRawCandidateChoice(ctx, authorized, snapshot, document.RootResourceType, candidate, current.route, proofCache); addableErr != nil {
			return nil, nil, addableErr
		} else if !addable {
			decisions = append(decisions, FieldPresentationKeepRaw{Kind: FieldPresentationDecisionKeepRaw, CandidateID: candidate.ID, Reason: FieldPresentationCandidateNotAddable})
			continue
		}
		coverage := coverageByPath[semanticCoverageCandidatePathKey(semanticarango.SemanticCoverageCandidatePath{
			ResourceType: candidate.ResourceType, CanonicalPath: candidate.FieldPath,
		})]
		if coverage.ResourceState != semantic.SemanticCoverageBuildComplete {
			decisions = append(decisions, FieldPresentationKeepRaw{
				Kind: FieldPresentationDecisionKeepRaw, CandidateID: candidate.ID, Reason: FieldPresentationIncompleteCoverage,
			})
			continue
		}
		switch coverage.Status {
		case semanticarango.SemanticCoverageCandidateKeepRaw:
			reason, ok := fieldPresentationCoverageReason(string(coverage.Reason))
			if !ok {
				return choices, fieldPresentationUnavailable(FieldPresentationIncompleteCoverage), nil
			}
			decisions = append(decisions, FieldPresentationKeepRaw{Kind: FieldPresentationDecisionKeepRaw, CandidateID: candidate.ID, Reason: reason})
		case semanticarango.SemanticCoverageCandidateUnavailable:
			decisions = append(decisions, FieldPresentationKeepRaw{Kind: FieldPresentationDecisionKeepRaw, CandidateID: candidate.ID, Reason: FieldPresentationIncompleteCoverage})
		case semanticarango.SemanticCoverageCandidateGroupable:
			if strings.TrimSpace(coverage.CoverageID) == "" || len(coverage.References) == 0 {
				return choices, fieldPresentationUnavailable(FieldPresentationIncompleteCoverage), nil
			}
			references := make([]semanticPresentationReference, 0, len(coverage.References))
			seen := make(map[string]struct{}, len(coverage.References))
			for _, reference := range coverage.References {
				resolvedReference, valid := semanticPresentationReferenceFromCoverage(reference, candidate, build.SourceKind)
				if !valid {
					return choices, fieldPresentationUnavailable(FieldPresentationIncompleteCoverage), nil
				}
				if _, duplicate := seen[resolvedReference.key]; duplicate {
					continue
				}
				seen[resolvedReference.key] = struct{}{}
				references = append(references, resolvedReference)
			}
			if len(references) == 0 {
				return choices, fieldPresentationUnavailable(FieldPresentationIncompleteCoverage), nil
			}
			newReferenceCount := 0
			for _, reference := range references {
				if _, exists := requestedReferences[reference.key]; !exists {
					newReferenceCount++
				}
			}
			if len(requestedReferences)+newReferenceCount > FieldPresentationReplacementLimit {
				decisions = append(decisions, FieldPresentationKeepRaw{Kind: FieldPresentationDecisionKeepRaw, CandidateID: candidate.ID, Reason: FieldPresentationReplacementUnavailable})
				continue
			}
			groupReferences[candidate.ID] = references
			for _, reference := range references {
				requestedReferences[reference.key] = reference
			}
			decisions = append(decisions, nil)
		default:
			return choices, fieldPresentationUnavailable(FieldPresentationIncompleteCoverage), nil
		}
	}
	if len(groupReferences) == 0 {
		return choices, FieldPresentationReady{Kind: FieldPresentationResultReady, Context: context, Decisions: decisions, Replacements: []SemanticInventoryItem{}}, nil
	}
	resolvedItems, replacementsComplete, err := b.resolveFieldPresentationReplacements(ctx, authorized, snapshot, document.RootResourceType, contextToken, build.BuildID, current, requestedReferences, proofCache)
	if err != nil {
		return nil, nil, err
	}
	if !replacementsComplete {
		return choices, fieldPresentationUnavailable(FieldPresentationReplacementUnavailable), nil
	}
	replacementByKey := resolvedItems
	groupedReferences := make(map[string]semanticPresentationReference)
	decisionIndex := 0
	for _, candidate := range current.candidates {
		if decisionIndex >= len(decisions) {
			return choices, fieldPresentationUnavailable(FieldPresentationIncompleteCoverage), nil
		}
		if references, group := groupReferences[candidate.ID]; group {
			identities := make([]semanticInventoryItemIdentity, 0, len(references))
			available := true
			for _, reference := range references {
				if _, found := replacementByKey[reference.key]; !found {
					available = false
					break
				}
				identities = append(identities, reference.identity)
			}
			if available {
				coverage := coverageByPath[semanticCoverageCandidatePathKey(semanticarango.SemanticCoverageCandidatePath{
					ResourceType: candidate.ResourceType, CanonicalPath: candidate.FieldPath,
				})]
				decisions[decisionIndex] = FieldPresentationGroupUnderSemantic{
					Kind: FieldPresentationDecisionGroup, CandidateID: candidate.ID, CoverageID: coverage.CoverageID,
					ReplacementIDs: identities, Access: FieldPresentationAccessSourceFields,
				}
				for _, reference := range references {
					groupedReferences[reference.key] = reference
				}
			} else {
				decisions[decisionIndex] = FieldPresentationKeepRaw{Kind: FieldPresentationDecisionKeepRaw, CandidateID: candidate.ID, Reason: FieldPresentationReplacementUnavailable}
			}
		}
		decisionIndex++
	}
	replacementKeys := make([]string, 0, len(groupedReferences))
	for key := range groupedReferences {
		replacementKeys = append(replacementKeys, key)
	}
	sort.Strings(replacementKeys)
	replacements := make([]SemanticInventoryItem, 0, len(replacementKeys))
	for _, key := range replacementKeys {
		replacements = append(replacements, replacementByKey[key])
	}
	return choices, FieldPresentationReady{Kind: FieldPresentationResultReady, Context: context, Decisions: decisions, Replacements: replacements}, nil
}

func (b *Service) fieldPresentationCurrentContext(
	ctx context.Context,
	request BrowseSemanticInventoryRequest,
	snapshot capability.Snapshot,
) (fieldPresentationCurrentContext, error) {
	var current fieldPresentationCurrentContext
	presentation := request.FieldPresentation
	if presentation == nil {
		return current, support.Malformed("catalog", "fieldPresentation request is required", nil)
	}
	owner, err := b.core.GetOwner(ctx, projectid.Canonical(request.Project), request.ExplorerID)
	if errors.Is(err, explorer.ErrNotFound) {
		return current, support.NotFound("catalog", "EXPLORER_NOT_FOUND", "Explorer not found", err)
	}
	if err != nil {
		return current, support.PersistenceFailure("catalog", "the current Explorer draft could not be loaded", err)
	}
	if owner == nil || owner.ExplorerID != request.ExplorerID || projectid.Canonical(owner.Project) != projectid.Canonical(request.Project) {
		return current, support.NotFound("catalog", "EXPLORER_NOT_FOUND", "Explorer not found", explorer.ErrNotFound)
	}
	if owner.DraftVersion != presentation.ExpectedDraftVersion || owner.DraftDigest != presentation.ExpectedDraftDigest {
		return current, support.Conflict("catalog", "STALE_FIELD_PRESENTATION_CONTEXT", "the Explorer draft changed; reload before requesting field presentation", nil, explorer.ErrDraftConflict)
	}
	if len(owner.DraftConfig) == 0 {
		return current, support.Conflict("catalog", "STALE_FIELD_PRESENTATION_CONTEXT", "field presentation requires the current saved draft", nil, nil)
	}
	workspace, err := ws.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return current, support.Conflict("catalog", "DRAFT_STATE_INVALID", "the saved Explorer draft is invalid", nil, err)
	}
	actualDigest, err := workspace.Digest()
	if err != nil || actualDigest != owner.DraftDigest {
		return current, support.Conflict("catalog", "STALE_FIELD_PRESENTATION_CONTEXT", "the saved workspace does not match its draft digest", nil, err)
	}
	document := findSemanticBrowseOutput(workspace, request.OutputID)
	if document == nil || document.RootResourceType != request.RowRoot {
		return current, support.NotFound("catalog", "OUTPUT_NOT_FOUND", "the output was not found for this row root", nil)
	}
	node, nodeFound := snapshot.Node(presentation.SourceNodeID)
	if !nodeFound {
		return current, support.Conflict("catalog", "STALE_FIELD_PRESENTATION_CONTEXT", "the source node is not present in the authorized snapshot", nil, nil)
	}
	occurrencePath, occurrenceFound := support.RoutePathToOccurrence(document.Route, presentation.OccurrenceID)
	if !occurrenceFound || len(occurrencePath) == 0 || occurrencePath[len(occurrencePath)-1].ResourceType != node.ResourceType {
		return current, support.Conflict("catalog", "STALE_FIELD_PRESENTATION_CONTEXT", "the selected occurrence is not present in the current output route", nil, nil)
	}
	candidates := make([]capability.Candidate, 0, len(presentation.CandidateIDs))
	for _, candidateID := range presentation.CandidateIDs {
		candidate, found := uniqueCapabilityCandidate(snapshot, candidateID)
		if !found || candidate.NodeID != presentation.SourceNodeID || candidate.ResourceType != node.ResourceType {
			return current, support.Conflict("catalog", "STALE_FIELD_PRESENTATION_CONTEXT", "a requested raw candidate is not part of the selected current source node", nil, nil)
		}
		candidates = append(candidates, candidate)
	}
	route, err := constructionRouteForOccurrence(snapshot, document.RootResourceType, presentation.SourceNodeID, document.Route, presentation.OccurrenceID)
	if err != nil {
		return current, support.Conflict("catalog", "STALE_FIELD_PRESENTATION_CONTEXT", "the exact selected output occurrence no longer resolves to the source node", nil, err)
	}
	stageID, stageSupported := "", true
	if presentation.StageID != nil {
		stageID = *presentation.StageID
		if stageID != recipe.ConstructionSourceProjectionID {
			found := false
			if document.Construction != nil {
				for _, step := range document.Construction.Steps {
					if step.ID == stageID {
						found = true
						break
					}
				}
			}
			if !found {
				return current, support.Conflict("catalog", "STALE_FIELD_PRESENTATION_CONTEXT", "the selected construction stage is not part of the current output", nil, nil)
			}
			stageSupported = false
		}
	} else if document.Construction != nil && len(document.Construction.Steps) > 0 {
		stageSupported = false
	}
	return fieldPresentationCurrentContext{
		owner: *owner, document: *document, sourceNodeID: presentation.SourceNodeID, candidates: candidates, route: route,
		stageID: stageID, stageSupported: stageSupported,
	}, nil
}
