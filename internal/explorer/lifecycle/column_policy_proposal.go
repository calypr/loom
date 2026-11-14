package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/projectid"
)

type ColumnPolicyProposalRequest struct {
	Project              string `json:"-"`
	ExplorerID           string `json:"-"`
	SnapshotToken        string `json:"snapshotToken"`
	ExpectedDraftVersion int64  `json:"expectedDraftVersion"`
	ExpectedDraftDigest  string `json:"expectedDraftDigest"`
	OutputID             string `json:"outputId"`
	ColumnID             string `json:"columnId"`
	ProjectionMode       string `json:"projectionMode"`
	Limit                int    `json:"limit,omitempty"`
}

type ColumnPolicyProposal SourceRelationshipProposal

func (r *ColumnPolicyProposalRequest) UnmarshalJSON(raw []byte) error {
	var wire struct {
		SnapshotToken        string `json:"snapshotToken"`
		ExpectedDraftVersion int64  `json:"expectedDraftVersion"`
		ExpectedDraftDigest  string `json:"expectedDraftDigest"`
		OutputID             string `json:"outputId"`
		ColumnID             string `json:"columnId"`
		ProjectionMode       string `json:"projectionMode"`
		Limit                *int   `json:"limit,omitempty"`
	}
	if err := decodeClosedSourceIntent(raw, &wire); err != nil {
		return err
	}
	var supplied map[string]json.RawMessage
	if err := json.Unmarshal(raw, &supplied); err != nil {
		return err
	}
	if value, ok := supplied["limit"]; ok && (bytes.Equal(bytes.TrimSpace(value), []byte("null")) || wire.Limit == nil || *wire.Limit < 1) {
		return fmt.Errorf("limit must be positive when provided")
	}
	*r = ColumnPolicyProposalRequest{SnapshotToken: wire.SnapshotToken, ExpectedDraftVersion: wire.ExpectedDraftVersion, ExpectedDraftDigest: wire.ExpectedDraftDigest, OutputID: wire.OutputID, ColumnID: wire.ColumnID, ProjectionMode: wire.ProjectionMode}
	if wire.Limit != nil {
		r.Limit = *wire.Limit
	}
	return nil
}

func (r ColumnPolicyProposalRequest) validate() error {
	for name, value := range map[string]string{"project": r.Project, "explorerId": r.ExplorerID, "snapshotToken": r.SnapshotToken, "expectedDraftDigest": r.ExpectedDraftDigest, "outputId": r.OutputID, "columnId": r.ColumnID} {
		if err := requireExactIdentity(value, name); err != nil {
			return err
		}
	}
	if r.ExpectedDraftVersion < 1 || r.Limit < 0 || r.Limit > dataframeexecution.MaxPreviewLimit {
		return fmt.Errorf("positive draft version and bounded limit are required")
	}
	switch r.ProjectionMode {
	case "VALUE", "INDEXED", "FIRST", "ALL", "DISTINCT":
	default:
		return fmt.Errorf("projectionMode is unsupported")
	}
	return nil
}

