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
			// Nested objects are byte-identical passthroughs: values, literals
			// and key order all survive the top-level limit override.
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
	const wantBody = `{"query":"query { ping }","variables":{"input":{"filters":[{"value":9007199254740993}],"threshold":1.5e3},"limit":1000}}`

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
			Body:       io.NopCloser(strings.NewReader(`{"data":{"ping":"pong"}}`)),
			Header:     make(http.Header),
		}, nil
	})}

	variables, err := withLimit([]byte(`{"limit":25,"input":{"filters":[{"value":9007199254740993}],"threshold":1.5e3}}`), 1000)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := encodeGraphQLRequest([]byte("query { ping }"), variables)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := execute(client, "http://example.test/graphql", payload); err != nil {
		t.Fatal(err)
	}
	if sent != wantBody {
		t.Fatalf("request body = %s, want %s", sent, wantBody)
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
