package materialization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
)

var (
	ErrBundleInFlight     = errors.New("identical bundle execution is already in flight")
	ErrBundleAlreadyReady = errors.New("identical bundle execution is already ready")
)

// IdentityBundleStore is implemented by the production adapter. The older
// AtomicBundleStore interface remains available for fixtures and callers that
// do not yet have recipe provenance.
type IdentityBundleStore interface {
	AtomicBundleStore
	BeginBundleFor(context.Context, BundleIdentity) (AtomicBundleTx, error)
}

// ClickHouseBundleStore publishes staged tables by advancing a durable logical
// pointer in BundleCatalog. ClickHouse itself has no cross-table transaction;
// the pointer is therefore the visibility boundary.
type ClickHouseBundleStore struct {
	ClickHouse BundleClickHouseStore
	Catalog    BundleCatalog
	Prefix     string
	mu         sync.Mutex
}

type BundleClickHouseStore interface {
	CreateTable(context.Context, string, []clickhouse.Column) error
	InsertRows(context.Context, string, []clickhouse.Column, []map[string]any) error
	DropTable(context.Context, string) error
}

func NewClickHouseBundleStore(client BundleClickHouseStore, catalog BundleCatalog) (*ClickHouseBundleStore, error) {
	if client == nil || catalog == nil {
		return nil, fmt.Errorf("ClickHouse client and bundle catalog are required")
	}
	return &ClickHouseBundleStore{ClickHouse: client, Catalog: catalog, Prefix: "loom_bundle"}, nil
}

func (s *ClickHouseBundleStore) BeginBundle(ctx context.Context) (AtomicBundleTx, error) {
	identity := BundleIdentity{Name: "anonymous-" + uuid.NewString(), EngineVersion: "loom"}
	return s.BeginBundleFor(ctx, identity)
}

func (s *ClickHouseBundleStore) BeginBundleFor(ctx context.Context, identity BundleIdentity) (AtomicBundleTx, error) {
	if strings.TrimSpace(identity.Name) == "" {
		return nil, fmt.Errorf("bundle name is required")
	}
	if identity.EngineVersion == "" {
		identity.EngineVersion = "loom"
	}
	key := identity.Key()
	if existing, err := s.Catalog.FindExecutionByKey(ctx, key); err == nil {
		switch existing.State {
		case BundleReady:
			return &clickHouseBundleTx{store: s, execution: existing, idempotent: true}, nil
		case BundlePending, BundlePreflight, BundleLoading, BundleValidating:
			return nil, fmt.Errorf("%w: %s", ErrBundleInFlight, existing.ID)
		}
	} else if !errors.Is(err, ErrBundleNotFound) {
		return nil, err
	}
	id := uuid.NewString()
	now := time.Now().UTC()
	execution := BundleExecution{ID: id, Key: key, BundleIdentity: identity, State: BundlePending, CreatedAt: now, UpdatedAt: now}
	if err := s.Catalog.SaveExecution(ctx, execution); err != nil {
		return nil, err
	}
	execution.State = BundleLoading
	execution.UpdatedAt = time.Now().UTC()
	if err := s.Catalog.SaveExecution(ctx, execution); err != nil {
		return nil, err
	}
	return &clickHouseBundleTx{store: s, execution: execution, expectedPointer: s.pointer(ctx, identity.PointerName())}, nil
}

func (s *ClickHouseBundleStore) pointer(ctx context.Context, name string) string {
	p, err := s.Catalog.GetPointer(ctx, name)
	if err != nil {
		return ""
	}
	return p.ExecutionID
}

type clickHouseBundleTx struct {
	store           *ClickHouseBundleStore
	execution       BundleExecution
	expectedPointer string
	idempotent      bool
	closed          bool
}

func (t *clickHouseBundleTx) CreateOutput(ctx context.Context, name string, columns []clickhouse.Column) error {
	if t.idempotent {
		return nil
	}
	if t.closed {
		return fmt.Errorf("bundle transaction is closed")
	}
	if !validBundleOutput(name) {
		return fmt.Errorf("invalid bundle output name %q", name)
	}
	for _, output := range t.execution.Outputs {
		if output.Name == name {
			return fmt.Errorf("bundle output %q is duplicated", name)
		}
	}
	if len(columns) == 0 {
		return fmt.Errorf("bundle output %q has no columns", name)
	}
	columns = withRowIdentityColumn(columns)
	for _, c := range columns {
		if c.Name == "__loom_row_id" {
			continue
		}
		if err := validateSchemaColumn(Column{Name: c.Name, ClickHouse: c.Type}); err != nil {
			return err
		}
	}
	table := t.tableName(name)
	if err := t.store.ClickHouse.CreateTable(ctx, table, columns); err != nil {
		return err
	}
	converted := make([]Column, len(columns))
	for i, c := range columns {
		converted[i] = Column{Name: c.Name, ClickHouse: c.Type}
	}
	t.execution.Outputs = append(t.execution.Outputs, BundleOutputRecord{Name: name, PhysicalTable: table, Columns: converted, State: BundleLoading})
	return t.save(ctx)
}

