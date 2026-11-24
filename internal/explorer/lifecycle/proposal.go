package lifecycle

import (
	"context"
	"time"

	"github.com/calypr/loom/internal/explorer/authoringcore"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/population"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/proposal"
)

func (s *Service) proposalConfig() proposal.Config {
	return proposal.Config{
		Core:                     s.core,
		Capability:               s.config.Capability,
		AuthoringNetwork:         s.config.AuthoringNetwork,
		EditImportedRelationship: s.config.EditImportedRelationship,
		CreateImportedSourceRule: s.config.CreateImportedSourceRule,
		EditImportedSource:       s.config.EditImportedSource,
		EditImportedSources:      s.config.EditImportedSources,
	}
}

func (s *Service) prepareImportedRelationshipProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, authorized ports.AuthorizedCapability, current ws.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	return s.proposalService.PrepareImportedRelationshipProposal(ctx, project, explorerID, request, authorized, current, commands)
}

func (s *Service) prepareColumnPolicyProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, authorized ports.AuthorizedCapability, current ws.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	return s.proposalService.PrepareColumnPolicyProposal(ctx, project, explorerID, request, authorized, current, commands)
}

func (s *Service) ProposeConstructionAndPreview(ctx context.Context, request construction.ConstructionProposalRequest, options proposal.ProposalPreviewOptions, prepare func(construction.ConstructionProposalResponse) error) (proposal.ProposalPreview, error) {
	draft, err := s.constructionService.ProposeConstruction(ctx, request)
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
	draft, err := s.constructionService.ProposeConstructionChoice(ctx, request)
	if err != nil {
		return proposal.ProposalPreview{}, err
	}
	if err := prepare(draft); err != nil {
		return proposal.ProposalPreview{}, err
	}
	return s.previewProposal(ctx, request.Project, request.ExplorerID, draft.PreviewReceiptID, request.OutputID, options)
}

func (s *Service) ProposePopulationMemberRemovalAndPreview(ctx context.Context, request population.PopulationMemberRemovalProposalRequest, options proposal.ProposalPreviewOptions, prepare func(population.PopulationMemberRemovalProposalResponse) error) (proposal.ProposalPreview, error) {
	draft, err := s.populationService.ProposePopulationMemberRemoval(ctx, request)
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
	preview, err := s.core.Preview(previewCtx, authoringcore.PreviewRequest{
		Project: project, ExplorerID: explorerID, ReceiptID: receiptID, OutputID: outputID,
		Limit: limit, AllowSampledAggregatePreview: options.AllowSampledAggregate, SinkFactory: options.Sink,
	})
	if err != nil {
		return proposal.ProposalPreview{}, &proposal.ProposalPreviewExecutionError{Cause: err}
	}
	return proposal.ProposalPreview{Result: preview, Started: started, Completed: true}, nil
}
