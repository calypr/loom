package construction

import (
	"context"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/rowsource"
)

func sourceLaneRequest(request ConstructionProposalRequest) rowsource.ProposalRequest {
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

func (s *Service) resolveConstructionFilterSource(ctx context.Context, base Base, request ConstructionProposalRequest, acceptedStageID string, selection ConstructionFilterSourceSelection) (ws.ConstructionSourceProjection, error) {
	return s.sourceLanes().ResolveConstructionFilterSource(ctx, base, sourceLaneRequest(request), acceptedStageID, selection)
}

func (s *Service) constructionCandidateWithFilterSources(ctx context.Context, base Base, request ConstructionProposalRequest, candidate ws.Construction) (ws.Construction, error) {
	return s.sourceLanes().ConstructionCandidateWithFilterSources(ctx, base, sourceLaneRequest(request), candidate)
}

func (s *Service) reauthorizeConstructionFilterSources(ctx context.Context, base Base, outputID string, construction ws.Construction) error {
	return s.sourceLanes().ReauthorizeConstructionFilterSources(ctx, base, outputID, construction)
}

func (s *Service) constructionDeriveSourceCapability(ctx context.Context, base Base, outputID, stageID string) (ConstructionDeriveSourceCapability, error) {
	return s.sourceLanes().ConstructionDeriveSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) resolveConstructionDeriveSource(ctx context.Context, base Base, request ConstructionProposalRequest, acceptedStageID string, selection ConstructionDeriveSourceSelection) (ws.ConstructionSourceProjection, error) {
	return s.sourceLanes().ResolveConstructionDeriveSource(ctx, base, sourceLaneRequest(request), acceptedStageID, selection)
}

func (s *Service) constructionCandidateWithDeriveSources(ctx context.Context, base Base, request ConstructionProposalRequest, candidate ws.Construction) (ws.Construction, error) {
	return s.sourceLanes().ConstructionCandidateWithDeriveSources(ctx, base, sourceLaneRequest(request), candidate)
}

func (s *Service) reauthorizeConstructionDeriveSources(ctx context.Context, base Base, outputID string, construction ws.Construction) error {
	return s.sourceLanes().ReauthorizeConstructionDeriveSources(ctx, base, outputID, construction)
}

func (s *Service) constructionExpandSourceCapability(ctx context.Context, base Base, outputID, stageID string) (ConstructionExpandSourceCapability, error) {
	return s.sourceLanes().ConstructionExpandSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) resolveConstructionExpandSource(ctx context.Context, base Base, request ConstructionProposalRequest, acceptedStageID string, selection ConstructionExpandSourceSelection) (ws.ConstructionSourceProjection, error) {
	return s.sourceLanes().ResolveConstructionExpandSource(ctx, base, sourceLaneRequest(request), acceptedStageID, selection)
}

func (s *Service) constructionCandidateWithExpandSources(ctx context.Context, base Base, request ConstructionProposalRequest, candidate ws.Construction) (ws.Construction, error) {
	return s.sourceLanes().ConstructionCandidateWithExpandSources(ctx, base, sourceLaneRequest(request), candidate)
}

func (s *Service) reauthorizeConstructionExpandSources(ctx context.Context, base Base, outputID string, construction ws.Construction) error {
	return s.sourceLanes().ReauthorizeConstructionExpandSources(ctx, base, outputID, construction)
}

func (s *Service) constructionUnpivotSourceCapability(ctx context.Context, base Base, outputID, stageID string) (ConstructionUnpivotSourceCapability, error) {
	return s.sourceLanes().ConstructionUnpivotSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) resolveConstructionUnpivotSource(ctx context.Context, base Base, request ConstructionProposalRequest, acceptedStageID string, selection ConstructionUnpivotSourceSelection) (ws.ConstructionSourceProjection, error) {
	return s.sourceLanes().ResolveConstructionUnpivotSource(ctx, base, sourceLaneRequest(request), acceptedStageID, selection)
}

func (s *Service) constructionCandidateWithUnpivotSources(ctx context.Context, base Base, request ConstructionProposalRequest, candidate ws.Construction) (ws.Construction, error) {
	return s.sourceLanes().ConstructionCandidateWithUnpivotSources(ctx, base, sourceLaneRequest(request), candidate)
}

func (s *Service) reauthorizeConstructionUnpivotSources(ctx context.Context, base Base, outputID string, construction ws.Construction) error {
	return s.sourceLanes().ReauthorizeConstructionUnpivotSources(ctx, base, outputID, construction)
}

func (s *Service) reauthorizeFrameSourceChoice(ctx context.Context, project, explorerID, rowRoot, outputID, choiceID string, authorized ports.AuthorizedCapability, identity capability.ConstructionChoiceIdentity, base Base) (FrameSourceFamilyCandidate, []capability.ConstructionRouteStep, error) {
	if s.config.ReauthorizeFrameSourceChoice == nil {
		return FrameSourceFamilyCandidate{}, nil, unavailable("construction-proposal", "FRAME_SOURCE_UNAVAILABLE", "frame source reauthorization is not configured", nil)
	}
	return s.config.ReauthorizeFrameSourceChoice(ctx, base, project, explorerID, rowRoot, outputID, choiceID, authorized, identity)
}
