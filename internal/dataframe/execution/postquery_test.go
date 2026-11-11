package execution

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
)

func TestDynamicChecksRetainsEveryFamilySharingACompatibilityColumn(t *testing.T) {
	checks := dynamicChecks([]lower.DynamicColumnMetadata{
		{Name: "HTAN_DATA_FILE_ID", DynamicName: "identifier_keys", SourceKey: "HTAN_DATA_FILE_ID", ValueType: "string"},
		{Name: "HTAN_DATA_FILE_ID", DynamicName: "category_keys", SourceKey: "HTAN_DATA_FILE_ID", ValueType: "string"},
	})
	if _, ok := checks["identifier_keys"]["HTAN_DATA_FILE_ID"]; !ok {
		t.Fatalf("identifier compatibility key was discarded: %#v", checks)
	}
	if _, ok := checks["category_keys"]["HTAN_DATA_FILE_ID"]; !ok {
		t.Fatalf("category compatibility key was discarded: %#v", checks)
	}
}

func TestMaterializePostQueryRejectsDynamicKeyDrift(t *testing.T) {
	checks := map[string]map[string]DynamicColumnCheck{
		"code": {"a": {ColumnName: "code_a", ValueType: "string"}},
	}
	row := map[string]any{
		"code_a":                      "value",
		"__loom_dynamic_runtime_keys": map[string]any{"code": []any{"unexpected"}},
	}
	_, err := materializePostQueryRowWithChecks(row, checks)
	if err == nil {
		t.Fatal("unexpected dynamic key was accepted")
	}
	var drift *DynamicDriftError
	if !errors.As(err, &drift) || drift.DynamicName != "code" || drift.Key != "unexpected" || drift.FrozenKeyCount != 1 {
		t.Fatalf("dynamic drift = %#v, want code/unexpected/1", drift)
	}
}

func TestMaterializePostQueryRejectsDynamicTypeDrift(t *testing.T) {
	checks := map[string]map[string]DynamicColumnCheck{
		"code": {"a": {ColumnName: "code_a", ValueType: "string"}},
	}
	row := map[string]any{
		"code_a":                      42,
		"__loom_dynamic_runtime_keys": map[string]any{"code": []any{"a"}},
	}
	if _, err := materializePostQueryRowWithChecks(row, checks); err == nil {
		t.Fatal("dynamic type drift was accepted")
	}
}

func TestMaterializePostQueryValidatesEveryPresentColumnWhenRuntimeKeysCoverAnotherFamily(t *testing.T) {
	checks := map[string]map[string]DynamicColumnCheck{
		"category_keys": {"stage": {ColumnName: "category_stage", ValueType: "string"}},
		"identifier_keys": {
			"count": {ColumnName: "identifier_count", ValueType: "integer"},
			"tags":  {ColumnName: "identifier_tags", ValueType: "string", Many: true},
		},
	}
	// Runtime key metadata enumerates only family A; family B columns are still
	// present in the row and must satisfy their frozen types.
	newRow := func(dynamicValues map[string]any) map[string]any {
		row := map[string]any{
			"category_stage":              "stage-a",
			"__loom_dynamic_runtime_keys": map[string]any{"category_keys": []any{"stage"}},
		}
		for key, value := range dynamicValues {
			row[key] = value
		}
		return row
	}

	mismatches := map[string]map[string]any{
		"integer mismatch": newRow(map[string]any{"identifier_count": "not-an-integer"}),
		"array mismatch":   newRow(map[string]any{"identifier_tags": "tag"}),
		"both mismatch":    newRow(map[string]any{"identifier_count": "not-an-integer", "identifier_tags": []any{"tag", 42}}),
	}
	for name, row := range mismatches {
		if _, err := materializePostQueryRowWithChecks(row, checks); err == nil {
			t.Fatalf("%s in an unenumerated family was accepted: %#v", name, row)
		}
	}

	valid := newRow(map[string]any{"identifier_count": 7, "identifier_tags": []any{"tag", "other"}})
	if _, err := materializePostQueryRowWithChecks(valid, checks); err != nil {
		t.Fatalf("valid row across both families rejected: %v", err)
	}
	// An unselected family B column stays valid because only present values bind.
	if _, err := materializePostQueryRowWithChecks(newRow(map[string]any{"identifier_count": 7}), checks); err != nil {
		t.Fatalf("valid row with a missing unselected column rejected: %v", err)
	}
}

