package explorer

import (
	"encoding/json"
	"testing"
)

func TestExplorerPersistsDurableV2DraftState(t *testing.T) {
	want := Explorer{Project: "program-project", ExplorerID: "default", DraftConfig: json.RawMessage(`{"apiVersion":"loom.calypr.org/explorer-authoring/v2"}`), DraftVersion: 3, DraftDigest: "sha256:intent"}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Explorer
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.DraftVersion != want.DraftVersion || got.DraftDigest != want.DraftDigest || string(got.DraftConfig) != string(want.DraftConfig) {
		t.Fatalf("draft state did not round trip: %#v", got)
	}
}
