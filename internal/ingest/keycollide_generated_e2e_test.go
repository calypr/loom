package ingest

// F-B25 (hypothesis probe): internal/ingest/documents.go SanitizeKey (:107-113)
// collapses every disallowed char to `_` via validKeyPart (:17) — non-injective.
// The vertex insert path posts with onDuplicate=replace
// (internal/store/arango/client.go:450-451,494-495 region) and the summary
// counter adds len(task.docs) SENT, not persisted
// (internal/ingest/generation_workers.go:426 region). If two DIFFERENT FHIR
// resource ids sanitize onto one `_key`, the second write silently replaces
// the first while LoadSummary still reports both as VerticesInserted — a lost
// acknowledged write.
//
// This artifact drives the REAL production Load() entry point on a fresh
// verification Arango database, per builder path (UseGeneric false and true —
// spelling matches LoadOptions field), and compares the acknowledged counter
// against the real collection count:
//   acknowledged == collected          -> the path prevents the collision
//     (typically via id charset validation; the RowErrors/ValidationErrors
//      samples logged per path classify WHICH prevention fired).
//   acknowledged >  collected           -> CONFIRMED silent replacement on
//     that path.
//
// Collision pair: "coll/1" and "coll_1" — both non-blank strings; SanitizeKey
// maps '/' -> '_', so both land on _key "coll_1" while the JSON payload keeps
// the different ids. Control id "x9" must survive next to them.
//
// Cleanup: every created database is dropped via the registered first (runs
// last, LIFO) helper; final DB list is asserted to exclude the prefix.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	publication "github.com/calypr/loom/internal/dataset"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

const kcollPrefix = "loom_verify_kcoll_"

func kcollEndpoint(t *testing.T) string {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL to run the F-B25 sanitize-key collision reproduction")
	}
	return endpoint
}