func TestMaterializePostQueryValidatesPresentColumnsWithoutRuntimeKeys(t *testing.T) {
	checks := map[string]map[string]DynamicColumnCheck{
		"category_keys": {"stage": {ColumnName: "category_stage", ValueType: "string", AllowUnknownKeys: true}},
	}
	if _, err := materializePostQueryRowWithChecks(map[string]any{"category_stage": "stage-a"}, checks); err != nil {
		t.Fatalf("valid row without runtime keys rejected: %v", err)
	}
	if _, err := materializePostQueryRowWithChecks(map[string]any{"category_stage": 42}, checks); err == nil {
		t.Fatal("type drift without runtime keys was accepted")
	}
}

func TestOutputStreamRejectsMalformedRowFromUnenumeratedDynamicFamily(t *testing.T) {
	newStream := func(rows ...map[string]any) OutputStream {
		index := 0
		return OutputStream{
			Name:    "Condition",
			Columns: []string{"condition", "category_stage", "identifier_count"},
			command: mustAQLCommand("FOR root IN Condition RETURN root", nil),
			DynamicChecks: map[string]map[string]DynamicColumnCheck{
				"category_keys":   {"stage": {ColumnName: "category_stage", ValueType: "string"}},
				"identifier_keys": {"count": {ColumnName: "identifier_count", ValueType: "integer"}},
			},
			sourceRows: func(_ context.Context, request RowsRequest, visit RowVisitor) error {
				command := request.Command
				_, _, dispatchErr := command.AQLParameters()
				if dispatchErr != nil {
					return dispatchErr
				}

				if err := visit(rows[index]); err != nil {
					return err
				}
				index++
				return nil
			},
		}
	}
	malformed := map[string]any{
		"__loom_row_id":               "row-1",
		"condition":                   "C50.9",
		"category_stage":              "stage-a",
		"identifier_count":            "not-an-integer",
		"__loom_dynamic_runtime_keys": map[string]any{"category_keys": []any{"stage"}},
	}
	var visited []map[string]any
	if _, err := newStream(malformed).Stream(context.Background(), func(row map[string]any) error {
		visited = append(visited, row)
		return nil
	}); err == nil {
		t.Fatal("malformed row from an unenumerated dynamic family reached the public visitor")
	}
	if len(visited) != 0 {
		t.Fatalf("public visitor received malformed rows: %#v", visited)
	}

	valid := map[string]any{
		"__loom_row_id":               "row-2",
		"condition":                   "C50.9",
		"category_stage":              "stage-a",
		"identifier_count":            float64(7),
		"__loom_dynamic_runtime_keys": map[string]any{"category_keys": []any{"stage"}},
	}
	if _, err := newStream(valid).Stream(context.Background(), func(row map[string]any) error {
		visited = append(visited, row)
		return nil
	}); err != nil {
		t.Fatalf("valid stream: %v", err)
	}
	if len(visited) != 1 || visited[0]["identifier_count"] != float64(7) {
		t.Fatalf("public rows = %#v", visited)
	}
	if _, ok := visited[0]["__loom_dynamic_runtime_keys"]; ok {
		t.Fatalf("public row leaked dynamic runtime keys: %#v", visited[0])
	}
}

func TestMaterializePostQueryExtensionFamilyIgnoresSiblingKeys(t *testing.T) {
	checks := map[string]map[string]DynamicColumnCheck{
		"attachment_extension_keys__checksum_sha256": {
			"checksum_sha256": {ColumnName: "checksum_sha256", ValueType: "string", AllowUnknownKeys: true},
		},
	}
	row, err := materializePostQueryRowWithChecks(map[string]any{
		"checksum_sha256": "abc123",
		"__loom_dynamic_runtime_keys": map[string]any{
			"attachment_extension_keys__checksum_sha256": []any{"checksum_sha256", "sha256"},
		},
	}, checks)
	if err != nil {
		t.Fatal(err)
	}
	if row["checksum_sha256"] != "abc123" {
		t.Fatalf("projected extension value = %#v", row["checksum_sha256"])
	}
}

