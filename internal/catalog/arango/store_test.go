package arango

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	store "github.com/calypr/loom/internal/store/arango"
)

type fakeClient struct {
	queries []string
	row     map[string]any
}

func (f *fakeClient) QueryRows(_ context.Context, query string, _ int, _ map[string]any, visit store.RowVisitor) error {
	f.queries = append(f.queries, query)
	if query == populatedFieldsAQL {
		row := f.row
		if row == nil {
			row = map[string]any{"project": "p", "resource_type": "Patient", "path": "id", "doc_count": int64(1), "sample_count": int64(1)}
		}
		return visit(row)
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
	adapter, err := New(client)
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

func TestStorePreservesFieldMetadataAndRejectsMalformedRows(t *testing.T) {
	client := &fakeClient{row: map[string]any{
		"project": "p", "resource_type": "Patient", "path": "code", "doc_count": int64(2), "sample_count": int64(1),
		"distinct_values": []any{"A"}, "distinct_truncated": true, "pivot_candidate": true,
		"pivot_kind": "code", "pivot_columns": []any{"A"}, "pivot_family": "family",
		"pivot_column_selector": "coding.display", "pivot_value_selector": "coding.code",
		"pivot_item_source": "item", "pivot_item_resource_type": "Observation", "pivot_value_selectors": []any{"code"},
	}}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := adapter.DiscoverFields(context.Background(), catalog.PopulatedFieldOptions{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	field := fields[0]
	if !field.PivotCandidate || field.PivotKind != "code" || len(field.DistinctValues) != 1 || field.PivotItemResourceType != "Observation" {
		t.Fatalf("field metadata = %#v", field)
	}

	client.row["doc_count"] = "bad"
	if _, err := adapter.DiscoverFields(context.Background(), catalog.PopulatedFieldOptions{Project: "p"}); err == nil {
		t.Fatal("malformed numeric field row was accepted")
	}
}
