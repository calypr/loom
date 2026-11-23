package proposal

import (
	"encoding/json"
	"fmt"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"testing"
)

func TestSourceProposalWireAcceptsExactlyOneClosedIntent(t *testing.T) {
	for _, payload := range []string{
		`{}`, `{"sourceStep":{"stepId":"fields","remove":true},"includePreview":null}`, `{"sourceStep":{"stepId":"fields","remove":true},"includePreview":"true"}`, `{"sourceStep":{"stepId":"fields","remove":true},"previewSinkFactory":{}}`, `{"sourceStep":{"stepId":"fields","remove":true},"nativePreview":{}}`, `{"change":null}`, `{"sourceStep":null}`, `{"change":{"action":"REMOVE","occurrenceId":"r"},"sourceStep":{"stepId":"fields","remove":true}}`,
		`{"sourceStep":{"stepId":"fields","remove":true,"parameters":{"fieldGroup":{"fields":[]}}}}`,
		`{"sourceStep":{"stepId":"fields","remove":true},"project":"other"}`,
		`{"sourceStep":{"stepId":"fields","remove":true},"change":null}`,
		`{"sourceStep":{"stepId":"fields","remove":true},"limit":0}`,
		`{"sourceStep":{"stepId":"fields","remove":true},"limit":-1}`,
		`{"sourceStep":{"stepId":"fields","remove":true},"limit":null}`,
		`{"sourceStep":{"stepId":"fields","enabled":false}}`,
	} {
		var request SourceRelationshipProposalRequest
		if err := json.Unmarshal([]byte(payload), &request); err == nil {
			t.Fatalf("accepted injected or ambiguous source intent: %s", payload)
		}
	}
	input := SourceRelationshipProposalRequest{Project: "server-project", ExplorerID: "server-owner", SnapshotToken: "snapshot", ExpectedDraftVersion: 3, ExpectedDraftDigest: "digest", OutputID: "patients", SourceStep: &authoringv2.ImportedSourceStepChange{StepID: "fields", Remove: true}}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var restored SourceRelationshipProposalRequest
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Project != "" || restored.ExplorerID != "" || restored.Change.Action != "" || restored.SourceStep == nil || !restored.SourceStep.Remove || restored.SourceStep.StepID != "fields" || restored.ExpectedDraftVersion != 3 {
		t.Fatalf("closed source-step intent round trip lost ownership or settings: %s", raw)
	}
}

func TestSourceProposalWireAdmitsOnlyAtomicBoundedUniqueBatch(t *testing.T) {
	valid := `{"sourceSteps":[{"stepId":"fields","remove":true},{"stepId":"dynamicColumns[0]","remove":true}]}`
	var request SourceRelationshipProposalRequest
	if err := json.Unmarshal([]byte(valid), &request); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SourceRelationshipProposalRequest
	if err := json.Unmarshal(raw, &decoded); err != nil || len(decoded.SourceSteps) != 2 || decoded.SourceSteps[0].StepID != "fields" {
		t.Fatalf("batch roundtrip lost identities: %v", err)
	}
	for _, payload := range []string{`{"sourceSteps":null}`, `{"sourceSteps":[]}`, `{"sourceSteps":[null]}`, `{"sourceSteps":[{"stepId":"fields","remove":true},{"stepId":"fields","remove":true}]}`, `{"sourceSteps":[{"stepId":"fields","remove":true}],"sourceStep":{"stepId":"fields","remove":true}}`, `{"sourceSteps":[{"stepId":"fields","remove":true}],"change":{"action":"REMOVE","occurrenceId":"o"}}`, `{"sourceSteps":[{"stepId":"fields","remove":true,"columnId":"forged"}]}`} {
		if err := json.Unmarshal([]byte(payload), &decoded); err == nil {
			t.Fatalf("invalid batch admitted: %s", payload)
		}
	}
	changes := make([]authoringv2.ImportedSourceStepChange, 129)
	for i := range changes {
		changes[i] = authoringv2.ImportedSourceStepChange{StepID: fmt.Sprintf("fields-%d", i), Remove: true}
	}
	if err := authoringv2.ValidateImportedSourceStepBatch(changes); err == nil {
		t.Fatal("unbounded source batch admitted")
	}
}

func TestSourceProposalPreviewOptInRoundTripsWithoutRuntimeFields(t *testing.T) {
	for _, include := range []bool{false, true} {
		input := SourceRelationshipProposalRequest{SnapshotToken: "snapshot", ExpectedDraftVersion: 1, ExpectedDraftDigest: "digest", OutputID: "out", IncludePreview: include, SourceStep: &authoringv2.ImportedSourceStepChange{StepID: "fields", Remove: true}}
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		_, exists := fields["includePreview"]
		if exists != include {
			t.Fatalf("opt-in presence=%v include=%v", exists, include)
		}
		var restored SourceRelationshipProposalRequest
		if err = json.Unmarshal(raw, &restored); err != nil {
			t.Fatal(err)
		}
		if restored.IncludePreview != include || restored.PreviewSinkFactory != nil {
			t.Fatal("preview opt-in did not round trip safely")
		}
	}
}
