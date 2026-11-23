package lifecycle

import (
	"context"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/proposal"
)

func (s *Service) verifyProposalReceipt(_ context.Context, stage string, receipt *receiptmodel.CompilationReceipt, project, explorerID, snapshotToken string, snapshot capability.Snapshot, expected *ws.Workspace) (ws.Workspace, error) {
	return proposal.VerifyReceipt(stage, receipt, project, explorerID, snapshotToken, snapshot, expected, func() ws.CatalogSnapshot {
		return s.catalog(snapshot, explorerID)
	})
}
