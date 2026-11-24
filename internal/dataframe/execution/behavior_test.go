package execution

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestOutputStreamDispatchesPinnedClickHouseCombine(t *testing.T) {
	plan := ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineAppend,
		Inputs: []ir.PhysicalCombineInputRef{
			{TableID: "table-left", RevisionID: "execution-left", OutputID: "left"},
			{TableID: "table-right", RevisionID: "execution-right", OutputID: "right"},
		},
		Projections: []ir.PhysicalCombineProjection{
			{OutputColumnID: "value", InputIndex: 0, InputColumnID: "left-value"},
			{OutputColumnID: "value", InputIndex: 1, InputColumnID: "right-value"},
		},
		Outputs: []ir.PhysicalCombineOutputColumn{{ID: "value", Name: "value", LogicalType: "string", ClickHouseType: "String"}},
	}
	inputs := []ir.ResolvedClickHouseTable{
		engineResolvedClickHouseInput("table-left", "execution-left", "left", "left_table", "left-value", "left_name"),
		engineResolvedClickHouseInput("table-right", "execution-right", "right", "right_table", "right-value", "right_name"),
	}
	var events []string
	var query string
	stream := OutputStream{
		Name: "combined", Columns: []string{"value"}, physicalEngine: ir.PhysicalEngineClickHouse,
		clickHouseCombine: &plan, project: "project-a", bindings: recipe.RuntimeBindings{Project: "project-a"},
		withExecutionReadPins: func(_ context.Context, ids []string, visit func(context.Context) error) error {
			if !reflect.DeepEqual(ids, []string{"execution-left", "execution-right"}) {
				t.Fatalf("pin IDs = %#v", ids)
			}
			events = append(events, "pinned")
			return visit(context.Background())
		},
		resolveClickHouseInputs: func(_ context.Context, _ ir.PhysicalClickHouseCombine, _ recipe.RuntimeBindings) ([]ir.ResolvedClickHouseTable, error) {
			if len(events) == 0 || events[len(events)-1] != "pinned" {
				t.Fatal("exact input metadata resolved before read pins were acquired")
			}
			events = append(events, "resolved")
			return inputs, nil
		},
		clickHouseQueryRows: func(_ context.Context, gotQuery string, columns []string, visit func(map[string]any) error, args ...any) error {
			query = gotQuery
			if !reflect.DeepEqual(columns, []string{"__loom_row_id", "value", "auth_resource_path"}) {
				t.Fatalf("ClickHouse result columns = %#v", columns)
			}
			if len(args) != 0 {
				t.Fatalf("unrestricted ClickHouse args = %#v", args)
			}
			events = append(events, "queried")
			return visit(map[string]any{"__loom_row_id": "stable-row", "value": "Ada", "auth_resource_path": ""})
		},
	}
	var got map[string]any
	result, err := stream.Stream(context.Background(), func(row map[string]any) error {
		got = row
		return nil
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if !reflect.DeepEqual(events, []string{"pinned", "resolved", "queried"}) {
		t.Fatalf("execution order = %#v", events)
	}
	if !strings.Contains(query, "UNION ALL") || result.RowCount != 1 || got["value"] != "Ada" || got["__loom_row_id"] != "stable-row" {
		t.Fatalf("stream result %#v row %#v query %q", result, got, query)
	}
}

func TestOutputStreamStopsBeforeQueryWhenRevisionPinFails(t *testing.T) {
	plan := ir.PhysicalClickHouseCombine{Inputs: []ir.PhysicalCombineInputRef{{RevisionID: "execution-a"}}}
	queryCalled := false
	stream := OutputStream{
		physicalEngine: ir.PhysicalEngineClickHouse, clickHouseCombine: &plan,
		withExecutionReadPins: func(context.Context, []string, func(context.Context) error) error { return errors.New("pin denied") },
		resolveClickHouseInputs: func(context.Context, ir.PhysicalClickHouseCombine, recipe.RuntimeBindings) ([]ir.ResolvedClickHouseTable, error) {
			t.Fatal("input resolution ran after pin failure")
			return nil, nil
		},
		clickHouseQueryRows: func(context.Context, string, []string, func(map[string]any) error, ...any) error {
			queryCalled = true
			return nil
		},
	}
	err := stream.streamRaw(context.Background(), func(map[string]any) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "pin denied") || queryCalled {
		t.Fatalf("streamRaw() error = %v, queryCalled=%v", err, queryCalled)
	}
}

func engineResolvedClickHouseInput(tableID, revisionID, outputID, physicalTable, columnID, columnName string) ir.ResolvedClickHouseTable {
	return ir.ResolvedClickHouseTable{
		TableID: tableID, RevisionID: revisionID, OutputID: outputID,
		Project: "project-a", DatasetGeneration: "generation-a", ReceiptID: "receipt-" + revisionID,
		SchemaDigest: "schema-" + revisionID, ScopeDigest: "scope-a", PhysicalTable: physicalTable,
		Unrestricted: true,
		Columns: []ir.ResolvedClickHouseColumn{
			{Name: "__loom_row_id", ClickHouseType: "String"},
			{ID: columnID, Name: columnName, LogicalType: "string", ClickHouseType: "String"},
		},
	}
}
