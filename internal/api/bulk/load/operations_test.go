package load

import (
	"bytes"
	"context"
	"mime/multipart"
	"testing"

	"github.com/calypr/loom/internal/authscope"
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
