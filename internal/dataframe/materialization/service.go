package materialization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	dataframeruntime "github.com/calypr/loom/internal/dataframe/runtime"
	"github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
)

type Service struct {
	Dataframes DataframeStreamer
	ClickHouse ClickHouseStore
	Registry   Registry
	BatchSize  int
}

// DataframeStreamer and ClickHouseStore keep the materialization boundary
// testable without requiring ArangoDB or ClickHouse for every fixture test.
type DataframeStreamer interface {
	Stream(context.Context, dataframeruntime.RunRequest, func(map[string]any) error) (dataframeruntime.StreamResult, error)
}

type ClickHouseStore interface {
	CreateTable(context.Context, string, []clickhouse.Column) error
	AddColumn(context.Context, string, clickhouse.Column) error
	InsertRows(context.Context, string, []clickhouse.Column, []map[string]any) error
	DropTable(context.Context, string) error
}

type Request struct {
	Name   string
	Run    dataframeruntime.RunRequest
	Schema []SchemaColumn
}

// Preflight validates an explicit output schema before any ClickHouse table
// is created. An empty schema preserves the original inference behavior.
func (s *Service) Preflight(req Request) ([]Column, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("materialization name is required")
	}
	if req.Run.Builder.Project == "" || req.Run.Builder.RootResourceType == "" {
		return nil, fmt.Errorf("materialization dataframe project and root resource type are required")
	}
	if len(req.Schema) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(req.Schema))
	result := make([]Column, 0, len(req.Schema))
	for _, column := range req.Schema {
		if err := validateSchemaColumn(column); err != nil {
			return nil, err
		}
		if _, ok := seen[column.Name]; ok {
			return nil, fmt.Errorf("schema column %q is duplicated", column.Name)
		}
		seen[column.Name] = struct{}{}
		result = append(result, column)
	}
	return result, nil
}

func (s *Service) Materialize(ctx context.Context, req Request) (Materialization, error) {
	if s.Dataframes == nil || s.ClickHouse == nil || s.Registry == nil {
		return Materialization{}, fmt.Errorf("dataframe, ClickHouse, and registry dependencies are required")
	}
	explicitSchema, err := s.Preflight(req)
	if err != nil {
		return Materialization{}, err
	}
	batchSize := s.BatchSize
	if batchSize <= 0 {
		batchSize = 500
	}
	id := uuid.NewString()
	m := Materialization{
		ID: id, Name: req.Name, Project: req.Run.Builder.Project,
		DatasetGeneration: req.Run.Builder.DatasetGeneration,
		State:             StatePending, AuthScopeMode: req.Run.Builder.AuthScopeMode,
		AuthResourcePaths: append([]string(nil), req.Run.Builder.AuthResourcePaths...),
		PhysicalTable:     "loom_df_" + strings.ReplaceAll(id, "-", ""), CreatedAt: time.Now().UTC(),
	}
	if err := s.Registry.Save(ctx, m); err != nil {
		return Materialization{}, err
	}
	m.State = StateLoading
	if err := s.Registry.Save(ctx, m); err != nil {
		return Materialization{}, err
	}

	var columns []Column
	known := map[string]struct{}{}
	var batch []map[string]any
	var rowCount int64
	created := false
	flushWithColumns := func(physicalColumns []Column) error {
		if len(batch) == 0 {
			return nil
		}
		allColumns := append([]Column{{Name: "__loom_row_id", ClickHouse: "UInt64"}}, physicalColumns...)
		if err := s.ClickHouse.InsertRows(ctx, m.PhysicalTable, toClickHouseColumns(allColumns), batch); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}
	flush := func() error {
		return flushWithColumns(columns)
	}
	fail := func(err error) (Materialization, error) {
		if created {
			if cleanupErr := s.ClickHouse.DropTable(context.Background(), m.PhysicalTable); cleanupErr != nil {
				err = fmt.Errorf("%w (drop failed materialization table: %v)", err, cleanupErr)
			}
		}
		m.State, m.Error = StateFailed, err.Error()
		_ = s.Registry.Save(context.Background(), m)
		return Materialization{}, err
	}
	if len(explicitSchema) > 0 {
		columns = append(columns, explicitSchema...)
		for _, column := range columns {
			known[column.Name] = struct{}{}
		}
		all := append([]Column{{Name: "__loom_row_id", ClickHouse: "UInt64"}}, columns...)
		if err := s.ClickHouse.CreateTable(ctx, m.PhysicalTable, toClickHouseColumns(all)); err != nil {
			return fail(err)
		}
		created = true
	}
	streamResult, err := s.Dataframes.Stream(ctx, req.Run, func(row map[string]any) error {
		rowCount++
		row = cloneMap(row)
		row["__loom_row_id"] = rowCount
		newColumns := make([]Column, 0)
		for name, value := range row {
			if name == "__loom_row_id" {
				continue
			}
			if _, ok := known[name]; ok {
				if len(explicitSchema) > 0 {
					if column, ok := findColumn(columns, name); ok {
						if err := ValidateValue(column, value); err != nil {
							return err
						}
					}
				}
				continue
			}
			if len(explicitSchema) > 0 {
				return fmt.Errorf("dataframe emitted column %q not declared in schema", name)
			}
			column, err := InferColumn(name, value)
			if err != nil {
				return err
			}
			known[name] = struct{}{}
			columns = append(columns, column)
			newColumns = append(newColumns, column)
		}
		sort.Slice(newColumns, func(i, j int) bool { return newColumns[i].Name < newColumns[j].Name })
		if !created {
			all := append([]Column{{Name: "__loom_row_id", ClickHouse: "UInt64"}}, columns...)
			if err := s.ClickHouse.CreateTable(ctx, m.PhysicalTable, toClickHouseColumns(all)); err != nil {
				return err
			}
			created = true
		} else if len(newColumns) > 0 {
			previousColumns := columns[:len(columns)-len(newColumns)]
			if err := flushWithColumns(previousColumns); err != nil {
				return err
			}
			for _, column := range newColumns {
				if err := s.ClickHouse.AddColumn(ctx, m.PhysicalTable, clickhouse.Column{Name: column.Name, Type: column.ClickHouse}); err != nil {
					return err
				}
			}
		}
		batch = append(batch, row)
		if len(batch) >= batchSize {
			return flush()
		}
		return nil
	})
	if err != nil {
		return fail(err)
	}
	if rowCount == 0 && len(explicitSchema) == 0 {
		return fail(fmt.Errorf("cannot materialize an empty dataframe without an output schema"))
	}
	if err := flush(); err != nil {
		return fail(err)
	}
	if len(streamResult.Columns) > 0 {
		// The runtime's finalized order is useful metadata, but the physical
		// schema remains the deterministic order discovered above.
		_ = streamResult
	}
	m.Columns = append([]Column{{Name: "__loom_row_id", ClickHouse: "UInt64"}}, columns...)
	sort.Slice(m.Columns[1:], func(i, j int) bool { return m.Columns[i+1].Name < m.Columns[j+1].Name })
	m.RowCount = rowCount
	now := time.Now().UTC()
	m.ReadyAt, m.State = &now, StateReady
	if err := s.Registry.Save(ctx, m); err != nil {
		return fail(err)
	}
	return m, nil
}

