package lifecycle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

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
