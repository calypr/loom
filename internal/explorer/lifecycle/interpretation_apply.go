package lifecycle

import (
	"context"
	"fmt"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

// validateInterpretationCandidateCommands is the proof boundary for the one
// receipt-backed mutation. The reducer has already produced the proposed
// workspace, so a successful return means SaveDraft is the only remaining
// state change in ApplyWorkspaceCommandsChecked.
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
		if err := s.validateReceiptRoute(receipt, project, explorerID); err != nil {
			return conflict("commands", "INVALID_INTERPRETATION_CANDIDATE_RECEIPT", "the candidate receipt is not usable for this Explorer", nil, err)
		}
		if err := validateCandidateReceiptIdentity(receipt, project, explorerID, request.SnapshotToken, snapshot); err != nil {
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
		if err := verifyCandidateWorkspacePin(candidateWorkspace, command.OutputID, command.Column, command.InterpretationCandidate.RevisionID); err != nil {
			return conflict("commands", "INTERPRETATION_CANDIDATE_MISMATCH", "the candidate receipt does not contain the requested exact pin", nil, err)
		}
		if err := verifyCandidateReceiptInterpretation(receipt, command.OutputID, command.Column, command.InterpretationCandidate.RevisionID); err != nil {
			return conflict("commands", "INTERPRETATION_CANDIDATE_MISMATCH", "the candidate receipt does not freeze the requested exact revision", nil, err)
		}
	}
	if candidateCount == 0 {
		return nil
	}
	return nil
}

func validateCandidateReceiptIdentity(receipt *explorer.CompilationReceipt, project, explorerID, snapshotToken string, snapshot capability.Snapshot) error {
	if projectid.Canonical(receipt.Project) != projectid.Canonical(project) || receipt.ExplorerID != explorerID {
		return fmt.Errorf("receipt project or explorer identity changed")
	}
	if receipt.SnapshotToken != snapshotToken || snapshot.Token != snapshotToken || snapshot.ValidateToken(snapshotToken) != nil || projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(project) {
		return fmt.Errorf("receipt snapshot token changed")
	}
	if receipt.SourceGeneration != snapshot.Identity.Generation {
		return fmt.Errorf("receipt source generation changed")
	}
	if receipt.ShapeDigest != snapshot.Identity.ShapeDigest {
		return fmt.Errorf("receipt capability shape changed")
	}
	if receipt.AuthorizationScopeDigest != snapshot.Identity.AuthorizationScopeDigest {
		return fmt.Errorf("receipt authorization scope changed")
	}
	if receipt.CapabilitySchemaDigest != snapshot.Identity.SchemaDigest {
		return fmt.Errorf("receipt capability schema changed")
	}
	return nil
}

func verifyCandidateWorkspacePin(workspace authoringv2.Workspace, outputID, column, revisionID string) error {
	for _, document := range workspace.Documents {
		if document.Output.ID != outputID {
			continue
		}
		for _, value := range document.Columns {
			if value.Column != column {
				continue
			}
			if value.Interpretation == nil || value.Interpretation.Kind != authoringv2.FeatureInterpretationPinned || value.Interpretation.Pinned == nil || value.Interpretation.Pinned.RevisionID != revisionID {
				return fmt.Errorf("target workspace column is not pinned to revision %q", revisionID)
			}
			return nil
		}
		return fmt.Errorf("target column %q was not found", column)
	}
	return fmt.Errorf("target output %q was not found", outputID)
}

func verifyCandidateReceiptInterpretation(receipt *explorer.CompilationReceipt, outputID, column, revisionID string) error {
	found := 0
	for _, value := range receipt.ResolvedInterpretations {
		if value.OutputID != outputID || value.Column != column {
			continue
		}
		found++
		if string(value.Revision.ID) != revisionID {
			return fmt.Errorf("target resolved interpretation uses revision %q, want %q", value.Revision.ID, revisionID)
		}
	}
	if found != 1 {
		return fmt.Errorf("candidate receipt contains %d target resolved interpretations, want one", found)
	}
	return nil
}
