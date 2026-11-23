package rowdef

import (
	"context"
	"fmt"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringnetwork"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
)

func (s *Service) AssessRowChange(ctx context.Context, request AssessRowChangeRequest) (AssessRowChangeResult, error) {
	if strings.TrimSpace(request.SnapshotToken) == "" || request.DraftVersion < 1 || strings.TrimSpace(request.DraftDigest) == "" || strings.TrimSpace(request.OutputID) == "" || strings.TrimSpace(request.RootNodeID) == "" {
		return AssessRowChangeResult{}, support.Malformed("row-change", "snapshotToken, draftVersion, draftDigest, outputId, and rootNodeId are required", nil)
	}
	if s.config.Capability.Token == nil || s.config.Capability.Catalog == nil {
		return AssessRowChangeResult{}, support.Unavailable("row-change", "CAPABILITY_UNAVAILABLE", "Explorer capability lookup is not configured", nil)
	}
	owner, err := s.config.GetOwner(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return AssessRowChangeResult{}, err
	}
	if owner.DraftVersion != request.DraftVersion || owner.DraftDigest != request.DraftDigest {
		return AssessRowChangeResult{}, support.Conflict("row-change", "DRAFT_CONFLICT", "the Explorer draft changed; reassess the row definition", nil, explorer.ErrDraftConflict)
	}
	workspace, err := ws.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return AssessRowChangeResult{}, support.Conflict("row-change", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be assessed", nil, err)
	}
	snapshot, err := s.config.Capability.Token(ctx, request.Project, request.SnapshotToken)
	if err != nil || snapshot.ValidateToken(request.SnapshotToken) != nil {
		return AssessRowChangeResult{}, support.Conflict("row-change", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, err)
	}
	catalog := s.config.Capability.Catalog(snapshot, request.ExplorerID)
	assessment, err := authoringv2.AssessRowChange(workspace, catalog, authoringv2.RowChangeRequest{
		Mode: request.Mode, OutputID: request.OutputID, RootNodeID: request.RootNodeID, RootOccurrenceID: request.RootOccurrenceID, RouteRebase: request.RouteRebase,
	})
	if err != nil {
		return AssessRowChangeResult{}, support.Unprocessable("row-change", "INVALID_ROW_CHANGE", err.Error(), err)
	}
	result := AssessRowChangeResult{
		SnapshotToken: request.SnapshotToken,
		DraftVersion:  owner.DraftVersion,
		DraftDigest:   owner.DraftDigest,
		Assessment:    assessment,
	}
	if assessment.Status != authoringv2.RowChangeReady || assessment.Proposal == nil {
		return result, nil
	}
	if s.config.PreviewReceipt == nil || s.config.Capability.ForExecution == nil {
		return AssessRowChangeResult{}, support.Unavailable("row-change", "PREVIEW_UNAVAILABLE", "candidate receipt preview is not configured", nil)
	}
	workspaceDigest, err := workspace.Digest()
	if err != nil || workspaceDigest != owner.DraftDigest {
		return AssessRowChangeResult{}, support.Conflict("row-change", "DRAFT_CONFLICT", "the saved workspace does not match its draft digest", nil, err)
	}
	candidateWorkspace, _, err := authoringv2.ApplyCommands(workspace, catalog, "row-change-preview", []authoringv2.Command{{Type: authoringv2.CommandApplyTableRootRebase, OutputID: request.OutputID, Payload: &authoringv2.RowPayload{RowChange: assessment.Proposal}}})
	if err != nil {
		return AssessRowChangeResult{}, support.Unprocessable("row-change", "INVALID_ROW_CHANGE", err.Error(), err)
	}
	previewWorkspace, err := s.config.PrepareCandidateWorkspace(candidateWorkspace, request.OutputID, assessment.Proposal.Mode == authoringv2.RowChangeModeReplace)
	if err != nil {
		return AssessRowChangeResult{}, support.Unprocessable("row-change", "INVALID_ROW_CHANGE", err.Error(), err)
	}
	candidateReceipt, err := s.config.Compile(ctx, CompileRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, Workspace: previewWorkspace,
		SnapshotToken: request.SnapshotToken, RequestID: "row-change-preview-candidate",
	})
	if err != nil {
		return AssessRowChangeResult{}, err
	}
	if err := support.VerifyPreviewReceiptIntent(candidateReceipt, previewWorkspace); err != nil {
		return AssessRowChangeResult{}, support.Conflict("row-change", "INVALID_COMPILATION_RECEIPT", "the candidate receipt does not represent the row-change candidate", nil, err)
	}
	if !support.ReceiptHasOutput(candidateReceipt.Bundle, request.OutputID) || support.ValidateReceiptOutputContract(candidateReceipt, request.OutputID) != nil {
		return AssessRowChangeResult{}, support.Conflict("row-change", "INVALID_COMPILATION_RECEIPT", "the row-change candidate receipt does not contain the requested output", nil, nil)
	}

	verified, err := s.config.VerifyProposalReceipt(ctx, "row-change", candidateReceipt, request.Project, request.ExplorerID, request.SnapshotToken, snapshot, &previewWorkspace)
	if err != nil {
		return AssessRowChangeResult{}, err
	}
	candidateDocument := ProposalDocument(verified, request.OutputID)
	if candidateDocument == nil {
		return AssessRowChangeResult{}, support.Conflict("row-change", "INVALID_COMPILATION_RECEIPT", "candidate output is missing", nil, nil)
	}
	candidateNetwork, err := authoringnetwork.NetworkForDocument(*candidateDocument, catalog)
	if err != nil {
		return AssessRowChangeResult{}, support.Unprocessable("row-change", "INVALID_AUTHORING_NETWORK", err.Error(), err)
	}
	candidateIdentity := support.RowIdentityForNetwork(*candidateDocument, candidateNetwork)
	result.CandidateDocument = candidateDocument
	result.CandidateNetwork = &candidateNetwork
	result.CandidateRowIdentity = &candidateIdentity
	result.CandidateReceiptID = candidateReceipt.ID
	return result, nil
}

func RowChangeReceiptWorkspace(workspace ws.Workspace, outputID string) (ws.Workspace, error) {
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		return ws.Workspace{}, err
	}
	projected, err := ws.DecodeWorkspace(raw)
	if err != nil {
		return ws.Workspace{}, err
	}
	documentIndex := -1
	for index := range projected.Documents {
		if projected.Documents[index].Output.ID != outputID {
			continue
		}
		if documentIndex >= 0 {
			return ws.Workspace{}, fmt.Errorf("replacement output %q is duplicated in the candidate workspace", outputID)
		}
		documentIndex = index
	}
	if documentIndex < 0 {
		return ws.Workspace{}, fmt.Errorf("replacement output %q was not found in the candidate workspace", outputID)
	}
	tabs := make([]ws.Tab, 0, 1)
	for _, tab := range projected.Tabs {
		if tab.OutputID == outputID {
			tabs = append(tabs, tab)
		}
	}
	if len(tabs) != 1 {
		return ws.Workspace{}, fmt.Errorf("replacement output %q must have exactly one matching tab", outputID)
	}
	tabs[0].Order = 0
	document := projected.Documents[documentIndex]
	projected.Documents = []ws.Document{document}
	projected.Tabs = tabs
	projected.SharedFilters = nil
	projected.FileActions = nil
	return projected, nil
}