func TestMaterializePostQueryExtensionFamilyStillRejectsKnownKeyTypeDrift(t *testing.T) {
	checks := map[string]map[string]DynamicColumnCheck{
		"attachment_extension_keys__checksum_sha256": {
			"checksum_sha256": {ColumnName: "checksum_sha256", ValueType: "string", AllowUnknownKeys: true},
		},
	}
	_, err := materializePostQueryRowWithChecks(map[string]any{
		"checksum_sha256": 42,
		"__loom_dynamic_runtime_keys": map[string]any{
			"attachment_extension_keys__checksum_sha256": []any{"checksum_sha256", "sha256"},
		},
	}, checks)
	if err == nil {
		t.Fatal("known extension key type drift was accepted")
	}
}

func TestMaterializePostQueryAllowsFixedLookupAfterConstructionDropsRuntimeKeys(t *testing.T) {
	checks := map[string]map[string]DynamicColumnCheck{
		"condition_identifier": {
			"https://cda.readthedocs.io/diagnosis": {ColumnName: "diagnosis", ValueType: "string", AllowUnknownKeys: true},
		},
	}
	row, err := materializePostQueryRowWithChecks(map[string]any{"diagnosis": "C50.9"}, checks)
	if err != nil || row["diagnosis"] != "C50.9" {
		t.Fatalf("fixed lookup after construction = %#v, %v", row, err)
	}
	if _, err := materializePostQueryRowWithChecks(map[string]any{"diagnosis": 42}, checks); err == nil {
		t.Fatal("fixed lookup with wrong value type was accepted")
	}
	checks["frozen"] = map[string]DynamicColumnCheck{"a": {ColumnName: "frozen_a", ValueType: "string"}}
	if _, err := materializePostQueryRowWithChecks(map[string]any{"diagnosis": "C50.9"}, checks); err == nil {
		t.Fatal("frozen dynamic family without runtime keys was accepted")
	}
}

func TestMaterializePostQueryValidatesAllValuesAfterConstruction(t *testing.T) {
	checks := map[string]map[string]DynamicColumnCheck{
		"condition_identifier": {
			"https://cda.readthedocs.io/diagnosis": {ColumnName: "diagnosis", ValueType: "string", Many: true, AllowUnknownKeys: true},
		},
	}
	for _, values := range []any{[]any{}, []any{"C50.9", "C50.8"}} {
		if _, err := materializePostQueryRowWithChecks(map[string]any{"diagnosis": values}, checks); err != nil {
			t.Fatalf("valid diagnosis list %#v rejected: %v", values, err)
		}
	}
	for _, values := range []any{"C50.9", nil, []any{"C50.9", 42}} {
		if _, err := materializePostQueryRowWithChecks(map[string]any{"diagnosis": values}, checks); err == nil {
			t.Fatalf("invalid diagnosis list %#v accepted", values)
		}
	}
}

func TestMaterializePostQueryStripsDynamicMetadata(t *testing.T) {
	checks := map[string]map[string]DynamicColumnCheck{
		"code": {"a": {ColumnName: "code_a", ValueType: "string"}},
	}
	row, err := materializePostQueryRowWithChecks(map[string]any{
		"code_a":                      "value",
		"__loom_dynamic_runtime_keys": map[string]any{"code": []any{"a"}},
	}, checks)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := row["__loom_dynamic_runtime_keys"]; ok {
		t.Fatal("dynamic metadata leaked into logical row")
	}
}

// ---------------------------------------------------------------------------
// pg-exec-03: PostgreSQL JSONB row values through the shared materializer.
//
// The PostgreSQL row adapter decodes every json/jsonb column to
// json.RawMessage (internal/backends/postgres/internal/rows/rows.go) so SQL
// NULL stays distinct from the JSON null scalar at the adapter boundary.
// These legs pin that the shared materializer runs the identical Loom
// post-query and named-UUID contract on that shape. Arango and ClickHouse
// never produce json.RawMessage: both decode with encoding/json
// (go-driver/v2@v2.3.1 arangodb/cursor_impl.go:123;
// internal/store/clickhouse/client.go QueryRowsArgsVisit) — module-source
// probes banked in the ticket plan — so the legs below are behavior
// additions for PostgreSQL rows only, and the equivalence cells here and in
// engine_postgres_postquery_pg18_test.go prove cross-target sameness.
// ---------------------------------------------------------------------------

