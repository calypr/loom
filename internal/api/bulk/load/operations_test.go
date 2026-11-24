package load

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	publication "github.com/calypr/loom/internal/dataset"
	ingestevent "github.com/calypr/loom/internal/ingest/event"
	"github.com/calypr/loom/internal/ingest/generation"
	"github.com/gofiber/fiber/v3"
)

type fakeGenerationRunner struct{ req GenerationLoadRequest }

func (r *fakeGenerationRunner) RunGeneration(_ context.Context, req GenerationLoadRequest, _ ingestevent.EventSink) (generation.LoadSummary, error) {
	r.req = req
	return generation.LoadSummary{Files: 2, VerticesInserted: 4}, nil
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

func (h *Handler) createDatasetGenerationBaseline(ctx context.Context, project, generation string, body *multipart.Reader, principal *authscope.Principal) (*GenerationLoadResult, error) {
	if body == nil {
		return nil, fiber.NewError(fiber.StatusUnsupportedMediaType)
	}
	form, err := body.ReadForm(32 << 20)
	if err != nil {
		if strings.Contains(err.Error(), "boundary is empty") {
			return nil, fiber.NewError(fiber.StatusUnsupportedMediaType)
		}
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidRequest, "")
	}
	defer form.RemoveAll()
	project, generation = strings.TrimSpace(project), strings.TrimSpace(generation)
	if project == "" || generation == "" || len(form.File["file"]) == 0 {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	authResourcePath := strings.TrimSpace(firstFormValue(form.Value, "auth_resource_path"))
	if authResourcePath != "" && authscope.NormalizeAuthResourcePath(authResourcePath) == "" {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	deferActivation := false
	if raw := strings.TrimSpace(firstFormValue(form.Value, "defer_activation")); raw != "" {
		deferActivation, err = strconv.ParseBool(raw)
		if err != nil {
			return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidRequest, "")
		}
	}
	if err := h.authz.AuthorizeWrite(ctx, principal, project, authResourcePath); err != nil {
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeForbidden, "")
	}
	authResourcePath = authscope.NormalizeAuthResourcePath(authResourcePath)
	stagedDir, err := stageGenerationFilesBaseline(form.File["file"])
	if err != nil {
		switch {
		case errors.Is(err, os.ErrInvalid):
			return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidGenerationFile, "")
		case errors.Is(err, os.ErrExist):
			return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeDuplicateGenerationFile, "")
		default:
			return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
		}
	}
	defer os.RemoveAll(stagedDir)
	req := GenerationLoadRequest{Project: project, Generation: generation, AuthResourcePath: authResourcePath, StagedDir: stagedDir, DeferActivation: deferActivation}
	if principal != nil {
		req.SubmittedBy = principal.Subject
	}
	result, err := h.service.runGeneration(ctx, req)
	return result, normalizeError(err)
}

