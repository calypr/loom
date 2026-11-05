package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// TestWithLimitPreservesLargeNumbers pins the second cli-boundary-probe
// finding: a float64 decode silently rewrote 9007199254740993 into
// 9007199254740992, so a variables file can never be re-encoded through
// float64.
func TestWithLimitPreservesLargeNumbers(t *testing.T) {
	original := `{"input":{"project":"P1","window":9007199254740993}}`
	variables, err := withLimit([]byte(original), 7)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(variables), `9007199254740993`) {
		t.Fatalf("large integer lost precision: %s", variables)
	}
	if !strings.Contains(string(variables), `"limit":7`) {
		t.Fatalf("limit not overridden: %s", variables)
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

func TestClassifyGraphQLEnvelopeAcceptsDataOnly(t *testing.T) {
	if err := classifyGraphQLEnvelope([]byte(`{"data":{"runFhirDataframe":{"rowCount":2,"rows":[{"id":"1"},{"id":"2"}]}}}`)); err != nil {
		t.Fatalf("want success for data-only response, got %v", err)
	}
	if err := classifyGraphQLEnvelope([]byte(`{"data":{"runFhirDataframe":{"rowCount":0,"rows":[]}},"errors":null}`)); err != nil {
		t.Fatalf("want success for an empty-but-successful query, got %v", err)
	}
	if err := classifyGraphQLEnvelope([]byte(`{"data":{"runFhirDataframe":{"rowCount":1,"rows":[{}]}},"errors":[]}`)); err != nil {
		t.Fatalf("want success for an empty errors array, got %v", err)
	}
}

func TestClassifyGraphQLEnvelopeRejectsHTTPOkErrors(t *testing.T) {
	err := classifyGraphQLEnvelope([]byte(`{"errors":[{"message":"Unknown type FhirDataframeInput","locations":[{"line":1,"column":10}]}]}`))
	if err == nil {
		t.Fatal("want error for HTTP-200 GraphQL errors with no data")
	}
	message := err.Error()
	for _, want := range []string{"GraphQL operation reported errors", "Unknown type FhirDataframeInput", "line 1, column 10"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q missing %q", message, want)
		}
	}
}

func TestClassifyGraphQLEnvelopeRejectsPartialSuccess(t *testing.T) {
	err := classifyGraphQLEnvelope([]byte(`{"data":{"runFhirDataframe":{"rowCount":2,"rows":[{"id":"1"},{"id":"2"}]}},"errors":[{"message":"traversal failed","path":["runFhirDataframe","rows",1]}]}`))
	if err == nil {
		t.Fatal("want error when data and errors are both present")
	}
	message := err.Error()
	for _, want := range []string{"incomplete", "traversal failed", "runFhirDataframe.rows.1"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q missing %q", message, want)
		}
	}
}

func TestClassifyGraphQLEnvelopeRejectsMissingPayload(t *testing.T) {
	for _, body := range []string{
		`{"data":null}`,
		`{"data":{"runFhirDataframe":null}}`,
		`{"data":{"ping":"pong"}}`,
		`{}`,
		`{"errors":[{"message":"boom"}],"data":null}`,
	} {
		if err := classifyGraphQLEnvelope([]byte(body)); err == nil {
			t.Fatalf("want error for body %s", body)
		} else if !strings.Contains(err.Error(), "no dataframe payload") {
			t.Fatalf("error for %s should name the missing payload: %v", body, err)
		}
	}
}

func TestClassifyGraphQLEnvelopeRejectsUnusableBody(t *testing.T) {
	for _, body := range []string{
		`<html>502 Bad Gateway</html>`,
		`[]`,
		`{"errors":"boom"}`,
	} {
		if err := classifyGraphQLEnvelope([]byte(body)); err == nil {
			t.Fatalf("want error for body %s", body)
		} else if !strings.Contains(err.Error(), "invalid GraphQL response") {
			t.Fatalf("error for %s should name the malformed envelope: %v", body, err)
		}
	}
}

