package lifecycle

import (
	"context"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/proposal"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func (s *Service) prepareConstructionChoice(ctx context.Context, project, explorerID string, authorized ports.AuthorizedCapability, identities []capability.ConstructionChoiceIdentity, workspace ws.Workspace, catalogSnapshot ws.CatalogSnapshot, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	return s.syncConstructionService().PrepareConstructionChoice(ctx, project, explorerID, authorized, identities, workspace, catalogSnapshot, commands)
}

func (s *Service) prepareConstructionProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, snapshot capability.Snapshot, current ws.Workspace, commands []authoringv2.Command, sinkFactory proposal.PreviewSinkFactory) ([]authoringv2.Command, *receiptmodel.CompilationReceipt, *construction.AcceptedConstruction, error) {
	prepared, receipt, accepted, err := s.syncConstructionService().PrepareConstructionProposal(ctx, project, explorerID, request, snapshot, current, commands, sinkFactory)
	return prepared, receipt, accepted, err
}

func (s *Service) currentWorkspace(ctx context.Context, project, explorerID string) (ws.Workspace, error) {
	return s.syncConstructionService().CurrentWorkspace(ctx, project, explorerID)
}

func (s *Service) ProposeConstruction(ctx context.Context, request construction.ConstructionProposalRequest) (construction.ConstructionProposalResponse, error) {
	return s.syncConstructionService().ProposeConstruction(ctx, request)
}

func (s *Service) ProposeConstructionChoice(ctx context.Context, request construction.ConstructionChoiceProposalRequest) (construction.ConstructionChoiceProposalResponse, error) {
	return s.syncConstructionService().ProposeConstructionChoice(ctx, request)
}

func (s *Service) GetConstructionCapabilities(ctx context.Context, request construction.ConstructionCapabilitiesRequest) (construction.ConstructionCapabilitiesResponse, error) {
	return s.syncConstructionService().GetConstructionCapabilities(ctx, request)
}

func (s *Service) GetConstructionColumnPopulation(ctx context.Context, request construction.ConstructionColumnPopulationRequest) (construction.ConstructionColumnPopulationResponse, error) {
	return s.syncConstructionService().GetConstructionColumnPopulation(ctx, request)
}

func (s *Service) DiscoverConstructionCategories(ctx context.Context, request construction.ConstructionCategoryDiscoveryRequest) (construction.ConstructionCategoryDiscoveryResponse, error) {
	return s.syncConstructionService().DiscoverConstructionCategories(ctx, request)
}

func (s *Service) SearchRelatedFieldChoices(ctx context.Context, request construction.RelatedFieldChoiceSearchRequest) (construction.RelatedFieldChoiceSearchResponse, error) {
	return s.syncConstructionService().SearchRelatedFieldChoices(ctx, request)
}

func (s *Service) SearchRelatedExpandContributorChoices(ctx context.Context, request construction.RelatedExpandContributorChoiceSearchRequest) (construction.RelatedExpandContributorChoiceSearchResponse, error) {
	return s.syncConstructionService().SearchRelatedExpandContributorChoices(ctx, request)
}

func (s *Service) SearchRelatedExpandChoices(ctx context.Context, request construction.RelatedExpandChoiceSearchRequest) (construction.RelatedExpandChoiceSearchResponse, error) {
	return s.syncConstructionService().SearchRelatedExpandChoices(ctx, request)
}

func (s *Service) SearchConstructionChoices(ctx context.Context, request construction.ConstructionChoiceSearchRequest) (construction.ConstructionChoiceSearchResponse, error) {
	return s.syncConstructionService().SearchConstructionChoices(ctx, request)
}
