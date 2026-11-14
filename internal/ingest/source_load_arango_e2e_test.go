package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	publication "github.com/calypr/loom/internal/dataset"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestRepositorySourceLoaderArangoResumeCopyAndEmptyReplacement(t *testing.T) {
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL to run source-load Arango acceptance")
	}
	database := kcollPrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	t.Cleanup(func() { kcollDropDatabase(t, endpoint, database) })
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
		Client: client, DatasetStore: datasets, SchemaPath: repoPath(t, "schemas", "graph-fhir.json"),
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
	if err := client.Bootstrap(ctx, bootstrapSpecWithReporter([]string{"BodyStructure"}, false, nil)); err != nil {
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
		`{"resourceType":"Patient","id":"patient-1","managingOrganization":{"reference":"Organization/org-1"}}`,
		`{"resourceType":"Patient","id":"patient-2","managingOrganization":{"reference":"Organization/org-1"}}`,
		`{"resourceType":"Patient","id":"patient-3","managingOrganization":{"reference":"Organization/org-1"}}`,
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
	if len(initialEdges) != 6 {
		t.Fatalf("initial Patient edge count = %d, want six edges across three rows", len(initialEdges))
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
	repairedEdges := sourceLoadEdges(t, ctx, client, project, repairedActive.Release.Generation)
	if len(repairedEdges) != 6 {
		t.Fatalf("copied Patient edge count = %d, want 6", len(repairedEdges))
	}
	organizationKey := "Organization/" + generationDocumentKey(project, repairedActive.Release.Generation, "Organization", SanitizeKey("org-1"))
	patientKeys := map[string]bool{}
	for _, id := range []string{"patient-1", "patient-2", "patient-3"} {
		patientKeys["Patient/"+generationDocumentKey(project, repairedActive.Release.Generation, "Patient", SanitizeKey(id))] = true
	}
	for _, edge := range repairedEdges {
		if edge.SourceFile != "Patient" {
			t.Fatalf("copied edge owner = %q, want Patient", edge.SourceFile)
		}
		if !(patientKeys[edge.From] && edge.To == organizationKey || edge.From == organizationKey && patientKeys[edge.To]) {
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
	if len(clearedEdges) != 6 {
		t.Fatalf("empty replacement retained %d Patient edges, want 6", len(clearedEdges))
	}
	clearedOrganizationKey := "Organization/" + generationDocumentKey(project, clearedActive.Release.Generation, "Organization", SanitizeKey("org-1"))
	clearedPatientKeys := map[string]bool{}
	for _, id := range []string{"patient-1", "patient-2", "patient-3"} {
		clearedPatientKeys["Patient/"+generationDocumentKey(project, clearedActive.Release.Generation, "Patient", SanitizeKey(id))] = true
	}
	for _, edge := range clearedEdges {
		if !(clearedPatientKeys[edge.From] && edge.To == clearedOrganizationKey || edge.From == clearedOrganizationKey && clearedPatientKeys[edge.To]) {
			t.Fatalf("retained edge does not point to rebased candidate identities: %#v", edge)
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
