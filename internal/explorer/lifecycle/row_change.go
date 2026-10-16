package lifecycle

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

// AssessRowChange resolves capability evidence against one exact persisted
// draft and compiles a ready candidate without mutating the draft. Applying the
// candidate remains on the existing CAS command path.
func (s *Service) AssessRowChange(ctx context.Context, request AssessRowChangeRequest) (AssessRowChangeResult, error) {
	if strings.TrimSpace(request.SnapshotToken) == "" || request.DraftVersion < 1 || strings.TrimSpace(request.DraftDigest) == "" || strings.TrimSpace(request.OutputID) == "" || strings.TrimSpace(request.RootNodeID) == "" {
		return AssessRowChangeResult{}, malformed("row-change", "snapshotToken, draftVersion, draftDigest, outputId, and rootNodeId are required", nil)
	}
	if s.config.Capability.Token == nil || s.config.Capability.Catalog == nil {
		return AssessRowChangeResult{}, unavailable("row-change", "CAPABILITY_UNAVAILABLE", "Explorer capability lookup is not configured", nil)
	}
	owner, err := s.store.Get(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return AssessRowChangeResult{}, err
	}
	if owner.DraftVersion != request.DraftVersion || owner.DraftDigest != request.DraftDigest {
		return AssessRowChangeResult{}, conflict("row-change", "DRAFT_CONFLICT", "the Explorer draft changed; reassess the row definition", nil, explorer.ErrDraftConflict)
	}
	workspace, err := authoringv2.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return AssessRowChangeResult{}, conflict("row-change", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be assessed", nil, err)
	}
	snapshot, err := s.config.Capability.Token(ctx, request.Project, request.SnapshotToken)
	if err != nil || snapshot.ValidateToken(request.SnapshotToken) != nil {
		return AssessRowChangeResult{}, conflict("row-change", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, err)
	}
	catalog := s.config.Capability.Catalog(snapshot, request.ExplorerID)
	assessment, err := authoringv2.AssessRowChange(workspace, catalog, authoringv2.RowChangeRequest{
		Mode: request.Mode, OutputID: request.OutputID, RootNodeID: request.RootNodeID, RootOccurrenceID: request.RootOccurrenceID, RouteRebase: request.RouteRebase,
	})
	if err != nil {
		return AssessRowChangeResult{}, unprocessable("row-change", "INVALID_ROW_CHANGE", err.Error(), err)
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
		return AssessRowChangeResult{}, unavailable("row-change", "PREVIEW_UNAVAILABLE", "candidate receipt preview is not configured", nil)
	}
	workspaceDigest, err := workspace.Digest()
	if err != nil || workspaceDigest != owner.DraftDigest {
		return AssessRowChangeResult{}, conflict("row-change", "DRAFT_CONFLICT", "the saved workspace does not match its draft digest", nil, err)
	}
	candidateWorkspace, _, err := authoringv2.ApplyCommands(workspace, catalog, "row-change-preview", []authoringv2.Command{{
		Type: authoringv2.CommandApplyTableRootRebase, OutputID: request.OutputID, RowChange: assessment.Proposal,
	}})
	if err != nil {
		return AssessRowChangeResult{}, unprocessable("row-change", "INVALID_ROW_CHANGE", err.Error(), err)
	}
	previewWorkspace := candidateWorkspace
	if assessment.Proposal.Mode == authoringv2.RowChangeModeReplace {
		previewWorkspace, err = rowChangeReceiptWorkspace(candidateWorkspace, request.OutputID)
		if err != nil {
			return AssessRowChangeResult{}, unprocessable("row-change", "INVALID_ROW_CHANGE", err.Error(), err)
		}
	} else {
		previewWorkspace, err = constructionCapabilitiesWorkspace(candidateWorkspace, request.OutputID)
		if err != nil {
			return AssessRowChangeResult{}, unprocessable("row-change", "INVALID_ROW_CHANGE", err.Error(), err)
		}
	}
	candidateReceipt, err := s.compile(ctx, compileRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, Workspace: previewWorkspace,
		SnapshotToken: request.SnapshotToken, RequestID: "row-change-preview-candidate",
	})
	if err != nil {
		return AssessRowChangeResult{}, err
	}
	if err := verifyPreviewReceiptIntent(candidateReceipt, previewWorkspace); err != nil {
		return AssessRowChangeResult{}, conflict("row-change", "INVALID_COMPILATION_RECEIPT", "the candidate receipt does not represent the row-change candidate", nil, err)
	}
	if !receiptHasOutput(candidateReceipt.Bundle, request.OutputID) || validateReceiptOutputContract(candidateReceipt, request.OutputID) != nil {
		return AssessRowChangeResult{}, conflict("row-change", "INVALID_COMPILATION_RECEIPT", "the row-change candidate receipt does not contain the requested output", nil, nil)
	}
	result.CandidateReceiptID = candidateReceipt.ID
	return result, nil
}

// rowChangeReceiptWorkspace keeps the receipt scoped to the output being
// replaced. Current-draft WORKSPACE_OUTPUT consumers remain unchanged in the
// persisted candidate and may need separate repair after the source schema
// changes.
func rowChangeReceiptWorkspace(workspace authoringv2.Workspace, outputID string) (authoringv2.Workspace, error) {
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		return authoringv2.Workspace{}, err
	}
	projected, err := authoringv2.DecodeWorkspace(raw)
	if err != nil {
		return authoringv2.Workspace{}, err
	}
	documentIndex := -1
	for index := range projected.Documents {
		if projected.Documents[index].Output.ID != outputID {
			continue
		}
		if documentIndex >= 0 {
			return authoringv2.Workspace{}, fmt.Errorf("replacement output %q is duplicated in the candidate workspace", outputID)
		}
		documentIndex = index
	}
	if documentIndex < 0 {
		return authoringv2.Workspace{}, fmt.Errorf("replacement output %q was not found in the candidate workspace", outputID)
	}
	tabs := make([]authoringv2.Tab, 0, 1)
	for _, tab := range projected.Tabs {
		if tab.OutputID == outputID {
			tabs = append(tabs, tab)
		}
	}
	if len(tabs) != 1 {
		return authoringv2.Workspace{}, fmt.Errorf("replacement output %q must have exactly one matching tab", outputID)
	}
	tabs[0].Order = 0
	document := projected.Documents[documentIndex]
	projected.Documents = []authoringv2.Document{document}
	projected.Tabs = tabs
	projected.SharedFilters = nil
	projected.FileActions = nil
	return projected, nil
}
