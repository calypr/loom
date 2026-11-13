package benchmark

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	loomexplorer "github.com/calypr/loom/internal/explorer"
)

// The explorer route answers ExplorerStateV1 (internal/explorer
// state_v1.go): the draft digest is at draft.digest and the revision
// identity at active.revisionId. The paired run at 7c5ef71e aborted
// "explorer readback on arango omitted the draft digest" because the
// client decoded top-level draftDigest/activeRevisionId, which that
// shape never carries. This test serves a real-marshal ExplorerStateV1
// and fails on the pre-fix client.

func serverStateV1(digest, revisionID string) loomexplorer.ExplorerStateV1 {
	return loomexplorer.ExplorerStateV1{
		APIVersion: loomexplorer.ExplorerStateV1APIVersion,
		Kind:       loomexplorer.ExplorerStateV1Kind,
		Project:    "loom-bench",
		ExplorerID: "bench-workload",
		Title:      "bench-workload",
		Management: loomexplorer.ManagementInteractive,
		Draft:      loomexplorer.ExplorerStateV1Draft{Version: 1, Digest: digest},
		Active:     loomexplorer.ExplorerStateV1Active{RevisionID: revisionID, Status: "PUBLISHED"},
		ActiveURL:  "/api/v1/projects/loom-bench/explorers/bench-workload",
		UpdatedBy:  "bench",
		UpdatedAt:  time.Now().UTC(),
	}
}

func TestFetchExplorerActiveStateReadsRealServerStateShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(serverStateV1("sha256:server-draft-digest", "authoring-rev-1"))
	}))
	defer srv.Close()
	arm := &Arm{Backend: BackendArango, BaseURL: srv.URL, Client: srv.Client()}
	state, err := arm.FetchExplorerActiveState(context.Background(), "t", "loom-bench", "bench-workload")
	if err != nil {
		t.Fatalf("readback errored: %v", err)
	}
	if state.DraftDigest != "sha256:server-draft-digest" {
		t.Errorf("DraftDigest lost: got %q, want draft.digest", state.DraftDigest)
	}
	if state.ActiveRevisionID != "authoring-rev-1" {
		t.Errorf("ActiveRevisionID lost: got %q, want active.revisionId", state.ActiveRevisionID)
	}
}

func TestSetupProofEvidenceAbortsOnEmptyDraftDigest(t *testing.T) {
	f := newFakeArm(BackendArango)
	defer f.close()
	f.mu.Lock()
	f.forceEmptyActiveState = true
	f.rows["out"] = []map[string]any{{"patient_id": "p1"}}
	f.mu.Unlock()
	_, err := f.arm(BackendArango).SetupProofEvidence(context.Background(), "Bearer x", "proj", "exp-1", "rcpt-out", "out")
	if err == nil || !strings.Contains(err.Error(), "omitted the draft digest") {
		t.Fatalf("empty draft digest must abort fail-closed, got: %v", err)
	}
}
