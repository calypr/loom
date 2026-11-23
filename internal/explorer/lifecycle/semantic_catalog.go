package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/calypr/loom/internal/authscope"
	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	"github.com/calypr/loom/internal/catalog/fieldprofile"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/projectid"
	"github.com/calypr/loom/internal/semantic"
)

const (
	semanticFieldPresentationRevision = "semantic-field-presentation/v1"
	fieldPresentationCandidateLimit   = 50
	fieldPresentationReplacementLimit = 100
)

type fieldPresentationResult interface {
	fieldPresentationResult()
}

type FieldPresentationUnavailable struct {
	Kind   FieldPresentationResultKind `json:"kind"`
	Reason FieldPresentationReason     `json:"reason"`
}

func (FieldPresentationUnavailable) fieldPresentationResult() {}

type FieldPresentationReady struct {
	Kind         FieldPresentationResultKind `json:"kind"`
	Context      FieldPresentationContext    `json:"context"`
	Decisions    []fieldPresentationDecision `json:"decisions"`
	Replacements []SemanticInventoryItem     `json:"replacements"`
}

func (FieldPresentationReady) fieldPresentationResult() {}

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

type fieldPresentationDecision interface {
	fieldPresentationDecision()
}

type FieldPresentationKeepRaw struct {
	Kind        FieldPresentationDecisionKind `json:"kind"`
	CandidateID string                        `json:"candidateId"`
	Reason      FieldPresentationReason       `json:"reason"`
}

func (FieldPresentationKeepRaw) fieldPresentationDecision() {}

type semanticInventoryItemIdentity interface {
	semanticInventoryItemIdentity()
}

type ExactSemanticInventoryItemIdentity struct {
	Kind      SemanticInventoryItemIdentityKind `json:"kind"`
	ConceptID string                            `json:"conceptId"`
	BindingID string                            `json:"bindingId"`
}

func (ExactSemanticInventoryItemIdentity) semanticInventoryItemIdentity() {}

type FamilySemanticInventoryItemIdentity struct {
	Kind      SemanticInventoryItemIdentityKind `json:"kind"`
	FamilyID  string                            `json:"familyId"`
	BindingID string                            `json:"bindingId"`
}

func (FamilySemanticInventoryItemIdentity) semanticInventoryItemIdentity() {}

type FieldPresentationGroupUnderSemantic struct {
	Kind           FieldPresentationDecisionKind   `json:"kind"`
	CandidateID    string                          `json:"candidateId"`
	CoverageID     string                          `json:"coverageId"`
	ReplacementIDs []semanticInventoryItemIdentity `json:"replacementIds"`
	Access         FieldPresentationAccess         `json:"access"`
}

func (FieldPresentationGroupUnderSemantic) fieldPresentationDecision() {}

type BrowseSemanticInventoryResponse struct {
	FrameID            string                       `json:"frameId,omitempty"`
	FrameSource        *authoringv2.FrameDefinition `json:"frameSource,omitempty"`
	ContextToken       string                       `json:"contextToken"`
	BuildID            string                       `json:"buildId"`
	State              semantic.State               `json:"state"`
	SourceAvailability string                       `json:"sourceAvailability"`
	Entries            []SemanticInventoryItem      `json:"entries"`
	NextCursor         string                       `json:"nextCursor,omitempty"`
	FieldPresentation  fieldPresentationResult      `json:"fieldPresentation,omitempty"`
}

type semanticBrowseCursor struct {
	Context        string `json:"context"`
	Page           string `json:"page"`
	SelectionMode  string `json:"selectionMode,omitempty"`
	FrameID        string `json:"frameId,omitempty"`
	SourceChoiceID string `json:"sourceChoiceId,omitempty"`
	OutputID       string `json:"outputId,omitempty"`
}

