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
	conflict   bool
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
	if c.conflict {
		return ErrBundlePointerConflict
	}
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

func TestPublishStreamBundleUsesBoundedBatches(t *testing.T) {
	catalog := newBundleCatalogFixture()
	client := newBundleClickHouseFixture()
	store, err := NewClickHouseBundleStore(client, catalog)
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{{"__loom_row_id": "1", "id": "a"}, {"__loom_row_id": "2", "id": "b"}, {"__loom_row_id": "3", "id": "c"}, {"__loom_row_id": "4", "id": "d"}, {"__loom_row_id": "5", "id": "e"}}
	err = PublishStreamBundle(context.Background(), store, BundleIdentity{Name: "streamed", Project: "p"}, []StreamOutput{{
		Name: "patients", Columns: []clickhouse.Column{{Name: "__loom_row_id", Type: "String"}, {Name: "id", Type: "String"}},
		Stream: func(_ context.Context, visit func(map[string]any) error) error {
			for _, row := range rows {
				if err := visit(row); err != nil {
					return err
				}
			}
			return nil
		},
	}}, StreamPublishConfig{BatchRows: 2, BatchBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if client.insertCalls != 3 || client.maxRows > 2 {
		t.Fatalf("unexpected bounded batches: calls=%d maxRows=%d", client.insertCalls, client.maxRows)
	}
	if len(client.tables) != 1 || len(client.tables["loom_bundle_"+strings.ReplaceAll(catalog.pointers["streamed"].ExecutionID, "-", "")+"_patients"]) != len(rows) {
		t.Fatalf("streamed rows were not fully published: %#v", client.tables)
	}
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

func TestClickHouseBundleStorePublishesAndIsIdempotent(t *testing.T) {
	catalog := newBundleCatalogFixture()
	client := newBundleClickHouseFixture()
	store, err := NewClickHouseBundleStore(client, catalog)
	if err != nil {
		t.Fatal(err)
	}
	identity := BundleIdentity{Name: "default", Project: "p", DatasetGeneration: "g", RecipeDigest: "r", SchemaDigest: "s"}
	outputs := []BundleOutput{{Name: "patients", Columns: []Column{{Name: "id", ClickHouse: "String"}}, Rows: []map[string]any{{"id": "1"}}}}
	if err := PublishBundleFor(context.Background(), store, identity, outputs); err != nil {
		t.Fatal(err)
	}
	pointer := catalog.pointers[identity.Name]
	if pointer.ExecutionID == "" {
		t.Fatal("bundle pointer was not published")
	}
	execution := catalog.executions[pointer.ExecutionID]
	if execution.State != BundleReady || execution.Outputs[0].State != BundleReady {
		t.Fatalf("execution not ready: %#v", execution)
	}
	reader := &BundleReader{ClickHouse: client, Catalog: catalog}
	rows, err := reader.Rows(context.Background(), identity.Name, "patients", []string{"id"})
	if err != nil || len(rows) != 1 || rows[0]["id"] != "1" {
		t.Fatalf("logical bundle read failed: rows=%#v err=%v", rows, err)
	}
	if err := PublishBundleFor(context.Background(), store, identity, outputs); err != nil {
		t.Fatalf("idempotent retry failed: %v", err)
	}
	if len(catalog.executions) != 1 {
		t.Fatalf("retry created another execution: %#v", catalog.executions)
	}
}

func TestClickHouseBundleStorePreservesPointerOnCommitFailure(t *testing.T) {
	catalog := newBundleCatalogFixture()
	client := newBundleClickHouseFixture()
	store, _ := NewClickHouseBundleStore(client, catalog)
	identity := BundleIdentity{Name: "default", Project: "p"}
	if err := PublishBundleFor(context.Background(), store, identity, []BundleOutput{{Name: "one", Columns: []Column{{Name: "id", ClickHouse: "String"}}, Rows: []map[string]any{{"id": "1"}}}}); err != nil {
		t.Fatal(err)
	}
	previous := catalog.pointers[identity.Name].ExecutionID
	identity.RecipeDigest = "new"
	catalog.conflict = true
	err := PublishBundleFor(context.Background(), store, identity, []BundleOutput{{Name: "one", Columns: []Column{{Name: "id", ClickHouse: "String"}}, Rows: []map[string]any{{"id": "2"}}}})
	if err == nil {
		t.Fatal("expected pointer conflict")
	}
	if got := catalog.pointers[identity.Name].ExecutionID; got != previous {
		t.Fatalf("previous pointer changed: %s", got)
	}
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
