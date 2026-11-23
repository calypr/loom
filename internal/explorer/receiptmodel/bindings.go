package receiptmodel

import (
	"fmt"
	"strings"

	explorerselection "github.com/calypr/loom/internal/explorer/selection"
)

type RowDefinitionProposalBinding struct {
	DraftVersion             int64  `json:"draftVersion"`
	DraftDigest              string `json:"draftDigest"`
	OutputID                 string `json:"outputId"`
	BaseDocumentDigest       string `json:"baseDocumentDigest"`
	CandidateWorkspaceDigest string `json:"candidateWorkspaceDigest"`
	SnapshotToken            string `json:"snapshotToken"`
}

func (b RowDefinitionProposalBinding) Validate(intentDigest, snapshotToken string) error {
	if err := validateDraftProposalFields("row-definition", b.DraftVersion, map[string]string{
		"draftDigest": b.DraftDigest, "outputId": b.OutputID, "baseDocumentDigest": b.BaseDocumentDigest,
		"candidateWorkspaceDigest": b.CandidateWorkspaceDigest, "snapshotToken": b.SnapshotToken,
	}); err != nil {
		return err
	}
	return validateDraftProposalDigests("row-definition", b.CandidateWorkspaceDigest, b.SnapshotToken, intentDigest, snapshotToken)
}

type TableShapeProposalBinding struct {
	DraftVersion             int64  `json:"draftVersion"`
	DraftDigest              string `json:"draftDigest"`
	OutputID                 string `json:"outputId"`
	BaseDocumentDigest       string `json:"baseDocumentDigest"`
	CandidateWorkspaceDigest string `json:"candidateWorkspaceDigest"`
	SnapshotToken            string `json:"snapshotToken"`
}

func (b TableShapeProposalBinding) Validate(intentDigest, snapshotToken string) error {
	if err := validateDraftProposalFields("table-shape", b.DraftVersion, map[string]string{
		"draftDigest": b.DraftDigest, "outputId": b.OutputID, "baseDocumentDigest": b.BaseDocumentDigest,
		"candidateWorkspaceDigest": b.CandidateWorkspaceDigest, "snapshotToken": b.SnapshotToken,
	}); err != nil {
		return err
	}
	return validateDraftProposalDigests("table-shape", b.CandidateWorkspaceDigest, b.SnapshotToken, intentDigest, snapshotToken)
}

func validateDraftProposalFields(kind string, draftVersion int64, fields map[string]string) error {
	if draftVersion < 1 {
		return fmt.Errorf("%s proposal draftVersion must be positive", kind)
	}
	for name, value := range fields {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("%s proposal %s must be an exact non-empty value", kind, name)
		}
	}
	return nil
}

func validateDraftProposalDigests(kind, candidateWorkspaceDigest, bindingSnapshotToken, intentDigest, snapshotToken string) error {
	if candidateWorkspaceDigest != intentDigest {
		return fmt.Errorf("%s proposal candidate workspace digest does not match receipt intent", kind)
	}
	if bindingSnapshotToken != snapshotToken {
		return fmt.Errorf("%s proposal snapshot token does not match receipt snapshot", kind)
	}
	return nil
}

type PopulationMemberRemovalProposalBinding struct {
	DraftVersion                 int64                         `json:"draftVersion"`
	DraftDigest                  string                        `json:"draftDigest"`
	OutputID                     string                        `json:"outputId"`
	BaseDocumentDigest           string                        `json:"baseDocumentDigest"`
	BaseSelectionRevisionID      string                        `json:"baseSelectionRevisionId"`
	BaseMembershipDigest         string                        `json:"baseMembershipDigest"`
	BaseMemberCount              int64                         `json:"baseMemberCount"`
	CandidateSelectionRevisionID string                        `json:"candidateSelectionRevisionId"`
	CandidateMembershipDigest    string                        `json:"candidateMembershipDigest"`
	CandidateMemberCount         int64                         `json:"candidateMemberCount"`
	RemovedMember                explorerselection.ResourceRef `json:"removedMember"`
	RouteChoiceID                string                        `json:"routeChoiceId"`
	CandidateWorkspaceDigest     string                        `json:"candidateWorkspaceDigest"`
	SnapshotToken                string                        `json:"snapshotToken"`
	PreviewLimit                 int                           `json:"previewLimit"`
}

