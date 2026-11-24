package lifecycle

import (
	"context"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/rowsource"
)

func rowSourceProposalRequest(request construction.ConstructionProposalRequest) rowsource.ProposalRequest {
	return rowsource.ProposalRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, OutputID: request.OutputID,
		ChangedStepID: request.ChangedStepID,
		FilterSources: request.FilterSources, DeriveSources: request.DeriveSources,
		UnpivotSources: request.UnpivotSources, ExpandSources: request.ExpandSources,
	}
}

func ConstructionFilterSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (ws.ConstructionSourceProjection, bool) {
	return rowsource.ConstructionFilterSourceFacts(snapshot, rootResourceType, fieldPath)
}

func ConstructionDeriveSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (ws.ConstructionSourceProjection, bool) {
	return rowsource.ConstructionDeriveSourceFacts(snapshot, rootResourceType, fieldPath)
}

func ConstructionExpandSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (ws.ConstructionSourceProjection, bool) {
	return rowsource.ConstructionExpandSourceFacts(snapshot, rootResourceType, fieldPath)
}

func ConstructionUnpivotSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (ws.ConstructionSourceProjection, bool) {
	return rowsource.ConstructionUnpivotSourceFacts(snapshot, rootResourceType, fieldPath)
}

func (s *Service) constructionFilterSourceCapability(ctx context.Context, base constructionBase, outputID, stageID string) (rowsource.ConstructionFilterSourceCapability, error) {
	return s.rowSourceService.ConstructionFilterSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) resolveConstructionFilterSource(ctx context.Context, base constructionBase, request construction.ConstructionProposalRequest, acceptedStageID string, selection rowsource.ConstructionFilterSourceSelection) (ws.ConstructionSourceProjection, error) {
	return s.rowSourceService.ResolveConstructionFilterSource(ctx, base, rowSourceProposalRequest(request), acceptedStageID, selection)
}

func (s *Service) constructionCandidateWithFilterSources(ctx context.Context, base constructionBase, request construction.ConstructionProposalRequest, candidate ws.Construction) (ws.Construction, error) {
	return s.rowSourceService.ConstructionCandidateWithFilterSources(ctx, base, rowSourceProposalRequest(request), candidate)
}

func (s *Service) constructionDeriveSourceCapability(ctx context.Context, base constructionBase, outputID, stageID string) (rowsource.ConstructionDeriveSourceCapability, error) {
	return s.rowSourceService.ConstructionDeriveSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) resolveConstructionDeriveSource(ctx context.Context, base constructionBase, request construction.ConstructionProposalRequest, acceptedStageID string, selection rowsource.ConstructionDeriveSourceSelection) (ws.ConstructionSourceProjection, error) {
	return s.rowSourceService.ResolveConstructionDeriveSource(ctx, base, rowSourceProposalRequest(request), acceptedStageID, selection)
}

func (s *Service) constructionCandidateWithDeriveSources(ctx context.Context, base constructionBase, request construction.ConstructionProposalRequest, candidate ws.Construction) (ws.Construction, error) {
	return s.rowSourceService.ConstructionCandidateWithDeriveSources(ctx, base, rowSourceProposalRequest(request), candidate)
}

func (s *Service) constructionExpandSourceCapability(ctx context.Context, base constructionBase, outputID, stageID string) (rowsource.ConstructionExpandSourceCapability, error) {
	return s.rowSourceService.ConstructionExpandSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) constructionCandidateWithExpandSources(ctx context.Context, base constructionBase, request construction.ConstructionProposalRequest, candidate ws.Construction) (ws.Construction, error) {
	return s.rowSourceService.ConstructionCandidateWithExpandSources(ctx, base, rowSourceProposalRequest(request), candidate)
}

func (s *Service) reauthorizeConstructionExpandSources(ctx context.Context, base constructionBase, outputID string, construction ws.Construction) error {
	return s.rowSourceService.ReauthorizeConstructionExpandSources(ctx, base, outputID, construction)
}

func (s *Service) constructionUnpivotSourceCapability(ctx context.Context, base constructionBase, outputID, stageID string) (rowsource.ConstructionUnpivotSourceCapability, error) {
	return s.rowSourceService.ConstructionUnpivotSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) resolveConstructionUnpivotSource(ctx context.Context, base constructionBase, request construction.ConstructionProposalRequest, acceptedStageID string, selection rowsource.ConstructionUnpivotSourceSelection) (ws.ConstructionSourceProjection, error) {
	return s.rowSourceService.ResolveConstructionUnpivotSource(ctx, base, rowSourceProposalRequest(request), acceptedStageID, selection)
}

func (s *Service) constructionCandidateWithUnpivotSources(ctx context.Context, base constructionBase, request construction.ConstructionProposalRequest, candidate ws.Construction) (ws.Construction, error) {
	return s.rowSourceService.ConstructionCandidateWithUnpivotSources(ctx, base, rowSourceProposalRequest(request), candidate)
}
