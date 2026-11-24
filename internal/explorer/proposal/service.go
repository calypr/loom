package proposal

import (
	"context"
	"reflect"

	"github.com/calypr/loom/internal/explorer/authoringcore"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/authoringnetwork"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
)

type Config struct {
	Core       *authoringcore.Core
	Capability ports.CapabilityResolver

	AuthoringNetwork         func(context.Context, string, ports.AuthorizedCapability, ws.Document) (authoringnetwork.AuthoringNetwork, error)
	EditImportedRelationship func(context.Context, string, ports.AuthorizedCapability, ws.Document, authoringv2.ImportedRelationshipChange) (ws.Document, error)
	CreateImportedSourceRule func(context.Context, string, ports.AuthorizedCapability, ws.Document, ws.SourceRuleCreate) (ws.Document, error)
	EditImportedSource       func(context.Context, string, ports.AuthorizedCapability, ws.Document, *authoringv2.ImportedSourceStepChange) (ws.Document, error)
	EditImportedSources      func(context.Context, string, ports.AuthorizedCapability, ws.Document, []authoringv2.ImportedSourceStepChange) (ws.Document, error)
}

type Service struct {
	config Config
}

func NewService(config Config) (*Service, error) {
	return &Service{config: config}, nil
}

func (s *Service) catalog(snapshot capability.Snapshot, explorerID string) ws.CatalogSnapshot {
	return s.config.Core.Catalog(snapshot, explorerID)
}

func (s *Service) PrepareImportedRelationshipProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, authorized ports.AuthorizedCapability, current ws.Workspace, commands []authoringv2.Command) ([]authoringv2.PreparedCommand, error) {
	return s.prepareImportedRelationshipProposal(ctx, project, explorerID, request, authorized, current, commands)
}

func (s *Service) PrepareColumnPolicyProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, authorized ports.AuthorizedCapability, current ws.Workspace, commands []authoringv2.Command) ([]authoringv2.PreparedCommand, error) {
	return s.prepareColumnPolicyProposal(ctx, project, explorerID, request, authorized, current, commands)
}

