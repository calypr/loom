package lifecycle

import (
	"context"

	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/semantic"
)

type fieldBrowse struct {
	capability        ports.CapabilityResolver
	semanticCoverage  func(context.Context, catalogarango.SemanticCoverageLookupOptions) (catalogarango.SemanticCoverageLookupResult, error)
	resolveSelections func(context.Context, semantic.ResolveOptions) (semantic.ResolveResult, error)
	getOwner          func(context.Context, string, string) (*explorer.Explorer, error)
	currentWorkspace  func(context.Context, string, string) (authoringv2.Workspace, error)
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
