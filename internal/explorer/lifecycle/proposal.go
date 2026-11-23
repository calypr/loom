package lifecycle

import (
	"context"
	"time"

	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/population"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
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

func (s *Service) ProposeSourceRelationship(ctx context.Context, request proposal.SourceRelationshipProposalRequest) (proposal.SourceRelationshipProposal, error) {
	return s.proposalService.ProposeSourceRelationship(ctx, request)
}

func (s *Service) ProposeColumnPolicy(ctx context.Context, request proposal.ColumnPolicyProposalRequest) (proposal.ColumnPolicyProposal, error) {
	return s.proposalService.ProposeColumnPolicy(ctx, request)
}

func (s *Service) prepareImportedRelationshipProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, authorized ports.AuthorizedCapability, current authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	return s.proposalService.PrepareImportedRelationshipProposal(ctx, project, explorerID, request, authorized, current, commands)
}

func (s *Service) prepareColumnPolicyProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, authorized ports.AuthorizedCapability, current authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	return s.proposalService.PrepareColumnPolicyProposal(ctx, project, explorerID, request, authorized, current, commands)
}

func (s *Service) ProposeConstructionAndPreview(ctx context.Context, request construction.ConstructionProposalRequest, options proposal.ProposalPreviewOptions, prepare func(construction.ConstructionProposalResponse) error) (proposal.ProposalPreview, error) {
	draft, err := s.ProposeConstruction(ctx, request)
	if err != nil {
		return proposal.ProposalPreview{}, err
	}
	if err := prepare(draft); err != nil {
		return proposal.ProposalPreview{}, err
	}
	if draft.PreviewStatus == "NEEDS_REPAIR" {
		return proposal.ProposalPreview{}, nil
	}
	return s.previewProposal(ctx, request.Project, request.ExplorerID, draft.ProposalID, request.OutputID, options)
}

func (s *Service) ProposeConstructionChoiceAndPreview(ctx context.Context, request construction.ConstructionChoiceProposalRequest, options proposal.ProposalPreviewOptions, prepare func(construction.ConstructionChoiceProposalResponse) error) (proposal.ProposalPreview, error) {
	draft, err := s.ProposeConstructionChoice(ctx, request)
	if err != nil {
		return proposal.ProposalPreview{}, err
	}
	if err := prepare(draft); err != nil {
		return proposal.ProposalPreview{}, err
	}
	return s.previewProposal(ctx, request.Project, request.ExplorerID, draft.PreviewReceiptID, request.OutputID, options)
}

func (s *Service) ProposePopulationMemberRemovalAndPreview(ctx context.Context, request population.PopulationMemberRemovalProposalRequest, options proposal.ProposalPreviewOptions, prepare func(population.PopulationMemberRemovalProposalResponse) error) (proposal.ProposalPreview, error) {
	draft, err := s.ProposePopulationMemberRemoval(ctx, request)
	if err != nil {
		return proposal.ProposalPreview{}, err
	}
	if err := prepare(draft); err != nil {
		return proposal.ProposalPreview{}, err
	}
	return s.previewProposal(ctx, request.Project, request.ExplorerID, draft.ProposalID, request.OutputID, options)
}

func (s *Service) previewProposal(ctx context.Context, project, explorerID, receiptID, outputID string, options proposal.ProposalPreviewOptions) (proposal.ProposalPreview, error) {
	timeout := options.Timeout
	if options.Budget != nil {
		ctx, timeout = options.Budget(ctx)
	}
	started := time.Now()
	if err := options.Authorize(ctx); err != nil {
		return proposal.ProposalPreview{}, err
	}
	limit := dataframeexecution.DefaultPreviewLimit
	if options.Limit != nil {
		limit = *options.Limit
		if limit == 0 {
			limit = -1
		}
	}
	previewCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	preview, err := s.Preview(previewCtx, PreviewRequest{
		Project: project, ExplorerID: explorerID, ReceiptID: receiptID, OutputID: outputID,
		Limit: limit, AllowSampledAggregatePreview: options.AllowSampledAggregate, SinkFactory: options.Sink,
	})
	if err != nil {
		return proposal.ProposalPreview{}, &proposal.ProposalPreviewExecutionError{Cause: err}
	}
	return proposal.ProposalPreview{Result: preview, Started: started, Completed: true}, nil
}
