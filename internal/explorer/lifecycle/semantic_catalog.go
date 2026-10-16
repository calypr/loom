package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	compilerprobe "github.com/calypr/loom/internal/dataframe/compiler/capability"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

type BrowseSemanticInventoryRequest struct {
	Project           string
	ExplorerID        string
	SnapshotToken     string
	RowRoot           string
	SelectionMode     string
	FrameID           string
	SourceChoiceID    string
	OutputID          string
	ResourceType      string
	Query             string
	Cursor            string
	Limit             int
	FieldPresentation *FieldPresentationRequest
}

const (
	semanticFieldPresentationRevision = "semantic-field-presentation/v1"
	fieldPresentationCandidateLimit   = 50
	fieldPresentationReplacementLimit = 100
)

// FieldPresentationRequest scopes one bounded batch of raw candidates to an
// exact current output, draft, source node, and saved route occurrence.
type FieldPresentationRequest struct {
	ExpectedDraftVersion int64
	ExpectedDraftDigest  string
	SourceNodeID         string
	OccurrenceID         string
	StageID              *string
	CandidateIDs         []string
}

func (request FieldPresentationRequest) validate() error {
	if request.ExpectedDraftVersion < 1 || strings.TrimSpace(request.ExpectedDraftDigest) == "" || request.ExpectedDraftDigest != strings.TrimSpace(request.ExpectedDraftDigest) {
		return errors.New("fieldPresentation requires a positive expectedDraftVersion and exact expectedDraftDigest")
	}
	if strings.TrimSpace(request.SourceNodeID) == "" || request.SourceNodeID != strings.TrimSpace(request.SourceNodeID) {
		return errors.New("fieldPresentation requires an exact sourceNodeId")
	}
	if strings.TrimSpace(request.OccurrenceID) == "" || request.OccurrenceID != strings.TrimSpace(request.OccurrenceID) {
		return errors.New("fieldPresentation requires an exact occurrenceId")
	}
	if request.StageID != nil && (strings.TrimSpace(*request.StageID) == "" || *request.StageID != strings.TrimSpace(*request.StageID)) {
		return errors.New("fieldPresentation stageId must be a non-empty exact identifier when present")
	}
	if len(request.CandidateIDs) < 1 || len(request.CandidateIDs) > fieldPresentationCandidateLimit {
		return errors.New("fieldPresentation candidateIds must contain between 1 and 50 opaque candidate IDs")
	}
	seen := make(map[string]struct{}, len(request.CandidateIDs))
	for _, candidateID := range request.CandidateIDs {
		if strings.TrimSpace(candidateID) == "" || candidateID != strings.TrimSpace(candidateID) {
			return errors.New("fieldPresentation candidateIds must be exact non-empty opaque IDs")
		}
		if _, exists := seen[candidateID]; exists {
			return errors.New("fieldPresentation candidateIds must be unique")
		}
		seen[candidateID] = struct{}{}
	}
	return nil
}

type FieldPresentationReason string

