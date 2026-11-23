package semanticbrowse

import (
	"errors"
	"strings"

	"github.com/calypr/loom/internal/explorer/semanticauthoring"

	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/semantic"
)

const (
	semanticFieldPresentationRevision = "semantic-field-presentation/v1"
	fieldPresentationCandidateLimit   = 50
	fieldPresentationReplacementLimit = 100
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

type FieldPresentationRequest struct {
	ExpectedDraftVersion int64
	ExpectedDraftDigest  string
	SourceNodeID         string
	OccurrenceID         string
	StageID              *string
	CandidateIDs         []string
}

func (request FieldPresentationRequest) Validate() error {
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

type FieldPresentationResultKind string

type FieldPresentationDecisionKind string

type SemanticInventoryItemIdentityKind string

type FieldPresentationAccess string

const (
	FieldPresentationResultUnavailable  FieldPresentationResultKind       = "UNAVAILABLE"
	FieldPresentationResultReady        FieldPresentationResultKind       = "READY"
	FieldPresentationDecisionKeepRaw    FieldPresentationDecisionKind     = "KEEP_RAW"
	FieldPresentationDecisionGroup      FieldPresentationDecisionKind     = "GROUP_UNDER_SEMANTIC"
	SemanticInventoryIdentityExact      SemanticInventoryItemIdentityKind = "EXACT_CONCEPT"
	SemanticInventoryIdentityFamily     SemanticInventoryItemIdentityKind = "SYSTEM_VERSION_CATEGORY"
	FieldPresentationAccessSourceFields FieldPresentationAccess           = "SOURCE_FIELDS"
)

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

type SemanticInventoryItem struct {
	ConceptID              string                                       `json:"conceptId"`
	BindingID              string                                       `json:"bindingId"`
	SelectionMode          string                                       `json:"selectionMode,omitempty"`
	FamilyID               string                                       `json:"familyId,omitempty"`
	ResourceType           string                                       `json:"resourceType"`
	SourcePath             string                                       `json:"sourcePath"`
	System                 string                                       `json:"system"`
	Code                   string                                       `json:"code"`
	CodingVersion          string                                       `json:"codingVersion"`
	Display                string                                       `json:"display"`
	ValueSelector          string                                       `json:"valueSelector"`
	ValueType              string                                       `json:"valueType"`
	OwningScope            string                                       `json:"owningScope"`
	Occurrences            int64                                        `json:"occurrences"`
	Examples               []string                                     `json:"examples,omitempty"`
	ExamplesTruncated      bool                                         `json:"examplesTruncated"`
	ObservedUnits          []string                                     `json:"observedUnits,omitempty"`
	ObservedUnitsTruncated bool                                         `json:"observedUnitsTruncated"`
	Completeness           semantic.Completeness                        `json:"completeness,omitempty"`
	Readiness              semanticauthoring.SemanticSelectionReadiness `json:"readiness"`
	ConstructionChoice     *capability.ConstructionChoice               `json:"constructionChoice,omitempty"`
}
