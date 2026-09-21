package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

type RowDefinitionSelectionKind string

const (
	RowDefinitionSelectionRecords       RowDefinitionSelectionKind = "RECORDS"
	RowDefinitionSelectionFieldGroup    RowDefinitionSelectionKind = "FIELD_GROUP"
	RowDefinitionSelectionExplicitGroup RowDefinitionSelectionKind = "EXPLICIT_GROUP"
	RowDefinitionSelectionExpanded      RowDefinitionSelectionKind = "EXPANDED"
)

// RowDefinitionSelection is the closed browser-intent union. It contains only
// opaque identities and user-selected policies; schema paths are resolved by
// RowChoiceResolver after the snapshot and output have been checked.
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
		if err := requireExactIdentity(s.FieldGroup.RowChoiceID, "rowChoiceId"); err != nil {
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
		if err := requireExactIdentity(s.ExplicitGroup.RevisionID, "revisionId"); err != nil {
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
		if err := requireExactIdentity(s.Expanded.RowChoiceID, "rowChoiceId"); err != nil {
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

func requireExactIdentity(value, name string) error {
	if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("%s must be an exact non-empty token", name)
	}
	return nil
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
	ProposalID               string                     `json:"proposalId,omitempty"`
	BaseReceiptID            string                     `json:"baseReceiptId"`
	OutputID                 string                     `json:"outputId"`
	SnapshotToken            string                     `json:"snapshotToken"`
	DraftVersion             int64                      `json:"draftVersion"`
	DraftDigest              string                     `json:"draftDigest"`
	BaseDocumentDigest       string                     `json:"baseDocumentDigest"`
	CandidateWorkspaceDigest string                     `json:"candidateWorkspaceDigest"`
	Mode                     RowDefinitionSelectionKind `json:"mode"`
	Comparison               RowDefinitionComparison    `json:"comparison"`
}

type RowChoiceKind string

const (
	RowChoiceFieldGroup RowChoiceKind = "FIELD_GROUP"
	RowChoiceExpanded   RowChoiceKind = "EXPANDED"
)

// RowChoiceResolveRequest is the server-side identity used to verify a choice
// against one output and one immutable capability snapshot.
type RowChoiceResolveRequest struct {
	Project      string
	ExplorerID   string
	OutputID     string
	Snapshot     capability.Snapshot
	Route        authoringv2.RouteNode
	RowChoiceID  string
	ExpectedKind RowChoiceKind
}

type ResolvedRowChoice struct {
	Kind         RowChoiceKind
	OccurrenceID string
	FieldPath    string
	ScopePath    string
}

type RowChoiceResolver interface {
	ResolveRowChoiceID(context.Context, RowChoiceResolveRequest) (ResolvedRowChoice, error)
}

type RowChoicePlanner interface {
	ListRowChoices(context.Context, capability.Snapshot, authoringv2.Document) ([]capability.RowChoice, error)
}

type ExplicitGroupRevisionResolveRequest struct {
	Project          string
	ExplorerID       string
	OutputID         string
	Snapshot         capability.Snapshot
	RevisionID       string
	RootResourceType string
}

type ExplicitGroupRevisionListRequest struct {
	Project          string
	Snapshot         capability.Snapshot
	RootResourceType string
}

type ExplicitGroupRevisionChoice struct {
	RevisionID               string                               `json:"revisionId"`
	GroupCount               int64                                `json:"groupCount"`
	MemberCount              int64                                `json:"memberCount"`
	CreatedAt                time.Time                            `json:"createdAt"`
	UnassignedMemberPolicies []authoringv2.UnassignedMemberPolicy `json:"unassignedMemberPolicies"`
}

// ExplicitGroupRevisionProof contains the immutable identities a group
// adapter validates before a proposal can use a revision.
type ExplicitGroupRevisionProof struct {
	RevisionID                string
	Project                   string
	SourceGeneration          string
	AuthorizationScopeDigest  string
	RootResourceType          string
	SelectionRevisionID       string
	SelectionMembershipDigest string
	DefinitionDigest          string
	MembershipDigest          string
	Complete                  bool
}

type ExplicitGroupRevisionResolver interface {
	ListExplicitGroupRevisions(context.Context, ExplicitGroupRevisionListRequest) ([]ExplicitGroupRevisionChoice, error)
	ResolveExplicitGroupRevision(context.Context, ExplicitGroupRevisionResolveRequest) (ExplicitGroupRevisionProof, error)
	ValidateCompilationReceipt(context.Context, ExplicitGroupRevisionProof, *explorer.CompilationReceipt) error
}

// ProposeRowDefinition compiles a complete candidate workspace without saving
// the draft. The candidate receipt ID is the proposal ID; no mutable proposal
// store is involved.
func (s *Service) ProposeRowDefinition(ctx context.Context, request RowDefinitionProposalRequest) (RowDefinitionProposal, error) {
	if err := request.Validate(); err != nil {
		return RowDefinitionProposal{}, malformed("row-definition-proposal", err.Error(), err)
	}
	if s.config.Capability.ForCompilation == nil || s.config.Capability.Catalog == nil || s.config.CompileReceipt == nil {
		return RowDefinitionProposal{}, unavailable("row-definition-proposal", "PROPOSAL_UNAVAILABLE", "row definition proposal compilation is not configured", nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	snapshot := authorized.Snapshot
	if err != nil || snapshot.ValidateToken(request.SnapshotToken) != nil || projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(request.Project) {
		return RowDefinitionProposal{}, conflict("row-definition-proposal", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, err)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return RowDefinitionProposal{}, conflict("row-definition-proposal", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	owner, err := s.store.Get(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return RowDefinitionProposal{}, err
	}
	if owner.DraftVersion != request.ExpectedDraftVersion || owner.DraftDigest != request.ExpectedDraftDigest {
		return RowDefinitionProposal{}, conflict("row-definition-proposal", "DRAFT_CONFLICT", "the Explorer draft changed; reload before proposing a row definition", nil, explorer.ErrDraftConflict)
	}
	workspace, err := previewWorkspace(ctx, s.store, owner)
	if err != nil {
		return RowDefinitionProposal{}, conflict("row-definition-proposal", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be proposed", nil, err)
	}
	baseDigest, err := workspace.Digest()
	if err != nil || baseDigest != owner.DraftDigest {
		return RowDefinitionProposal{}, conflict("row-definition-proposal", "DRAFT_CONFLICT", "the saved workspace does not match its draft digest", nil, err)
	}
	baseDocument := proposalDocument(workspace, request.OutputID)
	if baseDocument == nil {
		return RowDefinitionProposal{}, unprocessable("row-definition-proposal", "OUTPUT_NOT_FOUND", "outputId does not identify a saved table", nil)
	}
	baseDocumentDigest, err := documentDigest(*baseDocument)
	if err != nil {
		return RowDefinitionProposal{}, err
	}
	baseGroupProof, err := s.resolveExplicitGroupForDocument(ctx, request.Project, request.ExplorerID, request.OutputID, snapshot, *baseDocument, nil)
	if err != nil {
		return RowDefinitionProposal{}, err
	}
	rows, candidateGroupProof, err := s.resolveRowDefinitionSelection(ctx, request, snapshot, *baseDocument)
	if err != nil {
		return RowDefinitionProposal{}, err
	}
	command := authoringv2.Command{Type: authoringv2.CommandApplyRowDefinitionProposal, OutputID: request.OutputID, ProposalID: "proposal-preview"}
	if err := command.ResolveRowDefinitionProposal(rows); err != nil {
		return RowDefinitionProposal{}, unprocessable("row-definition-proposal", "INVALID_ROW_DEFINITION", err.Error(), err)
	}
	candidateWorkspace, _, err := authoringv2.ApplyCommands(workspace, s.config.Capability.Catalog(snapshot, request.ExplorerID), "row-definition-preview", []authoringv2.Command{command})
	if err != nil {
		return RowDefinitionProposal{}, unprocessable("row-definition-proposal", "INVALID_ROW_DEFINITION", err.Error(), err)
	}
	if _, err := rowDefinitionWorkspaceChange(workspace, candidateWorkspace, request.OutputID); err != nil {
		return RowDefinitionProposal{}, unprocessable("row-definition-proposal", "INVALID_ROW_DEFINITION", err.Error(), err)
	}
	candidateDigest, err := candidateWorkspace.Digest()
	if err != nil {
		return RowDefinitionProposal{}, err
	}
	limit := request.Limit
	if limit == 0 {
		limit = dataframeexecution.DefaultPreviewLimit
	}
	baseReceipt, err := s.compile(ctx, compileRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: workspace, SnapshotToken: request.SnapshotToken, RequestID: "row-definition-proposal-base"})
	if err != nil {
		return RowDefinitionProposal{}, err
	}
	if _, err := s.verifyRowDefinitionReceipt(ctx, baseReceipt, request.Project, request.ExplorerID, request.SnapshotToken, snapshot, &workspace); err != nil {
		return RowDefinitionProposal{}, err
	}
	if err := s.validateExplicitGroupReceipt(ctx, baseGroupProof, baseReceipt); err != nil {
		return RowDefinitionProposal{}, err
	}
	if request.Selection.Kind == RowDefinitionSelectionFieldGroup {
		comparison, err := s.previewUnavailableRowDefinitionComparison(ctx, request, snapshot, baseReceipt, "GROUPED_ROW_COMPILER_UNAVAILABLE", "FIELD_GROUP row-definition execution is unavailable in this workflow.", limit)
		if err != nil {
			return RowDefinitionProposal{}, err
		}
		return RowDefinitionProposal{
			BaseReceiptID: baseReceipt.ID, OutputID: request.OutputID, SnapshotToken: request.SnapshotToken,
			DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest, BaseDocumentDigest: baseDocumentDigest,
			CandidateWorkspaceDigest: candidateDigest, Mode: request.Selection.Kind, Comparison: comparison,
		}, nil
	}
	binding := &explorer.RowDefinitionProposalBinding{
		DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest, OutputID: request.OutputID,
		BaseDocumentDigest: baseDocumentDigest, CandidateWorkspaceDigest: candidateDigest,
		SnapshotToken: request.SnapshotToken,
	}
	candidateReceipt, err := s.compile(ctx, compileRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, Workspace: candidateWorkspace,
		SnapshotToken: request.SnapshotToken, RequestID: "row-definition-proposal-candidate",
		RowDefinitionProposal: binding,
	})
	if err != nil {
		return RowDefinitionProposal{}, err
	}
	if _, err := s.verifyRowDefinitionReceipt(ctx, candidateReceipt, request.Project, request.ExplorerID, request.SnapshotToken, snapshot, &candidateWorkspace); err != nil {
		return RowDefinitionProposal{}, err
	}
	if err := s.validateExplicitGroupReceipt(ctx, candidateGroupProof, candidateReceipt); err != nil {
		return RowDefinitionProposal{}, err
	}
	comparison, err := s.compareRowDefinitionReceipts(ctx, request, snapshot, baseReceipt, candidateReceipt, limit)
	if err != nil {
		return RowDefinitionProposal{}, err
	}
	return RowDefinitionProposal{
		ProposalID: candidateReceipt.ID, BaseReceiptID: baseReceipt.ID, OutputID: request.OutputID,
		SnapshotToken: request.SnapshotToken, DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest,
		BaseDocumentDigest: baseDocumentDigest, CandidateWorkspaceDigest: candidateDigest, Mode: request.Selection.Kind,
		Comparison: comparison,
	}, nil
}

func (s *Service) resolveRowDefinitionSelection(ctx context.Context, request RowDefinitionProposalRequest, snapshot capability.Snapshot, document authoringv2.Document) (authoringv2.RowDefinition, *ExplicitGroupRevisionProof, error) {
	switch request.Selection.Kind {
	case RowDefinitionSelectionRecords:
		return authoringv2.RecordsRowDefinition(), nil, nil
	case RowDefinitionSelectionFieldGroup:
		if s.config.RowChoiceResolver == nil {
			return authoringv2.RowDefinition{}, nil, unavailable("row-definition-proposal", "ROW_CHOICE_UNAVAILABLE", "row choice resolution is not configured", nil)
		}
		resolved, err := s.config.RowChoiceResolver.ResolveRowChoiceID(ctx, RowChoiceResolveRequest{
			Project: request.Project, ExplorerID: request.ExplorerID, OutputID: request.OutputID,
			Snapshot: snapshot.Clone(), Route: document.Route, RowChoiceID: request.Selection.FieldGroup.RowChoiceID, ExpectedKind: RowChoiceFieldGroup,
		})
		if err != nil || validateResolvedRowChoice(document.Route, resolved, RowChoiceFieldGroup) != nil {
			return authoringv2.RowDefinition{}, nil, conflict("row-definition-proposal", "STALE_ROW_CHOICE", "the selected field grouping choice is stale or unavailable", nil, err)
		}
		return authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionGroups, Groups: &authoringv2.GroupedRows{
			Source: authoringv2.GroupSource{Kind: authoringv2.GroupSourceField, Field: &authoringv2.FieldGroupSource{
				OccurrenceID: resolved.OccurrenceID, FieldPath: resolved.FieldPath, MissingKeyPolicy: request.Selection.FieldGroup.MissingKeyPolicy,
			}},
		}}, nil, nil
	case RowDefinitionSelectionExplicitGroup:
		policy := request.Selection.ExplicitGroup
		proof, err := s.resolveExplicitGroupRevision(ctx, ExplicitGroupRevisionResolveRequest{
			Project: request.Project, ExplorerID: request.ExplorerID, OutputID: request.OutputID,
			Snapshot: snapshot.Clone(), RevisionID: policy.RevisionID, RootResourceType: document.RootResourceType,
		})
		if err != nil {
			return authoringv2.RowDefinition{}, nil, err
		}
		return authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionGroups, Groups: &authoringv2.GroupedRows{
			Source: authoringv2.GroupSource{Kind: authoringv2.GroupSourceExplicit, Explicit: &authoringv2.ExplicitGroupSource{
				RevisionID: proof.RevisionID, UnassignedMemberPolicy: policy.UnassignedMemberPolicy,
			}},
		}}, &proof, nil
	case RowDefinitionSelectionExpanded:
		if s.config.RowChoiceResolver == nil {
			return authoringv2.RowDefinition{}, nil, unavailable("row-definition-proposal", "ROW_CHOICE_UNAVAILABLE", "row choice resolution is not configured", nil)
		}
		resolved, err := s.config.RowChoiceResolver.ResolveRowChoiceID(ctx, RowChoiceResolveRequest{
			Project: request.Project, ExplorerID: request.ExplorerID, OutputID: request.OutputID,
			Snapshot: snapshot.Clone(), Route: document.Route, RowChoiceID: request.Selection.Expanded.RowChoiceID, ExpectedKind: RowChoiceExpanded,
		})
		if err != nil || validateResolvedRowChoice(document.Route, resolved, RowChoiceExpanded) != nil {
			return authoringv2.RowDefinition{}, nil, conflict("row-definition-proposal", "STALE_ROW_CHOICE", "the selected expansion choice is stale or unavailable", nil, err)
		}
		return authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionExpanded, Expanded: &authoringv2.ExpandedRows{
			OccurrenceID: resolved.OccurrenceID, ScopePath: resolved.ScopePath,
			EmptyCollectionPolicy: request.Selection.Expanded.EmptyCollectionPolicy,
		}}, nil, nil
	default:
		return authoringv2.RowDefinition{}, nil, malformed("row-definition-proposal", "row selection kind is unsupported", nil)
	}
}

func validateResolvedRowChoice(route authoringv2.RouteNode, resolved ResolvedRowChoice, expected RowChoiceKind) error {
	if resolved.Kind != expected || requireExactIdentity(resolved.OccurrenceID, "resolved occurrenceId") != nil || !routeContainsOccurrence(route, resolved.OccurrenceID) {
		return fmt.Errorf("row choice does not identify an authored route occurrence")
	}
	switch expected {
	case RowChoiceFieldGroup:
		if requireExactIdentity(resolved.FieldPath, "resolved fieldPath") != nil || resolved.ScopePath != "" {
			return fmt.Errorf("field-group choice has an invalid resolved field")
		}
	case RowChoiceExpanded:
		if requireExactIdentity(resolved.ScopePath, "resolved scopePath") != nil || resolved.FieldPath != "" {
			return fmt.Errorf("expanded choice has an invalid resolved scope")
		}
	default:
		return fmt.Errorf("row choice kind is unsupported")
	}
	return nil
}

func routeContainsOccurrence(route authoringv2.RouteNode, occurrenceID string) bool {
	if route.OccurrenceID == occurrenceID {
		return true
	}
	for _, child := range route.Children {
		if routeContainsOccurrence(child, occurrenceID) {
			return true
		}
	}
	return false
}

func (s *Service) resolveExplicitGroupForDocument(ctx context.Context, project, explorerID, outputID string, snapshot capability.Snapshot, document authoringv2.Document, receipt *explorer.CompilationReceipt) (*ExplicitGroupRevisionProof, error) {
	rows := document.Rows
	if rows.Kind != authoringv2.RowDefinitionGroups || rows.Groups == nil || rows.Groups.Source.Kind != authoringv2.GroupSourceExplicit || rows.Groups.Source.Explicit == nil {
		return nil, nil
	}
	return s.resolveExplicitGroupRevisionForRows(ctx, project, explorerID, outputID, snapshot, document, rows, receipt)
}

func (s *Service) resolveExplicitGroupRevision(ctx context.Context, request ExplicitGroupRevisionResolveRequest) (ExplicitGroupRevisionProof, error) {
	if s.config.ExplicitGroupResolver == nil {
		return ExplicitGroupRevisionProof{}, unavailable("row-definition-proposal", "EXPLICIT_GROUP_UNAVAILABLE", "explicit group revision validation is not configured", nil)
	}
	proof, err := s.config.ExplicitGroupResolver.ResolveExplicitGroupRevision(ctx, request)
	if err != nil {
		return ExplicitGroupRevisionProof{}, conflict("row-definition-proposal", "STALE_EXPLICIT_GROUP_REVISION", "the explicit group revision is stale or unavailable", nil, err)
	}
	if err := validateExplicitGroupRevisionProof(proof, request); err != nil {
		return ExplicitGroupRevisionProof{}, conflict("row-definition-proposal", "STALE_EXPLICIT_GROUP_REVISION", "the explicit group revision does not match the current project, snapshot, or table", nil, err)
	}
	return proof, nil
}

func (s *Service) resolveExplicitGroupRevisionForRows(ctx context.Context, project, explorerID, outputID string, snapshot capability.Snapshot, document authoringv2.Document, rows authoringv2.RowDefinition, receipt *explorer.CompilationReceipt) (*ExplicitGroupRevisionProof, error) {
	if rows.Kind != authoringv2.RowDefinitionGroups || rows.Groups == nil || rows.Groups.Source.Kind != authoringv2.GroupSourceExplicit || rows.Groups.Source.Explicit == nil {
		return nil, nil
	}
	request := ExplicitGroupRevisionResolveRequest{
		Project: project, ExplorerID: explorerID, OutputID: outputID, Snapshot: snapshot.Clone(),
		RevisionID: rows.Groups.Source.Explicit.RevisionID, RootResourceType: document.RootResourceType,
	}
	proof, err := s.resolveExplicitGroupRevision(ctx, request)
	if err != nil {
		return nil, err
	}
	if receipt != nil {
		if err := s.validateExplicitGroupReceipt(ctx, &proof, receipt); err != nil {
			return nil, err
		}
	}
	return &proof, nil
}

func validateExplicitGroupRevisionProof(proof ExplicitGroupRevisionProof, request ExplicitGroupRevisionResolveRequest) error {
	if proof.RevisionID != request.RevisionID || projectid.Canonical(proof.Project) != projectid.Canonical(request.Project) ||
		proof.SourceGeneration != request.Snapshot.Identity.Generation || proof.AuthorizationScopeDigest != request.Snapshot.Identity.AuthorizationScopeDigest ||
		proof.RootResourceType != request.RootResourceType || !proof.Complete {
		return fmt.Errorf("explicit group revision identity is incomplete or stale")
	}
	for name, value := range map[string]string{
		"selectionRevisionId": proof.SelectionRevisionID, "selectionMembershipDigest": proof.SelectionMembershipDigest,
		"definitionDigest": proof.DefinitionDigest, "membershipDigest": proof.MembershipDigest,
	} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("explicit group proof %s is missing", name)
		}
	}
	return nil
}

