package semanticbrowse

import (
	"context"

	"github.com/calypr/loom/internal/explorer/authoringcore"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/semantic"
	semanticarango "github.com/calypr/loom/internal/semantic/arango"
)

type Service struct {
	core                         *authoringcore.Core
	capability                   ports.CapabilityResolver
	semanticInventory            func(context.Context, semantic.PageOptions) (semantic.Page, error)
	semanticCoverage             func(context.Context, semanticarango.SemanticCoverageLookupOptions) (semanticarango.SemanticCoverageLookupResult, error)
	resolveSelections            func(context.Context, semantic.ResolveOptions) (semantic.ResolveResult, error)
	provePopulationRoute         func(context.Context, ports.AuthorizedCapability, string, string, []capability.ConstructionRouteStep) bool
	reauthorizeFrameSourceChoice func(context.Context, string, string, string, string, string, ports.AuthorizedCapability, capability.ConstructionChoiceIdentity) (FrameSourceFamilyCandidate, []capability.ConstructionRouteStep, error)
}
