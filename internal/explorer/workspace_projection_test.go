package explorer

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

type projectionTestReader struct {
	ExplorerStore
	projection WorkspaceProjection
	err        error
	calls      []WorkspaceArtifact
}

func (r *projectionTestReader) WorkspaceProjection(_ context.Context, source WorkspaceArtifact) (WorkspaceProjection, error) {
	r.calls = append(r.calls, source)
	return r.projection, r.err
}
func projectionTestPacket(t *testing.T) (json.RawMessage, string) {
	t.Helper()
	w := authoringv2.Workspace{APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, SemanticsVersion: authoringv2.CurrentSemanticsVersion, Explorer: authoringv2.ExplorerMetadata{Title: "Projected"}, Documents: []authoringv2.Document{}, Tabs: []authoringv2.Tab{}}
	raw, err := w.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = authoringv2.DecodeWorkspace(raw); err != nil {
		t.Fatal(err)
	}
	digest, err := w.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return raw, digest
}
func projectionTestSidecar(t *testing.T, source WorkspaceArtifact) WorkspaceProjection {
	raw, digest := projectionTestPacket(t)
	p := WorkspaceProjection{Source: source, Workspace: raw, Digest: digest}
	p.ID = p.ContentID()
	return p
}
func TestWorkspaceProjectionCurrentPacketsBypassReader(t *testing.T) {
	raw, digest := projectionTestPacket(t)
	reader := &projectionTestReader{err: errors.New("must not read sidecar")}
	s := &Service{store: reader}
	revision := Revision{AuthoringBundle: raw, IntentDigest: digest}
	if _, err := s.RevisionWorkspace(context.Background(), revision); err != nil {
		t.Fatal(err)
	}
	draft := DraftRevision{DraftConfig: raw, DraftDigest: digest}
	if _, got, err := s.DraftRevisionWorkspace(context.Background(), draft); err != nil || got != digest {
		t.Fatalf("digest=%s err=%v", got, err)
	}
	if len(reader.calls) != 0 {
		t.Fatalf("current packets queried sidecars: %+v", reader.calls)
	}
}
func TestWorkspaceProjectionImmutablePacketsRemainExact(t *testing.T) {

	raw := json.RawMessage(`{"legacyPacket":"immutable-original"}`)
	revision := Revision{ID: "revision", Project: "tenant", ExplorerID: "explorer", AuthoringBundle: raw, IntentDigest: "original-intent", CompilationReceiptID: "original-receipt", SourceGeneration: "generation"}
	before, _ := json.Marshal(revision)
	source := RevisionWorkspaceArtifact(revision)
	reader := &projectionTestReader{projection: projectionTestSidecar(t, source)}
	s := &Service{store: reader}
	if _, err := s.RevisionWorkspace(context.Background(), revision); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(revision)
	if string(before) != string(after) {
		t.Fatal("immutable revision changed")
	}
	if !reflect.DeepEqual(reader.calls, []WorkspaceArtifact{source}) {
		t.Fatalf("wrong revision lookup: %+v", reader.calls)
	}
	draft := DraftRevision{ID: "history", Project: "tenant", ExplorerID: "explorer", DraftConfig: raw, DraftDigest: "original-history-digest", SnapshotToken: "snapshot", SourceGeneration: "generation", AuthorizationScopeDigest: "scope", DraftVersion: 3}
	before, _ = json.Marshal(draft)
	source = DraftWorkspaceArtifact(draft)
	reader.projection = projectionTestSidecar(t, source)
	reader.calls = nil
	_, digest, err := s.DraftRevisionWorkspace(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	if digest != reader.projection.Digest || digest == draft.DraftDigest {
		t.Fatal("canonical history digest must be separate from original digest")
	}
	after, _ = json.Marshal(draft)
	if string(before) != string(after) {
		t.Fatal("immutable history changed")
	}
	if !reflect.DeepEqual(reader.calls, []WorkspaceArtifact{source}) {
		t.Fatalf("wrong history lookup: %+v", reader.calls)
	}
}
func TestWorkspaceProjectionRejectsSourceAndContentTampering(t *testing.T) {
	source := WorkspaceArtifact{Kind: "draft-revision", Project: "tenant", ExplorerID: "explorer", ID: "history", RawDigest: "raw", IntentDigest: "intent", SnapshotToken: "snapshot", Generation: "generation", ScopeDigest: "scope", ReceiptID: "receipt", DraftVersion: 3}
	fields := []string{"Kind", "Project", "ExplorerID", "ID", "RawDigest", "IntentDigest", "SnapshotToken", "Generation", "ScopeDigest", "ReceiptID", "DraftVersion"}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			p := projectionTestSidecar(t, source)
			v := reflect.ValueOf(&p.Source).Elem().FieldByName(field)
			if v.Kind() == reflect.String {
				v.SetString(v.String() + "-other")
			} else {
				v.SetInt(v.Int() + 1)
			}
			p.ID = p.ContentID()
			if _, err := p.Validate(source); err == nil {
				t.Fatal("accepted mismatched source")
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*WorkspaceProjection)
	}{
		{"missing", func(p *WorkspaceProjection) { *p = WorkspaceProjection{} }},
		{"id", func(p *WorkspaceProjection) { p.ID = "wrong" }},
		{"bytes", func(p *WorkspaceProjection) { p.Workspace = json.RawMessage(`{}`) }},
		{"digest", func(p *WorkspaceProjection) { p.Digest = "wrong"; p.ID = p.ContentID() }},
		{"invalid-packet", func(p *WorkspaceProjection) { p.Workspace = json.RawMessage(`{}`); p.ID = p.ContentID() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := projectionTestSidecar(t, source)
			tc.mutate(&p)
			if _, err := p.Validate(source); err == nil {
				t.Fatal("accepted invalid projection")
			}
		})
	}
}
func TestWorkspaceProjectionUnavailableOrWrongSidecarFails(t *testing.T) {
	revision := Revision{ID: "revision", Project: "tenant", AuthoringBundle: json.RawMessage(`{"legacyPacket":true}`)}
	for _, tc := range []struct {
		name   string
		reader *projectionTestReader
	}{
		{"missing", &projectionTestReader{err: errors.New("not found")}},
		{"zero", &projectionTestReader{}},
		{"wrong-source", &projectionTestReader{projection: projectionTestSidecar(t, WorkspaceArtifact{ID: "different"})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Service{store: tc.reader}
			if _, err := s.RevisionWorkspace(context.Background(), revision); err == nil {
				t.Fatal("accepted absent or unrelated sidecar")
			}
		})
	}
	s := &Service{store: struct{ ExplorerStore }{}}
	if _, err := s.RevisionWorkspace(context.Background(), revision); err == nil {
		t.Fatal("accepted legacy packet without projection reader")
	}
}
