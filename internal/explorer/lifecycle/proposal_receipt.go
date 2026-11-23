package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/proposal"
)

func (s *Service) verifyProposalReceipt(_ context.Context, stage string, receipt *explorer.CompilationReceipt, project, explorerID, snapshotToken string, snapshot capability.Snapshot, expected *authoringv2.Workspace) (authoringv2.Workspace, error) {
	return proposal.VerifyReceipt(stage, receipt, project, explorerID, snapshotToken, snapshot, expected, func() authoringv2.CatalogSnapshot {
		return s.catalog(snapshot, explorerID)
	})
}