func (s *Service) validateExplicitGroupReceipt(ctx context.Context, proof *ExplicitGroupRevisionProof, receipt *explorer.CompilationReceipt) error {
	if proof == nil {
		return nil
	}
	if s.config.ExplicitGroupResolver == nil {
		return unavailable("row-definition-proposal", "EXPLICIT_GROUP_UNAVAILABLE", "explicit group receipt validation is not configured", nil)
	}
	if err := s.config.ExplicitGroupResolver.ValidateCompilationReceipt(ctx, *proof, receipt); err != nil {
		return conflict("row-definition-proposal", "INVALID_EXPLICIT_GROUP_RECEIPT", "the candidate receipt does not prove the exact explicit group revision", nil, err)
	}
	return nil
}

func (s *Service) verifyRowDefinitionReceipt(ctx context.Context, receipt *explorer.CompilationReceipt, project, explorerID, snapshotToken string, snapshot capability.Snapshot, expected *authoringv2.Workspace) (authoringv2.Workspace, error) {
	if receipt == nil || strings.TrimSpace(receipt.ID) == "" {
		return authoringv2.Workspace{}, conflict("row-definition-proposal", "INVALID_COMPILATION_RECEIPT", "the compiled receipt is missing", nil, nil)
	}
	if err := validateCandidateReceiptIdentity(receipt, project, explorerID, snapshotToken, snapshot); err != nil {
		return authoringv2.Workspace{}, conflict("row-definition-proposal", "INVALID_COMPILATION_RECEIPT", "the compiled receipt capability identity does not match the request", nil, err)
	}
	if err := receipt.Validate(); err != nil {
		return authoringv2.Workspace{}, conflict("row-definition-proposal", "INVALID_COMPILATION_RECEIPT", "the compiled receipt failed integrity validation", nil, err)
	}
	if strings.TrimSpace(receipt.IntentDigest) == "" {
		return authoringv2.Workspace{}, conflict("row-definition-proposal", "INVALID_COMPILATION_RECEIPT", "the compiled receipt has no authoring intent digest", nil, nil)
	}
	if expected != nil {
		expectedDigest, err := expected.Digest()
		if err != nil {
			return authoringv2.Workspace{}, err
		}
		if receipt.IntentDigest != expectedDigest {
			return authoringv2.Workspace{}, conflict("row-definition-proposal", "INVALID_COMPILATION_RECEIPT", "the compiled receipt does not represent the expected workspace", nil, nil)
		}
	}
	compiled, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
	if err != nil {
		return authoringv2.Workspace{}, conflict("row-definition-proposal", "INVALID_COMPILATION_RECEIPT", "the compiled receipt workspace is invalid", nil, err)
	}
	compiledDigest, err := compiled.Digest()
	if err != nil || compiledDigest != receipt.IntentDigest {
		return authoringv2.Workspace{}, conflict("row-definition-proposal", "INVALID_COMPILATION_RECEIPT", "the compiled receipt workspace digest does not match its intent digest", nil, err)
	}
	return compiled, nil
}

