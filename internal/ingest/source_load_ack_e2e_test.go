package ingest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	publication "github.com/calypr/loom/internal/dataset"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestSourceFileLostReceiptAcknowledgementPreservesAcceptedRows(t *testing.T) {
	endpoint := strings.TrimSuffix(os.Getenv("LOOM_TEST_ARANGO_URL"), "/")
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL for isolated Arango acknowledgement regression")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	database := kcollPrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	t.Cleanup(func() { kcollDropDatabase(t, endpoint, database) })
	var dropped atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "test proxy could not read request", 500)
			return
		}
		upstream, err := http.NewRequestWithContext(r.Context(), r.Method, endpoint+r.URL.RequestURI(), bytes.NewReader(body))
		if err != nil {
			http.Error(w, "test proxy could not build request", 500)
			return
		}
		upstream.Header = r.Header.Clone()
		response, err := http.DefaultClient.Do(upstream)
		if err != nil {
			http.Error(w, "test proxy backend failed", 502)
			return
		}
		defer response.Body.Close()
		responseBody, err := io.ReadAll(response.Body)
		if err != nil {
			http.Error(w, "test proxy backend response failed", 502)
			return
		}
		if response.StatusCode < 300 && bytes.Contains(body, []byte("file_claims: UNSET")) && dropped.CompareAndSwap(false, true) {
			// Arango has committed the receipt. Lose its acknowledgement before the
			// caller can observe it, exercising the actual upload recovery path.
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			_ = connection.Close()
			return
		}
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(responseBody)
	}))
	defer proxy.Close()
	client, err := arangostore.Open(ctx, proxy.URL, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	datasets, err := datasetarango.New(client)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := NewRepositorySourceLoader(SourceLoadPublisherConfig{
		Client: client, DatasetStore: datasets, SchemaPath: "../../schemas/graph-fhir.json", BatchSize: 2,
		MaterializeAndSelect: func(context.Context, string, string, string, string, int64) (string, int64, error) {
			return "", 0, fmt.Errorf("publish must not run in upload regression")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	const project = "ack-regression"
	status, err := loader.Begin(ctx, project, "lost-ack", SourceLoadPlan{Repository: "https://github.com/example/data", Commit: strings.Repeat("a", 40), AuthResourcePath: "project/data", Files: []SourceManifestFile{{Path: "META/Patient.ndjson"}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "Patient.ndjson")
	if err := os.WriteFile(path, []byte(`{"resourceType":"Patient","id":"p1"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	receipt, err := loader.UploadFile(ctx, project, "lost-ack", "Patient", path)
	if err != nil {
		t.Fatalf("recover committed receipt: %v", err)
	}
	if !dropped.Load() || receipt.State != "READY" {
		t.Fatalf("fault not exercised or receipt missing: dropped=%v receipt=%#v", dropped.Load(), receipt)
	}
	current, err := loader.Status(ctx, project, "lost-ack")
	if err != nil {
		t.Fatal(err)
	}
	if current.State != SourceLoadReady || len(current.Files) != 1 {
		t.Fatalf("durable receipt lost: %#v", current)
	}
	var count int64
	err = client.QueryRows(ctx, `FOR doc IN Patient FILTER doc.project == @project AND doc.dataset_generation == @generation COLLECT WITH COUNT INTO count RETURN {count}`, 1, map[string]any{"project": project, "generation": status.Generation}, func(row map[string]any) error { count = numericInt64(row["count"]); return nil })
	if err != nil || count != 1 {
		t.Fatalf("accepted graph was cleared after lost ACK: count=%d err=%v", count, err)
	}
	retried, err := loader.UploadFile(ctx, project, "lost-ack", "Patient", path)
	if err != nil || retried.SHA256 != receipt.SHA256 {
		t.Fatalf("idempotent retry: %#v %v", retried, err)
	}
}

func TestSourceLoadReconcilesLostInitialActivationAcknowledgement(t *testing.T) {
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL for isolated release acknowledgement regression")
	}
	database := kcollPrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	t.Cleanup(func() { kcollDropDatabase(t, endpoint, database) })
	client, err := arangostore.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	datasets, err := datasetarango.New(client)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	loader, err := NewRepositorySourceLoader(SourceLoadPublisherConfig{Client: client, DatasetStore: datasets, SchemaPath: repoPath(t, "schemas", "graph-fhir.json"), BatchSize: 2, WriteAPI: "import", MaterializeAndSelect: func(ctx context.Context, project, generation, operationID, gitCommit string, expectedRevision int64) (string, int64, error) {
		calls++
		release, err := datasets.SaveRelease(ctx, publication.ProjectRelease{ID: "review-" + operationID, Project: project, GitCommit: gitCommit, Generation: generation, CreatedAt: time.Now().UTC()})
		if err != nil {
			return "", 0, err
		}
		active, err := datasets.CompareAndSwapActivateRelease(ctx, release, expectedRevision)
		if err != nil {
			return "", 0, err
		}
		if calls == 1 {
			return "", 0, errors.New("synthetic activation ACK lost after commit")
		}
		return "execution", active.Revision, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	const project = "REVIEW_ACK"
	const operation = "first"
	status, err := loader.Begin(ctx, project, operation, SourceLoadPlan{Repository: "https://github.com/example/data", Commit: strings.Repeat("a", 40), AuthResourcePath: "project/data", Files: []SourceManifestFile{{Path: "META/Patient.ndjson"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loader.UploadFile(ctx, project, operation, "Patient", writeSourceLoadTempFile(t, "")); err != nil {
		t.Fatal(err)
	}
	_, firstErr := loader.Publish(ctx, project, operation)
	active, err := datasets.ReadActiveRelease(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("first publish=%v active generation matches=%v revision=%d", firstErr, active.Release.Generation == status.Generation, active.Revision)
	receipt, retryErr := loader.Publish(ctx, project, operation)
	current, err := loader.Status(ctx, project, operation)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("retry=%v materializer calls=%d state=%s", retryErr, calls, current.State)
	if firstErr == nil || active.Release.Generation != status.Generation || active.Revision != 1 {
		t.Fatalf("activation fault not exercised: first=%v active=%#v", firstErr, active)
	}
	if retryErr != nil || receipt.State != "PUBLISHED" || receipt.SourceRevision != 1 || current.State != SourceLoadPublished || calls != 2 {
		t.Fatalf("committed release did not reconcile: receipt=%#v state=%s calls=%d err=%v", receipt, current.State, calls, retryErr)
	}
	cached, err := loader.Publish(ctx, project, operation)
	if err != nil || !cached.Reused || calls != 2 {
		t.Fatalf("durable publication not reused: %#v calls=%d err=%v", cached, calls, err)
	}
}
