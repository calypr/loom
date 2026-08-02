package arango

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	store "github.com/calypr/loom/internal/store/arango"
)

type fakeClient struct{ queries []string }

func (f *fakeClient) QueryRows(_ context.Context, query string, _ int, _ map[string]any, visit store.RowVisitor) error {
	f.queries = append(f.queries, query)
	if query == populatedFieldsAQL {
		return visit(map[string]any{"project": "p", "resource_type": "Patient", "path": "id", "doc_count": int64(1), "sample_count": int64(1)})
	}
	return nil
}
func (*fakeClient) InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error {
	return nil
}
func (*fakeClient) ExecuteAQL(context.Context, string, map[string]any) error { return nil }
func (*fakeClient) Bootstrap(context.Context, store.BootstrapSpec) error     { return nil }

func TestStoreUsesInjectedClient(t *testing.T) {
	client := &fakeClient{}
	adapter, err := New(client, "db")
	if err != nil {
		t.Fatal(err)
	}
	fields, err := adapter.DiscoverFields(context.Background(), catalog.PopulatedFieldOptions{Project: "p"})
	if err != nil || len(fields) != 1 || fields[0].Path != "id" {
		t.Fatalf("fields=%#v err=%v", fields, err)
	}
	if len(client.queries) != 1 {
		t.Fatalf("queries=%d", len(client.queries))
	}
}