func (b PopulationMemberRemovalProposalBinding) Validate(intentDigest, snapshotToken, project, generation string) error {
	if err := validateDraftProposalFields("population member", b.DraftVersion, map[string]string{
		"draftDigest": b.DraftDigest, "outputId": b.OutputID, "baseDocumentDigest": b.BaseDocumentDigest,
		"baseSelectionRevisionId": b.BaseSelectionRevisionID, "baseMembershipDigest": b.BaseMembershipDigest,
		"candidateSelectionRevisionId": b.CandidateSelectionRevisionID, "candidateMembershipDigest": b.CandidateMembershipDigest,
		"routeChoiceId": b.RouteChoiceID, "candidateWorkspaceDigest": b.CandidateWorkspaceDigest, "snapshotToken": b.SnapshotToken,
	}); err != nil {
		return err
	}
	if b.BaseSelectionRevisionID == b.CandidateSelectionRevisionID || b.BaseMembershipDigest == b.CandidateMembershipDigest {
		return fmt.Errorf("population member proposal must bind distinct immutable selections")
	}
	if b.BaseMemberCount < 1 || b.CandidateMemberCount != b.BaseMemberCount-1 {
		return fmt.Errorf("population member proposal candidate must remove exactly one base member")
	}
	if err := b.RemovedMember.Validate(project, generation, b.RemovedMember.ResourceType); err != nil {
		return fmt.Errorf("population member proposal removed member is outside the receipt scope: %w", err)
	}
	if err := validateDraftProposalDigests("population member", b.CandidateWorkspaceDigest, b.SnapshotToken, intentDigest, snapshotToken); err != nil {
		return err
	}
	if b.PreviewLimit < 1 {
		return fmt.Errorf("population member proposal previewLimit must be positive")
	}
	return nil
}

type ConstructionProposalBinding struct {
	DraftVersion             int64    `json:"draftVersion"`
	DraftDigest              string   `json:"draftDigest"`
	OutputID                 string   `json:"outputId"`
	ChangedStepID            string   `json:"changedStepId"`
	RemoveStepIDs            []string `json:"removeStepIds,omitempty"`
	BaseDocumentDigest       string   `json:"baseDocumentDigest"`
	CandidateWorkspaceDigest string   `json:"candidateWorkspaceDigest"`
	SnapshotToken            string   `json:"snapshotToken"`
	PreviewLimit             int      `json:"previewLimit"`
}

func (b ConstructionProposalBinding) Validate(intentDigest, snapshotToken string) error {
	if err := validateDraftProposalFields("construction", b.DraftVersion, map[string]string{
		"draftDigest": b.DraftDigest, "outputId": b.OutputID, "baseDocumentDigest": b.BaseDocumentDigest,
		"candidateWorkspaceDigest": b.CandidateWorkspaceDigest, "snapshotToken": b.SnapshotToken,
	}); err != nil {
		return err
	}
	if b.ChangedStepID == "" {
		if len(b.RemoveStepIDs) == 0 {
			return fmt.Errorf("construction proposal requires changedStepId or removeStepIds")
		}
	} else if strings.TrimSpace(b.ChangedStepID) != b.ChangedStepID {
		return fmt.Errorf("construction proposal changedStepId must be exact")
	}
	if err := validateDraftProposalDigests("construction", b.CandidateWorkspaceDigest, b.SnapshotToken, intentDigest, snapshotToken); err != nil {
		return err
	}
	if b.PreviewLimit < 1 {
		return fmt.Errorf("construction proposal previewLimit must be positive")
	}
	seenRemovals := make(map[string]struct{}, len(b.RemoveStepIDs))
	for _, id := range b.RemoveStepIDs {
		if strings.TrimSpace(id) == "" || id != strings.TrimSpace(id) {
			return fmt.Errorf("construction proposal removeStepIds must contain exact non-empty values")
		}
		if _, exists := seenRemovals[id]; exists {
			return fmt.Errorf("construction proposal removeStepIds contains duplicate %q", id)
		}
		if id == b.ChangedStepID {
			return fmt.Errorf("construction proposal changedStepId cannot also be removed")
		}
		seenRemovals[id] = struct{}{}
	}
	return nil
}

type SourceRelationshipProposalBinding RowDefinitionProposalBinding

func (b SourceRelationshipProposalBinding) Validate(intentDigest, snapshotToken string) error {
	return RowDefinitionProposalBinding(b).Validate(intentDigest, snapshotToken)
}

type ColumnPolicyProposalBinding struct {
	RowDefinitionProposalBinding
	ColumnID       string `json:"columnId"`
	ProjectionMode string `json:"projectionMode"`
}

func (b ColumnPolicyProposalBinding) Validate(intentDigest, snapshotToken string) error {
	if err := b.RowDefinitionProposalBinding.Validate(intentDigest, snapshotToken); err != nil {
		return err
	}
	if strings.TrimSpace(b.ColumnID) == "" || b.ColumnID != strings.TrimSpace(b.ColumnID) {
		return fmt.Errorf("column policy proposal requires an exact columnId")
	}
	switch b.ProjectionMode {
	case "VALUE", "INDEXED", "FIRST", "ALL", "DISTINCT":
	default:
		return fmt.Errorf("column policy proposal projectionMode is unsupported")
	}
	return nil
}
