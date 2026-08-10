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
	acquire              bool
	acquireCalls         int
	onAcquire            func()
	releaseCalls         int
	releaseErr           error
	renewResult          bool
	renewErr             error
	renewBlock           bool
	renewStarted         chan struct{}
	saveErr              error
	requireSaveContext   bool
	pointerErr           error
	publishErr           error
	publishCommitThenErr error
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
func (c *leaseBundleCatalog) PublishExecution(ctx context.Context, name, expected string, execution publication.BundleExecution) error {
	if c.publishCommitThenErr != nil {
		if err := c.bundleCatalogFixture.PublishExecution(ctx, name, expected, execution); err != nil {
			return err
		}
		return c.publishCommitThenErr
	}
	if c.publishErr != nil {
		return c.publishErr
	}
	return c.bundleCatalogFixture.PublishExecution(ctx, name, expected, execution)
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
func (c *bundleCatalogFixture) PublishExecution(ctx context.Context, name, expected string, execution publication.BundleExecution) error {
	if err := c.CompareAndSwapPointer(ctx, name, expected, execution.ID); err != nil {
		return err
	}
	c.executions[execution.ID] = execution
	return nil
}
func (c *bundleCatalogFixture) ListExecutions(_ context.Context, state publication.BundleState, before time.Time) ([]publication.BundleExecution, error) {
	out := []publication.BundleExecution{}
	for _, e := range c.executions {
		if e.State.Canonical() == state.Canonical() && e.UpdatedAt.Before(before) {
			out = append(out, e)
		}
	}
	return out, nil
}
func (c *bundleCatalogFixture) AcquireBundleLease(context.Context, string, string, time.Time) (bool, error) {
	return true, nil
}
func (c *bundleCatalogFixture) RenewBundleLease(context.Context, string, string, time.Time) (bool, error) {
	return true, nil
}
func (c *bundleCatalogFixture) ReleaseBundleLease(context.Context, string, string) error {
	return nil
}

type bundleClickHouseFixture struct {
	tables      map[string][]map[string]any
	failInsert  bool
	insertCalls int
	maxRows     int
	verifyErr   error
	dropErr     error
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
	if c.dropErr != nil {
		return c.dropErr
	}
	delete(c.tables, name)
	return nil
}
func (c *bundleClickHouseFixture) VerifyOutput(_ context.Context, name string, columns []clickhouse.Column, expectedRows int64) error {
	if c.verifyErr != nil {
		return c.verifyErr
	}
	rows, ok := c.tables[name]
	if !ok {
		return errors.New("table not found")
	}
	if int64(len(rows)) != expectedRows || len(columns) == 0 {
		return errors.New("verification mismatch")
	}
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
			Outputs: []publication.BundleOutputRecord{{Name: "Observation", PhysicalTable: "loom_table_" + identity.Project, Columns: []publication.PhysicalColumn{{Name: "id", ClickHouse: "String"}}, State: publication.BundleReady}},
		}
		catalog.executions[execution.ID] = execution
		catalog.pointers[identity.PointerName()] = publication.BundlePointer{Name: identity.PointerName(), ExecutionID: execution.ID}
	}
	reader := &dfpublished.Reader{Catalog: catalog, LegacyTranslationVersion: "legacy"}
	selector := dfpublished.DataframeSelector{Recipe: "observation", TranslationVersion: "legacy", Output: "Observation"}
	firstSources, err := reader.CurrentFederatedSources(context.Background(), []string{"project-a"}, selector)
	if err != nil {
		t.Fatal(err)
	}
	secondSources, err := reader.CurrentFederatedSources(context.Background(), []string{"project-b"}, selector)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstSources) != 1 || len(secondSources) != 1 {
		t.Fatalf("source counts = %d and %d, want 1 each", len(firstSources), len(secondSources))
	}
	first, second := firstSources[0], secondSources[0]
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