func stageGenerationFilesBaseline(headers []*multipart.FileHeader) (string, error) {
	dir, err := os.MkdirTemp("", "loom-generation-")
	if err != nil {
		return "", err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	seen := make(map[string]struct{}, len(headers))
	for _, header := range headers {
		name := filepath.Base(header.Filename)
		if name == "." || name == "" || name != header.Filename || filepath.Ext(name) != ".ndjson" {
			cleanup()
			return "", os.ErrInvalid
		}
		if _, ok := seen[name]; ok {
			cleanup()
			return "", os.ErrExist
		}
		seen[name] = struct{}{}
		src, err := header.Open()
		if err != nil {
			cleanup()
			return "", err
		}
		dst, err := os.Create(filepath.Join(dir, name))
		if err == nil {
			_, err = io.Copy(dst, src)
		}
		_ = src.Close()
		if dst != nil {
			if closeErr := dst.Close(); err == nil {
				err = closeErr
			}
		}
		if err != nil {
			cleanup()
			return "", err
		}
	}
	return dir, nil
}

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

type recordingGenerationRunner struct {
	req      GenerationLoadRequest
	contents map[string]string
	visible  []string
	calls    int
	err      error
}

func (r *recordingGenerationRunner) RunGeneration(_ context.Context, req GenerationLoadRequest, _ ingestevent.EventSink) (generation.LoadSummary, error) {
	r.req = req
	r.calls++
	r.contents = map[string]string{}
	r.visible = nil
	entries, err := os.ReadDir(req.StagedDir)
	if err != nil {
		return generation.LoadSummary{}, fmt.Errorf("staged dir unreadable at load time: %w", err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(req.StagedDir, entry.Name()))
		if err != nil {
			return generation.LoadSummary{}, fmt.Errorf("staged %s unreadable at load time: %w", entry.Name(), err)
		}
		r.contents[entry.Name()] = string(data)
		r.visible = append(r.visible, entry.Name())
	}
	if r.err != nil {
		return generation.LoadSummary{}, r.err
	}
	return generation.LoadSummary{Files: len(entries)}, nil
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

func assertFiberStatus(t *testing.T, err error, status int) {
	t.Helper()
	var fiberErr *fiber.Error
	if !errors.As(err, &fiberErr) || fiberErr.Code != status {
		t.Fatalf("error = %v (%T), want fiber status %d", err, err, status)
	}
}

type generationOutcome struct {
	errIsNil    bool
	errCode     string
	errFiber    int
	resultNil   bool
	calls       int
	project     string
	generation  string
	authPath    string
	submittedBy string
	deferAct    bool
	visible     []string
	contents    map[string]string
}

func outcomeOf(result *GenerationLoadResult, err error, runner *recordingGenerationRunner) generationOutcome {
	outcome := generationOutcome{
		errIsNil:    err == nil,
		resultNil:   result == nil,
		calls:       runner.calls,
		project:     runner.req.Project,
		generation:  runner.req.Generation,
		authPath:    runner.req.AuthResourcePath,
		submittedBy: runner.req.SubmittedBy,
		deferAct:    runner.req.DeferActivation,
		visible:     append([]string(nil), runner.visible...),
		contents:    runner.contents,
	}
	if userErr, ok := dataframeerrors.AsUserError(err); ok {
		outcome.errCode = userErr.Code()
	}
	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		outcome.errFiber = fiberErr.Code
	}
	return outcome
}

func runGenerationOnTempRoot(t *testing.T, parts []orderedPart, truncate int, baseline bool, authz authscope.Authorizer, loaderErr error) generationOutcome {
	t.Helper()
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)
	if resolved := os.TempDir(); resolved != tempDir {
		t.Fatalf("TMPDIR not in effect: os.TempDir()=%s tempDir=%s", resolved, tempDir)
	}
	runner := &recordingGenerationRunner{err: loaderErr}
	service, err := NewService(ServiceConfig{LoadGeneration: runner.RunGeneration})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Config{Service: service, Authorizer: authz})
	if err != nil {
		t.Fatal(err)
	}
	body, boundary := orderedMultipart(t, parts)
	if truncate > 0 {
		body = body[:len(body)-truncate]
	}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	var result *GenerationLoadResult
	if baseline {
		result, err = handler.createDatasetGenerationBaseline(context.Background(), "P1", "generation-1", reader, nil)
	} else {
		result, err = handler.CreateDatasetGeneration(context.Background(), "P1", "generation-1", reader, nil)
	}
	outcome := outcomeOf(result, err, runner)
	assertNoTemporaryUploadFiles(t, tempDir)
	return outcome
}