func (t *clickHouseBundleTx) InsertRows(ctx context.Context, name string, columns []clickhouse.Column, rows []map[string]any) error {
	if t.idempotent || len(rows) == 0 {
		return nil
	}
	if t.closed {
		return fmt.Errorf("bundle transaction is closed")
	}
	idx := t.outputIndex(name)
	if idx < 0 {
		return fmt.Errorf("bundle output %q was not created", name)
	}
	record := &t.execution.Outputs[idx]
	effectiveColumns := columns
	if len(columns)+1 == len(record.Columns) && record.Columns[0].Name == "__loom_row_id" {
		effectiveColumns = make([]clickhouse.Column, 0, len(columns)+1)
		effectiveColumns = append(effectiveColumns, clickhouse.Column{Name: "__loom_row_id", Type: record.Columns[0].ClickHouse})
		effectiveColumns = append(effectiveColumns, columns...)
		rows = addGeneratedRowIdentities(rows, record.RowCount)
	}
	if len(effectiveColumns) != len(record.Columns) {
		return fmt.Errorf("output %q schema changed after preflight", name)
	}
	for i, c := range effectiveColumns {
		if record.Columns[i].Name != c.Name || record.Columns[i].ClickHouse != c.Type {
			return fmt.Errorf("output %q schema changed after preflight", name)
		}
	}
	if err := t.store.ClickHouse.InsertRows(ctx, record.PhysicalTable, effectiveColumns, rows); err != nil {
		return err
	}
	record.RowCount += int64(len(rows))
	for _, row := range rows {
		encoded, _ := json.Marshal(row)
		record.ByteCount += int64(len(encoded))
	}
	return t.save(ctx)
}

func withRowIdentityColumn(columns []clickhouse.Column) []clickhouse.Column {
	for _, column := range columns {
		if column.Name == "__loom_row_id" {
			return append([]clickhouse.Column(nil), columns...)
		}
	}
	result := make([]clickhouse.Column, 0, len(columns)+1)
	result = append(result, clickhouse.Column{Name: "__loom_row_id", Type: "String"})
	return append(result, columns...)
}

func addGeneratedRowIdentities(rows []map[string]any, start int64) []map[string]any {
	result := make([]map[string]any, len(rows))
	for index, row := range rows {
		copy := cloneBundleRow(row)
		if _, ok := copy["__loom_row_id"]; !ok {
			copy["__loom_row_id"] = fmt.Sprintf("%d", start+int64(index))
		}
		result[index] = copy
	}
	return result
}

func cloneBundleRow(row map[string]any) map[string]any {
	copy := make(map[string]any, len(row)+1)
	for key, value := range row {
		copy[key] = value
	}
	return copy
}

func (t *clickHouseBundleTx) Commit(ctx context.Context) error {
	if t.idempotent {
		t.closed = true
		return nil
	}
	if t.closed {
		return fmt.Errorf("bundle transaction is closed")
	}
	if len(t.execution.Outputs) == 0 {
		return t.fail(ctx, fmt.Errorf("bundle has no outputs"))
	}
	t.execution.State = BundleValidating
	if err := t.save(ctx); err != nil {
		return err
	}
	for i := range t.execution.Outputs {
		if t.execution.Outputs[i].RowCount < 0 {
			return t.fail(ctx, fmt.Errorf("output %q has invalid row count", t.execution.Outputs[i].Name))
		}
		t.execution.Outputs[i].State = BundleReady
	}
	if err := t.save(ctx); err != nil {
		return err
	}
	now := time.Now().UTC()
	t.execution.State, t.execution.ReadyAt, t.execution.UpdatedAt = BundleReady, &now, now
	if err := t.save(ctx); err != nil {
		return err
	}
	if err := t.store.Catalog.CompareAndSwapPointer(ctx, t.execution.PointerName(), t.expectedPointer, t.execution.ID); err != nil {
		return t.fail(ctx, fmt.Errorf("publish bundle pointer: %w", err))
	}
	t.closed = true
	return nil
}

func (t *clickHouseBundleTx) Rollback(ctx context.Context) error {
	if t.idempotent || t.closed {
		return nil
	}
	var first error
	for _, output := range t.execution.Outputs {
		if err := t.store.ClickHouse.DropTable(context.Background(), output.PhysicalTable); err != nil && first == nil {
			first = err
		}
	}
	if err := t.fail(ctx, fmt.Errorf("bundle rolled back")); err != nil && first == nil {
		first = err
	}
	t.closed = true
	return first
}

func (t *clickHouseBundleTx) outputIndex(name string) int {
	for i := range t.execution.Outputs {
		if t.execution.Outputs[i].Name == name {
			return i
		}
	}
	return -1
}

func (t *clickHouseBundleTx) tableName(output string) string {
	return t.store.Prefix + "_" + strings.ReplaceAll(t.execution.ID, "-", "") + "_" + output
}

func (t *clickHouseBundleTx) save(ctx context.Context) error {
	t.execution.UpdatedAt = time.Now().UTC()
	return t.store.Catalog.SaveExecution(ctx, t.execution)
}

func (t *clickHouseBundleTx) fail(ctx context.Context, err error) error {
	t.execution.State, t.execution.Error = BundleFailed, err.Error()
	_ = t.save(ctx)
	return err
}

var bundleOutputRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validBundleOutput(value string) bool { return bundleOutputRE.MatchString(value) }

// Reconcile marks abandoned executions failed and removes their staging
// tables. It is safe to call repeatedly during server startup; READY pointers
// are never touched.
func (s *ClickHouseBundleStore) Reconcile(ctx context.Context, olderThan time.Time) error {
	catalog, ok := s.Catalog.(StaleBundleCatalog)
	if !ok {
		return fmt.Errorf("bundle catalog does not support stale execution listing")
	}
	for _, state := range []BundleState{BundlePending, BundlePreflight, BundleLoading, BundleValidating} {
		executions, err := catalog.ListExecutions(ctx, state, olderThan)
		if err != nil {
			return err
		}
		for _, execution := range executions {
			for _, output := range execution.Outputs {
				if err := s.ClickHouse.DropTable(context.Background(), output.PhysicalTable); err != nil {
					return err
				}
			}
			execution.State = BundleFailed
			execution.Error = "stale execution reconciled"
			execution.UpdatedAt = time.Now().UTC()
			if err := s.Catalog.SaveExecution(ctx, execution); err != nil {
				return err
			}
		}
	}
	return nil
}
