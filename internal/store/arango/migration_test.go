package arango

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	driver "github.com/arangodb/go-driver/v2/arangodb"
	"github.com/arangodb/go-driver/v2/utils"
	"github.com/google/uuid"
)

func TestPreviewSupersededIndexMigrationAgainstArango(t *testing.T) {
	endpoint, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if endpoint == "" || database == "" || !strings.HasPrefix(database, "loom_test_") {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must name a disposable loom_test_* database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := OpenExisting(ctx, endpoint, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())

	t.Run("legacy", func(t *testing.T) {
		collection := newMigrationTestCollection(t, client)
		old := createMigrationTestIndex(t, ctx, collection.Name(), collection, "payload.valueQuantity.code")
		candidates, err := previewMigrationTestCandidates(ctx, collection.Name(), collection)
		if err != nil || len(candidates) != 1 {
			t.Fatalf("legacy dry-run candidates=%+v err=%v", candidates, err)
		}
		indexesBefore, _ := collection.Indexes(ctx)
		for _, candidate := range candidates {
			if err := migratePreviewSupersededIndex(ctx, collection, candidate); err != nil {
				t.Fatal(err)
			}
		}
		assertMigrationIndexState(t, ctx, collection, old.ID, candidates[0].NewName, candidates[0].NewFields, candidates[0].NewStoredValues)
		if candidates, err := previewMigrationTestCandidates(ctx, collection.Name(), collection); err != nil || len(candidates) != 0 {
			t.Fatalf("rerun candidates=%+v err=%v, want none", candidates, err)
		}
		indexesAfter, _ := collection.Indexes(ctx)
		if len(indexesBefore) != 2 || len(indexesAfter) != 2 {
			t.Fatalf("dry-run/apply/rerun inventory sizes=%d/%d", len(indexesBefore), len(indexesAfter))
		}
	})

	t.Run("already-migrated", func(t *testing.T) {
		collection := newMigrationTestCollection(t, client)
		candidate := syntheticCategoryCandidate(collection.Name(), "payload.status")
		createMigrationTestIndexExact(t, ctx, collection, candidate.NewName, candidate.NewFields, candidate.NewStoredValues)
		candidates, err := previewMigrationTestCandidates(ctx, collection.Name(), collection)
		if err != nil || len(candidates) != 0 {
			t.Fatalf("already-migrated candidates=%+v err=%v", candidates, err)
		}
		indexes, err := collection.Indexes(ctx)
		if err != nil || len(indexes) != 2 {
			t.Fatalf("already-migrated inventory=%d err=%v", len(indexes), err)
		}
	})

	t.Run("mixed-and-rerun", func(t *testing.T) {
		collection := newMigrationTestCollection(t, client)
		old := createMigrationTestIndex(t, ctx, collection.Name(), collection, "payload.status")
		current := syntheticCategoryCandidate(collection.Name(), "payload.valueQuantity.code")
		createMigrationTestIndexExact(t, ctx, collection, current.NewName, current.NewFields, current.NewStoredValues)
		unrelatedFields := []string{"project", "dataset_generation", "auth_resource_path", "payload.id", "payload.status"}
		createMigrationTestIndexExact(t, ctx, collection, previewCoveringIndexName(collection.Name(), unrelatedFields), unrelatedFields, nil)
		candidates, err := previewMigrationTestCandidates(ctx, collection.Name(), collection)
		if err != nil || len(candidates) != 1 {
			t.Fatalf("mixed dry-run candidates=%+v err=%v", candidates, err)
		}
		for _, candidate := range candidates {
			if err := migratePreviewSupersededIndex(ctx, collection, candidate); err != nil {
				t.Fatal(err)
			}
		}
		if candidates, err := previewMigrationTestCandidates(ctx, collection.Name(), collection); err != nil || len(candidates) != 0 {
			t.Fatalf("mixed rerun candidates=%+v err=%v", candidates, err)
		}
		indexes, err := collection.Indexes(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := findIndexByID(indexes, old.ID); ok {
			t.Fatal("legacy index remains after mixed migration")
		}
		if _, ok := findIndexByName(indexes, current.NewName); !ok {
			t.Fatal("pre-existing current replacement was removed")
		}
		if _, ok := findIndexByName(indexes, previewCoveringIndexName(collection.Name(), unrelatedFields)); !ok {
			t.Fatal("unrelated preview index was removed")
		}
	})

	t.Run("delete-failure-rolls-back-only-created-candidate", func(t *testing.T) {
		collection := newMigrationTestCollection(t, client)
		old := createMigrationTestIndex(t, ctx, collection.Name(), collection, "payload.status")
		for _, path := range []string{"payload.a", "payload.b", "payload.c"} {
			createMigrationTestCurrentIndex(t, ctx, collection, path)
		}
		candidates, err := previewMigrationTestCandidates(ctx, collection.Name(), collection)
		if err != nil || len(candidates) != 1 {
			t.Fatalf("dry-run candidates=%+v err=%v", candidates, err)
		}
		fault := &previewMigrationDeleteFault{previewMigrationCollection: collection, oldID: old.ID}
		err = migratePreviewSupersededIndex(ctx, fault, candidates[0])
		if err == nil || !strings.Contains(err.Error(), "rolled back newly created replacement") {
			t.Fatalf("old-delete failure = %v, want confirmed rollback", err)
		}
		indexes, readErr := collection.Indexes(ctx)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, ok := findIndexByID(indexes, old.ID); !ok {
			t.Fatal("old index was not retained after confirmed delete failure")
		}
		if _, ok := findIndexByName(indexes, candidates[0].NewName); ok {
			t.Fatal("newly created candidate remained after confirmed old-delete failure")
		}
		if err := migratePreviewSupersededIndex(ctx, collection, candidates[0]); err != nil {
			t.Fatalf("normal retry after rollback: %v", err)
		}
		assertMigrationIndexState(t, ctx, collection, old.ID, candidates[0].NewName, candidates[0].NewFields, candidates[0].NewStoredValues)
	})

	t.Run("delete-error-after-old-gone-keeps-candidate", func(t *testing.T) {
		collection := newMigrationTestCollection(t, client)
		old := createMigrationTestIndex(t, ctx, collection.Name(), collection, "payload.status")
		candidates, err := previewMigrationTestCandidates(ctx, collection.Name(), collection)
		if err != nil || len(candidates) != 1 {
			t.Fatalf("dry-run candidates=%+v err=%v", candidates, err)
		}
		fault := &previewMigrationDeleteFault{previewMigrationCollection: collection, oldID: old.ID, deleteThenError: true}
		err = migratePreviewSupersededIndex(ctx, fault, candidates[0])
		if err == nil || !strings.Contains(err.Error(), "after deletion; verified replacement retained") {
			t.Fatalf("lost old-delete response = %v, want replacement retained", err)
		}
		indexes, readErr := collection.Indexes(ctx)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, ok := findIndexByID(indexes, old.ID); ok {
			t.Fatal("old index remains after underlying delete succeeded")
		}
		if _, ok := findIndexByName(indexes, candidates[0].NewName); !ok {
			t.Fatal("verified replacement was removed after uncertain delete response")
		}
		if err := migratePreviewSupersededIndex(ctx, collection, candidates[0]); err != nil {
			t.Fatalf("idempotent retry after lost response: %v", err)
		}
	})

	t.Run("delete-failure-retains-preexisting-candidate", func(t *testing.T) {
		collection := newMigrationTestCollection(t, client)
		old := createMigrationTestIndex(t, ctx, collection.Name(), collection, "payload.status")
		candidate := syntheticCategoryCandidate(collection.Name(), "payload.status")
		createMigrationTestIndexExact(t, ctx, collection, candidate.NewName, candidate.NewFields, candidate.NewStoredValues)
		createMigrationTestCurrentIndex(t, ctx, collection, "payload.a")
		createMigrationTestCurrentIndex(t, ctx, collection, "payload.b")
		candidate.OldID = old.ID
		fault := &previewMigrationDeleteFault{previewMigrationCollection: collection, oldID: old.ID}
		err := migratePreviewSupersededIndex(ctx, fault, candidate)
		if err == nil || !strings.Contains(err.Error(), "pre-existing verified replacement retained") {
			t.Fatalf("old-delete failure with pre-existing replacement = %v", err)
		}
		indexes, readErr := collection.Indexes(ctx)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, ok := findIndexByID(indexes, old.ID); !ok {
			t.Fatal("old index was not retained after delete failure")
		}
		if _, ok := findIndexByName(indexes, candidate.NewName); !ok {
			t.Fatal("pre-existing replacement was incorrectly rolled back")
		}
	})

	t.Run("stored-value-legacy", func(t *testing.T) {
		collection := newMigrationTestCollection(t, client)
		oldFields := append([]string(nil), previewQuantityCategoryLegacyFields...)
		oldName := previewCoveringIndexNameWithStoredValues("Observation", oldFields, previewQuantityCategoryStoredValues)
		newName := previewCoveringIndexNameWithStoredValues("Observation", append(append([]string(nil), oldFields...), "resourceType"), previewQuantityCategoryStoredValues)
		indexes, err := collection.Indexes(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := findIndexByName(indexes, oldName); exists {
			t.Fatalf("disposable test DB already has legacy index %q", oldName)
		}
		if _, exists := findIndexByName(indexes, newName); exists {
			t.Fatalf("disposable test DB already has replacement index %q", newName)
		}
		old := createMigrationTestIndexExact(t, ctx, collection, oldName, oldFields, previewQuantityCategoryStoredValues)
		candidates, err := previewMigrationTestCandidates(ctx, "Observation", collection)
		if err != nil || len(candidates) != 1 {
			t.Fatalf("stored-value dry-run candidates=%+v err=%v", candidates, err)
		}
		candidate := candidates[0]
		if candidate.Kind != "observation-quantity-stored-values" {
			t.Fatalf("candidate kind=%q", candidate.Kind)
		}
		if err := migratePreviewSupersededIndex(ctx, collection, candidate); err != nil {
			t.Fatal(err)
		}
		assertMigrationIndexState(t, ctx, collection, old.ID, candidate.NewName, candidate.NewFields, candidate.NewStoredValues)
	})
}

type previewMigrationDeleteFault struct {
	previewMigrationCollection
	oldID           string
	deleteThenError bool
	failed          bool
}

func (collection *previewMigrationDeleteFault) DeleteIndexByID(ctx context.Context, id string) error {
	if id != collection.oldID || collection.failed {
		return collection.previewMigrationCollection.DeleteIndexByID(ctx, id)
	}
	collection.failed = true
	if collection.deleteThenError {
		if err := collection.previewMigrationCollection.DeleteIndexByID(ctx, id); err != nil {
			return err
		}
	}
	return errors.New("injected old-index delete response failure")
}

func previewMigrationTestCandidates(ctx context.Context, collectionName string, collection previewMigrationCollection) ([]PreviewSupersededIndex, error) {
	indexes, err := collection.Indexes(ctx)
	if err != nil {
		return nil, err
	}
	_, candidates, _ := auditPreviewIndexCollection(collectionName, indexes)
	return candidates, nil
}

func TestPreviewSupersededIndexClassificationRejectsHashAndTypeMismatch(t *testing.T) {
	legacy := previewIndexResponse(previewCoveringIndexName("Observation", []string{"project", "dataset_generation", "payload.status", "auth_resource_path"}), "Observation/legacy", []string{"project", "dataset_generation", "payload.status", "auth_resource_path"})
	candidate, ok := previewSupersededCandidate("Observation", legacy)
	if !ok || candidate.NewName != previewCoveringIndexName("Observation", []string{"project", "dataset_generation", "resourceType", "payload.status", "auth_resource_path"}) {
		t.Fatalf("field-only category classification=%+v ok=%v", candidate, ok)
	}
	bad := legacy
	bad.Name = "loom_pivot_preview_wrong_hash"
	if _, ok := previewSupersededCandidate("Observation", bad); ok {
		t.Fatal("wrong hash was classified for deletion")
	}
	if _, ok := previewSupersededCandidate("Observation", driver.IndexResponse{Type: driver.IndexType("hash"), RegularIndex: legacy.RegularIndex, IndexSharedOptions: legacy.IndexSharedOptions}); ok {
		t.Fatal("non-persistent index was classified for deletion")
	}
}

func newMigrationTestCollection(t *testing.T, client *Client) driver.Collection {
	t.Helper()
	name := "wp19_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	collection, err := client.db.CreateCollectionV2(context.Background(), name, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := collection.RemoveWithOptions(ctx, nil); err != nil {
			t.Errorf("remove disposable collection: %v", err)
		}
	})
	return collection
}

