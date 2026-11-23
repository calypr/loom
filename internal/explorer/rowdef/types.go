package rowdef

import (
	"context"
	"fmt"
	"strings"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/explicitgroup"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
)

type CompileRequest struct {
	Project               string
	ExplorerID            string
	Workspace             authoringv2.Workspace
	SnapshotToken         string
	RequestID             string
	RowDefinitionProposal *explorer.RowDefinitionProposalBinding
}

type Config struct {
	Store                      *explorer.Service
	SelectionMembersCollection string
	Capability                 ports.CapabilityResolver

	GetOwner                       func(context.Context, string, string) (*explorer.Explorer, error)
	Compile                        func(context.Context, CompileRequest) (*explorer.CompilationReceipt, error)
	LookupReceipt                  func(context.Context, string, string, string) (*explorer.CompilationReceipt, error)
	ValidateExecutableReceiptRoute func(*explorer.CompilationReceipt, string, string) error
	VerifyProposalReceipt          func(context.Context, string, *explorer.CompilationReceipt, string, string, string, capability.Snapshot, *authoringv2.Workspace) (authoringv2.Workspace, error)
	PrepareCandidateWorkspace      func(authoringv2.Workspace, string, bool) (authoringv2.Workspace, error)

	PreviewReceipt        func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error)
	ValidateReceiptStream func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings) error
	RowLineage            func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.RowLineageRequest) (dataframeexecution.RowLineageResult, error)

	RowChoiceResolver     RowChoiceResolver
	RowChoicePlanner      RowChoicePlanner
	ExplicitGroupResolver explicitgroup.ExplicitGroupRevisionResolver
}

type Service struct {
	config Config
}

func NewService(config Config) (*Service, error) {
	if config.Store == nil {
		return nil, fmt.Errorf("Explorer row definition store is required")
	}
	return &Service{config: config}, nil
}

type AssessRowChangeRequest struct {
	Mode             string
	Project          string
	ExplorerID       string
	SnapshotToken    string
	DraftVersion     int64
	DraftDigest      string
	OutputID         string
	RootNodeID       string
	RootOccurrenceID string
	RouteRebase      []authoringv2.RouteRebaseChoice
}

type AssessRowChangeResult struct {
	CandidateDocument    *authoringv2.Document
	CandidateNetwork     *authoringv2.AuthoringNetwork
	CandidateRowIdentity *authoringv2.RowIdentityPlan
	SnapshotToken        string
	DraftVersion         int64
	DraftDigest          string
	CandidateReceiptID   string
	Assessment           authoringv2.RowChangeAssessment
}

type RowDefinitionChoicesRequest struct {
	Project       string
	ExplorerID    string
	SnapshotToken string
	OutputID      string
}

type RowDefinitionChoicesResponse struct {
	SnapshotToken  string                                      `json:"snapshotToken"`
	OutputID       string                                      `json:"outputId"`
	Choices        []RowDefinitionChoice                       `json:"choices"`
	ExplicitGroups []explicitgroup.ExplicitGroupRevisionChoice `json:"explicitGroups"`
}

type RowLineageRequest struct {
	Project    string
	ExplorerID string
	ReceiptID  string
	OutputID   string
	RowID      string
	Offset     int
	Limit      int
}

type RowLineageResult struct {
	ReceiptID    string
	OutputID     string
	RowID        string
	Status       string
	Contributors []dataframeexecution.RowLineageContributor
	HasMore      bool
	NextOffset   *int
	ReasonCode   string
	Operation    string
}

type RowDefinitionSelectionKind string

const (
	RowDefinitionSelectionRecords       RowDefinitionSelectionKind = "RECORDS"
	RowDefinitionSelectionFieldGroup    RowDefinitionSelectionKind = "FIELD_GROUP"
	RowDefinitionSelectionExplicitGroup RowDefinitionSelectionKind = "EXPLICIT_GROUP"
	RowDefinitionSelectionExpanded      RowDefinitionSelectionKind = "EXPANDED"
)

type RowDefinitionSelection struct {
	Kind          RowDefinitionSelectionKind `json:"kind"`
	FieldGroup    *FieldGroupSelection       `json:"fieldGroup,omitempty"`
	ExplicitGroup *ExplicitGroupSelection    `json:"explicitGroup,omitempty"`
	Expanded      *ExpandedSelection         `json:"expanded,omitempty"`
}

type FieldGroupSelection struct {
	RowChoiceID      string                       `json:"rowChoiceId"`
	MissingKeyPolicy authoringv2.MissingKeyPolicy `json:"missingKeyPolicy"`
}

type ExplicitGroupSelection struct {
	RevisionID             string                             `json:"revisionId"`
	UnassignedMemberPolicy authoringv2.UnassignedMemberPolicy `json:"unassignedMemberPolicy"`
}

type ExpandedSelection struct {
	RowChoiceID           string                            `json:"rowChoiceId"`
	EmptyCollectionPolicy authoringv2.EmptyCollectionPolicy `json:"emptyCollectionPolicy"`
}