const (
	FieldPresentationMissingSystem              FieldPresentationReason = "MISSING_SYSTEM"
	FieldPresentationMissingCode                FieldPresentationReason = "MISSING_CODE"
	FieldPresentationTextOnly                   FieldPresentationReason = "TEXT_ONLY"
	FieldPresentationAmbiguousAnchor            FieldPresentationReason = "AMBIGUOUS_ANCHOR"
	FieldPresentationMixedChoice                FieldPresentationReason = "MIXED_CHOICE"
	FieldPresentationUnsupportedValueProjection FieldPresentationReason = "UNSUPPORTED_VALUE_PROJECTION"
	FieldPresentationUnsupportedVersion         FieldPresentationReason = "UNSUPPORTED_VERSION"
	FieldPresentationUnknownSchemaMember        FieldPresentationReason = "UNKNOWN_SCHEMA_MEMBER"
	FieldPresentationUnknownCodeClassification  FieldPresentationReason = "UNKNOWN_CODE_CLASSIFICATION"
	FieldPresentationUnsupportedSelector        FieldPresentationReason = "UNSUPPORTED_SELECTOR"
	FieldPresentationIncompleteSource           FieldPresentationReason = "INCOMPLETE_SOURCE"
	FieldPresentationWalkFailed                 FieldPresentationReason = "WALK_FAILED"
	FieldPresentationTruncatedEvidence          FieldPresentationReason = "TRUNCATED_EVIDENCE"
	FieldPresentationUnrepresentedMetadata      FieldPresentationReason = "UNREPRESENTED_METADATA"
	FieldPresentationCoverageUnavailable        FieldPresentationReason = "COVERAGE_UNAVAILABLE"
	FieldPresentationIncompleteCoverage         FieldPresentationReason = "INCOMPLETE_COVERAGE"
	FieldPresentationNoExactRoute               FieldPresentationReason = "NO_EXACT_ROUTE"
	FieldPresentationUnsupportedCurrentStage    FieldPresentationReason = "UNSUPPORTED_CURRENT_STAGE"
	FieldPresentationStaleContext               FieldPresentationReason = "STALE_CONTEXT"
	FieldPresentationReplacementUnavailable     FieldPresentationReason = "REPLACEMENT_UNAVAILABLE"
	FieldPresentationSourceFieldsInaccessible   FieldPresentationReason = "SOURCE_FIELDS_INACCESSIBLE"
	FieldPresentationCandidateNotPopulated      FieldPresentationReason = "CANDIDATE_NOT_POPULATED"
	FieldPresentationCandidateNotAddable        FieldPresentationReason = "CANDIDATE_NOT_ADDABLE"
)

type fieldPresentationResult interface {
	fieldPresentationResult()
}

type FieldPresentationUnavailable struct {
	Kind   string                  `json:"kind"`
	Reason FieldPresentationReason `json:"reason"`
}

func (FieldPresentationUnavailable) fieldPresentationResult() {}

type FieldPresentationReady struct {
	Kind         string                      `json:"kind"`
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
	Kind        string                  `json:"kind"`
	CandidateID string                  `json:"candidateId"`
	Reason      FieldPresentationReason `json:"reason"`
}

func (FieldPresentationKeepRaw) fieldPresentationDecision() {}

type semanticInventoryItemIdentity interface {
	semanticInventoryItemIdentity()
}

type ExactSemanticInventoryItemIdentity struct {
	Kind      string `json:"kind"`
	ConceptID string `json:"conceptId"`
	BindingID string `json:"bindingId"`
}

func (ExactSemanticInventoryItemIdentity) semanticInventoryItemIdentity() {}

type FamilySemanticInventoryItemIdentity struct {
	Kind      string `json:"kind"`
	FamilyID  string `json:"familyId"`
	BindingID string `json:"bindingId"`
}

func (FamilySemanticInventoryItemIdentity) semanticInventoryItemIdentity() {}

type FieldPresentationGroupUnderSemantic struct {
	Kind           string                          `json:"kind"`
	CandidateID    string                          `json:"candidateId"`
	CoverageID     string                          `json:"coverageId"`
	ReplacementIDs []semanticInventoryItemIdentity `json:"replacementIds"`
	Access         string                          `json:"access"`
}

func (FieldPresentationGroupUnderSemantic) fieldPresentationDecision() {}

type SemanticInventoryItem struct {
	ConceptID              string                                  `json:"conceptId"`
	BindingID              string                                  `json:"bindingId"`
	SelectionMode          string                                  `json:"selectionMode,omitempty"`
	FamilyID               string                                  `json:"familyId,omitempty"`
	ResourceType           string                                  `json:"resourceType"`
	SourcePath             string                                  `json:"sourcePath"`
	System                 string                                  `json:"system"`
	Code                   string                                  `json:"code"`
	CodingVersion          string                                  `json:"codingVersion"`
	Display                string                                  `json:"display"`
	ValueSelector          string                                  `json:"valueSelector"`
	ValueType              string                                  `json:"valueType"`
	OwningScope            string                                  `json:"owningScope"`
	Occurrences            int64                                   `json:"occurrences"`
	Examples               []string                                `json:"examples,omitempty"`
	ExamplesTruncated      bool                                    `json:"examplesTruncated"`
	ObservedUnits          []string                                `json:"observedUnits,omitempty"`
	ObservedUnitsTruncated bool                                    `json:"observedUnitsTruncated"`
	Completeness           catalog.SemanticObservationCompleteness `json:"completeness,omitempty"`
	Readiness              authoringv2.SemanticSelectionReadiness  `json:"readiness"`
	ConstructionChoice     *capability.ConstructionChoice          `json:"constructionChoice,omitempty"`
}

