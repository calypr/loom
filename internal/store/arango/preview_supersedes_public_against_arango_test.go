package arango

import (
	"context"
	"encoding/json"
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

	"github.com/google/uuid"
)

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
			dry, err := client.MigratePreviewSupersededIndexes(ctx, true)
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
			final, err := client.MigratePreviewSupersededIndexes(ctx, false)
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
			rerun, err := client.MigratePreviewSupersededIndexes(ctx, false)
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