const (
	exec03DNSNamespace  = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	exec03UUID5Wire     = `{"__loom_exact_uuid_operation":"uuid5","__loom_exact_uuid_args":["6ba7b810-9dad-11d1-80b4-00c04fd430c8","www.widgets.com"]}`
	exec03UUID3Wire     = `{"__loom_exact_uuid_operation":"uuid3","__loom_exact_uuid_args":["6ba7b810-9dad-11d1-80b4-00c04fd430c8","www.widgets.com"]}`
	exec03UUID5CallWire = `{"__loom_postquery_call":"uuid5","__loom_postquery_args":["6ba7b810-9dad-11d1-80b4-00c04fd430c8","www.widgets.com"]}`
	exec03UUID5Literal  = "21f7f8de-8051-5b89-8680-0195ef798b6a"
	exec03UUID3Literal  = "3d813cbb-47fb-32ba-91df-831e1593ac29"
)

func TestMaterializePostQueryJSONBMarkerComputesNamedUUID(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire string
		want string
	}{
		{"exact uuid5 marker", exec03UUID5Wire, exec03UUID5Literal},
		{"exact uuid3 marker", exec03UUID3Wire, exec03UUID3Literal},
		{"post-query call marker", exec03UUID5CallWire, exec03UUID5Literal},
		{"cast call keeps target", `{"__loom_postquery_call":"cast","__loom_postquery_target":"string","__loom_postquery_args":[42]}`, "42"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := materializePostQueryValue(json.RawMessage(tc.wire))
			if err != nil {
				t.Fatalf("materialize: %v", err)
			}
			if got != tc.want {
				t.Fatalf("materialized %#v, want %#v", got, tc.want)
			}
			if _, ok := got.(string); !ok {
				t.Fatalf("materialized value has Go type %T, want string", got)
			}
		})
	}
	// Malformed marker shape inside valid JSON keeps the existing typed
	// refusal: args must be an ordered array.
	if _, err := materializePostQueryValue(json.RawMessage(`{"__loom_exact_uuid_operation":"uuid5","__loom_exact_uuid_args":"nope"}`)); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("non-array exact-UUID args accepted: %v", err)
	}
	if _, err := materializePostQueryValue(json.RawMessage(`{"__loom_postquery_call":"concat","__loom_postquery_args":{}}`)); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("non-array post-query args accepted: %v", err)
	}
}

func TestMaterializePostQueryPGShapeMatchesDriverDecodedShape(t *testing.T) {
	// One logical row delivered in the two wire shapes. The Arango/CH shape
	// is produced by the very primitive those drivers use (json.Unmarshal
	// into any); the PostgreSQL shape carries each column as the
	// json.RawMessage the adapter hands the visitor.
	pgRow := map[string]any{
		"marker": json.RawMessage(exec03UUID5Wire),
		"nested": json.RawMessage(`{"inner":` + exec03UUID3Wire + `}`),
		"list":   json.RawMessage(`[` + exec03UUID5CallWire + `,null,1.5]`),
		"flag":   json.RawMessage(`true`),
		"name":   json.RawMessage(`"w"`),
		"count":  json.RawMessage(`2`),
	}
	driverWire := `{"marker":` + exec03UUID5Wire + `,"nested":{"inner":` + exec03UUID3Wire + `},"list":[` + exec03UUID5CallWire + `,null,1.5],"flag":true,"name":"w","count":2}`
	var driverRow map[string]any
	if err := json.Unmarshal([]byte(driverWire), &driverRow); err != nil {
		t.Fatalf("driver-shape decode: %v", err)
	}
	pgResult, err := materializePostQueryRowWithChecks(pgRow, nil)
	if err != nil {
		t.Fatalf("materialize PG shape: %v", err)
	}
	driverResult, err := materializePostQueryRowWithChecks(driverRow, nil)
	if err != nil {
		t.Fatalf("materialize driver shape: %v", err)
	}
	if !reflect.DeepEqual(pgResult, driverResult) {
		t.Fatalf("PG shape %v != driver shape %v", pgResult, driverResult)
	}
	want := map[string]any{
		"marker": exec03UUID5Literal,
		"nested": map[string]any{"inner": exec03UUID3Literal},
		"list":   []any{exec03UUID5Literal, nil, 1.5},
		"flag":   true,
		"name":   "w",
		"count":  float64(2),
	}
	if !reflect.DeepEqual(pgResult, want) {
		t.Fatalf("materialized PG row = %v, want %v", pgResult, want)
	}
	// Discriminating control: without materialization the PG shape is raw
	// bytes, not the driver shape — so the DeepEqual above really catches a
	// missing unwrap (mutant M1 victim).
	if reflect.DeepEqual(map[string]any(pgRow), driverRow) {
		t.Fatal("discriminating control broken: unmaterialized RawMessage row equals the decoded driver row")
	}
}