func (s *Service) ProposeColumnPolicy(ctx context.Context, request ColumnPolicyProposalRequest) (ColumnPolicyProposal, error) {
	fail := ColumnPolicyProposal{}
	const stage = "column-policy-proposal"
	if err := request.validate(); err != nil {
		return fail, support.Malformed(stage, err.Error(), err)
	}
	capabilityResolver := s.config.Capability
	if capabilityResolver.ForCompilation == nil || capabilityResolver.Catalog == nil || !s.config.Core.CompileConfigured() {
		return fail, support.Unavailable(stage, "PROPOSAL_UNAVAILABLE", "column policy compilation is unavailable", nil)
	}
	draft, err := s.prepareDraft(ctx, draftTarget{
		project: request.Project, explorerID: request.ExplorerID, snapshotToken: request.SnapshotToken,
		draftVersion: request.ExpectedDraftVersion, draftDigest: request.ExpectedDraftDigest, outputID: request.OutputID,
	}, draftDiagnostics{
		stage: stage, staleCatalog: "reload the source catalog", staleScope: "source authorization changed",
		changedDraft: "saved draft changed; preview again", unreadableDraft: "saved source cannot be read",
		workspaceDigest: "saved source digest does not match",
	})
	if err != nil {
		return fail, err
	}
	document := draft.document
	if document == nil {
		return fail, support.Unprocessable(stage, "OUTPUT_NOT_FOUND", "saved output is unavailable", nil)
	}
	if document.ImportedRecipe != nil {
		return fail, support.Unprocessable(stage, "COLUMN_POLICY_IMPORTED_SOURCE_REQUIRED", "edit this policy through its exact imported source step; the recipe pin cannot be rewritten", nil)
	}
	baseDigest, err := support.DocumentDigest(*document)
	if err != nil {
		return fail, err
	}
	command := authoringv2.Command{Type: authoringv2.CommandApplyColumnPolicyProposal, OutputID: request.OutputID, Payload: &authoringv2.ProposalPayload{ProposalID: "candidate-preview"}}
	resolution, err := authoringv2.ResolveColumnPolicyProposal(command, request.ColumnID, request.ProjectionMode)
	if err != nil {
		return fail, support.Malformed(stage, err.Error(), err)
	}
	catalog := s.catalog(draft.authorization.Snapshot, request.ExplorerID)
	prepared := []authoringv2.PreparedCommand{{Command: command, Resolution: resolution}}
	candidate, _, err := authoringv2.ApplyCommands(draft.workspace, catalog, "column-policy-preview", prepared)
	if err != nil {
		return fail, support.Unprocessable(stage, "INVALID_COLUMN_POLICY", err.Error(), err)
	}
	candidate, err = ws.PrepareWorkspaceForCompilation(candidate, catalog)
	if err != nil {
		return fail, support.Unprocessable(stage, "INVALID_COLUMN_POLICY", err.Error(), err)
	}
	candidateDigest, err := candidate.Digest()
	if err != nil {
		return fail, err
	}
	binding := &receiptmodel.ColumnPolicyProposalBinding{RowDefinitionProposalBinding: receiptmodel.RowDefinitionProposalBinding{DraftVersion: draft.owner.DraftVersion, DraftDigest: draft.owner.DraftDigest, OutputID: request.OutputID, BaseDocumentDigest: baseDigest, CandidateWorkspaceDigest: candidateDigest, SnapshotToken: request.SnapshotToken}, ColumnID: request.ColumnID, ProjectionMode: request.ProjectionMode}
	receipt, err := s.config.Core.Compile(ctx, authoringcore.CompileRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: candidate, SnapshotToken: request.SnapshotToken, RequestID: stage, ColumnPolicyProposal: binding})
	if err != nil {
		return fail, err
	}
	verified, err := s.config.Core.VerifyProposalReceipt(ctx, stage, receipt, request.Project, request.ExplorerID, request.SnapshotToken, draft.authorization.Snapshot, &candidate)
	if err != nil {
		return fail, err
	}
	candidateDocument := verified.FindOutputDocument(request.OutputID)
	if candidateDocument == nil {
		return fail, support.Conflict(stage, "INVALID_COLUMN_POLICY_PROPOSAL", "candidate output missing", nil, nil)
	}
	network, err := authoringnetwork.NetworkForDocument(*candidateDocument, catalog)
	if err != nil {
		return fail, support.Unprocessable(stage, "INVALID_AUTHORING_NETWORK", err.Error(), err)
	}
	preview, err := s.config.Core.Preview(ctx, authoringcore.PreviewRequest{Project: request.Project, ExplorerID: request.ExplorerID, ReceiptID: receipt.ID, OutputID: request.OutputID, Limit: request.Limit, SinkFactory: func(*receiptmodel.CompilationReceipt, []viewer.EmittedColumn) (func(map[string]any) error, error) {
		return func(map[string]any) error { return nil }, nil
	}})
	if err != nil {
		return fail, err
	}
	return ColumnPolicyProposal{CandidateDocument: *candidateDocument, CandidateNetwork: network, CandidateRowIdentity: support.RowIdentityForNetwork(*candidateDocument, network), ProposalID: receipt.ID, CandidateReceiptID: receipt.ID, OutputID: request.OutputID, SnapshotToken: request.SnapshotToken, DraftVersion: draft.owner.DraftVersion, DraftDigest: draft.owner.DraftDigest, BaseDocumentDigest: baseDigest, CandidateWorkspaceDigest: candidateDigest, PreviewStatus: "READY", SampledRows: preview.Summary.RowCount, Sampled: !preview.Summary.Complete || preview.Summary.Truncated || preview.Summary.PartialValidation, AffectedColumns: []string{request.ColumnID}, Notices: []string{"The existing column keeps its identity, source occurrence, and field. Only its value policy changes; output row identity is preserved.", "Existing downstream operations must compile and validate before Apply."}}, nil
}

