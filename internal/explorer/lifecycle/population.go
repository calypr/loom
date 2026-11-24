package lifecycle

import (
	"context"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/authoringcore"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
)

func (s *Service) resolveWorkspacePopulations(ctx context.Context, project string, workspace ws.Workspace, snapshot capability.Snapshot, scopeDigest string) (explorercompilation.ResolvedInputs, error) {
	return authoringcore.ResolveWorkspacePopulations(ctx, project, workspace, snapshot, scopeDigest, s.config.SelectionStore)
}
