package proposal

import (
	"bytes"
	"encoding/json"
	"fmt"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
)

type ColumnPolicyProposalRequest struct {
	Project              string `json:"-"`
	ExplorerID           string `json:"-"`
	SnapshotToken        string `json:"snapshotToken"`
	ExpectedDraftVersion int64  `json:"expectedDraftVersion"`
	ExpectedDraftDigest  string `json:"expectedDraftDigest"`
	OutputID             string `json:"outputId"`
	ColumnID             string `json:"columnId"`
	ProjectionMode       string `json:"projectionMode"`
	Limit                int    `json:"limit,omitempty"`
}

func (r *ColumnPolicyProposalRequest) UnmarshalJSON(raw []byte) error {
	var wire struct {
		SnapshotToken        string `json:"snapshotToken"`
		ExpectedDraftVersion int64  `json:"expectedDraftVersion"`
		ExpectedDraftDigest  string `json:"expectedDraftDigest"`
		OutputID             string `json:"outputId"`
		ColumnID             string `json:"columnId"`
		ProjectionMode       string `json:"projectionMode"`
		Limit                *int   `json:"limit,omitempty"`
	}
	if err := DecodeClosedSourceIntent(raw, &wire); err != nil {
		return err
	}
	var supplied map[string]json.RawMessage
	if err := json.Unmarshal(raw, &supplied); err != nil {
		return err
	}
	if value, ok := supplied["limit"]; ok && (bytes.Equal(bytes.TrimSpace(value), []byte("null")) || wire.Limit == nil || *wire.Limit < 1) {
		return fmt.Errorf("limit must be positive when provided")
	}
	*r = ColumnPolicyProposalRequest{SnapshotToken: wire.SnapshotToken, ExpectedDraftVersion: wire.ExpectedDraftVersion, ExpectedDraftDigest: wire.ExpectedDraftDigest, OutputID: wire.OutputID, ColumnID: wire.ColumnID, ProjectionMode: wire.ProjectionMode}
	if wire.Limit != nil {
		r.Limit = *wire.Limit
	}
	return nil
}

func (r ColumnPolicyProposalRequest) validate() error {
	for name, value := range map[string]string{"project": r.Project, "explorerId": r.ExplorerID, "snapshotToken": r.SnapshotToken, "expectedDraftDigest": r.ExpectedDraftDigest, "outputId": r.OutputID, "columnId": r.ColumnID} {
		if err := support.RequireExactIdentity(value, name); err != nil {
			return err
		}
	}
	if r.ExpectedDraftVersion < 1 || r.Limit < 0 || r.Limit > dataframeexecution.MaxPreviewLimit {
		return fmt.Errorf("positive draft version and bounded limit are required")
	}
	switch r.ProjectionMode {
	case "VALUE", "INDEXED", "FIRST", "ALL", "DISTINCT":
	default:
		return fmt.Errorf("projectionMode is unsupported")
	}
	return nil
}

type ColumnPolicyProposal SourceRelationshipProposal
