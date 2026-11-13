package server

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestPostgresRuntimeRepositoryPublishRetryAfterActivationFailureStaysPinned(t *testing.T) {
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
		"Patient.ndjson": pgCompPatientLines(t, "patient-publish-11", "patient-publish-12"),
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
	bootConfig.Server.DevRepositoryActivationConflictOnce = true
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
	workspace := string(baselineExplorerWorkspaceV2())
	headers := map[string]string{pgPubCommitHeader: "commit-publish-11"}

	first := requestJSONWithHeaders(t, app, http.MethodPost, path, workspace, headers)
	pgCompAssertNoLaneLeak(t, fixture, "conflict-armed deploy", first.Body)
	if first.StatusCode != http.StatusConflict || !strings.Contains(first.Body, "PUBLICATION_ACTIVATION_CONFLICT") {
		t.Fatalf("conflict-armed deploy returned %d, want 409 PUBLICATION_ACTIVATION_CONFLICT: %s",
			first.StatusCode, fixture.scrub(first.Body))
	}
	revisionID := pgPubConflictRevision(t, fixture, pgCompProject)
	status, failedAt, diagnostics, pointer := pgPubConflictWitness(t, fixture, pgCompProject, revisionID)
	if status != "FAILED" || failedAt.IsZero() {
		t.Fatalf("revision after activation failure = %s/failed_at %v, want FAILED with failed_at set", status, failedAt)
	}
	if !strings.Contains(diagnostics, "PUBLICATION_ACTIVATION_CONFLICT") {
		t.Fatalf("lifecycle did not mark the failure evidence: diagnostics = %s", fixture.scrub(diagnostics))
	}
	if pointer != "" {
		t.Fatalf("activation failure moved the owner pointer to %q, want unchanged", pointer)
	}

	for attempt := 2; attempt <= 3; attempt++ {
		retry := requestJSONWithHeaders(t, app, http.MethodPost, path, workspace, headers)
		pgCompAssertNoLaneLeak(t, fixture, fmt.Sprintf("retry %d after activation failure", attempt), retry.Body)
		if retry.StatusCode != http.StatusConflict || !strings.Contains(retry.Body, "GENERATION_ACTIVATION_FAILED") {
			t.Fatalf("retry %d after an activation failure returned %d, want 409 GENERATION_ACTIVATION_FAILED: %s",
				attempt, retry.StatusCode, fixture.scrub(retry.Body))
		}
		if got := pgPubConflictRevision(t, fixture, pgCompProject); got != revisionID {
			t.Fatalf("retry %d minted a different revision %q, want the pinned identity %q", attempt, got, revisionID)
		}
		status, retryFailedAt, retryDiagnostics, retryPointer := pgPubConflictWitness(t, fixture, pgCompProject, revisionID)
		if status != "FAILED" || !retryFailedAt.Equal(failedAt) || retryDiagnostics != diagnostics || retryPointer != pointer {
			t.Fatalf("retry %d churned the pinned revision: %s/failed_at %v, want FAILED/failed_at %v unchanged",
				attempt, status, retryFailedAt, failedAt)
		}
	}

	const siblingProject = "pgcomp-p2"
	fixture.load(t, siblingProject, pgCompGenA, map[string][]string{
		"Patient.ndjson": pgCompPatientLines(t, "patient-publish-21", "patient-publish-22"),
	})
	fixture.activate(t, siblingProject, pgCompGenA)
	siblingPath := "/api/v1/projects/" + siblingProject + "/generations/" + pgCompGenA + "/explorer-config"
	deploy := requestJSONWithHeaders(t, app, http.MethodPost, siblingPath, workspace,
		map[string]string{pgPubCommitHeader: "commit-publish-21"})
	pgCompAssertNoLaneLeak(t, fixture, "sibling deploy", deploy.Body)
	if deploy.StatusCode != http.StatusOK {
		t.Fatalf("sibling deploy returned %d, want 200: %s", deploy.StatusCode, fixture.scrub(deploy.Body))
	}
	var result struct {
		RevisionID string `json:"revisionId"`
		Activated  *bool  `json:"activated"`
	}
	if err := json.Unmarshal([]byte(deploy.Body), &result); err != nil {
		t.Fatalf("decode sibling deploy body: %v\n%s", err, fixture.scrub(deploy.Body))
	}
	if result.RevisionID == "" || result.Activated == nil || !*result.Activated {
		t.Fatalf("sibling deploy did not report an activated revision: %s", fixture.scrub(deploy.Body))
	}
	siblingStatus, siblingActivatedAt, siblingPointer := pgPubProjectWitness(t, fixture, siblingProject, result.RevisionID)
	if siblingStatus != "ACTIVE" || siblingActivatedAt.IsZero() || siblingPointer != result.RevisionID {
		t.Fatalf("sibling witness after deploy: %s/activated_at %v/pointer %q, want ACTIVE/non-zero/%q",
			siblingStatus, siblingActivatedAt, siblingPointer, result.RevisionID)
	}
	replay := requestJSONWithHeaders(t, app, http.MethodPost, siblingPath, workspace,
		map[string]string{pgPubCommitHeader: "commit-publish-21"})
	if replay.StatusCode != http.StatusOK {
		t.Fatalf("sibling replay returned %d, want 200: %s", replay.StatusCode, fixture.scrub(replay.Body))
	}
	var replayed struct {
		RevisionID string `json:"revisionId"`
		Activated  *bool  `json:"activated"`
	}
	if err := json.Unmarshal([]byte(replay.Body), &replayed); err != nil {
		t.Fatalf("decode sibling replay body: %v\n%s", err, fixture.scrub(replay.Body))
	}
	if replayed.RevisionID != result.RevisionID || replayed.Activated == nil || !*replayed.Activated {
		t.Fatalf("sibling replay must be the same activated revision: got %+v, want revisionId %q activated=true",
			replayed, result.RevisionID)
	}
	siblingStatus, siblingReplayAt, siblingReplayPointer := pgPubProjectWitness(t, fixture, siblingProject, result.RevisionID)
	if siblingStatus != "ACTIVE" || siblingReplayPointer != result.RevisionID {
		t.Fatalf("sibling replay moved activation state: %s/pointer %q", siblingStatus, siblingReplayPointer)
	}
	if !siblingActivatedAt.Equal(siblingReplayAt) {
		t.Fatalf("sibling replay restamped activated_at: %v -> %v (the no-op replay law forbids it)",
			siblingActivatedAt, siblingReplayAt)
	}
}

