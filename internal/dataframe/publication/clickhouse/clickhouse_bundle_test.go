package clickhouse

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	dfpublished "github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/store/clickhouse"
)

type bundleCatalogFixture struct {
	executions map[string]publication.BundleExecution
	pointers   map[string]publication.BundlePointer
}

type leaseBundleCatalog struct {
	*bundleCatalogFixture
	acquire            bool
	acquireCalls       int
	onAcquire          func()
	releaseCalls       int
	releaseErr         error
	renewResult        bool
	renewErr           error
	renewBlock         bool
	renewStarted       chan struct{}
	saveErr            error
	requireSaveContext bool
	pointerErr         error
}

func newBundleCatalogFixture() *bundleCatalogFixture {
	return &bundleCatalogFixture{executions: map[string]publication.BundleExecution{}, pointers: map[string]publication.BundlePointer{}}
}
func (c *bundleCatalogFixture) SaveExecution(_ context.Context, e publication.BundleExecution) error {
	c.executions[e.ID] = e
	return nil
}
func (c *bundleCatalogFixture) GetExecution(_ context.Context, id string) (publication.BundleExecution, error) {
	e, ok := c.executions[id]
	if !ok {
		return publication.BundleExecution{}, publication.ErrBundleNotFound
	}
	return e, nil
}
func (c *bundleCatalogFixture) FindExecutionByKey(_ context.Context, key string) (publication.BundleExecution, error) {
	for _, e := range c.executions {
		if e.Key == key {
			return e, nil
		}
	}
	return publication.BundleExecution{}, publication.ErrBundleNotFound
}
func (c *bundleCatalogFixture) GetPointer(_ context.Context, name string) (publication.BundlePointer, error) {
	p, ok := c.pointers[name]
	if !ok {
		return publication.BundlePointer{}, publication.ErrBundleNotFound
	}
	return p, nil
}

func (c *leaseBundleCatalog) SaveExecution(ctx context.Context, e publication.BundleExecution) error {
	if c.requireSaveContext && ctx.Err() != nil {
		return ctx.Err()
	}
	if c.saveErr != nil {
		return c.saveErr
	}
	return c.bundleCatalogFixture.SaveExecution(ctx, e)
}
func (c *leaseBundleCatalog) GetPointer(ctx context.Context, name string) (publication.BundlePointer, error) {
	if c.pointerErr != nil {
		return publication.BundlePointer{}, c.pointerErr
	}
	return c.bundleCatalogFixture.GetPointer(ctx, name)
}
func (c *leaseBundleCatalog) AcquireBundleLease(context.Context, string, string, time.Time) (bool, error) {
	c.acquireCalls++
	if c.onAcquire != nil {
		c.onAcquire()
	}
	return c.acquire, nil
}

func (c *leaseBundleCatalog) RenewBundleLease(ctx context.Context, _ string, _ string, _ time.Time) (bool, error) {
	if c.renewStarted != nil {
		select {
		case <-c.renewStarted:
		default:
			close(c.renewStarted)
		}
	}
	if c.renewBlock {
		<-ctx.Done()
		return false, ctx.Err()
	}
	return c.renewResult, c.renewErr
}
func (c *leaseBundleCatalog) ReleaseBundleLease(context.Context, string, string) error {
	c.releaseCalls++
	return c.releaseErr
}
func (c *bundleCatalogFixture) CompareAndSwapPointer(_ context.Context, name, expected, next string) error {
	p, ok := c.pointers[name]
	if ok && p.ExecutionID != expected {
		return publication.ErrBundlePointerConflict
	}
	c.pointers[name] = publication.BundlePointer{Name: name, ExecutionID: next}
	return nil
}
func (c *bundleCatalogFixture) ListExecutions(_ context.Context, state publication.BundleState, before time.Time) ([]publication.BundleExecution, error) {
	out := []publication.BundleExecution{}
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
	store, _ := NewBundleStore(client, catalog)
	tx, err := store.BeginBundleFor(context.Background(), publication.BundleIdentity{Name: "stale"})
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
	if catalog.executions[id].State != publication.BundleFailed {
		t.Fatalf("stale execution not failed: %#v", catalog.executions[id])
	}
	if len(client.tables) != 0 {
		t.Fatalf("staging tables survived reconciliation: %#v", client.tables)
	}
}

func TestClickHouseBundleStoreReconcileFinishesClaimedCleanupAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	catalog := &leaseBundleCatalog{bundleCatalogFixture: newBundleCatalogFixture(), acquire: true, onAcquire: cancel, requireSaveContext: true}
	client := newBundleClickHouseFixture()
	store, _ := NewBundleStore(client, catalog)
	execution := publication.BundleExecution{ID: "stale", Key: "stale-key", BundleIdentity: publication.BundleIdentity{Name: "stale"}, State: publication.BundleLoading, UpdatedAt: time.Now().Add(-time.Hour), Outputs: []publication.BundleOutputRecord{{Name: "one", PhysicalTable: "loom_stale_one"}}}
	catalog.executions[execution.ID] = execution
	client.tables[execution.Outputs[0].PhysicalTable] = nil

	if err := store.Reconcile(ctx, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if got := catalog.executions[execution.ID].State; got != publication.BundleFailed {
		t.Fatalf("execution state = %q, want %q", got, publication.BundleFailed)
	}
	if _, ok := client.tables[execution.Outputs[0].PhysicalTable]; ok {
		t.Fatal("staging table survived reconciliation")
	}
}