func TestCreateDatasetGenerationStreamingPathMatchesShippedPath(t *testing.T) {
	small := []byte("{\"resourceType\":\"Patient\",\"id\":\"1\"}\n")

	overBudget := bytes.Repeat([]byte("z"), 20*1024*1024)

	pastBudget := bytes.Repeat([]byte("z"), 33*1024*1024)

	cases := []struct {
		name      string
		parts     []orderedPart
		loaderErr error
		deny      bool
		truncate  int
	}{
		{
			name: "files before scalar fields",
			parts: []orderedPart{
				{field: "file", filename: "Patient.ndjson", content: small},
				{field: "file", filename: "Specimen.ndjson", content: bytes.Repeat([]byte("s"), 3)},
				{field: "defer_activation", content: []byte("true")},
				{field: "auth_resource_path", content: []byte("p1")},
				{field: "auth_resource_path", content: []byte("ignored")},
			},
		},
		{
			name: "scalar fields before files",
			parts: []orderedPart{
				{field: "defer_activation", content: []byte("1")},
				{field: "auth_resource_path", content: []byte("p1")},
				{field: "file", filename: "Patient.ndjson", content: small},
			},
		},
		{
			name: "empty file part",
			parts: []orderedPart{
				{field: "file", filename: "Empty.ndjson", content: nil},
			},
		},
		{
			name: "file field without a filename is a scalar",
			parts: []orderedPart{
				{field: "file", content: []byte("scalar-under-file")},
			},
		},
		{
			name: "file parts outside the bundle field",
			parts: []orderedPart{
				{field: "other", filename: "A.ndjson", content: []byte("a\n")},
				{field: "", filename: "B.ndjson", content: []byte("b\n")},
				{field: "file", filename: "C.ndjson", content: []byte("c\n")},
			},
		},
		{
			name: "over-budget file pair",
			parts: []orderedPart{
				{field: "defer_activation", content: []byte("true")},
				{field: "file", filename: "Patient.ndjson", content: overBudget},
				{field: "file", filename: "Specimen.ndjson", content: overBudget},
			},
		},
		{

			name: "staged oversized file rejected by a later duplicate",
			parts: []orderedPart{
				{field: "file", filename: "Patient.ndjson", content: pastBudget},
				{field: "file", filename: "Patient.ndjson", content: []byte("again\n")},
			},
		},
		{
			name: "staged oversized file rejected by a later invalid name",
			parts: []orderedPart{
				{field: "file", filename: "Patient.ndjson", content: pastBudget},
				{field: "file", filename: "Specimen.txt", content: []byte("nope\n")},
			},
		},
		{
			name: "staged oversized file followed by a truncated stream",
			parts: []orderedPart{
				{field: "file", filename: "Patient.ndjson", content: pastBudget},
				{field: "file", filename: "Specimen.ndjson", content: small},
			},
			truncate: 8,
		},
		{

			name: "flushed files rejected by a later duplicate",
			parts: []orderedPart{
				{field: "file", filename: "Patient.ndjson", content: small},
				{field: "defer_activation", content: []byte("true")},
				{field: "file", filename: "Specimen.ndjson", content: small},
				{field: "file", filename: "Specimen.ndjson", content: []byte("again\n")},
			},
		},
		{
			name: "flushed files followed by a truncated stream",
			parts: []orderedPart{
				{field: "file", filename: "Patient.ndjson", content: small},
				{field: "defer_activation", content: []byte("true")},
				{field: "file", filename: "Specimen.ndjson", content: small},
			},
			truncate: 8,
		},
		{

			name: "staged oversized file past the scalar budget",
			parts: []orderedPart{
				{field: "file", filename: "Patient.ndjson", content: pastBudget},
				{field: "huge", content: bytes.Repeat([]byte("q"), 12*1024*1024)},
				{field: "file", filename: "Specimen.ndjson", content: pastBudget},
			},
		},
		{

			name: "oversized scalar value as the last part",
			parts: []orderedPart{
				{field: "file", filename: "Patient.ndjson", content: small},
				{field: "huge", content: bytes.Repeat([]byte("q"), 45*1024*1024)},
			},
		},
		{

			name:  "metadata charges exhaust the memory budget",
			parts: metadataHeavyParts(500, 96*1024),
		},
		{

			name:  "file part metadata charges exhaust the memory budget",
			parts: metadataHeavyFileParts(500, 96*1024),
		},
		{

			name:  "per-part overhead crosses the memory budget",
			parts: metadataHeavyParts(1000, 43950),
		},
		{
			name: "path separator in filename",
			parts: []orderedPart{
				{field: "file", filename: "nested/Patient.ndjson", content: small},
			},
		},
		{
			name: "windows path in filename",
			parts: []orderedPart{
				{field: "file", filename: `C:\dir\Patient.ndjson`, content: small},
			},
		},
		{
			name: "dot filename",
			parts: []orderedPart{
				{field: "file", filename: ".", content: small},
			},
		},
		{
			name: "wrong extension",
			parts: []orderedPart{
				{field: "file", filename: "Patient.txt", content: small},
			},
		},
		{
			name: "duplicate name",
			parts: []orderedPart{
				{field: "file", filename: "Patient.ndjson", content: small},
				{field: "file", filename: "Patient.ndjson", content: []byte("again\n")},
			},
		},
		{
			name: "duplicate after base normalization",
			parts: []orderedPart{
				{field: "file", filename: "Patient.ndjson", content: overBudget},
				{field: "file", filename: "nested/Patient.ndjson", content: []byte("again\n")},
			},
		},
		{
			name: "invalid defer activation",
			parts: []orderedPart{
				{field: "defer_activation", content: []byte("maybe")},
				{field: "file", filename: "Patient.ndjson", content: small},
			},
		},
		{
			name: "control byte auth resource path",
			parts: []orderedPart{
				{field: "auth_resource_path", content: []byte("a\x00b")},
				{field: "file", filename: "Patient.ndjson", content: small},
			},
		},
		{
			name: "empty auth resource path keeps single-operator posture",
			parts: []orderedPart{
				{field: "auth_resource_path", content: []byte("")},
				{field: "file", filename: "Patient.ndjson", content: small},
			},
		},
		{
			name: "loader failure",
			parts: []orderedPart{
				{field: "file", filename: "Patient.ndjson", content: overBudget},
			},
			loaderErr: errors.New("loader exploded"),
		},
		{
			name: "denied authorization",
			parts: []orderedPart{
				{field: "file", filename: "Patient.ndjson", content: overBudget},
			},
			deny: true,
		},
		{

			name: "denied authorization with an invalid file name",
			parts: []orderedPart{
				{field: "file", filename: "Patient.txt", content: small},
			},
			deny: true,
		},
		{
			name: "denied authorization with a duplicate file name",
			parts: []orderedPart{
				{field: "file", filename: "Patient.ndjson", content: small},
				{field: "file", filename: "Patient.ndjson", content: []byte("again\n")},
			},
			deny: true,
		},
		{

			name: "denied authorization with a bad name after a staged oversized file",
			parts: []orderedPart{
				{field: "file", filename: "Patient.ndjson", content: pastBudget},
				{field: "auth_resource_path", content: []byte("p1")},
				{field: "file", filename: "Specimen.txt", content: []byte("nope\n")},
			},
			deny: true,
		},
		{

			name:  "invalid file name plus too many parts",
			parts: tooManyPartsWithBadName(),
		},
		{

			name: "invalid file name plus invalid defer activation",
			parts: []orderedPart{
				{field: "file", filename: "Patient.txt", content: small},
				{field: "defer_activation", content: []byte("maybe")},
			},
		},
		{
			name: "invalid file name plus control byte auth resource path",
			parts: []orderedPart{
				{field: "file", filename: "Patient.txt", content: small},
				{field: "auth_resource_path", content: []byte("a\x00b")},
			},
		},
		{
			name: "duplicate file name plus invalid defer activation",
			parts: []orderedPart{
				{field: "file", filename: "Patient.ndjson", content: small},
				{field: "defer_activation", content: []byte("maybe")},
				{field: "file", filename: "Patient.ndjson", content: []byte("again\n")},
			},
		},
		{

			name: "invalid file name before a duplicate",
			parts: []orderedPart{
				{field: "file", filename: "Patient.txt", content: small},
				{field: "file", filename: "Specimen.ndjson", content: small},
				{field: "file", filename: "Specimen.ndjson", content: []byte("again\n")},
			},
		},
		{
			name: "duplicate file name before an invalid name",
			parts: []orderedPart{
				{field: "file", filename: "Patient.ndjson", content: small},
				{field: "file", filename: "Specimen.ndjson", content: small},
				{field: "file", filename: "Specimen.ndjson", content: []byte("again\n")},
				{field: "file", filename: "Other.txt", content: []byte("nope\n")},
			},
		},
		{
			name: "invalid file name before a duplicate, oversized parts",
			parts: []orderedPart{
				{field: "file", filename: "Patient.txt", content: pastBudget},
				{field: "file", filename: "Specimen.ndjson", content: pastBudget},
				{field: "file", filename: "Specimen.ndjson", content: pastBudget},
			},
		},
		{

			name: "oversized invalid-name part then a scalar past the budget",
			parts: []orderedPart{
				{field: "file", filename: "Patient.txt", content: pastBudget},
				{field: "huge", content: bytes.Repeat([]byte("q"), 13*1024*1024)},
				{field: "file", filename: "Specimen.ndjson", content: small},
			},
		},
		{
			name: "oversized duplicate-name part then a scalar past the budget",
			parts: []orderedPart{
				{field: "file", filename: "Patient.ndjson", content: pastBudget},
				{field: "file", filename: "Patient.ndjson", content: pastBudget},
				{field: "huge", content: bytes.Repeat([]byte("q"), 13*1024*1024)},
				{field: "file", filename: "Specimen.ndjson", content: small},
			},
		},
		{

			name: "oversized non-bundle file part then a scalar past the budget",
			parts: []orderedPart{
				{field: "other", filename: "junk.txt", content: pastBudget},
				{field: "huge", content: bytes.Repeat([]byte("q"), 13*1024*1024)},
				{field: "file", filename: "Specimen.ndjson", content: small},
			},
		},
		{

			name:  "many non-bundle file parts then a scalar past the budget",
			parts: nonBundleFillerParts(200, 160000, 13*1024*1024),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var authz authscope.Authorizer = authscope.AllowAllAuthorizer{}
			if tc.deny {
				authz = rejectingAuthorizer{}
			}
			want := runGenerationOnTempRoot(t, tc.parts, tc.truncate, true, authz, tc.loaderErr)
			got := runGenerationOnTempRoot(t, tc.parts, tc.truncate, false, authz, tc.loaderErr)
			if diff := compareOutcomes(want, got); diff != "" {
				t.Fatalf("streaming path diverged from the shipped path:\n%s", diff)
			}
		})
	}
}

