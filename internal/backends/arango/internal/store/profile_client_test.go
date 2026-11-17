package arango

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestClientProfilePostsParameterizedCursorRequest(t *testing.T) {
	client := &Client{
		rawURL: "http://arango.test",
		dbName: "fhir_proto",
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodPost || r.URL.Path != "/_db/fhir_proto/_api/cursor" {
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			text := string(body)
			for _, fragment := range []string{`"query":"FOR p IN Patient RETURN p"`, `"bindVars":{"project":"demo"}`, `"profile":2`} {
				if !strings.Contains(text, fragment) {
					t.Fatalf("request body %s does not contain %s", text, fragment)
				}
			}
			return &http.Response{
				StatusCode: http.StatusCreated,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewBufferString(`{"result":[{"_key":"p1"}],"hasMore":false,"extra":{"stats":{"nodes":[{"id":1,"calls":1,"items":1,"runtime":0.01}]},"plan":{"nodes":[{"id":1,"type":"ReturnNode"}]}}}`)),
				Request:    r,
			}, nil
		})},
	}
	result, err := client.Profile(context.Background(), ProfileRequest{Query: "FOR p IN Patient RETURN p", BindVars: map[string]any{"project": "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Result) != 1 || len(result.Extra.Stats.Nodes) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestClientProfileRejectsHTTPError(t *testing.T) {
	client := &Client{
		rawURL: "http://arango.test",
		dbName: "fhir_proto",
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("bad query")), Request: r}, nil
		})},
	}
	_, err := client.Profile(context.Background(), ProfileRequest{Query: "FOR p IN Patient RETURN p"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("expected HTTP error, got %v", err)
	}
}
