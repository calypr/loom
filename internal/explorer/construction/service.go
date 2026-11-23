package construction

import (
	"context"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func (s *Service) PrepareConstructionChoice(ctx context.Context, project, explorerID string, authorized ports.AuthorizedCapability, identities []capability.ConstructionChoiceIdentity, workspace authoringv2.Workspace, catalogSnapshot authoringv2.CatalogSnapshot, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	return s.prepareConstructionChoice(ctx, project, explorerID, authorized, identities, workspace, catalogSnapshot, commands)
}

func (s *Service) PrepareConstructionProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, snapshot capability.Snapshot, current authoringv2.Workspace, commands []authoringv2.Command, sinkFactory PreviewSinkFactory) ([]authoringv2.Command, *explorer.CompilationReceipt, *AcceptedConstruction, error) {
	return s.prepareConstructionProposal(ctx, project, explorerID, request, snapshot, current, commands, sinkFactory)
}

func (s *Service) CurrentWorkspace(ctx context.Context, project, explorerID string) (authoringv2.Workspace, error) {
	return s.currentWorkspace(ctx, project, explorerID)
}