// TestMaterializePostQueryCrossTargetJSONNullEquivalence is the ratified
// cross-target cell: the same JSON-null payload materializes to the
// identical public value on the PostgreSQL row shape (json.RawMessage per
// column, exactly what internal/backends/postgres/internal/rows/rows.go
// decodeColumn hands the visitor), the Arango row shape (driver cursor
// decodes documents with encoding/json — go-driver/v2 cursor_impl.go:123,
// module-source probe banked in the plan), and the ClickHouse row shape
// (client decodes a row by json.Unmarshal of toJSONString(tuple(..)) —
// internal/store/clickhouse/client.go QueryRowsArgsVisit, probe banked).
// The live PostgreSQL end-to-end leg against the real server — production
// adapter plus production PreviewOutput — is
// TestPostgresPostQueryCrossTargetJSONNullConvergence in
// engine_postgres_postquery_pg18_test.go and asserts the same witnesses.
func TestMaterializePostQueryCrossTargetJSONNullEquivalence(t *testing.T) {
	// Shared column wires: one JSON null, one string, one number, one array
	// containing a null, one boolean.
	const (
		genderWire = `null`
		codeWire   = `"C50.9"`
		countWire  = `7`
		tagsWire   = `["a",null]`
		flagWire   = `true`
	)
	wantPublic := map[string]any{
		"gender": nil,
		"code":   "C50.9",
		"count":  float64(7),
		"tags":   []any{"a", nil},
		"flag":   true,
	}
	type leg struct {
		name string
		row  map[string]any
	}
	// PostgreSQL: adapter hands the visitor one map whose jsonb columns are
	// json.RawMessage and whose SQL-NULL columns are nil (three-state at
	// the adapter; gender exercises the JSON-null scalar).
	postgresLeg := map[string]any{
		"gender": json.RawMessage(genderWire),
		"code":   json.RawMessage(codeWire),
		"count":  json.RawMessage(countWire),
		"tags":   json.RawMessage(tagsWire),
		"flag":   json.RawMessage(flagWire),
	}
	// Arango: the driver decodes the document with encoding/json
	// (cursor_impl.go:123); each projected column is that decode's value.
	arangoLeg := map[string]any{}
	for column, wire := range map[string]string{"gender": genderWire, "code": codeWire, "count": countWire, "tags": tagsWire, "flag": flagWire} {
		var value any
		if err := json.Unmarshal([]byte(wire), &value); err != nil {
			t.Fatalf("arango-shape decode %s: %v", column, err)
		}
		arangoLeg[column] = value
	}
	// ClickHouse: the client decodes one row by json.Unmarshal of the
	// toJSONString(tuple(columns)) text the server returns
	// (QueryRowsArgsVisit); positional values are assigned to columns.
	var clickHouseValues []any
	if err := json.Unmarshal([]byte(`[`+genderWire+","+codeWire+","+countWire+","+tagsWire+","+flagWire+`]`), &clickHouseValues); err != nil {
		t.Fatalf("clickhouse-shape decode: %v", err)
	}
	clickHouseLeg := map[string]any{}
	for i, column := range []string{"gender", "code", "count", "tags", "flag"} {
		clickHouseLeg[column] = clickHouseValues[i]
	}
	checks := map[string]map[string]DynamicColumnCheck{
		"code": {"C50.9": {ColumnName: "code", ValueType: "string", AllowUnknownKeys: true}},
	}
	legs := []leg{{"postgres", postgresLeg}, {"arangodb", arangoLeg}, {"clickhouse", clickHouseLeg}}
	results := map[string]map[string]any{}
	for _, entry := range legs {
		row, err := materializePostQueryRowWithChecks(entry.row, checks)
		if err != nil {
			t.Fatalf("%s leg: %v", entry.name, err)
		}
		results[entry.name] = row
		if !reflect.DeepEqual(row, wantPublic) {
			t.Fatalf("%s leg public row = %v, want %v", entry.name, row, wantPublic)
		}
		// Go type identity per column, not JSON-text equality: RawMessage
		// would marshal identically and hide here.
		if value, ok := row["gender"]; !ok || value != nil {
			t.Fatalf("%s leg: JSON-null column public value is %#v (Go %T), want untyped nil", entry.name, value, value)
		}
		if _, ok := row["code"].(string); !ok {
			t.Fatalf("%s leg: code column has Go type %T, want string", entry.name, row["code"])
		}
		if _, ok := row["count"].(float64); !ok {
			t.Fatalf("%s leg: count column has Go type %T, want float64", entry.name, row["count"])
		}
	}
	for _, pair := range [][2]string{{"postgres", "arangodb"}, {"postgres", "clickhouse"}, {"arangodb", "clickhouse"}} {
		if !reflect.DeepEqual(results[pair[0]], results[pair[1]]) {
			t.Fatalf("cross-target convergence broken: %s %v != %s %v", pair[0], results[pair[0]], pair[1], results[pair[1]])
		}
	}
	// ALIVE controls. (a) A non-null payload on the same harness must
	// converge to the string, so the cell cannot pass by collapsing every
	// column to nil. (b) The unmaterialized PostgreSQL RawMessage("null")
	// must NOT equal the driver nil: the cell is sensitive to a dropped
	// unwrap (mutant M3 victim).
	aliveWires := map[string]string{"gender": `"female"`, "code": `"C50.9"`, "count": countWire, "tags": tagsWire, "flag": flagWire}
	for _, entry := range legs {
		row := map[string]any{}
		switch entry.name {
		case "postgres":
			for column, wire := range aliveWires {
				row[column] = json.RawMessage(wire)
			}
		case "arangodb":
			for column, wire := range aliveWires {
				var value any
				if err := json.Unmarshal([]byte(wire), &value); err != nil {
					t.Fatalf("alive arango decode: %v", err)
				}
				row[column] = value
			}
		case "clickhouse":
			var values []any
			if err := json.Unmarshal([]byte(`["female","C50.9",7,["a",null],true]`), &values); err != nil {
				t.Fatalf("alive clickhouse decode: %v", err)
			}
			for i, column := range []string{"gender", "code", "count", "tags", "flag"} {
				row[column] = values[i]
			}
		}
		materialized, err := materializePostQueryRowWithChecks(row, checks)
		if err != nil {
			t.Fatalf("%s alive control: %v", entry.name, err)
		}
		if materialized["gender"] != "female" {
			t.Fatalf("%s alive control: gender = %#v, want \"female\"", entry.name, materialized["gender"])
		}
	}
	if pre := postgresLeg["gender"]; pre == nil {
		t.Fatal("discriminating control broken: adapter-shape gender column is already nil before materialization, so the nil convergence below is vacuous")
	} else if _, isRaw := pre.(json.RawMessage); !isRaw {
		t.Fatalf("adapter-shape gender column is %T, want json.RawMessage", pre)
	}
}