func metadataHeavyParts(n, nameLen int) []orderedPart {
	name := strings.Repeat("n", nameLen)
	parts := make([]orderedPart, 0, n+1)
	for i := 0; i < n; i++ {
		parts = append(parts, orderedPart{field: fmt.Sprintf("f%d-%s", i, name), content: []byte("v")})
	}
	return parts
}

func metadataHeavyFileParts(n, nameLen int) []orderedPart {
	name := strings.Repeat("n", nameLen)
	parts := make([]orderedPart, 0, n+1)
	for i := 0; i < n; i++ {
		parts = append(parts, orderedPart{field: "file", filename: fmt.Sprintf("f%d-%s.ndjson", i, name), content: []byte("x\n")})
	}
	return parts
}

func tooManyPartsWithBadName() []orderedPart {
	parts := []orderedPart{{field: "file", filename: "Patient.txt", content: []byte("nope\n")}}
	for i := 0; i <= generationFormLimits().maxParts; i++ {
		parts = append(parts, orderedPart{field: "f", content: []byte("v")})
	}
	return parts
}

func nonBundleFillerParts(n, bytesPerPart, scalarBytes int) []orderedPart {
	parts := make([]orderedPart, 0, n+2)
	filler := bytes.Repeat([]byte("f"), bytesPerPart)
	for i := 0; i < n; i++ {
		parts = append(parts, orderedPart{field: "other", filename: fmt.Sprintf("junk%d.bin", i), content: filler})
	}
	parts = append(parts, orderedPart{field: "huge", content: bytes.Repeat([]byte("q"), scalarBytes)})
	parts = append(parts, orderedPart{field: "file", filename: "Patient.ndjson", content: []byte("{}\n")})
	return parts
}