func (s *Service) ProposeColumnPolicy(ctx context.Context, request ColumnPolicyProposalRequest) (ColumnPolicyProposal, error) {
	fail := ColumnPolicyProposal{}
	const stage = "column-policy-proposal"
	if err := request.validate(); err != nil {
		return fail, malformed(stage, err.Error(), err)
	}
	if s.config.Capability.ForCompilation == nil || s.config.Capability.Catalog == nil || s.config.CompileReceipt == nil {
		return fail, unavailable(stage, "PROPOSAL_UNAVAILABLE", "column policy compilation is unavailable", nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	if err != nil || authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil || projectid.Canonical(authorized.Snapshot.Identity.Project) != projectid.Canonical(request.Project) || authorized.Snapshot.Identity.Generation == "" {
		return fail, conflict(stage, "STALE_CATALOG_SNAPSHOT", "reload the source catalog", nil, err)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, authorized.Snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return fail, conflict(stage, "STALE_AUTHORIZATION_SCOPE", "source authorization changed", nil, err)
	}
	owner, err := s.getOwner(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return fail, err
	}
	if owner.DraftVersion != request.ExpectedDraftVersion || owner.DraftDigest != request.ExpectedDraftDigest {
		return fail, conflict(stage, "DRAFT_CONFLICT", "saved draft changed; preview again", nil, explorer.ErrDraftConflict)
	}
	workspace, err := authoringv2.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return fail, conflict(stage, "AUTHORING_STATE_MISSING", "saved source cannot be read", nil, err)
	}
	digest, err := workspace.Digest()
	if err != nil || digest != owner.DraftDigest {
		return fail, conflict(stage, "DRAFT_CONFLICT", "saved source digest does not match", nil, err)
	}
	document := proposalDocument(workspace, request.OutputID)
	if document == nil {
		return fail, unprocessable(stage, "OUTPUT_NOT_FOUND", "saved output is unavailable", nil)
	}
	if document.ImportedRecipe != nil {
		return fail, unprocessable(stage, "COLUMN_POLICY_IMPORTED_SOURCE_REQUIRED", "edit this policy through its exact imported source step; the recipe pin cannot be rewritten", nil)
	}
	baseDigest, err := documentDigest(*document)
	if err != nil {
		return fail, err
	}
	command := authoringv2.Command{Type: authoringv2.CommandApplyColumnPolicyProposal, OutputID: request.OutputID, ProposalID: "candidate-preview"}
	if err := command.ResolveColumnPolicyProposal(request.ColumnID, request.ProjectionMode); err != nil {
		return fail, malformed(stage, err.Error(), err)
	}
	catalog := s.catalog(authorized.Snapshot, request.ExplorerID)
	candidate, _, err := authoringv2.ApplyCommands(workspace, catalog, "column-policy-preview", []authoringv2.Command{command})
	if err != nil {
		return fail, unprocessable(stage, "INVALID_COLUMN_POLICY", err.Error(), err)
	}
	candidate, err = authoringv2.PrepareWorkspaceForCompilation(candidate, catalog)
	if err != nil {
		return fail, unprocessable(stage, "INVALID_COLUMN_POLICY", err.Error(), err)
	}
	candidateDigest, err := candidate.Digest()
	if err != nil {
		return fail, err
	}
	binding := &explorer.ColumnPolicyProposalBinding{RowDefinitionProposalBinding: explorer.RowDefinitionProposalBinding{DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest, OutputID: request.OutputID, BaseDocumentDigest: baseDigest, CandidateWorkspaceDigest: candidateDigest, SnapshotToken: request.SnapshotToken}, ColumnID: request.ColumnID, ProjectionMode: request.ProjectionMode}
	receipt, err := s.compile(ctx, compileRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: candidate, SnapshotToken: request.SnapshotToken, RequestID: stage, ColumnPolicyProposal: binding})
	if err != nil {
		return fail, err
	}
	verified, err := s.verifyProposalReceipt(ctx, stage, receipt, request.Project, request.ExplorerID, request.SnapshotToken, authorized.Snapshot, &candidate)
	if err != nil {
		return fail, err
	}
	candidateDocument := proposalDocument(verified, request.OutputID)
	if candidateDocument == nil {
		return fail, conflict(stage, "INVALID_COLUMN_POLICY_PROPOSAL", "candidate output missing", nil, nil)
	}
	network, err := authoringv2.NetworkForDocument(*candidateDocument, catalog)
	if err != nil {
		return fail, unprocessable(stage, "INVALID_AUTHORING_NETWORK", err.Error(), err)
	}
	preview, err := s.Preview(ctx, PreviewRequest{Project: request.Project, ExplorerID: request.ExplorerID, ReceiptID: receipt.ID, OutputID: request.OutputID, Limit: request.Limit, SinkFactory: func(*explorer.CompilationReceipt, []explorer.EmittedColumn) (func(map[string]any) error, error) {
		return func(map[string]any) error { return nil }, nil
	}})
	if err != nil {
		return fail, err
	}
	return ColumnPolicyProposal{CandidateDocument: *candidateDocument, CandidateNetwork: network, CandidateRowIdentity: rowIdentityForNetwork(*candidateDocument, network), ProposalID: receipt.ID, CandidateReceiptID: receipt.ID, OutputID: request.OutputID, SnapshotToken: request.SnapshotToken, DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest, BaseDocumentDigest: baseDigest, CandidateWorkspaceDigest: candidateDigest, PreviewStatus: "READY", SampledRows: preview.Summary.RowCount, Sampled: !preview.Summary.Complete || preview.Summary.Truncated || preview.Summary.PartialValidation, AffectedColumns: []string{request.ColumnID}, Notices: []string{"The existing column keeps its identity, source occurrence, and field. Only its value policy changes; output row identity is preserved.", "Existing downstream operations must compile and validate before Apply."}}, nil
}

