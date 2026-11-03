package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

type artifactDownloadStoreFixture struct {
	record      *explorer.ArtifactRecord
	requestedId string
	opened      int
}

func (f *artifactDownloadStoreFixture) Begin(context.Context, explorer.ArtifactRecord) (explorer.ArtifactStage, *explorer.ArtifactRecord, error) {
	panic("download failure branches must never stage artifacts")
}

func (f *artifactDownloadStoreFixture) Get(_ context.Context, id string) (*explorer.ArtifactRecord, error) {
	f.requestedId = id
	if id != artifactDownloadId() {
		panic("download looked up a different artifact id than the URL addressed: " + id)
	}
	return f.record, nil
}

func (f *artifactDownloadStoreFixture) Open(context.Context, string) (io.ReadCloser, *explorer.ArtifactRecord, error) {
	f.opened++
	return nil, nil, nil
}

func artifactDownloadId() string {
	return "artifact_" + strings.Repeat("a", 64)
}

func downloadArtifactWithRecord(t *testing.T, now time.Time, record *explorer.ArtifactRecord) testHTTPResponse {
	t.Helper()
	store := &artifactDownloadStoreFixture{record: record}
	domain, err := explorer.NewService(newTestExplorerStore())
	if err != nil {
		t.Fatal(err)
	}
	config := lifecycle.Config{
		Now:           func() time.Time { return now },
		ArtifactStore: store,
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, domain, config)
	path := "/api/v1/projects/project-a/explorers/custom/authoring/v2/artifacts/" + artifactDownloadId()
	response := requestJSON(t, app, http.MethodGet, path, "")
	if store.opened != 0 {
		t.Fatalf("failure branch opened archive payload: %d opens", store.opened)
	}
	if store.requestedId != artifactDownloadId() {
		t.Fatalf("download consulted %q, not the URL-addressed artifact id", store.requestedId)
	}
	return response
}

func TestDownloadExplorerArtifactClassificationCoversExpiryStateAndOwnership(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	complete := func() *explorer.ArtifactRecord {
		return &explorer.ArtifactRecord{Project: "project-a", ExplorerID: "custom", State: explorer.ArtifactComplete, ExpiresAt: now.Add(24 * time.Hour)}
	}
	expired := complete()
	expired.ExpiresAt = now.Add(-time.Minute)
	response := downloadArtifactWithRecord(t, now, expired)
	if response.StatusCode != http.StatusGone || !strings.Contains(response.Body, "ARTIFACT_EXPIRED") {
		t.Fatalf("expired artifact must be typed 410 ARTIFACT_EXPIRED (documented '410'): status=%d body=%s", response.StatusCode, response.Body)
	}
	incomplete := complete()
	incomplete.State = explorer.ArtifactPreparing
	response = downloadArtifactWithRecord(t, now, incomplete)
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(response.Body, "ARTIFACT_NOT_COMPLETE") {
		t.Fatalf("incomplete artifact must be typed 503 ARTIFACT_NOT_COMPLETE: status=%d body=%s", response.StatusCode, response.Body)
	}
	both := complete()
	both.State = explorer.ArtifactPreparing
	both.ExpiresAt = now.Add(-time.Minute)
	response = downloadArtifactWithRecord(t, now, both)
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(response.Body, "ARTIFACT_NOT_COMPLETE") {
		t.Fatalf("preparing-and-expired artifact must classify NOT_COMPLETE before EXPIRED (committed precedence): status=%d body=%s", response.StatusCode, response.Body)
	}
	foreign := complete()
	foreign.Project = "project-b"
	response = downloadArtifactWithRecord(t, now, foreign)
	if response.StatusCode != http.StatusNotFound || !strings.Contains(response.Body, "ARTIFACT_NOT_FOUND") {
		t.Fatalf("cross-project artifact must not leak: status=%d body=%s", response.StatusCode, response.Body)
	}
	foreignExplorer := complete()
	foreignExplorer.ExplorerID = "someone-else"
	response = downloadArtifactWithRecord(t, now, foreignExplorer)
	if response.StatusCode != http.StatusNotFound || !strings.Contains(response.Body, "ARTIFACT_NOT_FOUND") {
		t.Fatalf("cross-explorer artifact must not leak: status=%d body=%s", response.StatusCode, response.Body)
	}
	response = downloadArtifactWithRecord(t, now, nil)
	if response.StatusCode != http.StatusNotFound || !strings.Contains(response.Body, "ARTIFACT_NOT_FOUND") {
		t.Fatalf("missing artifact must be typed 404: status=%d body=%s", response.StatusCode, response.Body)
	}
}