func compareOutcomes(want, got generationOutcome) string {
	var diffs []string
	check := func(field string, wantValue, gotValue any) {
		if fmt.Sprint(wantValue) != fmt.Sprint(gotValue) {
			diffs = append(diffs, fmt.Sprintf("%s: shipped=%v streaming=%v", field, wantValue, gotValue))
		}
	}
	check("error present", want.errIsNil, got.errIsNil)
	check("error code", want.errCode, got.errCode)
	check("fiber status", want.errFiber, got.errFiber)
	check("result present", want.resultNil, got.resultNil)
	check("loader calls", want.calls, got.calls)
	check("project", want.project, got.project)
	check("generation", want.generation, got.generation)
	check("auth path", want.authPath, got.authPath)
	check("submitted by", want.submittedBy, got.submittedBy)
	check("defer activation", want.deferAct, got.deferAct)
	check("loader-visible files", want.visible, got.visible)
	if len(want.contents) != len(got.contents) {
		diffs = append(diffs, fmt.Sprintf("staged file count: shipped=%d streaming=%d", len(want.contents), len(got.contents)))
	}
	for name, content := range want.contents {
		gotContent, ok := got.contents[name]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("staged %s missing from the streaming path", name))
			continue
		}
		if !bytes.Equal([]byte(content), []byte(gotContent)) {
			diffs = append(diffs, fmt.Sprintf("staged %s not byte-identical: shipped=%d bytes streaming=%d bytes", name, len(content), len(gotContent)))
		}
	}
	return strings.Join(diffs, "\n")
}

func TestCreateDatasetGenerationParserLimitsMatchShippedPath(t *testing.T) {
	small := []byte("{\"resourceType\":\"Patient\",\"id\":\"1\"}\n")
	authz := authscope.AllowAllAuthorizer{}

	t.Run("part count limit", func(t *testing.T) {
		parts := make([]orderedPart, 0, generationFormLimits().maxParts+2)
		for i := 0; i <= generationFormLimits().maxParts; i++ {
			parts = append(parts, orderedPart{field: "f", content: []byte("v")})
		}
		parts = append(parts, orderedPart{field: "file", filename: "Patient.ndjson", content: small})
		want := runGenerationOnTempRoot(t, parts, 0, true, authz, nil)
		got := runGenerationOnTempRoot(t, parts, 0, false, authz, nil)
		if want.errIsNil || got.errCode != want.errCode || got.calls != 0 {
			t.Fatalf("part-count limit: shipped=%+v streaming=%+v", want, got)
		}
	})

	t.Run("scalar value budget", func(t *testing.T) {

		oversized := bytes.Repeat([]byte("z"), 45*1024*1024)
		parts := []orderedPart{
			{field: "huge", content: oversized},
			{field: "file", filename: "Patient.ndjson", content: small},
		}
		want := runGenerationOnTempRoot(t, parts, 0, true, authz, nil)
		got := runGenerationOnTempRoot(t, parts, 0, false, authz, nil)
		if want.errIsNil || got.errCode != want.errCode || got.calls != 0 {
			t.Fatalf("scalar budget: shipped=%+v streaming=%+v", want, got)
		}
	})

	t.Run("godebug part override reaches both paths", func(t *testing.T) {
		parts := make([]orderedPart, 0, 6)
		for i := 0; i < 5; i++ {
			parts = append(parts, orderedPart{field: "f", content: []byte("v")})
		}
		parts = append(parts, orderedPart{field: "file", filename: "Patient.ndjson", content: small})
		t.Setenv("GODEBUG", "multipartmaxparts=4")
		if limits := generationFormLimits(); limits.maxParts != 4 {
			t.Fatalf("multipartmaxparts override ignored: %+v", limits)
		}
		want := runGenerationOnTempRoot(t, parts, 0, true, authz, nil)
		got := runGenerationOnTempRoot(t, parts, 0, false, authz, nil)
		if want.errIsNil || got.errCode != want.errCode || got.calls != 0 {
			t.Fatalf("multipartmaxparts: shipped=%+v streaming=%+v", want, got)
		}
	})
}

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

