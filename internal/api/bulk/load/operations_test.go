package load

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest"
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