func TestClickHouseBundleTransactionContinuesAfterLeaseRenewal(t *testing.T) {
	catalog := &leaseBundleCatalog{bundleCatalogFixture: newBundleCatalogFixture(), acquire: true, renewResult: true, renewStarted: make(chan struct{})}
	store, _ := NewBundleStore(newBundleClickHouseFixture(), catalog)
	store.leaseRenewInterval = time.Millisecond
	tx, err := store.BeginBundleFor(context.Background(), publication.BundleIdentity{Name: "lease-renewal"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.CreateOutput(context.Background(), "one", []clickhouse.Column{{Name: "id", Type: "String"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-catalog.renewStarted:
	case <-time.After(time.Second):
		t.Fatal("lease renewal did not start")
	}
	time.Sleep(10 * time.Millisecond)
	done := make(chan error, 1)
	go func() {
		done <- tx.InsertRows(context.Background(), "one", []clickhouse.Column{{Name: "id", Type: "String"}}, []map[string]any{{"id": "1"}})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("transaction deadlocked after successful lease renewal")
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

func TestPlanReadyCleanupRetainsActiveAndPriorRelease(t *testing.T) {
	catalog := newBundleCatalogFixture()
	client := newBundleClickHouseFixture()
	store, err := NewBundleStore(client, catalog)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i, generation := range []string{"g1", "g2", "g3"} {
		readyAt := now.Add(-time.Duration(3-i) * time.Hour)
		execution := publication.BundleExecution{
			ID:             "execution-" + generation,
			BundleIdentity: publication.BundleIdentity{Project: "project-a", DatasetGeneration: generation, Name: "Observation"},
			State:          publication.BundleReady, ReadyAt: &readyAt, CreatedAt: readyAt, UpdatedAt: readyAt,
			Outputs: []publication.BundleOutputRecord{{Name: "Observation", PhysicalTable: "loom_bundle_" + generation + "_Observation", State: publication.BundleReady}},
		}
		catalog.executions[execution.ID] = execution
		client.tables[execution.Outputs[0].PhysicalTable] = nil
	}
	// Every generation has its own dataframe pointer. Only g3 is the active
	// project generation; g1/g2 pointers represent rollback metadata.
	for _, id := range []string{"execution-g1", "execution-g2", "execution-g3"} {
		execution := catalog.executions[id]
		catalog.pointers[execution.PointerName()] = publication.BundlePointer{Name: execution.PointerName(), ExecutionID: execution.ID}
	}

	plan, err := store.PlanReadyCleanupForActiveGenerations(context.Background(), map[string]string{"project-a": "g3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0].ExecutionID != "execution-g1" {
		t.Fatalf("cleanup plan = %#v, want only execution-g1", plan)
	}
	if plan[0].PhysicalTable != "loom_bundle_g1_Observation" {
		t.Fatalf("planned table = %q", plan[0].PhysicalTable)
	}
	if len(client.tables) != 3 {
		t.Fatalf("planner changed physical tables: %#v", client.tables)
	}
}

func TestPlanReadyCleanupNeverPlansReferencedRollbackOrForeignTable(t *testing.T) {
	catalog := newBundleCatalogFixture()
	store, err := NewBundleStore(newBundleClickHouseFixture(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	readyAt := time.Now().UTC().Add(-time.Hour)
	execution := publication.BundleExecution{
		ID: "rollback", BundleIdentity: publication.BundleIdentity{Project: "project-a", DatasetGeneration: "rollback", Name: "Observation"},
		State: publication.BundleReady, ReadyAt: &readyAt, CreatedAt: readyAt, UpdatedAt: readyAt,
		Outputs: []publication.BundleOutputRecord{
			{Name: "Observation", PhysicalTable: "loom_bundle_rollback_Observation", State: publication.BundleReady},
			{Name: "bad", PhysicalTable: "other_table", State: publication.BundleReady},
		},
	}
	catalog.executions[execution.ID] = execution
	catalog.pointers[execution.PointerName()] = publication.BundlePointer{Name: execution.PointerName(), ExecutionID: execution.ID}
	plan, err := store.PlanReadyCleanup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 0 {
		t.Fatalf("cleanup plan = %#v, referenced/foreign tables must be protected", plan)
	}
}

func TestClickHouseBundleCandidateIsInvisibleUntilReadyPointerCAS(t *testing.T) {
	catalog := newBundleCatalogFixture()
	client := newBundleClickHouseFixture()
	store, err := NewBundleStore(client, catalog)
	if err != nil {
		t.Fatal(err)
	}
	identity := publication.BundleIdentity{Project: "project-a", DatasetGeneration: "g1", Name: "Observation"}
	tx, err := store.BeginBundleFor(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.CreateOutput(context.Background(), "Observation", []clickhouse.Column{{Name: "id", Type: "String"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.GetPointer(context.Background(), identity.PointerName()); !errors.Is(err, publication.ErrBundleNotFound) {
		t.Fatalf("candidate became visible before commit: %v", err)
	}
	if err := tx.InsertRows(context.Background(), "Observation", []clickhouse.Column{{Name: "id", Type: "String"}}, []map[string]any{{"id": "1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.GetPointer(context.Background(), identity.PointerName()); !errors.Is(err, publication.ErrBundleNotFound) {
		t.Fatalf("candidate became visible before CAS: %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	pointer, err := catalog.GetPointer(context.Background(), identity.PointerName())
	if err != nil {
		t.Fatal(err)
	}
	if pointer.ExecutionID == "" {
		t.Fatal("successful CAS published an empty execution ID")
	}
	execution, err := catalog.GetExecution(context.Background(), pointer.ExecutionID)
	if err != nil || !execution.State.Successful() {
		t.Fatalf("published execution = %#v, err = %v", execution, err)
	}
}

func TestClickHouseBundleFailedCandidatePreservesOldPointer(t *testing.T) {
	catalog := newBundleCatalogFixture()
	client := newBundleClickHouseFixture()
	store, err := NewBundleStore(client, catalog)
	if err != nil {
		t.Fatal(err)
	}
	old := publication.BundleExecution{
		ID: "old", BundleIdentity: publication.BundleIdentity{Project: "project-a", DatasetGeneration: "g1", Name: "Observation"},
		State: publication.BundleReady, UpdatedAt: time.Now().UTC(),
		Outputs: []publication.BundleOutputRecord{{Name: "Observation", PhysicalTable: "loom_bundle_old_Observation", State: publication.BundleReady}},
	}
	catalog.executions[old.ID] = old
	catalog.pointers[old.PointerName()] = publication.BundlePointer{Name: old.PointerName(), ExecutionID: old.ID}
	client.tables[old.Outputs[0].PhysicalTable] = nil

	client.failInsert = true
	candidateIdentity := old.BundleIdentity
	candidateIdentity.RecipeDigest = "new-recipe"
	tx, err := store.BeginBundleFor(context.Background(), candidateIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.CreateOutput(context.Background(), "Observation", []clickhouse.Column{{Name: "id", Type: "String"}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.InsertRows(context.Background(), "Observation", []clickhouse.Column{{Name: "id", Type: "String"}}, []map[string]any{{"id": "1"}}); err == nil {
		t.Fatal("failed candidate insert unexpectedly succeeded")
	}
	_ = tx.Abort(context.Background(), errors.New("candidate failed"))
	pointer, err := catalog.GetPointer(context.Background(), old.PointerName())
	if err != nil {
		t.Fatal(err)
	}
	if pointer.ExecutionID != old.ID {
		t.Fatalf("failed candidate changed visible pointer to %q", pointer.ExecutionID)
	}
}
