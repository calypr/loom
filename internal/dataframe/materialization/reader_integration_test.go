package materialization

import (
	"context"
	"os"
	"testing"
	"time"

	clickhousestore "github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
)

// TestReaderAgainstRealClickHouse exercises publication resolution, native
// decoding, federated rows, streaming, and the aggregate contract. Set
// LOOM_CLICKHOUSE_URL to run it locally or in CI.
func TestReaderAgainstRealClickHouse(t *testing.T) {
	url := os.Getenv("LOOM_CLICKHOUSE_URL")
	if url == "" {
		t.Skip("LOOM_CLICKHOUSE_URL is not set")
	}
	database := os.Getenv("LOOM_CLICKHOUSE_DATABASE")
	if database == "" {
		database = "loom_test"
	}
	client, err := clickhousestore.New(clickhousestore.Options{URL: url, Database: database, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := context.Background()
	if err := client.EnsureDatabase(ctx); err != nil {
		t.Fatal(err)
	}
	table := "loom_reader_it_" + uuid.NewString()[:12]
	defer client.DropTable(ctx, table)
	columns := []clickhousestore.Column{
		{Name: "__loom_row_id", Type: "String"},
		{Name: "auth_resource_path", Type: "Nullable(String)"},
		{Name: "category", Type: "Nullable(String)"},
		{Name: "amount", Type: "Nullable(Float64)"},
		{Name: "observed", Type: "DateTime"},
	}
	if err := client.CreateTable(ctx, table, columns); err != nil {
		t.Fatal(err)
	}
	if err := client.InsertRows(ctx, table, columns, []map[string]any{
		{"__loom_row_id": "1", "auth_resource_path": "/public", "category": "alpha", "amount": 4.0, "observed": time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"__loom_row_id": "2", "auth_resource_path": "/public", "category": "alpha", "amount": 16.0, "observed": time.Date(2026, 1, 1, 0, 0, 30, 0, time.UTC)},
		{"__loom_row_id": "3", "auth_resource_path": "/private", "category": nil, "amount": nil, "observed": time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
	}); err != nil {
		t.Fatal(err)
	}

	catalog := newBundleCatalogFixture()
	execution := BundleExecution{
		ID: "reader-it-execution", BundleIdentity: BundleIdentity{Name: "files", Project: "project-a", DatasetGeneration: "generation-1"},
		State: BundleReady, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		Outputs: []BundleOutputRecord{{Name: "files", Alias: "files", PhysicalTable: table, Columns: []Column{
			{Name: "__loom_row_id", ClickHouse: "String"}, {Name: "auth_resource_path", ClickHouse: "Nullable(String)"}, {Name: "category", ClickHouse: "Nullable(String)"}, {Name: "amount", ClickHouse: "Nullable(Float64)"}, {Name: "observed", ClickHouse: "DateTime"},
		}, State: BundleReady, RowCount: 3}},
	}
	catalog.executions[execution.ID] = execution
	catalog.pointers[execution.PointerName()] = BundlePointer{Name: execution.PointerName(), ExecutionID: execution.ID}
	reader := &Reader{ClickHouse: client, Catalog: catalog, MaxPage: 100}

	dataset, err := reader.ResolveFederatedDataset(ctx, []string{"project-a"}, "files")
	if err != nil {
		t.Fatal(err)
	}
	if dataset.RowCount != 3 || len(dataset.Columns) != 4 {
		t.Fatalf("dataset = %#v", dataset)
	}
	page, err := reader.PageFederated(ctx, []string{"project-a"}, "files", FederatedPageRequest{Columns: []string{"category", "amount"}, Sort: &Sort{Column: "category"}, First: 2, AuthUnrestricted: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 2 || !page.HasNext {
		t.Fatalf("page = %#v", page)
	}
	aggregations, err := reader.AggregateFederatedBatch(ctx, []string{"project-a"}, "files", AggregationsRequest{AuthUnrestricted: true, Specs: []AggregationSpec{
		{Name: "categories", Kind: "TERMS", Column: "category", Size: 10},
		{Name: "amounts", Kind: "STATS", Column: "amount"},
		{Name: "missing-category", Kind: "MISSING", Column: "category"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregations.Aggregations) != 3 || aggregations.Aggregations[0].Name != "categories" {
		t.Fatalf("aggregations = %#v", aggregations.Aggregations)
	}
	streamed := 0
	_, err = reader.StreamFederated(ctx, []string{"project-a"}, "files", FederatedStreamRequest{Columns: []string{"category"}, AuthUnrestricted: true}, func(row map[string]any) error {
		streamed++
		if _, ok := row["category"]; !ok {
			t.Errorf("stream row missing category: %#v", row)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if streamed != 3 {
		t.Fatalf("streamed rows = %d, want 3", streamed)
	}
}