func InferColumn(name string, value any) (Column, error) {
	if strings.TrimSpace(name) == "" || name == "__loom_row_id" {
		return Column{}, fmt.Errorf("invalid output column %q", name)
	}
	t := "Nullable(String)"
	switch value.(type) {
	case bool:
		t = "Nullable(Bool)"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
		t = "Nullable(Int64)"
	case float32, float64:
		t = "Nullable(Float64)"
	case []string:
		t = "Array(String)"
	case []int, []int8, []int16, []int32, []int64, []uint, []uint8, []uint16, []uint32, []uint64:
		t = "Array(Int64)"
	case []float32, []float64:
		t = "Array(Float64)"
	case []bool:
		t = "Array(Bool)"
	case []any:
		t = "Array(String)"
	case nil:
		// The logical type is unknown until a non-null value arrives. String is
		// the conservative ClickHouse type for an all-null output column.
	}
	return Column{Name: name, ClickHouse: t}, nil
}

func toClickHouseColumns(in []Column) []clickhouse.Column {
	out := make([]clickhouse.Column, 0, len(in))
	for _, column := range in {
		out = append(out, clickhouse.Column{Name: column.Name, Type: column.ClickHouse})
	}
	return out
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

// MemoryRegistry is useful for unit tests and local one-process experiments.
// Production wiring uses the Arango registry adapter.
type MemoryRegistry struct {
	mu   sync.RWMutex
	byID map[string]Materialization
}

func NewMemoryRegistry() *MemoryRegistry { return &MemoryRegistry{byID: map[string]Materialization{}} }
func (r *MemoryRegistry) Save(_ context.Context, m Materialization) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[m.ID] = m
	return nil
}
func (r *MemoryRegistry) Get(_ context.Context, id string) (Materialization, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.byID[id]
	if !ok {
		return Materialization{}, fmt.Errorf("materialization %q not found", id)
	}
	return m, nil
}
func (r *MemoryRegistry) ListReady(_ context.Context, project string) ([]Materialization, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []Materialization{}
	for _, m := range r.byID {
		if m.Project == project && m.State == StateReady {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func RecipeHash(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