func TestPublishedOutputResolutionIsProjectAndGenerationScoped(t *testing.T) {
	catalog := newBundleCatalogFixture()
	identities := []publication.BundleIdentity{
		{Name: "observation", Project: "project-a", DatasetGeneration: "generation-1"},
		{Name: "observation", Project: "project-b", DatasetGeneration: "generation-1"},
	}
	for index, identity := range identities {
		execution := publication.BundleExecution{
			ID: "execution-" + string(rune('a'+index)), BundleIdentity: identity,
			State: publication.BundleReady, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
			Outputs: []publication.BundleOutputRecord{{Name: "observation", PhysicalTable: "loom_table_" + identity.Project, Columns: []publication.PhysicalColumn{{Name: "id", ClickHouse: "String"}}, State: publication.BundleReady}},
		}
		catalog.executions[execution.ID] = execution
		catalog.pointers[identity.PointerName()] = publication.BundlePointer{Name: identity.PointerName(), ExecutionID: execution.ID}
	}
	first, err := dfpublished.ResolvePublishedOutput(context.Background(), catalog, "project-a", "generation-1", "observation")
	if err != nil {
		t.Fatal(err)
	}
	second, err := dfpublished.ResolvePublishedOutput(context.Background(), catalog, "project-b", "generation-1", "observation")
	if err != nil {
		t.Fatal(err)
	}
	if first.PhysicalTable == second.PhysicalTable || first.Project == second.Project {
		t.Fatalf("project-scoped outputs collided: first=%#v second=%#v", first, second)
	}
}

func TestClickHouseBundleStoreRejectsDuplicateInFlightExecution(t *testing.T) {
	catalog := newBundleCatalogFixture()
	client := newBundleClickHouseFixture()
	store, _ := NewBundleStore(client, catalog)
	identity := publication.BundleIdentity{Name: "in-flight"}
	if _, err := store.BeginBundleFor(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginBundleFor(context.Background(), identity); !errors.Is(err, ErrBundleInFlight) {
		t.Fatalf("duplicate execution error = %v", err)
	}
}

func TestClickHouseBundleStoreDoesNotSwallowPointerFailure(t *testing.T) {
	catalog := &leaseBundleCatalog{bundleCatalogFixture: newBundleCatalogFixture(), acquire: true, pointerErr: errors.New("pointer lookup failed")}
	store, _ := NewBundleStore(newBundleClickHouseFixture(), catalog)
	_, err := store.BeginBundleFor(context.Background(), publication.BundleIdentity{Name: "pointer-failure"})
	if err == nil || !errors.Is(err, catalog.pointerErr) {
		t.Fatalf("BeginBundleFor() error = %v", err)
	}
	if catalog.acquireCalls != 0 || catalog.releaseCalls != 0 {
		t.Fatalf("lease calls = acquire %d release %d, want no lease on pointer failure", catalog.acquireCalls, catalog.releaseCalls)
	}
}

func TestClickHouseBundleStoreReleasesLeaseWhenInitialSaveFails(t *testing.T) {
	catalog := &leaseBundleCatalog{bundleCatalogFixture: newBundleCatalogFixture(), acquire: true, saveErr: errors.New("save failed")}
	store, _ := NewBundleStore(newBundleClickHouseFixture(), catalog)
	_, err := store.BeginBundleFor(context.Background(), publication.BundleIdentity{Name: "save-failure"})
	if err == nil || !errors.Is(err, catalog.saveErr) {
		t.Fatalf("BeginBundleFor() error = %v", err)
	}
	if catalog.releaseCalls != 1 {
		t.Fatalf("lease release calls = %d, want 1", catalog.releaseCalls)
	}
}

func TestClickHouseBundleTransactionFailsAfterLeaseLoss(t *testing.T) {
	catalog := &leaseBundleCatalog{bundleCatalogFixture: newBundleCatalogFixture(), acquire: true}
	client := newBundleClickHouseFixture()
	store, _ := NewBundleStore(client, catalog)
	store.leaseRenewInterval = time.Millisecond
	tx, err := store.BeginBundleFor(context.Background(), publication.BundleIdentity{Name: "lease-loss"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.CreateOutput(context.Background(), "one", []clickhouse.Column{{Name: "id", Type: "String"}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := tx.InsertRows(context.Background(), "one", []clickhouse.Column{{Name: "id", Type: "String"}}, []map[string]any{{"id": "1"}}); !errors.Is(err, ErrBundleLeaseLost) {
		t.Fatalf("InsertRows() error = %v, want lease loss", err)
	}
	_ = tx.Rollback(context.Background())
}

func TestClickHouseBundleLeaseRenewalStopsWithTransactionCleanup(t *testing.T) {
	catalog := &leaseBundleCatalog{bundleCatalogFixture: newBundleCatalogFixture(), acquire: true, renewBlock: true, renewStarted: make(chan struct{})}
	store, _ := NewBundleStore(newBundleClickHouseFixture(), catalog)
	store.leaseRenewInterval = time.Millisecond
	tx, err := store.BeginBundleFor(context.Background(), publication.BundleIdentity{Name: "lease-cancel"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-catalog.renewStarted:
	case <-time.After(time.Second):
		t.Fatal("lease renewal did not start")
	}
	done := make(chan error, 1)
	go func() { done <- tx.Rollback(context.Background()) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("transaction cleanup did not cancel lease renewal")
	}
}