func TestFB25SanitizeKeyCollisionLosesAcknowledgedWrite(t *testing.T) {
	endpoint := kcollEndpoint(t)
	directCollision := map[string]string{}
	for _, pair := range [][2]string{{"coll/1", "coll_1"}} {
		a, b := SanitizeKey(pair[0]), SanitizeKey(pair[1])
		if a != b {
			t.Fatalf("F-B25 probe premise lost — SanitizeKey(%q)=%q != SanitizeKey(%q)=%q", pair[0], a, pair[1], b)
		}
		directCollision[pair[0]] = a
	}
	t.Logf("FB25 GATE | SanitizeKey non-injective confirmed in-process | %q and %q both -> %q (documents.go:107-113 validKeyPart :17)", "coll/1", "coll_1", directCollision["coll/1"])

	lines := []string{
		`{"resourceType":"Patient","id":"coll/1","gender":"female"}`,
		`{"resourceType":"Patient","id":"coll_1","gender":"male"}`,
		`{"resourceType":"Patient","id":"x9","gender":"female"}`,
	}
	for _, useGeneric := range []bool{false, true} {
		label := "Generated"
		if useGeneric {
			label = "Generic"
		}
		t.Run(label, func(t *testing.T) {
			database := kcollPrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			// Registered FIRST => runs LAST: drop the verification database
			// after all sub-assertions, then nothing else holds it.
			t.Cleanup(func() { kcollDropDatabase(t, endpoint, database) })

			metaDir := t.TempDir()
			file := filepath.Join(metaDir, "Patient.ndjson")
			if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			ref, err := publication.NewRef("KCOLL_TEST", "gen-"+label)
			if err != nil {
				t.Fatalf("ref: %v", err)
			}
			summary, err := Load(ctx, LoadOptions{
				ConnectionOptions: arangostore.ConnectionOptions{URL: endpoint, Database: database},
				Schema:            repoPath(t, "schemas", "graph-fhir.json"),
				MetaDir:           metaDir,
				Project:           "KCOLL_TEST",
				Dataset:           &ref,
				BatchSize:         10,
				ProgressEvery:     100,
				UseGeneric:        useGeneric,
			})
			if err != nil && !strings.Contains(err.Error(), "is incomplete") {
				t.Fatalf("F-B25 %s: production Load failed (non-rejection error): %v", label, err)
			}
			if err != nil {
				t.Logf("FB25 %s | Load reported row rejections (classification follows the switch below): %v", label, err)
			}
			samples, _ := json.Marshal(summary.RowErrors)
			t.Logf("FB25 %s | LOAD_SUMMARY acked=%d validation_errors=%d generation_errors=%d row_errors=%s",
				label, summary.VerticesInserted, summary.ValidationErrors, summary.GenerationErrors, samples)

			client, err := arangostore.Open(ctx, endpoint, database)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer func() { _ = client.Close(context.Background()) }()
			exists, err := client.CollectionExists(ctx, "Patient")
			if err != nil {
				t.Fatalf("collection exists: %v", err)
			}
			collected := int64(0)
			if exists {
				collected, err = client.CollectionCount(ctx, "Patient")
				if err != nil {
					t.Fatalf("collection count: %v", err)
				}
			}
			t.Logf("FB25 %s | COUNTS acknowledged=%d persisted=%d (non-write error samples=%d)", label, summary.VerticesInserted, collected, summary.ValidationErrors+summary.GenerationErrors+summary.EdgeErrors)

			switch {
			case collected == int64(len(lines)):
				t.Logf("FB25 %s verdict | PREVENTED — every submitted id persisted, no replacement occurred on this path", label)
			case summary.VerticesInserted > 0 && collected < int64(summary.VerticesInserted) && summary.ValidationErrors == 0 && summary.GenerationErrors == 0:
				t.Errorf("FB25 %s RED-ON-PURPOSE CONFIRMED silent replacement | Load acknowledged %d vertices (counter += len(sent docs), generation_workers.go:426) but only %d documents exist in Patient: one of ids coll/1 vs coll_1 (both SanitizeKey->coll_1, documents.go:107-113) was overwritten by onDuplicate=replace with zero error surfaced to the caller — lost acknowledged write", label, summary.VerticesInserted, collected)
			case summary.VerticesInserted == 0 && (summary.ValidationErrors+summary.GenerationErrors) > 0:
				t.Errorf("FB25 %s unexpected total rejection (all %d rows errored): row_errors=%s — probe premise about this path's validation strength lost; classify separately", label, len(lines), samples)
			default:
				// Partial rejection: validation caught the disallowed id on
				// this path. Honest PREVENTED via validation — but only if the
				// SURVIVING pair still round-trips with x9 absent collisions.
				if collected == int64(summary.VerticesInserted) {
					t.Logf("FB25 %s verdict | PREVENTED by validation on this path | acknowledged=%d persisted=%d, %d row(s) rejected before write (see row_errors above)", label, summary.VerticesInserted, collected, summary.ValidationErrors+summary.GenerationErrors)
				} else {
					t.Errorf("FB25 %s INCONCLUSIVE accounting mismatch | acknowledged=%d persisted=%d errors val=%d gen=%d — manual classification needed, row_errors=%s", label, summary.VerticesInserted, collected, summary.ValidationErrors, summary.GenerationErrors, samples)
				}
			}
		})
	}
	// Residue: no verification databases may survive.
	t.Cleanup(func() { kcollAssertNoResidue(t, endpoint) })
	_ = fmt.Sprint()
}

func kcollDropDatabase(t *testing.T, endpoint, database string) {
	t.Helper()
	if !strings.HasPrefix(database, kcollPrefix) {
		t.Errorf("FB25 refusal to drop %s outside %s", database, kcollPrefix)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := arangostore.Open(ctx, endpoint, "_system")
	if err != nil {
		t.Logf("FB25_CLEANUP_WARN: open _system: %v", err)
		return
	}
	defer func() { _ = client.Close(context.Background()) }()
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(database), nil)
	if err != nil {
		t.Logf("FB25_CLEANUP_WARN: build drop: %v", err)
		return
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Logf("FB25_CLEANUP_WARN: drop %s: %v", database, err)
		return
	}
	_ = response.Body.Close()
	if response.StatusCode >= 300 {
		t.Logf("FB25_CLEANUP_WARN: drop %s status %d", database, response.StatusCode)
	}
}

func kcollAssertNoResidue(t *testing.T, endpoint string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/_api/database", nil)
	if err != nil {
		t.Logf("FB25_RESIDUE_WARN: build list: %v", err)
		return
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Logf("FB25_RESIDUE_WARN: list databases: %v", err)
		return
	}
	defer func() { _ = response.Body.Close() }()
	var payload struct {
		Result []string `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Logf("FB25_RESIDUE_WARN: decode list: %v", err)
		return
	}
	for _, db := range payload.Result {
		if strings.HasPrefix(db, kcollPrefix) {
			t.Errorf("F-B25 residue verification database left behind: %s", db)
		}
	}
}
