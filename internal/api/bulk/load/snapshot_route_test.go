package load

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataset"
)

type routePublicationVerifier struct {
	result dataset.PublicationVerification
}

func (v routePublicationVerifier) VerifyPublication(context.Context, string, string, dataset.DataframeSelector) (dataset.PublicationVerification, error) {
	return v.result, nil
}

func TestSnapshotHTTPContractCreateUploadFinalizeStatusAndAbort(t *testing.T) {
	store := newMemoryLifecycleStore()
	runner := &snapshotRunnerFixture{}
	snapshots := &SnapshotService{Repository: store, Blobs: LocalSnapshotBlobs{Root: t.TempDir()}, Runner: runner}
	selector := dataset.DataframeSelector{Recipe: "core", TranslationVersion: "v1", Output: "Patient"}
	releases := &dataset.ReleaseService{Snapshots: store, Releases: store, Verifier: routePublicationVerifier{result: dataset.PublicationVerification{
		Selector: selector, ExecutionID: "execution-a", Generation: "commit-a", State: "PUBLISHED", Queryable: true, VerifiedAt: time.Now().UTC(),
	}}, Required: []dataset.DataframeSelector{selector}}
	baseService, err := NewService(ServiceConfig{GenerationRunner: runner})
	if err != nil {
		t.Fatal(err)
	}
	server, err := httpapi.NewHTTPServer(httpapi.HTTPConfig{Authenticator: authscope.StaticAuthenticator{}, Authorizer: authscope.AllowAllAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Config{Service: baseService, Authorizer: authscope.AllowAllAuthorizer{}, Snapshots: snapshots, Releases: releases})
	if err != nil {
		t.Fatal(err)
	}
	handler.RegisterSnapshotRoutes(server.App())

	createBody := []byte(`{"gitCommit":"commit-a","expectedResourceTypes":["Patient"]}`)
	for range 2 {
		response := requestSnapshot(t, server, http.MethodPost, "/api/v1/projects/project-a/generations/commit-a", createBody, "")
		if response.StatusCode != http.StatusOK {
			t.Fatalf("create status = %d, body=%s", response.StatusCode, readBody(response))
		}
		_ = response.Body.Close()
	}
	body := []byte("{\"resourceType\":\"Patient\",\"id\":\"p1\"}\n")
	response := requestSnapshot(t, server, http.MethodPut, "/api/v1/projects/project-a/generations/commit-a/resources/Patient", body, checksumFor([]byte("different")))
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("checksum conflict status = %d, body=%s", response.StatusCode, readBody(response))
	}
	var conflict httpapi.ErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&conflict); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if conflict.Error.Code != "CHECKSUM_CONFLICT" || conflict.Error.Retryable {
		t.Fatalf("checksum error = %#v", conflict)
	}

	response = requestSnapshot(t, server, http.MethodPut, "/api/v1/projects/project-a/generations/commit-a/resources/Patient", body, checksumFor(body))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d, body=%s", response.StatusCode, readBody(response))
	}
	_ = response.Body.Close()
	response = requestSnapshot(t, server, http.MethodPost, "/api/v1/projects/project-a/generations/commit-a/finalize", nil, "")
	if response.StatusCode != http.StatusOK || runner.calls != 1 || !runner.requests[0].StageOnly {
		t.Fatalf("finalize status=%d calls=%d requests=%#v body=%s", response.StatusCode, runner.calls, runner.requests, readBody(response))
	}
	_ = response.Body.Close()
	response = requestSnapshot(t, server, http.MethodGet, "/api/v1/projects/project-a/generations/commit-a", nil, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.StatusCode, readBody(response))
	}
	_ = response.Body.Close()

	releaseBody := []byte(`{"generation":"commit-a"}`)
	response = requestSnapshot(t, server, http.MethodPost, "/api/v1/projects/project-a/releases", releaseBody, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("create release status = %d, body=%s", response.StatusCode, readBody(response))
	}
	var release dataset.ProjectRelease
	if err := json.NewDecoder(response.Body).Decode(&release); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	response = requestSnapshot(t, server, http.MethodGet, "/api/v1/projects/project-a/releases/active", nil, "")
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("inactive release became visible: status=%d body=%s", response.StatusCode, readBody(response))
	}
	_ = response.Body.Close()
	activatePath := "/api/v1/projects/project-a/releases/" + release.ID + "/activate"
	for range 2 {
		response = requestSnapshot(t, server, http.MethodPost, activatePath, []byte(`{"expectedRevision":0}`), "")
		if response.StatusCode != http.StatusOK {
			t.Fatalf("activate release status = %d, body=%s", response.StatusCode, readBody(response))
		}
		_ = response.Body.Close()
	}
	if status, err := store.ReadSnapshot(context.Background(), dataset.Ref{Project: "project-a", Generation: "commit-a"}); err != nil || status.State != dataset.StateStaged {
		t.Fatalf("snapshot after activation = %#v, %v", status, err)
	}
	response = requestSnapshot(t, server, http.MethodDelete, "/api/v1/projects/project-a/generations/commit-a", nil, "")
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("abort finalized status = %d, body=%s", response.StatusCode, readBody(response))
	}
	_ = response.Body.Close()
}

func requestSnapshot(t *testing.T, server *httpapi.HTTPServer, method, path string, body []byte, checksum string) *http.Response {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if checksum != "" {
		request.Header.Set("X-Content-SHA256", checksum)
	}
	response, err := server.App().Test(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func readBody(response *http.Response) string {
	body, _ := io.ReadAll(response.Body)
	return string(body)
}
