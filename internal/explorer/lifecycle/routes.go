package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer/population"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func provePopulationRoute(ctx context.Context, authorized ports.AuthorizedCapability, rootResourceType, terminalResourceType string, route []capability.ConstructionRouteStep) bool {
	compilerRoute := make([]facade.Traversal, 0, len(route))
	for _, step := range route {
		matchMode := spec.TraversalMatchOptional
		if step.MatchMode == string(spec.TraversalMatchRequired) {
			matchMode = spec.TraversalMatchRequired
		}
		traversal := facade.Traversal{FromResourceType: step.FromResourceType, EdgeLabel: step.Relationship, ToResourceType: step.ToResourceType, MatchMode: matchMode}
		proof, err := facade.ProbeTraversal(ctx, facade.TraversalRequest{
			Scope: constructionCompilerScope(authorized), RootResourceType: step.FromResourceType, Traversal: traversal,
		})
		if err != nil || proof.Traversal == nil || string(proof.Traversal.StorageDirection) != step.StorageDirection {
			return false
		}
		compilerRoute = append(compilerRoute, traversal)
	}
	_, err := facade.ProbeCandidate(ctx, facade.CandidateRequest{
		Scope: constructionCompilerScope(authorized), RootResourceType: rootResourceType,
		ResourceType: terminalResourceType, FieldRef: terminalResourceType + ".id", Selector: "id", Route: compilerRoute,
	})
	return err == nil
}

func (s *Service) SearchPopulationRoutes(ctx context.Context, request population.PopulationRoutesRequest) (population.PopulationRoutesResponse, error) {
	return s.populationService.SearchPopulationRoutes(ctx, request)
}

func (s *Service) preparePopulationRouteChoices(ctx context.Context, project string, authorized ports.AuthorizedCapability, identities []capability.PopulationRouteChoiceIdentity, workspace authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	return s.populationService.PreparePopulationRouteChoices(ctx, project, authorized, identities, workspace, commands)
}
