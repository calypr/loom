package sourceload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	publication "github.com/calypr/loom/internal/dataset"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	"github.com/calypr/loom/internal/ingest/bootstrap"
	"github.com/calypr/loom/internal/ingest/identity"
	"github.com/calypr/loom/internal/ingest/row"
	"github.com/calypr/loom/internal/semantic"
	semanticarango "github.com/calypr/loom/internal/semantic/arango"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/testkit/arangotest"
	"github.com/google/uuid"
)

func TestRepositorySourceLoaderArangoResumeCopyAndEmptyReplacement(t *testing.T) {
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL to run source-load Arango acceptance")
	}
	database := arangotest.KeyCollisionDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	t.Cleanup(func() { arangotest.DropKeyCollisionDatabase(t, endpoint, database) })
	client, err := arangostore.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer func() { _ = client.Close(context.Background()) }()
	datasets, err := datasetarango.New(client)
	if err != nil {
		t.Fatal(err)
	}
	const project = "SOURCE_LOAD_E2E"
	const repository = "https://github.com/example/clinical-data"
	const commit = "0123456789abcdef0123456789abcdef01234567"
	loader, err := NewRepositorySourceLoader(SourceLoadPublisherConfig{
		Client: client, DatasetStore: datasets,
		BatchSize: 2, WriteAPI: "import",
		MaterializeAndSelect: func(ctx context.Context, project, generation, operationID, gitCommit string, expectedRevision int64) (string, int64, error) {
			release := publication.ProjectRelease{
				ID: "source-load-test-" + operationID, Project: project, GitCommit: gitCommit,
				Generation: generation, CreatedAt: time.Now().UTC(),
			}
			saved, err := datasets.SaveRelease(ctx, release)
			if err != nil {
				return "", 0, err
			}
			active, err := datasets.CompareAndSwapActivateRelease(ctx, saved, expectedRevision)
			if err != nil {
				return "", 0, err
			}
			return "test-execution-" + operationID, active.Revision, nil
		},
	})
	if err != nil {
		t.Fatalf("construct source loader: %v", err)
	}
	t.Log("source-load loader constructed")
	if err := client.Bootstrap(ctx, bootstrap.BootstrapSpecWithReporter([]string{"BodyStructure"}, false, nil)); err != nil {
		t.Fatalf("bootstrap copy-query control: %v", err)
	}
	copyQuery := `FOR doc IN @@collection
FILTER doc.project == @project
FILTER (@source_generation == "" AND NOT_NULL(doc.dataset_generation, "") == "") OR doc.dataset_generation == @source_generation
FILTER doc._key > @after_key
SORT doc._key
LIMIT @limit
RETURN {document: doc}`
	copyControlCtx, copyControlCancel := context.WithTimeout(ctx, 10*time.Second)
	defer copyControlCancel()
	if err := client.QueryRows(copyControlCtx, copyQuery, 2, map[string]any{
		"@collection": "BodyStructure", "project": project, "source_generation": "", "after_key": "", "limit": 2,
	}, func(map[string]any) error { return nil }); err != nil {
		t.Fatalf("copy-query control: %v", err)
	}
	t.Log("copy-query control returned")

	patientPath := writeSourceLoadTempFile(t, strings.Join([]string{
		`{"resourceType":"Patient","id":"patient-1","identifier":[{"system":"urn:source-load:test-one","value":"shared-one"},{"system":"urn:source-load:test-two","value":"shared-two"}],"managingOrganization":{"reference":"Organization/org-1"}}`,
		`{"resourceType":"Patient","id":"patient-2","identifier":[{"system":"urn:source-load:test-one","value":"shared-one"},{"system":"urn:source-load:test-two","value":"shared-two"}],"managingOrganization":{"reference":"Organization/org-1"}}`,
		`{"resourceType":"Patient","id":"patient-3","identifier":[{"system":"urn:source-load:test-one","value":"shared-one"},{"system":"urn:source-load:test-two","value":"shared-two"}],"managingOrganization":{"reference":"Organization/org-1"}}`,
	}, "\n")+"\n")
	organizationPath := writeSourceLoadTempFile(t, `{"resourceType":"Organization","id":"org-1"}`+"\n")
	status, err := loader.Begin(ctx, project, "initial", SourceLoadPlan{
		Repository: repository, Commit: commit, AuthResourcePath: "/programs/example/projects/source-load-e2e",
		Files: []SourceManifestFile{{Path: "META/Patient.ndjson"}, {Path: "META/Organization.ndjson"}},
	})
	if err != nil {
		t.Fatalf("begin initial load: %v", err)
	}
	t.Logf("initial Begin returned generation %s", status.Generation)
	for _, upload := range []struct{ resourceType, path string }{{"Patient", patientPath}, {"Organization", organizationPath}} {
		if _, err := loader.UploadFile(ctx, project, "initial", upload.resourceType, upload.path); err != nil {
			t.Fatalf("upload initial %s: %v", upload.resourceType, err)
		}
		t.Logf("initial upload %s complete", upload.resourceType)
	}
	initialReceipt, err := loader.Publish(ctx, project, "initial")
	if err != nil {
		t.Fatalf("publish initial load: %v", err)
	}
	t.Log("initial Publish complete")
	if initialReceipt.State != "PUBLISHED" || initialReceipt.SourceRevision != 1 {
		t.Fatalf("initial receipt = %#v", initialReceipt)
	}
	initialActive, err := datasets.ReadActiveRelease(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	if initialActive.Release.Generation != status.Generation {
		t.Fatalf("active generation = %q, want %q", initialActive.Release.Generation, status.Generation)
	}
	initialEdges := sourceLoadEdges(t, ctx, client, project, initialActive.Release.Generation)
	if len(initialEdges) != 3 {
		t.Fatalf("initial Patient edge count = %d, want one forward edge per Patient", len(initialEdges))
	}
	for _, edge := range initialEdges {
		if edge.SourceFile != "Patient" {
			t.Fatalf("initial edge owner = %q, want Patient", edge.SourceFile)
		}
	}

	badRows := strings.Repeat(`{"resourceType":"Organization"}`+"\n", 105)
	badPath := writeSourceLoadTempFile(t, badRows)
	partial, err := loader.Begin(ctx, project, "repair-file", SourceLoadPlan{
		Repository: repository, Commit: commit, AuthResourcePath: "/programs/example/projects/source-load-e2e",
		ExpectedRevision: int64Pointer(1), Files: []SourceManifestFile{{Path: "META/Organization.ndjson"}},
	})
	if err != nil {
		t.Fatalf("begin repair load: %v", err)
	}
	t.Logf("repair Begin returned generation %s", partial.Generation)
	_, err = loader.UploadFile(ctx, project, "repair-file", "Organization", badPath)
	var validation *SourceFileValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("late invalid file error = %T %v, want SourceFileValidationError", err, err)
	}
	if validation.TotalErrors != 105 || len(validation.Issues) != 100 || !validation.Truncated {
		t.Fatalf("bounded validation summary = errors:%d issues:%d truncated:%v", validation.TotalErrors, len(validation.Issues), validation.Truncated)
	}
	partial, err = loader.Status(ctx, project, "repair-file")
	if err != nil {
		t.Fatal(err)
	}
	if partial.State != SourceLoadUploading || len(partial.Files) != 0 {
		t.Fatalf("rejected file status = %#v, want UPLOADING with no receipt", partial)
	}
	if got := sourceLoadVertexCount(t, ctx, client, project, partial.Generation, "Organization"); got != 0 {
		t.Fatalf("rejected file wrote %d candidate Organization vertices", got)
	}

	correctedPath := writeSourceLoadTempFile(t, `{"resourceType":"Organization","id":"org-1","name":"corrected"}`+"\n")
	correctedReceipt, err := loader.UploadFile(ctx, project, "repair-file", "Organization", correctedPath)
	if err != nil {
		t.Fatalf("retry corrected Organization file: %v", err)
	}
	if correctedReceipt.State != "READY" || correctedReceipt.Rows != 1 {
		t.Fatalf("corrected receipt = %#v", correctedReceipt)
	}
	if _, err := loader.Publish(ctx, project, "repair-file"); err != nil {
		t.Fatalf("publish corrected file: %v", err)
	}
	repairedActive, err := datasets.ReadActiveRelease(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	assertCopiedSourceLoadInventory(t, ctx, client, project, initialActive.Release.Generation, repairedActive.Release.Generation)
	repairedEdges := sourceLoadEdges(t, ctx, client, project, repairedActive.Release.Generation)
	if len(repairedEdges) != 3 {
		t.Fatalf("copied Patient edge count = %d, want 3", len(repairedEdges))
	}
	organizationKey := "Organization/" + identity.GenerationDocumentKeyWithFormat(project, repairedActive.Release.Generation, "Organization", row.SanitizeKey("org-1"), publication.IdentityFormatCompact)
	patientKeys := map[string]bool{}
	for _, id := range []string{"patient-1", "patient-2", "patient-3"} {
		patientKeys["Patient/"+identity.GenerationDocumentKeyWithFormat(project, repairedActive.Release.Generation, "Patient", row.SanitizeKey(id), publication.IdentityFormatCompact)] = true
	}
	for _, edge := range repairedEdges {
		if edge.SourceFile != "Patient" {
			t.Fatalf("copied edge owner = %q, want Patient", edge.SourceFile)
		}
		if !patientKeys[edge.From] || edge.To != organizationKey {
			t.Fatalf("copied edge endpoints = (%q,%q), want candidate Patient/Organization endpoints", edge.From, edge.To)
		}
	}
	if got := sourceLoadVertexCount(t, ctx, client, project, repairedActive.Release.Generation, "Organization"); got != 1 {
		t.Fatalf("repaired Organization count = %d, want 1", got)
	}

	emptyPath := writeSourceLoadTempFile(t, "")
	emptyStatus, err := loader.Begin(ctx, project, "clear-organization", SourceLoadPlan{
		Repository: repository, Commit: commit, AuthResourcePath: "/programs/example/projects/source-load-e2e",
		ExpectedRevision: int64Pointer(2), Files: []SourceManifestFile{{Path: "META/Organization.ndjson"}},
	})
	if err != nil {
		t.Fatalf("begin empty replacement: %v", err)
	}
	emptyReceipt, err := loader.UploadFile(ctx, project, "clear-organization", "Organization", emptyPath)
	if err != nil {
		t.Fatalf("upload empty selected file: %v", err)
	}
	if emptyReceipt.Rows != 0 || emptyReceipt.State != "READY" {
		t.Fatalf("empty file receipt = %#v", emptyReceipt)
	}
	if _, err := loader.Publish(ctx, project, "clear-organization"); err != nil {
		t.Fatalf("publish empty replacement: %v", err)
	}
	clearedActive, err := datasets.ReadActiveRelease(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	if clearedActive.Release.Generation != emptyStatus.Generation {
		t.Fatalf("active generation after empty file = %q, want %q", clearedActive.Release.Generation, emptyStatus.Generation)
	}
	if got := sourceLoadVertexCount(t, ctx, client, project, clearedActive.Release.Generation, "Organization"); got != 0 {
		t.Fatalf("empty selected file retained %d Organization vertices", got)
	}
	clearedEdges := sourceLoadEdges(t, ctx, client, project, clearedActive.Release.Generation)
	if len(clearedEdges) != 3 {
		t.Fatalf("empty replacement retained %d Patient edges, want 3", len(clearedEdges))
	}
	clearedOrganizationKey := "Organization/" + identity.GenerationDocumentKeyWithFormat(project, clearedActive.Release.Generation, "Organization", row.SanitizeKey("org-1"), publication.IdentityFormatCompact)
	clearedPatientKeys := map[string]bool{}
	for _, id := range []string{"patient-1", "patient-2", "patient-3"} {
		clearedPatientKeys["Patient/"+identity.GenerationDocumentKeyWithFormat(project, clearedActive.Release.Generation, "Patient", row.SanitizeKey(id), publication.IdentityFormatCompact)] = true
	}
	for _, edge := range clearedEdges {
		if edge.SourceFile != "Patient" || !clearedPatientKeys[edge.From] || edge.To != clearedOrganizationKey {
			t.Fatalf("retained edge endpoints = (%q,%q) owner=%q, want Patient/Organization endpoints owned by Patient", edge.From, edge.To, edge.SourceFile)
		}
	}

	patientReplacement := writeSourceLoadTempFile(t, `{"resourceType":"Patient","id":"patient-replacement","identifier":[{"system":"urn:source-load:test-one","value":"replacement"}]}`+"\n")
	replacement, err := loader.Begin(ctx, project, "replace-patient", SourceLoadPlan{
		Repository: repository, Commit: commit, AuthResourcePath: "/programs/example/projects/source-load-e2e",
		ExpectedRevision: int64Pointer(3), Files: []SourceManifestFile{{Path: "META/Patient.ndjson"}},
	})
	if err != nil {
		t.Fatalf("begin Patient replacement: %v", err)
	}
	if _, err := loader.UploadFile(ctx, project, "replace-patient", "Patient", patientReplacement); err != nil {
		t.Fatalf("upload Patient replacement: %v", err)
	}
	if _, err := loader.Publish(ctx, project, "replace-patient"); err != nil {
		t.Fatalf("publish Patient replacement: %v", err)
	}
	store, err := semanticarango.New(client)
	if err != nil {
		t.Fatal(err)
	}
	unrestricted := true
	page, err := store.PageSemanticInventory(ctx, semantic.PageOptions{
		Project: project, DatasetGeneration: replacement.Generation, ResourceType: "Patient",
		AuthResourcePathsUnrestricted: &unrestricted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Observation.Population != 1 {
		t.Fatalf("replacement Patient inventory = %#v, want one current entry with population one", page.Entries)
	}
}

func assertCopiedSourceLoadInventory(t *testing.T, ctx context.Context, client *arangostore.Client, project, source, target string) {
	t.Helper()
	store, err := semanticarango.New(client)
	if err != nil {
		t.Fatal(err)
	}
	unrestricted := true
	entries := func(generation string) []semantic.Entry {
		t.Helper()
		page, err := store.PageSemanticInventory(ctx, semantic.PageOptions{
			Project: project, DatasetGeneration: generation, ResourceType: "Patient", AuthResourcePathsUnrestricted: &unrestricted,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Entries) != 2 {
			t.Fatalf("generation %q retained %d inventory entries, want 2", generation, len(page.Entries))
		}
		for _, entry := range page.Entries {
			if entry.Observation.Population != 3 {
				t.Fatalf("generation %q entry %q population = %d, want 3", generation, entry.ConceptID, entry.Observation.Population)
			}
		}
		return page.Entries
	}
	before, after := entries(source), entries(target)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("copied inventory differs: before %#v, after %#v", before, after)
	}

	readEntries := func(generation string) map[string]map[string]any {
		t.Helper()
		entries := make(map[string]map[string]any)
		err := client.QueryRows(ctx, `FOR doc IN fhir_semantic_inventory_entries
FILTER doc.project == @project AND doc.dataset_generation == @generation AND doc.resource_type == "Patient"
FILTER doc.source_kind == "file"
SORT doc.binding_id, doc.concept_id
RETURN UNSET(doc, "_id", "_rev")`, 10, map[string]any{"project": project, "generation": generation}, func(doc map[string]any) error {
			identity := sourceLoadString(doc["binding_id"]) + "|" + sourceLoadString(doc["concept_id"])
			entries[identity] = doc
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 {
			t.Fatalf("generation %q has %d materialized Patient entries, want two", generation, len(entries))
		}
		return entries
	}
	sourceEntries, targetEntries := readEntries(source), readEntries(target)
	for identity, targetEntry := range targetEntries {
		sourceEntry, exists := sourceEntries[identity]
		if !exists {
			t.Fatalf("target generation has unexpected inventory entry %q", identity)
		}
		sourceKey := sourceLoadString(sourceEntry["_key"])
		targetKey := sourceLoadString(targetEntry["_key"])
		wantKey := semantic.EntryKey(project, target,
			semantic.BuildID(project, target), sourceLoadString(targetEntry["source_kind"]),
			sourceLoadString(targetEntry["auth_resource_path"]), sourceLoadString(targetEntry["binding_id"]),
			sourceLoadString(targetEntry["concept_id"]))
		if targetKey != wantKey || targetKey == sourceKey {
			t.Fatalf("copied entry key = %q, want deterministic target key %q distinct from %q", targetKey, wantKey, sourceKey)
		}
		for _, field := range []string{"_key", "dataset_generation", "build_id"} {
			delete(sourceEntry, field)
			delete(targetEntry, field)
		}
		if !reflect.DeepEqual(sourceEntry, targetEntry) {
			t.Fatalf("copied entry %q changed: source=%#v target=%#v", identity, sourceEntry, targetEntry)
		}
	}
	for _, generation := range []string{source, target} {
		found, err := arangostore.QueryHasRow(ctx, client, `FOR doc IN fhir_semantic_inventory
FILTER doc.project == @project AND doc.dataset_generation == @generation AND doc.resource_type == "Patient"
LIMIT 1
RETURN {found: true}`, 1, map[string]any{"project": project, "generation": generation})
		if err != nil {
			t.Fatal(err)
		}
		if found {
			t.Fatalf("generation %q unexpectedly retained raw Patient inventory contributions", generation)
		}
	}
}

type sourceLoadEdgeFixture struct {
	From       string `json:"from"`
	To         string `json:"to"`
	SourceFile string `json:"source_file"`
}

func sourceLoadEdges(t *testing.T, ctx context.Context, client *arangostore.Client, project, generation string) []sourceLoadEdgeFixture {
	t.Helper()
	var edges []sourceLoadEdgeFixture
	err := client.QueryRows(ctx, `FOR edge IN fhir_edge
FILTER edge.project == @project AND edge.dataset_generation == @generation
SORT edge._key
RETURN {from: edge._from, to: edge._to, source_file: edge.source_file}`, 100, map[string]any{
		"project": project, "generation": generation,
	}, func(row map[string]any) error {
		var edge sourceLoadEdgeFixture
		encoded, err := json.Marshal(row)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(encoded, &edge); err != nil {
			return err
		}
		edges = append(edges, edge)
		return nil
	})
	if err != nil {
		t.Fatalf("read source-load edges: %v", err)
	}
	return edges
}

func sourceLoadVertexCount(t *testing.T, ctx context.Context, client *arangostore.Client, project, generation, resourceType string) int64 {
	t.Helper()
	var count int64
	err := client.QueryRows(ctx, `FOR doc IN @@collection
FILTER doc.project == @project AND doc.dataset_generation == @generation
COLLECT WITH COUNT INTO count
RETURN {count}`, 1, map[string]any{
		"@collection": resourceType, "project": project, "generation": generation,
	}, func(row map[string]any) error {
		count = sourceLoadNumber(row["count"])
		return nil
	})
	if err != nil {
		t.Fatalf("count candidate %s rows: %v", resourceType, err)
	}
	return count
}

func writeSourceLoadTempFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "resource.ndjson")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write source-load file: %v", err)
	}
	return path
}

func int64Pointer(value int64) *int64 { return &value }

func TestSourceFileLostReceiptAcknowledgementPreservesAcceptedRows(t *testing.T) {
	endpoint := strings.TrimSuffix(os.Getenv("LOOM_TEST_ARANGO_URL"), "/")
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL for isolated Arango acknowledgement regression")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	database := arangotest.KeyCollisionDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	t.Cleanup(func() { arangotest.DropKeyCollisionDatabase(t, endpoint, database) })
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
			http.Error(w, "test proxy backend failed", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		responseBody, err := io.ReadAll(response.Body)
		if err != nil {
			http.Error(w, "test proxy backend response failed", http.StatusBadGateway)
			return
		}
		if response.StatusCode < 300 && bytes.Contains(body, []byte("file_claims: UNSET")) && dropped.CompareAndSwap(false, true) {

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
		Client: client, DatasetStore: datasets, BatchSize: 2,
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
	database := arangotest.KeyCollisionDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	t.Cleanup(func() { arangotest.DropKeyCollisionDatabase(t, endpoint, database) })
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
	loader, err := NewRepositorySourceLoader(SourceLoadPublisherConfig{Client: client, DatasetStore: datasets, BatchSize: 2, WriteAPI: "import", MaterializeAndSelect: func(ctx context.Context, project, generation, operationID, gitCommit string, expectedRevision int64) (string, int64, error) {
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
