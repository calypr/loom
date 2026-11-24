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

func (s *Service) constructionFilterSourceCapability(ctx context.Context, base Base, outputID, stageID string) (rowsource.ConstructionFilterSourceCapability, error) {
	return s.config.SourceLanes.ConstructionFilterSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) constructionCandidateWithFilterSources(ctx context.Context, base Base, request ConstructionProposalRequest, candidate ws.Construction) (ws.Construction, error) {
	return s.config.SourceLanes.ConstructionCandidateWithFilterSources(ctx, base, request.RowSourceRequest(), candidate)
}

func (s *Service) reauthorizeConstructionFilterSources(ctx context.Context, base Base, outputID string, construction ws.Construction) error {
	return s.config.SourceLanes.ReauthorizeConstructionFilterSources(ctx, base, outputID, construction)
}

func (s *Service) constructionDeriveSourceCapability(ctx context.Context, base Base, outputID, stageID string) (rowsource.ConstructionDeriveSourceCapability, error) {
	return s.config.SourceLanes.ConstructionDeriveSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) constructionCandidateWithDeriveSources(ctx context.Context, base Base, request ConstructionProposalRequest, candidate ws.Construction) (ws.Construction, error) {
	return s.config.SourceLanes.ConstructionCandidateWithDeriveSources(ctx, base, request.RowSourceRequest(), candidate)
}

func (s *Service) reauthorizeConstructionDeriveSources(ctx context.Context, base Base, outputID string, construction ws.Construction) error {
	return s.config.SourceLanes.ReauthorizeConstructionDeriveSources(ctx, base, outputID, construction)
}

func (s *Service) constructionExpandSourceCapability(ctx context.Context, base Base, outputID, stageID string) (rowsource.ConstructionExpandSourceCapability, error) {
	return s.config.SourceLanes.ConstructionExpandSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) constructionCandidateWithExpandSources(ctx context.Context, base Base, request ConstructionProposalRequest, candidate ws.Construction) (ws.Construction, error) {
	return s.config.SourceLanes.ConstructionCandidateWithExpandSources(ctx, base, request.RowSourceRequest(), candidate)
}

func (s *Service) reauthorizeConstructionExpandSources(ctx context.Context, base Base, outputID string, construction ws.Construction) error {
	return s.config.SourceLanes.ReauthorizeConstructionExpandSources(ctx, base, outputID, construction)
}

func (s *Service) constructionUnpivotSourceCapability(ctx context.Context, base Base, outputID, stageID string) (rowsource.ConstructionUnpivotSourceCapability, error) {
	return s.config.SourceLanes.ConstructionUnpivotSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) constructionCandidateWithUnpivotSources(ctx context.Context, base Base, request ConstructionProposalRequest, candidate ws.Construction) (ws.Construction, error) {
	return s.config.SourceLanes.ConstructionCandidateWithUnpivotSources(ctx, base, request.RowSourceRequest(), candidate)
}

func (s *Service) reauthorizeConstructionUnpivotSources(ctx context.Context, base Base, outputID string, construction ws.Construction) error {
	return s.config.SourceLanes.ReauthorizeConstructionUnpivotSources(ctx, base, outputID, construction)
}
