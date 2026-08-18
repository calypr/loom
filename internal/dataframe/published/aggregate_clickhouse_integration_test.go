package published

import (
	"context"
	"os"
	"testing"
	"time"

	storeclickhouse "github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
)

func TestAggregateBatchClickHouseIntegration(t *testing.T) {
	url := os.Getenv("LOOM_CLICKHOUSE_URL")
	if url == "" {
		t.Skip("LOOM_CLICKHOUSE_URL is not set")
	}
	database := os.Getenv("LOOM_CLICKHOUSE_DATABASE")
	if database == "" {
		database = "loom_test"
	}
	client, err := storeclickhouse.New(storeclickhouse.Options{
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
	tableA := "loom_aggregate_a_" + uuid.NewString()[:8]
	tableB := "loom_aggregate_b_" + uuid.NewString()[:8]
	defer client.DropTable(ctx, tableA)
	defer client.DropTable(ctx, tableB)
	physicalColumns := []storeclickhouse.Column{
		{Name: "__loom_row_id", Type: "UInt64"},
		{Name: "facet", Type: "Nullable(String)"},
		{Name: "facet_other", Type: "Nullable(String)"},
		{Name: "value", Type: "Nullable(Float64)"},
		{Name: "auth_resource_path", Type: "String"},
	}
	for _, table := range []string{tableA, tableB} {
		if err := client.CreateTable(ctx, table, physicalColumns); err != nil {
			t.Fatal(err)
		}
	}
	if err := client.InsertRows(ctx, tableA, physicalColumns, []map[string]any{
		{"__loom_row_id": uint64(1), "facet": "a", "facet_other": "x", "value": 2.0, "auth_resource_path": "allowed"},
		{"__loom_row_id": uint64(2), "facet": nil, "facet_other": "x", "value": 3.0, "auth_resource_path": "allowed"},
		{"__loom_row_id": uint64(3), "facet": "secret", "facet_other": "secret", "value": 100.0, "auth_resource_path": "denied"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := client.InsertRows(ctx, tableB, physicalColumns, []map[string]any{
		{"__loom_row_id": uint64(1), "facet": "b", "facet_other": "y", "value": 5.0, "auth_resource_path": "anything"},
	}); err != nil {
		t.Fatal(err)
	}
	sourceColumns := []Column{
		{Name: "__loom_row_id", ClickHouse: "UInt64"},
		{Name: "facet", ClickHouse: "Nullable(String)"},
		{Name: "facet_other", ClickHouse: "Nullable(String)"},
		{Name: "value", ClickHouse: "Nullable(Float64)"},
		{Name: authResourcePathColumn, ClickHouse: "String"},
	}
	dataset := FederatedDataset{
		Columns: []Column{
			{Name: "facet", ClickHouse: "Nullable(String)"},
			{Name: "facet_other", ClickHouse: "Nullable(String)"},
			{Name: "value", ClickHouse: "Nullable(Float64)"},
			{Name: projectIDColumn, ClickHouse: "String"},
		},
		Sources: []Materialization{
			{ID: "a:Patient", Project: "a", PhysicalTable: tableA, Columns: sourceColumns},
			{ID: "b:Patient", Project: "b", PhysicalTable: tableB, Columns: sourceColumns},
		},
	}
	result, err := (&Reader{ClickHouse: client}).ExecuteAggregateBatch(ctx, dataset, AggregateBatchRequest{
		Jobs: []AggregateJob{
			{ID: 1, ResponseMode: AggregateResponseLegacy, GroupBy: []string{"facet"}, Operation: "COUNT"},
			{ID: 2, ResponseMode: AggregateResponseLegacy, Operation: "COUNT"},
			{ID: 3, ResponseMode: AggregateResponseLegacy, Operation: "SUM", Column: "value"},
			{ID: 4, ResponseMode: AggregateResponseTerms, Column: "facet", Size: 2},
			{ID: 5, ResponseMode: AggregateResponseTerms, Column: "facet_other", Size: 1},
		},
		AccessByProject: map[string]SourceAccess{
			"a": {ResourcePaths: []string{"allowed"}},
			"b": {Unrestricted: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range result.Jobs {
		if job.Err != nil {
			t.Fatalf("job %d: %v", job.ID, job.Err)
		}
	}
	if len(result.Jobs[0].Rows) != 3 {
		t.Fatalf("grouped rows = %#v", result.Jobs[0].Rows)
	}
	if got, err := numericCount(result.Jobs[1].Rows[0]["count"]); err != nil || got != 3 {
		t.Fatalf("ungrouped count = %v, err = %v", result.Jobs[1].Rows, err)
	}
	if got := result.Jobs[3]; len(got.Rows) != 2 || got.Truncated || got.MissingCount != 1 {
		t.Fatalf("facet terms = %#v", got)
	}
	if got := result.Jobs[4]; len(got.Rows) != 1 || !got.Truncated || got.MissingCount != 0 || got.Rows[0]["key"] != "x" {
		t.Fatalf("facet_other terms = %#v", got)
	}
}
