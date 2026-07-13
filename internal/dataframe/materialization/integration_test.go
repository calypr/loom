package materialization

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	clickhousestore "github.com/calypr/loom/internal/store/clickhouse"
)

// This opt-in test exercises the actual materialization reader against
// ClickHouse, including keyset pagination and aggregate filters.
func TestClickHouseReaderPaginationAndFilteredAggregate(t *testing.T) {
	url := os.Getenv("LOOM_CLICKHOUSE_URL")
	if url == "" {
		t.Skip("LOOM_CLICKHOUSE_URL is not set")
	}
	database := os.Getenv("LOOM_CLICKHOUSE_DATABASE")
	if database == "" {
		database = "loom_test"
	}
	client, err := clickhousestore.New(clickhousestore.Options{URL: url, Database: database, Timeout: 10 * time.Second})
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
		{Name: "__loom_row_id", Type: "UInt64"},
		{Name: "status", Type: "String"},
		{Name: "score", Type: "Float64"},
	}
	if err := client.CreateTable(ctx, table, columns); err != nil {
		t.Fatal(err)
	}
	if err := client.InsertRows(ctx, table, columns, []map[string]any{
		{"__loom_row_id": uint64(1), "status": "active", "score": 1.0},
		{"__loom_row_id": uint64(2), "status": "inactive", "score": 2.0},
		{"__loom_row_id": uint64(3), "status": "active", "score": 3.0},
	}); err != nil {
		t.Fatal(err)
	}
	registry := NewMemoryRegistry()
	if err := registry.Save(ctx, Materialization{ID: "it", State: StateReady, PhysicalTable: table, Columns: []Column{
		{Name: "__loom_row_id", ClickHouse: "UInt64"},
		{Name: "status", ClickHouse: "String"},
		{Name: "score", ClickHouse: "Float64"},
	}}); err != nil {
		t.Fatal(err)
	}
	reader := &Reader{ClickHouse: client, Registry: registry, MaxPage: 10}
	first, err := reader.Page(ctx, PageRequest{MaterializationID: "it", Columns: []string{"status", "score"}, Sort: &Sort{Column: "score"}, First: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Rows) != 1 || !first.HasNext || first.NextCursor == "" {
		t.Fatalf("first page = %#v", first)
	}
	second, err := reader.Page(ctx, PageRequest{MaterializationID: "it", Columns: []string{"status", "score"}, Sort: &Sort{Column: "score"}, First: 2, After: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Rows) != 2 || second.Rows[0]["score"] != float64(2) {
		t.Fatalf("second page = %#v", second)
	}
	aggregate, err := reader.Aggregate(ctx, AggregateRequest{MaterializationID: "it", GroupBy: []string{"status"}, Filters: []Filter{{Column: "status", Op: "EQ", Value: "active"}}, Operation: "COUNT"})
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregate.Rows) != 1 || aggregate.Rows[0]["count"] != float64(2) {
		t.Fatalf("filtered aggregate = %#v", aggregate.Rows)
	}
}
