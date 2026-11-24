package semanticbrowse

import (
	"context"

	"github.com/calypr/loom/internal/explorer/authoringcore"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/semantic"
	semanticarango "github.com/calypr/loom/internal/semantic/arango"
)

type Config struct {
	Core                               *authoringcore.Core
	Capability                         ports.CapabilityResolver
	SemanticInventory                  func(context.Context, semantic.PageOptions) (semantic.Page, error)
	SemanticCoverage                   func(context.Context, semanticarango.SemanticCoverageLookupOptions) (semanticarango.SemanticCoverageLookupResult, error)
	ResolveSemanticInventorySelections func(context.Context, semantic.ResolveOptions) (semantic.ResolveResult, error)
	ProvePopulationRoute               func(context.Context, ports.AuthorizedCapability, string, string, []capability.ConstructionRouteStep) bool
	ReauthorizeFrameSourceChoice       func(context.Context, string, string, string, string, string, ports.AuthorizedCapability, capability.ConstructionChoiceIdentity) (ports.FrameSourceFamilyCandidate, []capability.ConstructionRouteStep, error)
}

func NewService(config Config) *Service {
	return &Service{
		core:                         config.Core,
		capability:                   config.Capability,
		semanticInventory:            config.SemanticInventory,
		semanticCoverage:             config.SemanticCoverage,
		resolveSelections:            config.ResolveSemanticInventorySelections,
		reauthorizeFrameSourceChoice: config.ReauthorizeFrameSourceChoice,
		provePopulationRoute:         config.ProvePopulationRoute,
	}
}
