package ingest

// DURABLE guarantee pin for the pre-STAGED failure leg of loadGeneration
// (generation_orchestration.go:179-190 deferred cleanup + :289-295
// GenerationLoadIncompleteError). Recorded as coverage candidate (c) in
// FINDINGS.md pass 2026-11-04i: no prior durable test drives a real mid-load
// failure against ArangoDB and observes the store-visible consequences.
//
// PINNED CONTRACT (pre-stated before execution):
//   1. A load that passes preflight then accrues row errors returns
//      GenerationLoadIncompleteError and the persisted manifest is FAILED —
//      never left LOADING (evidence: store-visible ReadManifest readback;
//      guard-level CAS-vs-blind-UPDATE equivalence is pinned by
//      internal/dataset/arango/generation_activation_cas_e2e_test.go, not
//      by this file).
//   2. The failed generation is never activatable: Store.Activate returns
//      publication.ErrGenerationNotReady.
//   3. The failed generation is never active: ResolveActiveManifest returns
//      publication.ErrNoActiveGeneration AND the lifecycle active document's
//      manifestKey (read raw, so the resolver's own STAGED-only filter cannot
//      mask a dangling pointer to the FAILED manifest) is absent.
//   4. It cannot be silently recreated: a second Load for the same dataset
//      reference is rejected sequentially at the single-query
//      publicationarango.CreateManifest claim with
//      publication.ErrManifestAlreadyExists. (Concurrent-claim behavior is
//      not pinned by this test.)
//   5. The paired semantic inventory build is marked failed
//      (catalog.SemanticInventoryBuildCollection doc reaches state "failed").
//   6. Positive control: a clean load yields a STAGED manifest reachable via
//      ResolveActiveManifest; persisted row count is checked against the acked
//      count (3/3); row-content equality is not asserted.
//
// FAULT MODEL (measured, not asserted-by-hope; see FINDINGS repair-27 review):
//   M1 neutralize the deferred FAILED transition (generation_orchestration.go
//     cleanup func returns nil) -> the observing RED witness is PIN 1.
//     PINS 2-3 stay green under M1 (their guards reject every non-staged state
//     identically, LOADING included); PIN 4 is state-agnostic (existence-based
//     claim). PINS 2-4 are contract invariants, not sensors of the transition.
//   M2 neutralize FailSemanticInventoryBuild -> the observing RED witness is
//     PIN 5/5b (state stays 'running', diagnostics never set).
//   M3 set the project's active.manifestKey to the failed manifest before the
//     incomplete-error return -> the observing RED witness is PIN 3's raw
//     active-document read (the resolver's STAGED-only join filter alone would
//     stay green; measured via worker fault run).
//   Each deferred cleanup leg therefore has exactly one dedicated sensor.
//
// Gating and owned-database lifecycle follow the package precedent
// keycollide_generated_e2e_test.go (skip-clean without LOOM_TEST_ARANGO_URL).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	catalog "github.com/calypr/loom/internal/catalog"
	publication "github.com/calypr/loom/internal/dataset"
	publicationarango "github.com/calypr/loom/internal/dataset/arango"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

const failLegPrefix = "loom_verify_failleg_"

func failLegEndpoint(t *testing.T) string {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL to run the generation FAILED-cleanup guarantee e2e")
	}
	return endpoint
}

func failLegPatientRow(index int) string {
	return fmt.Sprintf(`{"resourceType":"Patient","id":"leg-%03d","gender":"female"}`, index)
}

