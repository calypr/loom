package lifecycle

import (
	"encoding/json"
	"fmt"
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

func TestSourceProposalWireAdmitsOnlyAtomicBoundedUniqueBatch(t *testing.T) {
	valid := `{"sourceSteps":[{"stepId":"expand","enabled":true,"parameters":{"expansion":{"from":{"select":"root.member[]"},"as":"entry"}}},{"stepId":"identity","enabled":true,"parameters":{"identity":{"name":"id","expr":{"select":"entry.id"}}}}]}`
	var request SourceRelationshipProposalRequest
	if err := json.Unmarshal([]byte(valid), &request); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SourceRelationshipProposalRequest
	if err := json.Unmarshal(raw, &decoded); err != nil || len(decoded.SourceSteps) != 2 || decoded.SourceSteps[0].StepID != "expand" {
		t.Fatalf("batch roundtrip lost identities: %v", err)
	}
	for _, payload := range []string{`{"sourceSteps":null}`, `{"sourceSteps":[]}`, `{"sourceSteps":[null]}`, `{"sourceSteps":[{"stepId":"fields","enabled":true},{"stepId":"fields","enabled":false}]}`, `{"sourceSteps":[{"stepId":"fields","enabled":true}],"sourceStep":{"stepId":"fields","enabled":true}}`, `{"sourceSteps":[{"stepId":"fields","enabled":true}],"change":{"action":"REMOVE","occurrenceId":"o"}}`, `{"sourceSteps":[{"stepId":"fields","enabled":true,"columnId":"forged"}]}`} {
		if err := json.Unmarshal([]byte(payload), &decoded); err == nil {
			t.Fatalf("invalid batch admitted: %s", payload)
		}
	}
	changes := make([]authoringv2.ImportedSourceStepChange, 129)
	for i := range changes {
		changes[i] = authoringv2.ImportedSourceStepChange{StepID: fmt.Sprintf("fields-%d", i), Enabled: true}
	}
	if err := authoringv2.ValidateImportedSourceStepBatch(changes); err == nil {
		t.Fatal("unbounded source batch admitted")
	}
}
