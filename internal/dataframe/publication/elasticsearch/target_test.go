package elasticsearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/calypr/loom/internal/dataframe/publication"
	es "github.com/calypr/loom/internal/store/elasticsearch"
)

func TestTargetPublishesWithAtomicAliasSwap(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	var bulkBody string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/_bulk" {
			bulkBody = string(body)
		}
		mu.Unlock()
		status := http.StatusOK
		payload := `{"acknowledged":true}`
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/_alias/"):
			status = http.StatusNotFound
			payload = `{"error":"missing"}`
		case r.URL.Path == "/_bulk":
			payload = `{"errors":false,"items":[{"index":{"_id":"a","status":201}},{"index":{"_id":"b","status":201}}]}`
		case strings.HasSuffix(r.URL.Path, "/_count"):
			payload = `{"count":2}`
		}
		return jsonResponse(status, payload), nil
	})

	client, err := es.New(es.Options{URL: "http://elasticsearch.test", HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	target, err := New(Options{Client: client, IndexPrefix: "loom"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := publication.Publish(context.Background(), target, publication.PublicationIdentity{Name: "gdc", Project: "demo", AuthResourcePaths: []string{"/controlled"}}, []publication.OutputStream{{
		Name:    "patients",
		Columns: []publication.LogicalColumn{{Name: "__loom_row_id", Kind: "string", IsIdentity: true}, {Name: "id", Kind: "string"}},
		Stream: func(_ context.Context, visit func(map[string]any) error) error {
			for _, row := range []map[string]any{{"__loom_row_id": "a", "id": "p1"}, {"__loom_row_id": "b", "id": "p2"}} {
				if err := visit(row); err != nil {
					return err
				}
			}
			return nil
		},
	}}, publication.Limits{BatchRows: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Outputs) != 1 || result.Outputs[0].RowCount != 2 {
		t.Fatalf("unexpected publication result: %#v", result)
	}
	if !strings.Contains(bulkBody, `"project_id":"demo"`) || !strings.Contains(bulkBody, `"auth_resource_path":"/controlled"`) {
		t.Fatalf("trusted publication envelope missing from bulk body: %s", bulkBody)
	}
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(paths, "\n")
	if !strings.Contains(joined, "PUT /") || !strings.Contains(joined, "POST /_bulk") || !strings.Contains(joined, "POST /_aliases") {
		t.Fatalf("expected staging, bulk, and atomic alias requests: %s", joined)
	}
}

func TestTargetRollsBackStagedIndexOnStreamFailure(t *testing.T) {
	var deleted bool
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodDelete {
			deleted = true
		}
		return jsonResponse(http.StatusOK, `{"acknowledged":true}`), nil
	})
	client, _ := es.New(es.Options{URL: "http://elasticsearch.test", HTTPClient: &http.Client{Transport: transport}})
	target, _ := New(Options{Client: client})
	_, err := publication.Publish(context.Background(), target, publication.PublicationIdentity{Name: "bad", Project: "demo"}, []publication.OutputStream{{
		Name: "patients", Columns: []publication.LogicalColumn{{Name: "id", Kind: "string"}}, Stream: func(_ context.Context, visit func(map[string]any) error) error {
			return visit(map[string]any{"unexpected": "value"})
		},
	}}, publication.Limits{})
	if err == nil || !deleted {
		t.Fatalf("expected stream failure and staged-index cleanup: err=%v deleted=%v", err, deleted)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestMappingRejectsObjects(t *testing.T) {
	if _, err := mapping([]publication.LogicalColumn{{Name: "payload", Kind: "object"}}); err == nil {
		t.Fatal("expected flat mapping to reject object values")
	}
	properties, err := mapping([]publication.LogicalColumn{{Name: "ids", Kind: "uuid", Repeated: true}, {Name: "count", Kind: "integer"}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(properties)
	if !strings.Contains(string(encoded), `"ids":{"type":"keyword"}`) || !strings.Contains(string(encoded), `"count":{"type":"long"}`) {
		t.Fatalf("unexpected mapping: %s", encoded)
	}
}
