package lifecycle

import (
	"encoding/json"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"testing"
)

func TestSourceProposalWireAcceptsExactlyOneClosedIntent(t *testing.T) {
	for _, payload := range []string{
		`{}`, `{"change":null}`, `{"sourceStep":null}`, `{"change":{"action":"REMOVE","occurrenceId":"r"},"sourceStep":{"stepId":"fields","enabled":false}}`,
		`{"sourceStep":{"stepId":"fields"}}`,
		`{"sourceStep":{"stepId":"fields","enabled":false,"expr":{"select":"root.secret"}}}`,
		`{"sourceStep":{"stepId":"fields","enabled":false},"project":"other"}`,
		`{"sourceStep":{"stepId":"fields","enabled":false},"change":null}`,
		`{"sourceStep":{"stepId":"fields","enabled":false},"limit":0}`,
		`{"sourceStep":{"stepId":"fields","enabled":false},"limit":-1}`,
		`{"sourceStep":{"stepId":"fields","enabled":false},"limit":null}`,
	} {
		var request SourceRelationshipProposalRequest
		if err := json.Unmarshal([]byte(payload), &request); err == nil {
			t.Fatalf("accepted injected or ambiguous source intent: %s", payload)
		}
	}
	input := SourceRelationshipProposalRequest{Project: "server-project", ExplorerID: "server-owner", SnapshotToken: "snapshot", ExpectedDraftVersion: 3, ExpectedDraftDigest: "digest", OutputID: "patients", SourceStep: &authoringv2.ImportedSourceStepChange{StepID: "fields", Enabled: false}}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var restored SourceRelationshipProposalRequest
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Project != "" || restored.ExplorerID != "" || restored.Change.Action != "" || restored.SourceStep == nil || restored.SourceStep.Enabled || restored.SourceStep.StepID != "fields" || restored.ExpectedDraftVersion != 3 {
		t.Fatalf("closed source-step intent round trip lost ownership or settings: %s", raw)
	}
}
