package generation

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/calypr/loom/internal/ingest/source"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	"github.com/calypr/loom/internal/semantic"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestLoadFilePersistsSemanticInventoryFromSharedEmitter(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "Observation.ndjson")
	data := []byte(`{"resourceType":"Observation","id":"obs-1","status":"final","code":{"coding":[{"system":"urn:inventory","version":"v1","code":"panel"}]},"component":[{"code":{"coding":[{"system":"urn:inventory","version":"v1","code":"glucose","display":"Glucose"}]},"valueQuantity":{"value":1,"unit":"mg"}}]}` + "\n")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	schema := source.LoadRuntimeFHIRSchema()
	opts := normalizeLoadOptions(LoadOptions{
		Project:     "project",
		MetaDir:     root,
		BatchSize:   1,
		WorkerCount: 1,
		WriterCount: 1,
	})
	var inventoryCollectionWrites []string
	result, err := fileLoader{options: opts, client: nil, schema: schema, datasetGeneration: "generation-1", overwrite: false, insert: func(_ context.Context, _ *arangostore.Client, collection string, docs []json.RawMessage, overwrite bool, _ string) error {
		if collection == catalogarango.SemanticCoverageCollection {
			t.Fatalf("normal generation load wrote optional semantic coverage collection %q", collection)
		}
		if collection == semantic.Collection || collection == semantic.EntryCollection {
			inventoryCollectionWrites = append(inventoryCollectionWrites, collection)
		}
		return nil
	}}.load(context.Background(), file, time.Now(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventoryCollectionWrites) != 0 || result.SemanticInventoryBatches != 0 || len(result.SemanticInventoryEntryRuns) == 0 {
		message := "no row error sample"
		if len(result.RowErrors) > 0 {
			message = result.RowErrors[0].Message
		}
		t.Fatalf("inventory writes=%v batches=%d runs=%d rows=%d validation=%d sample=%s", inventoryCollectionWrites, result.SemanticInventoryBatches, len(result.SemanticInventoryEntryRuns), result.Rows, result.ValidationErrors, message)
	}
	var inventoryDocs []json.RawMessage
	err = mergeSemanticInventoryEntryRuns(context.Background(), result.SemanticInventoryEntryRuns, func(partial semantic.EntryPartial) error {
		document := semantic.EntryDocument(partial, "project", "generation-1", semantic.BuildID("project", "generation-1"), semantic.SourceFile)
		encoded, err := json.Marshal(document)
		if err != nil {
			return err
		}
		inventoryDocs = append(inventoryDocs, encoded)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inventoryDocs) != 2 {
		t.Fatalf("materialized entries = %d, want two", len(inventoryDocs))
	}
	var entry map[string]any
	if err := json.Unmarshal(inventoryDocs[0], &entry); err != nil {
		t.Fatal(err)
	}
	if entry["resource_type"] != "Observation" || entry["auth_resource_path"] != "" {
		t.Fatalf("inventory resource/scope = %q/%q", entry["resource_type"], entry["auth_resource_path"])
	}
	if entry["concept_id"] == "" || entry["binding_id"] == "" {
		t.Fatalf("inventory identity = %+v", entry)
	}
	ownerOptions := opts
	ownerOptions.SourceFileOwner = "Observation"
	var uploadedEntries []json.RawMessage
	result, err = fileLoader{options: ownerOptions, client: nil, schema: schema, datasetGeneration: "generation-1", overwrite: false, insert: func(_ context.Context, _ *arangostore.Client, collection string, docs []json.RawMessage, overwrite bool, _ string) error {
		if collection == semantic.Collection {
			t.Fatal("single-file upload wrote raw inventory contributions")
		}
		if collection == semantic.EntryCollection {
			if !overwrite {
				t.Fatal("single-file inventory entry write must replace prior rows")
			}
			uploadedEntries = append(uploadedEntries, docs...)
		}
		return nil
	}}.load(context.Background(), file, time.Now(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(uploadedEntries) != 2 || len(result.SemanticInventoryEntryRuns) != 0 {
		t.Fatalf("single-file entry writes=%d remaining runs=%d", len(uploadedEntries), len(result.SemanticInventoryEntryRuns))
	}
}

func TestLoadFileWaitsForInventorySpillOwnershipAfterWriterFailure(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "Observation.ndjson")
	data := []byte(`{"resourceType":"Observation","id":"obs-1","status":"final","code":{"coding":[{"system":"urn:inventory","code":"panel"}]},"component":[{"code":{"coding":[{"system":"urn:inventory","code":"glucose","display":"Glucose"}]},"valueQuantity":{"value":1,"unit":"mg"}}]}` + "\n")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := filepath.Glob(filepath.Join(os.TempDir(), "loom-semantic-entry-*.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	opts := normalizeLoadOptions(LoadOptions{Project: "project", MetaDir: root, BatchSize: 1, WorkerCount: 1, WriterCount: 1, FailFast: true})
	_, err = fileLoader{options: opts, client: nil, schema: source.LoadRuntimeFHIRSchema(), datasetGeneration: "generation-1", overwrite: false, insert: func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error {
		return errors.New("forced writer failure")
	}}.load(context.Background(), file, time.Now(), 0, 0)
	if err == nil || !strings.Contains(err.Error(), "forced writer failure") {
		t.Fatalf("load error = %v, want writer failure", err)
	}
	after, err := filepath.Glob(filepath.Join(os.TempDir(), "loom-semantic-entry-*.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	known := make(map[string]struct{}, len(before))
	for _, path := range before {
		known[path] = struct{}{}
	}
	for _, path := range after {
		if _, existed := known[path]; !existed {
			t.Errorf("failed file load leaked inventory spill %q", path)
		}
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
