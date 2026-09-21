package lifecycle

import (
	"context"
	"fmt"
	"reflect"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

type TableShapeProposalRequest struct {
	Project              string
	ExplorerID           string
	SnapshotToken        string
	ExpectedDraftVersion int64
	ExpectedDraftDigest  string
	OutputID             string
	TableShape           *authoringv2.TableShape
	Limit                int
}

func (r TableShapeProposalRequest) Validate() error {
	for name, value := range map[string]string{
		"project": r.Project, "explorerId": r.ExplorerID, "snapshotToken": r.SnapshotToken,
		"expectedDraftDigest": r.ExpectedDraftDigest, "outputId": r.OutputID,
	} {
		if err := requireExactIdentity(value, name); err != nil {
			return err
		}
	}
	if r.ExpectedDraftVersion < 1 {
		return fmt.Errorf("expectedDraftVersion must be positive")
	}
	if r.Limit < 0 || r.Limit > dataframeexecution.MaxPreviewLimit {
		return fmt.Errorf("limit is outside the supported range")
	}
	return nil
}

type TableShapeProposalMode string

const (
	TableShapeProposalAdd     TableShapeProposalMode = "ADD"
	TableShapeProposalReplace TableShapeProposalMode = "REPLACE"
	TableShapeProposalRemove  TableShapeProposalMode = "REMOVE"
)

type TableShapeComparisonStatus string

const (
	TableShapeComparisonAvailable   TableShapeComparisonStatus = "AVAILABLE"
	TableShapeComparisonUnavailable TableShapeComparisonStatus = "UNAVAILABLE"
)

type TableShapePreviewSummary struct {
	RowCount int  `json:"rowCount"`
	Sampled  bool `json:"sampled"`
}

type TableShapeChangedRow struct {
	RowIdentity      string   `json:"rowIdentity"`
	BasePresent      bool     `json:"basePresent"`
	CandidatePresent bool     `json:"candidatePresent"`
	ChangedColumns   []string `json:"changedColumns"`
}

type TableShapeComparison struct {
	Status          TableShapeComparisonStatus `json:"status"`
	ReasonCode      string                     `json:"reasonCode,omitempty"`
	Reason          string                     `json:"reason,omitempty"`
	Base            *TableShapePreviewSummary  `json:"base,omitempty"`
	Candidate       *TableShapePreviewSummary  `json:"candidate,omitempty"`
	ChangedColumns  []string                   `json:"changedColumns"`
	ChangedRowCount int                        `json:"changedRowCount"`
	ChangedRows     []TableShapeChangedRow     `json:"changedRows"`
	Notices         []string                   `json:"notices"`
}

type TableShapeProposal struct {
	ProposalID               string                 `json:"proposalId,omitempty"`
	BaseReceiptID            string                 `json:"baseReceiptId"`
	OutputID                 string                 `json:"outputId"`
	SnapshotToken            string                 `json:"snapshotToken"`
	DraftVersion             int64                  `json:"draftVersion"`
	DraftDigest              string                 `json:"draftDigest"`
	BaseDocumentDigest       string                 `json:"baseDocumentDigest"`
	CandidateWorkspaceDigest string                 `json:"candidateWorkspaceDigest"`
	Mode                     TableShapeProposalMode `json:"mode"`
	Comparison               TableShapeComparison   `json:"comparison"`
}

func (s *Service) ProposeTableShape(ctx context.Context, request TableShapeProposalRequest) (TableShapeProposal, error) {
	if err := request.Validate(); err != nil {
		return TableShapeProposal{}, malformed("table-shape-proposal", err.Error(), err)
	}
	if s.config.Capability.ForCompilation == nil || s.config.Capability.Catalog == nil || s.config.CompileReceipt == nil {
		return TableShapeProposal{}, unavailable("table-shape-proposal", "PROPOSAL_UNAVAILABLE", "table shape proposal compilation is not configured", nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	snapshot := authorized.Snapshot
	if err != nil || snapshot.ValidateToken(request.SnapshotToken) != nil || projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(request.Project) {
		return TableShapeProposal{}, conflict("table-shape-proposal", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, err)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return TableShapeProposal{}, conflict("table-shape-proposal", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	owner, err := s.store.Get(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return TableShapeProposal{}, err
	}
	if owner.DraftVersion != request.ExpectedDraftVersion || owner.DraftDigest != request.ExpectedDraftDigest {
		return TableShapeProposal{}, conflict("table-shape-proposal", "DRAFT_CONFLICT", "the Explorer draft changed; reload before proposing a table shape", nil, explorer.ErrDraftConflict)
	}
	workspace, err := previewWorkspace(ctx, s.store, owner)
	if err != nil {
		return TableShapeProposal{}, conflict("table-shape-proposal", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be proposed", nil, err)
	}
	baseDigest, err := workspace.Digest()
	if err != nil || baseDigest != owner.DraftDigest {
		return TableShapeProposal{}, conflict("table-shape-proposal", "DRAFT_CONFLICT", "the saved workspace does not match its draft digest", nil, err)
	}
	baseDocument := proposalDocument(workspace, request.OutputID)
	if baseDocument == nil {
		return TableShapeProposal{}, unprocessable("table-shape-proposal", "OUTPUT_NOT_FOUND", "outputId does not identify a saved table", nil)
	}
	mode, err := tableShapeProposalMode(baseDocument.TableShape, request.TableShape)
	if err != nil {
		return TableShapeProposal{}, unprocessable("table-shape-proposal", "INVALID_TABLE_SHAPE", err.Error(), err)
	}
	baseDocumentDigest, err := documentDigest(*baseDocument)
	if err != nil {
		return TableShapeProposal{}, err
	}
	command := authoringv2.Command{Type: authoringv2.CommandApplyTableShapeProposal, OutputID: request.OutputID, ProposalID: "proposal-preview"}
	if err := command.ResolveTableShapeProposal(request.TableShape); err != nil {
		return TableShapeProposal{}, unprocessable("table-shape-proposal", "INVALID_TABLE_SHAPE", err.Error(), err)
	}
	candidateWorkspace, _, err := authoringv2.ApplyCommands(workspace, s.config.Capability.Catalog(snapshot, request.ExplorerID), "table-shape-proposal-preview", []authoringv2.Command{command})
	if err != nil {
		return TableShapeProposal{}, unprocessable("table-shape-proposal", "INVALID_TABLE_SHAPE", err.Error(), err)
	}
	if _, err := tableShapeWorkspaceChange(workspace, candidateWorkspace, request.OutputID); err != nil {
		return TableShapeProposal{}, unprocessable("table-shape-proposal", "INVALID_TABLE_SHAPE", err.Error(), err)
	}
	candidateDigest, err := candidateWorkspace.Digest()
	if err != nil {
		return TableShapeProposal{}, err
	}
	limit := request.Limit
	if limit == 0 {
		limit = dataframeexecution.DefaultPreviewLimit
	}
	baseReceipt, err := s.compile(ctx, compileRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: workspace, SnapshotToken: request.SnapshotToken, RequestID: "table-shape-proposal-base"})
	if err != nil {
		return TableShapeProposal{}, err
	}
	if _, err := s.verifyProposalReceipt(ctx, "table-shape-proposal", baseReceipt, request.Project, request.ExplorerID, request.SnapshotToken, snapshot, &workspace); err != nil {
		return TableShapeProposal{}, err
	}
	binding := &explorer.TableShapeProposalBinding{
		DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest, OutputID: request.OutputID,
		BaseDocumentDigest: baseDocumentDigest, CandidateWorkspaceDigest: candidateDigest,
		SnapshotToken: request.SnapshotToken,
	}
	candidateReceipt, err := s.compile(ctx, compileRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, Workspace: candidateWorkspace,
		SnapshotToken: request.SnapshotToken, RequestID: "table-shape-proposal-candidate",
		TableShapeProposal: binding,
	})
	if err != nil {
		return TableShapeProposal{}, err
	}
	if _, err := s.verifyProposalReceipt(ctx, "table-shape-proposal", candidateReceipt, request.Project, request.ExplorerID, request.SnapshotToken, snapshot, &candidateWorkspace); err != nil {
		return TableShapeProposal{}, err
	}
	comparison, err := s.compareTableShapeReceipts(ctx, request, snapshot, baseReceipt, candidateReceipt, limit)
	if err != nil {
		return TableShapeProposal{}, err
	}
	return TableShapeProposal{
		ProposalID: candidateReceipt.ID, BaseReceiptID: baseReceipt.ID, OutputID: request.OutputID,
		SnapshotToken: request.SnapshotToken, DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest,
		BaseDocumentDigest: baseDocumentDigest, CandidateWorkspaceDigest: candidateDigest,
		Mode: mode, Comparison: comparison,
	}, nil
}

func tableShapeProposalMode(base, candidate *authoringv2.TableShape) (TableShapeProposalMode, error) {
	if reflect.DeepEqual(base, candidate) {
		return "", fmt.Errorf("candidate table shape does not change the saved table")
	}
	if candidate == nil {
		return TableShapeProposalRemove, nil
	}
	if base == nil {
		return TableShapeProposalAdd, nil
	}
	return TableShapeProposalReplace, nil
}

func (s *Service) prepareTableShapeProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, snapshot capability.Snapshot, current authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, *explorer.CompilationReceipt, error) {
	if len(commands) != 1 || commands[0].Type != authoringv2.CommandApplyTableShapeProposal {
		return nil, nil, malformed("commands", "APPLY_TABLE_SHAPE_PROPOSAL must be the only command in its atomic request", nil)
	}
	command := &commands[0]
	receipt, err := s.lookupReceipt(ctx, project, explorerID, command.ProposalID)
	if err != nil {
		return nil, nil, err
	}
	if receipt == nil || receipt.ID != command.ProposalID {
		return nil, nil, conflict("commands", "INVALID_TABLE_SHAPE_PROPOSAL", "the proposal ID does not match a candidate receipt", nil, nil)
	}
	binding := receipt.TableShapeProposal
	if binding == nil || binding.DraftVersion != request.ExpectedDraftVersion || binding.DraftDigest != request.ExpectedDraftDigest ||
		binding.OutputID != command.OutputID || binding.SnapshotToken != request.SnapshotToken {
		return nil, nil, conflict("commands", "STALE_TABLE_SHAPE_PROPOSAL", "the proposal is not bound to this exact draft, output, and snapshot", nil, nil)
	}
	currentDigest, err := current.Digest()
	if err != nil {
		return nil, nil, err
	}
	if currentDigest != binding.DraftDigest {
		return nil, nil, conflict("commands", "STALE_TABLE_SHAPE_PROPOSAL", "the saved draft no longer matches the proposal base", nil, nil)
	}
	baseDocument := proposalDocument(current, command.OutputID)
	if baseDocument == nil {
		return nil, nil, conflict("commands", "STALE_TABLE_SHAPE_PROPOSAL", "the proposal output is missing from the saved draft", nil, nil)
	}
	baseDocumentDigest, err := documentDigest(*baseDocument)
	if err != nil {
		return nil, nil, err
	}
	if baseDocumentDigest != binding.BaseDocumentDigest {
		return nil, nil, conflict("commands", "STALE_TABLE_SHAPE_PROPOSAL", "the target table changed after the proposal was compiled", nil, nil)
	}
	candidate, err := s.verifyProposalReceipt(ctx, "table-shape-proposal", receipt, project, explorerID, request.SnapshotToken, snapshot, nil)
	if err != nil {
		return nil, nil, err
	}
	candidateDocument := proposalDocument(candidate, command.OutputID)
	if candidateDocument == nil {
		return nil, nil, conflict("commands", "INVALID_TABLE_SHAPE_PROPOSAL", "the candidate receipt does not contain the requested output", nil, nil)
	}
	if _, err := tableShapeWorkspaceChange(current, candidate, command.OutputID); err != nil {
		return nil, nil, conflict("commands", "STALE_TABLE_SHAPE_PROPOSAL", "the candidate receipt no longer changes only the requested table shape", nil, err)
	}
	if err := command.ResolveTableShapeProposal(candidateDocument.TableShape); err != nil {
		return nil, nil, conflict("commands", "INVALID_TABLE_SHAPE_PROPOSAL", "the candidate table shape is invalid", nil, err)
	}
	return commands, receipt, nil
}

func checkTableShapeProposalResult(workspace authoringv2.Workspace, receipt *explorer.CompilationReceipt) error {
	if receipt == nil {
		return conflict("commands", "INVALID_TABLE_SHAPE_PROPOSAL", "the candidate receipt was not prepared", nil, nil)
	}
	digest, err := workspace.Digest()
	if err != nil {
		return fmt.Errorf("digest applied table-shape workspace: %w", err)
	}
	if digest != receipt.IntentDigest {
		return conflict("commands", "TABLE_SHAPE_PROPOSAL_MISMATCH", "the applied table shape does not match the candidate receipt workspace", nil, nil)
	}
	return nil
}

func tableShapeWorkspaceChange(base, candidate authoringv2.Workspace, outputID string) (authoringv2.Document, error) {
	baseWithoutShape := base
	candidateWithoutShape := candidate
	baseWithoutShape.Documents = append([]authoringv2.Document(nil), base.Documents...)
	candidateWithoutShape.Documents = append([]authoringv2.Document(nil), candidate.Documents...)
	baseDocument, candidateDocument := -1, -1
	for index := range base.Documents {
		if base.Documents[index].Output.ID == outputID {
			if baseDocument >= 0 {
				return authoringv2.Document{}, fmt.Errorf("output %q is duplicated in the base workspace", outputID)
			}
			baseDocument = index
		}
	}
	for index := range candidate.Documents {
		if candidate.Documents[index].Output.ID == outputID {
			if candidateDocument >= 0 {
				return authoringv2.Document{}, fmt.Errorf("output %q is duplicated in the candidate workspace", outputID)
			}
			candidateDocument = index
		}
	}
	if baseDocument < 0 || candidateDocument < 0 || baseDocument != candidateDocument {
		return authoringv2.Document{}, fmt.Errorf("output %q is missing or moved in the candidate workspace", outputID)
	}
	if reflect.DeepEqual(base.Documents[baseDocument].TableShape, candidate.Documents[candidateDocument].TableShape) {
		return authoringv2.Document{}, fmt.Errorf("candidate table shape does not change output %q", outputID)
	}
	baseWithoutShape.Documents[baseDocument].TableShape = nil
	candidateWithoutShape.Documents[candidateDocument].TableShape = nil
	if !reflect.DeepEqual(baseWithoutShape, candidateWithoutShape) {
		return authoringv2.Document{}, fmt.Errorf("candidate changes authoring fields outside Document.TableShape")
	}
	return candidate.Documents[candidateDocument], nil
}
