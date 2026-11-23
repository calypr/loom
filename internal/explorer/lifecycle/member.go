package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/population"
)

func checkPopulationMemberRemovalProposalResult(workspace authoringv2.Workspace, receipt *explorer.CompilationReceipt) error {
	return population.CheckPopulationMemberRemovalProposalResult(workspace, receipt)
}

func previewPopulationMemberRemovalProposal(ctx context.Context, service *Service, project, explorerID string, receipt *explorer.CompilationReceipt) error {
	return service.populationService.PreviewPopulationMemberRemovalProposal(ctx, project, explorerID, receipt)
}

func (s *Service) ProposePopulationMemberRemoval(ctx context.Context, request population.PopulationMemberRemovalProposalRequest) (population.PopulationMemberRemovalProposalResponse, error) {
	return s.populationService.ProposePopulationMemberRemoval(ctx, request)
}

func (s *Service) preparePopulationMemberRemovalProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, authorized ports.AuthorizedCapability, current authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, *explorer.CompilationReceipt, error) {
	return s.populationService.PreparePopulationMemberRemovalProposal(ctx, project, explorerID, request, authorized, current, commands)
}
