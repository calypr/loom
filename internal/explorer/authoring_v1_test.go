package explorer

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestAuthoringBundleV1StrictDecodeAndCanonicalDigest(t *testing.T) {
	raw := []byte(`{"apiVersion":"loom.calypr.org/explorer-authoring/v1","kind":"ExplorerAuthoringBundle","project":"p","explorerId":"e","document":{"kind":"ExplorerBuilderDocument","output":{"id":"out"},"baseNodeId":"n_base","rowNodeId":"n_base","candidateIds":["s_id"],"presentation":{}}}`)
	bundle, err := DecodeAuthoringBundleV1(raw)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := bundle.DocumentDigest()
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := bundle.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(canonical), digest) {
		t.Fatalf("canonical bundle does not carry digest: %s", canonical)
	}
	if _, err := DecodeAuthoringBundleV1([]byte(strings.Replace(string(raw), `"candidateIds":["s_id"]`, `"candidateIds":["s_id"],"recipe":{"direction":"OUTBOUND"}`, 1))); err == nil {
		t.Fatal("accepted recipe-shaped authoring field")
	}
	if _, err := DecodeAuthoringBundleV1([]byte(strings.Replace(string(raw), `"candidateIds":["s_id"]`, `"candidateIds":["s_id"],"children":[]`, 1))); err == nil {
		t.Fatal("accepted children field")
	}
	if _, err := DecodeAuthoringBundleV1([]byte(strings.Replace(string(raw), `"candidateIds":["s_id"]`, `"candidateIds":["s_id"],"candidateIds":["s_id"]`, 1))); err == nil {
		t.Fatal("accepted duplicate field")
	}
}

func TestAuthoringBundleV1RejectsDigestMismatch(t *testing.T) {
	raw := []byte(`{"apiVersion":"loom.calypr.org/explorer-authoring/v1","kind":"ExplorerAuthoringBundle","project":"p","explorerId":"e","intentDigest":"sha256:bad","document":{"kind":"ExplorerBuilderDocument","output":{"id":"out"},"baseNodeId":"n_base","rowNodeId":"n_base"}}`)
	if _, err := DecodeAuthoringBundleV1(raw); err == nil || !strings.Contains(err.Error(), "INTENT_DIGEST_MISMATCH") {
		t.Fatalf("error=%v", err)
	}
}

func TestReceiptIDIsContentAddressed(t *testing.T) {
	r := CompilationReceipt{Project: "p", ExplorerID: "e", IntentDigest: "sha256:a", SnapshotToken: "sha256:s", SourceGeneration: "g"}
	first, err := ReceiptID(r)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReceiptID(r)
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first != second || !strings.HasPrefix(first, "receipt_") {
		t.Fatalf("receipt IDs=%q,%q", first, second)
	}
}

func TestReceiptIDNormalizesJSONRepresentation(t *testing.T) {
	base := CompilationReceipt{
		Project:          "p",
		ExplorerID:       "e",
		IntentDigest:     "sha256:a",
		SnapshotToken:    "sha256:s",
		SourceGeneration: "g",
		CompiledConfig:   json.RawMessage(`{"views":[{"columns":{"z":2,"a":1}}],"apiVersion":"loom.calypr.org/explorer-config/v2"}`),
	}
	reordered := base
	reordered.CompiledConfig = json.RawMessage(`{"apiVersion":"loom.calypr.org/explorer-config/v2","views":[{"columns":{"a":1,"z":2}}]}`)

	first, err := ReceiptID(base)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReceiptID(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("JSON representation changed receipt ID: %q != %q", first, second)
	}
}

func TestReceiptIDSurvivesDocumentRoundTrip(t *testing.T) {
	receipt := CompilationReceipt{
		Project:          "p",
		ExplorerID:       "e",
		IntentDigest:     "sha256:a",
		SnapshotToken:    "sha256:s",
		SourceGeneration: "g",
		CompiledConfig:   json.RawMessage(`{"views":[{"columns":{"z":2,"a":1}}],"apiVersion":"loom.calypr.org/explorer-config/v2"}`),
	}
	original, err := ReceiptID(receipt)
	if err != nil {
		t.Fatal(err)
	}

	// This mirrors the Arango store's document conversion: marshal the typed
	// receipt into a generic JSON document, then decode it again.
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var roundTripped CompilationReceipt
	if err := json.Unmarshal(raw, &roundTripped); err != nil {
		t.Fatal(err)
	}

	loaded, err := ReceiptID(roundTripped)
	if err != nil {
		t.Fatal(err)
	}
	if original != loaded {
		t.Fatalf("Arango-like round trip changed receipt ID: %q != %q", original, loaded)
	}
}

func TestReceiptIDChangesWhenContentChanges(t *testing.T) {
	receipt := CompilationReceipt{
		Project:          "p",
		ExplorerID:       "e",
		IntentDigest:     "sha256:a",
		SnapshotToken:    "sha256:s",
		SourceGeneration: "g",
		CompiledConfig:   json.RawMessage(`{"apiVersion":"loom.calypr.org/explorer-config/v2","views":[]}`),
	}
	original, err := ReceiptID(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.CompiledConfig = json.RawMessage(`{"apiVersion":"loom.calypr.org/explorer-config/v2","views":[{"id":"new"}]}`)
	changed, err := ReceiptID(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if original == changed {
		t.Fatal("receipt ID did not change after content changed")
	}
}

func TestMemoryStorePurgesOrphanAuthoringReceipts(t *testing.T) {
	store := NewMemoryStore()
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, id := range []string{"receipt_kept", "receipt_orphan"} {
		if _, err := service.StoreCompilationReceipt(ctx, CompilationReceipt{ID: id, Project: "p", ExplorerID: "e"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.InsertRevision(ctx, Revision{ID: "r1", Project: "p", ExplorerID: "e", CompilationReceiptID: "receipt_kept"}); err != nil {
		t.Fatal(err)
	}
	if err := service.PurgeDraftAuthoring(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompilationReceipt(ctx, "receipt_kept"); err != nil {
		t.Fatalf("referenced receipt removed: %v", err)
	}
	if _, err := service.CompilationReceipt(ctx, "receipt_orphan"); err != ErrNotFound {
		t.Fatalf("orphan receipt error=%v", err)
	}
}

func TestMemoryStoreAuthoringActivationIsLastWriterWins(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	if _, err := store.CreateInteractive(ctx, Explorer{Project: "p", ExplorerID: "e", ManagementMode: ManagementInteractive}); err != nil {
		t.Fatal(err)
	}
	for _, revision := range []Revision{
		{ID: "r1", Project: "p", ExplorerID: "e", Status: RevisionReady},
		{ID: "r2", Project: "p", ExplorerID: "e", Status: RevisionReady},
	} {
		if _, err := store.InsertRevision(ctx, revision); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wait sync.WaitGroup
	for _, revisionID := range []string{"r1", "r2"} {
		wait.Add(1)
		go func(id string) {
			defer wait.Done()
			<-start
			errs <- store.ActivateInteractive(ctx, "p", "e", id)
		}(revisionID)
	}
	close(start)
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	owner, err := store.Get(ctx, "p", "e")
	if err != nil || (owner.ActiveRevisionID != "r1" && owner.ActiveRevisionID != "r2") {
		t.Fatalf("last-writer-wins activation owner=%#v err=%v", owner, err)
	}
}
