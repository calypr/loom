package construction

import (
	"context"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func (s *Service) sourceLanes() SourceLanes { return s.config.SourceLanes }

func (s *Service) constructionFilterSourceCapability(ctx context.Context, base Base, outputID, stageID string) (ConstructionFilterSourceCapability, error) {
	return s.sourceLanes().ConstructionFilterSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) resolveConstructionFilterSource(ctx context.Context, base Base, request ConstructionProposalRequest, acceptedStageID string, selection ConstructionFilterSourceSelection) (authoringv2.ConstructionSourceProjection, error) {
	return s.sourceLanes().ResolveConstructionFilterSource(ctx, base, request, acceptedStageID, selection)
}

func (s *Service) constructionCandidateWithFilterSources(ctx context.Context, base Base, request ConstructionProposalRequest, candidate authoringv2.Construction) (authoringv2.Construction, error) {
	return s.sourceLanes().ConstructionCandidateWithFilterSources(ctx, base, request, candidate)
}

func (s *Service) reauthorizeConstructionFilterSources(ctx context.Context, base Base, outputID string, construction authoringv2.Construction) error {
	return s.sourceLanes().ReauthorizeConstructionFilterSources(ctx, base, outputID, construction)
}

func (s *Service) constructionDeriveSourceCapability(ctx context.Context, base Base, outputID, stageID string) (ConstructionDeriveSourceCapability, error) {
	return s.sourceLanes().ConstructionDeriveSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) resolveConstructionDeriveSource(ctx context.Context, base Base, request ConstructionProposalRequest, acceptedStageID string, selection ConstructionDeriveSourceSelection) (authoringv2.ConstructionSourceProjection, error) {
	return s.sourceLanes().ResolveConstructionDeriveSource(ctx, base, request, acceptedStageID, selection)
}

func (s *Service) constructionCandidateWithDeriveSources(ctx context.Context, base Base, request ConstructionProposalRequest, candidate authoringv2.Construction) (authoringv2.Construction, error) {
	return s.sourceLanes().ConstructionCandidateWithDeriveSources(ctx, base, request, candidate)
}

func (s *Service) reauthorizeConstructionDeriveSources(ctx context.Context, base Base, outputID string, construction authoringv2.Construction) error {
	return s.sourceLanes().ReauthorizeConstructionDeriveSources(ctx, base, outputID, construction)
}

func (s *Service) constructionExpandSourceCapability(ctx context.Context, base Base, outputID, stageID string) (ConstructionExpandSourceCapability, error) {
	return s.sourceLanes().ConstructionExpandSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) resolveConstructionExpandSource(ctx context.Context, base Base, request ConstructionProposalRequest, acceptedStageID string, selection ConstructionExpandSourceSelection) (authoringv2.ConstructionSourceProjection, error) {
	return s.sourceLanes().ResolveConstructionExpandSource(ctx, base, request, acceptedStageID, selection)
}

func (s *Service) constructionCandidateWithExpandSources(ctx context.Context, base Base, request ConstructionProposalRequest, candidate authoringv2.Construction) (authoringv2.Construction, error) {
	return s.sourceLanes().ConstructionCandidateWithExpandSources(ctx, base, request, candidate)
}

func (s *Service) reauthorizeConstructionExpandSources(ctx context.Context, base Base, outputID string, construction authoringv2.Construction) error {
	return s.sourceLanes().ReauthorizeConstructionExpandSources(ctx, base, outputID, construction)
}

func (s *Service) constructionUnpivotSourceCapability(ctx context.Context, base Base, outputID, stageID string) (ConstructionUnpivotSourceCapability, error) {
	return s.sourceLanes().ConstructionUnpivotSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) resolveConstructionUnpivotSource(ctx context.Context, base Base, request ConstructionProposalRequest, acceptedStageID string, selection ConstructionUnpivotSourceSelection) (authoringv2.ConstructionSourceProjection, error) {
	return s.sourceLanes().ResolveConstructionUnpivotSource(ctx, base, request, acceptedStageID, selection)
}

func (s *Service) constructionCandidateWithUnpivotSources(ctx context.Context, base Base, request ConstructionProposalRequest, candidate authoringv2.Construction) (authoringv2.Construction, error) {
	return s.sourceLanes().ConstructionCandidateWithUnpivotSources(ctx, base, request, candidate)
}

func (s *Service) reauthorizeConstructionUnpivotSources(ctx context.Context, base Base, outputID string, construction authoringv2.Construction) error {
	return s.sourceLanes().ReauthorizeConstructionUnpivotSources(ctx, base, outputID, construction)
}

func (s *Service) reauthorizeFrameSourceChoice(ctx context.Context, project, explorerID, rowRoot, outputID, choiceID string, authorized ports.AuthorizedCapability, identity capability.ConstructionChoiceIdentity, base Base) (FrameSourceFamilyCandidate, []capability.ConstructionRouteStep, error) {
	if s.config.ReauthorizeFrameSourceChoice == nil {
		return FrameSourceFamilyCandidate{}, nil, unavailable("construction-proposal", "FRAME_SOURCE_UNAVAILABLE", "frame source reauthorization is not configured", nil)
	}
	return s.config.ReauthorizeFrameSourceChoice(ctx, base, project, explorerID, rowRoot, outputID, choiceID, authorized, identity)
}
