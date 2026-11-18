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

func TestExecutePostsJSON(t *testing.T) {
	client := &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/api/v1/projects/P1/dataframes/run" {
			t.Fatalf("request path = %s", request.URL.Path)
		}
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
		if !strings.Contains(string(body), `"project":"P1"`) {
			t.Fatalf("unexpected request body: %s", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"rows":[],"rowCount":0}`)),
			Header:     make(http.Header),
		}, nil
	})}

	body, err := execute(client, "http://example.test/api/v1/projects/P1/dataframes/run", []byte(`{"input":{"project":"P1"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"rows":[],"rowCount":0}` {
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
	tests := []struct {
		name      string
		variables string
		limit     int
		want      string
	}{
		{
			name:      "replaces top level limit",
			variables: `{"limit":25,"input":{"project":"P1"}}`,
			limit:     1000,
			want:      `{"input":{"project":"P1"},"limit":1000}`,
		},
		{
			name:      "adds missing limit",
			variables: `{"input":{"project":"P1"}}`,
			limit:     7,
			want:      `{"input":{"project":"P1"},"limit":7}`,
		},
		{

			name:      "preserves nested numbers exactly",
			variables: `{"limit":25,"input":{"filters":[{"value":9007199254740993}],"threshold":1.5e3,"score":0.30000000000000004,"ratio":-0.5E-2}}`,
			limit:     1000,
			want:      `{"input":{"filters":[{"value":9007199254740993}],"threshold":1.5e3,"score":0.30000000000000004,"ratio":-0.5E-2},"limit":1000}`,
		},
		{
			name:      "zero limit preserves variables",
			variables: `{"limit":25,"input":{"filters":[{"value":9007199254740993}],"threshold":1.5e3}}`,
			limit:     0,
			want:      `{"limit":25,"input":{"filters":[{"value":9007199254740993}],"threshold":1.5e3}}`,
		},
		{
			name:      "negative limit preserves variables",
			variables: `{"limit":25,"nested":{"value":9007199254740993}}`,
			limit:     -1,
			want:      `{"limit":25,"nested":{"value":9007199254740993}}`,
		},
		{
			name:      "empty object gains limit",
			variables: `{}`,
			limit:     3,
			want:      `{"limit":3}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			variables, err := withLimit([]byte(test.variables), test.limit)
			if err != nil {
				t.Fatal(err)
			}
			if string(variables) != test.want {
				t.Fatalf("variables = %s, want %s", variables, test.want)
			}
		})
	}
}

func TestWithLimitRejectsMalformedInput(t *testing.T) {
	for _, variables := range []string{
		`{"limit":25,`,
		`{"limit":25,}`,
		`{"limit":25} trailing`,
		`not json`,
		``,
		`null`,
		`[{"limit":25}]`,
		`"25"`,
		`25`,
	} {
		t.Run(variables, func(t *testing.T) {
			if got, err := withLimit([]byte(variables), 1000); err == nil {
				t.Fatalf("expected error, got %s", got)
			}
		})
	}
}

func TestExecuteSendsExactOverriddenLimitBody(t *testing.T) {
	const wantBody = `{"input":{"filters":[{"value":9007199254740993}],"threshold":1.5e3},"limit":1000}`

	var sent string
	client := &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		sent = string(body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"rows":[],"rowCount":0}`)),
			Header:     make(http.Header),
		}, nil
	})}

	variables, err := withLimit([]byte(`{"limit":25,"input":{"filters":[{"value":9007199254740993}],"threshold":1.5e3}}`), 1000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := execute(client, "http://example.test/api/v1/projects/P1/dataframes/run", variables); err != nil {
		t.Fatal(err)
	}
	if sent != wantBody {
		t.Fatalf("request body = %s, want %s", sent, wantBody)
	}
}

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

func TestRunSucceedsForDataframeResponse(t *testing.T) {
	paths := writeRequestFiles(t)
	var stdout, stderr bytes.Buffer
	err := run(append(paths, "-print-response=true"), clientForResponses(http.StatusOK, `{"rowCount":2,"rows":[{"id":"1"},{"id":"2"}]}`), &stdout, &stderr)
	if err != nil {
		t.Fatalf("err = %v (stderr=%s)", err, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "rows=2") || !strings.Contains(out, `"rowCount": 2`) {
		t.Fatalf("stdout missing rows or printed response:\n%s", out)
	}
}

func TestRunRejectsNonObjectPayloadWithoutPrintingSuccess(t *testing.T) {
	for _, payload := range []string{"false", "42", "[]", `"not a dataframe"`, `{"error":{"message":"cursor expired"}}`} {
		t.Run(payload, func(t *testing.T) {
			paths := writeRequestFiles(t)
			var stdout, stderr bytes.Buffer
			body := payload
			err := run(paths, clientForResponses(http.StatusOK, body), &stdout, &stderr)
			if err == nil {
				t.Fatal("expected an unusable dataframe response to fail")
			}
			if stdout.Len() != 0 {
				t.Fatalf("unusable payload printed successful metrics: %s", stdout.String())
			}
		})
	}
}

func TestRunStopsAtFailingRequestIndex(t *testing.T) {
	paths := writeRequestFiles(t)
	requests := 0
	client := &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) {
		requests++
		body := `{"rowCount":1,"rows":[{"id":"1"}]}`
		if requests == 3 {
			body = `{"error":{"code":"STALE_CURSOR","message":"cursor expired"}}`
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
		fmt.Fprint(w, `{"rowCount":1,"rows":[{}]}`)
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
	requestPath := filepath.Join(dir, "request.json")
	if err := os.WriteFile(requestPath, []byte(`{"limit":1,"input":{"project":"META"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return []string{"-request", requestPath}
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
