package receiptmodel

import (
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

var (
	ErrInvalidCandidateReceipt         = errors.New("invalid interpretation candidate receipt")
	ErrInterpretationCandidateMismatch = errors.New("interpretation candidate receipt does not match the apply request")
)

func ValidateCandidateReceiptIdentity(receipt *CompilationReceipt, project, explorerID, snapshotToken string, snapshot capability.Snapshot) error {
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

func VerifyCandidateWorkspacePin(workspace authoringv2.Workspace, outputID, column, revisionID string) error {
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

func VerifyCandidateReceiptInterpretation(receipt *CompilationReceipt, outputID, column, revisionID string) error {
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

func ValidateInterpretationCandidate(receipt *CompilationReceipt, project, explorerID, snapshotToken string, snapshot capability.Snapshot, proposed authoringv2.Workspace, command authoringv2.Command) error {
	if command.InterpretationCandidate() == nil {
		return fmt.Errorf("APPLY_INTERPRETATION_CANDIDATE payload is required")
	}
	if receipt == nil || receipt.ID != command.InterpretationCandidate().CandidateReceiptID {
		return fmt.Errorf("%w: the candidate receipt ID does not match the command", ErrInvalidCandidateReceipt)
	}
	if err := ValidateCandidateReceiptIdentity(receipt, project, explorerID, snapshotToken, snapshot); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCandidateReceipt, err)
	}
	proposedDigest, err := proposed.Digest()
	if err != nil {
		return fmt.Errorf("digest proposed interpretation workspace: %w", err)
	}
	if receipt.IntentDigest != proposedDigest {
		return fmt.Errorf("%w: the candidate receipt was not compiled from this exact command result", ErrInterpretationCandidateMismatch)
	}
	candidateWorkspace, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
	if err != nil {
		return fmt.Errorf("%w: the candidate receipt normalized workspace is invalid: %v", ErrInvalidCandidateReceipt, err)
	}
	if candidateDigest, digestErr := candidateWorkspace.Digest(); digestErr != nil || candidateDigest != receipt.IntentDigest {
		return fmt.Errorf("%w: the candidate receipt normalized workspace digest does not match its intent digest", ErrInvalidCandidateReceipt)
	}
	if err := VerifyCandidateWorkspacePin(candidateWorkspace, command.OutputID, command.Column(), command.InterpretationCandidate().RevisionID); err != nil {
		return fmt.Errorf("%w: the candidate receipt does not contain the requested exact pin: %v", ErrInterpretationCandidateMismatch, err)
	}
	if err := VerifyCandidateReceiptInterpretation(receipt, command.OutputID, command.Column(), command.InterpretationCandidate().RevisionID); err != nil {
		return fmt.Errorf("%w: the candidate receipt does not freeze the requested exact revision: %v", ErrInterpretationCandidateMismatch, err)
	}
	return nil
}
