package lifecycle

import (
	"context"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/population"
)

func checkPopulationMemberRemovalProposalResult(workspace ws.Workspace, receipt *receiptmodel.CompilationReceipt) error {
	return population.CheckPopulationMemberRemovalProposalResult(workspace, receipt)
}

func previewPopulationMemberRemovalProposal(ctx context.Context, service *Service, project, explorerID string, receipt *receiptmodel.CompilationReceipt) error {
	return service.populationService.PreviewPopulationMemberRemovalProposal(ctx, project, explorerID, receipt)
}

func (s *Service) ProposePopulationMemberRemoval(ctx context.Context, request population.PopulationMemberRemovalProposalRequest) (population.PopulationMemberRemovalProposalResponse, error) {
	return s.populationService.ProposePopulationMemberRemoval(ctx, request)
}

func (s *Service) preparePopulationMemberRemovalProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, authorized ports.AuthorizedCapability, current ws.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, *receiptmodel.CompilationReceipt, error) {
	return s.populationService.PreparePopulationMemberRemovalProposal(ctx, project, explorerID, request, authorized, current, commands)
}