const spilloverFileBytes = 20 * 1024 * 1024

const measurementReps = 3

type spilloverRunner struct {
	project       string
	stagedAt      []int
	spillsAtLoad  []string
	stagedBytesAt int64
	spillBytesAt  int64
	otherBytesAt  int64
}

func (r *spilloverRunner) RunGeneration(_ context.Context, req GenerationLoadRequest, _ ingestevent.EventSink) (generation.LoadSummary, error) {
	r.project = req.Project
	r.stagedAt = nil
	r.spillsAtLoad = nil
	r.stagedBytesAt = 0
	r.spillBytesAt = 0
	r.otherBytesAt = 0
	for _, name := range []string{"Patient.ndjson", "Specimen.ndjson"} {
		data, err := os.ReadFile(filepath.Join(req.StagedDir, name))
		if err != nil {
			return generation.LoadSummary{}, fmt.Errorf("staged %s unreadable at load time: %w", name, err)
		}
		r.stagedAt = append(r.stagedAt, len(data))
		r.stagedBytesAt += int64(len(data))
	}
	snapshot, err := snapshotTempRoot(req.StagedDir)
	if err != nil {
		return generation.LoadSummary{}, err
	}
	r.spillsAtLoad = snapshot.spillNames
	r.spillBytesAt = snapshot.spillBytes
	r.otherBytesAt = snapshot.otherBytes
	return generation.LoadSummary{Files: 2}, nil
}

type tempRootSnapshot struct {
	spillBytes int64
	spillNames []string
	otherBytes int64
}

func snapshotTempRoot(stagingDir string) (tempRootSnapshot, error) {
	var snapshot tempRootSnapshot
	root := os.TempDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		return snapshot, fmt.Errorf("read temp root: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() == filepath.Base(stagingDir) {
			continue
		}
		size, err := dirBytes(filepath.Join(root, entry.Name()))
		if err != nil {
			return snapshot, fmt.Errorf("measure %s: %w", entry.Name(), err)
		}
		if strings.HasPrefix(entry.Name(), "multipart") {
			snapshot.spillBytes += size
			snapshot.spillNames = append(snapshot.spillNames, entry.Name())
			continue
		}
		snapshot.otherBytes += size
	}
	return snapshot, nil
}

