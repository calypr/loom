package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/construction"
)

type constructionSourceLanes struct {
	service *Service
}

func (l constructionSourceLanes) ConstructionFilterSourceCapability(ctx context.Context, base construction.Base, outputID, stageID string) (construction.ConstructionFilterSourceCapability, error) {
	return l.service.constructionFilterSourceCapability(ctx, base, outputID, stageID)
}
func (l constructionSourceLanes) ResolveConstructionFilterSource(ctx context.Context, base construction.Base, request construction.ConstructionProposalRequest, acceptedStageID string, selection construction.ConstructionFilterSourceSelection) (authoringv2.ConstructionSourceProjection, error) {
	return l.service.resolveConstructionFilterSource(ctx, base, request, acceptedStageID, selection)
}
func (l constructionSourceLanes) ConstructionCandidateWithFilterSources(ctx context.Context, base construction.Base, request construction.ConstructionProposalRequest, candidate authoringv2.Construction) (authoringv2.Construction, error) {
	return l.service.constructionCandidateWithFilterSources(ctx, base, request, candidate)
}
func (l constructionSourceLanes) ReauthorizeConstructionFilterSources(ctx context.Context, base construction.Base, outputID string, candidate authoringv2.Construction) error {
	return l.service.reauthorizeConstructionFilterSources(ctx, base, outputID, candidate)
}
func (l constructionSourceLanes) ConstructionHasFilterSourceInput(candidate authoringv2.Construction) bool {
	return constructionHasFilterSourceInput(candidate)
}

func (l constructionSourceLanes) ConstructionDeriveSourceCapability(ctx context.Context, base construction.Base, outputID, stageID string) (construction.ConstructionDeriveSourceCapability, error) {
	return l.service.constructionDeriveSourceCapability(ctx, base, outputID, stageID)
}
func (l constructionSourceLanes) ResolveConstructionDeriveSource(ctx context.Context, base construction.Base, request construction.ConstructionProposalRequest, acceptedStageID string, selection construction.ConstructionDeriveSourceSelection) (authoringv2.ConstructionSourceProjection, error) {
	return l.service.resolveConstructionDeriveSource(ctx, base, request, acceptedStageID, selection)
}
func (l constructionSourceLanes) ConstructionCandidateWithDeriveSources(ctx context.Context, base construction.Base, request construction.ConstructionProposalRequest, candidate authoringv2.Construction) (authoringv2.Construction, error) {
	return l.service.constructionCandidateWithDeriveSources(ctx, base, request, candidate)
}
func (l constructionSourceLanes) ReauthorizeConstructionDeriveSources(ctx context.Context, base construction.Base, outputID string, candidate authoringv2.Construction) error {
	return l.service.reauthorizeConstructionDeriveSources(ctx, base, outputID, candidate)
}
func (l constructionSourceLanes) ConstructionHasDeriveSourceInput(candidate authoringv2.Construction) bool {
	return constructionHasDeriveSourceInput(candidate)
}

func (l constructionSourceLanes) ConstructionExpandSourceCapability(ctx context.Context, base construction.Base, outputID, stageID string) (construction.ConstructionExpandSourceCapability, error) {
	return l.service.constructionExpandSourceCapability(ctx, base, outputID, stageID)
}
func (l constructionSourceLanes) ResolveConstructionExpandSource(ctx context.Context, base construction.Base, request construction.ConstructionProposalRequest, acceptedStageID string, selection construction.ConstructionExpandSourceSelection) (authoringv2.ConstructionSourceProjection, error) {
	return l.service.resolveConstructionExpandSource(ctx, base, request, acceptedStageID, selection)
}
func (l constructionSourceLanes) ConstructionCandidateWithExpandSources(ctx context.Context, base construction.Base, request construction.ConstructionProposalRequest, candidate authoringv2.Construction) (authoringv2.Construction, error) {
	return l.service.constructionCandidateWithExpandSources(ctx, base, request, candidate)
}
func (l constructionSourceLanes) ReauthorizeConstructionExpandSources(ctx context.Context, base construction.Base, outputID string, candidate authoringv2.Construction) error {
	return l.service.reauthorizeConstructionExpandSources(ctx, base, outputID, candidate)
}
func (l constructionSourceLanes) ConstructionHasExpandSourceInput(candidate authoringv2.Construction) bool {
	return constructionHasExpandSourceInput(candidate)
}

func (l constructionSourceLanes) ConstructionUnpivotSourceCapability(ctx context.Context, base construction.Base, outputID, stageID string) (construction.ConstructionUnpivotSourceCapability, error) {
	return l.service.constructionUnpivotSourceCapability(ctx, base, outputID, stageID)
}
func (l constructionSourceLanes) ResolveConstructionUnpivotSource(ctx context.Context, base construction.Base, request construction.ConstructionProposalRequest, acceptedStageID string, selection construction.ConstructionUnpivotSourceSelection) (authoringv2.ConstructionSourceProjection, error) {
	return l.service.resolveConstructionUnpivotSource(ctx, base, request, acceptedStageID, selection)
}
func (l constructionSourceLanes) ConstructionCandidateWithUnpivotSources(ctx context.Context, base construction.Base, request construction.ConstructionProposalRequest, candidate authoringv2.Construction) (authoringv2.Construction, error) {
	return l.service.constructionCandidateWithUnpivotSources(ctx, base, request, candidate)
}
func (l constructionSourceLanes) ReauthorizeConstructionUnpivotSources(ctx context.Context, base construction.Base, outputID string, candidate authoringv2.Construction) error {
	return l.service.reauthorizeConstructionUnpivotSources(ctx, base, outputID, candidate)
}
func (l constructionSourceLanes) ConstructionHasUnpivotSourceInput(candidate authoringv2.Construction) bool {
	return constructionHasUnpivotSourceInput(candidate)
}