func (s *Service) prepareColumnPolicyProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, authorized ports.AuthorizedCapability, current ws.Workspace, commands []authoringv2.Command) ([]authoringv2.PreparedCommand, error) {
	const stage = "commands"
	if len(commands) != 1 {
		return nil, support.Malformed(stage, "column policy Apply requires one command", nil)
	}
	command := commands[0]
	receipt, err := s.config.Core.LookupReceipt(ctx, project, explorerID, command.ProposalID())
	if err != nil {
		return nil, err
	}
	if receipt == nil || receipt.ID != command.ProposalID() {
		return nil, support.Conflict(stage, "INVALID_COLUMN_POLICY_PROPOSAL", "candidate receipt was not found", nil, nil)
	}
	binding := receipt.ColumnPolicyProposal
	if binding == nil || binding.DraftVersion != request.ExpectedDraftVersion || binding.DraftDigest != request.ExpectedDraftDigest || binding.OutputID != command.OutputID || binding.SnapshotToken != request.SnapshotToken {
		return nil, support.Conflict(stage, "STALE_COLUMN_POLICY_PROPOSAL", "preview belongs to another draft, output or snapshot", nil, nil)
	}
	currentDigest, err := current.Digest()
	if err != nil || currentDigest != binding.DraftDigest {
		return nil, support.Conflict(stage, "STALE_COLUMN_POLICY_PROPOSAL", "saved draft changed after Preview", nil, err)
	}
	base := current.FindOutputDocument(command.OutputID)
	if base == nil || base.ImportedRecipe != nil {
		return nil, support.Conflict(stage, "INVALID_COLUMN_POLICY_PROPOSAL", "ordinary source output is unavailable", nil, nil)
	}
	baseDigest, err := support.DocumentDigest(*base)
	if err != nil || baseDigest != binding.BaseDocumentDigest {
		return nil, support.Conflict(stage, "STALE_COLUMN_POLICY_PROPOSAL", "saved source changed after Preview", nil, err)
	}
	candidate, err := s.config.Core.VerifyProposalReceipt(ctx, stage, receipt, project, explorerID, request.SnapshotToken, authorized.Snapshot, nil)
	if err != nil {
		return nil, err
	}
	resolution, err := authoringv2.ResolveColumnPolicyProposal(command, binding.ColumnID, binding.ProjectionMode)
	if err != nil {
		return nil, err
	}
	prepared := []authoringv2.PreparedCommand{{Command: command, Resolution: resolution}}
	applied, _, err := authoringv2.ApplyCommands(current, s.catalog(authorized.Snapshot, explorerID), "column-policy-apply-validation", prepared)
	if err != nil {
		return nil, support.Unprocessable(stage, "INVALID_COLUMN_POLICY", err.Error(), err)
	}
	applied, err = ws.PrepareWorkspaceForCompilation(applied, s.catalog(authorized.Snapshot, explorerID))
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(applied, candidate) {
		return nil, support.Conflict(stage, "INVALID_COLUMN_POLICY_PROPOSAL", "candidate changes facts outside the existing column policy", nil, nil)
	}
	if _, err := s.config.Core.Preview(ctx, authoringcore.PreviewRequest{Project: project, ExplorerID: explorerID, ReceiptID: receipt.ID, OutputID: command.OutputID, SinkFactory: func(*receiptmodel.CompilationReceipt, []viewer.EmittedColumn) (func(map[string]any) error, error) {
		return func(map[string]any) error { return nil }, nil
	}}); err != nil {
		return nil, err
	}
	fresh, err := s.config.Core.Compile(ctx, authoringcore.CompileRequest{Project: project, ExplorerID: explorerID, Workspace: applied, SnapshotToken: request.SnapshotToken, RequestID: "column-policy-apply"})
	if err != nil {
		return nil, err
	}
	if fresh.IntentDigest != receipt.IntentDigest || fresh.ResolvedRecipeDigest != receipt.ResolvedRecipeDigest || fresh.ResolvedSchemaDigest != receipt.ResolvedSchemaDigest {
		return nil, support.Conflict(stage, "STALE_COLUMN_POLICY_PROPOSAL", "resolved source changed after Preview; preview again", nil, nil)
	}
	return prepared, nil
}
