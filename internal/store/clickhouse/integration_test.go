package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	renderclickhouse "github.com/calypr/loom/internal/dataframe/compiler/render/clickhouse"
	publishedreader "github.com/calypr/loom/internal/dataframe/published"
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
		{Name: "OWNER_RECORDS", Type: "Array(Nullable(String))", LogicalType: "object"},
	}
	if err := client.CreateTable(ctx, table, columns); err != nil {
		t.Fatal(err)
	}

	ownerRecordJSON := []string{
		`[{"source":{"resourceType":"Observation","resourceId":"obs-1","ownerPath":"component","ownerOrdinal":0},"codings":[{"system":"urn:test","code":"quantity"}],"choiceArm":"valueQuantity","logicalType":"decimal","value":111,"values":[111],"unit":null,"status":"VALUE","owner":{"id":"obs-1","detail":{"location":"arm","exact":9007199254740993}}}]`,
		`[{"source":{"resourceType":"Observation","resourceId":"obs-2","ownerPath":"component","ownerOrdinal":1},"codings":[],"choiceArm":"valueQuantity","logicalType":"decimal","value":null,"values":[],"unit":null,"status":"ABSENT","owner":{"id":"obs-2"}}]`,
		`[{"source":{"resourceType":"Observation","resourceId":"obs-3","ownerPath":"component","ownerOrdinal":2},"codings":[],"choiceArm":"valueString","logicalType":"string","value":"text","values":["text"],"unit":"mg","status":"VALUE","owner":{"id":"obs-3"}}]`,
		`[{"source":{"resourceType":"Observation","resourceId":"obs-4","ownerPath":"component","ownerOrdinal":3},"codings":[],"choiceArm":"valueQuantity","logicalType":"decimal","value":null,"values":[null],"unit":null,"status":"ABSENT","owner":{"id":"obs-4"}}]`,
		`[{"source":{"resourceType":"Observation","resourceId":"obs-5","ownerPath":"component","ownerOrdinal":4},"codings":[],"choiceArm":"valueQuantity","logicalType":"decimal","status":"ABSENT","owner":{"id":"obs-5"}}]`,
		`[{"source":{"resourceType":"Observation","resourceId":"obs-6","ownerPath":"component","ownerOrdinal":5},"codings":[],"choiceArm":"valueString","logicalType":"string","value":true,"values":[true,"mixed",null,[2,null],{"nested":{"score":0}}],"unit":"mg","status":"VALUE","owner":{"id":"obs-6"}}]`,
		`[{"source":{"resourceType":"Observation","resourceId":"obs-7a","ownerPath":"component","ownerOrdinal":6},"codings":[],"choiceArm":"valueString","logicalType":"string","unit":null,"status":"ABSENT","owner":{"id":"obs-7a"}},null,{"source":{"resourceType":"Observation","resourceId":"obs-7b","ownerPath":"component","ownerOrdinal":7},"codings":[],"choiceArm":"valueString","logicalType":"string","status":"ABSENT","owner":{"id":"obs-7b"}}]`,
	}
	rows := make([]map[string]any, len(ownerRecordJSON))
	want := make([][]any, len(ownerRecordJSON))
	for index, encoded := range ownerRecordJSON {
		var input []any
		decoder := json.NewDecoder(strings.NewReader(encoded))
		decoder.UseNumber()
		if err := decoder.Decode(&input); err != nil {
			t.Fatalf("decode OWNER_RECORDS fixture %d: %v", index, err)
		}
		// The materialized OWNER_RECORDS contract distinguishes an explicit
		// null-valued object path from a path that was absent in the source.
		// Preserve the exact source values here, including null object fields
		// and null array elements, so this roundtrip catches either being lost.
		want[index] = input
		rows[index] = map[string]any{
			"__loom_row_id": fmt.Sprintf("%d", index+1),
			"OWNER_RECORDS": input,
		}
	}
	if err := client.InsertRows(ctx, table, columns, rows); err != nil {
		t.Fatal(err)
	}
	materialization := publishedreader.Materialization{
		ID: "roundtrip", Revision: "roundtrip", PhysicalTable: table,
		Columns: []publishedreader.Column{{Name: "OWNER_RECORDS", ClickHouse: "Array(Nullable(String))", LogicalType: "object", Repeated: true}},
	}
	reader := &publishedreader.Reader{ClickHouse: client}
	page, err := reader.Page(ctx, materialization, publishedreader.PageRequest{Columns: []string{"OWNER_RECORDS"}, First: len(want), Unrestricted: true})
	if err != nil {
		t.Fatalf("read typed OWNER_RECORDS page: %v", err)
	}
	if len(page.Rows) != len(want) {
		t.Fatalf("OWNER_RECORDS page row count = %d, want %d", len(page.Rows), len(want))
	}
	for index, row := range page.Rows {
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
	var streamed [][]any
	err = reader.Stream(ctx, materialization, publishedreader.StreamRequest{Columns: []string{"OWNER_RECORDS"}, Unrestricted: true}, func(row map[string]any) error {
		items, ok := row["OWNER_RECORDS"].([]any)
		if !ok {
			return fmt.Errorf("stream OWNER_RECORDS = %T, want array", row["OWNER_RECORDS"])
		}
		streamed = append(streamed, items)
		return nil
	})
	if err != nil {
		t.Fatalf("stream typed OWNER_RECORDS: %v", err)
	}
	if len(streamed) != len(want) {
		t.Fatalf("OWNER_RECORDS stream row count = %d, want %d", len(streamed), len(want))
	}
	for index, row := range streamed {
		gotEncoded, err := json.Marshal(row)
		if err != nil {
			t.Fatalf("encode streamed OWNER_RECORDS %d: %v", index, err)
		}
		wantEncoded, err := json.Marshal(want[index])
		if err != nil {
			t.Fatalf("encode OWNER_RECORDS expectation %d: %v", index, err)
		}
		if string(gotEncoded) != string(wantEncoded) {
			t.Errorf("stream OWNER_RECORDS row %d = %s, want %s", index, gotEncoded, wantEncoded)
		}
	}
}

