package load

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest"
	"github.com/gofiber/fiber/v3"
)

type fakeGenerationRunner struct{ req GenerationLoadRequest }

func (r *fakeGenerationRunner) RunGeneration(_ context.Context, req GenerationLoadRequest, _ ingest.EventSink) (ingest.LoadSummary, error) {
	r.req = req
	return ingest.LoadSummary{Files: 2, VerticesInserted: 4}, nil
}

func TestCreateDatasetGenerationStagesCompleteBundle(t *testing.T) {
	runner := &fakeGenerationRunner{}
	service, err := NewService(ServiceConfig{LoadGeneration: runner.RunGeneration})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Config{Service: service, Authorizer: authscope.AllowAllAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	body, boundary := generationMultipart(t, true, map[string][]byte{
		"Patient.ndjson":  []byte("{\"resourceType\":\"Patient\",\"id\":\"1\"}\n"),
		"Specimen.ndjson": []byte("{\"resourceType\":\"Specimen\",\"id\":\"2\"}\n"),
	})
	result, err := handler.CreateDatasetGeneration(context.Background(), "P1", "generation-1", multipart.NewReader(bytes.NewReader(body), boundary), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Project != "P1" || result.Generation != "generation-1" || runner.req.Project != "P1" || runner.req.Generation != "generation-1" || !runner.req.DeferActivation {
		t.Fatalf("result=%#v request=%#v", result, runner.req)
	}
}

// orderedPart is one multipart part in the exact order the client sent it.
type orderedPart struct {
	field    string
	filename string
	content  []byte
}

func orderedMultipart(t *testing.T, parts []orderedPart) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, part := range parts {
		if part.filename == "" {
			if err := writer.WriteField(part.field, string(part.content)); err != nil {
				t.Fatal(err)
			}
			continue
		}
		header := map[string][]string{
			"Content-Disposition": {fmt.Sprintf(`form-data; name=%q; filename=%q`, part.field, part.filename)},
		}
		if part.field == "" {
			header["Content-Disposition"] = []string{fmt.Sprintf(`form-data; filename=%q`, part.filename)}
		}
		partWriter, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := partWriter.Write(part.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), writer.Boundary()
}

// recordingGenerationRunner captures the loader-visible bundle and can fail.
type recordingGenerationRunner struct {
	req      GenerationLoadRequest
	contents map[string]string
	visible  []string
	err      error
}

func (r *recordingGenerationRunner) RunGeneration(_ context.Context, req GenerationLoadRequest, _ ingest.EventSink) (ingest.LoadSummary, error) {
	r.req = req
	r.contents = map[string]string{}
	r.visible = nil
	entries, err := os.ReadDir(req.StagedDir)
	if err != nil {
		return ingest.LoadSummary{}, fmt.Errorf("staged dir unreadable at load time: %w", err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(req.StagedDir, entry.Name()))
		if err != nil {
			return ingest.LoadSummary{}, fmt.Errorf("staged %s unreadable at load time: %w", entry.Name(), err)
		}
		r.contents[entry.Name()] = string(data)
		r.visible = append(r.visible, entry.Name())
	}
	if r.err != nil {
		return ingest.LoadSummary{}, r.err
	}
	return ingest.LoadSummary{Files: len(entries)}, nil
}

func allowAllHandler(t *testing.T, runner *recordingGenerationRunner) *Handler {
	t.Helper()
	service, err := NewService(ServiceConfig{LoadGeneration: runner.RunGeneration})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Config{Service: service, Authorizer: authscope.AllowAllAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

// assertNoTemporaryUploadFiles pins the cleanup contract: after the handler
// returns, the redirected temp root holds no staged directory and no
// multipart spillover file.
func assertNoTemporaryUploadFiles(t *testing.T, tempDir string) {
	t.Helper()
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	var leftovers []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "loom-generation-") || strings.HasPrefix(name, "multipart") {
			leftovers = append(leftovers, name)
		}
	}
	if len(leftovers) != 0 {
		t.Fatalf("temporary upload files left behind: %v", leftovers)
	}
}

// TestCreateDatasetGenerationStagesFilesThatArriveBeforeScalarFields pins the
// compatibility-sensitive ordering contract: files first in multipart order
// must still reach the loader byte-identical, and the scalar fields that
// follow them (including a repeat value, where the first one wins) must still
// reach the handler.
func TestCreateDatasetGenerationStagesFilesThatArriveBeforeScalarFields(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)
	runner := &recordingGenerationRunner{}
	handler := allowAllHandler(t, runner)
	patient := []byte("{\"resourceType\":\"Patient\",\"id\":\"1\"}\n")
	specimen := []byte("{\"resourceType\":\"Specimen\",\"id\":\"2\"}\n")
	body, boundary := orderedMultipart(t, []orderedPart{
		{field: "file", filename: "Patient.ndjson", content: patient},
		{field: "file", filename: "Specimen.ndjson", content: specimen},
		{field: "defer_activation", content: []byte("true")},
		{field: "auth_resource_path", content: []byte("p1")},
		{field: "auth_resource_path", content: []byte("ignored")},
	})

	if _, err := handler.CreateDatasetGeneration(context.Background(), "P1", "generation-1", multipart.NewReader(bytes.NewReader(body), boundary), nil); err != nil {
		t.Fatalf("load: %v", err)
	}
	if runner.req.Project != "P1" || runner.req.Generation != "generation-1" || !runner.req.DeferActivation {
		t.Fatalf("loader request = %#v", runner.req)
	}
	if want := authscope.NormalizeAuthResourcePath("p1"); runner.req.AuthResourcePath != want {
		t.Fatalf("auth path = %q, want first value %q", runner.req.AuthResourcePath, want)
	}
	if len(runner.visible) != 2 {
		t.Fatalf("loader-visible staged files = %v, want both bundle files", runner.visible)
	}
	if got := runner.contents["Patient.ndjson"]; got != string(patient) {
		t.Fatalf("staged Patient.ndjson = %q, want %q", got, patient)
	}
	if got := runner.contents["Specimen.ndjson"]; got != string(specimen) {
		t.Fatalf("staged Specimen.ndjson = %q, want %q", got, specimen)
	}
	assertNoTemporaryUploadFiles(t, tempDir)
}

// TestCreateDatasetGenerationRejectsBundleWithoutStagingAnything pins the
// file-name and duplicate contract plus its cleanup: every rejection is a
// typed error and leaves nothing on disk, including a file already streamed
// into the staging directory before the rejecting part arrived.
func TestCreateDatasetGenerationRejectsBundleWithoutStagingAnything(t *testing.T) {
	large := bytes.Repeat([]byte("y"), 20*1024*1024)
	for _, tc := range []struct {
		name  string
		code  dataframeerrors.ErrorCode
		parts []orderedPart
	}{
		{
			name: "invalid extension",
			code: dataframeerrors.CodeInvalidGenerationFile,
			parts: []orderedPart{
				{field: "file", filename: "Patient.txt", content: []byte("nope\n")},
			},
		},
		{
			name: "dot-only name",
			code: dataframeerrors.CodeInvalidGenerationFile,
			parts: []orderedPart{
				{field: "file", filename: ".", content: []byte("nope\n")},
			},
		},
		{
			name: "duplicate name after base normalization",
			code: dataframeerrors.CodeDuplicateGenerationFile,
			parts: []orderedPart{
				{field: "file", filename: "Patient.ndjson", content: large},
				{field: "file", filename: "nested/Patient.ndjson", content: []byte("again\n")},
			},
		},
		{
			name: "duplicate name",
			code: dataframeerrors.CodeDuplicateGenerationFile,
			parts: []orderedPart{
				{field: "file", filename: "Patient.ndjson", content: large},
				{field: "file", filename: "Patient.ndjson", content: []byte("again\n")},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			t.Setenv("TMPDIR", tempDir)
			runner := &recordingGenerationRunner{}
			handler := allowAllHandler(t, runner)
			body, boundary := orderedMultipart(t, tc.parts)

			_, err := handler.CreateDatasetGeneration(context.Background(), "P1", "generation-1", multipart.NewReader(bytes.NewReader(body), boundary), nil)
			userErr, ok := dataframeerrors.AsUserError(err)
			if !ok || userErr.Code() != string(tc.code) {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
			if len(runner.visible) != 0 {
				t.Fatalf("loader saw a rejected bundle: %v", runner.visible)
			}
			assertNoTemporaryUploadFiles(t, tempDir)
		})
	}
}

// TestCreateDatasetGenerationMalformedAndAbsentBodyAreInvalidInput pins the
// pre-fix classification of parse failures: an absent or unparseable body is
// never a staging fault, and a truncated stream leaves nothing behind.
func TestCreateDatasetGenerationMalformedAndAbsentBodyAreInvalidInput(t *testing.T) {
	t.Run("absent body", func(t *testing.T) {
		handler := allowAllHandler(t, &recordingGenerationRunner{})
		_, err := handler.CreateDatasetGeneration(context.Background(), "P1", "generation-1", nil, nil)
		assertFiberStatus(t, err, fiber.StatusUnsupportedMediaType)
	})

	t.Run("truncated stream", func(t *testing.T) {
		tempDir := t.TempDir()
		t.Setenv("TMPDIR", tempDir)
		runner := &recordingGenerationRunner{}
		handler := allowAllHandler(t, runner)
		body, boundary := orderedMultipart(t, []orderedPart{
			{field: "file", filename: "Patient.ndjson", content: []byte("{\"resourceType\":\"Patient\"}\n")},
		})
		truncated := body[:len(body)-8]

		_, err := handler.CreateDatasetGeneration(context.Background(), "P1", "generation-1", multipart.NewReader(bytes.NewReader(truncated), boundary), nil)
		userErr, ok := dataframeerrors.AsUserError(err)
		if !ok || userErr.Code() != string(dataframeerrors.CodeInvalidRequest) {
			t.Fatalf("truncated body error = %v, want INVALID_REQUEST", err)
		}
		assertNoTemporaryUploadFiles(t, tempDir)
	})

	t.Run("empty boundary", func(t *testing.T) {
		handler := allowAllHandler(t, &recordingGenerationRunner{})
		_, err := handler.CreateDatasetGeneration(context.Background(), "P1", "generation-1", multipart.NewReader(strings.NewReader("nothing"), ""), nil)
		assertFiberStatus(t, err, fiber.StatusUnsupportedMediaType)
	})
}

// assertFiberStatus pins the transport-level error the route maps to a status.
func assertFiberStatus(t *testing.T, err error, status int) {
	t.Helper()
	var fiberErr *fiber.Error
	if !errors.As(err, &fiberErr) || fiberErr.Code != status {
		t.Fatalf("error = %v (%T), want fiber status %d", err, err, status)
	}
}

// TestCreateDatasetGenerationRejectedAuthorizationAndLoaderFailureLeaveNoFiles
// pins that authorization still runs before the loader, that a loader failure
// keeps its identity through the handler, and that both paths dispose of the
// staged bundle.
func TestCreateDatasetGenerationRejectedAuthorizationAndLoaderFailureLeaveNoFiles(t *testing.T) {
	t.Run("forbidden", func(t *testing.T) {
		tempDir := t.TempDir()
		t.Setenv("TMPDIR", tempDir)
		runner := &recordingGenerationRunner{}
		service, err := NewService(ServiceConfig{LoadGeneration: runner.RunGeneration})
		if err != nil {
			t.Fatal(err)
		}
		handler, err := NewHandler(Config{Service: service, Authorizer: rejectingAuthorizer{}})
		if err != nil {
			t.Fatal(err)
		}
		body, boundary := orderedMultipart(t, []orderedPart{
			{field: "file", filename: "Patient.ndjson", content: []byte("{\"resourceType\":\"Patient\",\"id\":\"1\"}\n")},
			{field: "defer_activation", content: []byte("true")},
		})

		_, err = handler.CreateDatasetGeneration(context.Background(), "P1", "generation-1", multipart.NewReader(bytes.NewReader(body), boundary), nil)
		userErr, ok := dataframeerrors.AsUserError(err)
		if !ok || userErr.Code() != string(dataframeerrors.CodeForbidden) {
			t.Fatalf("error = %v, want FORBIDDEN", err)
		}
		if len(runner.visible) != 0 {
			t.Fatalf("loader ran despite a forbidden request: %v", runner.visible)
		}
		assertNoTemporaryUploadFiles(t, tempDir)
	})

	t.Run("loader failure", func(t *testing.T) {
		tempDir := t.TempDir()
		t.Setenv("TMPDIR", tempDir)
		sentinel := errors.New("loader exploded")
		runner := &recordingGenerationRunner{err: sentinel}
		handler := allowAllHandler(t, runner)
		body, boundary := orderedMultipart(t, []orderedPart{
			{field: "file", filename: "Patient.ndjson", content: []byte("{\"resourceType\":\"Patient\",\"id\":\"1\"}\n")},
			{field: "defer_activation", content: []byte("true")},
		})

		_, err := handler.CreateDatasetGeneration(context.Background(), "P1", "generation-1", multipart.NewReader(bytes.NewReader(body), boundary), nil)
		if !errors.Is(err, sentinel) {
			t.Fatalf("loader error lost: %v", err)
		}
		if len(runner.visible) != 1 || runner.contents["Patient.ndjson"] != "{\"resourceType\":\"Patient\",\"id\":\"1\"}\n" {
			t.Fatalf("loader-visible bundle = %v %v", runner.visible, runner.contents)
		}
		assertNoTemporaryUploadFiles(t, tempDir)
	})
}

func TestGetDatasetGenerationStatusMapsMissingManifest(t *testing.T) {
	service, err := NewService(ServiceConfig{
		LoadGeneration:      activationGenerationRunner{}.RunGeneration,
		GenerationActivator: &activationManifestStore{readErr: publication.ErrManifestNotFound},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Config{Service: service, Authorizer: authscope.AllowAllAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}

	_, err = handler.GetDatasetGenerationStatus(context.Background(), "project-a", "generation-a", "", nil)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeDatasetNotFound) {
		t.Fatalf("status error = %v, want DATASET_NOT_FOUND", err)
	}
}

func TestGetDatasetGenerationStatusAuthorizesBeforeManifestLookup(t *testing.T) {
	store := &activationManifestStore{manifest: activationManifest(t, "project-a", "generation-a")}
	service, err := NewService(ServiceConfig{
		LoadGeneration:      activationGenerationRunner{}.RunGeneration,
		GenerationActivator: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Config{Service: service, Authorizer: rejectingAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}

	_, err = handler.GetDatasetGenerationStatus(context.Background(), "project-a", "generation-a", "", nil)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeForbidden) {
		t.Fatalf("status error = %v, want FORBIDDEN", err)
	}
	if store.readCalls != 0 {
		t.Fatalf("manifest reads = %d, want 0", store.readCalls)
	}
}

type rejectingAuthorizer struct{}

func (rejectingAuthorizer) AuthorizeWrite(context.Context, *authscope.Principal, string, string) error {
	return errors.New("denied")
}

func generationMultipart(t *testing.T, deferActivation bool, files map[string][]byte) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if deferActivation {
		_ = writer.WriteField("defer_activation", "true")
	}
	for name, content := range files {
		part, err := writer.CreateFormFile("file", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), writer.Boundary()
}
