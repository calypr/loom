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

func TestPublishedOutputResolutionIsProjectAndGenerationScoped(t *testing.T) {
	catalog := newBundleCatalogFixture()
	identities := []BundleIdentity{
		{Name: "observation", Project: "project-a", DatasetGeneration: "generation-1"},
		{Name: "observation", Project: "project-b", DatasetGeneration: "generation-1"},
	}
	for index, identity := range identities {
		execution := BundleExecution{
			ID: "execution-" + string(rune('a'+index)), BundleIdentity: identity,
			State: BundleReady, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
			Outputs: []BundleOutputRecord{{Name: "observation", PhysicalTable: "loom_table_" + identity.Project, Columns: []Column{{Name: "id", ClickHouse: "String"}}, State: BundleReady}},
		}
		catalog.executions[execution.ID] = execution
		catalog.pointers[identity.PointerName()] = BundlePointer{Name: identity.PointerName(), ExecutionID: execution.ID}
	}
	first, err := ResolvePublishedOutput(context.Background(), catalog, "project-a", "generation-1", "observation")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ResolvePublishedOutput(context.Background(), catalog, "project-b", "generation-1", "observation")
	if err != nil {
		t.Fatal(err)
	}
	if first.PhysicalTable == second.PhysicalTable || first.Project == second.Project {
		t.Fatalf("project-scoped outputs collided: first=%#v second=%#v", first, second)
	}
}

func TestResolveFederatedDatasetUsesAuthorizedProjectSet(t *testing.T) {
	catalog := newBundleCatalogFixture()
	for index, project := range []string{"project-a", "project-b", "project-c"} {
		execution := BundleExecution{
			ID:             "federated-execution-" + string(rune('a'+index)),
			BundleIdentity: BundleIdentity{Name: "observation", Project: project, DatasetGeneration: "generation-1"},
			State:          BundleReady, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
			Outputs: []BundleOutputRecord{{Name: "observation", Alias: "observation", PhysicalTable: "loom_" + project, Columns: []Column{{Name: "auth_resource_path", ClickHouse: "Nullable(String)"}, {Name: "id", ClickHouse: "String"}}, State: BundleReady}},
		}
		catalog.executions[execution.ID] = execution
		catalog.pointers[execution.PointerName()] = BundlePointer{Name: execution.PointerName(), ExecutionID: execution.ID}
	}
	dataset, err := ResolveFederatedDataset(context.Background(), catalog, []string{"project-b", "project-a", "project-b"}, "observation")
	if err != nil {
		t.Fatal(err)
	}
	if len(dataset.Sources) != 2 || dataset.Sources[0].Project != "project-a" || dataset.Sources[1].Project != "project-b" {
		t.Fatalf("federated sources = %#v", dataset.Sources)
	}
	if len(dataset.Columns) != 2 || dataset.Columns[0].Name != "auth_resource_path" {
		t.Fatalf("federated columns = %#v", dataset.Columns)
	}
	if dataset.Revision == "" {
		t.Fatal("federated revision is empty")
	}
}

func TestResolveFederatedDatasetRejectsSchemaConflict(t *testing.T) {
	catalog := newBundleCatalogFixture()
	identities := []BundleIdentity{
		{Name: "observation", Project: "project-a", DatasetGeneration: "generation-1"},
		{Name: "observation", Project: "project-b", DatasetGeneration: "generation-1"},
	}
	for index, identity := range identities {
		tableType := "String"
		if index == 1 {
			tableType = "Int64"
		}
		execution := BundleExecution{ID: "conflict-" + string(rune('a'+index)), BundleIdentity: identity, State: BundleReady, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Outputs: []BundleOutputRecord{{Name: "observation", PhysicalTable: "loom_conflict_" + identity.Project, Columns: []Column{{Name: "id", ClickHouse: tableType}}, State: BundleReady}}}
		catalog.executions[execution.ID] = execution
		catalog.pointers[execution.PointerName()] = BundlePointer{Name: execution.PointerName(), ExecutionID: execution.ID}
	}
	if _, err := ResolveFederatedDataset(context.Background(), catalog, []string{"project-a", "project-b"}, "observation"); err == nil {
		t.Fatal("schema conflict unexpectedly resolved")
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
