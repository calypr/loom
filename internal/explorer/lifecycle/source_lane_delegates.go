package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/rowsource"
)

func (s *Service) rowSourceService() *rowsource.Service {
	return rowsource.NewService(rowsource.Config{
		Capability:        s.config.Capability,
		SemanticInventory: s.config.SemanticInventory,
		RowChoiceResolver: s.config.RowChoiceResolver,
		RowChoicePlanner:  s.config.RowChoicePlanner,
	})
}

func (s *Service) constructionFilterSourceCapability(ctx context.Context, base constructionBase, outputID, stageID string) (ConstructionFilterSourceCapability, error) {
	return s.rowSourceService().ConstructionFilterSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) resolveConstructionFilterSource(ctx context.Context, base constructionBase, request ConstructionProposalRequest, acceptedStageID string, selection ConstructionFilterSourceSelection) (authoringv2.ConstructionSourceProjection, error) {
	return s.rowSourceService().ResolveConstructionFilterSource(ctx, base, rowSourceProposalRequest(request), acceptedStageID, selection)
}

func (s *Service) constructionCandidateWithFilterSources(ctx context.Context, base constructionBase, request ConstructionProposalRequest, candidate authoringv2.Construction) (authoringv2.Construction, error) {
	return s.rowSourceService().ConstructionCandidateWithFilterSources(ctx, base, rowSourceProposalRequest(request), candidate)
}

func (s *Service) reauthorizeConstructionFilterSources(ctx context.Context, base constructionBase, outputID string, construction authoringv2.Construction) error {
	return s.rowSourceService().ReauthorizeConstructionFilterSources(ctx, base, outputID, construction)
}

func (s *Service) constructionDeriveSourceCapability(ctx context.Context, base constructionBase, outputID, stageID string) (ConstructionDeriveSourceCapability, error) {
	return s.rowSourceService().ConstructionDeriveSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) resolveConstructionDeriveSource(ctx context.Context, base constructionBase, request ConstructionProposalRequest, acceptedStageID string, selection ConstructionDeriveSourceSelection) (authoringv2.ConstructionSourceProjection, error) {
	return s.rowSourceService().ResolveConstructionDeriveSource(ctx, base, rowSourceProposalRequest(request), acceptedStageID, selection)
}

func (s *Service) constructionCandidateWithDeriveSources(ctx context.Context, base constructionBase, request ConstructionProposalRequest, candidate authoringv2.Construction) (authoringv2.Construction, error) {
	return s.rowSourceService().ConstructionCandidateWithDeriveSources(ctx, base, rowSourceProposalRequest(request), candidate)
}

func (s *Service) reauthorizeConstructionDeriveSources(ctx context.Context, base constructionBase, outputID string, construction authoringv2.Construction) error {
	return s.rowSourceService().ReauthorizeConstructionDeriveSources(ctx, base, outputID, construction)
}

func (s *Service) constructionExpandSourceCapability(ctx context.Context, base constructionBase, outputID, stageID string) (ConstructionExpandSourceCapability, error) {
	return s.rowSourceService().ConstructionExpandSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) resolveConstructionExpandSource(ctx context.Context, base constructionBase, request ConstructionProposalRequest, acceptedStageID string, selection ConstructionExpandSourceSelection) (authoringv2.ConstructionSourceProjection, error) {
	return s.rowSourceService().ResolveConstructionExpandSource(ctx, base, rowSourceProposalRequest(request), acceptedStageID, selection)
}

func (s *Service) constructionCandidateWithExpandSources(ctx context.Context, base constructionBase, request ConstructionProposalRequest, candidate authoringv2.Construction) (authoringv2.Construction, error) {
	return s.rowSourceService().ConstructionCandidateWithExpandSources(ctx, base, rowSourceProposalRequest(request), candidate)
}

func (s *Service) reauthorizeConstructionExpandSources(ctx context.Context, base constructionBase, outputID string, construction authoringv2.Construction) error {
	return s.rowSourceService().ReauthorizeConstructionExpandSources(ctx, base, outputID, construction)
}

func (s *Service) constructionUnpivotSourceCapability(ctx context.Context, base constructionBase, outputID, stageID string) (ConstructionUnpivotSourceCapability, error) {
	return s.rowSourceService().ConstructionUnpivotSourceCapability(ctx, base, outputID, stageID)
}

func (s *Service) resolveConstructionUnpivotSource(ctx context.Context, base constructionBase, request ConstructionProposalRequest, acceptedStageID string, selection ConstructionUnpivotSourceSelection) (authoringv2.ConstructionSourceProjection, error) {
	return s.rowSourceService().ResolveConstructionUnpivotSource(ctx, base, rowSourceProposalRequest(request), acceptedStageID, selection)
}

func (s *Service) constructionCandidateWithUnpivotSources(ctx context.Context, base constructionBase, request ConstructionProposalRequest, candidate authoringv2.Construction) (authoringv2.Construction, error) {
	return s.rowSourceService().ConstructionCandidateWithUnpivotSources(ctx, base, rowSourceProposalRequest(request), candidate)
}

func (s *Service) reauthorizeConstructionUnpivotSources(ctx context.Context, base constructionBase, outputID string, construction authoringv2.Construction) error {
	return s.rowSourceService().ReauthorizeConstructionUnpivotSources(ctx, base, outputID, construction)
}

func rowSourceProposalRequest(request ConstructionProposalRequest) rowsource.ProposalRequest {
	return rowsource.ProposalRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, OutputID: request.OutputID,
		ChangedStepID: request.ChangedStepID,
		FilterSources: request.FilterSources, DeriveSources: request.DeriveSources,
		UnpivotSources: request.UnpivotSources, ExpandSources: request.ExpandSources,
	}
}

func ConstructionFilterSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (authoringv2.ConstructionSourceProjection, bool) {
	return rowsource.ConstructionFilterSourceFacts(snapshot, rootResourceType, fieldPath)
}

func ConstructionDeriveSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (authoringv2.ConstructionSourceProjection, bool) {
	return rowsource.ConstructionDeriveSourceFacts(snapshot, rootResourceType, fieldPath)
}

func ConstructionExpandSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (authoringv2.ConstructionSourceProjection, bool) {
	return rowsource.ConstructionExpandSourceFacts(snapshot, rootResourceType, fieldPath)
}

func ConstructionUnpivotSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (authoringv2.ConstructionSourceProjection, bool) {
	return rowsource.ConstructionUnpivotSourceFacts(snapshot, rootResourceType, fieldPath)
}

func deriveSourceLogicalTypeIsNumeric(logicalType string) bool {
	return rowsource.DeriveSourceLogicalTypeIsNumeric(logicalType)
}

func unpivotSourceLogicalTypeIsTableScalar(logicalType string) bool {
	return rowsource.UnpivotSourceLogicalTypeIsTableScalar(logicalType)
}

func unpivotSourceLogicalTypeIsMeltEligible(logicalType string) bool {
	return rowsource.UnpivotSourceLogicalTypeIsMeltEligible(logicalType)
}

func unpivotMeltKindsSatisfiable(kinds []string) bool {
	return rowsource.UnpivotMeltKindsSatisfiable(kinds)
}
