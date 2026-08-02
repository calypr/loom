package load

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/ingest"
)

type fakeRunner struct {
	summary ingest.LoadSummary
	err     error
}

func (r fakeRunner) Run(ctx context.Context, req ImportRequest, sink ingest.EventSink) (ingest.LoadSummary, error) {
	if r.err != nil {
		return ingest.LoadSummary{}, r.err
	}
	return r.summary, nil
}

func TestServiceRunSuccess(t *testing.T) {
	var invalidated []string
	svc, err := NewService(ServiceConfig{
		Runner: fakeRunner{summary: ingest.LoadSummary{Files: 1, VerticesInserted: 3, EdgesInserted: 5}},
		OnSuccess: func(project string) {
			invalidated = append(invalidated, project)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := svc.Run(context.Background(), ImportRequest{
		Project:          "P1",
		ResourceType:     "Patient",
		StagedFilePath:   "/tmp/patient.ndjson",
		OriginalFilename: "Patient.ndjson",
		SubmittedBy:      "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary == nil || result.Summary.VerticesInserted != 3 || result.Summary.EdgesInserted != 5 {
		t.Fatalf("unexpected summary: %#v", result.Summary)
	}
	if len(invalidated) != 1 || invalidated[0] != "P1" {
		t.Fatalf("invalidated = %#v", invalidated)
	}
}

func TestServiceRunFailure(t *testing.T) {
	called := false
	svc, err := NewService(ServiceConfig{
		Runner: fakeRunner{err: errors.New("boom")},
		OnSuccess: func(project string) {
			called = true
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.Run(context.Background(), ImportRequest{
		Project:        "P1",
		ResourceType:   "Patient",
		StagedFilePath: "/tmp/patient.ndjson",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if called {
		t.Fatal("expected no invalidation on failure")
	}
}

func TestNewHandlerRequiresAuthorizer(t *testing.T) {
	service, err := NewService(ServiceConfig{Runner: fakeRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewHandler(Config{Service: service}); err == nil {
		t.Fatal("expected missing authorizer error")
	}
}