type BrowseSemanticInventoryResponse struct {
	FrameID            string                         `json:"frameId,omitempty"`
	FrameSource        *authoringv2.FrameDefinition   `json:"frameSource,omitempty"`
	ContextToken       string                         `json:"contextToken"`
	BuildID            string                         `json:"buildId"`
	State              catalog.SemanticInventoryState `json:"state"`
	SourceAvailability string                         `json:"sourceAvailability"`
	Entries            []SemanticInventoryItem        `json:"entries"`
	NextCursor         string                         `json:"nextCursor,omitempty"`
	FieldPresentation  fieldPresentationResult        `json:"fieldPresentation,omitempty"`
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
	var result BrowseSemanticInventoryResponse
	if req.FieldPresentation != nil {
		if err := req.FieldPresentation.validate(); err != nil {
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
		req.SelectionMode = string(catalog.SemanticInventoryExactConcept)
	}
	if req.SelectionMode != string(catalog.SemanticInventoryExactConcept) && req.SelectionMode != string(catalog.SemanticInventorySystemVersionCategory) {
		return result, malformed("catalog", "selectionMode must be EXACT_CONCEPT or SYSTEM_VERSION_CATEGORY", nil)
	}
	if strings.TrimSpace(req.Project) == "" || strings.TrimSpace(req.ExplorerID) == "" || req.SnapshotToken == "" || req.RowRoot == "" || req.Limit < 0 || req.Limit > catalog.SemanticInventoryPageLimit || len(req.Query) > 256 || len(req.Cursor) > 4096 {
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
		return result, err
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
	if req.SelectionMode == string(catalog.SemanticInventorySystemVersionCategory) && (req.FrameID != "" || req.SourceChoiceID != "") {
		return result, malformed("catalog", "system-version category browsing is available only outside a saved frame or coded source selection", nil)
	}
	var frame *authoringv2.FrameDefinition
	var codedFamily *frameSourceFamilyCandidate
	var codedRoute []capability.ConstructionRouteStep
	var outputDocument *authoringv2.Document
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
				copy.Route = cloneConstructionRoute(candidate.Route)
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
			// The current inventory uses semantic-binding/v2 IDs. Query the
			// authorized resource page without the obsolete v1 filter, then require
			// an exact reconstructed v1 identity in semanticEntryForFrameFamily.
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
	var cursor semanticBrowseCursor
	if req.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(req.Cursor)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Context == "" || cursor.Page == "" || semanticBrowseCursorSelectionMode(cursor.SelectionMode) != req.SelectionMode || cursor.FrameID != req.FrameID || cursor.SourceChoiceID != req.SourceChoiceID || cursor.OutputID != req.OutputID {
			return result, conflict("catalog", "STALE_CATALOG_CURSOR", "restart catalog search with an empty cursor", nil, nil)
		}
	}
	unrestricted := authorized.Scope.Mode == authscope.ReadScopeUnrestricted
	page, err := s.config.SemanticInventory(ctx, catalog.SemanticInventoryPageOptions{
		Project: projectid.Legacy(snapshot.Identity.Project), DatasetGeneration: snapshot.Identity.Generation,
		AuthResourcePathsUnrestricted: &unrestricted, AuthResourcePaths: authorized.Scope.AuthResourcePaths,
		ResourceType: req.ResourceType, BindingID: bindingFilter, Query: req.Query, Cursor: cursor.Page, Limit: req.Limit,
		SelectionMode: catalog.SemanticInventorySelectionMode(req.SelectionMode),
	})
	if errors.Is(err, catalog.ErrSemanticInventoryCursorMismatch) {
		return result, conflict("catalog", "STALE_CATALOG_CURSOR", "restart catalog search with an empty cursor", nil, err)
	}
	if err != nil {
		return result, err
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
		result.SourceAvailability = catalog.SemanticInventorySourceAvailabilityUnknown
	}
	categoryEntries := make([]catalog.SemanticInventoryEntry, 0, len(page.Families))
	familyByChoice := make(map[string]catalog.SemanticInventoryFamily, len(page.Families))
	for _, family := range page.Families {
		entry := semanticInventoryEntryForFamily(family)
		key := semanticChoiceKey(entry.ConceptID, entry.BindingID)
		categoryEntries = append(categoryEntries, entry)
		familyByChoice[key] = family
	}
	entries := make([]catalog.SemanticInventoryEntry, 0, len(page.Entries)+len(categoryEntries))
	entries = append(entries, page.Entries...)
	entries = append(entries, categoryEntries...)
	var outputChoices map[string]capability.ConstructionChoice
	if outputDocument != nil {
		outputChoices, err = semanticInventoryChoicesForOutput(ctx, authorized, snapshot, *outputDocument, req.OutputID, result.ContextToken, result.BuildID, page.Entries, page.Families)
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
			item.SelectionMode = string(catalog.SemanticInventorySystemVersionCategory)
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
		return string(catalog.SemanticInventoryExactConcept)
	}
	return mode
}

type semanticInventoryRouteCandidate struct {
	key       string
	entry     catalog.SemanticInventoryEntry
	family    *catalog.SemanticInventoryFamily
	candidate capability.Candidate
}

type semanticCandidateRouteProof struct {
	candidate capability.Candidate
	route     []capability.ConstructionRouteStep
	ok        bool
}

// semanticInventoryChoicesForOutput keeps table applicability separate from
// global semantic inventory. Routes are planned once per distinct candidate
// node; repeated entries reuse the exact candidate+route compiler proof, while
// each semantic binding still gets its own choice ID.
func semanticInventoryChoicesForOutput(
	ctx context.Context,
	authorized AuthorizedCapability,
	snapshot capability.Snapshot,
	document authoringv2.Document,
	outputID, contextToken, buildID string,
	entries []catalog.SemanticInventoryEntry,
	families []catalog.SemanticInventoryFamily,
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

// semanticInventoryReachableNodes uses the authorized snapshot graph only as
// a negative prefilter. Every retained route is still reauthorized and
// compiler-probed below; disconnected resource nodes never trigger a full
// alternate-route search.
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

func semanticInventoryConstructionChoice(snapshot capability.Snapshot, contextToken, buildID string, entry catalog.SemanticInventoryEntry, candidate capability.Candidate, ownerRecordsProved bool) (capability.ConstructionChoice, error) {
	return semanticInventoryConstructionChoiceForRoute(snapshot, contextToken, buildID, nil, entry, candidate, ownerRecordsProved)
}

func semanticInventoryConstructionChoiceForRoute(snapshot capability.Snapshot, contextToken, buildID string, route []capability.ConstructionRouteStep, entry catalog.SemanticInventoryEntry, candidate capability.Candidate, ownerRecordsProved bool) (capability.ConstructionChoice, error) {
	observation := entry.Observation
	if observation.RuleHint == catalog.SemanticRuleHintCodedConceptV1 {
		// Owner-record output is defined over the enclosing semantic owner. A
		// standalone Coding choice must stay attached to the exact matched
		// Coding, so expose only the compiler-proved scalar/list choices.
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

func semanticInventoryEntryForFamily(family catalog.SemanticInventoryFamily) catalog.SemanticInventoryEntry {
	return catalog.SemanticInventoryEntry{
		ConceptID: family.FamilyID, BindingID: family.BindingID,
		Observation: catalog.SemanticObservation{
			SchemaVersion: family.SchemaVersion,
			Source:        family.Source,
			Key: catalog.SemanticObservationKey{
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
			RuleHint:          catalog.SemanticRuleHintCodedCategoryV1,
			RuleVersion:       family.RuleVersion,
		},
	}
}

func semanticCategoryConstructionChoiceForRoute(
	snapshot capability.Snapshot,
	contextToken, buildID string,
	route []capability.ConstructionRouteStep,
	family catalog.SemanticInventoryFamily,
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
		RuleHint:              catalog.SemanticRuleHintCodedCategoryV1, RuleVersion: family.RuleVersion,
		SchemaVersion: family.SchemaVersion,
	}
	return capability.NewSemanticConstructionChoiceForRoute(snapshot.Token, contextToken, buildID, route, source, projectedCandidate)
}

func semanticCategoryLabel(family catalog.SemanticInventoryFamily, candidate capability.Candidate) string {
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
	var words []string
	var current strings.Builder
	for index, r := range value {
		if r == '_' || r == '-' || r == '.' {
			if current.Len() > 0 {
				words = append(words, current.String())
				current.Reset()
			}
			continue
		}
		if index > 0 && r >= 'A' && r <= 'Z' && current.Len() > 0 {
			words = append(words, current.String())
			current.Reset()
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		words = append(words, current.String())
	}
	for index, word := range words {
		word = strings.ToLower(word)
		if word != "" {
			if index == 0 {
				words[index] = strings.ToUpper(word[:1]) + word[1:]
			} else {
				words[index] = word
			}
		}
	}
	return strings.Join(words, " ")
}

func semanticEntryMatchesFrame(entry catalog.SemanticInventoryEntry, frame authoringv2.FrameDefinition) bool {
	_, _, ok := semanticEntryForFrameFamily(entry, frame.Source)
	return ok
}

func semanticEntryMatchesFrameFamily(entry catalog.SemanticInventoryEntry, family capability.SemanticFrameFamily) bool {
	_, _, ok := semanticEntryForFrameFamily(entry, family)
	return ok
}

func semanticFrameFamilyMatchesPinnedPrior(current, pinned capability.SemanticFrameFamily, observation catalog.SemanticObservation) bool {
	return current.SchemaVersion == catalog.SemanticObservationSchemaVersion &&
		current.RuleVersion == strconv.Itoa(catalog.SemanticObservationRuleVersion) &&
		pinned.SchemaVersion == catalog.SemanticObservationLegacyV1SchemaVersion &&
		pinned.RuleVersion == strconv.Itoa(catalog.SemanticObservationLegacyV1RuleVersion) &&
		len(pinned.ValueFallbacks) == 0 && !pinned.FallbackOnBlankString &&
		pinned.BindingID == catalog.LegacySemanticBindingIDV1(observation) &&
		semanticEntrySourceMatchesFrameFamily(observation, pinned) &&
		current.ResourceType == pinned.ResourceType &&
		current.SourcePath == pinned.SourcePath && current.SourceCanonical == pinned.SourceCanonical &&
		current.SourceProfile == pinned.SourceProfile && current.OwningScope == pinned.OwningScope &&
		current.KeyPath == pinned.KeyPath && current.ValuePath == pinned.ValuePath &&
		current.LogicalType == pinned.LogicalType && reflect.DeepEqual(current.ChoiceArms, pinned.ChoiceArms)
}

// semanticEntryForFrameFamily projects a current inventory observation back
// to the exact value binding pinned by a saved frame. The only supported
// transition is the immediately preceding semantic build, whose new behavior
// adds fallback selectors. A legacy frame without fallbacks keeps its primary
// selector and cannot silently acquire the new fallback behavior.
func semanticEntryForFrameFamily(
	entry catalog.SemanticInventoryEntry,
	family capability.SemanticFrameFamily,
) (catalog.SemanticInventoryEntry, authoringv2.SemanticSelectionPlan, bool) {
	observation := entry.Observation
	if observation.RuleHint != catalog.SemanticRuleHintCodedValueV1 || !semanticEntrySourceMatchesFrameFamily(observation, family) || observation.Key.Version != "" {
		return catalog.SemanticInventoryEntry{}, authoringv2.SemanticSelectionPlan{}, false
	}

	currentRuleVersion := strconv.Itoa(catalog.SemanticObservationRuleVersion)
	if observation.SchemaVersion != catalog.SemanticObservationSchemaVersion || observation.RuleVersion != currentRuleVersion {
		return catalog.SemanticInventoryEntry{}, authoringv2.SemanticSelectionPlan{}, false
	}
	currentFamily := family.SchemaVersion == observation.SchemaVersion && family.RuleVersion == observation.RuleVersion && family.BindingID == entry.BindingID
	priorFamily := isPriorPrimaryOnlySemanticFrameFamily(family) &&
		family.BindingID == catalog.LegacySemanticBindingIDV1(observation)
	if !currentFamily && !priorFamily {
		return catalog.SemanticInventoryEntry{}, authoringv2.SemanticSelectionPlan{}, false
	}
	if priorFamily {
		observation.Value.FallbackSelectors = nil
		observation.Value.FallbackOnBlankString = false
		entry.Observation = observation
	}

	plan := authoringv2.ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Kind != authoringv2.SourceCodedValue ||
		plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil {
		return catalog.SemanticInventoryEntry{}, authoringv2.SemanticSelectionPlan{}, false
	}
	binding := plan.Source.Lookup.Binding
	if binding.OwnerPath != family.OwningScope || binding.KeyPath != family.KeyPath || binding.ValuePath != family.ValuePath ||
		!slices.Equal(binding.ValueFallback, family.ValueFallbacks) || binding.FallbackOnBlankString != family.FallbackOnBlankString ||
		binding.LogicalType != family.LogicalType || !reflect.DeepEqual(binding.ChoiceArms, family.ChoiceArms) {
		return catalog.SemanticInventoryEntry{}, authoringv2.SemanticSelectionPlan{}, false
	}
	return entry, plan, true
}

func isPriorPrimaryOnlySemanticFrameFamily(family capability.SemanticFrameFamily) bool {
	return family.SchemaVersion == catalog.SemanticObservationLegacyV1SchemaVersion &&
		family.RuleVersion == strconv.Itoa(catalog.SemanticObservationLegacyV1RuleVersion) &&
		len(family.ValueFallbacks) == 0 && !family.FallbackOnBlankString
}

func semanticEntrySourceMatchesFrameFamily(observation catalog.SemanticObservation, family capability.SemanticFrameFamily) bool {
	return observation.Source.Type == family.ResourceType &&
		observation.Source.Path == family.SourcePath && observation.Source.Canonical == family.SourceCanonical &&
		observation.Source.Profile == family.SourceProfile
}

func proveOwnerRecords(ctx context.Context, authorized AuthorizedCapability, rootResourceType string, observation catalog.SemanticObservation) bool {
	return proveOwnerRecordsForRoute(ctx, authorized, rootResourceType, observation, nil)
}

func proveOwnerRecordsForRoute(ctx context.Context, authorized AuthorizedCapability, rootResourceType string, observation catalog.SemanticObservation, route []capability.ConstructionRouteStep) bool {
	plan := authoringv2.ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Kind != authoringv2.SourceCodedValue || plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil || plan.Source.Lookup.Key == nil {
		return false
	}
	compilerRoute := make([]compilerprobe.Traversal, 0, len(route))
	for _, step := range route {
		matchMode := spec.TraversalMatchOptional
		if step.MatchMode == string(spec.TraversalMatchRequired) {
			matchMode = spec.TraversalMatchRequired
		}
		compilerRoute = append(compilerRoute, compilerprobe.Traversal{FromResourceType: step.FromResourceType, EdgeLabel: step.Relationship, ToResourceType: step.ToResourceType, MatchMode: matchMode})
	}
	_, err := compilerprobe.ProbeOwnerRecords(ctx, compilerprobe.OwnerRecordsRequest{
		Scope: compilerprobe.Scope{
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

func semanticConstructionCandidate(snapshot capability.Snapshot, observation catalog.SemanticObservation) (capability.Candidate, bool) {
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

func semanticCandidateWithFallback(snapshot capability.Snapshot, observation catalog.SemanticObservation, primary capability.Candidate) (capability.Candidate, bool) {
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

// Coded-value observations keep key and value selectors relative to the object
// that owns their correlation. Capability candidates address the scalar from
// the resource root, so the owning scope is restored only at this boundary.
func semanticObservationValuePath(observation catalog.SemanticObservation) string {
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
