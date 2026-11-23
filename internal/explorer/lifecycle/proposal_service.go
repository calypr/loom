package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/proposal"
)

func (s *Service) proposalConfig() proposal.Config {
	return proposal.Config{
		Capability:        func() ports.CapabilityResolver { return s.config.Capability },
		GetOwner:          s.getOwner,
		LookupReceipt:     s.lookupReceipt,
		CompileConfigured: func() bool { return s.config.CompileReceipt != nil },
		Compile: func(ctx context.Context, request proposal.CompileRequest) (*explorer.CompilationReceipt, error) {
			return s.compile(ctx, compileRequest{
				Project: request.Project, ExplorerID: request.ExplorerID, Workspace: request.Workspace,
				SnapshotToken: request.SnapshotToken, RequestID: request.RequestID,
				ColumnPolicyProposal: request.ColumnPolicyProposal, SourceRelationshipProposal: request.SourceRelationshipProposal,
			})
		},
		VerifyProposalReceipt: s.verifyProposalReceipt,
		Preview: func(ctx context.Context, request proposal.PreviewRequest) (proposal.PreviewResult, error) {
			return s.Preview(ctx, PreviewRequest{
				Project: request.Project, ExplorerID: request.ExplorerID, ReceiptID: request.ReceiptID,
				OutputID: request.OutputID, Limit: request.Limit, SinkFactory: request.SinkFactory,
			})
		},
		Catalog: func(snapshot capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
			return s.catalog(snapshot, explorerID)
		},
		AuthoringNetwork: func() func(context.Context, string, ports.AuthorizedCapability, authoringv2.Document) (authoringv2.AuthoringNetwork, error) {
			return s.config.AuthoringNetwork
		},
		EditImportedRelationship: func() func(context.Context, string, ports.AuthorizedCapability, authoringv2.Document, authoringv2.ImportedRelationshipChange) (authoringv2.Document, error) {
			return s.config.EditImportedRelationship
		},
		CreateImportedSourceRule: func() func(context.Context, string, ports.AuthorizedCapability, authoringv2.Document, authoringv2.SourceRuleCreate) (authoringv2.Document, error) {
			return s.config.CreateImportedSourceRule
		},
		EditImportedSource: func() func(context.Context, string, ports.AuthorizedCapability, authoringv2.Document, *authoringv2.ImportedSourceStepChange) (authoringv2.Document, error) {
			return s.config.EditImportedSource
		},
		EditImportedSources: func() func(context.Context, string, ports.AuthorizedCapability, authoringv2.Document, []authoringv2.ImportedSourceStepChange) (authoringv2.Document, error) {
			return s.config.EditImportedSources
		},
	}
}

func (s *Service) ProposeSourceRelationship(ctx context.Context, request SourceRelationshipProposalRequest) (SourceRelationshipProposal, error) {
	return s.proposalService.ProposeSourceRelationship(ctx, request)
}

func (s *Service) ProposeColumnPolicy(ctx context.Context, request ColumnPolicyProposalRequest) (ColumnPolicyProposal, error) {
	return s.proposalService.ProposeColumnPolicy(ctx, request)
}

func (s *Service) prepareImportedRelationshipProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, authorized ports.AuthorizedCapability, current authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	return s.proposalService.PrepareImportedRelationshipProposal(ctx, project, explorerID, request, authorized, current, commands)
}

func (s *Service) prepareColumnPolicyProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, authorized ports.AuthorizedCapability, current authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	return s.proposalService.PrepareColumnPolicyProposal(ctx, project, explorerID, request, authorized, current, commands)
}
