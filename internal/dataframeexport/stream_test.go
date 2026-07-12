package dataframeexport

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe"
	"github.com/calypr/loom/internal/export"
)

func TestNewRowStreamForwardsRowsAndRequest(t *testing.T) {
	request := dataframe.RunRequest{
		Builder: dataframe.Builder{Project: "project-a", RootResourceType: "Patient"},
		Limit:   9,
	}
	runner := &fakeRunner{rows: []map[string]any{
		{"patient_id": "p1", "status": "active"},
		{"patient_id": "p2", "status": "inactive"},
	}}
	stream, err := NewRowStream(runner, request)
	if err != nil {
		t.Fatalf("NewRowStream() error = %v", err)
	}

	var got []map[string]any
	err = stream(context.Background(), func(row map[string]any) error {
		got = append(got, row)
		return nil
	})
	if err != nil {
		t.Fatalf("RowStream() error = %v", err)
	}
	if !reflect.DeepEqual(got, runner.rows) {
		t.Fatalf("forwarded rows = %#v, want %#v", got, runner.rows)
	}
	if runner.calls != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.calls)
	}
	if !reflect.DeepEqual(runner.requests, []dataframe.RunRequest{request}) {
		t.Fatalf("runner requests = %#v, want %#v", runner.requests, []dataframe.RunRequest{request})
	}
}

func TestNewRowStreamPreservesRunnerAndVisitorErrors(t *testing.T) {
	t.Run("runner", func(t *testing.T) {
		runnerErr := errors.New("runner stopped")
		stream, err := NewRowStream(&fakeRunner{err: runnerErr}, dataframe.RunRequest{})
		if err != nil {
			t.Fatalf("NewRowStream() error = %v", err)
		}
		if err := stream(context.Background(), func(map[string]any) error { return nil }); !errors.Is(err, runnerErr) {
			t.Fatalf("RowStream() error = %v, want runner error", err)
		}
	})

	t.Run("visitor", func(t *testing.T) {
		visitorErr := errors.New("stop after first row")
		stream, err := NewRowStream(&fakeRunner{rows: []map[string]any{{"id": "one"}}}, dataframe.RunRequest{})
		if err != nil {
			t.Fatalf("NewRowStream() error = %v", err)
		}
		if err := stream(context.Background(), func(map[string]any) error { return visitorErr }); !errors.Is(err, visitorErr) {
			t.Fatalf("RowStream() error = %v, want visitor error", err)
		}
	})
}

func TestNewRowStreamPreservesCanceledContextWithoutCallingRunner(t *testing.T) {
	runner := &fakeRunner{rows: []map[string]any{{"id": "one"}}}
	stream, err := NewRowStream(runner, dataframe.RunRequest{})
	if err != nil {
		t.Fatalf("NewRowStream() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = stream(ctx, func(map[string]any) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RowStream() error = %v, want context.Canceled", err)
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls = %d, want 0 after cancellation", runner.calls)
	}
}

func TestEncodeCSVWithExplicitColumnsRunsOneDataframeStream(t *testing.T) {
	request := dataframe.RunRequest{Builder: dataframe.Builder{Project: "project-a"}, Limit: 2}
	runner := &fakeRunner{rows: []map[string]any{
		{"patient_id": "p1", "status": "active"},
		{"patient_id": "p2", "status": "inactive"},
	}}
	var output bytes.Buffer

	result, err := EncodeCSV(context.Background(), &output, export.CSVOptions{Columns: []string{"patient_id", "status"}}, runner, request)
	if err != nil {
		t.Fatalf("EncodeCSV() error = %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("runner calls = %d, want one explicit-schema pass", runner.calls)
	}
	if result.Rows != 2 {
		t.Fatalf("rows = %d, want 2", result.Rows)
	}
	if got, want := output.String(), "patient_id,status\np1,active\np2,inactive\n"; got != want {
		t.Fatalf("CSV = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(runner.requests, []dataframe.RunRequest{request}) {
		t.Fatalf("runner requests = %#v, want one original request", runner.requests)
	}
}

func TestEncodeCSVWithInferredColumnsReplaysDataframeStream(t *testing.T) {
	request := dataframe.RunRequest{Builder: dataframe.Builder{Project: "project-a"}, Limit: 2}
	runner := &fakeRunner{rows: []map[string]any{
		{"patient_id": "p1", "status": "active"},
		{"patient_id": "p2", "status": "inactive"},
	}}
	var output bytes.Buffer

	result, err := EncodeCSV(context.Background(), &output, export.CSVOptions{}, runner, request)
	if err != nil {
		t.Fatalf("EncodeCSV() error = %v", err)
	}
	if runner.calls != 2 {
		t.Fatalf("runner calls = %d, want discovery and writing passes", runner.calls)
	}
	if result.Rows != 2 {
		t.Fatalf("rows = %d, want 2 from the writing pass", result.Rows)
	}
	if got, want := output.String(), "patient_id,status\np1,active\np2,inactive\n"; got != want {
		t.Fatalf("CSV = %q, want %q", got, want)
	}
	wantRequests := []dataframe.RunRequest{request, request}
	if !reflect.DeepEqual(runner.requests, wantRequests) {
		t.Fatalf("runner requests = %#v, want %#v", runner.requests, wantRequests)
	}
}

func TestNewRowStreamRejectsNilRunner(t *testing.T) {
	stream, err := NewRowStream(nil, dataframe.RunRequest{})
	if err == nil {
		t.Fatal("NewRowStream() error = nil, want runner-required error")
	}
	if stream != nil {
		t.Fatalf("NewRowStream() stream = %v, want nil", stream)
	}
}

type fakeRunner struct {
	rows     []map[string]any
	err      error
	calls    int
	requests []dataframe.RunRequest
}

func (r *fakeRunner) Stream(ctx context.Context, request dataframe.RunRequest, visit func(map[string]any) error) (dataframe.StreamResult, error) {
	r.calls++
	r.requests = append(r.requests, request)
	var count int
	for _, row := range r.rows {
		if err := ctx.Err(); err != nil {
			return dataframe.StreamResult{RowCount: count}, err
		}
		if err := visit(row); err != nil {
			return dataframe.StreamResult{RowCount: count}, err
		}
		count++
	}
	if r.err != nil {
		return dataframe.StreamResult{RowCount: count}, r.err
	}
	return dataframe.StreamResult{RowCount: count}, nil
}
