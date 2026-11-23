package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/population"
)

func (s *Service) resolveWorkspacePopulations(ctx context.Context, project string, workspace authoringv2.Workspace, snapshot capability.Snapshot, scopeDigest string) (explorercompilation.ResolvedInputs, error) {
	return population.ResolveWorkspacePopulations(ctx, project, workspace, snapshot, scopeDigest, s.store)
}