func (s *Service) prepareRowDefinitionProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, snapshot capability.Snapshot, current authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, *explorer.CompilationReceipt, error) {
	if len(commands) != 1 || commands[0].Type != authoringv2.CommandApplyRowDefinitionProposal {
		return nil, nil, malformed("commands", "APPLY_ROW_DEFINITION_PROPOSAL must be the only command in its atomic request", nil)
	}
	command := &commands[0]
	receipt, err := s.lookupReceipt(ctx, project, explorerID, command.ProposalID)
	if err != nil {
		return nil, nil, err
	}
	if receipt == nil || receipt.ID != command.ProposalID {
		return nil, nil, conflict("commands", "INVALID_ROW_DEFINITION_PROPOSAL", "the proposal ID does not match a candidate receipt", nil, nil)
	}
	binding := receipt.RowDefinitionProposal
	if binding == nil || binding.DraftVersion != request.ExpectedDraftVersion || binding.DraftDigest != request.ExpectedDraftDigest ||
		binding.OutputID != command.OutputID || binding.SnapshotToken != request.SnapshotToken {
		return nil, nil, conflict("commands", "STALE_ROW_DEFINITION_PROPOSAL", "the proposal is not bound to this exact draft, output, and snapshot", nil, nil)
	}
	currentDigest, err := current.Digest()
	if err != nil {
		return nil, nil, err
	}
	if currentDigest != binding.DraftDigest {
		return nil, nil, conflict("commands", "STALE_ROW_DEFINITION_PROPOSAL", "the saved draft no longer matches the proposal base", nil, nil)
	}
	baseDocument := proposalDocument(current, command.OutputID)
	if baseDocument == nil {
		return nil, nil, conflict("commands", "STALE_ROW_DEFINITION_PROPOSAL", "the proposal output is missing from the saved draft", nil, nil)
	}
	baseDocumentDigest, err := documentDigest(*baseDocument)
	if err != nil {
		return nil, nil, err
	}
	if baseDocumentDigest != binding.BaseDocumentDigest {
		return nil, nil, conflict("commands", "STALE_ROW_DEFINITION_PROPOSAL", "the target table changed after the proposal was compiled", nil, nil)
	}
	candidate, err := s.verifyRowDefinitionReceipt(ctx, receipt, project, explorerID, request.SnapshotToken, snapshot, nil)
	if err != nil {
		return nil, nil, err
	}
	candidateDocument := proposalDocument(candidate, command.OutputID)
	if candidateDocument == nil {
		return nil, nil, conflict("commands", "INVALID_ROW_DEFINITION_PROPOSAL", "the candidate receipt does not contain the requested output", nil, nil)
	}
	if _, err := rowDefinitionWorkspaceChange(current, candidate, command.OutputID); err != nil {
		return nil, nil, conflict("commands", "STALE_ROW_DEFINITION_PROPOSAL", "the candidate receipt no longer changes only the requested table rows", nil, err)
	}
	if _, err := s.resolveExplicitGroupRevisionForRows(ctx, project, explorerID, command.OutputID, snapshot, *candidateDocument, candidateDocument.Rows, receipt); err != nil {
		return nil, nil, err
	}
	if err := command.ResolveRowDefinitionProposal(candidateDocument.Rows); err != nil {
		return nil, nil, conflict("commands", "INVALID_ROW_DEFINITION_PROPOSAL", "the candidate row definition is invalid", nil, err)
	}
	return commands, receipt, nil
}

