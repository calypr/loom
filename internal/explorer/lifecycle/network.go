package lifecycle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/authoringnetwork"
	"github.com/calypr/loom/internal/explorer/proposal"
)

type AuthoringNetworkRequest struct {
	Project              string `json:"-"`
	ExplorerID           string `json:"-"`
	OutputID             string `json:"outputId"`
	SnapshotToken        string `json:"snapshotToken"`
	ExpectedDraftVersion int64  `json:"expectedDraftVersion"`
	ExpectedDraftDigest  string `json:"expectedDraftDigest"`
}

type AuthoringNetworkResponse struct {
	SnapshotToken string                            `json:"snapshotToken"`
	DraftVersion  int64                             `json:"draftVersion"`
	DraftDigest   string                            `json:"draftDigest"`
	RowIdentity   ws.RowIdentityPlan                `json:"rowIdentity"`
	Network       authoringnetwork.AuthoringNetwork `json:"network"`
}

func rowIdentityForNetwork(document ws.Document, network authoringnetwork.AuthoringNetwork) ws.RowIdentityPlan {
	return proposal.RowIdentityForNetwork(document, network)
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
