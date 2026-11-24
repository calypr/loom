package lifecycle

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer/authoringcore"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/explorer/lifecycle/support"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/interpretation"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/projectid"
)

func previewWorkspace(ctx context.Context, store *explorer.Service, owner *explorer.Explorer) (ws.Workspace, error) {
	if owner == nil {
		return ws.Workspace{}, fmt.Errorf("Explorer owner is required")
	}
	if len(owner.DraftConfig) != 0 {
		return ws.DecodeWorkspace(owner.DraftConfig)
	}
	if owner.ActiveRevisionID != "" {
		revision, err := store.ActiveRevision(ctx, owner.Project, owner.ExplorerID)
		if err != nil {
			return ws.Workspace{}, err
		}
		return store.RevisionWorkspace(ctx, *revision)
	}
	return ws.NewWorkspace(owner.Title), nil
}

func (s *Service) PreviewInterpretationCandidate(ctx context.Context, request interpretation.PreviewInterpretationCandidateRequest) (interpretation.PreviewInterpretationCandidateResult, error) {
	if err := interpretation.ValidateCandidatePreviewRequest(request); err != nil {
		return interpretation.PreviewInterpretationCandidateResult{}, support.Malformed("interpretation-preview", err.Error(), err)
	}
	limit := request.Limit
	if limit == 0 {
		limit = dataframeexecution.DefaultPreviewLimit
	}
	if limit < 1 || limit > dataframeexecution.MaxPreviewLimit {
		return interpretation.PreviewInterpretationCandidateResult{}, support.Unprocessable("interpretation-preview", "INVALID_PREVIEW_LIMIT", "limit is outside the supported range", nil)
	}
	if s.config.Capability.Token == nil || s.config.Capability.Catalog == nil {
		return interpretation.PreviewInterpretationCandidateResult{}, support.Unavailable("interpretation-preview", "PREVIEW_UNAVAILABLE", "Explorer capability lookup is not configured", nil)
	}
	if s.config.CompileReceipt == nil || s.config.PreviewReceipt == nil {
		return interpretation.PreviewInterpretationCandidateResult{}, support.Unavailable("interpretation-preview", "PREVIEW_UNAVAILABLE", "candidate receipt preview is not configured", nil)
	}
	if s.config.Capability.ForExecution == nil {
		return interpretation.PreviewInterpretationCandidateResult{}, support.Unavailable("interpretation-preview", "PREVIEW_UNAVAILABLE", "authorized receipt execution is not configured", nil)
	}

	snapshot, err := s.config.Capability.Token(ctx, request.Project, request.SnapshotToken)
	if err != nil || snapshot.ValidateToken(request.SnapshotToken) != nil || projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(request.Project) {
		return interpretation.PreviewInterpretationCandidateResult{}, support.Conflict("interpretation-preview", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, err)
	}
	owner, err := s.core.GetOwner(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return interpretation.PreviewInterpretationCandidateResult{}, err
	}
	if owner.DraftVersion != request.ExpectedDraftVersion || owner.DraftDigest != request.ExpectedDraftDigest {
		return interpretation.PreviewInterpretationCandidateResult{}, support.Conflict("interpretation-preview", "DRAFT_CONFLICT", "the Explorer draft changed; reload before previewing", nil, explorer.ErrDraftConflict)
	}
	workspace, err := previewWorkspace(ctx, s.store, owner)
	if err != nil {
		return interpretation.PreviewInterpretationCandidateResult{}, support.Conflict("interpretation-preview", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be previewed", nil, err)
	}

	catalog := s.core.Catalog(snapshot, request.ExplorerID)
	candidateWorkspace, _, err := authoringv2.ApplyCommands(workspace, catalog, "interpretation-preview", []authoringv2.Command{{Type: authoringv2.CommandApplyInterpretationCandidate, OutputID: request.OutputID, Payload: &authoringv2.ColumnPayload{Column: request.Column, InterpretationCandidate: &authoringv2.ApplyInterpretationCandidate{CandidateReceiptID: "preview", RevisionID: request.RevisionID}}}})
	if err != nil {
		return interpretation.PreviewInterpretationCandidateResult{}, support.Unprocessable("interpretation-preview", "INVALID_INTERPRETATION_CANDIDATE", err.Error(), err)
	}

	baseReceipt, err := s.core.Compile(ctx, authoringcore.CompileRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: workspace, SnapshotToken: request.SnapshotToken, RequestID: "interpretation-preview-base"})
	if err != nil {
		return interpretation.PreviewInterpretationCandidateResult{}, err
	}
	candidateReceipt, err := s.core.Compile(ctx, authoringcore.CompileRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: candidateWorkspace, SnapshotToken: request.SnapshotToken, RequestID: "interpretation-preview-candidate"})
	if err != nil {
		return interpretation.PreviewInterpretationCandidateResult{}, err
	}
	baseForCompilation, err := ws.PrepareWorkspaceForCompilation(workspace, catalog)
	if err != nil {
		return interpretation.PreviewInterpretationCandidateResult{}, support.Conflict("interpretation-preview", "INVALID_COMPILATION_RECEIPT", "the base workspace cannot be normalized for compilation", nil, err)
	}
	if err := support.VerifyPreviewReceiptIntent(baseReceipt, baseForCompilation); err != nil {
		return interpretation.PreviewInterpretationCandidateResult{}, support.Conflict("interpretation-preview", "INVALID_COMPILATION_RECEIPT", "the base receipt does not represent the current draft", nil, err)
	}
	if err := support.VerifyPreviewReceiptIntent(candidateReceipt, candidateWorkspace); err != nil {
		return interpretation.PreviewInterpretationCandidateResult{}, support.Conflict("interpretation-preview", "INVALID_COMPILATION_RECEIPT", "the candidate receipt does not represent the proposed workspace", nil, err)
	}
	authorized, err := s.config.Capability.ForExecution(ctx, request.Project, request.SnapshotToken)
	if err != nil {
		return interpretation.PreviewInterpretationCandidateResult{}, support.ReceiptCapabilityFailure("interpretation-preview", "the candidate receipt's capability snapshot is no longer authorized or retained", err)
	}
	if authorized.Snapshot.Token != request.SnapshotToken || authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil || projectid.Canonical(authorized.Snapshot.Identity.Project) != projectid.Canonical(request.Project) || authorized.Snapshot.Identity.Generation != snapshot.Identity.Generation || authorized.Snapshot.Identity.ShapeDigest != snapshot.Identity.ShapeDigest {
		return interpretation.PreviewInterpretationCandidateResult{}, support.Conflict("interpretation-preview", "RECEIPT_STALE", "the candidate receipt's capability snapshot is no longer authorized or retained", nil, nil)
	}
	if err := support.ValidateAuthorizedReceiptExecution(baseReceipt, authorized); err != nil {
		return interpretation.PreviewInterpretationCandidateResult{}, support.Conflict("interpretation-preview", "RECEIPT_STALE", "the base receipt's capability snapshot is no longer authorized or retained", nil, err)
	}
	if err := support.ValidateAuthorizedReceiptExecution(candidateReceipt, authorized); err != nil {
		return interpretation.PreviewInterpretationCandidateResult{}, support.Conflict("interpretation-preview", "RECEIPT_STALE", "the candidate receipt's capability snapshot is no longer authorized or retained", nil, err)
	}

	baseEmissions := interpretation.AuthoredEmissions(baseReceipt.EmittedColumns, request.OutputID, request.Column)
	candidateEmissions := interpretation.AuthoredEmissions(candidateReceipt.EmittedColumns, request.OutputID, request.Column)
	if len(baseEmissions) == 0 || len(candidateEmissions) == 0 {
		return interpretation.PreviewInterpretationCandidateResult{}, support.Unprocessable("interpretation-preview", "INTERPRETATION_OUTPUT_MISSING", "the target authored feature has no physical receipt emissions", nil)
	}
	bindings := recipe.RuntimeBindings{
		Project: projectid.Legacy(request.Project), SelectionProject: projectid.Canonical(request.Project),
		DatasetGeneration: snapshot.Identity.Generation, SelectionMembersCollection: s.config.SelectionMembersCollection,
		PreviewLimit: limit, OutputNames: []string{request.OutputID}, IncludeRowIdentity: true,
	}
	support.ApplyAuthorizedScope(&bindings, authorized, false)
	baseRows, baseSummary, err := s.previewInterpretationReceipt(ctx, baseReceipt, bindings)
	if err != nil {
		return interpretation.PreviewInterpretationCandidateResult{}, err
	}
	candidateRows, candidateSummary, err := s.previewInterpretationReceipt(ctx, candidateReceipt, bindings)
	if err != nil {
		return interpretation.PreviewInterpretationCandidateResult{}, err
	}

	result := interpretation.CompareCandidateRows(baseRows, candidateRows, baseEmissions, candidateEmissions, request, limit)
	if !interpretation.PreviewSummaryComplete(baseSummary) || !interpretation.PreviewSummaryComplete(candidateSummary) {
		result.Completeness = interpretation.CandidatePreviewIncomplete
	}
	return result, nil
}

func (s *Service) previewInterpretationReceipt(ctx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings) (interpretation.PreviewRows, dataframeexecution.PreviewSummary, error) {
	rows := interpretation.PreviewRows{ReceiptID: receipt.ID, Rows: make(map[string]map[string]any)}
	summary, err := s.config.PreviewReceipt(ctx, receipt, bindings, func(row map[string]any) error {
		value, ok := row["__loom_row_id"]
		if !ok || value == nil || strings.TrimSpace(fmt.Sprint(value)) == "" {
			return fmt.Errorf("candidate preview row is missing stable __loom_row_id")
		}
		rowID := fmt.Sprint(value)
		if _, exists := rows.Rows[rowID]; exists {
			return fmt.Errorf("candidate preview emitted duplicate stable __loom_row_id %q", rowID)
		}
		copy := make(map[string]any, len(row))
		for key, item := range row {

			copy[key] = item
		}
		rows.Rows[rowID] = copy
		return nil
	})
	if err != nil {
		return interpretation.PreviewRows{}, summary, err
	}
	return rows, summary, nil
}