func pgPubConflictRevision(t *testing.T, fixture *pgCompFixture, project string) string {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), fixture.dsn)
	if err != nil {
		t.Fatalf("connect witness pool: %s", fixture.scrub(err))
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctl := `"` + fixture.schemas[control.SeriesExplorerControl] + `"`
	if !pgCompSchemaPattern.MatchString(fixture.schemas[control.SeriesExplorerControl]) {
		t.Fatalf("witness refuses a control schema %q outside the owned namespace", fixture.schemas[control.SeriesExplorerControl])
	}
	rows, err := pool.Query(ctx,
		`SELECT id FROM `+ctl+`.explorer_revisions WHERE project = $1 AND explorer_id = 'default'`, project)
	if err != nil {
		t.Fatalf("witness revisions for %s: %s", project, fixture.scrub(err))
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan revision id for %s: %s", project, fixture.scrub(err))
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read revision ids for %s: %s", project, fixture.scrub(err))
	}
	if len(ids) != 1 {
		t.Fatalf("witness revisions for %s = %v, want exactly one pinned revision", project, ids)
	}
	return ids[0]
}

func pgPubConflictWitness(t *testing.T, fixture *pgCompFixture, project, revisionID string) (string, time.Time, string, string) {
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
	var failedAt *time.Time
	var diagnostics string
	if err := pool.QueryRow(ctx,
		`SELECT status, failed_at, diagnostics::text FROM `+ctl+`.explorer_revisions WHERE id = $1`, revisionID).
		Scan(&status, &failedAt, &diagnostics); err != nil {
		t.Fatalf("witness revision %s: %s", revisionID, fixture.scrub(err))
	}
	var pointer *string
	if err := pool.QueryRow(ctx,
		`SELECT active_revision_id FROM `+ctl+`.explorers WHERE project = $1 AND explorer_id = 'default'`, project).
		Scan(&pointer); err != nil {
		t.Fatalf("witness owner pointer for %s: %s", project, fixture.scrub(err))
	}
	at := time.Time{}
	if failedAt != nil {
		at = *failedAt
	}
	pointerText := ""
	if pointer != nil {
		pointerText = *pointer
	}
	return status, at, diagnostics, pointerText
}

func pgPubProjectWitness(t *testing.T, fixture *pgCompFixture, project, revisionID string) (string, time.Time, string) {
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
	var pointer *string
	if err := pool.QueryRow(ctx,
		`SELECT active_revision_id FROM `+ctl+`.explorers WHERE project = $1 AND explorer_id = 'default'`, project).
		Scan(&pointer); err != nil {
		t.Fatalf("witness owner pointer for %s: %s", project, fixture.scrub(err))
	}
	pointerText := ""
	if pointer != nil {
		pointerText = *pointer
	}
	return status, activatedAt, pointerText
}