func (s *Service) checkRowDefinitionProposalResult(workspace authoringv2.Workspace, receipt *explorer.CompilationReceipt) error {
	if receipt == nil {
		return conflict("commands", "INVALID_ROW_DEFINITION_PROPOSAL", "the candidate receipt was not prepared", nil, nil)
	}
	digest, err := workspace.Digest()
	if err != nil {
		return fmt.Errorf("digest applied row-definition workspace: %w", err)
	}
	if digest != receipt.IntentDigest {
		return conflict("commands", "ROW_DEFINITION_PROPOSAL_MISMATCH", "the applied row definition does not match the candidate receipt workspace", nil, nil)
	}
	return nil
}

func rowDefinitionWorkspaceChange(base, candidate authoringv2.Workspace, outputID string) (authoringv2.Document, error) {
	baseWithoutRows := base
	candidateWithoutRows := candidate
	baseWithoutRows.Documents = append([]authoringv2.Document(nil), base.Documents...)
	candidateWithoutRows.Documents = append([]authoringv2.Document(nil), candidate.Documents...)
	baseDocument, candidateDocument := -1, -1
	for index := range base.Documents {
		if base.Documents[index].Output.ID == outputID {
			baseDocument = index
		}
	}
	for index := range candidate.Documents {
		if candidate.Documents[index].Output.ID == outputID {
			candidateDocument = index
		}
	}
	if baseDocument < 0 || candidateDocument < 0 || baseDocument != candidateDocument {
		return authoringv2.Document{}, fmt.Errorf("output %q is missing or moved in the candidate workspace", outputID)
	}
	if reflect.DeepEqual(base.Documents[baseDocument].Rows, candidate.Documents[candidateDocument].Rows) {
		return authoringv2.Document{}, fmt.Errorf("candidate row definition does not change output %q", outputID)
	}
	for index := range base.Documents {
		if index != baseDocument && !reflect.DeepEqual(base.Documents[index].Rows, candidate.Documents[index].Rows) {
			return authoringv2.Document{}, fmt.Errorf("candidate changes rows for output %q outside the requested output", base.Documents[index].Output.ID)
		}
		baseWithoutRows.Documents[index].Rows = authoringv2.RowDefinition{}
		candidateWithoutRows.Documents[index].Rows = authoringv2.RowDefinition{}
	}
	if !reflect.DeepEqual(baseWithoutRows, candidateWithoutRows) {
		return authoringv2.Document{}, fmt.Errorf("candidate changes authoring fields outside Document.Rows")
	}
	return candidate.Documents[candidateDocument], nil
}

func proposalDocument(workspace authoringv2.Workspace, outputID string) *authoringv2.Document {
	for index := range workspace.Documents {
		if workspace.Documents[index].Output.ID == outputID {
			return &workspace.Documents[index]
		}
	}
	return nil
}

func documentDigest(document authoringv2.Document) (string, error) {
	raw, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("encode row-definition document digest: %w", err)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
