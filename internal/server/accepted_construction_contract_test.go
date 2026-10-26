package server

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
)

func TestAcceptedConstructionSharedContractFixture(t *testing.T) {
	fixture, err := os.ReadFile("../../testdata/openapi/accepted-construction-response.json")
	if err != nil {
		t.Fatal(err)
	}
	var response loomapi.ApplyCommandsResponse
	if err := decodeStrict(fixture, &response); err != nil {
		t.Fatalf("strictly decode accepted construction fixture: %v", err)
	}
	accepted := response.AcceptedConstruction
	if accepted == nil {
		t.Fatal("fixture omitted acceptedConstruction")
	}
	if accepted.Receipt.IntentDigest != response.DraftDigest || accepted.Preview.ReceiptId != accepted.Receipt.ReceiptId {
		t.Fatalf("accepted identities do not match the saved draft: accepted=%#v draftDigest=%q", accepted, response.DraftDigest)
	}
	if accepted.PreviewLimit < 1 || accepted.PreviewLimit > 1000 || len(accepted.Preview.Rows) != 1 || accepted.Preview.Rows[0]["patient_id"] != "p1" {
		t.Fatalf("accepted preview shape is invalid: %#v", accepted)
	}
	if len(accepted.Preview.RowSources) != len(accepted.Preview.Rows) || accepted.Preview.RowLineageCapability.Status != loomapi.RowLineageCapabilityStatusAVAILABLE {
		t.Fatalf("accepted preview row metadata is incomplete: %#v", accepted.Preview)
	}
	receiptJSON, err := json.Marshal(accepted.Receipt)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(receiptJSON, []byte(`"builder"`)) || bytes.Contains(receiptJSON, []byte(`"constructionProposal"`)) {
		t.Fatalf("public accepted receipt exposed duplicated workspace or private binding: %s", receiptJSON)
	}

	var raw map[string]any
	if err := json.Unmarshal(fixture, &raw); err != nil {
		t.Fatal(err)
	}
	acceptedObject := raw["acceptedConstruction"].(map[string]any)
	receiptObject := acceptedObject["receipt"].(map[string]any)
	receiptObject["constructionProposal"] = map[string]any{"baseDraftDigest": "private"}
	withPrivateBinding, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := decodeStrict(withPrivateBinding, &loomapi.ApplyCommandsResponse{}); err == nil {
		t.Fatal("strict accepted-construction contract accepted a private proposal binding")
	}
}
