package arango

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestStoredWorkspaceMigrationMixedNilDigestAgainstArango(t *testing.T) {
	ctx, client, adapter, _ := draftCASDB(t)
	workspace := auditFixture(t)
	workspace.Documents[0].Construction.Steps = []authoringv2.ConstructionStep{}
	second := workspace.Documents[0]
	second.Output.ID, second.Output.Title = "more_patients", "More patients"
	second.Construction = &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{}}
	workspace.Documents = append(workspace.Documents, second)
	workspace.Tabs = append(workspace.Tabs, authoringv2.Tab{ID: second.Output.ID, Title: second.Output.Title, OutputID: second.Output.ID, Order: 1})
	canonical, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	at := bytes.LastIndex(canonical, []byte(`"steps":[]`))
	if at < 0 {
		t.Fatal("missing second empty sequence")
	}
	original := append([]byte(nil), canonical[:at]...)
	original = append(original, []byte(`"steps":null`)...)
	original = append(original, canonical[at+len(`"steps":[]`):]...)
	sum := sha256.Sum256(original)
	oldDigest := "sha256:" + hex.EncodeToString(sum[:])
	owner := workspaceMigrationSeed(t, ctx, adapter, "mixed-empty-nil", "Mixed lists", original, oldDigest, "snapshot-wp18")
	dry, err := MigrateStoredWorkspaces(ctx, client, true)
	if err != nil {
		t.Fatal(err)
	}
	if dry.Owners != 1 || dry.ChangedDigests != 1 {
		t.Fatalf("dry-run=%#v", dry)
	}
	if _, err := MigrateStoredWorkspaces(ctx, client, false); err != nil {
		t.Fatal(err)
	}
	migrated, err := adapter.Get(ctx, owner.Project, owner.ExplorerID)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.DraftVersion != owner.DraftVersion+1 || migrated.DraftDigest == oldDigest {
		t.Fatalf("owner did not advance exactly once: %#v", migrated)
	}
	current, err := authoringv2.DecodeCurrentWorkspace(migrated.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	for index, document := range current.Documents {
		if document.Construction == nil || document.Construction.Steps == nil || len(document.Construction.Steps) != 0 {
			t.Fatalf("document %d lost its empty construction", index)
		}
	}
	workspaceMigrationVerifyHistoryProjection(t, ctx, client, adapter, owner, migrated.DraftDigest)
	before := workspaceMigrationInventory(t, ctx, client)
	if _, err := MigrateStoredWorkspaces(ctx, client, false); err != nil {
		t.Fatal(err)
	}
	if after := workspaceMigrationInventory(t, ctx, client); !reflect.DeepEqual(before, after) {
		t.Fatal("mixed nil migration rerun changed stored state")
	}
}