func TestGenerationLoadFailureMarksManifestFailedAndBlocksActivation(t *testing.T) {
	endpoint := failLegEndpoint(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	t.Run("pre_staged_fault", func(t *testing.T) {
		database := failLegPrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
		t.Cleanup(func() { failLegDropDatabase(t, endpoint, database) })
		metaDir := t.TempDir()
		rows := make([]string, 0, 26)
		for i := 0; i < 25; i++ {
			rows = append(rows, failLegPatientRow(i))
		}
		// Malformed line placed beyond the 10-row preflight sample window
		// (preflight.go:13 defaultPreflightSampleRows) so the claim, bootstrap
		// and inventory begin all execute before the failure lands.
		rows = append(rows, `{oops`)
		for i := 25; i < 30; i++ {
			rows = append(rows, failLegPatientRow(i))
		}
		if err := os.WriteFile(filepath.Join(metaDir, "Patient.ndjson"), []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		project := "FAILLEG_FAULT"
		ref, err := publication.NewRef(project, "gen-fault")
		if err != nil {
			t.Fatalf("ref: %v", err)
		}
		_, loadErr := Load(ctx, LoadOptions{
			ConnectionOptions: arangostore.ConnectionOptions{URL: endpoint, Database: database},
			Schema:            repoPath(t, "schemas", "graph-fhir.json"),
			MetaDir:           metaDir,
			Project:           project,
			Dataset:           &ref,
			BatchSize:         10,
			ProgressEvery:     100,
		})
		if loadErr == nil {
			t.Fatalf("PIN 1 pre-fault: expected GenerationLoadIncompleteError for a row beyond the preflight sample, got nil")
		}
		var incomplete *GenerationLoadIncompleteError
		if !errors.As(loadErr, &incomplete) {
			t.Fatalf("PIN 1 fault class: expected *GenerationLoadIncompleteError, got %T: %v", loadErr, loadErr)
		}
		t.Logf("FAILLEG fault load | %v", loadErr)

		client, err := arangostore.Open(ctx, endpoint, database)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer func() { _ = client.Close(context.Background()) }()
		store, err := publicationarango.New(client)
		if err != nil {
			t.Fatalf("lifecycle store: %v", err)
		}

		manifest, err := store.ReadManifest(ctx, ref)
		if err != nil {
			t.Fatalf("PIN 1 readback: ReadManifest after failed load: %v", err)
		}
		if manifest.State != publication.StateFailed {
			t.Errorf("PIN 1 | failed load left manifest in state %q, want %q (deferred cleanup leg must reach the store; ReadManifest is the store-visible readback)", manifest.State, publication.StateFailed)
		}

		if err := store.Activate(ctx, manifest); !errors.Is(err, publication.ErrGenerationNotReady) {
			t.Errorf("PIN 2 | Activate(failed generation) returned %v, want ErrGenerationNotReady", err)
		}
		if active, err := store.ResolveActiveManifest(ctx, project); !errors.Is(err, publication.ErrNoActiveGeneration) {
			t.Errorf("PIN 3 | failed generation reached active pointer: active=%+v err=%v", active, err)
		}
		// Raw read, independent of readActiveAQL's manifestKey!=null and
		// STAGED/READY join filters: a dangling pointer pinned to the FAILED
		// manifest must not hide behind the resolver's own guard.
		activeKey, pointerFound, err := failLegReadActivePointer(ctx, client, project)
		if err != nil {
			t.Fatalf("PIN 3 raw active read: %v", err)
		}
		if pointerFound && strings.TrimSpace(activeKey) != "" {
			t.Errorf("PIN 3 | lifecycle active document points at manifest %q after failed load, want no pointer", activeKey)
		}

		if _, err := Load(ctx, LoadOptions{
			ConnectionOptions: arangostore.ConnectionOptions{URL: endpoint, Database: database},
			Schema:            repoPath(t, "schemas", "graph-fhir.json"),
			MetaDir:           metaDir,
			Project:           project,
			Dataset:           &ref,
			BatchSize:         10,
			ProgressEvery:     100,
		}); !errors.Is(err, publicationarango.ErrManifestAlreadyExists) {
			// FAILED blocks silent re-creation through the atomic claim: the
			// second Load must die at CreateManifest, never reaching the
			// loader loop again.
			t.Errorf("PIN 4 | re-load of failed generation returned %v, want ErrManifestAlreadyExists", err)
		}

		buildState, buildDiagnostic, buildCount, found, err := failLegReadBuildState(ctx, client, catalog.SemanticInventoryBuildCollection, project, "gen-fault")
		if err != nil {
			t.Fatalf("PIN 5 inventory readback: %v", err)
		}
		if !found {
			t.Fatalf("PIN 5 | no semantic inventory build document found for the failed generation (Begin ran before the fault; the pairing deferred must mark it failed)")
		}
		if buildCount != 1 {
			t.Errorf("PIN 5 | %d build documents for the failed generation, want exactly 1 (a stale duplicate can mask the paired failure by scan order)", buildCount)
		}
		if buildState != string(catalog.SemanticInventoryFailed) {
			t.Errorf("PIN 5 | inventory build state %q, want %q", buildState, catalog.SemanticInventoryFailed)
		}
		if !strings.Contains(buildDiagnostic, "is incomplete") || !strings.Contains(buildDiagnostic, "FAILLEG_FAULT/gen-fault") {
			t.Errorf("PIN 5b | inventory build diagnostics %q, want the specific load failure propagated (contains %q and the dataset ref)", buildDiagnostic, "is incomplete")
		}
	})

	t.Run("positive_staged_control", func(t *testing.T) {
		database := failLegPrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
		t.Cleanup(func() { failLegDropDatabase(t, endpoint, database) })
		metaDir := t.TempDir()
		rows := make([]string, 0, 3)
		for i := 0; i < 3; i++ {
			rows = append(rows, failLegPatientRow(i))
		}
		if err := os.WriteFile(filepath.Join(metaDir, "Patient.ndjson"), []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		project := "FAILLEG_OK"
		ref, err := publication.NewRef(project, "gen-clean")
		if err != nil {
			t.Fatalf("ref: %v", err)
		}
		summary, err := Load(ctx, LoadOptions{
			ConnectionOptions: arangostore.ConnectionOptions{URL: endpoint, Database: database},
			Schema:            repoPath(t, "schemas", "graph-fhir.json"),
			MetaDir:           metaDir,
			Project:           project,
			Dataset:           &ref,
			BatchSize:         10,
			ProgressEvery:     100,
		})
		if err != nil {
			t.Fatalf("positive control load failed: %v", err)
		}
		client, err := arangostore.Open(ctx, endpoint, database)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer func() { _ = client.Close(context.Background()) }()
		store, err := publicationarango.New(client)
		if err != nil {
			t.Fatalf("lifecycle store: %v", err)
		}
		active, err := store.ResolveActiveManifest(ctx, project)
		if err != nil {
			t.Fatalf("positive control: ResolveActiveManifest: %v", err)
		}
		if !active.IsStaged() {
			t.Errorf("positive control | active manifest state %q is not staged", active.State)
		}
		count, err := client.CollectionCount(ctx, "Patient")
		if err != nil {
			t.Fatalf("positive control count: %v", err)
		}
		if count != 3 || summary.VerticesInserted != 3 {
			t.Errorf("positive control | acked=%d persisted=%d, want 3/3", summary.VerticesInserted, count)
		}
	})

	t.Cleanup(func() { failLegAssertNoResidue(t, endpoint) })
}

func failLegReadActivePointer(ctx context.Context, client *arangostore.Client, project string) (string, bool, error) {
	exists, err := client.CollectionExists(ctx, publicationarango.LifecycleCollection)
	if err != nil {
		return "", false, err
	}
	if !exists {
		return "", false, nil
	}
	var manifestKey string
	var found bool
	err = client.QueryRows(ctx, `FOR a IN @@lifecycle FILTER a._key == @active_key AND a.recordType == "active_generation" RETURN {manifestKey: a.manifestKey}`, 1, map[string]any{
		"@lifecycle": publicationarango.LifecycleCollection,
		"active_key": publicationarango.ActiveDocumentKey(project),
	}, func(row map[string]any) error {
		value, _ := row["manifestKey"].(string)
		manifestKey, found = value, true
		return nil
	})
	return manifestKey, found, err
}

func failLegReadBuildState(ctx context.Context, client *arangostore.Client, collection, project, generation string) (string, string, int, bool, error) {
	exists, err := client.CollectionExists(ctx, collection)
	if err != nil {
		return "", "", 0, false, err
	}
	if !exists {
		return "", "", 0, false, nil
	}
	bindVars := map[string]any{
		"@collection": collection,
		"project":     project,
		"generation":  generation,
	}
	var state string
	var diagnostic string
	var found bool
	var count int
	err = client.QueryRows(ctx, `FOR b IN @@collection FILTER b.project == @project AND b.dataset_generation == @generation RETURN {state: b.state, diagnostics: b.diagnostics}`, 10, bindVars, func(row map[string]any) error {
		count++
		value, _ := row["state"].(string)
		state, found = value, true
		if list, ok := row["diagnostics"].([]any); ok && len(list) > 0 {
			diagnostic, _ = list[0].(string)
		}
		return nil
	})
	return state, diagnostic, count, found, err
}

func failLegDropDatabase(t *testing.T, endpoint, database string) {
	t.Helper()
	if !strings.HasPrefix(database, failLegPrefix) {
		t.Errorf("FAILLEG refusal to drop %s outside %s", database, failLegPrefix)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(database), nil)
	if err != nil {
		t.Logf("FAILLEG_CLEANUP_WARN: build drop: %v", err)
		return
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Logf("FAILLEG_CLEANUP_WARN: drop %s: %v", database, err)
		return
	}
	_ = response.Body.Close()
	if response.StatusCode >= 300 {
		t.Logf("FAILLEG_CLEANUP_WARN: drop %s status %d", database, response.StatusCode)
	}
}

func failLegAssertNoResidue(t *testing.T, endpoint string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/_api/database", nil)
	if err != nil {
		t.Errorf("FAILLEG_RESIDUE_WARN: build list: %v", err)
		return
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Errorf("FAILLEG_RESIDUE_WARN: list databases: %v", err)
		return
	}
	defer func() { _ = response.Body.Close() }()
	var payload struct {
		Result []string `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Errorf("FAILLEG_RESIDUE_WARN: decode list: %v", err)
		return
	}
	for _, db := range payload.Result {
		if strings.HasPrefix(db, failLegPrefix) {
			t.Errorf("FAILLEG residue verification database left behind: %s", db)
		}
	}
}
