package explorer

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/calypr/loom/internal/explorer/viewer"
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

func TestNewRevisionStartsReadyAndPreservesPublicationTimes(t *testing.T) {
	createdAt := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	readyAt := createdAt.Add(time.Minute)
	revision := NewRevision(Revision{
		ID: "revision-a", CreatedAt: createdAt, ReadyAt: &readyAt,
		Publication: viewer.PublicationMetadata{State: "READY", UpdatedAt: readyAt},
		Status:      RevisionFailed,
	})
	if revision.Status != RevisionReady || !revision.CreatedAt.Equal(createdAt) || revision.ReadyAt == nil || !revision.ReadyAt.Equal(readyAt) || revision.Publication.State != "READY" || !revision.Publication.UpdatedAt.Equal(readyAt) {
		t.Fatalf("new revision = %+v, want ready status with supplied publication metadata and timestamps", revision)
	}
}
