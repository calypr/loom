package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer/authoringcore"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/explorer/construction"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func (s *Service) prepareConstructionProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, snapshot capability.Snapshot, current ws.Workspace, commands []authoringv2.Command, sinkFactory authoringcore.PreviewSinkFactory) ([]authoringv2.Command, *receiptmodel.CompilationReceipt, *construction.AcceptedConstruction, error) {
	prepared, receipt, accepted, err := s.constructionService.PrepareConstructionProposal(ctx, project, explorerID, request, snapshot, current, commands, sinkFactory)
	return prepared, receipt, accepted, err
}