func syntheticCategoryCandidate(collection, path string) PreviewSupersededIndex {
	oldFields := []string{"project", "dataset_generation", path, "auth_resource_path"}
	newFields := []string{"project", "dataset_generation", "resourceType", path, "auth_resource_path"}
	return PreviewSupersededIndex{Collection: collection, Kind: "category-field-only", OldName: previewCoveringIndexName(collection, oldFields), OldFields: oldFields, OldStoredValues: []string{}, NewName: previewCoveringIndexName(collection, newFields), NewFields: newFields, NewStoredValues: []string{}}
}

func createMigrationTestIndex(t *testing.T, ctx context.Context, collectionName string, collection previewMigrationCollection, path string) driver.IndexResponse {
	t.Helper()
	fields := []string{"project", "dataset_generation", path, "auth_resource_path"}
	return createMigrationTestIndexExact(t, ctx, collection, previewCoveringIndexName(collectionName, fields), fields, nil)
}

func createMigrationTestIndexExact(t *testing.T, ctx context.Context, collection previewMigrationCollection, name string, fields, stored []string) driver.IndexResponse {
	t.Helper()
	index, _, err := collection.EnsurePersistentIndex(ctx, fields, &driver.CreatePersistentIndexOptions{Name: name, StoredValues: stored, Sparse: utils.NewType(false), Unique: utils.NewType(false)})
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func assertMigrationIndexState(t *testing.T, ctx context.Context, collection previewMigrationCollection, oldID, newName string, fields, stored []string) {
	t.Helper()
	indexes, err := collection.Indexes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findIndexByID(indexes, oldID); ok {
		t.Fatalf("old index %q remains", oldID)
	}
	index, ok := findIndexByName(indexes, newName)
	if !ok || !previewCoveringIndexMatchesDefinition(index, fields, stored) {
		t.Fatalf("replacement missing or mismatched: %+v", index)
	}
}

func createMigrationTestCurrentIndex(t *testing.T, ctx context.Context, collection driver.Collection, path string) driver.IndexResponse {
	t.Helper()
	candidate := syntheticCategoryCandidate(collection.Name(), path)
	return createMigrationTestIndexExact(t, ctx, collection, candidate.NewName, candidate.NewFields, candidate.NewStoredValues)
}

func wp19PublicTestEndpoint(t *testing.T) string {
	t.Helper()
	endpoint, database := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL")), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	parsed, err := url.Parse(endpoint)
	if err != nil || endpoint == "" || !strings.HasPrefix(database, "loom_test_") {
		t.Skip("set LOOM_TEST_ARANGO_URL and a disposable LOOM_TEST_ARANGO_DATABASE=loom_test_* authorization guard")
	}
	ip := net.ParseIP(parsed.Hostname())
	if parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		t.Skip("WP19 public migration tests require a local loopback Arango endpoint")
	}
	return strings.TrimRight(endpoint, "/")
}

