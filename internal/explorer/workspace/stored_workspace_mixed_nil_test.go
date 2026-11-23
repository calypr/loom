package workspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"testing"
)

func TestMigrateStoredWorkspaceRecognizesMixedNilStepDigest(t *testing.T) {
	w := constructionWorkspace(workspaceDocument("a"))
	w.Documents = append(w.Documents, workspaceDocument("b"))
	w.Tabs = append(w.Tabs, Tab{ID: "b", Title: "B", OutputID: "b", Order: 1, Visible: true})
	for i := range w.Documents {
		d, err := migrateStoredDocumentFixture(w.Documents[i])
		if err != nil {
			t.Fatal(err)
		}
		w.Documents[i] = d
		w.Documents[i].Construction.Steps = []ConstructionStep{}
	}
	canonical, err := w.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	at := bytes.LastIndex(canonical, []byte(`"steps":[]`))
	if at < 0 {
		t.Fatal(string(canonical))
	}
	raw := append([]byte(nil), canonical[:at]...)
	raw = append(raw, []byte(`"steps":null`)...)
	raw = append(raw, canonical[at+len(`"steps":[]`):]...)
	sum := sha256.Sum256(raw)
	want := "sha256:" + hex.EncodeToString(sum[:])
	migrated, err := MigrateStoredWorkspace(raw, CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(migrated.AcceptedOriginalDigests, want) {
		t.Fatalf("original mixed-null canonical digest %s absent from accepted %v; nil lists=%d", want, migrated.AcceptedOriginalDigests, migrated.OriginalNilConstructionStepLists)
	}
}