func (s *Service) BrowseSemanticInventory(ctx context.Context, req BrowseSemanticInventoryRequest) (BrowseSemanticInventoryResponse, error) {
	browse := s.fieldBrowse()
	var result BrowseSemanticInventoryResponse
	if req.FieldPresentation != nil {
		if err := req.FieldPresentation.Validate(); err != nil {
			return result, malformed("catalog", err.Error(), err)
		}
		if req.OutputID == "" || req.OutputID != strings.TrimSpace(req.OutputID) {
			return result, malformed("catalog", "outputId is required and must be exact with fieldPresentation", nil)
		}
		if req.FrameID != "" || req.SourceChoiceID != "" {
			return result, malformed("catalog", "fieldPresentation cannot be combined with frameId or sourceChoiceId", nil)
		}
	}
	if req.SelectionMode == "" {
		req.SelectionMode = string(semantic.ExactConcept)
	}
	if req.SelectionMode != string(semantic.ExactConcept) && req.SelectionMode != string(semantic.SystemVersionCategory) {
		return result, malformed("catalog", "selectionMode must be EXACT_CONCEPT or SYSTEM_VERSION_CATEGORY", nil)
	}
	if strings.TrimSpace(req.Project) == "" || strings.TrimSpace(req.ExplorerID) == "" || req.SnapshotToken == "" || req.RowRoot == "" || req.Limit < 0 || req.Limit > semantic.PageLimit || len(req.Query) > 256 || len(req.Cursor) > 4096 {
		return result, malformed("catalog", "project, explorer, snapshotToken, rowRoot, and a page limit of at most 50 are required", nil)
	}
	if s.config.SemanticInventory == nil || s.config.Capability.ForCompilation == nil {
		return result, unavailable("catalog", "CATALOG_UNAVAILABLE", "semantic inventory browsing is not configured", nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, req.Project, req.SnapshotToken)
	if errors.Is(err, capability.ErrStaleSnapshot) || errors.Is(err, capability.ErrSnapshotUnavailable) {
		return result, conflict("catalog", "STALE_CATALOG_SNAPSHOT", "reload the catalog before browsing", nil, err)
	}
	if err != nil {
		return result, capabilityResolutionFailure("catalog", "reload the catalog before browsing", err)
	}
	snapshot := authorized.Snapshot
	if snapshot.ValidateToken(req.SnapshotToken) != nil || projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(req.Project) || snapshot.Identity.Generation == "" {
		return result, conflict("catalog", "STALE_CATALOG_SNAPSHOT", "reload the catalog before browsing", nil, nil)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, conflict("catalog", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	rootAllowed := false
	resourceAllowed := req.ResourceType == ""
	for _, node := range snapshot.Nodes {
		rootAllowed = rootAllowed || node.ResourceType == req.RowRoot && node.RowRootEligible
		resourceAllowed = resourceAllowed || node.ResourceType == req.ResourceType
	}
	if !rootAllowed || !resourceAllowed {
		return result, malformed("catalog", "the row root or resource filter is not available in this snapshot", nil)
	}
	if req.FrameID != "" && req.SourceChoiceID != "" {
		return result, malformed("catalog", "frameId and sourceChoiceId are mutually exclusive", nil)
	}
	if req.SelectionMode == string(semantic.SystemVersionCategory) && (req.FrameID != "" || req.SourceChoiceID != "") {
		return result, malformed("catalog", "system-version category browsing is available only outside a saved frame or coded source selection", nil)
	}
	var frame *authoringv2.FrameDefinition
	var codedFamily *frameSourceFamilyCandidate
	var codedRoute []capability.ConstructionRouteStep
	var outputDocument *authoringv2.Document
	var presentationCurrent *fieldPresentationCurrentContext
	bindingFilter := ""
	if req.FrameID != "" {
		if strings.TrimSpace(req.OutputID) == "" || req.OutputID != strings.TrimSpace(req.OutputID) {
			return result, malformed("catalog", "outputId is required with frameId", nil)
		}
		workspace, workspaceErr := s.currentWorkspace(ctx, req.Project, req.ExplorerID)
		if workspaceErr != nil {
			return result, workspaceErr
		}
		documentIndex := -1
		for index := range workspace.Documents {
			if workspace.Documents[index].Output.ID == req.OutputID {
				documentIndex = index
				break
			}
		}
		if documentIndex < 0 || workspace.Documents[documentIndex].RootResourceType != req.RowRoot {
			return result, notFound("catalog", "OUTPUT_NOT_FOUND", "the output was not found for this row root", nil)
		}
		for _, candidate := range workspace.Documents[documentIndex].Frames {
			if candidate.ID == req.FrameID {
				copy := candidate
				copy.Route = capability.CloneConstructionRoute(candidate.Route)
				copy.Source.ChoiceArms = append([]string(nil), candidate.Source.ChoiceArms...)
				frame = &copy
			}
		}
		if frame == nil {
			return result, notFound("catalog", "FRAME_NOT_FOUND", "the saved frame source was not found for this row root", nil)
		}
		if req.ResourceType != "" && req.ResourceType != frame.Source.ResourceType {
			return result, malformed("catalog", "resourceType must match the saved frame source", nil)
		}
		req.ResourceType = frame.Source.ResourceType
		bindingFilter = frame.Source.BindingID
		if isPriorPrimaryOnlySemanticFrameFamily(frame.Source) {

			bindingFilter = ""
		}
		result.FrameID, result.FrameSource = frame.ID, frame
	}
	if req.SourceChoiceID != "" {
		if strings.TrimSpace(req.OutputID) == "" || req.OutputID != strings.TrimSpace(req.OutputID) {
			return result, malformed("catalog", "outputId is required with sourceChoiceId", nil)
		}
		workspace, workspaceErr := s.currentWorkspace(ctx, req.Project, req.ExplorerID)
		if workspaceErr != nil {
			return result, workspaceErr
		}
		document := findSemanticOutput(workspace, req.OutputID)
		if document == nil || document.Route.OccurrenceID != authoringv2.RootOccurrenceID || document.RootResourceType != req.RowRoot {
			return result, notFound("catalog", "OUTPUT_NOT_FOUND", "the output was not found for this row root", nil)
		}
		identity, decodeErr := capability.DecodeConstructionChoiceID(req.SourceChoiceID)
		if decodeErr != nil || identity.SnapshotToken != snapshot.Token {
			return result, conflict("catalog", "STALE_CONSTRUCTION_CHOICE", "reload the coded source for the current authorized snapshot", nil, decodeErr)
		}
		family, route, verifyErr := s.reauthorizeFrameSourceChoice(ctx, req.Project, req.ExplorerID, req.RowRoot, req.OutputID, req.SourceChoiceID, authorized, identity)
		if verifyErr != nil {
			return result, verifyErr
		}
		if len(route) != 0 || family.Family.ResourceType != req.RowRoot {
			return result, unprocessable("catalog", "CODED_PIVOT_SOURCE_UNSUPPORTED", "coded Pivot currently requires a direct root source", nil)
		}
		if req.ResourceType != "" && req.ResourceType != family.Family.ResourceType {
			return result, malformed("catalog", "resourceType must match sourceChoiceId", nil)
		}
		req.ResourceType = family.Family.ResourceType
		bindingFilter = family.Family.BindingID
		codedFamily = &family
		codedRoute = route
	}
	if req.OutputID != "" && req.OutputID != strings.TrimSpace(req.OutputID) {
		return result, malformed("catalog", "outputId must be an exact identifier", nil)
	}
	if req.OutputID != "" && frame == nil && codedFamily == nil {
		if req.FieldPresentation != nil {
			current, currentErr := browse.fieldPresentationCurrentContext(ctx, req, snapshot)
			if currentErr != nil {
				return result, currentErr
			}
			presentationCurrent = &current
			document := current.document
			outputDocument = &document
		} else {
			workspace, workspaceErr := s.currentWorkspace(ctx, req.Project, req.ExplorerID)
			if workspaceErr != nil {
				return result, workspaceErr
			}
			document := findSemanticOutput(workspace, req.OutputID)
			if document == nil || document.RootResourceType != req.RowRoot {
				return result, notFound("catalog", "OUTPUT_NOT_FOUND", "the output was not found for this row root", nil)
			}
			outputDocument = document
		}
	}
	var cursor semanticBrowseCursor
	if req.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(req.Cursor)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Context == "" || cursor.Page == "" || semanticBrowseCursorSelectionMode(cursor.SelectionMode) != req.SelectionMode || cursor.FrameID != req.FrameID || cursor.SourceChoiceID != req.SourceChoiceID || cursor.OutputID != req.OutputID {
			return result, conflict("catalog", "STALE_CATALOG_CURSOR", "restart catalog search with an empty cursor", nil, nil)
		}
	}
	unrestricted := authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	page, err := s.config.SemanticInventory(ctx, semantic.PageOptions{
		Project: projectid.Legacy(snapshot.Identity.Project), DatasetGeneration: snapshot.Identity.Generation,
		AuthResourcePathsUnrestricted: &unrestricted, AuthResourcePaths: authorized.Scope.AuthResourcePaths,
		ResourceType: req.ResourceType, BindingID: bindingFilter, Query: req.Query, Cursor: cursor.Page, Limit: req.Limit,
		SelectionMode: semantic.SelectionMode(req.SelectionMode),
	})
	if errors.Is(err, semantic.ErrSemanticInventoryCursorMismatch) {
		return result, conflict("catalog", "STALE_CATALOG_CURSOR", "restart catalog search with an empty cursor", nil, err)
	}
	if err != nil {
		return result, persistenceFailure("catalog", "semantic inventory could not be read", err)
	}
	result.ContextToken, err = semanticInventoryContextToken(snapshot, req.ExplorerID, req.RowRoot, page.Build.BuildID)
	if err != nil {
		return result, err
	}
	if req.Cursor != "" && cursor.Context != result.ContextToken {
		return BrowseSemanticInventoryResponse{}, conflict("catalog", "STALE_CATALOG_CURSOR", "catalog context changed; restart search", nil, nil)
	}
	result.BuildID, result.State = page.Build.BuildID, page.State
	result.SourceAvailability = page.Build.SourceAvailability
	if result.SourceAvailability == "" {
		result.SourceAvailability = semantic.SourceAvailabilityUnknown
	}
	categoryEntries := make([]semantic.Entry, 0, len(page.Families))
	familyByChoice := make(map[string]semantic.Family, len(page.Families))
	for _, family := range page.Families {
		entry := semanticInventoryEntryForFamily(family)
		key := semanticChoiceKey(entry.ConceptID, entry.BindingID)
		categoryEntries = append(categoryEntries, entry)
		familyByChoice[key] = family
	}
	entries := make([]semantic.Entry, 0, len(page.Entries)+len(categoryEntries))
	entries = append(entries, page.Entries...)
	entries = append(entries, categoryEntries...)
	var outputChoices map[string]capability.ConstructionChoice
	if outputDocument != nil {
		if presentationCurrent != nil {
			outputChoices, result.FieldPresentation, err = browse.semanticInventoryPresentationForOutput(
				ctx, authorized, snapshot, *outputDocument, req.OutputID, result.ContextToken,
				page.Build, page.Entries, page.Families, *presentationCurrent, *req.FieldPresentation,
			)
		} else {
			outputChoices, err = semanticInventoryChoicesForOutput(ctx, authorized, snapshot, *outputDocument, req.OutputID, result.ContextToken, result.BuildID, page.Entries, page.Families)
		}
		if err != nil {
			return result, err
		}
	}
	result.Entries = make([]SemanticInventoryItem, 0, len(entries))
	for _, entry := range entries {
		observation := entry.Observation
		family, isCategory := familyByChoice[semanticChoiceKey(entry.ConceptID, entry.BindingID)]
		plan := authoringv2.ResolveSemanticSelectionPlan(observation)
		if frame != nil {
			projected, projectedPlan, ok := semanticEntryForFrameFamily(entry, frame.Source)
			if !ok {
				continue
			}
			entry, observation, plan = projected, projected.Observation, projectedPlan
		}
		item := SemanticInventoryItem{
			ConceptID: entry.ConceptID, BindingID: entry.BindingID, ResourceType: observation.Source.Type,
			SourcePath: observation.Source.Path, System: observation.Key.System, Code: observation.Key.Code,
			CodingVersion: observation.Key.Version, Display: observation.Key.Display,
			ValueSelector: observation.Value.Selector, ValueType: observation.Value.Type,
			OwningScope: observation.OwningScope, Occurrences: observation.Population,
			Examples: append([]string(nil), observation.Examples...), ExamplesTruncated: observation.ExamplesTruncated,
			ObservedUnits: append([]string(nil), observation.ObservedUnits...), ObservedUnitsTruncated: observation.ObservedUnitsTruncated,
			Completeness: observation.Completeness,
			Readiness:    plan.Readiness,
		}
		if observation.RuleHint == "IDENTIFIER_SYSTEM_VALUE" {
			item.Display = semanticIdentifierLabel(observation.Key.System)
		}
		if isCategory {
			item.SelectionMode = string(semantic.SystemVersionCategory)
			item.FamilyID = family.FamilyID
			item.Display = semanticCategoryLabel(family, capability.Candidate{})
			item.Code = ""
			candidate, ok := semanticConstructionCandidate(snapshot, observation)
			if !ok {
				item.Readiness = compilerProofUnavailable()
			} else {
				item.Display = semanticCategoryLabel(family, candidate)
			}
		}
		if outputDocument != nil {
			choice, ok := outputChoices[semanticChoiceKey(entry.ConceptID, entry.BindingID)]
			if !ok {
				if presentationCurrent == nil {
					continue
				}
				result.Entries = append(result.Entries, item)
				continue
			}
			item.ConstructionChoice = &choice
			result.Entries = append(result.Entries, item)
			continue
		}
		if plan.Readiness.Addable() {
			if candidate, ok := semanticConstructionCandidate(snapshot, observation); ok {
				if codedFamily != nil && (candidate.ID != codedFamily.Candidate.ID || candidate.NodeID != codedFamily.Candidate.NodeID || candidate.FieldPath != codedFamily.Candidate.FieldPath) {
					continue
				}
				var route []capability.ConstructionRouteStep
				if frame != nil {
					if !semanticEntryMatchesFrame(entry, *frame) {
						continue
					}
					resolvedRoute, routeErr := reauthorizeConstructionRoute(snapshot, req.RowRoot, candidate.NodeID, frame.Route)
					if routeErr != nil {
						continue
					}
					route = resolvedRoute
				} else if codedFamily != nil {
					if observation.Key.Version != "" || !semanticEntryMatchesFrameFamily(entry, codedFamily.Family) {
						continue
					}
					route = codedRoute
				}
				ownerRecordsProved := !isCategory && frame == nil && proveOwnerRecordsForRoute(ctx, authorized, req.RowRoot, observation, route)
				var choice capability.ConstructionChoice
				var choiceErr error
				if isCategory {
					choice, choiceErr = semanticCategoryConstructionChoiceForRoute(snapshot, result.ContextToken, result.BuildID, route, family, candidate)
				} else {
					choice, choiceErr = semanticInventoryConstructionChoiceForRoute(snapshot, result.ContextToken, result.BuildID, route, entry, candidate, ownerRecordsProved)
				}
				if choiceErr == nil {
					if frame != nil && !constructionChoiceSupports(choice, frame.Form) {
						continue
					}
					item.ConstructionChoice = &choice
				} else {
					if frame != nil {
						continue
					}
					item.Readiness = compilerProofUnavailable()
				}
			} else {
				if frame != nil {
					continue
				}
				item.Readiness = compilerProofUnavailable()
			}
		} else if frame != nil {
			continue
		}
		result.Entries = append(result.Entries, item)
	}
	if page.NextCursor != "" {
		raw, err := json.Marshal(semanticBrowseCursor{Context: result.ContextToken, Page: page.NextCursor, SelectionMode: req.SelectionMode, FrameID: req.FrameID, SourceChoiceID: req.SourceChoiceID, OutputID: req.OutputID})
		if err != nil {
			return BrowseSemanticInventoryResponse{}, err
		}
		result.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return result, nil
}

func semanticBrowseCursorSelectionMode(mode string) string {
	if mode == "" {
		return string(semantic.ExactConcept)
	}
	return mode
}

type fieldPresentationCurrentContext struct {
	owner          explorer.Explorer
	document       authoringv2.Document
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

func (b *fieldBrowse) semanticInventoryPresentationForOutput(
	ctx context.Context,
	authorized ports.AuthorizedCapability,
	snapshot capability.Snapshot,
	document authoringv2.Document,
	outputID, contextToken string,
	build semantic.Build,
	entries []semantic.Entry,
	families []semantic.Family,
	current fieldPresentationCurrentContext,
	request FieldPresentationRequest,
) (map[string]capability.ConstructionChoice, fieldPresentationResult, error) {
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
	coveragePaths := make([]catalogarango.SemanticCoverageCandidatePath, 0, len(current.candidates))
	coveragePathKeys := make(map[string]struct{}, len(current.candidates))
	for _, candidate := range current.candidates {
		path := catalogarango.SemanticCoverageCandidatePath{ResourceType: candidate.ResourceType, CanonicalPath: candidate.FieldPath}
		key := semanticCoverageCandidatePathKey(path)
		if _, exists := coveragePathKeys[key]; exists {
			continue
		}
		coveragePathKeys[key] = struct{}{}
		coveragePaths = append(coveragePaths, path)
	}
	unrestricted := authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	lookup, err := b.semanticCoverage(ctx, catalogarango.SemanticCoverageLookupOptions{
		Project: projectid.Legacy(snapshot.Identity.Project), DatasetGeneration: snapshot.Identity.Generation,
		BuildID: build.BuildID, SchemaIdentity: snapshot.Identity.SchemaDigest, SourceKind: build.SourceKind,
		AuthResourcePaths:             append([]string(nil), authorized.Scope.AuthResourcePaths...),
		AuthResourcePathsUnrestricted: unrestricted, Candidates: coveragePaths,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return nil, nil, unavailable("catalog", "FIELD_PRESENTATION_LOOKUP_INTERRUPTED", "semantic field coverage lookup was interrupted", err)
		}
		return nil, nil, persistenceFailure("catalog", "semantic field coverage could not be read", err)
	}
	if lookup.State != catalogarango.SemanticCoverageBuildComplete && lookup.State != catalogarango.SemanticCoverageBuildPartial {
		reason := FieldPresentationCoverageUnavailable
		if lookup.State == catalogarango.SemanticCoverageBuildBuilding || lookup.State == catalogarango.SemanticCoverageBuildFailed {
			reason = FieldPresentationIncompleteCoverage
		}
		return choices, fieldPresentationUnavailable(reason), nil
	}
	coverageByPath := make(map[string]catalogarango.SemanticCoverageCandidateResult, len(lookup.Candidates))
	for _, candidate := range lookup.Candidates {
		key := semanticCoverageCandidatePathKey(catalogarango.SemanticCoverageCandidatePath{
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
		return nil, nil, internal("catalog", "FIELD_PRESENTATION_BATCH_IDENTITY_FAILED", "the candidate batch identity could not be encoded", err)
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
		PresentationRevision: semanticFieldPresentationRevision, CandidateBatchDigest: batchDigest,
	}
	decisions := make([]fieldPresentationDecision, 0, len(current.candidates))
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
		coverage := coverageByPath[semanticCoverageCandidatePathKey(catalogarango.SemanticCoverageCandidatePath{
			ResourceType: candidate.ResourceType, CanonicalPath: candidate.FieldPath,
		})]
		if coverage.ResourceState != catalogarango.SemanticCoverageBuildComplete {
			decisions = append(decisions, FieldPresentationKeepRaw{
				Kind: FieldPresentationDecisionKeepRaw, CandidateID: candidate.ID, Reason: FieldPresentationIncompleteCoverage,
			})
			continue
		}
		switch coverage.Status {
		case catalogarango.SemanticCoverageCandidateKeepRaw:
			reason, ok := fieldPresentationCoverageReason(string(coverage.Reason))
			if !ok {
				return choices, fieldPresentationUnavailable(FieldPresentationIncompleteCoverage), nil
			}
			decisions = append(decisions, FieldPresentationKeepRaw{Kind: FieldPresentationDecisionKeepRaw, CandidateID: candidate.ID, Reason: reason})
		case catalogarango.SemanticCoverageCandidateUnavailable:
			decisions = append(decisions, FieldPresentationKeepRaw{Kind: FieldPresentationDecisionKeepRaw, CandidateID: candidate.ID, Reason: FieldPresentationIncompleteCoverage})
		case catalogarango.SemanticCoverageCandidateGroupable:
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
			if len(requestedReferences)+newReferenceCount > fieldPresentationReplacementLimit {
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
				coverage := coverageByPath[semanticCoverageCandidatePathKey(catalogarango.SemanticCoverageCandidatePath{
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

func semanticInventoryChoicesForExactOccurrence(
	ctx context.Context,
	authorized ports.AuthorizedCapability,
	snapshot capability.Snapshot,
	rootResourceType string,
	contextToken, buildID string,
	entries []semantic.Entry,
	families []semantic.Family,
	sourceNodeID string,
	route []capability.ConstructionRouteStep,
	proofCache map[string]semanticCandidateRouteProof,
) (map[string]capability.ConstructionChoice, error) {
	choices := make(map[string]capability.ConstructionChoice)
	add := func(entry semantic.Entry, family *semantic.Family) error {
		if !authoringv2.ResolveSemanticSelectionPlan(entry.Observation).Readiness.Addable() {
			return nil
		}
		candidate, found := semanticConstructionCandidate(snapshot, entry.Observation)
		if !found || candidate.NodeID != sourceNodeID || !candidate.Populated {
			return nil
		}
		proof, err := proveSemanticCandidateForRoute(ctx, authorized, rootResourceType, candidate, route, proofCache)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
				return unavailable("catalog", "CONSTRUCTION_ROUTE_PROOF_INTERRUPTED", "compiler proof for the exact field-presentation route was interrupted", err)
			}
			return internal("catalog", "CONSTRUCTION_ROUTE_PROOF_FAILED", "compiler proof for the exact field-presentation route failed", err)
		}
		fieldChoice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, route, proof.candidate)
		if err != nil || len(fieldChoice.Options) == 0 {
			return nil
		}
		var choice capability.ConstructionChoice
		ownerRecordsProved := family == nil && proveOwnerRecordsForRoute(ctx, authorized, rootResourceType, entry.Observation, route)
		if family != nil {
			choice, err = semanticCategoryConstructionChoiceForRoute(snapshot, contextToken, buildID, route, *family, proof.candidate)
		} else {
			choice, err = semanticInventoryConstructionChoiceForRoute(snapshot, contextToken, buildID, route, entry, proof.candidate, ownerRecordsProved)
		}
		if err == nil && len(choice.Options) > 0 && semanticChoiceFieldOptionsMatch(choice, fieldChoice) {
			choices[semanticChoiceKey(entry.ConceptID, entry.BindingID)] = choice
		}
		return nil
	}
	for _, entry := range entries {
		if err := add(entry, nil); err != nil {
			return nil, err
		}
	}
	for index := range families {
		family := families[index]
		if err := add(semanticInventoryEntryForFamily(family), &family); err != nil {
			return nil, err
		}
	}
	return choices, nil
}

func proveSemanticCandidateForRoute(
	ctx context.Context,
	authorized ports.AuthorizedCapability,
	rootResourceType string,
	candidate capability.Candidate,
	route []capability.ConstructionRouteStep,
	cache map[string]semanticCandidateRouteProof,
) (semanticCandidateRouteProof, error) {
	key := semanticCandidateRouteProofCacheKey(candidate, route)
	if proof, ok := cache[key]; ok {
		return proof, nil
	}
	proof := semanticCandidateRouteProof{candidate: candidate, route: capability.CloneConstructionRoute(route), ok: true}
	if len(route) > 0 || strings.HasPrefix(candidate.ID, "schema_field.") {
		proven, err := proveConstructionCandidate(ctx, authorized, rootResourceType, candidate, route)
		if err != nil {
			return semanticCandidateRouteProof{}, err
		}
		proof.candidate = proven
	}
	cache[key] = proof
	return proof, nil
}

func fieldPresentationUnavailable(reason FieldPresentationReason) fieldPresentationResult {
	return FieldPresentationUnavailable{Kind: FieldPresentationResultUnavailable, Reason: reason}
}

func semanticCoverageCandidatePathKey(path catalogarango.SemanticCoverageCandidatePath) string {
	return path.ResourceType + "\x00" + path.CanonicalPath
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
			return capability.ConstructionChoice{}, false, unavailable("catalog", "FIELD_PRESENTATION_CANDIDATE_PROOF_INTERRUPTED", "compiler proof for the raw field was interrupted", err)
		}
		return capability.ConstructionChoice{}, false, internal("catalog", "FIELD_PRESENTATION_CANDIDATE_PROOF_FAILED", "the raw field compiler proof failed", err)
	}
	choice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, route, proof.candidate)
	if err != nil || len(choice.Options) == 0 {
		return capability.ConstructionChoice{}, false, nil
	}
	return choice, true, nil
}

func (b *fieldBrowse) resolveFieldPresentationReplacements(
	ctx context.Context,
	authorized ports.AuthorizedCapability,
	snapshot capability.Snapshot,
	rootResourceType, semanticContextToken, buildID string,
	current fieldPresentationCurrentContext,
	references map[string]semanticPresentationReference,
	proofCache map[string]semanticCandidateRouteProof,
) (map[string]SemanticInventoryItem, bool, error) {
	items := make(map[string]SemanticInventoryItem, len(references))
	if len(references) == 0 {
		return items, true, nil
	}
	if b.resolveSelections == nil {
		return items, false, nil
	}
	keys := make([]string, 0, len(references))
	for key := range references {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	exactReferences := make([]semantic.Reference, 0, len(keys))
	familyReferences := make([]semantic.FamilyReference, 0, len(keys))
	for _, key := range keys {
		reference := references[key]
		if reference.exact != nil {
			exactReferences = append(exactReferences, *reference.exact)
		}
		if reference.family != nil {
			familyReferences = append(familyReferences, *reference.family)
		}
	}
	unrestricted := authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	resolved, err := b.resolveSelections(ctx, semantic.ResolveOptions{
		Project: projectid.Legacy(snapshot.Identity.Project), DatasetGeneration: snapshot.Identity.Generation,
		AuthResourcePathsUnrestricted: &unrestricted,
		AuthResourcePaths:             append([]string(nil), authorized.Scope.AuthResourcePaths...),
		References:                    exactReferences, FamilyReferences: familyReferences,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return nil, false, unavailable("catalog", "FIELD_PRESENTATION_REPLACEMENT_READ_INTERRUPTED", "semantic replacement resolution was interrupted", err)
		}
		return nil, false, persistenceFailure("catalog", "authorized semantic replacements could not be read", err)
	}
	if resolved.State != semantic.Complete || resolved.Build.State != semantic.Complete || resolved.Build.BuildID != buildID {
		return items, false, nil
	}
	exactEntries := make(map[string][]semantic.Entry, len(exactReferences))
	for _, entry := range resolved.Entries {
		key := "EXACT\x00" + entry.BindingID + "\x00" + entry.ConceptID
		if _, requested := references[key]; requested {
			exactEntries[key] = append(exactEntries[key], entry)
		}
	}
	familyEntries := make(map[string][]semantic.Family, len(familyReferences))
	for _, family := range resolved.Families {
		key := "FAMILY\x00" + family.BindingID + "\x00" + family.FamilyID
		if _, requested := references[key]; requested {
			familyEntries[key] = append(familyEntries[key], family)
		}
	}
	for _, key := range keys {
		reference := references[key]
		var entry semantic.Entry
		var family *semantic.Family
		if reference.exact != nil {
			matches := exactEntries[key]
			if len(matches) != 1 {
				continue
			}
			entry = matches[0]
		} else if reference.family != nil {
			matches := familyEntries[key]
			if len(matches) != 1 {
				continue
			}
			copy := matches[0]
			family = &copy
			entry = semanticInventoryEntryForFamily(copy)
		} else {
			continue
		}
		item, available, _, itemErr := b.semanticFieldPresentationReplacement(
			ctx, authorized, snapshot, rootResourceType, semanticContextToken, buildID,
			current, entry, family, proofCache,
		)
		if itemErr != nil {
			return nil, false, itemErr
		}
		if available {
			items[key] = item
		}
	}
	return items, true, nil
}

func (b *fieldBrowse) semanticFieldPresentationReplacement(
	ctx context.Context,
	authorized ports.AuthorizedCapability,
	snapshot capability.Snapshot,
	rootResourceType, semanticContextToken, buildID string,
	current fieldPresentationCurrentContext,
	entry semantic.Entry,
	family *semantic.Family,
	proofCache map[string]semanticCandidateRouteProof,
) (SemanticInventoryItem, bool, FieldPresentationReason, error) {
	observation := entry.Observation
	if !fieldPresentationRuleVisibleInCodedView(observation.RuleHint) {
		return SemanticInventoryItem{}, false, FieldPresentationReplacementUnavailable, nil
	}
	if observation.Population <= 0 {
		return SemanticInventoryItem{}, false, FieldPresentationCandidateNotPopulated, nil
	}
	if !authoringv2.ResolveSemanticSelectionPlan(observation).Readiness.Addable() {
		return SemanticInventoryItem{}, false, FieldPresentationReplacementUnavailable, nil
	}
	candidate, found := semanticConstructionCandidate(snapshot, observation)
	if !found || candidate.NodeID != current.sourceNodeID || !candidate.Populated {
		return SemanticInventoryItem{}, false, FieldPresentationReplacementUnavailable, nil
	}
	proof, err := proveSemanticCandidateForRoute(ctx, authorized, rootResourceType, candidate, current.route, proofCache)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return SemanticInventoryItem{}, false, FieldPresentationReplacementUnavailable, unavailable("catalog", "FIELD_PRESENTATION_REPLACEMENT_PROOF_INTERRUPTED", "compiler proof for a semantic replacement was interrupted", err)
		}
		return SemanticInventoryItem{}, false, FieldPresentationReplacementUnavailable, internal("catalog", "FIELD_PRESENTATION_REPLACEMENT_PROOF_FAILED", "compiler proof for a semantic replacement failed", err)
	}
	fieldChoice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, current.route, proof.candidate)
	if err != nil || len(fieldChoice.Options) == 0 {
		return SemanticInventoryItem{}, false, FieldPresentationSourceFieldsInaccessible, nil
	}
	ownerRecordsProved := family == nil && proveOwnerRecordsForRoute(ctx, authorized, rootResourceType, observation, current.route)
	var choice capability.ConstructionChoice
	if family != nil {
		choice, err = semanticCategoryConstructionChoiceForRoute(snapshot, semanticContextToken, buildID, current.route, *family, proof.candidate)
	} else {
		choice, err = semanticInventoryConstructionChoiceForRoute(snapshot, semanticContextToken, buildID, current.route, entry, proof.candidate, ownerRecordsProved)
	}
	if err != nil || !semanticChoiceFieldOptionsMatch(choice, fieldChoice) || len(choice.Options) == 0 {
		return SemanticInventoryItem{}, false, FieldPresentationSourceFieldsInaccessible, nil
	}
	item := semanticInventoryItemForPresentation(entry, family, proof.candidate, choice)
	return item, true, "", nil
}

func fieldPresentationRuleVisibleInCodedView(ruleHint string) bool {
	switch ruleHint {
	case semantic.RuleHintCodedValueV1, semantic.RuleHintCodedConceptV1, semantic.RuleHintCodedCategoryV1:
		return true
	default:
		return false
	}
}

func semanticInventoryItemForPresentation(
	entry semantic.Entry,
	family *semantic.Family,
	candidate capability.Candidate,
	choice capability.ConstructionChoice,
) SemanticInventoryItem {
	observation := entry.Observation
	plan := authoringv2.ResolveSemanticSelectionPlan(observation)
	item := SemanticInventoryItem{
		ConceptID: entry.ConceptID, BindingID: entry.BindingID, ResourceType: observation.Source.Type,
		SourcePath: observation.Source.Path, System: observation.Key.System, Code: observation.Key.Code,
		CodingVersion: observation.Key.Version, Display: observation.Key.Display,
		ValueSelector: observation.Value.Selector, ValueType: observation.Value.Type,
		OwningScope: observation.OwningScope, Occurrences: observation.Population,
		Examples: append([]string(nil), observation.Examples...), ExamplesTruncated: observation.ExamplesTruncated,
		ObservedUnits: append([]string(nil), observation.ObservedUnits...), ObservedUnitsTruncated: observation.ObservedUnitsTruncated,
		Completeness: observation.Completeness, Readiness: plan.Readiness, ConstructionChoice: &choice,
	}
	if family != nil {
		item.SelectionMode = string(semantic.SystemVersionCategory)
		item.FamilyID = family.FamilyID
		item.Display = semanticCategoryLabel(*family, candidate)
		item.Code = ""
	} else if observation.RuleHint == "IDENTIFIER_SYSTEM_VALUE" {
		item.Display = semanticIdentifierLabel(observation.Key.System)
	}
	return item
}

func (b *fieldBrowse) fieldPresentationCurrentContext(
	ctx context.Context,
	request BrowseSemanticInventoryRequest,
	snapshot capability.Snapshot,
) (fieldPresentationCurrentContext, error) {
	var current fieldPresentationCurrentContext
	presentation := request.FieldPresentation
	if presentation == nil {
		return current, malformed("catalog", "fieldPresentation request is required", nil)
	}
	if !b.storeConfigured {
		return current, unavailable("catalog", "AUTHORING_STATE_UNAVAILABLE", "current Explorer drafts are not configured", nil)
	}
	owner, err := b.getOwner(ctx, projectid.Canonical(request.Project), request.ExplorerID)
	if errors.Is(err, explorer.ErrNotFound) {
		return current, notFound("catalog", "EXPLORER_NOT_FOUND", "Explorer not found", err)
	}
	if err != nil {
		return current, persistenceFailure("catalog", "the current Explorer draft could not be loaded", err)
	}
	if owner == nil || owner.ExplorerID != request.ExplorerID || projectid.Canonical(owner.Project) != projectid.Canonical(request.Project) {
		return current, notFound("catalog", "EXPLORER_NOT_FOUND", "Explorer not found", explorer.ErrNotFound)
	}
	if owner.DraftVersion != presentation.ExpectedDraftVersion || owner.DraftDigest != presentation.ExpectedDraftDigest {
		return current, conflict("catalog", "STALE_FIELD_PRESENTATION_CONTEXT", "the Explorer draft changed; reload before requesting field presentation", nil, explorer.ErrDraftConflict)
	}
	if len(owner.DraftConfig) == 0 {
		return current, conflict("catalog", "STALE_FIELD_PRESENTATION_CONTEXT", "field presentation requires the current saved draft", nil, nil)
	}
	workspace, err := authoringv2.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return current, conflict("catalog", "DRAFT_STATE_INVALID", "the saved Explorer draft is invalid", nil, err)
	}
	actualDigest, err := workspace.Digest()
	if err != nil || actualDigest != owner.DraftDigest {
		return current, conflict("catalog", "STALE_FIELD_PRESENTATION_CONTEXT", "the saved workspace does not match its draft digest", nil, err)
	}
	document := findSemanticOutput(workspace, request.OutputID)
	if document == nil || document.RootResourceType != request.RowRoot {
		return current, notFound("catalog", "OUTPUT_NOT_FOUND", "the output was not found for this row root", nil)
	}
	node, nodeFound := snapshot.Node(presentation.SourceNodeID)
	if !nodeFound {
		return current, conflict("catalog", "STALE_FIELD_PRESENTATION_CONTEXT", "the source node is not present in the authorized snapshot", nil, nil)
	}
	occurrencePath, occurrenceFound := routePathToOccurrence(document.Route, presentation.OccurrenceID)
	if !occurrenceFound || len(occurrencePath) == 0 || occurrencePath[len(occurrencePath)-1].ResourceType != node.ResourceType {
		return current, conflict("catalog", "STALE_FIELD_PRESENTATION_CONTEXT", "the selected occurrence is not present in the current output route", nil, nil)
	}
	candidates := make([]capability.Candidate, 0, len(presentation.CandidateIDs))
	for _, candidateID := range presentation.CandidateIDs {
		candidate, found := uniqueCapabilityCandidate(snapshot, candidateID)
		if !found || candidate.NodeID != presentation.SourceNodeID || candidate.ResourceType != node.ResourceType {
			return current, conflict("catalog", "STALE_FIELD_PRESENTATION_CONTEXT", "a requested raw candidate is not part of the selected current source node", nil, nil)
		}
		candidates = append(candidates, candidate)
	}
	route, err := constructionRouteForOccurrence(snapshot, document.RootResourceType, presentation.SourceNodeID, document.Route, presentation.OccurrenceID)
	if err != nil {
		return current, conflict("catalog", "STALE_FIELD_PRESENTATION_CONTEXT", "the exact selected output occurrence no longer resolves to the source node", nil, err)
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
				return current, conflict("catalog", "STALE_FIELD_PRESENTATION_CONTEXT", "the selected construction stage is not part of the current output", nil, nil)
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

type semanticInventoryRouteCandidate struct {
	key       string
	entry     semantic.Entry
	family    *semantic.Family
	candidate capability.Candidate
}

type semanticCandidateRouteProof struct {
	candidate capability.Candidate
	route     []capability.ConstructionRouteStep
	ok        bool
}

func semanticInventoryChoicesForOutput(
	ctx context.Context,
	authorized ports.AuthorizedCapability,
	snapshot capability.Snapshot,
	document authoringv2.Document,
	outputID, contextToken, buildID string,
	entries []semantic.Entry,
	families []semantic.Family,
) (map[string]capability.ConstructionChoice, error) {
	choices := make(map[string]capability.ConstructionChoice)
	byNode := make(map[string][]semanticInventoryRouteCandidate)
	targets := make(map[string]struct{})
	for _, entry := range entries {
		if !authoringv2.ResolveSemanticSelectionPlan(entry.Observation).Readiness.Addable() {
			continue
		}
		candidate, ok := semanticConstructionCandidate(snapshot, entry.Observation)
		if !ok || candidate.NodeID == "" {
			continue
		}
		key := semanticChoiceKey(entry.ConceptID, entry.BindingID)
		byNode[candidate.NodeID] = append(byNode[candidate.NodeID], semanticInventoryRouteCandidate{key: key, entry: entry, candidate: candidate})
		targets[candidate.NodeID] = struct{}{}
	}
	for index := range families {
		family := families[index]
		entry := semanticInventoryEntryForFamily(family)
		if !authoringv2.ResolveSemanticSelectionPlan(entry.Observation).Readiness.Addable() {
			continue
		}
		candidate, ok := semanticConstructionCandidate(snapshot, entry.Observation)
		if !ok || candidate.NodeID == "" {
			continue
		}
		key := semanticChoiceKey(family.FamilyID, family.BindingID)
		familyCopy := family
		byNode[candidate.NodeID] = append(byNode[candidate.NodeID], semanticInventoryRouteCandidate{key: key, entry: entry, family: &familyCopy, candidate: candidate})
		targets[candidate.NodeID] = struct{}{}
	}
	if len(targets) == 0 {
		return choices, nil
	}
	targetNodeIDs := make([]string, 0, len(targets))
	for targetNodeID := range targets {
		targetNodeIDs = append(targetNodeIDs, targetNodeID)
	}
	sort.Strings(targetNodeIDs)
	reachable := semanticInventoryReachableNodes(snapshot, document.RootResourceType)
	reachableTargets := targetNodeIDs[:0]
	for _, targetNodeID := range targetNodeIDs {
		if reachable[targetNodeID] {
			reachableTargets = append(reachableTargets, targetNodeID)
		} else {
			delete(byNode, targetNodeID)
		}
	}
	targetNodeIDs = reachableTargets
	if len(targetNodeIDs) == 0 {
		return choices, nil
	}
	proofCache := make(map[string]semanticCandidateRouteProof)
	for _, targetNodeID := range targetNodeIDs {
		routeCandidates := byNode[targetNodeID]
		keyBytes, err := json.Marshal(struct {
			OutputID string
			Snapshot string
			Target   string
		}{OutputID: outputID, Snapshot: snapshot.Token, Target: targetNodeID})
		if err != nil {
			return nil, internal("catalog", "CONSTRUCTION_ROUTE_SEARCH_FAILED", "the table's semantic route scope could not be encoded", err)
		}
		sourceDigest := sha256.Sum256(keyBytes)
		sourceKey := "semantic-inventory:" + hex.EncodeToString(sourceDigest[:])
		cursor := ""
		for {
			if err := ctx.Err(); err != nil {
				return nil, unavailable("catalog", "CONSTRUCTION_ROUTE_SEARCH_INTERRUPTED", "the table's semantic route search was interrupted", err)
			}
			page, err := capability.PlanConstructionRoutes(capability.ConstructionRouteSearch{
				Snapshot: snapshot, RootResource: document.RootResourceType, TargetNodeID: targetNodeID,
				SourceKey: sourceKey, Cursor: cursor, Limit: capability.ConstructionRouteMaxLimit,
			})
			if err != nil {
				return nil, internal("catalog", "CONSTRUCTION_ROUTE_SEARCH_FAILED", "the table's semantic routes could not be planned", err)
			}
			for _, route := range page.Routes {
				for _, routeCandidate := range routeCandidates {
					if _, resolved := choices[routeCandidate.key]; resolved {
						continue
					}
					resolvedRoute, routeErr := reauthorizeConstructionRoute(snapshot, document.RootResourceType, targetNodeID, route)
					if routeErr != nil {
						continue
					}
					proofKey := semanticCandidateRouteProofCacheKey(routeCandidate.candidate, resolvedRoute)
					proof, cached := proofCache[proofKey]
					if !cached {
						proof = semanticCandidateRouteProof{candidate: routeCandidate.candidate, route: resolvedRoute, ok: true}
						if len(resolvedRoute) > 0 || strings.HasPrefix(routeCandidate.candidate.ID, "schema_field.") {
							proof.candidate, err = proveConstructionCandidate(ctx, authorized, document.RootResourceType, routeCandidate.candidate, resolvedRoute)
							proof.ok = err == nil
							if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil) {
								return nil, unavailable("catalog", "CONSTRUCTION_ROUTE_PROOF_INTERRUPTED", "compiler proof for the table's semantic routes was interrupted", err)
							}
						}
						proofCache[proofKey] = proof
					}
					if !proof.ok {
						continue
					}
					ownerRecordsProved := routeCandidate.family == nil && proveOwnerRecordsForRoute(ctx, authorized, document.RootResourceType, routeCandidate.entry.Observation, proof.route)
					if err := ctx.Err(); err != nil {
						return nil, unavailable("catalog", "CONSTRUCTION_ROUTE_PROOF_INTERRUPTED", "compiler proof for the table's semantic routes was interrupted", err)
					}
					var choice capability.ConstructionChoice
					var choiceErr error
					if routeCandidate.family != nil {
						choice, choiceErr = semanticCategoryConstructionChoiceForRoute(snapshot, contextToken, buildID, proof.route, *routeCandidate.family, proof.candidate)
					} else {
						choice, choiceErr = semanticInventoryConstructionChoiceForRoute(snapshot, contextToken, buildID, proof.route, routeCandidate.entry, proof.candidate, ownerRecordsProved)
					}
					if choiceErr == nil {
						choices[routeCandidate.key] = choice
					}
				}
			}
			allResolved := true
			for _, candidate := range routeCandidates {
				if _, ok := choices[candidate.key]; !ok {
					allResolved = false
					break
				}
			}
			if allResolved || page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
	}
	return choices, nil
}

func semanticInventoryReachableNodes(snapshot capability.Snapshot, rootResourceType string) map[string]bool {
	adjacency := make(map[string][]string)
	for _, edge := range snapshot.Edges {
		if edge.BlockedReason != "" || edge.ID == "" || edge.FromNodeID == "" || edge.ToNodeID == "" || edge.Label == "" {
			continue
		}
		from, fromOK := snapshot.Node(edge.FromNodeID)
		to, toOK := snapshot.Node(edge.ToNodeID)
		if fromOK && toOK && from.ResourceType == edge.SourceResourceType && to.ResourceType == edge.TargetResourceType {
			adjacency[edge.FromNodeID] = append(adjacency[edge.FromNodeID], edge.ToNodeID)
		}
	}
	queue := make([]string, 0)
	reachable := make(map[string]bool)
	for _, node := range snapshot.Nodes {
		if node.RowRootEligible && node.ResourceType == rootResourceType && !reachable[node.ID] {
			reachable[node.ID] = true
			queue = append(queue, node.ID)
		}
	}
	for head := 0; head < len(queue); head++ {
		for _, targetNodeID := range adjacency[queue[head]] {
			if !reachable[targetNodeID] {
				reachable[targetNodeID] = true
				queue = append(queue, targetNodeID)
			}
		}
	}
	return reachable
}

func semanticCandidateRouteProofCacheKey(candidate capability.Candidate, route []capability.ConstructionRouteStep) string {
	identity, _ := json.Marshal(struct {
		Candidate capability.Candidate
		Route     []capability.ConstructionRouteStep
	}{Candidate: candidate, Route: route})
	return string(identity)
}

func semanticInventoryConstructionChoiceForRoute(snapshot capability.Snapshot, contextToken, buildID string, route []capability.ConstructionRouteStep, entry semantic.Entry, candidate capability.Candidate, ownerRecordsProved bool) (capability.ConstructionChoice, error) {
	observation := entry.Observation
	if observation.RuleHint == semantic.RuleHintCodedConceptV1 {

		ownerRecordsProved = false
	}
	logicalType := observation.LogicalType
	if logicalType == "" {
		logicalType = observation.Value.Type
	}
	source := capability.SemanticBindingChoiceSource{
		ConceptID: entry.ConceptID, BindingID: entry.BindingID, ResourceType: observation.Source.Type,
		SourcePath: observation.Source.Path, SourceCanonical: observation.Source.Canonical,
		SourceProfile: observation.Source.Profile, FieldPath: candidate.FieldPath,
		OwningScope: observation.OwningScope, ExtensionURLPath: observation.ExtensionURLPath,
		KeySelector: observation.Key.Selector, System: observation.Key.System, Version: observation.Key.Version,
		Code: observation.Key.Code, ValueSelector: observation.Value.Selector,
		ValueFallbacks:        append([]string(nil), observation.Value.FallbackSelectors...),
		FallbackOnBlankString: observation.Value.FallbackOnBlankString, ChoiceArm: observation.ChoiceArm,
		LogicalType: logicalType, RuleHint: observation.RuleHint, RuleVersion: observation.RuleVersion,
		SchemaVersion: observation.SchemaVersion,
	}
	projectedCandidate, ok := semanticCandidateWithFallback(snapshot, observation, candidate)
	if !ok {
		return capability.ConstructionChoice{}, errors.New("semantic fallback value selectors have no exact compiler-proved candidates")
	}
	return capability.NewSemanticConstructionChoiceForRoute(snapshot.Token, contextToken, buildID, route, source, projectedCandidate, ownerRecordsProved)
}

func semanticInventoryEntryForFamily(family semantic.Family) semantic.Entry {
	return semantic.Entry{
		ConceptID: family.FamilyID, BindingID: family.BindingID,
		Observation: semantic.Observation{
			SchemaVersion: family.SchemaVersion,
			Source:        family.Source,
			Key: semantic.Key{
				Selector: family.KeySelector, System: family.System, Version: family.Version,
			},
			Value:             family.Value,
			OwningScope:       family.OwningScope,
			LogicalType:       family.LogicalType,
			Completeness:      family.Completeness,
			Status:            family.Status,
			Population:        family.Population,
			Examples:          append([]string(nil), family.Examples...),
			ExamplesTruncated: family.ExamplesTruncated,
			RuleHint:          semantic.RuleHintCodedCategoryV1,
			RuleVersion:       family.RuleVersion,
		},
	}
}

func semanticCategoryConstructionChoiceForRoute(
	snapshot capability.Snapshot,
	contextToken, buildID string,
	route []capability.ConstructionRouteStep,
	family semantic.Family,
	candidate capability.Candidate,
) (capability.ConstructionChoice, error) {
	entry := semanticInventoryEntryForFamily(family)
	observation := entry.Observation
	plan := authoringv2.ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil {
		return capability.ConstructionChoice{}, errors.New("coded category has no complete system-version compiler binding")
	}
	projectedCandidate, ok := semanticCandidateWithFallback(snapshot, observation, candidate)
	if !ok {
		return capability.ConstructionChoice{}, errors.New("coded category fallback value selectors have no exact compiler-proved candidates")
	}
	source := capability.SemanticBindingChoiceSource{
		ConceptID: family.FamilyID, BindingID: family.BindingID, SourceKind: family.SourceKind,
		ResourceType: family.Source.Type, SourcePath: family.Source.Path,
		SourceCanonical: family.Source.Canonical, SourceProfile: family.Source.Profile,
		FieldPath: candidate.FieldPath, OwningScope: family.OwningScope,
		KeySelector: family.KeySelector, System: family.System, Version: family.Version,
		ValueSelector:         family.Value.Selector,
		ValueFallbacks:        append([]string(nil), family.Value.FallbackSelectors...),
		FallbackOnBlankString: family.Value.FallbackOnBlankString,
		LogicalType:           firstNonEmpty(family.LogicalType, family.Value.Type),
		RuleHint:              semantic.RuleHintCodedCategoryV1, RuleVersion: family.RuleVersion,
		SchemaVersion: family.SchemaVersion,
	}
	return capability.NewSemanticConstructionChoiceForRoute(snapshot.Token, contextToken, buildID, route, source, projectedCandidate)
}

func semanticCategoryLabel(family semantic.Family, candidate capability.Candidate) string {
	if label := semanticSystemTailLabel(family.System); label != "" {
		return label
	}
	path := strings.TrimSpace(family.Source.Path)
	resourcePrefix := strings.TrimSpace(family.Source.Type) + "."
	path = strings.TrimPrefix(path, resourcePrefix)
	path = strings.TrimSuffix(path, ".coding[]")
	path = strings.TrimSuffix(path, "coding[]")
	path = strings.Trim(path, ".[]")
	if path == "" {
		path = strings.TrimSpace(candidate.Label)
	}
	if path == "" {
		return family.Source.Type
	}
	parts := strings.Split(path, ".")
	field := semanticHumanizeLabel(parts[len(parts)-1])
	if field == "" {
		field = semanticHumanizeLabel(path)
	}
	if field == "" {
		return family.Source.Type
	}
	if field != "" {
		field = strings.ToLower(field[:1]) + field[1:]
	}
	return strings.TrimSpace(family.Source.Type + " " + field)
}

func semanticIdentifierLabel(system string) string {
	if strings.TrimSpace(system) == "" {
		return "Identifier"
	}
	return "Identifier · " + strings.TrimSpace(system)
}

func semanticSystemTailLabel(system string) string {
	path := strings.TrimRight(strings.TrimSpace(system), "/")
	if index := strings.LastIndex(path, "/"); index >= 0 {
		path = path[index+1:]
	}
	if !strings.ContainsAny(path, "_-") {
		return ""
	}
	return semanticHumanizeLabel(path)
}

func semanticHumanizeLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	words := make([]string, 0)
	for _, raw := range strings.FieldsFunc(value, func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	}) {
		if raw == "" {
			continue
		}
		if raw == strings.ToUpper(raw) {
			words = append(words, strings.ToLower(raw))
			continue
		}
		runes := []rune(raw)
		start := 0
		for index := 1; index < len(runes); index++ {
			previous, current := runes[index-1], runes[index]
			if !unicode.IsUpper(current) {
				continue
			}
			nextLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
			if unicode.IsLower(previous) || (unicode.IsUpper(previous) && nextLower) {
				words = append(words, strings.ToLower(string(runes[start:index])))
				start = index
			}
		}
		words = append(words, strings.ToLower(string(runes[start:])))
	}
	if len(words) == 0 {
		return ""
	}
	words[0] = strings.ToUpper(words[0][:1]) + words[0][1:]
	return strings.Join(words, " ")
}

func semanticEntryMatchesFrame(entry semantic.Entry, frame authoringv2.FrameDefinition) bool {
	_, _, ok := semanticEntryForFrameFamily(entry, frame.Source)
	return ok
}

func semanticEntryMatchesFrameFamily(entry semantic.Entry, family capability.SemanticFrameFamily) bool {
	_, _, ok := semanticEntryForFrameFamily(entry, family)
	return ok
}

func semanticFrameFamilyMatchesPinnedPrior(current, pinned capability.SemanticFrameFamily, observation semantic.Observation) bool {
	return current.SchemaVersion == semantic.ObservationSchemaVersion &&
		current.RuleVersion == strconv.Itoa(semantic.SemanticObservationRuleVersion) &&
		pinned.SchemaVersion == semantic.SemanticObservationLegacyV1SchemaVersion &&
		pinned.RuleVersion == strconv.Itoa(semantic.SemanticObservationLegacyV1RuleVersion) &&
		len(pinned.ValueFallbacks) == 0 && !pinned.FallbackOnBlankString &&
		pinned.BindingID == semantic.LegacySemanticBindingIDV1(observation) &&
		semanticEntrySourceMatchesFrameFamily(observation, pinned) &&
		current.ResourceType == pinned.ResourceType &&
		current.SourcePath == pinned.SourcePath && current.SourceCanonical == pinned.SourceCanonical &&
		current.SourceProfile == pinned.SourceProfile && current.OwningScope == pinned.OwningScope &&
		current.KeyPath == pinned.KeyPath && current.ValuePath == pinned.ValuePath &&
		current.LogicalType == pinned.LogicalType && reflect.DeepEqual(current.ChoiceArms, pinned.ChoiceArms)
}

func semanticEntryForFrameFamily(
	entry semantic.Entry,
	family capability.SemanticFrameFamily,
) (semantic.Entry, authoringv2.SemanticSelectionPlan, bool) {
	observation := entry.Observation
	if observation.RuleHint != semantic.RuleHintCodedValueV1 || !semanticEntrySourceMatchesFrameFamily(observation, family) || observation.Key.Version != "" {
		return semantic.Entry{}, authoringv2.SemanticSelectionPlan{}, false
	}

	currentRuleVersion := strconv.Itoa(semantic.SemanticObservationRuleVersion)
	if observation.SchemaVersion != semantic.ObservationSchemaVersion || observation.RuleVersion != currentRuleVersion {
		return semantic.Entry{}, authoringv2.SemanticSelectionPlan{}, false
	}
	currentFamily := family.SchemaVersion == observation.SchemaVersion && family.RuleVersion == observation.RuleVersion && family.BindingID == entry.BindingID
	priorFamily := isPriorPrimaryOnlySemanticFrameFamily(family) &&
		family.BindingID == semantic.LegacySemanticBindingIDV1(observation)
	if !currentFamily && !priorFamily {
		return semantic.Entry{}, authoringv2.SemanticSelectionPlan{}, false
	}
	if priorFamily {
		observation.Value.FallbackSelectors = nil
		observation.Value.FallbackOnBlankString = false
		entry.Observation = observation
	}

	plan := authoringv2.ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Kind != authoringv2.SourceCodedValue ||
		plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil {
		return semantic.Entry{}, authoringv2.SemanticSelectionPlan{}, false
	}
	binding := plan.Source.Lookup.Binding
	if binding.OwnerPath != family.OwningScope || binding.KeyPath != family.KeyPath || binding.ValuePath != family.ValuePath ||
		!slices.Equal(binding.ValueFallback, family.ValueFallbacks) || binding.FallbackOnBlankString != family.FallbackOnBlankString ||
		binding.LogicalType != family.LogicalType || !reflect.DeepEqual(binding.ChoiceArms, family.ChoiceArms) {
		return semantic.Entry{}, authoringv2.SemanticSelectionPlan{}, false
	}
	return entry, plan, true
}

func isPriorPrimaryOnlySemanticFrameFamily(family capability.SemanticFrameFamily) bool {
	return family.SchemaVersion == semantic.SemanticObservationLegacyV1SchemaVersion &&
		family.RuleVersion == strconv.Itoa(semantic.SemanticObservationLegacyV1RuleVersion) &&
		len(family.ValueFallbacks) == 0 && !family.FallbackOnBlankString
}

func semanticEntrySourceMatchesFrameFamily(observation semantic.Observation, family capability.SemanticFrameFamily) bool {
	return observation.Source.Type == family.ResourceType &&
		observation.Source.Path == family.SourcePath && observation.Source.Canonical == family.SourceCanonical &&
		observation.Source.Profile == family.SourceProfile
}

func proveOwnerRecordsForRoute(ctx context.Context, authorized ports.AuthorizedCapability, rootResourceType string, observation semantic.Observation, route []capability.ConstructionRouteStep) bool {
	plan := authoringv2.ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Kind != authoringv2.SourceCodedValue || plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil {
		return false
	}
	compilerRoute := make([]facade.Traversal, 0, len(route))
	for _, step := range route {
		matchMode := spec.TraversalMatchOptional
		if step.MatchMode == string(spec.TraversalMatchRequired) {
			matchMode = spec.TraversalMatchRequired
		}
		compilerRoute = append(compilerRoute, facade.Traversal{FromResourceType: step.FromResourceType, EdgeLabel: step.Relationship, ToResourceType: step.ToResourceType, MatchMode: matchMode})
	}
	_, err := facade.ProbeOwnerRecords(ctx, facade.OwnerRecordsRequest{
		Scope: facade.Scope{
			Project: projectid.Legacy(authorized.Snapshot.Identity.Project), DatasetGeneration: authorized.Snapshot.Identity.Generation,
			AuthResourcePaths: append([]string(nil), authorized.Scope.AuthResourcePaths...), AuthScopeMode: authorized.Scope.Mode,
		},
		RootResourceType: rootResourceType,
		ResourceType:     observation.Source.Type,
		Route:            compilerRoute,
		Binding:          *plan.Source.Lookup.Binding,
		Key:              *plan.Source.Lookup.Key,
	})
	return err == nil
}

func semanticConstructionCandidate(snapshot capability.Snapshot, observation semantic.Observation) (capability.Candidate, bool) {
	resourceType := strings.TrimSpace(observation.Source.Type)
	valuePath := semanticObservationValuePath(observation)
	if resourceType == "" || valuePath == "" {
		return capability.Candidate{}, false
	}
	var match capability.Candidate
	found := false
	for _, candidate := range snapshot.Candidates {
		if candidate.ResourceType == resourceType && canonicalInventoryPath(candidate.FieldPath) == valuePath {
			if found {
				return capability.Candidate{}, false
			}
			match, found = candidate, true
		}
	}
	return match, found
}

func semanticCandidateWithFallback(snapshot capability.Snapshot, observation semantic.Observation, primary capability.Candidate) (capability.Candidate, bool) {
	if len(observation.Value.FallbackSelectors) == 0 {
		return primary, !observation.Value.FallbackOnBlankString
	}
	if primary.LogicalType != observation.Value.Type {
		return capability.Candidate{}, false
	}
	combined := primary
	unknownCardinality := strings.EqualFold(strings.TrimSpace(primary.Cardinality), "unknown_observed_many")
	repeatedCardinality := capability.IsRepeatedCardinality(primary.Cardinality)
	projectionModes := make(map[capability.ProjectionMode]struct{}, len(primary.ProjectionModes))
	for _, mode := range primary.ProjectionModes {
		projectionModes[mode] = struct{}{}
	}
	boundaries := make(map[string]capability.RepeatedBoundary, len(primary.RepeatedBoundaries))
	for _, boundary := range primary.RepeatedBoundaries {
		boundaries[boundary.Path] = boundary
	}
	for _, selector := range observation.Value.FallbackSelectors {
		path := canonicalInventoryPath(selector)
		ownerPath := canonicalInventoryPath(observation.OwningScope)
		if ownerPath != "" && path != ownerPath && !strings.HasPrefix(path, ownerPath+".") {
			path = ownerPath + "." + path
		}
		var fallback capability.Candidate
		found := false
		for _, candidate := range snapshot.Candidates {
			if candidate.ResourceType != observation.Source.Type || canonicalInventoryPath(candidate.FieldPath) != path {
				continue
			}
			if found || candidate.NodeID != primary.NodeID || candidate.LogicalType != primary.LogicalType {
				return capability.Candidate{}, false
			}
			fallback, found = candidate, true
		}
		if !found || len(fallback.ProjectionModes) == 0 {
			return capability.Candidate{}, false
		}
		unknownCardinality = unknownCardinality || strings.EqualFold(strings.TrimSpace(fallback.Cardinality), "unknown_observed_many")
		repeatedCardinality = repeatedCardinality || capability.IsRepeatedCardinality(fallback.Cardinality)
		for _, boundary := range fallback.RepeatedBoundaries {
			if existing, ok := boundaries[boundary.Path]; !ok || boundary.MaxItems > existing.MaxItems {
				boundaries[boundary.Path] = boundary
			}
		}
		for _, mode := range fallback.ProjectionModes {
			projectionModes[mode] = struct{}{}
		}
	}
	if unknownCardinality {
		combined.Cardinality = "unknown_observed_many"
	} else if repeatedCardinality {
		combined.Cardinality = "many"
	}
	combined.RepeatedBoundaries = make([]capability.RepeatedBoundary, 0, len(boundaries))
	for _, boundary := range boundaries {
		combined.RepeatedBoundaries = append(combined.RepeatedBoundaries, boundary)
	}
	sort.Slice(combined.RepeatedBoundaries, func(i, j int) bool { return combined.RepeatedBoundaries[i].Path < combined.RepeatedBoundaries[j].Path })
	combined.ProjectionModes = make([]capability.ProjectionMode, 0, len(projectionModes))
	for _, mode := range []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionFirst, capability.ProjectionArray, capability.ProjectionDistinctArray} {
		if _, ok := projectionModes[mode]; ok {
			combined.ProjectionModes = append(combined.ProjectionModes, mode)
		}
	}
	return combined, true
}

func semanticObservationValuePath(observation semantic.Observation) string {
	valuePath := canonicalInventoryPath(observation.Value.Selector)
	ownerPath := canonicalInventoryPath(observation.OwningScope)
	if valuePath == "" || ownerPath == "" || valuePath == ownerPath || strings.HasPrefix(valuePath, ownerPath+".") {
		return valuePath
	}
	return ownerPath + "." + valuePath
}

func canonicalInventoryPath(raw string) string {
	path := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "root."))
	if path == "" {
		return ""
	}
	segments := strings.Split(path, ".")
	for index := range segments {
		segments[index] = strings.TrimSpace(segments[index])
		if segments[index] == "" {
			return ""
		}
	}
	return strings.Join(segments, ".")
}

func compilerProofUnavailable() authoringv2.SemanticSelectionReadiness {
	return authoringv2.SemanticSelectionReadiness{
		Status: authoringv2.SemanticReadinessUnsupported, Code: "COMPILER_PROOF_UNAVAILABLE",
		Message: "The exact semantic value path has no matching compiler-proved capability field.",
	}
}

func semanticInventoryContextToken(snapshot capability.Snapshot, explorerID, rowRoot, buildID string) (string, error) {
	identity, err := json.Marshal([]string{"semantic-browse/v1", snapshot.Identity.Project, explorerID, snapshot.Token, snapshot.Identity.AuthorizationScopeDigest, rowRoot, "all-authorized", buildID})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(identity)
	return hex.EncodeToString(digest[:]), nil
}
