package lifecycle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

// These request decoders are used by the generated public DTO aliases. Route
// ownership is server-owned; a client may send only the bounded read/proposal
// intent declared in OpenAPI, never project metadata or a candidate workspace.
func (r *SourceRelationshipProposalRequest) UnmarshalJSON(raw []byte) error {
	var wire struct {
		SnapshotToken        string                                  `json:"snapshotToken"`
		ExpectedDraftVersion int64                                   `json:"expectedDraftVersion"`
		ExpectedDraftDigest  string                                  `json:"expectedDraftDigest"`
		OutputID             string                                  `json:"outputId"`
		Change               *authoringv2.ImportedRelationshipChange `json:"change,omitempty"`
		SourceStep           *authoringv2.ImportedSourceStepChange   `json:"sourceStep,omitempty"`
		Limit                *int                                    `json:"limit,omitempty"`
	}
	if err := decodeClosedSourceIntent(raw, &wire); err != nil {
		return err
	}
	var supplied map[string]json.RawMessage
	if err := json.Unmarshal(raw, &supplied); err != nil {
		return err
	}
	for _, name := range []string{"change", "sourceStep"} {
		if value, ok := supplied[name]; ok && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("%s must be a typed object", name)
		}
	}
	if (wire.Change == nil) == (wire.SourceStep == nil) {
		return fmt.Errorf("exactly one change or sourceStep is required")
	}
	if value, ok := supplied["limit"]; ok && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return fmt.Errorf("limit must be a positive integer when provided")
	}
	if wire.Limit != nil && *wire.Limit < 1 {
		return fmt.Errorf("limit must be positive when provided")
	}
	*r = SourceRelationshipProposalRequest{SnapshotToken: wire.SnapshotToken, ExpectedDraftVersion: wire.ExpectedDraftVersion, ExpectedDraftDigest: wire.ExpectedDraftDigest, OutputID: wire.OutputID, SourceStep: wire.SourceStep}
	if wire.Limit != nil {
		r.Limit = *wire.Limit
	}
	if wire.Change != nil {
		r.Change = *wire.Change
	}
	return r.validateSourceSelection()
}

func (r *AuthoringNetworkRequest) UnmarshalJSON(raw []byte) error {
	var wire struct {
		SnapshotToken        string `json:"snapshotToken"`
		ExpectedDraftVersion int64  `json:"expectedDraftVersion"`
		ExpectedDraftDigest  string `json:"expectedDraftDigest"`
		OutputID             string `json:"outputId"`
	}
	if err := decodeClosedSourceIntent(raw, &wire); err != nil {
		return err
	}
	*r = AuthoringNetworkRequest{SnapshotToken: wire.SnapshotToken, ExpectedDraftVersion: wire.ExpectedDraftVersion, ExpectedDraftDigest: wire.ExpectedDraftDigest, OutputID: wire.OutputID}
	return nil
}

func decodeClosedSourceIntent(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return err
		}
		return fmt.Errorf("source intent must contain one JSON value")
	}
	return nil
}

func (r SourceRelationshipProposalRequest) hasRelationshipChange() bool {
	c := r.Change
	return c.Action != "" || c.OccurrenceID != "" || c.ParentOccurrenceID != "" || c.EdgeID != "" || c.MatchMode != ""
}
func (r SourceRelationshipProposalRequest) validateSourceSelection() error {
	if (r.SourceStep != nil) == r.hasRelationshipChange() {
		return fmt.Errorf("exactly one change or sourceStep is required")
	}
	if r.SourceStep != nil {
		return r.SourceStep.Validate()
	}
	return r.Change.Validate()
}
func (r SourceRelationshipProposalRequest) MarshalJSON() ([]byte, error) {
	var change *authoringv2.ImportedRelationshipChange
	if r.hasRelationshipChange() {
		copy := r.Change
		change = &copy
	}
	return json.Marshal(struct {
		SnapshotToken        string                                  `json:"snapshotToken"`
		ExpectedDraftVersion int64                                   `json:"expectedDraftVersion"`
		ExpectedDraftDigest  string                                  `json:"expectedDraftDigest"`
		OutputID             string                                  `json:"outputId"`
		Change               *authoringv2.ImportedRelationshipChange `json:"change,omitempty"`
		SourceStep           *authoringv2.ImportedSourceStepChange   `json:"sourceStep,omitempty"`
		Limit                int                                     `json:"limit,omitempty"`
	}{r.SnapshotToken, r.ExpectedDraftVersion, r.ExpectedDraftDigest, r.OutputID, change, r.SourceStep, r.Limit})
}
