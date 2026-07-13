package materialization

import (
	"context"
	"errors"
	"testing"

	dataframeruntime "github.com/calypr/loom/internal/dataframe/runtime"
	"github.com/calypr/loom/internal/store/clickhouse"
)

func TestInferColumnMapsPublishedDataframeValues(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"string", "x", "Nullable(String)"},
		{"integer", int64(1), "Nullable(Int64)"},
		{"float", 1.5, "Nullable(Float64)"},
		{"boolean", true, "Nullable(Bool)"},
		{"strings", []string{"a", "b"}, "Array(String)"},
		{"null", nil, "Nullable(String)"},
	}
	for _, tc := range cases {
		got, err := InferColumn(tc.name, tc.value)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got.ClickHouse != tc.want {
			t.Fatalf("%s: type = %q, want %q", tc.name, got.ClickHouse, tc.want)
		}
	}
}

func TestMemoryRegistryOnlyListsReadyRows(t *testing.T) {
	registry := NewMemoryRegistry()
	if err := registry.Save(nil, Materialization{ID: "pending", Project: "P", State: StatePending}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Save(nil, Materialization{ID: "ready", Project: "P", State: StateReady}); err != nil {
		t.Fatal(err)
	}
	rows, err := registry.ListReady(nil, "P")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "ready" {
		t.Fatalf("ready rows = %#v", rows)
	}
}

func TestPreflightValidatesExplicitSchema(t *testing.T) {
	service := &Service{}
	request := Request{
		Name:   "patients",
		Run:    dataframeruntime.RunRequest{Builder: dataframeruntime.Builder{Project: "P", RootResourceType: "Patient"}},
		Schema: []SchemaColumn{{Name: "patient_id", ClickHouse: "Nullable(String)"}, {Name: "score", ClickHouse: "Nullable(Float64)"}},
	}
	columns, err := service.Preflight(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != 2 || columns[1].ClickHouse != "Nullable(Float64)" {
		t.Fatalf("preflight schema = %#v", columns)
	}
	request.Schema[1].ClickHouse = "Map(String,String)"
	if _, err := service.Preflight(request); err == nil {
		t.Fatal("expected unsupported schema type error")
	}
}

func TestValidateValueAgainstSchema(t *testing.T) {
	if err := ValidateValue(Column{Name: "tags", ClickHouse: "Array(String)"}, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateValue(Column{Name: "score", ClickHouse: "Nullable(Float64)"}, "not-a-number"); err == nil {
		t.Fatal("expected type validation error")
	}
}

type fixtureStreamer struct {
	rows []map[string]any
}

func (f fixtureStreamer) Stream(_ context.Context, _ dataframeruntime.RunRequest, visit func(map[string]any) error) (dataframeruntime.StreamResult, error) {
	for _, row := range f.rows {
		if err := visit(row); err != nil {
			return dataframeruntime.StreamResult{}, err
		}
	}
	return dataframeruntime.StreamResult{RowCount: len(f.rows)}, nil
}

type fixtureClickHouse struct {
	created    []string
	dropped    []string
	inserted   []map[string]any
	failInsert bool
}

func (f *fixtureClickHouse) CreateTable(_ context.Context, table string, _ []clickhouse.Column) error {
	f.created = append(f.created, table)
	return nil
}

func (f *fixtureClickHouse) AddColumn(_ context.Context, _ string, _ clickhouse.Column) error {
	return nil
}

func (f *fixtureClickHouse) InsertRows(_ context.Context, _ string, _ []clickhouse.Column, rows []map[string]any) error {
	if f.failInsert {
		return errors.New("fixture insert failed")
	}
	f.inserted = append(f.inserted, rows...)
	return nil
}

func (f *fixtureClickHouse) DropTable(_ context.Context, table string) error {
	f.dropped = append(f.dropped, table)
	return nil
}

func fixtureRequest() Request {
	return Request{
		Name: "fixture",
		Run:  dataframeruntime.RunRequest{Builder: dataframeruntime.Builder{Project: "P", RootResourceType: "Patient"}},
		Schema: []SchemaColumn{
			{Name: "patient_id", ClickHouse: "Nullable(String)"},
			{Name: "score", ClickHouse: "Nullable(Float64)"},
			{Name: "active", ClickHouse: "Nullable(Bool)"},
			{Name: "tags", ClickHouse: "Array(String)"},
			{Name: "note", ClickHouse: "Nullable(String)"},
		},
	}
}

func TestMaterializeRepresentativeFixture(t *testing.T) {
	store := &fixtureClickHouse{}
	service := &Service{Dataframes: fixtureStreamer{rows: []map[string]any{
		{"patient_id": "p1", "score": 1.5, "active": true, "tags": []string{"a", "b"}, "note": nil},
		{"patient_id": "p2", "score": 2.5, "active": false, "tags": []string{}, "note": "ok"},
	}}, ClickHouse: store, Registry: NewMemoryRegistry(), BatchSize: 1}
	result, err := service.Materialize(context.Background(), fixtureRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.State != StateReady || result.RowCount != 2 || len(store.inserted) != 2 {
		t.Fatalf("materialization = %#v, inserted = %#v", result, store.inserted)
	}
	if len(store.dropped) != 0 {
		t.Fatalf("successful materialization dropped tables: %#v", store.dropped)
	}
}

func TestMaterializeExplicitSchemaAllowsEmptyResult(t *testing.T) {
	store := &fixtureClickHouse{}
	service := &Service{Dataframes: fixtureStreamer{}, ClickHouse: store, Registry: NewMemoryRegistry()}
	result, err := service.Materialize(context.Background(), fixtureRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.State != StateReady || result.RowCount != 0 || len(store.created) != 1 {
		t.Fatalf("empty materialization = %#v, created = %#v", result, store.created)
	}
}

func TestMaterializeFailureDropsCreatedTable(t *testing.T) {
	store := &fixtureClickHouse{failInsert: true}
	registry := NewMemoryRegistry()
	service := &Service{Dataframes: fixtureStreamer{rows: []map[string]any{{
		"patient_id": "p1", "score": 1.5, "active": true, "tags": []string{"a"}, "note": nil,
	}}}, ClickHouse: store, Registry: registry}
	_, err := service.Materialize(context.Background(), fixtureRequest())
	if err == nil {
		t.Fatal("expected insert failure")
	}
	if len(store.created) != 1 || len(store.dropped) != 1 || store.created[0] != store.dropped[0] {
		t.Fatalf("cleanup created=%#v dropped=%#v", store.created, store.dropped)
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	for _, materialization := range registry.byID {
		if materialization.State != StateFailed {
			t.Fatalf("failed materialization state = %s", materialization.State)
		}
		return
	}
	t.Fatal("materialization was not registered")
}

func TestMaterializeRejectsFixtureTypeMismatchAndCleansUp(t *testing.T) {
	store := &fixtureClickHouse{}
	service := &Service{Dataframes: fixtureStreamer{rows: []map[string]any{{
		"patient_id": "p1", "score": "wrong", "active": true, "tags": []string{"a"}, "note": nil,
	}}}, ClickHouse: store, Registry: NewMemoryRegistry()}
	if _, err := service.Materialize(context.Background(), fixtureRequest()); err == nil {
		t.Fatal("expected schema type mismatch")
	}
	if len(store.dropped) != 1 {
		t.Fatalf("type mismatch cleanup = %#v", store.dropped)
	}
}