func TestMaterializePostQueryJSONBNullsScalarsAndBytes(t *testing.T) {
	got, err := materializePostQueryValue(json.RawMessage(`null`))
	if err != nil || got != nil {
		t.Fatalf("JSON null scalar materialized to %#v (err %v), want untyped nil", got, err)
	}
	for _, tc := range []struct {
		wire string
		want any
	}{
		{`"C50"`, "C50"},
		{`7`, float64(7)},
		{`true`, true},
	} {
		got, err := materializePostQueryValue(json.RawMessage(tc.wire))
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("scalar %s materialized to %#v (err %v), want %#v", tc.wire, got, err, tc.want)
		}
	}
	// bytea columns stay opaque bytes: valid-JSON text inside a []byte is
	// NOT unwrapped (mutant M4 victim).
	opaque := []byte(`"C50"`)
	got, err = materializePostQueryValue(opaque)
	if err != nil {
		t.Fatalf("bytea passthrough: %v", err)
	}
	if bytes, ok := got.([]byte); !ok || string(bytes) != `"C50"` {
		t.Fatalf("bytea value materialized to %#v (Go %T), want unchanged []byte", got, got)
	}
	// A typed-nil RawMessage is fail-closed: typed error, no silent nil.
	got, err = materializePostQueryValue(json.RawMessage(nil))
	if err == nil || got != nil {
		t.Fatalf("typed-nil RawMessage materialized to %#v (err %v), want refusal", got, err)
	}
}

