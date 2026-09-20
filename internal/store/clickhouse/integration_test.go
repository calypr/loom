package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestClickHouseNativeOWNERRecordsJSONRoundTrip(t *testing.T) {
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
	table := "loom_owner_records_it_" + uuid.NewString()[:8]
	defer client.DropTable(ctx, table)
	columns := []Column{
		{Name: "__loom_row_id", Type: "String"},
		{Name: "OWNER_RECORDS", Type: "Array(JSON)"},
	}
	if err := client.CreateTable(ctx, table, columns); err != nil {
		t.Fatal(err)
	}

	ownerRecordJSON := []string{
		`[{"source":{"resourceType":"Observation","resourceId":"obs-1","ownerPath":"component","ownerOrdinal":0},"codings":[{"system":"urn:test","code":"quantity"}],"choiceArm":"valueQuantity","logicalType":"decimal","value":111,"values":[111],"unit":null,"status":"VALUE","owner":{"id":"obs-1","detail":{"location":"arm"}}}]`,
		`[{"source":{"resourceType":"Observation","resourceId":"obs-2","ownerPath":"component","ownerOrdinal":1},"codings":[],"choiceArm":"valueQuantity","logicalType":"decimal","value":null,"values":[],"unit":null,"status":"ABSENT","owner":{"id":"obs-2"}}]`,
		`[{"source":{"resourceType":"Observation","resourceId":"obs-3","ownerPath":"component","ownerOrdinal":2},"codings":[],"choiceArm":"valueString","logicalType":"string","value":"text","values":["text"],"unit":"mg","status":"VALUE","owner":{"id":"obs-3"}}]`,
		`[{"source":{"resourceType":"Observation","resourceId":"obs-4","ownerPath":"component","ownerOrdinal":3},"codings":[],"choiceArm":"valueQuantity","logicalType":"decimal","value":null,"values":[null],"unit":null,"status":"ABSENT","owner":{"id":"obs-4"}}]`,
		`[{"source":{"resourceType":"Observation","resourceId":"obs-5","ownerPath":"component","ownerOrdinal":4},"codings":[],"choiceArm":"valueQuantity","logicalType":"decimal","status":"ABSENT","owner":{"id":"obs-5"}}]`,
		`[{"source":{"resourceType":"Observation","resourceId":"obs-6","ownerPath":"component","ownerOrdinal":5},"codings":[],"choiceArm":"valueString","logicalType":"string","value":true,"values":[true,"mixed",null,[2,null],{"nested":{"score":0}}],"unit":"mg","status":"VALUE","owner":{"id":"obs-6"}}]`,
	}
	rows := make([]map[string]any, len(ownerRecordJSON))
	want := make([][]map[string]any, len(ownerRecordJSON))
	for index, encoded := range ownerRecordJSON {
		var input []map[string]any
		if err := json.Unmarshal([]byte(encoded), &input); err != nil {
			t.Fatalf("decode OWNER_RECORDS fixture %d: %v", index, err)
		}
		// ClickHouse JSON omits null-valued object paths. Null array elements
		// remain meaningful and are retained by this oracle.
		want[index] = make([]map[string]any, len(input))
		for recordIndex, record := range input {
			want[index][recordIndex] = withoutNativeJSONNullObjectFields(record).(map[string]any)
		}
		rows[index] = map[string]any{
			"__loom_row_id": fmt.Sprintf("%d", index+1),
			"OWNER_RECORDS": input,
		}
	}
	if err := client.InsertRows(ctx, table, columns, rows); err != nil {
		t.Fatal(err)
	}
	got, err := client.QueryRowsArgs(ctx, "SELECT `OWNER_RECORDS` FROM `"+table+"` ORDER BY `__loom_row_id`", []string{"OWNER_RECORDS"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("OWNER_RECORDS row count = %d, want %d", len(got), len(want))
	}
	for index, row := range got {
		encoded, err := json.Marshal(row["OWNER_RECORDS"])
		if err != nil {
			t.Fatalf("encode OWNER_RECORDS result %d: %v", index, err)
		}
		wantEncoded, err := json.Marshal(want[index])
		if err != nil {
			t.Fatalf("encode OWNER_RECORDS expectation %d: %v", index, err)
		}
		if string(encoded) != string(wantEncoded) {
			t.Errorf("OWNER_RECORDS row %d = %s, want %s", index, encoded, wantEncoded)
		}
	}
}

func withoutNativeJSONNullObjectFields(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, field := range typed {
			if field == nil {
				continue
			}
			result[key] = withoutNativeJSONNullObjectFields(field)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = withoutNativeJSONNullObjectFields(item)
		}
		return result
	default:
		return value
	}
}
