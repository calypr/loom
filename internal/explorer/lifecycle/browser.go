package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/semanticbrowse"
)

func (s *Service) browseService() *semanticbrowse.Service {
	if s.semanticBrowse != nil {
		return s.semanticBrowse
	}
	return semanticbrowse.NewService(semanticbrowse.Config{
		Core:                               s.core,
		Capability:                         s.config.Capability,
		SemanticInventory:                  s.config.SemanticInventory,
		SemanticCoverage:                   s.config.SemanticCoverage,
		ResolveSemanticInventorySelections: s.config.ResolveSemanticInventorySelections,
		ProvePopulationRoute:               provePopulationRoute,
		ReauthorizeFrameSourceChoice: func(ctx context.Context, project, explorerID, rowRoot, outputID, choiceID string, authorized ports.AuthorizedCapability, identity capability.ConstructionChoiceIdentity) (ports.FrameSourceFamilyCandidate, []capability.ConstructionRouteStep, error) {
			family, route, err := s.constructionService.ReauthorizeFrameSourceChoice(ctx, project, explorerID, rowRoot, outputID, choiceID, authorized, identity)
			return family, route, err
		},
	})
}