func TestClickHouseNativeINNERJoinProjectsOWNERRecordsJSONRoundTrip(t *testing.T) {
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
		Timeout:  20 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.EnsureDatabase(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("ping ClickHouse: %v", err)
	}

	suffix := uuid.NewString()[:8]
	leftTable := "loom_owner_join_left_it_" + suffix
	rightTable := "loom_owner_join_right_it_" + suffix
	outputTable := "loom_owner_join_output_it_" + suffix
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		for _, table := range []string{leftTable, rightTable, outputTable} {
			if err := client.DropTable(cleanupCtx, table); err != nil {
				t.Errorf("drop scratch table %s: %v", table, err)
			}
		}
	}()

	leftColumns := []Column{
		{Name: "__loom_row_id", Type: "String"},
		{Name: "join_key", Type: "String"},
	}
	rightColumns := []Column{
		{Name: "__loom_row_id", Type: "String"},
		{Name: "join_key", Type: "String"},
		{Name: "OWNER_RECORDS", Type: "Array(Nullable(String))", LogicalType: "object"},
	}
	for _, setup := range []struct {
		table   string
		columns []Column
	}{
		{table: leftTable, columns: leftColumns},
		{table: rightTable, columns: rightColumns},
	} {
		if err := client.CreateTable(ctx, setup.table, setup.columns); err != nil {
			t.Fatalf("create %s: %v", setup.table, err)
		}
	}
	leftRows := []map[string]any{
		{"__loom_row_id": "l1", "join_key": "matched"},
		{"__loom_row_id": "l2", "join_key": "left-only"},
	}
	ownerJSON := []string{
		`[{"owner":"first","unit":null,"values":[1,null,{"deep":true}]},null,{"owner":"second","values":[9007199254740993]}]`,
		`[{"owner":"third","unit":"mg","values":[2,1]}]`,
		`[{"owner":"right-only"}]`,
	}
	ownerLists := make([][]any, len(ownerJSON))
	rightRows := make([]map[string]any, len(ownerJSON))
	for index, encoded := range ownerJSON {
		decoder := json.NewDecoder(strings.NewReader(encoded))
		decoder.UseNumber()
		if err := decoder.Decode(&ownerLists[index]); err != nil {
			t.Fatalf("decode owner fixture %d: %v", index, err)
		}
		key := "matched"
		if index == 2 {
			key = "right-only"
		}
		rightRows[index] = map[string]any{
			"__loom_row_id": fmt.Sprintf("r%d", index+1),
			"join_key":      key,
			"OWNER_RECORDS": ownerLists[index],
		}
	}
	if err := client.InsertRows(ctx, leftTable, leftColumns, leftRows); err != nil {
		t.Fatalf("insert left join input: %v", err)
	}
	if err := client.InsertRows(ctx, rightTable, rightColumns, rightRows); err != nil {
		t.Fatalf("insert right join input: %v", err)
	}

	leftInput := ir.ResolvedClickHouseTable{
		TableID: "left-table", RevisionID: "left-revision", OutputID: "left-output",
		Project: "owner-join-test", DatasetGeneration: "generation-1", ReceiptID: "left-receipt",
		SchemaDigest: "left-schema", ScopeDigest: "same-scope", PhysicalTable: leftTable, Unrestricted: true,
		Columns: []ir.ResolvedClickHouseColumn{
			{ID: "left-row-id", Name: "__loom_row_id", ClickHouseType: "String"},
			{ID: "left-key", Name: "join_key", ClickHouseType: "String", LogicalType: "string"},
		},
	}
	rightInput := ir.ResolvedClickHouseTable{
		TableID: "right-table", RevisionID: "right-revision", OutputID: "right-output",
		Project: "owner-join-test", DatasetGeneration: "generation-1", ReceiptID: "right-receipt",
		SchemaDigest: "right-schema", ScopeDigest: "same-scope", PhysicalTable: rightTable, Unrestricted: true,
		Columns: []ir.ResolvedClickHouseColumn{
			{ID: "right-row-id", Name: "__loom_row_id", ClickHouseType: "String"},
			{ID: "right-key", Name: "join_key", ClickHouseType: "String", LogicalType: "string"},
			{ID: "right-owners", Name: "OWNER_RECORDS", ClickHouseType: "Array(Nullable(String))", LogicalType: "object", Repeated: true},
		},
	}
	plan := ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineKeyJoin,
		Inputs: []ir.PhysicalCombineInputRef{
			{TableID: "left-table", RevisionID: "left-revision", OutputID: "left-output"},
			{TableID: "right-table", RevisionID: "right-revision", OutputID: "right-output"},
		},
		Keys:             []ir.PhysicalCombineKey{{LeftColumnID: "left-key", RightColumnID: "right-key"}},
		JoinType:         "INNER",
		RightMatchPolicy: "PRESERVE_ALL",
		Projections: []ir.PhysicalCombineProjection{
			{OutputColumnID: "joined-key", InputIndex: 0, InputColumnID: "left-key"},
			{OutputColumnID: "joined-owners", InputIndex: 1, InputColumnID: "right-owners"},
		},
		Outputs: []ir.PhysicalCombineOutputColumn{
			{ID: "joined-key", Name: "join_key", LogicalType: "string", ClickHouseType: "String"},
			{ID: "joined-owners", Name: "OWNER_RECORDS", LogicalType: "object", ClickHouseType: "Array(Nullable(String))", Repeated: true},
		},
	}
	rendered, err := renderclickhouse.RenderCombine(plan, []ir.ResolvedClickHouseTable{leftInput, rightInput}, "owner-join-test")
	if err != nil {
		t.Fatalf("render INNER OWNER_RECORDS join: %v", err)
	}
	if !strings.Contains(rendered.Query, "ALL INNER JOIN") {
		t.Fatalf("renderer did not produce an INNER join: %s", rendered.Query)
	}
	outputColumns := []Column{
		{Name: "__loom_row_id", Type: "String"},
		{Name: "join_key", Type: "String", LogicalType: "string"},
		{Name: "OWNER_RECORDS", Type: "Array(Nullable(String))", LogicalType: "object"},
		{Name: "auth_resource_path", Type: "String"},
	}
	if !reflect.DeepEqual(rendered.Columns, []string{"__loom_row_id", "join_key", "OWNER_RECORDS", "auth_resource_path"}) {
		t.Fatalf("rendered output columns = %#v", rendered.Columns)
	}
	if err := client.CreateTable(ctx, outputTable, outputColumns); err != nil {
		t.Fatalf("create INNER join output: %v", err)
	}
	quotedColumns := make([]string, len(rendered.Columns))
	for index, column := range rendered.Columns {
		quotedColumns[index] = "`" + column + "`"
	}
	insert := fmt.Sprintf("INSERT INTO `%s` (%s) %s", outputTable, strings.Join(quotedColumns, ", "), rendered.Query)
	if err := client.conn.Exec(ctx, insert, rendered.Args...); err != nil {
		t.Fatalf("materialize rendered INNER join: %v", err)
	}
	if err := client.VerifyOutput(ctx, outputTable, outputColumns, 2); err != nil {
		t.Fatalf("verify rendered INNER join output: %v", err)
	}

	materialization := publishedreader.Materialization{
		ID: "joined-output", Revision: "joined-revision", Project: "owner-join-test",
		DatasetGeneration: "generation-1", PhysicalTable: outputTable,
		Columns: []publishedreader.Column{
			{Name: "join_key", ClickHouse: "String", LogicalType: "string"},
			{Name: "OWNER_RECORDS", ClickHouse: "Array(Nullable(String))", LogicalType: "object", Repeated: true},
		},
	}
	reader := &publishedreader.Reader{ClickHouse: client}
	want := []struct {
		rowID string
		key   string
		list  []any
	}{
		{rowID: "2:l1:2:r1", key: "matched", list: ownerLists[0]},
		{rowID: "2:l1:2:r2", key: "matched", list: ownerLists[1]},
	}
	page, err := reader.Page(ctx, materialization, publishedreader.PageRequest{
		Columns: []string{"join_key", "OWNER_RECORDS"}, First: 10, Unrestricted: true,
	})
	if err != nil {
		t.Fatalf("read joined OWNER_RECORDS page: %v", err)
	}
	if len(page.Rows) != len(want) {
		t.Fatalf("INNER join page rows = %d, want %d: %#v", len(page.Rows), len(want), page.Rows)
	}
	if len(page.RowIDs) != len(want) {
		t.Fatalf("INNER join page row identities = %d, want %d: %#v", len(page.RowIDs), len(want), page.RowIDs)
	}
	seenRowIDs := map[string]bool{}
	for index, rowID := range page.RowIDs {
		if rowID != want[index].rowID || seenRowIDs[rowID] {
			t.Fatalf("joined row identity %d = %q, want unique %q", index, rowID, want[index].rowID)
		}
		seenRowIDs[rowID] = true
	}
	for index, row := range page.Rows {
		if row["join_key"] != want[index].key {
			t.Fatalf("joined row %d key = %#v, want %q", index, row["join_key"], want[index].key)
		}
		got, err := json.Marshal(row["OWNER_RECORDS"])
		if err != nil {
			t.Fatalf("encode joined OWNER_RECORDS row %d: %v", index, err)
		}
		expected, err := json.Marshal(want[index].list)
		if err != nil {
			t.Fatalf("encode expected OWNER_RECORDS row %d: %v", index, err)
		}
		if string(got) != string(expected) {
			t.Fatalf("joined OWNER_RECORDS row %d = %s, want exact ordered owners %s", index, got, expected)
		}
	}

	var streamed []map[string]any
	if err := reader.Stream(ctx, materialization, publishedreader.StreamRequest{
		Columns: []string{"join_key", "OWNER_RECORDS"}, Unrestricted: true,
	}, func(row map[string]any) error {
		streamed = append(streamed, row)
		return nil
	}); err != nil {
		t.Fatalf("stream joined OWNER_RECORDS rows: %v", err)
	}
	if len(streamed) != len(want) {
		t.Fatalf("INNER join stream rows = %d, want %d", len(streamed), len(want))
	}
	for index, row := range streamed {
		if row["join_key"] != want[index].key {
			t.Fatalf("streamed joined row %d key = %#v, want %q", index, row["join_key"], want[index].key)
		}
		got, err := json.Marshal(row["OWNER_RECORDS"])
		if err != nil {
			t.Fatalf("encode streamed OWNER_RECORDS row %d: %v", index, err)
		}
		expected, err := json.Marshal(want[index].list)
		if err != nil {
			t.Fatalf("encode streamed expected OWNER_RECORDS row %d: %v", index, err)
		}
		if string(got) != string(expected) {
			t.Fatalf("streamed OWNER_RECORDS row %d = %s, want exact ordered owners %s", index, got, expected)
		}
	}
}
