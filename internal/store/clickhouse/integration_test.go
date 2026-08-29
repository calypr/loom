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
	columns := []Column{
		{Name: "__loom_row_id", Type: "UInt64"},
		{Name: "name", Type: "Nullable(String)"},
		{Name: "score", Type: "Nullable(Float64)"},
		{Name: "tags", Type: "Array(String)"},
	}
	rows, err := client.QueryRowsArgs(ctx, "SELECT `name`, `score`, `tags` FROM `"+table+"`", []string{"name", "score", "tags"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["name"] != "alice" {
		t.Fatalf("round-trip rows = %#v", rows)
	}
	if err := client.VerifyOutput(ctx, table, columns, 1); err != nil {
		t.Fatalf("verify before pruning: %v", err)
	}
	if err := client.DropColumns(ctx, table, []string{"score"}); err != nil {
		t.Fatalf("drop discovered column: %v", err)
	}
	if err := client.VerifyOutput(ctx, table, []Column{
		{Name: "__loom_row_id", Type: "UInt64"},
		{Name: "name", Type: "Nullable(String)"},
		{Name: "tags", Type: "Array(String)"},
	}, 1); err != nil {
		t.Fatalf("verify after pruning: %v", err)
	}
}

func TestClickHouseNativeJSONRoundTrip(t *testing.T) {
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
	table := "loom_json_it_" + uuid.NewString()[:8]
	defer client.DropTable(ctx, table)
	columns := []Column{
		{Name: "__loom_row_id", Type: "String"},
		{Name: "method", Type: "Nullable(JSON)"},
		{Name: "methods", Type: "Array(JSON)"},
	}
	if err := client.CreateTable(ctx, table, columns); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{{
		"__loom_row_id": "1",
		"method": map[string]any{
			"coding": []map[string]any{{"code": "M1", "display": "method one"}},
			"text":   "method one",
		},
		"methods": []map[string]any{
			{"coding": map[string]any{"code": "M1"}},
			{"coding": map[string]any{"code": "M2"}},
		},
	}}
	if err := client.InsertRows(ctx, table, columns, rows); err != nil {
		t.Fatal(err)
	}
	result, err := client.QueryRowsArgs(ctx, "SELECT `method`, `methods` FROM `"+table+"`", []string{"method", "methods"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("JSON rows = %#v", result)
	}
	method, ok := result[0]["method"].(map[string]any)
	if !ok || method["text"] != "method one" {
		t.Fatalf("JSON object = %#v", result[0]["method"])
	}
	methods, ok := result[0]["methods"].([]any)
	if !ok || len(methods) != 2 {
		t.Fatalf("JSON array = %#v", result[0]["methods"])
	}
	if err := client.VerifyOutput(ctx, table, columns, 1); err != nil {
		t.Fatalf("verify JSON output: %v", err)
	}
}
