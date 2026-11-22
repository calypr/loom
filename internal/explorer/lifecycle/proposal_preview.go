package lifecycle

import (
	"context"
	"time"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
)

type ProposalPreviewOptions struct {
	Limit                 *int
	Budget                func(context.Context) (context.Context, time.Duration)
	Timeout               time.Duration
	AllowSampledAggregate bool
	Authorize             func(context.Context) error
	Sink                  PreviewSinkFactory
}

type ProposalPreview struct {
	Result    PreviewResult
	Started   time.Time
	Completed bool
}

func (s *Service) ProposeConstructionAndPreview(ctx context.Context, request ConstructionProposalRequest, options ProposalPreviewOptions, prepare func(ConstructionProposalResponse) error) (ProposalPreview, error) {
	proposal, err := s.ProposeConstruction(ctx, request)
	if err != nil {
		return ProposalPreview{}, err
	}
	if err := prepare(proposal); err != nil {
		return ProposalPreview{}, err
	}
	if proposal.PreviewStatus == "NEEDS_REPAIR" {
		return ProposalPreview{}, nil
	}
	return s.previewProposal(ctx, request.Project, request.ExplorerID, proposal.ProposalID, request.OutputID, options)
}

func (s *Service) ProposeConstructionChoiceAndPreview(ctx context.Context, request ConstructionChoiceProposalRequest, options ProposalPreviewOptions, prepare func(ConstructionChoiceProposalResponse) error) (ProposalPreview, error) {
	proposal, err := s.ProposeConstructionChoice(ctx, request)
	if err != nil {
		return ProposalPreview{}, err
	}
	if err := prepare(proposal); err != nil {
		return ProposalPreview{}, err
	}
	return s.previewProposal(ctx, request.Project, request.ExplorerID, proposal.PreviewReceiptID, request.OutputID, options)
}

func (s *Service) ProposePopulationMemberRemovalAndPreview(ctx context.Context, request PopulationMemberRemovalProposalRequest, options ProposalPreviewOptions, prepare func(PopulationMemberRemovalProposalResponse) error) (ProposalPreview, error) {
	proposal, err := s.ProposePopulationMemberRemoval(ctx, request)
	if err != nil {
		return ProposalPreview{}, err
	}
	if err := prepare(proposal); err != nil {
		return ProposalPreview{}, err
	}
	return s.previewProposal(ctx, request.Project, request.ExplorerID, proposal.ProposalID, request.OutputID, options)
}

type ProposalPreviewExecutionError struct{ Cause error }

func (e *ProposalPreviewExecutionError) Error() string { return e.Cause.Error() }
func (e *ProposalPreviewExecutionError) Unwrap() error { return e.Cause }

func (s *Service) previewProposal(ctx context.Context, project, explorerID, receiptID, outputID string, options ProposalPreviewOptions) (ProposalPreview, error) {
	timeout := options.Timeout
	if options.Budget != nil {
		ctx, timeout = options.Budget(ctx)
	}
	started := time.Now()
	if err := options.Authorize(ctx); err != nil {
		return ProposalPreview{}, err
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
		return ProposalPreview{}, &ProposalPreviewExecutionError{Cause: err}
	}
	return ProposalPreview{Result: preview, Started: started, Completed: true}, nil
}