func dirBytes(path string) (int64, error) {
	var total int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

type tempRootSampler struct {
	root   string
	peak   atomic.Int64
	done   chan struct{}
	closed sync.Once
}

func newTempRootSampler(root string) *tempRootSampler {
	sampler := &tempRootSampler{root: root, done: make(chan struct{})}
	go func() {
		ticker := time.NewTicker(200 * time.Microsecond)
		defer ticker.Stop()
		for {
			select {
			case <-sampler.done:
				return
			case <-ticker.C:
				if total, err := dirBytes(sampler.root); err == nil && total > sampler.peak.Load() {
					sampler.peak.Store(total)
				}
			}
		}
	}()
	return sampler
}

func (s *tempRootSampler) stop() int64 {
	s.closed.Do(func() { close(s.done) })
	return s.peak.Load()
}

type stagingRun struct {
	path        string
	rep         int
	payload     int64
	stagedBytes int64
	spillBytes  int64
	spillFiles  int
	otherBytes  int64
	peakBytes   int64
	elapsed     time.Duration
}

func (r stagingRun) String() string {
	return fmt.Sprintf("path=%s rep=%d payload=%d staged=%d spill=%d(%d files) other=%d peak=%d elapsed_ms=%.1f",
		r.path, r.rep, r.payload, r.stagedBytes, r.spillBytes, r.spillFiles, r.otherBytes, r.peakBytes,
		float64(r.elapsed.Microseconds())/1000)
}

func measureStagingRun(t *testing.T, path string, rep int, baseline bool) stagingRun {
	t.Helper()
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)
	if resolved := os.TempDir(); resolved != tempDir {
		t.Fatalf("TMPDIR not in effect: os.TempDir()=%s tempDir=%s", resolved, tempDir)
	}
	runner := &spilloverRunner{}
	handler := spilloverHandler(t, runner)
	body, boundary := spilloverMultipart(t, true)
	payload := int64(len(body))

	sampler := newTempRootSampler(tempDir)
	start := time.Now()
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	var err error
	if baseline {
		_, err = handler.createDatasetGenerationBaseline(context.Background(), "P1", "generation-1", reader, nil)
	} else {
		_, err = handler.CreateDatasetGeneration(context.Background(), "P1", "generation-1", reader, nil)
	}
	elapsed := time.Since(start)
	peak := sampler.stop()

	if err != nil {
		t.Fatalf("%s rep %d: load: %v", path, rep, err)
	}
	if orphans := countOrphanMultipartFiles(t, tempDir); len(orphans) != 0 {
		t.Fatalf("%s rep %d: temp files orphaned after load: %v", path, rep, orphans)
	}
	if leftovers := listUploadTempFiles(t, tempDir); len(leftovers) != 0 {
		t.Fatalf("%s rep %d: temporary upload files left behind: %v", path, rep, leftovers)
	}
	return stagingRun{
		path:        path,
		rep:         rep,
		payload:     payload,
		stagedBytes: runner.stagedBytesAt,
		spillBytes:  runner.spillBytesAt,
		spillFiles:  len(runner.spillsAtLoad),
		otherBytes:  runner.otherBytesAt,
		peakBytes:   peak,
		elapsed:     elapsed,
	}
}

func spilloverHandler(t *testing.T, runner *spilloverRunner) *Handler {
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

func spilloverMultipart(t *testing.T, deferActivation bool) ([]byte, string) {
	t.Helper()
	chunk := bytes.Repeat([]byte("x"), spilloverFileBytes)
	return generationMultipart(t, deferActivation, map[string][]byte{
		"Patient.ndjson":  chunk,
		"Specimen.ndjson": chunk,
	})
}

func listUploadTempFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	var leftovers []string
	for _, entry := range entries {
		if name := entry.Name(); strings.HasPrefix(name, "loom-generation-") || strings.HasPrefix(name, "multipart") {
			leftovers = append(leftovers, name)
		}
	}
	return leftovers
}

func countOrphanMultipartFiles(t *testing.T, dir string) []string {
	t.Helper()
	var orphans []string
	for _, name := range listUploadTempFiles(t, dir) {
		if strings.HasPrefix(name, "multipart") {
			orphans = append(orphans, name)
		}
	}
	return orphans
}

func TestCreateDatasetGenerationLeaksNoSpilloverTempFiles(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)
	runner := &spilloverRunner{}
	handler := spilloverHandler(t, runner)
	body, boundary := spilloverMultipart(t, true)

	if _, err := handler.CreateDatasetGeneration(context.Background(), "P1", "generation-1", multipart.NewReader(bytes.NewReader(body), boundary), nil); err != nil {
		t.Fatalf("load: %v", err)
	}

	if orphans := countOrphanMultipartFiles(t, tempDir); len(orphans) != 0 {
		t.Fatalf("multipart spillover temp files orphaned in TMPDIR after load: %v", orphans)
	}

	if runner.project != "P1" || len(runner.stagedAt) != 2 {
		t.Fatalf("loader view of the bundle: project=%s staged=%v", runner.project, runner.stagedAt)
	}
	for i, size := range runner.stagedAt {
		if size != spilloverFileBytes {
			t.Fatalf("staged file %d size=%d want=%d", i, size, spilloverFileBytes)
		}
	}

	if runner.spillsAtLoad != nil {
		t.Fatalf("duplicate multipart spill present while the loader ran: %v", runner.spillsAtLoad)
	}
	if runner.stagedBytesAt != 2*spilloverFileBytes {
		t.Fatalf("staged bytes at load time = %d, want %d", runner.stagedBytesAt, 2*spilloverFileBytes)
	}
	if runner.spillBytesAt != 0 || runner.otherBytesAt != 0 {
		t.Fatalf("temporary bytes outside the staged bundle while the loader ran = spill %d other %d, want 0 0",
			runner.spillBytesAt, runner.otherBytesAt)
	}
	t.Logf("SPLILLOVER_DISPOSED_ORPHANS=0 STAGED_AT_LOAD_TIME=2 DUPLICATE_TEMP_BYTES=0 loaderProject=%s", runner.project)
}

