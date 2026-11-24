package population

import (
	"fmt"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	explorerselection "github.com/calypr/loom/internal/explorer/selection"
)

type PopulationMemberRemovalProposalRequest struct {
	Project              string
	ExplorerID           string
	SnapshotToken        string
	ExpectedDraftVersion int64
	ExpectedDraftDigest  string
	OutputID             string
	BaseSelectionID      string
	RemovedMember        explorerselection.ResourceRef
	Limit                int
}

func (r PopulationMemberRemovalProposalRequest) Validate() error {
	for name, value := range map[string]string{
		"project": r.Project, "explorerId": r.ExplorerID, "snapshotToken": r.SnapshotToken,
		"expectedDraftDigest": r.ExpectedDraftDigest, "outputId": r.OutputID,
		"baseSelectionId": r.BaseSelectionID,
	} {
		if err := support.RequireExactIdentity(value, name); err != nil {
			return err
		}
	}
	if r.ExpectedDraftVersion < 1 {
		return fmt.Errorf("expectedDraftVersion must be positive")
	}
	if r.RemovedMember != r.RemovedMember.Canonical() {
		return fmt.Errorf("removedMember must contain exact canonical scope and identity fields")
	}
	if err := r.RemovedMember.Validate(r.Project, "", ""); err != nil {
		return fmt.Errorf("removedMember is invalid: %w", err)
	}
	if r.Limit < 0 || r.Limit > dataframeexecution.MaxPreviewLimit {
		return fmt.Errorf("limit must be between 1 and %d", dataframeexecution.MaxPreviewLimit)
	}
	return nil
}

type PopulationMemberRemovalProposalResponse struct {
	ProposalID               string                               `json:"proposalId,omitempty"`
	OutputID                 string                               `json:"outputId"`
	SnapshotToken            string                               `json:"snapshotToken"`
	DraftVersion             int64                                `json:"draftVersion"`
	DraftDigest              string                               `json:"draftDigest"`
	BaseDocumentDigest       string                               `json:"baseDocumentDigest"`
	CandidateWorkspaceDigest string                               `json:"candidateWorkspaceDigest"`
	BaseSelection            *explorerselection.SelectionRevision `json:"baseSelection,omitempty"`
	CandidateSelection       *explorerselection.SelectionRevision `json:"candidateSelection,omitempty"`
	RemovedMember            explorerselection.ResourceRef        `json:"removedMember"`
	PreviewStatus            string                               `json:"previewStatus"`
}
