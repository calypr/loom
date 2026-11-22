package generation

import (
	"context"
	"encoding/json"
	"github.com/calypr/loom/internal/ingest"
	"github.com/calypr/loom/internal/testkit/arangotest"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	publication "github.com/calypr/loom/internal/dataset"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

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
		a, b := ingest.SanitizeKey(pair[0]), ingest.SanitizeKey(pair[1])
		if a != b {
			t.Fatalf("F-B25 probe premise lost — ingest.SanitizeKey(%q)=%q != ingest.SanitizeKey(%q)=%q", pair[0], a, pair[1], b)
		}
		directCollision[pair[0]] = a
	}
	t.Logf("FB25 GATE | SanitizeKey non-injective confirmed in-process | %q and %q both -> %q (documents.go:107-113 validKeyPart :17)", "coll/1", "coll_1", directCollision["coll/1"])

	lines := []string{
		`{"resourceType":"Patient","id":"coll/1","gender":"female"}`,
		`{"resourceType":"Patient","id":"coll_1","gender":"male"}`,
		`{"resourceType":"Patient","id":"x9","gender":"female"}`,
	}
	label := "Generated"
	t.Run(label, func(t *testing.T) {
		database := arangotest.KeyCollisionDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		t.Cleanup(func() { arangotest.DropKeyCollisionDatabase(t, endpoint, database) })

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
			MetaDir:           metaDir,
			Project:           "KCOLL_TEST",
			Dataset:           &ref,
			BatchSize:         10,
			ProgressEvery:     100,
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

			if collected == int64(summary.VerticesInserted) {
				t.Logf("FB25 %s verdict | PREVENTED by validation on this path | acknowledged=%d persisted=%d, %d row(s) rejected before write (see row_errors above)", label, summary.VerticesInserted, collected, summary.ValidationErrors+summary.GenerationErrors)
			} else {
				t.Errorf("FB25 %s INCONCLUSIVE accounting mismatch | acknowledged=%d persisted=%d errors val=%d gen=%d — manual classification needed, row_errors=%s", label, summary.VerticesInserted, collected, summary.ValidationErrors, summary.GenerationErrors, samples)
			}
		}
	})

	t.Cleanup(func() { kcollAssertNoResidue(t, endpoint) })
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
		if strings.HasPrefix(db, arangotest.KeyCollisionDatabasePrefix) {
			t.Errorf("F-B25 residue verification database left behind: %s", db)
		}
	}
}