func TestCreateDatasetGenerationTemporaryBytesBeforeAfter(t *testing.T) {
	var before, after []stagingRun
	for rep := 1; rep <= measurementReps; rep++ {

		if rep%2 == 0 {
			after = append(after, measureStagingRun(t, "streaming", rep, false))
		}
		before = append(before, measureStagingRun(t, "readform-copy", rep, true))
		if rep%2 == 1 {
			after = append(after, measureStagingRun(t, "streaming", rep, false))
		}
	}

	payload := int64(2 * spilloverFileBytes)
	for _, run := range before {
		t.Logf("BEFORE %s", run)
		if run.stagedBytes != payload {
			t.Fatalf("shipped path staged %d bytes, want %d", run.stagedBytes, payload)
		}
		if run.spillFiles == 0 || run.spillBytes == 0 {
			t.Fatalf("shipped path produced no duplicate spill on a payload above the ReadForm budget; the measurement no longer exercises the behavior it compares (%s)", run)
		}
		if run.peakBytes <= payload {
			t.Fatalf("shipped path peak temp bytes %d does not exceed the %d byte payload; the sampler missed the duplicate (%s)", run.peakBytes, payload, run)
		}
	}
	for _, run := range after {
		t.Logf("AFTER  %s", run)
		if run.stagedBytes != payload {
			t.Fatalf("streaming path staged %d bytes, want %d", run.stagedBytes, payload)
		}
		if run.spillFiles != 0 || run.spillBytes != 0 {
			t.Fatalf("streaming path created a duplicate multipart spill: %s", run)
		}
		if run.otherBytes != 0 {
			t.Fatalf("streaming path left %d other temporary bytes behind: %s", run.otherBytes, run)
		}
		if run.peakBytes != payload {
			t.Fatalf("streaming path peak temp bytes = %d, want exactly the %d byte payload (a second copy would exceed it)", run.peakBytes, payload)
		}
	}

	t.Logf("MEASUREMENT payload_bytes=%d reps=%d temp_fs=%s", payload, measurementReps, tempFSType())
	t.Logf("MEASUREMENT BEFORE peak_bytes=%v", peakSeries(before))
	t.Logf("MEASUREMENT AFTER  peak_bytes=%v", peakSeries(after))
	t.Logf("MEASUREMENT BEFORE spill_bytes=%v", spillSeries(before))
	t.Logf("MEASUREMENT AFTER  spill_bytes=%v", spillSeries(after))
	t.Logf("MEASUREMENT elapsed_ms BEFORE median=%.1f AFTER median=%.1f (recorded, not asserted)",
		medianMillis(before), medianMillis(after))
}

func peakSeries(runs []stagingRun) []int64 {
	values := make([]int64, 0, len(runs))
	for _, run := range runs {
		values = append(values, run.peakBytes)
	}
	return values
}

func spillSeries(runs []stagingRun) []int64 {
	values := make([]int64, 0, len(runs))
	for _, run := range runs {
		values = append(values, run.spillBytes)
	}
	return values
}

func medianMillis(runs []stagingRun) float64 {
	values := make([]float64, 0, len(runs))
	for _, run := range runs {
		values = append(values, float64(run.elapsed.Microseconds())/1000)
	}
	sort.Float64s(values)
	if len(values) == 0 {
		return 0
	}
	return values[len(values)/2]
}

func tempFSType() string {
	if data, err := os.ReadFile("/proc/mounts"); err == nil {
		root := os.TempDir()
		best, bestType := 0, "unknown"
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			if strings.HasPrefix(root, fields[1]) && len(fields[1]) > best {
				best, bestType = len(fields[1]), fields[2]
			}
		}
		return bestType
	}
	return "unknown"
}

func TestCreateDatasetGenerationSpilloverDisposedOnValidationRejection(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)
	runner := &spilloverRunner{}
	handler := spilloverHandler(t, runner)
	body, boundary := spilloverMultipart(t, true)

	if _, err := handler.CreateDatasetGeneration(context.Background(), "P1", "  ", multipart.NewReader(bytes.NewReader(body), boundary), nil); err == nil {
		t.Fatal("expected validation rejection for blank generation")
	}

	if orphans := countOrphanMultipartFiles(t, tempDir); len(orphans) != 0 {
		t.Fatalf("multipart spillover temp files orphaned after validation rejection: %v", orphans)
	}
	if leftovers := listUploadTempFiles(t, tempDir); len(leftovers) != 0 {
		t.Fatalf("staged files left behind after validation rejection: %v", leftovers)
	}
}
