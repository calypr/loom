package lifecycle

import (
	"context"
	"reflect"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func constructionChoicesMatchSnapshot(identities []capability.ConstructionChoiceIdentity, snapshotToken string) bool {
	return construction.ConstructionChoicesMatchSnapshot(identities, snapshotToken)
}

func checkConstructionProposalResult(workspace authoringv2.Workspace, receipt *explorer.CompilationReceipt) error {
	return construction.CheckConstructionProposalResult(workspace, receipt)
}

func constructionCapabilitiesWorkspace(workspace authoringv2.Workspace, targetOutputID string) (authoringv2.Workspace, error) {
	return construction.ConstructionCapabilitiesWorkspace(workspace, targetOutputID)
}

func constructionChoiceIdentityMatches(choice capability.ConstructionChoice, submittedID string, identity capability.ConstructionChoiceIdentity) bool {
	return construction.ConstructionChoiceIdentityMatches(choice, submittedID, identity)
}

func constructionChoiceSupports(choice capability.ConstructionChoice, form capability.ConstructionChoiceForm) bool {
	return construction.ConstructionChoiceSupports(choice, form)
}

func constructionCompilerScope(authorized ports.AuthorizedCapability) facade.Scope {
	return construction.ConstructionCompilerScope(authorized)
}

func constructionDocumentIndex(workspace authoringv2.Workspace, outputID string) int {
	return construction.ConstructionDocumentIndex(workspace, outputID)
}

func constructionRouteForOccurrence(snapshot capability.Snapshot, rootResourceType, targetNodeID string, authoredRoot authoringv2.RouteNode, occurrenceID string) ([]capability.ConstructionRouteStep, error) {
	return construction.ConstructionRouteForOccurrence(snapshot, rootResourceType, targetNodeID, authoredRoot, occurrenceID)
}

func proveConstructionCandidate(ctx context.Context, authorized ports.AuthorizedCapability, rootResourceType string, candidate capability.Candidate, route []capability.ConstructionRouteStep) (capability.Candidate, error) {
	return construction.ProveConstructionCandidate(ctx, authorized, rootResourceType, candidate, route)
}

func (s *Service) prepareConstructionChoice(ctx context.Context, project, explorerID string, authorized ports.AuthorizedCapability, identities []capability.ConstructionChoiceIdentity, workspace authoringv2.Workspace, catalogSnapshot authoringv2.CatalogSnapshot, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	return s.syncConstructionService().PrepareConstructionChoice(ctx, project, explorerID, authorized, identities, workspace, catalogSnapshot, commands)
}

func (s *Service) prepareConstructionProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, snapshot capability.Snapshot, current authoringv2.Workspace, commands []authoringv2.Command, sinkFactory PreviewSinkFactory) ([]authoringv2.Command, *explorer.CompilationReceipt, *AcceptedConstruction, error) {
	prepared, receipt, accepted, err := s.syncConstructionService().PrepareConstructionProposal(ctx, project, explorerID, request, snapshot, current, commands, sinkFactory)
	return prepared, receipt, accepted, err
}

func (s *Service) currentWorkspace(ctx context.Context, project, explorerID string) (authoringv2.Workspace, error) {
	return s.syncConstructionService().CurrentWorkspace(ctx, project, explorerID)
}

func (s *Service) ProposeConstruction(ctx context.Context, request ConstructionProposalRequest) (ConstructionProposalResponse, error) {
	return s.syncConstructionService().ProposeConstruction(ctx, request)
}

func (s *Service) ProposeConstructionChoice(ctx context.Context, request ConstructionChoiceProposalRequest) (ConstructionChoiceProposalResponse, error) {
	return s.syncConstructionService().ProposeConstructionChoice(ctx, request)
}

func (s *Service) GetConstructionCapabilities(ctx context.Context, request ConstructionCapabilitiesRequest) (ConstructionCapabilitiesResponse, error) {
	return s.syncConstructionService().GetConstructionCapabilities(ctx, request)
}

func (s *Service) GetConstructionColumnPopulation(ctx context.Context, request ConstructionColumnPopulationRequest) (ConstructionColumnPopulationResponse, error) {
	return s.syncConstructionService().GetConstructionColumnPopulation(ctx, request)
}

func (s *Service) DiscoverConstructionCategories(ctx context.Context, request ConstructionCategoryDiscoveryRequest) (ConstructionCategoryDiscoveryResponse, error) {
	return s.syncConstructionService().DiscoverConstructionCategories(ctx, request)
}

func (s *Service) SearchRelatedFieldChoices(ctx context.Context, request RelatedFieldChoiceSearchRequest) (RelatedFieldChoiceSearchResponse, error) {
	return s.syncConstructionService().SearchRelatedFieldChoices(ctx, request)
}

func (s *Service) SearchRelatedExpandContributorChoices(ctx context.Context, request RelatedExpandContributorChoiceSearchRequest) (RelatedExpandContributorChoiceSearchResponse, error) {
	return s.syncConstructionService().SearchRelatedExpandContributorChoices(ctx, request)
}

func (s *Service) SearchRelatedExpandChoices(ctx context.Context, request RelatedExpandChoiceSearchRequest) (RelatedExpandChoiceSearchResponse, error) {
	return s.syncConstructionService().SearchRelatedExpandChoices(ctx, request)
}

func (s *Service) SearchConstructionChoices(ctx context.Context, request ConstructionChoiceSearchRequest) (ConstructionChoiceSearchResponse, error) {
	return s.syncConstructionService().SearchConstructionChoices(ctx, request)
}

func semanticChoiceKey(conceptID, bindingID string) string {
	return bindingID + "\x00" + conceptID
}

func semanticChoiceFieldOptionsMatch(semanticChoice, fieldChoice capability.ConstructionChoice) bool {
	fieldOptions := make([]capability.ConstructionChoiceOption, 0, len(semanticChoice.Options))
	for _, option := range semanticChoice.Options {
		if option.Form != capability.ConstructionChoiceOwnerRecords {
			fieldOptions = append(fieldOptions, option)
		}
	}
	return reflect.DeepEqual(fieldOptions, fieldChoice.Options)
}

func uniqueCapabilityCandidate(snapshot capability.Snapshot, candidateID string) (capability.Candidate, bool) {
	var match capability.Candidate
	found := false
	for _, candidate := range snapshot.Candidates {
		if candidate.ID != candidateID {
			continue
		}
		if found {
			return capability.Candidate{}, false
		}
		match, found = candidate, true
	}
	if found {
		return match, true
	}
	return resolveSchemaFieldCandidate(snapshot, candidateID)
}