func (s RowDefinitionSelection) Validate() error {
	payloads := 0
	if s.FieldGroup != nil {
		payloads++
	}
	if s.ExplicitGroup != nil {
		payloads++
	}
	if s.Expanded != nil {
		payloads++
	}
	wantPayloads := 1
	if s.Kind == RowDefinitionSelectionRecords {
		wantPayloads = 0
	}
	if payloads != wantPayloads {
		return fmt.Errorf("row selection must contain exactly the payload for kind %q", s.Kind)
	}
	switch s.Kind {
	case RowDefinitionSelectionRecords:
		return nil
	case RowDefinitionSelectionFieldGroup:
		if s.FieldGroup == nil {
			return fmt.Errorf("FIELD_GROUP payload is required")
		}
		if err := support.RequireExactIdentity(s.FieldGroup.RowChoiceID, "rowChoiceId"); err != nil {
			return err
		}
		switch s.FieldGroup.MissingKeyPolicy {
		case authoringv2.MissingKeyError, authoringv2.MissingKeyExclude, authoringv2.MissingKeyGroupAsMissing:
			return nil
		default:
			return fmt.Errorf("FIELD_GROUP missingKeyPolicy is unsupported")
		}
	case RowDefinitionSelectionExplicitGroup:
		if s.ExplicitGroup == nil {
			return fmt.Errorf("EXPLICIT_GROUP payload is required")
		}
		if err := support.RequireExactIdentity(s.ExplicitGroup.RevisionID, "revisionId"); err != nil {
			return err
		}
		switch s.ExplicitGroup.UnassignedMemberPolicy {
		case authoringv2.UnassignedMemberError, authoringv2.UnassignedMemberExclude, authoringv2.UnassignedMemberGroupAsUnassigned:
			return nil
		default:
			return fmt.Errorf("EXPLICIT_GROUP unassignedMemberPolicy is unsupported")
		}
	case RowDefinitionSelectionExpanded:
		if s.Expanded == nil {
			return fmt.Errorf("EXPANDED payload is required")
		}
		if err := support.RequireExactIdentity(s.Expanded.RowChoiceID, "rowChoiceId"); err != nil {
			return err
		}
		switch s.Expanded.EmptyCollectionPolicy {
		case authoringv2.EmptyCollectionError, authoringv2.EmptyCollectionExclude, authoringv2.EmptyCollectionPreserveParent:
			return nil
		default:
			return fmt.Errorf("EXPANDED emptyCollectionPolicy is unsupported")
		}
	default:
		return fmt.Errorf("row selection kind %q is unsupported", s.Kind)
	}
}

type RowDefinitionProposalRequest struct {
	Project              string
	ExplorerID           string
	SnapshotToken        string
	ExpectedDraftVersion int64
	ExpectedDraftDigest  string
	OutputID             string
	Selection            RowDefinitionSelection
	Limit                int
}

func (r RowDefinitionProposalRequest) Validate() error {
	for name, value := range map[string]string{
		"project": r.Project, "explorerId": r.ExplorerID, "snapshotToken": r.SnapshotToken,
		"expectedDraftDigest": r.ExpectedDraftDigest, "outputId": r.OutputID,
	} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("%s must be an exact non-empty value", name)
		}
	}
	if r.ExpectedDraftVersion < 1 {
		return fmt.Errorf("expectedDraftVersion must be positive")
	}
	if r.Limit < 0 || r.Limit > dataframeexecution.MaxPreviewLimit {
		return fmt.Errorf("limit is outside the supported range")
	}
	return r.Selection.Validate()
}

type RowDefinitionComparisonStatus string

const (
	RowDefinitionComparisonAvailable   RowDefinitionComparisonStatus = "AVAILABLE"
	RowDefinitionComparisonUnavailable RowDefinitionComparisonStatus = "UNAVAILABLE"
)

type RowDefinitionPreviewSummary struct {
	RowCount int  `json:"rowCount"`
	Sampled  bool `json:"sampled"`
}

type RowDefinitionComparisonExample struct {
	RowIdentity      string `json:"rowIdentity"`
	BasePresent      bool   `json:"basePresent"`
	CandidatePresent bool   `json:"candidatePresent"`
}

type RowDefinitionComparison struct {
	Status          RowDefinitionComparisonStatus    `json:"status"`
	ReasonCode      string                           `json:"reasonCode,omitempty"`
	Reason          string                           `json:"reason,omitempty"`
	Base            *RowDefinitionPreviewSummary     `json:"base,omitempty"`
	Candidate       *RowDefinitionPreviewSummary     `json:"candidate,omitempty"`
	AffectedColumns []string                         `json:"affectedColumns"`
	Notices         []string                         `json:"notices"`
	Examples        []RowDefinitionComparisonExample `json:"examples"`
}

type RowDefinitionProposal struct {
	CandidateDocument        authoringv2.Document         `json:"candidateDocument"`
	CandidateNetwork         authoringv2.AuthoringNetwork `json:"candidateNetwork"`
	CandidateRowIdentity     authoringv2.RowIdentityPlan  `json:"candidateRowIdentity"`
	ProposalID               string                       `json:"proposalId,omitempty"`
	BaseReceiptID            string                       `json:"baseReceiptId"`
	OutputID                 string                       `json:"outputId"`
	SnapshotToken            string                       `json:"snapshotToken"`
	DraftVersion             int64                        `json:"draftVersion"`
	DraftDigest              string                       `json:"draftDigest"`
	BaseDocumentDigest       string                       `json:"baseDocumentDigest"`
	CandidateWorkspaceDigest string                       `json:"candidateWorkspaceDigest"`
	Mode                     RowDefinitionSelectionKind   `json:"mode"`
	Comparison               RowDefinitionComparison      `json:"comparison"`
}
