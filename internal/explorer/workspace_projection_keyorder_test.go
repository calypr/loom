package explorer

import (
	"bytes"
	"encoding/json"
	"testing"

	workspacepkg "github.com/calypr/loom/internal/explorer/workspace"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func projectionMapRoundTrip(t *testing.T, raw []byte) json.RawMessage {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestWorkspaceProjectionSourceKeySurvivesJSONMapRoundTrip(t *testing.T) {
	raw := json.RawMessage(` { "z": {"second":2,"first":1}, "number":1152921504606846976, "a":true } `)
	before := append([]byte(nil), raw...)
	roundTrip := projectionMapRoundTrip(t, raw)
	if bytes.Equal(raw, roundTrip) {
		t.Fatal("fixture did not change JSON representation")
	}
	if !bytes.Contains(roundTrip, []byte("1152921504606846976")) {
		t.Fatal("large integer was rounded")
	}
	revision := Revision{ID: "revision", Project: "tenant", ExplorerID: "explorer", Config: raw, IntentDigest: "intent", SourceGeneration: "generation", CompilationReceiptID: "receipt"}
	source := RevisionWorkspaceArtifact(revision)
	revision.Config = roundTrip
	if got := RevisionWorkspaceArtifact(revision); got != source || got.Key() != source.Key() {
		t.Fatalf("map roundtrip changed source: %+v != %+v", got, source)
	}

	revision.AuthoringBundle = raw
	revision.Config = json.RawMessage(`{"ignored":true}`)
	if got := RevisionWorkspaceArtifact(revision); got != source {
		t.Fatalf("authoring slot changed source: %+v", got)
	}
	draft := DraftRevision{ID: "history", Project: "tenant", ExplorerID: "explorer", DraftConfig: raw, DraftVersion: 2, DraftDigest: "old-digest", SnapshotToken: "snapshot", AuthorizationScopeDigest: "scope"}
	history := DraftWorkspaceArtifact(draft)
	draft.DraftConfig = roundTrip
	if got := DraftWorkspaceArtifact(draft); got != history || got.Key() != history.Key() {
		t.Fatal("history map roundtrip changed key")
	}

	revision.AuthoringBundle = bytes.Replace(raw, []byte("1152921504606846976"), []byte("1152921504606846977"), 1)
	if got := RevisionWorkspaceArtifact(revision); got.RawDigest == source.RawDigest || got.Key() == source.Key() {
		t.Fatal("numeric content change was lost")
	}
	if !bytes.Equal(raw, before) {
		t.Fatal("source bytes mutated")
	}
}

func TestWorkspaceProjectionContentIDSurvivesJSONMapRoundTrip(t *testing.T) {
	raw := json.RawMessage(` {"z":2,"number":1152921504606846976,"a":{"y":2,"x":1}} `)
	p := workspacepkg.Projection{Source: workspacepkg.Artifact{Kind: "revision", Project: "tenant", ID: "id", RawDigest: workspaceBytesDigest(raw)}, Workspace: raw, Digest: "digest"}
	p.ID = p.ContentID()
	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var decoded workspacepkg.Projection
	if err = json.Unmarshal(projectionMapRoundTrip(t, encoded), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ID != p.ID || decoded.ContentID() != p.ID {
		t.Fatal("map roundtrip changed projection content address")
	}
	decoded.Workspace = bytes.Replace(decoded.Workspace, []byte("1152921504606846976"), []byte("1152921504606846977"), 1)
	if decoded.ContentID() == p.ID {
		t.Fatal("numeric content change retained content address")
	}
	decoded = p
	decoded.Source.Project = "different-tenant"
	if decoded.ContentID() == p.ID {
		t.Fatal("source context change retained content address")
	}
}

func TestWorkspaceProjectionReorderedValidContentStillAuthenticates(t *testing.T) {
	w := ws.Workspace{APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion, Explorer: ws.ExplorerMetadata{Title: "Canonical"}, Documents: []ws.Document{}, Tabs: []ws.Tab{}}
	raw, err := w.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := w.Digest()
	if err != nil {
		t.Fatal(err)
	}
	source := workspacepkg.Artifact{Kind: "revision", Project: "tenant", ID: "revision", RawDigest: "original-raw"}
	p := workspacepkg.Projection{Source: source, Workspace: raw, Digest: digest}
	p.ID = p.ContentID()
	p.Workspace = projectionMapRoundTrip(t, raw)
	if _, err = p.Validate(source); err != nil {
		t.Fatalf("reordered valid workspace failed: %v", err)
	}
	changed := p
	changed.Workspace = bytes.Replace(p.Workspace, []byte("Canonical"), []byte("Tampered"), 1)
	if _, err = changed.Validate(source); err == nil {
		t.Fatal("changed workspace accepted under old content ID")
	}
	changed.ID = changed.ContentID()
	if _, err = changed.Validate(source); err == nil {
		t.Fatal("changed workspace accepted under old canonical digest")
	}
	wrongSource := source
	wrongSource.Project = "other-tenant"
	if _, err = p.Validate(wrongSource); err == nil {
		t.Fatal("reordered projection accepted for another tenant")
	}
}