func TestMaterializePostQueryJSONBMalformedFailsClosed(t *testing.T) {
	// Every malformed payload must be a typed refusal carrying no value:
	// no panic (a panic fails this test) and never a silent nil value with
	// a nil error.
	for _, wire := range []string{"", " ", "{", "[1,", `"unterminated`, `{"a":1}}`, `nope`, `{"a":}`, `1x`, `{"__loom_exact_uuid_operation":`} {
		got, err := materializePostQueryValue(json.RawMessage(wire))
		if err == nil {
			t.Fatalf("malformed JSONB %q accepted with value %#v", wire, got)
		}
		if got != nil {
			t.Fatalf("malformed JSONB %q refused but leaked value %#v", wire, got)
		}
		if _, err := materializePostQueryRowWithChecks(map[string]any{"col": json.RawMessage(wire)}, nil); err == nil {
			t.Fatalf("row with malformed JSONB column %q published", wire)
		}
	}
	// ALIVE control: the well-formed sibling of every table entry
	// materializes on the same code path.
	if _, err := materializePostQueryValue(json.RawMessage(`{"a":1}`)); err != nil {
		t.Fatalf("alive control rejected: %v", err)
	}
}

func TestMaterializePostQueryJSONBDeepNestingBounds(t *testing.T) {
	// The unwrap decodes through encoding/json, whose decoder enforces a
	// hard 10,000-level nesting bound, and the re-entrant walk is bounded
	// by the decoded value's depth. Pin the exact boundary: 10,000 levels
	// materialize, 10,001 are a typed refusal (no panic, no silent nil).
	deep := func(levels int) string {
		return strings.Repeat("[", levels) + "1" + strings.Repeat("]", levels)
	}
	for _, levels := range []int{200, 10000} {
		got, err := materializePostQueryValue(json.RawMessage(deep(levels)))
		if err != nil {
			t.Fatalf("%d-level nesting refused: %v", levels, err)
		}
		// Walk to the leaf to prove full depth materialized.
		value := got
		for depth := 0; depth < levels; depth++ {
			items, ok := value.([]any)
			if !ok || len(items) != 1 {
				t.Fatalf("%d-level nesting truncated at depth %d: %#v", levels, depth, value)
			}
			value = items[0]
		}
		if value != float64(1) {
			t.Fatalf("%d-level nesting leaf = %#v, want 1.0", levels, value)
		}
	}
	for _, levels := range []int{10001, 20000} {
		got, err := materializePostQueryValue(json.RawMessage(deep(levels)))
		if err == nil {
			t.Fatalf("%d-level nesting accepted without refusal", levels)
		}
		if got != nil {
			t.Fatalf("%d-level nesting refused but leaked a value", levels)
		}
	}
	// A marker buried inside legitimate nesting materializes recursively
	// (acceptance 5 at depth).
	wire := `{"a":{"b":[{"col":` + exec03UUID5Wire + `}]}}`
	got, err := materializePostQueryValue(json.RawMessage(wire))
	if err != nil {
		t.Fatalf("nested marker: %v", err)
	}
	outer, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("nested marker top level = %#v", got)
	}
	levelA, ok := outer["a"].(map[string]any)
	if !ok {
		t.Fatalf("nested marker level a = %#v", outer["a"])
	}
	items, ok := levelA["b"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("nested marker level b = %#v", levelA["b"])
	}
	item, ok := items[0].(map[string]any)
	if !ok || item["col"] != exec03UUID5Literal {
		t.Fatalf("nested marker value = %#v, want %s", items[0], exec03UUID5Literal)
	}
	// Hand-built rows can also carry a RawMessage inside a decoded array;
	// the array arm re-enters the JSONB case.
	got, err = materializePostQueryValue([]any{json.RawMessage(exec03UUID5Wire), "x"})
	if err != nil {
		t.Fatalf("RawMessage inside array: %v", err)
	}
	listItems, isList := got.([]any)
	if !isList || len(listItems) != 2 || listItems[0] != exec03UUID5Literal || listItems[1] != "x" {
		t.Fatalf("RawMessage inside array materialized to %#v", got)
	}
}

