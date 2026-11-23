package workspace

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestCurrentWorkspaceDoesNotPerformStoredDataMigration(t *testing.T) {
	workspace := NewWorkspace("Current")
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeCurrentWorkspace(raw); err != nil {
		t.Fatal(err)
	}
	var packet map[string]any
	if err := json.Unmarshal(raw, &packet); err != nil {
		t.Fatal(err)
	}
	packet["semanticsVersion"] = 3
	legacy, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeCurrentWorkspace(legacy); err == nil {
		t.Fatal("current decoder accepted an unmigrated workspace")
	}
	migrated, err := MigrateStoredWorkspace(legacy, CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(migrated.CanonicalJSON, raw) {
		t.Fatalf("offline migration changed the empty workspace meaning: %s", migrated.CanonicalJSON)
	}
	if _, err := DecodeCurrentWorkspace(migrated.CanonicalJSON); err != nil {
		t.Fatal(err)
	}
}
