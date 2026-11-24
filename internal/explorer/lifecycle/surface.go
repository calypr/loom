package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer/authoringcore"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/explorer/construction"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func (s *Service) prepareConstructionChoice(ctx context.Context, project, explorerID string, authorized ports.AuthorizedCapability, identities []capability.ConstructionChoiceIdentity, workspace ws.Workspace, catalogSnapshot ws.CatalogSnapshot, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	return s.constructionService.PrepareConstructionChoice(ctx, project, explorerID, authorized, identities, workspace, catalogSnapshot, commands)
}

func (s *Service) prepareConstructionProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, snapshot capability.Snapshot, current ws.Workspace, commands []authoringv2.Command, sinkFactory authoringcore.PreviewSinkFactory) ([]authoringv2.Command, *receiptmodel.CompilationReceipt, *construction.AcceptedConstruction, error) {
	prepared, receipt, accepted, err := s.constructionService.PrepareConstructionProposal(ctx, project, explorerID, request, snapshot, current, commands, sinkFactory)
	return prepared, receipt, accepted, err
}

func (s *Service) ProposeConstruction(ctx context.Context, request construction.ConstructionProposalRequest) (construction.ConstructionProposalResponse, error) {
	return s.constructionService.ProposeConstruction(ctx, request)
}

func (s *Service) ProposeConstructionChoice(ctx context.Context, request construction.ConstructionChoiceProposalRequest) (construction.ConstructionChoiceProposalResponse, error) {
	return s.constructionService.ProposeConstructionChoice(ctx, request)
}

func (s *Service) GetConstructionCapabilities(ctx context.Context, request construction.ConstructionCapabilitiesRequest) (construction.ConstructionCapabilitiesResponse, error) {
	return s.constructionService.GetConstructionCapabilities(ctx, request)
}

func (s *Service) GetConstructionColumnPopulation(ctx context.Context, request construction.ConstructionColumnPopulationRequest) (construction.ConstructionColumnPopulationResponse, error) {
	return s.constructionService.GetConstructionColumnPopulation(ctx, request)
}

func (s *Service) DiscoverConstructionCategories(ctx context.Context, request construction.ConstructionCategoryDiscoveryRequest) (construction.ConstructionCategoryDiscoveryResponse, error) {
	return s.constructionService.DiscoverConstructionCategories(ctx, request)
}

func (s *Service) SearchRelatedFieldChoices(ctx context.Context, request construction.RelatedFieldChoiceSearchRequest) (construction.RelatedFieldChoiceSearchResponse, error) {
	return s.constructionService.SearchRelatedFieldChoices(ctx, request)
}

func (s *Service) SearchRelatedExpandContributorChoices(ctx context.Context, request construction.RelatedExpandContributorChoiceSearchRequest) (construction.RelatedExpandContributorChoiceSearchResponse, error) {
	return s.constructionService.SearchRelatedExpandContributorChoices(ctx, request)
}

func (s *Service) SearchRelatedExpandChoices(ctx context.Context, request construction.RelatedExpandChoiceSearchRequest) (construction.RelatedExpandChoiceSearchResponse, error) {
	return s.constructionService.SearchRelatedExpandChoices(ctx, request)
}

func (s *Service) SearchConstructionChoices(ctx context.Context, request construction.ConstructionChoiceSearchRequest) (construction.ConstructionChoiceSearchResponse, error) {
	return s.constructionService.SearchConstructionChoices(ctx, request)
}
