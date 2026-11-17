package arango

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestClientExplainPostsTypedRequest(t *testing.T) {
	client := &Client{
		rawURL: "http://arango.test",
		dbName: "fhir_proto",
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s", r.Method)
			}
			if r.URL.Path != "/_db/fhir_proto/_api/explain" {
				t.Fatalf("path = %s", r.URL.Path)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), `"query":"FOR p IN Patient RETURN p"`) {
				t.Fatalf("unexpected request body: %s", body)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewBufferString(`{"plan":{"nodes":[{"id":1,"type":"SingletonNode"}]}}`)),
				Request:    r,
			}, nil
		})},
	}
	result, err := client.Explain(context.Background(), ExplainRequest{Query: "FOR p IN Patient RETURN p"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Plan == nil || len(result.Plan.Nodes) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestClientExplainRejectsHTTPError(t *testing.T) {
	client := &Client{
		rawURL: "http://arango.test",
		dbName: "fhir_proto",
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("bad query")),
				Request:    r,
			}, nil
		})},
	}
	_, err := client.Explain(context.Background(), ExplainRequest{Query: "FOR p IN Patient RETURN p"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("expected HTTP error, got %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
