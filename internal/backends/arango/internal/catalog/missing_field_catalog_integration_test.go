package arango

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	storearango "github.com/calypr/loom/internal/backends/arango/internal/store"
	"github.com/calypr/loom/internal/catalog"
	"github.com/google/uuid"
)

// Durable control for the first-authorized-upload deadlock: the authscope
// project-binding gate (F-B27) asks this discovery which auth paths a project
// has recorded BEFORE ingest can bootstrap fhir_field_catalog. On a fresh
// database the collection does not exist, so a storage fault there is
// surfaced as 403 FORBIDDEN on the very first restricted-scope upload and the
// database can never receive its first generation. Discovery must answer
// "no recorded paths", the same posture the zero-path bootstrap branch of
// AuthorizeWrite is built for and the posture the postgres backend has
// because SetupMigrate pre-creates the table.

const missingCatalogTestDatabasePrefix = "loom_catalog_missing_"

func openMissingCatalogHarness(t *testing.T, budget time.Duration) (context.Context, *storearango.Client) {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL to run the missing-field-catalog Arango proof")
	}
	database := missingCatalogTestDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(database, missingCatalogTestDatabasePrefix) {
		t.Fatalf("verification database %q escapes the %s prefix", database, missingCatalogTestDatabasePrefix)
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	t.Cleanup(cancel)
	client, err := storearango.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatalf("open owned database %s: %v", database, err)
	}
	t.Logf("MISSINGCATALOG_DATABASE=%s", database)
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		request, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(database), nil)
		if err != nil {
			t.Errorf("build cleanup request for %s: %v", database, err)
			return
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Errorf("cleanup %s: %v", database, err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNotFound {
			t.Errorf("cleanup %s: HTTP %d", database, response.StatusCode)
		}
	})
	// Deliberately NO bootstrap: the whole point is a database that has
	// never ingested.
	return ctx, client
}

func TestDiscoverExistingAuthResourcePathsMissingCollectionIsNoPaths(t *testing.T) {
	ctx, client := openMissingCatalogHarness(t, 60*time.Second)
	store, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	for name, opts := range map[string]catalog.AuthResourcePathOptions{
		"generation scoped": {Project: "loom-bench", DatasetGeneration: "bench-first"},
		"all generations":   {Project: "loom-bench", AllGenerations: true},
	} {
		t.Run(name, func(t *testing.T) {
			paths, err := store.DiscoverExistingAuthResourcePaths(ctx, opts)
			if err != nil {
				t.Fatalf("missing fhir_field_catalog must answer no paths, got error: %v", err)
			}
			if len(paths) != 0 {
				t.Fatalf("fresh database must report zero recorded paths, got %v", paths)
			}
		})
	}
}
