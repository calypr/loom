package lifecycle

import (
	"context"
	"errors"
	"fmt"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/lifecycle/support"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
)

func (s *Service) validateInterpretationCandidateCommands(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, proposed ws.Workspace, snapshot capability.Snapshot) error {
	candidateCount := 0
	for _, command := range request.Commands {
		if command.Type != authoringv2.CommandApplyInterpretationCandidate {
			continue
		}
		candidateCount++
		if len(request.Commands) != 1 {
			return fmt.Errorf("APPLY_INTERPRETATION_CANDIDATE must be the only command in its batch")
		}
		if command.InterpretationCandidate() == nil {
			return fmt.Errorf("APPLY_INTERPRETATION_CANDIDATE payload is required")
		}
		receipt, err := s.core.LookupReceipt(ctx, project, explorerID, command.InterpretationCandidate().CandidateReceiptID)
		if err != nil {
			return err
		}
		if receipt == nil || receipt.ID != command.InterpretationCandidate().CandidateReceiptID {
			return support.Conflict("commands", "INVALID_INTERPRETATION_CANDIDATE_RECEIPT", "the candidate receipt ID does not match the command", nil, nil)
		}
		if err := s.core.ValidateExecutableReceiptRoute(receipt, project, explorerID); err != nil {
			return support.Conflict("commands", "INVALID_INTERPRETATION_CANDIDATE_RECEIPT", "the candidate receipt is not usable for this Explorer", nil, err)
		}
		if err := receiptmodel.ValidateInterpretationCandidate(receipt, project, explorerID, request.SnapshotToken, snapshot, proposed, command); err != nil {
			if errors.Is(err, receiptmodel.ErrInvalidCandidateReceipt) {
				return support.Conflict("commands", "INVALID_INTERPRETATION_CANDIDATE_RECEIPT", "the candidate receipt does not match the apply request", nil, err)
			}
			return support.Conflict("commands", "INTERPRETATION_CANDIDATE_MISMATCH", "the candidate receipt does not match the apply request", nil, err)
		}

	}
	if candidateCount == 0 {
		return nil
	}
	return nil
}
