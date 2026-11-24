package construction

import (
	"context"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/rowsource"
)

func (request ConstructionProposalRequest) RowSourceRequest() rowsource.ProposalRequest {
	return rowsource.ProposalRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, OutputID: request.OutputID,
		ChangedStepID: request.ChangedStepID,
		FilterSources: request.FilterSources, DeriveSources: request.DeriveSources,
		UnpivotSources: request.UnpivotSources, ExpandSources: request.ExpandSources,
	}
}

func (s *Service) sourceLanes() SourceLanes { return s.config.SourceLanes }

func (s *Service) constructionFilterSourceCapability(ctx context.Context, base Base, outputID, stageID string) (ConstructionFilterSourceCapability, error) {
	return s.sourceLanes().ConstructionFilterSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) constructionCandidateWithFilterSources(ctx context.Context, base Base, request ConstructionProposalRequest, candidate ws.Construction) (ws.Construction, error) {
	return s.sourceLanes().ConstructionCandidateWithFilterSources(ctx, base, request.RowSourceRequest(), candidate)
}

func (s *Service) reauthorizeConstructionFilterSources(ctx context.Context, base Base, outputID string, construction ws.Construction) error {
	return s.sourceLanes().ReauthorizeConstructionFilterSources(ctx, base, outputID, construction)
}

func (s *Service) constructionDeriveSourceCapability(ctx context.Context, base Base, outputID, stageID string) (ConstructionDeriveSourceCapability, error) {
	return s.sourceLanes().ConstructionDeriveSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) constructionCandidateWithDeriveSources(ctx context.Context, base Base, request ConstructionProposalRequest, candidate ws.Construction) (ws.Construction, error) {
	return s.sourceLanes().ConstructionCandidateWithDeriveSources(ctx, base, request.RowSourceRequest(), candidate)
}

func (s *Service) reauthorizeConstructionDeriveSources(ctx context.Context, base Base, outputID string, construction ws.Construction) error {
	return s.sourceLanes().ReauthorizeConstructionDeriveSources(ctx, base, outputID, construction)
}

func (s *Service) constructionExpandSourceCapability(ctx context.Context, base Base, outputID, stageID string) (ConstructionExpandSourceCapability, error) {
	return s.sourceLanes().ConstructionExpandSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) constructionCandidateWithExpandSources(ctx context.Context, base Base, request ConstructionProposalRequest, candidate ws.Construction) (ws.Construction, error) {
	return s.sourceLanes().ConstructionCandidateWithExpandSources(ctx, base, request.RowSourceRequest(), candidate)
}

func (s *Service) reauthorizeConstructionExpandSources(ctx context.Context, base Base, outputID string, construction ws.Construction) error {
	return s.sourceLanes().ReauthorizeConstructionExpandSources(ctx, base, outputID, construction)
}

func (s *Service) constructionUnpivotSourceCapability(ctx context.Context, base Base, outputID, stageID string) (ConstructionUnpivotSourceCapability, error) {
	return s.sourceLanes().ConstructionUnpivotSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) constructionCandidateWithUnpivotSources(ctx context.Context, base Base, request ConstructionProposalRequest, candidate ws.Construction) (ws.Construction, error) {
	return s.sourceLanes().ConstructionCandidateWithUnpivotSources(ctx, base, request.RowSourceRequest(), candidate)
}

func (s *Service) reauthorizeConstructionUnpivotSources(ctx context.Context, base Base, outputID string, construction ws.Construction) error {
	return s.sourceLanes().ReauthorizeConstructionUnpivotSources(ctx, base, outputID, construction)
}
