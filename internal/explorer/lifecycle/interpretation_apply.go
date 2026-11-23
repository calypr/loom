package lifecycle

import (
	"context"
	"fmt"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
)

func (s *Service) validateInterpretationCandidateCommands(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, proposed authoringv2.Workspace, snapshot capability.Snapshot) error {
	candidateCount := 0
	for _, command := range request.Commands {
		if command.Type != authoringv2.CommandApplyInterpretationCandidate {
			continue
		}
		candidateCount++
		if len(request.Commands) != 1 {
			return fmt.Errorf("APPLY_INTERPRETATION_CANDIDATE must be the only command in its batch")
		}
		if command.InterpretationCandidate == nil {
			return fmt.Errorf("APPLY_INTERPRETATION_CANDIDATE payload is required")
		}
		receipt, err := s.lookupReceipt(ctx, project, explorerID, command.InterpretationCandidate.CandidateReceiptID)
		if err != nil {
			return err
		}
		if receipt == nil || receipt.ID != command.InterpretationCandidate.CandidateReceiptID {
			return conflict("commands", "INVALID_INTERPRETATION_CANDIDATE_RECEIPT", "the candidate receipt ID does not match the command", nil, nil)
		}
		if err := s.validateExecutableReceiptRoute(receipt, project, explorerID); err != nil {
			return conflict("commands", "INVALID_INTERPRETATION_CANDIDATE_RECEIPT", "the candidate receipt is not usable for this Explorer", nil, err)
		}
		if err := receiptmodel.ValidateCandidateReceiptIdentity(receipt, project, explorerID, request.SnapshotToken, snapshot); err != nil {
			return conflict("commands", "INVALID_INTERPRETATION_CANDIDATE_RECEIPT", "the candidate receipt capability identity does not match the apply request", nil, err)
		}
		proposedDigest, err := proposed.Digest()
		if err != nil {
			return fmt.Errorf("digest proposed interpretation workspace: %w", err)
		}
		if receipt.IntentDigest != proposedDigest {
			return conflict("commands", "INTERPRETATION_CANDIDATE_MISMATCH", "the candidate receipt was not compiled from this exact command result", nil, nil)
		}
		candidateWorkspace, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
		if err != nil {
			return conflict("commands", "INVALID_INTERPRETATION_CANDIDATE_RECEIPT", "the candidate receipt normalized workspace is invalid", nil, err)
		}
		if candidateDigest, digestErr := candidateWorkspace.Digest(); digestErr != nil || candidateDigest != receipt.IntentDigest {
			return conflict("commands", "INTERPRETATION_CANDIDATE_MISMATCH", "the candidate receipt normalized workspace digest does not match its intent digest", nil, digestErr)
		}
		if err := receiptmodel.VerifyCandidateWorkspacePin(candidateWorkspace, command.OutputID, command.Column, command.InterpretationCandidate.RevisionID); err != nil {
			return conflict("commands", "INTERPRETATION_CANDIDATE_MISMATCH", "the candidate receipt does not contain the requested exact pin", nil, err)
		}
		if err := receiptmodel.VerifyCandidateReceiptInterpretation(receipt, command.OutputID, command.Column, command.InterpretationCandidate.RevisionID); err != nil {
			return conflict("commands", "INTERPRETATION_CANDIDATE_MISMATCH", "the candidate receipt does not freeze the requested exact revision", nil, err)
		}
	}
	if candidateCount == 0 {
		return nil
	}
	return nil
}
