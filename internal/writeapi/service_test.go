package writeapi

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"arangodb-proto/internal/proto"
)

type fakeRunner struct {
	summary proto.LoadSummary
	err     error
	delay   time.Duration
}

func (r fakeRunner) Run(ctx context.Context, req ImportRequest, sink proto.EventSink) (proto.LoadSummary, error) {
	sink("go_load_file_start", map[string]any{"resource": req.ResourceType})
	if r.delay > 0 {
		time.Sleep(r.delay)
	}
	if r.err != nil {
		return proto.LoadSummary{}, r.err
	}
	sink("go_load_complete", map[string]any{"vertices_inserted": r.summary.VerticesInserted})
	return r.summary, nil
}

func TestServiceLifecycleSuccess(t *testing.T) {
	tmp, err := os.CreateTemp("", "writeapi-service-*.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	svc, err := NewService(ServiceConfig{
		Runner: fakeRunner{summary: proto.LoadSummary{Files: 1, VerticesInserted: 3, EdgesInserted: 5}},
	})
	if err != nil {
		t.Fatal(err)
	}

	op, err := svc.Submit(context.Background(), ImportRequest{
		Project:          "P1",
		ResourceType:     "Patient",
		StagedFilePath:   tmp.Name(),
		OriginalFilename: "Patient.ndjson",
	})
	if err != nil {
		t.Fatal(err)
	}
	if op.Status != StatusPending {
		t.Fatalf("status = %s, want %s", op.Status, StatusPending)
	}

	final := waitForStatus(t, svc, op.ID)
	if final.Status != StatusSucceeded {
		t.Fatalf("status = %s, want %s", final.Status, StatusSucceeded)
	}
	if final.Summary == nil || final.Summary.VerticesInserted != 3 || final.Summary.EdgesInserted != 5 {
		t.Fatalf("unexpected summary: %#v", final.Summary)
	}
	events, ok := svc.Events(op.ID)
	if !ok {
		t.Fatal("events not found")
	}
	if len(events) < 4 {
		t.Fatalf("expected lifecycle and loader events, got %d", len(events))
	}
}

func TestServiceLifecycleFailure(t *testing.T) {
	tmp, err := os.CreateTemp("", "writeapi-service-*.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	svc, err := NewService(ServiceConfig{
		Runner: fakeRunner{err: errors.New("boom")},
	})
	if err != nil {
		t.Fatal(err)
	}

	op, err := svc.Submit(context.Background(), ImportRequest{
		Project:          "P1",
		ResourceType:     "Patient",
		StagedFilePath:   tmp.Name(),
		OriginalFilename: "Patient.ndjson",
	})
	if err != nil {
		t.Fatal(err)
	}

	final := waitForStatus(t, svc, op.ID)
	if final.Status != StatusFailed {
		t.Fatalf("status = %s, want %s", final.Status, StatusFailed)
	}
	if final.Error == "" {
		t.Fatal("expected error to be recorded")
	}
}

func TestServiceSuccessInvalidatesProject(t *testing.T) {
	tmp, err := os.CreateTemp("", "writeapi-service-*.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	var invalidated []string
	svc, err := NewService(ServiceConfig{
		Runner: fakeRunner{summary: proto.LoadSummary{Files: 1, VerticesInserted: 3, EdgesInserted: 5}},
		OnSuccess: func(project string) {
			invalidated = append(invalidated, project)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	op, err := svc.Submit(context.Background(), ImportRequest{
		Project:          "P1",
		ResourceType:     "Patient",
		StagedFilePath:   tmp.Name(),
		OriginalFilename: "Patient.ndjson",
	})
	if err != nil {
		t.Fatal(err)
	}
	final := waitForStatus(t, svc, op.ID)
	if final.Status != StatusSucceeded {
		t.Fatalf("status = %s, want %s", final.Status, StatusSucceeded)
	}
	if len(invalidated) != 1 || invalidated[0] != "P1" {
		t.Fatalf("invalidated = %#v", invalidated)
	}
}

func TestServiceFailureDoesNotInvalidateProject(t *testing.T) {
	tmp, err := os.CreateTemp("", "writeapi-service-*.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

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

	op, err := svc.Submit(context.Background(), ImportRequest{
		Project:          "P1",
		ResourceType:     "Patient",
		StagedFilePath:   tmp.Name(),
		OriginalFilename: "Patient.ndjson",
	})
	if err != nil {
		t.Fatal(err)
	}
	final := waitForStatus(t, svc, op.ID)
	if final.Status != StatusFailed {
		t.Fatalf("status = %s, want %s", final.Status, StatusFailed)
	}
	if called {
		t.Fatal("expected no invalidation on failure")
	}
}

func waitForStatus(t *testing.T, svc *Service, id string) Operation {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		op, ok := svc.Get(id)
		if !ok {
			t.Fatalf("operation %s not found", id)
		}
		if op.Status == StatusSucceeded || op.Status == StatusFailed {
			return op
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for operation %s", id)
	return Operation{}
}