func TestMaterializePostQueryJSONBDynamicMetadataAndChecks(t *testing.T) {
	checks := map[string]map[string]DynamicColumnCheck{
		"code": {"a": {ColumnName: "code_a", ValueType: "string"}},
		"identifier_keys": {
			"count": {ColumnName: "identifier_count", ValueType: "integer"},
			"tags":  {ColumnName: "identifier_tags", ValueType: "string", Many: true},
		},
	}
	metadataWire := `{"code":["a"],"identifier_keys":["count","tags"]}`
	// The conforming PG-shaped row passes and publishes decoded Go values.
	row, err := materializePostQueryRowWithChecks(map[string]any{
		"code_a":                      json.RawMessage(`"value"`),
		"identifier_count":            json.RawMessage(`7`),
		"identifier_tags":             json.RawMessage(`["a","b"]`),
		"__loom_dynamic_runtime_keys": json.RawMessage(metadataWire),
	}, checks)
	if err != nil {
		t.Fatalf("conforming PG-shaped dynamic row refused: %v", err)
	}
	if row["code_a"] != "value" || row["identifier_count"] != float64(7) || !reflect.DeepEqual(row["identifier_tags"], []any{"a", "b"}) {
		t.Fatalf("public PG-shaped row = %v", row)
	}
	if _, ok := row["__loom_dynamic_runtime_keys"]; ok {
		t.Fatal("JSONB dynamic metadata leaked into the public row")
	}
	// Key drift carried as JSONB metadata fails before publishing.
	if _, err := materializePostQueryRowWithChecks(map[string]any{
		"code_a":                      json.RawMessage(`"value"`),
		"__loom_dynamic_runtime_keys": json.RawMessage(`{"code":["a","unexpected"]}`),
	}, checks); err == nil {
		t.Fatal("JSONB key drift accepted")
	} else {
		var drift *DynamicDriftError
		if !errors.As(err, &drift) || drift.Key != "unexpected" {
			t.Fatalf("JSONB key drift error = %v, want DynamicDriftError for unexpected", err)
		}
	}
	// Type drift carried as JSONB values fails before publishing; the
	// conforming sibling above is the alive control on the same checks.
	for name, row := range map[string]map[string]any{
		"string column with number":     {"code_a": json.RawMessage(`42`), "__loom_dynamic_runtime_keys": json.RawMessage(metadataWire)},
		"integer column with fraction":  {"identifier_count": json.RawMessage(`7.5`), "__loom_dynamic_runtime_keys": json.RawMessage(metadataWire)},
		"array column with item drift":  {"identifier_tags": json.RawMessage(`["a",42]`), "__loom_dynamic_runtime_keys": json.RawMessage(metadataWire)},
		"array column from bare string": {"identifier_tags": json.RawMessage(`"a"`), "__loom_dynamic_runtime_keys": json.RawMessage(metadataWire)},
	} {
		if _, err := materializePostQueryRowWithChecks(row, checks); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	// Malformed JSONB metadata is refused, not treated as absent.
	if _, err := materializePostQueryRowWithChecks(map[string]any{
		"code_a":                      json.RawMessage(`"value"`),
		"__loom_dynamic_runtime_keys": json.RawMessage(`{"code":"a"}`),
	}, checks); err == nil {
		t.Fatal("malformed JSONB metadata accepted as absent")
	}
	// Missing metadata with a non-AllowUnknownKeys family still refuses
	// (PG shape reaches the same guard as the driver shape).
	if _, err := materializePostQueryRowWithChecks(map[string]any{"code_a": json.RawMessage(`"value"`)}, checks); err == nil {
		t.Fatal("missing metadata accepted for a strict family")
	}
}
