package receiptmodel

import (
	"fmt"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
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