func wp19DeleteOwnedDatabase(t *testing.T, endpoint, database string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint+"/_api/database/"+url.PathEscape(database), nil)
	if err != nil {
		t.Errorf("build owned database cleanup: %v", err)
		return
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Errorf("delete owned database %s: %v", database, err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNotFound {
		t.Errorf("delete owned database %s: HTTP %d", database, response.StatusCode)
	}
}

func wp19CollectionNames(t *testing.T, ctx context.Context, client *Client) []string {
	t.Helper()
	collections, err := client.db.Collections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(collections))
	for _, collection := range collections {
		names = append(names, collection.Name())
	}
	sort.Strings(names)
	return names
}

func wp19Rows(t *testing.T, ctx context.Context, client *Client, collection string) []map[string]any {
	t.Helper()
	rows := []map[string]any{}
	err := client.QueryRows(ctx, "FOR doc IN @@collection SORT doc._key RETURN doc", 16, map[string]interface{}{"@collection": collection}, func(row map[string]any) error { rows = append(rows, row); return nil })
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestPreviewSupersededPublicMigrationVersionedAgainstArango(t *testing.T) {
	endpoint := wp19PublicTestEndpoint(t)
	for _, shape := range []string{"legacy", "current", "mixed"} {
		t.Run(shape, func(t *testing.T) {
			database := "loom_test_wp19_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			defer wp19DeleteOwnedDatabase(t, endpoint, database)
			client, err := Open(ctx, endpoint, database)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close(context.Background())
			collection, err := client.db.CreateCollectionV2(ctx, "Observation", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := collection.CreateDocument(ctx, map[string]interface{}{"_key": "preserve_me", "project": "test", "resourceType": "Observation", "payload": map[string]interface{}{"status": "final"}, "unrelated": "sentinel"}); err != nil {
				t.Fatal(err)
			}
			unrelated := createMigrationTestIndexExact(t, ctx, collection, "unrelated_wp19_index", []string{"unrelated"}, nil)
			var oldID string
			if shape != "current" {
				oldID = createMigrationTestIndex(t, ctx, collection.Name(), collection, "payload.status").ID
			}
			var currentID, currentName string
			if shape != "legacy" {
				candidate := syntheticCategoryCandidate(collection.Name(), "payload.valueQuantity.code")
				index := createMigrationTestIndexExact(t, ctx, collection, candidate.NewName, candidate.NewFields, candidate.NewStoredValues)
				currentID, currentName = index.ID, index.Name
			}
			beforeNames := wp19CollectionNames(t, ctx, client)
			beforeIndexes, err := collection.Indexes(ctx)
			if err != nil {
				t.Fatal(err)
			}
			beforeDocuments := wp19Rows(t, ctx, client, collection.Name())
			dry, err := client.PreviewIndexes().MigratePreviewSupersededIndexes(ctx, true)
			if err != nil {
				t.Fatal(err)
			}
			wantLegacy := 0
			if shape != "current" {
				wantLegacy = 1
			}
			if dry.Action != "dry-run" || dry.LegacyCandidateCount != wantLegacy || dry.LedgerPresent {
				t.Fatalf("dry-run=%+v, want %d legacy candidates and no ledger", dry, wantLegacy)
			}
			encoded, err := json.Marshal(dry)
			if err != nil {
				t.Fatal(err)
			}
			var dryJSON map[string]any
			if err := json.Unmarshal(encoded, &dryJSON); err != nil {
				t.Fatal(err)
			}
			if dryJSON["wouldRecordVersion"] != true {
				t.Fatalf("dry-run must disclose pending version record: %s", encoded)
			}
			if got := wp19CollectionNames(t, ctx, client); !reflect.DeepEqual(got, beforeNames) {
				t.Fatalf("dry-run created/removed collections: before=%v after=%v", beforeNames, got)
			}
			afterDryIndexes, err := collection.Indexes(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !samePreviewIndexesExcept(beforeIndexes, afterDryIndexes) {
				t.Fatal("dry-run changed index definitions or IDs")
			}
			if got := wp19Rows(t, ctx, client, collection.Name()); !reflect.DeepEqual(got, beforeDocuments) {
				t.Fatalf("dry-run changed documents: %v", got)
			}
			final, err := client.PreviewIndexes().MigratePreviewSupersededIndexes(ctx, false)
			if err != nil {
				t.Fatal(err)
			}
			if final.LegacyCandidateCount != 0 || !final.LedgerPresent {
				t.Fatalf("apply did not finish versioned migration: %+v", final)
			}
			afterIndexes, err := collection.Indexes(ctx)
			if err != nil {
				t.Fatal(err)
			}
			replacementID := ""
			if oldID != "" {
				if _, found := findIndexByID(afterIndexes, oldID); found {
					t.Fatal("legacy index remains")
				}
				candidate := syntheticCategoryCandidate(collection.Name(), "payload.status")
				replacement, found := findIndexByName(afterIndexes, candidate.NewName)
				replacementID = replacement.ID
				if !found || !previewCoveringIndexMatchesDefinition(replacement, candidate.NewFields, candidate.NewStoredValues) {
					t.Fatalf("exact replacement missing: %+v", replacement)
				}
			}
			if currentID != "" {
				index, found := findIndexByName(afterIndexes, currentName)
				if !found || index.ID != currentID {
					t.Fatalf("pre-existing current index changed: %+v", index)
				}
			}
			if _, found := findIndexByID(afterIndexes, unrelated.ID); !found {
				t.Fatal("unrelated index was removed")
			}
			if !samePreviewIndexesExcept(beforeIndexes, afterIndexes, oldID, replacementID) {
				t.Fatal("apply changed unrelated index definitions")
			}
			if got := wp19Rows(t, ctx, client, collection.Name()); !reflect.DeepEqual(got, beforeDocuments) {
				t.Fatal("apply changed user documents or revisions")
			}
			wantCollections := append(append([]string{}, beforeNames...), previewSupersedesMigrationLedger)
			sort.Strings(wantCollections)
			if got := wp19CollectionNames(t, ctx, client); !reflect.DeepEqual(got, wantCollections) {
				t.Fatalf("apply changed collections beyond ledger: got=%v want=%v", got, wantCollections)
			}
			markers := wp19Rows(t, ctx, client, previewSupersedesMigrationLedger)
			if len(markers) != 1 || markers[0]["_key"] != previewSupersedesMigrationVersion || markers[0]["version"] != previewSupersedesMigrationVersion || markers[0]["_rev"] == nil {
				t.Fatalf("expected exactly one versioned ledger marker with revision: %v", markers)
			}
			markerRevision := fmt.Sprint(markers[0]["_rev"])
			rerun, err := client.PreviewIndexes().MigratePreviewSupersededIndexes(ctx, false)
			if err != nil {
				t.Fatal(err)
			}
			if !rerun.LedgerPresent || rerun.LegacyCandidateCount != 0 {
				t.Fatalf("rerun=%+v", rerun)
			}
			markersAfter := wp19Rows(t, ctx, client, previewSupersedesMigrationLedger)
			if !reflect.DeepEqual(markersAfter, markers) || len(markersAfter) != 1 || fmt.Sprint(markersAfter[0]["_rev"]) != markerRevision {
				t.Fatalf("rerun rewrote or duplicated ledger: before=%v after=%v", markers, markersAfter)
			}
			rerunIndexes, err := collection.Indexes(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !samePreviewIndexesExcept(afterIndexes, rerunIndexes) {
				t.Fatal("rerun changed indexes")
			}
			if got := wp19Rows(t, ctx, client, collection.Name()); !reflect.DeepEqual(got, beforeDocuments) {
				t.Fatal("rerun changed documents")
			}
		})
	}
}

func TestOpenExistingMissingDatabaseDoesNotCreateAgainstArango(t *testing.T) {
	endpoint := wp19PublicTestEndpoint(t)
	database := "loom_test_wp19_missing_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	defer wp19DeleteOwnedDatabase(t, endpoint, database)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := OpenExisting(ctx, endpoint, database)
	if client != nil {
		defer client.Close(context.Background())
	}
	if err == nil {
		t.Fatal("OpenExisting unexpectedly opened a missing database")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/_db/"+url.PathEscape(database)+"/_api/database/current", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("missing database was created or probe failed: HTTP %d", response.StatusCode)
	}
}
