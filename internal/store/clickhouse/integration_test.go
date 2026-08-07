package clickhouse

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Run with LOOM_CLICKHOUSE_URL and optional LOOM_CLICKHOUSE_USERNAME/
// LOOM_CLICKHOUSE_PASSWORD to exercise the native driver against a real
// ClickHouse instance. The default unit suite remains hermetic when ClickHouse
// is not running locally.
func TestClickHouseNativeRoundTrip(t *testing.T) {
	url := os.Getenv("LOOM_CLICKHOUSE_URL")
	if url == "" {
		t.Skip("LOOM_CLICKHOUSE_URL is not set")
	}
	database := os.Getenv("LOOM_CLICKHOUSE_DATABASE")
	if database == "" {
		database = "loom_test"
	}
	client, err := New(Options{
		URL: url, Database: database,
		Username: os.Getenv("LOOM_CLICKHOUSE_USERNAME"),
		Password: os.Getenv("LOOM_CLICKHOUSE_PASSWORD"),
		Timeout:  10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := context.Background()
	if err := client.EnsureDatabase(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("ping ClickHouse: %v", err)
	}
	table := "loom_it_" + uuid.NewString()[:8]
	defer client.DropTable(ctx, table)
	if err := client.CreateTable(ctx, table, []Column{
		{Name: "__loom_row_id", Type: "UInt64"},
		{Name: "name", Type: "Nullable(String)"},
		{Name: "score", Type: "Nullable(Float64)"},
		{Name: "tags", Type: "Array(String)"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := client.InsertRows(ctx, table, []Column{
		{Name: "__loom_row_id", Type: "UInt64"},
		{Name: "name", Type: "Nullable(String)"},
		{Name: "score", Type: "Nullable(Float64)"},
		{Name: "tags", Type: "Array(String)"},
	}, []map[string]any{{"__loom_row_id": uint64(1), "name": "alice", "score": 2.5, "tags": []string{"a", "b"}}}); err != nil {
		t.Fatal(err)
	}
	rows, err := client.QueryRowsArgs(ctx, "SELECT `name`, `score`, `tags` FROM `"+table+"`", []string{"name", "score", "tags"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["name"] != "alice" {
		t.Fatalf("round-trip rows = %#v", rows)
	}
}