func (s *Service) prepareColumnPolicyProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, authorized AuthorizedCapability, current authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	const stage = "commands"
	if len(commands) != 1 {
		return nil, malformed(stage, "column policy Apply requires one command", nil)
	}
	command := &commands[0]
	receipt, err := s.lookupReceipt(ctx, project, explorerID, command.ProposalID)
	if err != nil {
		return nil, err
	}
	if receipt == nil || receipt.ID != command.ProposalID {
		return nil, conflict(stage, "INVALID_COLUMN_POLICY_PROPOSAL", "candidate receipt was not found", nil, nil)
	}
	binding := receipt.ColumnPolicyProposal
	if binding == nil || binding.DraftVersion != request.ExpectedDraftVersion || binding.DraftDigest != request.ExpectedDraftDigest || binding.OutputID != command.OutputID || binding.SnapshotToken != request.SnapshotToken {
		return nil, conflict(stage, "STALE_COLUMN_POLICY_PROPOSAL", "preview belongs to another draft, output or snapshot", nil, nil)
	}
	currentDigest, err := current.Digest()
	if err != nil || currentDigest != binding.DraftDigest {
		return nil, conflict(stage, "STALE_COLUMN_POLICY_PROPOSAL", "saved draft changed after Preview", nil, err)
	}
	base := proposalDocument(current, command.OutputID)
	if base == nil || base.ImportedRecipe != nil {
		return nil, conflict(stage, "INVALID_COLUMN_POLICY_PROPOSAL", "ordinary source output is unavailable", nil, nil)
	}
	baseDigest, err := documentDigest(*base)
	if err != nil || baseDigest != binding.BaseDocumentDigest {
		return nil, conflict(stage, "STALE_COLUMN_POLICY_PROPOSAL", "saved source changed after Preview", nil, err)
	}
	candidate, err := s.verifyProposalReceipt(ctx, stage, receipt, project, explorerID, request.SnapshotToken, authorized.Snapshot, nil)
	if err != nil {
		return nil, err
	}
	if err := command.ResolveColumnPolicyProposal(binding.ColumnID, binding.ProjectionMode); err != nil {
		return nil, err
	}
	applied, _, err := authoringv2.ApplyCommands(current, s.catalog(authorized.Snapshot, explorerID), "column-policy-apply-validation", commands)
	if err != nil {
		return nil, unprocessable(stage, "INVALID_COLUMN_POLICY", err.Error(), err)
	}
	applied, err = authoringv2.PrepareWorkspaceForCompilation(applied, s.catalog(authorized.Snapshot, explorerID))
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(applied, candidate) {
		return nil, conflict(stage, "INVALID_COLUMN_POLICY_PROPOSAL", "candidate changes facts outside the existing column policy", nil, nil)
	}
	if _, err := s.Preview(ctx, PreviewRequest{Project: project, ExplorerID: explorerID, ReceiptID: receipt.ID, OutputID: command.OutputID, SinkFactory: func(*explorer.CompilationReceipt, []explorer.EmittedColumn) (func(map[string]any) error, error) {
		return func(map[string]any) error { return nil }, nil
	}}); err != nil {
		return nil, err
	}
	fresh, err := s.compile(ctx, compileRequest{Project: project, ExplorerID: explorerID, Workspace: applied, SnapshotToken: request.SnapshotToken, RequestID: "column-policy-apply"})
	if err != nil {
		return nil, err
	}
	if fresh.IntentDigest != receipt.IntentDigest || fresh.ResolvedRecipeDigest != receipt.ResolvedRecipeDigest || fresh.ResolvedSchemaDigest != receipt.ResolvedSchemaDigest {
		return nil, conflict(stage, "STALE_COLUMN_POLICY_PROPOSAL", "resolved source changed after Preview; preview again", nil, nil)
	}
	return commands, nil
}
