package server

// Repository-publish spine acceptance on the PostgreSQL lane. Mirrors the
// route coverage of TestRepositoryDeploymentPersistsExecutableDataframeSelectors
// (the in-memory shape) against the PRODUCTION composition: POST
// /api/v1/projects/{project}/generations/{generation}/explorer-config drives
// PublishRepository -> lifecycle.PublishRepositoryGeneration through the real
// capability, compile, materialize, PublishAuthoring, and release-activation
// legs, and ends in store.ActivateRepositoryGeneration -- the second CAS --
// against live PostgreSQL. The HTTP 200 alone does not prove the flip, so the
// leg witnesses the control-plane rows directly. The repeat POST proves the
// pinned replay law: a battery retry after an ambiguous response succeeds as
// an owner-already-flipped no-op without restamping activated_at.
//
// The leg is gated on BOTH live services: PostgreSQL (LOOM_TEST_POSTGRES_URL)
// and ClickHouse (LOOM_CLICKHOUSE_URL), because materialization between
// compile and activation is real. When both are configured the leg never
// skips; it fails loud.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/calypr/loom/internal/backends/postgres/control"
)

const pgPubCommitHeader = "X-Loom-Source-Commit"

func TestPostgresRuntimeRepositoryPublishRouteActivatesThroughFinalFlip(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL repository-publish route integration tests")
	}
	clickhouseURL := strings.TrimSpace(os.Getenv("LOOM_CLICKHOUSE_URL"))
	if clickhouseURL == "" {
		t.Skip("set LOOM_CLICKHOUSE_URL to run the PostgreSQL repository-publish route integration tests (materialization is real)")
	}
	fixture := newPgCompFixture(t)
	fixture.migrate(t)
	fixture.load(t, pgCompProject, pgCompGenA, map[string][]string{
		"Patient.ndjson": pgCompPatientLines(t, "patient-publish-01", "patient-publish-02"),
	})
	fixture.activate(t, pgCompProject, pgCompGenA)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bootConfig := pgCompBootConfig(t, fixture, "127.0.0.1:0")
	bootConfig.Server.ClickHouse = ClickHouseConfig{
		Enabled:  true,
		URL:      clickhouseURL,
		Database: os.Getenv("LOOM_CLICKHOUSE_DATABASE"),
		Username: os.Getenv("LOOM_CLICKHOUSE_USERNAME"),
		Password: os.Getenv("LOOM_CLICKHOUSE_PASSWORD"),
	}
	server, closeRuntime, err := composePostgreSQLFullRuntime(ctx, bootConfig, logger)
	if err != nil {
		t.Fatalf("compose ClickHouse-enabled full runtime: %s", fixture.scrub(err))
	}
	defer func() {
		if err := closeRuntime(); err != nil {
			t.Errorf("close ClickHouse-enabled full runtime: %s", fixture.scrub(err))
		}
	}()
	app := server.App()

	path := "/api/v1/projects/" + pgCompProject + "/generations/" + pgCompGenA + "/explorer-config"
	deploy := requestJSONWithHeaders(t, app, http.MethodPost, path, string(baselineExplorerWorkspaceV2()),
		map[string]string{pgPubCommitHeader: "commit-publish-01"})
	pgCompAssertNoLaneLeak(t, fixture, "repository publish deploy", deploy.Body)
	if deploy.StatusCode != http.StatusOK {
		t.Fatalf("repository publish deploy through the final activation flip returned %d, want 200: %s",
			deploy.StatusCode, fixture.scrub(deploy.Body))
	}
	var result struct {
		Project     string `json:"project"`
		Generation  string `json:"generation"`
		ExplorerID  string `json:"explorerId"`
		ReceiptID   string `json:"receiptId"`
		RevisionID  string `json:"revisionId"`
		ExecutionID string `json:"executionId"`
		Activated   *bool  `json:"activated"`
	}
	if err := json.Unmarshal([]byte(deploy.Body), &result); err != nil {
		t.Fatalf("decode deploy body: %v\n%s", err, fixture.scrub(deploy.Body))
	}
	if result.Project != pgCompProject || result.Generation != pgCompGenA || result.ExplorerID != "default" {
		t.Fatalf("deploy identity = %+v, want %s/%s/default", result, pgCompProject, pgCompGenA)
	}
	if result.ReceiptID == "" || result.RevisionID == "" || result.ExecutionID == "" {
		t.Fatalf("deploy lost the publication identity: %+v", result)
	}
	if result.Activated == nil || !*result.Activated {
		t.Fatalf("deploy did not report activated=true: %s", fixture.scrub(deploy.Body))
	}

	statusBefore, activatedAtBefore, pointerBefore := pgPubWitness(t, fixture, result.RevisionID)
	if statusBefore != "ACTIVE" || activatedAtBefore.IsZero() {
		t.Fatalf("witness after deploy: revision %s status=%s activated_at=%v, want ACTIVE/non-zero",
			result.RevisionID, statusBefore, activatedAtBefore)
	}
	if pointerBefore != result.RevisionID {
		t.Fatalf("witness after deploy: owner active_revision_id = %q, want the deployed revision %q",
			pointerBefore, result.RevisionID)
	}

	replay := requestJSONWithHeaders(t, app, http.MethodPost, path, string(baselineExplorerWorkspaceV2()),
		map[string]string{pgPubCommitHeader: "commit-publish-01"})
	if replay.StatusCode != http.StatusOK {
		t.Fatalf("replayed deploy (battery retry after an ambiguous response) returned %d, want 200: %s",
			replay.StatusCode, fixture.scrub(replay.Body))
	}
	var replayed struct {
		RevisionID string `json:"revisionId"`
		Activated  *bool  `json:"activated"`
	}
	if err := json.Unmarshal([]byte(replay.Body), &replayed); err != nil {
		t.Fatalf("decode replay body: %v\n%s", err, fixture.scrub(replay.Body))
	}
	if replayed.RevisionID != result.RevisionID || replayed.Activated == nil || !*replayed.Activated {
		t.Fatalf("replay must be the same activated revision: got %+v, want revisionId %q activated=true",
			replayed, result.RevisionID)
	}
	statusAfter, activatedAtAfter, pointerAfter := pgPubWitness(t, fixture, result.RevisionID)
	if statusAfter != "ACTIVE" || pointerAfter != result.RevisionID {
		t.Fatalf("replay moved activation state: status=%s pointer=%q", statusAfter, pointerAfter)
	}
	if !activatedAtBefore.Equal(activatedAtAfter) {
		t.Fatalf("replay restamped activated_at: %v -> %v (the no-op replay law forbids it)",
			activatedAtBefore, activatedAtAfter)
	}

	reload := requestJSON(t, app, http.MethodGet, "/api/v1/projects/"+pgCompProject+"/explorers/default", "")
	if reload.StatusCode != http.StatusOK || !strings.Contains(reload.Body, "\"Patient ID\"") {
		t.Fatalf("viewer state must serve the activated revision, got %d %s",
			reload.StatusCode, fixture.scrub(reload.Body))
	}
}

