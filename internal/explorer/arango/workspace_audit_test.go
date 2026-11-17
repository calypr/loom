package arango

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func auditFixture(t *testing.T) authoringv2.Workspace {
	t.Helper()
	return authoringv2.Workspace{APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, SemanticsVersion: authoringv2.CurrentSemanticsVersion, Explorer: authoringv2.ExplorerMetadata{Title: "Audit"}, Documents: []authoringv2.Document{{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}, Rows: authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionRecords, Records: &authoringv2.RecordRows{}}, Columns: []authoringv2.Column{}, Construction: &authoringv2.Construction{Version: 1}}}, Tabs: []authoringv2.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0}}}
}
func TestWorkspaceAuditCountsEveryAuthoritativeSlotAndSeparatesDiscovery(t *testing.T) {
	workspace := auditFixture(t)
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	report := WorkspaceAuditReport{}
	for _, fixture := range []struct{ collection, field, digest string }{{ExplorersCollection, "draftConfig", "draftDigest"}, {DraftRevisionsCollection, "draftConfig", "draftDigest"}, {RevisionsCollection, "authoringBundle", "intentDigest"}, {CompilationReceiptsCollection, "normalizedBundle", "intentDigest"}, {LegacyRepositoryConfigsCollection, "workspace", "intentDigest"}} {
		document, _ := json.Marshal(map[string]any{"_key": "one", fixture.field: json.RawMessage(raw), fixture.digest: digest, "snapshotToken": "pinned-token"})
		before := append([]byte(nil), document...)
		if err := report.ObserveWorkspaceAuditDocument(fixture.collection, document); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(document, before) {
			t.Fatal("audit mutated source document")
		}
	}
	unknown, _ := json.Marshal(map[string]any{"_key": "discovery", "commandResults": map[string]any{"workspace": json.RawMessage(raw)}})
	if err := report.ObserveWorkspaceAuditDocument("other_durable_collection", unknown); err != nil {
		t.Fatal(err)
	}
	if report.AuthoritativeWorkspaces != 5 || report.DiscoveryCandidates != 1 || report.ByClass["current"] != 5 || report.AlreadyCanonicalWorkspaces != 5 || report.DigestRelations["matches-decoded-canonical"] != 5 {
		t.Fatalf("unexpected audit counts: %+v", report)
	}
	for _, entry := range report.Entries {
		if !entry.Authoritative && entry.StoredDigest != "" {
			t.Fatal("discovery inherited an unrelated digest")
		}
	}
	presentation := []byte(`{"_key":"config","config":{"apiVersion":"loom.calypr.org/explorer-config/v2","kind":"ExplorerConfig"}}`)
	if err := report.ObserveWorkspaceAuditDocument(RevisionsCollection, presentation); err != nil {
		t.Fatal(err)
	}
	if report.NonWorkspacePackets != 1 || report.AuthoritativeWorkspaces != 5 {
		t.Fatalf("presentation falsely classified as workspace: %+v", report)
	}
}
func TestWorkspaceAuditReportsNilConstructionDigestWithoutRewriting(t *testing.T) {
	workspace := auditFixture(t)
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(`"steps":[]`), []byte(`"steps":null`), 1)
	legacy, err := workspace.LegacyNilConstructionStepsDigest()
	if err != nil || legacy == "" {
		t.Fatalf("legacy digest=%q err=%v", legacy, err)
	}
	document, _ := json.Marshal(map[string]any{"_key": "old", "draftConfig": json.RawMessage(raw), "draftDigest": legacy})
	report := WorkspaceAuditReport{}
	if err := report.ObserveWorkspaceAuditDocument(DraftRevisionsCollection, document); err != nil {
		t.Fatal(err)
	}
	if len(report.Entries) != 1 {
		t.Fatalf("entries=%+v", report)
	}
	entry := report.Entries[0]
	if entry.NilConstructionStepLists != 1 || entry.Classification != "legacy" || entry.DigestRelation != "matches-legacy-nil-construction" || !entry.Immutable {
		t.Fatalf("nil construction observation=%+v", entry)
	}
}
func TestWorkspaceAuditReportsLegacySourcesPinnedCatalogAndFutureVersions(t *testing.T) {
	raw := `{"apiVersion":"loom.calypr.org/explorer-authoring/v2","kind":"ExplorerBuilderWorkspace","semanticsVersion":0,"explorer":{"title":"Persisted"},"documents":[{"kind":"ExplorerBuilderDocument","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient"},"columns":[{"column":"names","label":"Names","occurrenceId":"base","source":{"kind":"field","fieldPath":"name[].family","projectionMode":"FIRST"}}]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`
	report := WorkspaceAuditReport{}
	document, _ := json.Marshal(map[string]any{"_key": "legacy", "workspace": json.RawMessage(raw), "intentDigest": "sha256:prior"})
	if err := report.ObserveWorkspaceAuditDocument(LegacyRepositoryConfigsCollection, document); err != nil {
		t.Fatal(err)
	}
	entry := report.Entries[0]
	if entry.Classification != "legacy" || !entry.NeedsPinnedCatalog || entry.StrictCurrentError == "" || entry.DecodeError != "" || entry.DigestRelation != "differs-from-decoded-canonical" {
		t.Fatalf("legacy source observation=%+v", entry)
	}
	raw = strings.Replace(raw, `"semanticsVersion":0`, `"semanticsVersion":999`, 1)
	document, _ = json.Marshal(map[string]any{"_key": "future", "draftConfig": json.RawMessage(raw)})
	if err := report.ObserveWorkspaceAuditDocument(ExplorersCollection, document); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range report.Entries {
		if entry.Key == "future" {
			found = entry.Classification == "future" && entry.DecodeError != ""
		}
	}
	if !found {
		t.Fatalf("future workspace was not isolated: %+v", report.Entries)
	}
}

func TestWorkspaceAuditDoesNotSilentlyDropMalformedAuthoritativePackets(t *testing.T) {
	report := WorkspaceAuditReport{}
	if err := report.ObserveWorkspaceAuditDocument(ExplorersCollection, []byte(`{"_key":"broken","draftConfig":{"unknown":"old shape"},"draftDigest":"prior"}`)); err != nil {
		t.Fatal(err)
	}
	if report.AuthoritativeWorkspaces != 1 || report.ByClass["invalid-or-unsupported"] != 1 || report.Entries[0].DigestRelation != "unresolved" {
		t.Fatalf("malformed packet disappeared: %+v", report)
	}
}

func TestWorkspaceAuditDiscoversRawRootWorkspaceWithoutStorageMetadata(t *testing.T) {
	workspace := auditFixture(t)
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["_key"] = json.RawMessage(`"raw-root"`)
	document["_rev"] = json.RawMessage(`"storage-only"`)
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	report := WorkspaceAuditReport{}
	if err := report.ObserveWorkspaceAuditDocument("unknown_workspace_copies", raw); err != nil {
		t.Fatal(err)
	}
	if report.DiscoveryCandidates != 1 || report.AuthoritativeWorkspaces != 0 || report.Entries[0].Path != "$" || report.Entries[0].Classification != "current" {
		t.Fatalf("root packet classification=%+v", report)
	}
}