func TestRunSucceedsForDataOnlyResponse(t *testing.T) {
	paths := writeRequestFiles(t)
	var stdout, stderr bytes.Buffer
	err := run(append(paths, "-print-response=true"), clientForResponses(http.StatusOK, `{"data":{"runFhirDataframe":{"rowCount":2,"rows":[{"id":"1"},{"id":"2"}]}}}`), &stdout, &stderr)
	if err != nil {
		t.Fatalf("err = %v (stderr=%s)", err, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "rows=2") || !strings.Contains(out, `"rowCount": 2`) {
		t.Fatalf("stdout missing rows or printed response:\n%s", out)
	}
}

func TestRunFailsForHTTPOkGraphQLErrorsWithoutPrintingResponse(t *testing.T) {
	paths := writeRequestFiles(t)
	var stdout, stderr bytes.Buffer
	err := run(append(paths, "-print-response=false"), clientForResponses(http.StatusOK, `{"errors":[{"message":"project not found","path":["runFhirDataframe"]}]}`), &stdout, &stderr)
	if err == nil {
		t.Fatal("want nonzero result for HTTP-200 GraphQL errors")
	}
	if !strings.Contains(err.Error(), "project not found") || !strings.Contains(err.Error(), "runFhirDataframe") {
		t.Fatalf("error missing diagnostics: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout should be empty when the request failed:\n%s", stdout.String())
	}
	// The payload is preserved for diagnosis even though printing is disabled.
	if !strings.Contains(stderr.String(), "project not found") {
		t.Fatalf("stderr missing preserved payload:\n%s", stderr.String())
	}
}

func TestRunMarksPartialSuccessIncomplete(t *testing.T) {
	paths := writeRequestFiles(t)
	var stdout, stderr bytes.Buffer
	err := run(paths, clientForResponses(http.StatusOK, `{"data":{"runFhirDataframe":{"rowCount":3,"rows":[{"id":"1"}]}},"errors":[{"message":"row 2 dropped"}]}`), &stdout, &stderr)
	if err == nil {
		t.Fatal("want nonzero result for data plus errors")
	}
	combined := err.Error() + stderr.String() + stdout.String()
	if !strings.Contains(combined, "incomplete") {
		t.Fatalf("output does not mark the result incomplete:\n%s", combined)
	}
	if strings.Contains(stdout.String(), "rows=3") {
		t.Fatalf("stdout presents partial data as a complete result:\n%s", stdout.String())
	}
}

func TestRunStopsAtFailingRequestIndex(t *testing.T) {
	paths := writeRequestFiles(t)
	requests := 0
	client := &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) {
		requests++
		body := `{"data":{"runFhirDataframe":{"rowCount":1,"rows":[{"id":"1"}]}}}`
		if requests == 3 {
			body = `{"errors":[{"message":"cursor expired"}]}`
		}
		return httpResponse(http.StatusOK, body), nil
	})}
	var stdout, stderr bytes.Buffer
	err := run(append(paths, "-repeat", "5"), func(time.Duration) *http.Client { return client }, &stdout, &stderr)
	if err == nil {
		t.Fatal("want nonzero result when one repeated request fails")
	}
	if !strings.Contains(err.Error(), "request 3/5") {
		t.Fatalf("error missing failing request index: %v", err)
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want to stop after the failure", requests)
	}
}

func TestRunKeepsNon2xxBehavior(t *testing.T) {
	paths := writeRequestFiles(t)
	var stdout, stderr bytes.Buffer
	err := run(paths, clientForResponses(http.StatusInternalServerError, `internal failure`), &stdout, &stderr)
	if err == nil {
		t.Fatal("want error for non-2xx response")
	}
	for _, want := range []string{"request 1/1", "HTTP 500 Internal Server Error", "internal failure"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestRunKeepsTimeoutBehavior(t *testing.T) {
	paths := writeRequestFiles(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		fmt.Fprint(w, `{"data":{"runFhirDataframe":{"rowCount":1,"rows":[{}]}}}`)
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	args := append(paths, "-url", server.URL, "-timeout", "20ms")
	err := run(args, newHTTPClient, &stdout, &stderr)
	if err == nil {
		t.Fatal("want timeout error")
	}
	if !strings.Contains(err.Error(), "request 1/1") || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("timeout error changed: %v", err)
	}
}

func writeRequestFiles(t *testing.T) []string {
	t.Helper()
	dir := t.TempDir()
	queryPath := filepath.Join(dir, "query.graphql")
	variablesPath := filepath.Join(dir, "variables.json")
	if err := os.WriteFile(queryPath, []byte("query { runFhirDataframe { rowCount rows } }"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(variablesPath, []byte(`{"limit":1,"input":{"project":"META"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return []string{"-query", queryPath, "-variables", variablesPath}
}

func clientForResponses(statusCode int, body string) func(time.Duration) *http.Client {
	return func(time.Duration) *http.Client {
		return &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) {
			return httpResponse(statusCode, body), nil
		})}
	}
}

func httpResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}
