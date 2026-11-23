package construction

import (
	"context"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func (s *Service) PrepareConstructionChoice(ctx context.Context, project, explorerID string, authorized ports.AuthorizedCapability, identities []capability.ConstructionChoiceIdentity, workspace ws.Workspace, catalogSnapshot ws.CatalogSnapshot, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	return s.prepareConstructionChoice(ctx, project, explorerID, authorized, identities, workspace, catalogSnapshot, commands)
}

func (s *Service) PrepareConstructionProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, snapshot capability.Snapshot, current ws.Workspace, commands []authoringv2.Command, sinkFactory PreviewSinkFactory) ([]authoringv2.Command, *receiptmodel.CompilationReceipt, *AcceptedConstruction, error) {
	return s.prepareConstructionProposal(ctx, project, explorerID, request, snapshot, current, commands, sinkFactory)
}

func (s *Service) CurrentWorkspace(ctx context.Context, project, explorerID string) (ws.Workspace, error) {
	return s.config.Core.CurrentWorkspace(ctx, "construction-choices", project, explorerID)
}
