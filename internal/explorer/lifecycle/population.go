package lifecycle

import (
	"context"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
)

func (s *Service) resolveWorkspacePopulations(ctx context.Context, project string, workspace ws.Workspace, snapshot capability.Snapshot, scopeDigest string) (explorercompilation.ResolvedInputs, error) {
	return s.populationService.ResolveWorkspacePopulations(ctx, project, workspace, snapshot, scopeDigest)
}
