package lifecycle

import (
	"context"
	"fmt"
	"strings"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

const maxConstructionChoiceProposalChoices = 100

// ConstructionChoiceProposalSelection mirrors one APPLY_CONSTRUCTION_CHOICE
// command. Title is presentation input and remains optional, just like the
// command itself.
type ConstructionChoiceProposalSelection struct {
	RowValuePolicy authoringv2.ConstructionRowValuePolicy `json:"rowValuePolicy,omitempty"`
	ChoiceID       string                                 `json:"choiceId"`
	Form           capability.ConstructionChoiceForm      `json:"form"`
	FrameID        string                                 `json:"frameId,omitempty"`
	Title          *string                                `json:"title,omitempty"`
}

type ConstructionChoiceProposalRequest struct {
	CommandID            string
	Project              string
	ExplorerID           string
	SnapshotToken        string
	ExpectedDraftVersion int64
	ExpectedDraftDigest  string
	OutputID             string
	ConstructionChoices  []ConstructionChoiceProposalSelection
	Limit                int
}

func (r ConstructionChoiceProposalRequest) Validate() error {
	for name, value := range map[string]string{
		"commandId": r.CommandID, "project": r.Project, "explorerId": r.ExplorerID, "snapshotToken": r.SnapshotToken,
		"expectedDraftDigest": r.ExpectedDraftDigest, "outputId": r.OutputID,
	} {
		if err := requireExactIdentity(value, name); err != nil {
			return err
		}
	}
	if r.ExpectedDraftVersion < 1 {
		return fmt.Errorf("expectedDraftVersion must be positive")
	}
	if len(r.ConstructionChoices) == 0 || len(r.ConstructionChoices) > maxConstructionChoiceProposalChoices {
		return fmt.Errorf("constructionChoices must contain between 1 and %d choices", maxConstructionChoiceProposalChoices)
	}
	if r.Limit < 0 || r.Limit > dataframeexecution.MaxPreviewLimit {
		return fmt.Errorf("limit must be between 1 and %d", dataframeexecution.MaxPreviewLimit)
	}
	for index, choice := range r.ConstructionChoices {
		if err := requireExactIdentity(choice.ChoiceID, fmt.Sprintf("constructionChoices[%d].choiceId", index)); err != nil {
			return err
		}
		if choice.FrameID != "" {
			if err := requireExactIdentity(choice.FrameID, fmt.Sprintf("constructionChoices[%d].frameId", index)); err != nil {
				return err
			}
		}
	}
	return nil
}

// ConstructionChoiceProposalResponse contains the exact candidate identities
// and receipt the transport must preview. PreviewReceiptID is deliberately
// transport-internal; clients receive the rendered preview instead.
type ConstructionChoiceProposalResponse struct {
	CommandID                string                                `json:"commandId"`
	SnapshotToken            string                                `json:"snapshotToken"`
	DraftVersion             int64                                 `json:"draftVersion"`
	DraftDigest              string                                `json:"draftDigest"`
	OutputID                 string                                `json:"outputId"`
	ConstructionChoices      []ConstructionChoiceProposalSelection `json:"constructionChoices"`
	CandidateWorkspaceDigest string                                `json:"candidateWorkspaceDigest"`
	CandidateColumnIDs       []string                              `json:"candidateColumnIds"`
	PreviewStatus            string                                `json:"previewStatus"`
	PreviewDurationMS        int64                                 `json:"previewDurationMs"`
	Preview                  any                                   `json:"preview,omitempty"`
	PreviewReceiptID         string                                `json:"-"`
}

// ProposeConstructionChoice compiles a candidate produced by the same
// server-side choice resolver and pure reducer used by APPLY_CONSTRUCTION_CHOICE.
// It does not save the candidate workspace or advance the draft version.
func (s *Service) ProposeConstructionChoice(ctx context.Context, request ConstructionChoiceProposalRequest) (ConstructionChoiceProposalResponse, error) {
	if err := request.Validate(); err != nil {
		return ConstructionChoiceProposalResponse{}, malformed("construction-choice-proposal", err.Error(), err)
	}
	if request.Limit == 0 {
		request.Limit = dataframeexecution.DefaultPreviewLimit
	}
	if s.config.Capability.ForCompilation == nil || s.config.Capability.Catalog == nil {
		return ConstructionChoiceProposalResponse{}, unavailable("construction-choice-proposal", "CAPABILITY_UNAVAILABLE", "authorized construction choice compilation is not configured", nil)
	}
	if s.config.CompileReceipt == nil {
		return ConstructionChoiceProposalResponse{}, unavailable("construction-choice-proposal", "PREVIEW_UNAVAILABLE", "candidate compilation is not configured", nil)
	}

	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	if err != nil {
		return ConstructionChoiceProposalResponse{}, conflict("construction-choice-proposal", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, err)
	}
	snapshot := authorized.Snapshot.Clone()
	if snapshot.ValidateToken(request.SnapshotToken) != nil || projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(request.Project) {
		return ConstructionChoiceProposalResponse{}, conflict("construction-choice-proposal", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, nil)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return ConstructionChoiceProposalResponse{}, conflict("construction-choice-proposal", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	authorized.Snapshot = snapshot.Clone()

	identities := make([]capability.ConstructionChoiceIdentity, len(request.ConstructionChoices))
	commands := make([]authoringv2.Command, len(request.ConstructionChoices))
	for index, selection := range request.ConstructionChoices {
		identity, decodeErr := capability.DecodeConstructionChoiceID(selection.ChoiceID)
		if decodeErr != nil {
			return ConstructionChoiceProposalResponse{}, malformed("construction-choice-proposal", fmt.Sprintf("constructionChoices[%d].choiceId is invalid", index), decodeErr)
		}
		if identity.SnapshotToken != request.SnapshotToken {
			return ConstructionChoiceProposalResponse{}, conflict("construction-choice-proposal", "STALE_CONSTRUCTION_CHOICE", "a construction choice belongs to a different catalog snapshot", nil, nil)
		}
		identities[index] = identity
		title := ""
		if selection.Title != nil {
			title = *selection.Title
		}
		commands[index] = authoringv2.Command{
			Type: authoringv2.CommandApplyConstructionChoice, OutputID: request.OutputID, Title: title,
			ConstructionChoice: &authoringv2.ConstructionChoiceSelection{ChoiceID: selection.ChoiceID, Form: selection.Form, FrameID: selection.FrameID, RowValuePolicy: selection.RowValuePolicy},
		}
	}
	applyRequest := authoringv2.ApplyCommandsRequest{
		CommandID: request.CommandID, SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		SnapshotToken: request.SnapshotToken, ExpectedDraftVersion: request.ExpectedDraftVersion,
		ExpectedDraftDigest: request.ExpectedDraftDigest, Commands: commands,
	}
	if err := applyRequest.Validate(); err != nil {
		return ConstructionChoiceProposalResponse{}, malformed("construction-choice-proposal", err.Error(), err)
	}

	owner, err := s.store.Get(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return ConstructionChoiceProposalResponse{}, err
	}
	if owner == nil || owner.ExplorerID != request.ExplorerID || projectid.Canonical(owner.Project) != projectid.Canonical(request.Project) {
		return ConstructionChoiceProposalResponse{}, notFound("construction-choice-proposal", "EXPLORER_NOT_FOUND", "the Explorer was not found", explorer.ErrNotFound)
	}
	if owner.DraftVersion != request.ExpectedDraftVersion || owner.DraftDigest != request.ExpectedDraftDigest {
		return ConstructionChoiceProposalResponse{}, conflict("construction-choice-proposal", "DRAFT_CONFLICT", "the Explorer draft changed; reload before requesting a construction choice preview", nil, explorer.ErrDraftConflict)
	}
	workspace, err := previewWorkspace(ctx, s.store, owner)
	if err != nil {
		return ConstructionChoiceProposalResponse{}, conflict("construction-choice-proposal", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be previewed", nil, err)
	}
	baseDigest, err := workspace.Digest()
	if err != nil || baseDigest != owner.DraftDigest {
		return ConstructionChoiceProposalResponse{}, conflict("construction-choice-proposal", "DRAFT_CONFLICT", "the saved workspace does not match its draft digest", nil, err)
	}
	if constructionDocumentIndex(workspace, request.OutputID) < 0 {
		return ConstructionChoiceProposalResponse{}, unprocessable("construction-choice-proposal", "OUTPUT_NOT_FOUND", "outputId does not identify one saved table", nil)
	}

	catalogSnapshot := s.config.Capability.Catalog(snapshot, request.ExplorerID)
	prepared, err := s.prepareConstructionChoice(ctx, request.Project, request.ExplorerID, authorized.Clone(), identities, workspace, catalogSnapshot, commands)
	if err != nil {
		return ConstructionChoiceProposalResponse{}, err
	}
	candidateWorkspace, results, err := authoringv2.ApplyCommands(workspace, catalogSnapshot, request.CommandID, prepared)
	if err != nil {
		return ConstructionChoiceProposalResponse{}, unprocessable("construction-choice-proposal", "INVALID_CONSTRUCTION_CHOICE", err.Error(), err)
	}
	if len(results) != len(request.ConstructionChoices) {
		return ConstructionChoiceProposalResponse{}, fmt.Errorf("construction choice reducer returned %d results for %d choices", len(results), len(request.ConstructionChoices))
	}
	candidateColumnIDs := make([]string, len(results))
	for index, result := range results {
		if result.Type != authoringv2.CommandResultColumnAdded || result.OutputID != request.OutputID || strings.TrimSpace(result.Column) == "" {
			return ConstructionChoiceProposalResponse{}, unprocessable("construction-choice-proposal", "INVALID_CONSTRUCTION_CHOICE", "the candidate choice did not produce its expected output column", nil)
		}
		candidateColumnIDs[index] = result.Column
	}
	candidateDigest, err := candidateWorkspace.Digest()
	if err != nil {
		return ConstructionChoiceProposalResponse{}, fmt.Errorf("digest construction choice candidate workspace: %w", err)
	}
	candidateReceipt, err := s.compile(ctx, compileRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, Workspace: candidateWorkspace,
		SnapshotToken: request.SnapshotToken, RequestID: "construction-choice-proposal-candidate",
	})
	if err != nil {
		return ConstructionChoiceProposalResponse{}, err
	}
	if err := verifyPreviewReceiptIntent(candidateReceipt, candidateWorkspace); err != nil {
		return ConstructionChoiceProposalResponse{}, conflict("construction-choice-proposal", "INVALID_COMPILATION_RECEIPT", "the candidate receipt does not represent the proposed workspace", nil, err)
	}

	choices := append([]ConstructionChoiceProposalSelection(nil), request.ConstructionChoices...)
	return ConstructionChoiceProposalResponse{
		CommandID: request.CommandID, SnapshotToken: request.SnapshotToken, DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest,
		OutputID: request.OutputID, ConstructionChoices: choices, CandidateWorkspaceDigest: candidateDigest,
		CandidateColumnIDs: candidateColumnIDs, PreviewStatus: "PREVIEW_PENDING", PreviewReceiptID: candidateReceipt.ID,
	}, nil
}
