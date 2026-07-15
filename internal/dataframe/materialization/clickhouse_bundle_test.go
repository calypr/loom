package materialization

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/store/clickhouse"
)

type bundleCatalogFixture struct {
	executions map[string]BundleExecution
	pointers   map[string]BundlePointer
}

func newBundleCatalogFixture() *bundleCatalogFixture {
	return &bundleCatalogFixture{executions: map[string]BundleExecution{}, pointers: map[string]BundlePointer{}}
}
func (c *bundleCatalogFixture) SaveExecution(_ context.Context, e BundleExecution) error {
	c.executions[e.ID] = e
	return nil
}
func (c *bundleCatalogFixture) GetExecution(_ context.Context, id string) (BundleExecution, error) {
	e, ok := c.executions[id]
	if !ok {
		return BundleExecution{}, ErrBundleNotFound
	}
	return e, nil
}
func (c *bundleCatalogFixture) FindExecutionByKey(_ context.Context, key string) (BundleExecution, error) {
	for _, e := range c.executions {
		if e.Key == key {
			return e, nil
		}
	}
	return BundleExecution{}, ErrBundleNotFound
}
func (c *bundleCatalogFixture) GetPointer(_ context.Context, name string) (BundlePointer, error) {
	p, ok := c.pointers[name]
	if !ok {
		return BundlePointer{}, ErrBundleNotFound
	}
	return p, nil
}
func (c *bundleCatalogFixture) CompareAndSwapPointer(_ context.Context, name, expected, next string) error {
	p, ok := c.pointers[name]
	if ok && p.ExecutionID != expected {
		return ErrBundlePointerConflict
	}
	c.pointers[name] = BundlePointer{Name: name, ExecutionID: next}
	return nil
}
func (c *bundleCatalogFixture) ListExecutions(_ context.Context, state BundleState, before time.Time) ([]BundleExecution, error) {
	out := []BundleExecution{}
	for _, e := range c.executions {
		if e.State == state && e.UpdatedAt.Before(before) {
			out = append(out, e)
		}
	}
	return out, nil
}

type bundleClickHouseFixture struct {
	tables      map[string][]map[string]any
	failInsert  bool
	insertCalls int
	maxRows     int
}

func newBundleClickHouseFixture() *bundleClickHouseFixture {
	return &bundleClickHouseFixture{tables: map[string][]map[string]any{}}
}
func (c *bundleClickHouseFixture) CreateTable(_ context.Context, name string, _ []clickhouse.Column) error {
	c.tables[name] = nil
	return nil
}
func (c *bundleClickHouseFixture) InsertRows(_ context.Context, name string, _ []clickhouse.Column, rows []map[string]any) error {
	c.insertCalls++
	if len(rows) > c.maxRows {
		c.maxRows = len(rows)
	}
	if c.failInsert {
		return errors.New("insert failed")
	}
	c.tables[name] = append(c.tables[name], rows...)
	return nil
}

func (c *bundleClickHouseFixture) DropTable(_ context.Context, name string) error {
	delete(c.tables, name)
	return nil
}
func (c *bundleClickHouseFixture) QueryRows(_ context.Context, query string, _ []string) ([]map[string]any, error) {
	for table, rows := range c.tables {
		if strings.Contains(query, "`"+table+"`") {
			return rows, nil
		}
	}
	return nil, errors.New("table not found")
}

func TestClickHouseBundleStoreReconcilesStaleExecution(t *testing.T) {
	catalog := newBundleCatalogFixture()
	client := newBundleClickHouseFixture()
	store, _ := NewClickHouseBundleStore(client, catalog)
	tx, err := store.BeginBundleFor(context.Background(), BundleIdentity{Name: "stale"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.CreateOutput(context.Background(), "one", []clickhouse.Column{{Name: "id", Type: "String"}}); err != nil {
		t.Fatal(err)
	}
	id := ""
	for key, execution := range catalog.executions {
		id = key
		execution.UpdatedAt = time.Now().Add(-time.Hour)
		catalog.executions[key] = execution
	}
	if err := store.Reconcile(context.Background(), time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if catalog.executions[id].State != BundleFailed {
		t.Fatalf("stale execution not failed: %#v", catalog.executions[id])
	}
	if len(client.tables) != 0 {
		t.Fatalf("staging tables survived reconciliation: %#v", client.tables)
	}
}

func TestClickHouseBundleStoreRejectsDuplicateInFlightExecution(t *testing.T) {
	catalog := newBundleCatalogFixture()
	client := newBundleClickHouseFixture()
	store, _ := NewClickHouseBundleStore(client, catalog)
	identity := BundleIdentity{Name: "in-flight"}
	if _, err := store.BeginBundleFor(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginBundleFor(context.Background(), identity); !errors.Is(err, ErrBundleInFlight) {
		t.Fatalf("duplicate execution error = %v", err)
	}
}