// pgPubWitness reads the control-plane activation state directly, bypassing
// every handler: the HTTP 200 is accepted only when the owner pointer names
// the deployed revision and the revision row is ACTIVE with a set activated_at.
func pgPubWitness(t *testing.T, fixture *pgCompFixture, revisionID string) (string, time.Time, string) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), fixture.dsn)
	if err != nil {
		t.Fatalf("connect witness pool: %s", fixture.scrub(err))
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctlSchema := fixture.schemas[control.SeriesExplorerControl]
	if !pgCompSchemaPattern.MatchString(ctlSchema) {
		t.Fatalf("witness refuses a control schema %q outside the owned namespace", ctlSchema)
	}
	ctl := `"` + ctlSchema + `"`
	var status string
	var activatedAt time.Time
	if err := pool.QueryRow(ctx,
		`SELECT status, activated_at FROM `+ctl+`.explorer_revisions WHERE id = $1`, revisionID).
		Scan(&status, &activatedAt); err != nil {
		t.Fatalf("witness revision %s: %s", revisionID, fixture.scrub(err))
	}
	var pointer string
	if err := pool.QueryRow(ctx,
		`SELECT active_revision_id FROM `+ctl+`.explorers WHERE project = $1 AND explorer_id = 'default'`, pgCompProject).
		Scan(&pointer); err != nil {
		t.Fatalf("witness owner pointer for %s: %s", pgCompProject, fixture.scrub(err))
	}
	return status, activatedAt, pointer
}
