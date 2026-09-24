package ingest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bmeg/jsonschemagraph/graph"
	"github.com/calypr/loom/internal/catalog"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestLoadFilePersistsSemanticInventoryFromSharedEmitter(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "Observation.ndjson")
	data := []byte(`{"resourceType":"Observation","id":"obs-1","status":"final","code":{"coding":[{"system":"urn:inventory","version":"v1","code":"panel"}]},"component":[{"code":{"coding":[{"system":"urn:inventory","version":"v1","code":"glucose","display":"Glucose"}]},"valueQuantity":{"value":1,"unit":"mg"}}]}` + "\n")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	schema, err := graph.Load(repoPath(t, "schemas", "graph-fhir.json"))
	if err != nil {
		t.Fatal(err)
	}
	opts := normalizeLoadOptions(LoadOptions{
		Project:     "project",
		MetaDir:     root,
		BatchSize:   1,
		WorkerCount: 1,
		WriterCount: 1,
	})
	var inventoryDocs []json.RawMessage
	var inventoryOverwrite []bool
	result, err := loadFile(context.Background(), opts, nil, schema, file, "generation-1", false, time.Now(), 0, 0,
		func(_ context.Context, _ *arangostore.Client, collection string, docs []json.RawMessage, overwrite bool, _ string) error {
			if collection == catalog.SemanticInventoryCollection {
				inventoryDocs = append(inventoryDocs, docs...)
				inventoryOverwrite = append(inventoryOverwrite, overwrite)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventoryDocs) != 4 || result.SemanticInventoryBatches != 4 {
		message := "no row error sample"
		if len(result.RowErrors) > 0 {
			message = result.RowErrors[0].Message
		}
		t.Fatalf("inventory docs=%d batches=%d rows=%d validation=%d sample=%s", len(inventoryDocs), result.SemanticInventoryBatches, result.Rows, result.ValidationErrors, message)
	}
	for _, overwrite := range inventoryOverwrite {
		if !overwrite {
			t.Fatalf("inventory overwrite modes = %v, want source-key replacement", inventoryOverwrite)
		}
	}
	observations := make(map[string]catalog.SemanticObservation, len(inventoryDocs))
	for _, document := range inventoryDocs {
		var contribution catalog.SemanticInventoryContribution
		if err := json.Unmarshal(document, &contribution); err != nil {
			t.Fatal(err)
		}
		if contribution.SourceID != "Observation.ndjson#1" || contribution.AuthResourcePath != "" {
			t.Fatalf("inventory source/scope = %q/%q, want stable relative source locator and unscoped path", contribution.SourceID, contribution.AuthResourcePath)
		}
		if contribution.ConceptID == "" || contribution.BindingID == "" {
			t.Fatalf("inventory identity/observation = %+v", contribution)
		}
		observations[contribution.Observation.Source.Path+":"+contribution.Observation.Key.Code] = contribution.Observation
	}
	for _, path := range []string{"code", "component[].code"} {
		if slot := observations[path+":"]; slot.Role != catalog.SemanticRoleCategoricalSlot || slot.Key.System != "urn:inventory" || slot.Value.Selector != path+".coding[].code" {
			t.Fatalf("categorical slot %s = %+v", path, slot)
		}
	}
	if status := observations["status:"]; status.Role != catalog.SemanticRoleCategoricalSlot || status.Value.Selector != "status" {
		t.Fatalf("status slot = %+v", status)
	}
	if glucose := observations["component[]:glucose"]; glucose.RuleHint != catalog.SemanticRuleHintCodedValueV1 || glucose.Key.Version != "v1" || glucose.Value.Selector != "valueQuantity.value" || glucose.ObservedUnits[0] != "mg" {
		t.Fatalf("paired glucose observation = %+v", glucose)
	}
}

func TestSemanticInventorySourceLocatorIncludesRelativeDirectory(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "a", "Observation.ndjson")
	second := filepath.Join(root, "b", "Observation.ndjson")
	firstID := semanticInventorySourceRecordID(semanticInventorySourceFileID(root, first), 7)
	secondID := semanticInventorySourceRecordID(semanticInventorySourceFileID(root, second), 7)
	if firstID == secondID {
		t.Fatalf("same-basename source files collided at %q", firstID)
	}
	if firstID != "a/Observation.ndjson#7" || secondID != "b/Observation.ndjson#7" {
		t.Fatalf("relative source IDs = %q and %q", firstID, secondID)
	}
}
