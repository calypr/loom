package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestExecutePostsGraphQLJSON(t *testing.T) {
	client := &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost {
			t.Fatalf("method = %s", request.Method)
		}
		if got := request.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("content type = %q", got)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), `"query":"query { ping }"`) {
			t.Fatalf("unexpected request body: %s", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"data":{"ping":"pong"}}`)),
			Header:     make(http.Header),
		}, nil
	})}

	body, err := execute(client, "http://example.test/graphql", []byte(`{"query":"query { ping }","variables":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"data":{"ping":"pong"}}` {
		t.Fatalf("body = %s", body)
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (fn roundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestSummarizeDurations(t *testing.T) {
	minimum, average, maximum := summarizeDurations([]time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond})
	if minimum != 10*time.Millisecond || average != 20*time.Millisecond || maximum != 30*time.Millisecond {
		t.Fatalf("summary = %s, %s, %s", minimum, average, maximum)
	}
}

func TestWithLimit(t *testing.T) {
	variables, err := withLimit([]byte(`{"limit":25,"input":{"project":"P1"}}`), 1000)
	if err != nil {
		t.Fatal(err)
	}
	if string(variables) != `{"input":{"project":"P1"},"limit":1000}` {
		t.Fatalf("variables = %s", variables)
	}
}

func TestInspectDataframeResponse(t *testing.T) {
	metrics := inspectDataframeResponse([]byte(`{"data":{"runFhirDataframe":{"rowCount":25,"rows":[{"id":"1"}]}}}`), time.Second)
	if metrics.Rows != 25 {
		t.Fatalf("rows = %d, want 25", metrics.Rows)
	}
	if metrics.Bytes == 0 || metrics.RowsPerSecond != 25 {
		t.Fatalf("metrics = %+v", metrics)
	}
}

func TestInspectDataframeResponseFallsBackToRowsLength(t *testing.T) {
	metrics := inspectDataframeResponse([]byte(`{"data":{"runFhirDataframe":{"rows":[{"id":"1"},{"id":"2"}]}}}`), 2*time.Second)
	if metrics.Rows != 2 || metrics.RowsPerSecond != 1 {
		t.Fatalf("metrics = %+v", metrics)
	}
}
