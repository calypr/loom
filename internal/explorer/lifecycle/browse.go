package lifecycle

import (
	"context"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/semantic"
	semanticarango "github.com/calypr/loom/internal/semantic/arango"
)

type fieldBrowse struct {
	capability        ports.CapabilityResolver
	semanticCoverage  func(context.Context, semanticarango.SemanticCoverageLookupOptions) (semanticarango.SemanticCoverageLookupResult, error)
	resolveSelections func(context.Context, semantic.ResolveOptions) (semantic.ResolveResult, error)
	getOwner          func(context.Context, string, string) (*explorer.Explorer, error)
	currentWorkspace  func(context.Context, string, string) (ws.Workspace, error)
	storeConfigured   bool
}

func (s *Service) fieldBrowse() *fieldBrowse {
	return &fieldBrowse{
		capability:        s.config.Capability,
		semanticCoverage:  s.config.SemanticCoverage,
		resolveSelections: s.config.ResolveSemanticInventorySelections,
		getOwner:          s.getOwner,
		currentWorkspace:  s.currentWorkspace,
		storeConfigured:   s.store != nil,
	}
}
