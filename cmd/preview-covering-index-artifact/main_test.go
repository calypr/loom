package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestFindProposalPreservesRequestAfterValidationResponse(t *testing.T) {
	body := json.RawMessage(`{"snapshotToken":"snapshot","outputId":"output","changedStepId":"pivot","expectedDraftVersion":4,"expectedDraftDigest":"draft"}`)
	report := capturedReport{Project: "project", Explorer: "explorer"}
	pending := capturedAuthoringRequest{Pathname: authoringPath(report, "construction-proposals"), Body: body}
	report.AuthoringRequests = []capturedAuthoringRequest{pending}
	want, wantBody, err := findProposal(report)
	if err != nil {
		t.Fatal(err)
	}
	status := 422
	completed := pending
	completed.Status = &status
	completed.Response = json.RawMessage(`{"error":{"code":"TABLE_PIVOT_CELL_CARDINALITY"}}`)
	report.AuthoringRequests = []capturedAuthoringRequest{completed}
	got, gotBody, err := findProposal(report)
	if err != nil {
		t.Fatal(err)
	}
	if got.OutputID != want.OutputID || got.ChangedStepID != want.ChangedStepID || got.ExpectedDraftDigest != want.ExpectedDraftDigest || !bytes.Equal(gotBody, wantBody) {
		t.Fatal("terminal response changed the captured candidate request")
	}
	report.AuthoringRequests = append(report.AuthoringRequests, completed)
	if _, _, err := findProposal(report); err == nil {
		t.Fatal("accepted ambiguous captured candidates")
	}
}
