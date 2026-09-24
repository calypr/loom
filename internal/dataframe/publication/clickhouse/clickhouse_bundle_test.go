package clickhouse

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
	dfpublished "github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/store/clickhouse"
)

type bundleCatalogFixture struct {
	executions map[string]publication.BundleExecution
	pointers   map[string]publication.BundlePointer
	pageCalls  int
	pageSizes  []int
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
	saveErrAfter         int
	saveCalls            int
	requireSaveContext   bool
	savedSnapshots       []publication.BundleExecution
	savedOwners          []string
	pointerErr           error
	publishErr           error
	publishCommitThenErr error
}

func newBundleCatalogFixture() *bundleCatalogFixture {
	return &bundleCatalogFixture{executions: map[string]publication.BundleExecution{}, pointers: map[string]publication.BundlePointer{}}
}
func (c *bundleCatalogFixture) SaveExecution(_ context.Context, e publication.BundleExecution, _ string) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	var snapshot publication.BundleExecution
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return err
	}
	c.executions[e.ID] = snapshot
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

func (c *leaseBundleCatalog) SaveExecution(ctx context.Context, e publication.BundleExecution, owner string) error {
	c.saveCalls++
	if c.requireSaveContext && ctx.Err() != nil {
		return ctx.Err()
	}
	if c.saveErrAfter > 0 && c.saveCalls == c.saveErrAfter {
		return c.saveErr
	}
	if c.saveErr != nil {
		return c.saveErr
	}
	c.savedSnapshots = append(c.savedSnapshots, e)
	c.savedOwners = append(c.savedOwners, owner)
	return c.bundleCatalogFixture.SaveExecution(ctx, e, owner)
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
func (c *bundleCatalogFixture) VisitExecutionPages(ctx context.Context, state publication.BundleState, before time.Time, pageSize int, visit publication.BundleExecutionPageFunc) error {
	if pageSize <= 0 {
		return errors.New("invalid execution page size")
	}
	executions, err := c.ListExecutions(ctx, state, before)
	if err != nil {
		return err
	}
	sort.Slice(executions, func(i, j int) bool {
		if executions[i].UpdatedAt.Equal(executions[j].UpdatedAt) {
			return executions[i].ID < executions[j].ID
		}
		return executions[i].UpdatedAt.Before(executions[j].UpdatedAt)
	})
	c.pageCalls++
	for start := 0; start < len(executions); start += pageSize {
		end := start + pageSize
		if end > len(executions) {
			end = len(executions)
		}
		page := executions[start:end]
		c.pageSizes = append(c.pageSizes, len(page))
		if err := visit(page); err != nil {
			return err
		}
	}
	return nil
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

type blockingFindCatalog struct {
	*bundleCatalogFixture
	blockedKey  string
	findStarted chan struct{}
	releaseFind chan struct{}
	findOnce    sync.Once
}

func (c *blockingFindCatalog) FindExecutionByKey(ctx context.Context, key string) (publication.BundleExecution, error) {
	if key == c.blockedKey {
		c.findOnce.Do(func() { close(c.findStarted) })
		select {
		case <-c.releaseFind:
		case <-ctx.Done():
			return publication.BundleExecution{}, ctx.Err()
		}
	}
	return c.bundleCatalogFixture.FindExecutionByKey(ctx, key)
}

type bundleClickHouseFixture struct {
	tables      map[string][]map[string]any
	lastColumns map[string][]clickhouse.Column
	failInsert  bool
	insertCalls int
	maxRows     int
	verifyErr   error
	dropErr     error
}

func TestObjectColumnsUseWholeDocumentStringStorage(t *testing.T) {
	columns, err := toColumns([]publication.LogicalColumn{
		{Name: "scalar", Kind: "object"},
		{Name: "repeated", Kind: "object", Repeated: true, Nullable: true},
		{Name: "ordinary", Kind: "string", Repeated: true, Nullable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []clickhouse.Column{
		{Name: "scalar", Type: "String"},
		{Name: "repeated", Type: "Nullable(String)"},
		{Name: "ordinary", Type: "Array(String)"},
	}
	if !reflect.DeepEqual(columns, want) {
		t.Fatalf("physical object columns = %#v, want %#v", columns, want)
	}
}

func TestEncodeObjectColumnsPreservesPresenceAndCallerRows(t *testing.T) {
	rows := []map[string]any{
		{"value": map[string]any{"unit": nil}},
		{"value": nil},
		{},
	}
	original := make([]map[string]any, len(rows))
	for index, row := range rows {
		original[index] = cloneBundleRow(row)
	}
	encoded, err := encodeObjectColumns([]publication.LogicalColumn{{Name: "value", Kind: "object", Nullable: true}}, rows)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rows, original) {
		t.Fatalf("caller rows mutated = %#v, want %#v", rows, original)
	}
	if encoded[0]["value"] != `{"unit":null}` {
		t.Fatalf("explicit null object = %#v", encoded[0]["value"])
	}
	if value, ok := encoded[1]["value"]; !ok || value != "null" {
		t.Fatalf("present nil object = %#v/%v, want JSON null text", value, ok)
	}
	if _, ok := encoded[2]["value"]; ok {
		t.Fatal("missing object key was fabricated")
	}
}

func TestBundleInsertEncodesObjectsBeforePhysicalInsert(t *testing.T) {
	catalog := newBundleCatalogFixture()
	client := newBundleClickHouseFixture()
	store, err := NewBundleStore(client, catalog)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.Begin(context.Background(), publication.PublicationIdentity{Name: "Observation"}, []publication.OutputSchema{{
		Name:    "Observation",
		Columns: []publication.LogicalColumn{{Name: "value", Kind: "object", Nullable: true}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	row := map[string]any{"value": map[string]any{"unit": nil}}
	if err := tx.WriteBatch(context.Background(), "Observation", []map[string]any{row}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	bundleTx := tx.(*clickHouseBundleTx)
	physical := client.tables[bundleTx.execution.Outputs[0].PhysicalTable]
	if len(physical) != 1 || physical[0]["value"] != `{"unit":null}` {
		t.Fatalf("physical object row = %#v, want normalized JSON text", physical)
	}
	if !reflect.DeepEqual(row, map[string]any{"value": map[string]any{"unit": nil}}) {
		t.Fatalf("caller row mutated = %#v", row)
	}
	columns := client.lastColumns[bundleTx.execution.Outputs[0].PhysicalTable]
	if len(columns) != 2 || columns[1].Type != "Nullable(String)" {
		t.Fatalf("physical object schema = %#v", columns)
	}
}

func newBundleClickHouseFixture() *bundleClickHouseFixture {
	return &bundleClickHouseFixture{tables: map[string][]map[string]any{}, lastColumns: map[string][]clickhouse.Column{}}
}
func (c *bundleClickHouseFixture) CreateTable(_ context.Context, name string, _ []clickhouse.Column) error {
	c.tables[name] = nil
	return nil
}
func (c *bundleClickHouseFixture) InsertRows(_ context.Context, name string, columns []clickhouse.Column, rows []map[string]any) error {
	c.insertCalls++
	c.lastColumns[name] = append([]clickhouse.Column(nil), columns...)
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
func (c *bundleClickHouseFixture) DropColumns(_ context.Context, name string, columns []string) error {
	rows, ok := c.tables[name]
	if !ok {
		return errors.New("table not found")
	}
	for _, row := range rows {
		for _, column := range columns {
			delete(row, column)
		}
	}
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

func TestClickHouseBundleStoreImplementsPublicationTargetDirectly(t *testing.T) {
	catalog := newBundleCatalogFixture()
	client := newBundleClickHouseFixture()
	store, err := NewBundleStore(client, catalog)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.Begin(context.Background(), publication.PublicationIdentity{Name: "Observation"}, []publication.OutputSchema{{
		Name:    "Observation",
		Columns: []publication.LogicalColumn{{Name: "id", Kind: "string"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.WriteBatch(context.Background(), "Observation", []map[string]any{{"id": "1"}}); err != nil {
		t.Fatal(err)
	}
	outputs, err := tx.Commit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 1 || outputs[0].Name != "Observation" {
		t.Fatalf("published outputs = %#v", outputs)
	}
	if len(client.lastColumns) != 1 {
		t.Fatalf("insert columns = %#v", client.lastColumns)
	}
	for _, columns := range client.lastColumns {
		if len(columns) != 2 || columns[0].Name != "__loom_row_id" || columns[1].Name != "id" {
			t.Fatalf("insert columns = %#v", columns)
		}
	}
}

func TestBundleEngineVersionRolloverCreatesNewExecutionAndRetryReusesIt(t *testing.T) {
	catalog := newBundleCatalogFixture()
	store, err := NewBundleStore(newBundleClickHouseFixture(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	oldIdentity := publication.BundleIdentity{Name: "recipe", Project: "project", DatasetGeneration: "generation", EngineVersion: "loom-recipe-v2"}
	newIdentity := oldIdentity
	newIdentity.EngineVersion = "loom-recipe-v3-object-fidelity"
	oldTx, err := store.beginBundle(context.Background(), oldIdentity)
	if err != nil {
		t.Fatal(err)
	}
	oldTx.execution.State = publication.BundlePublished
	if err := catalog.SaveExecution(context.Background(), oldTx.execution, oldTx.execution.OwnerID); err != nil {
		t.Fatal(err)
	}
	_ = oldTx.stopLease()

	newTx, err := store.beginBundle(context.Background(), newIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if newTx.idempotent || newTx.execution.Key == oldTx.execution.Key {
		t.Fatal("new object-fidelity engine reused the old execution key")
	}
	newTx.execution.State = publication.BundlePublished
	if err := catalog.SaveExecution(context.Background(), newTx.execution, newTx.execution.OwnerID); err != nil {
		t.Fatal(err)
	}
	_ = newTx.stopLease()

	retry, err := store.beginBundle(context.Background(), newIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if !retry.idempotent || retry.execution.ID != newTx.execution.ID {
		t.Fatalf("same-version retry = idempotent:%v execution:%q, want %q", retry.idempotent, retry.execution.ID, newTx.execution.ID)
	}
}

func TestClickHouseBundleStoreReportsInProgressExecution(t *testing.T) {
	catalog := newBundleCatalogFixture()
	store, err := NewBundleStore(newBundleClickHouseFixture(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	identity := publication.PublicationIdentity{Name: "Observation", Project: "project-a", DatasetGeneration: "generation-a"}
	bundleIdentity := publication.BundleIdentity{Name: identity.Name, Project: identity.Project, DatasetGeneration: identity.DatasetGeneration, EngineVersion: "loom"}
	catalog.executions["execution-a"] = publication.BundleExecution{ID: "execution-a", Key: bundleIdentity.Key(), BundleIdentity: bundleIdentity, State: publication.BundleRunning}

	_, err = store.Begin(context.Background(), identity, nil)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodePublicationInProgress) {
		t.Fatalf("Begin() error = %v, want PUBLICATION_IN_PROGRESS", err)
	}
	if got := userErr.Details()["executionId"]; got != "execution-a" {
		t.Fatalf("executionId = %v, want execution-a", got)
	}
}

func TestClickHouseBundleStoreReconcilesStaleExecution(t *testing.T) {
	catalog := newBundleCatalogFixture()
	client := newBundleClickHouseFixture()
	store, _ := NewBundleStore(client, catalog)
	tx, err := store.beginBundle(context.Background(), publication.BundleIdentity{Name: "stale"})
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

func TestClickHouseBundleStoreReconcileNeverDropsVisibleExecution(t *testing.T) {
	catalog := newBundleCatalogFixture()
	client := newBundleClickHouseFixture()
	store, _ := NewBundleStore(client, catalog)
	identity := publication.BundleIdentity{Project: "project-a", DatasetGeneration: "generation-1", Name: "Observation"}
	execution := publication.BundleExecution{
		ID: "published-but-stale", Key: identity.Key(), BundleIdentity: identity,
		State: publication.BundleRunning, UpdatedAt: time.Now().Add(-time.Hour),
		Outputs: []publication.BundleOutputRecord{{Name: "Observation", PhysicalTable: "loom_visible_observation", State: publication.BundleRunning}},
	}
	catalog.executions[execution.ID] = execution
	catalog.pointers[identity.PointerName()] = publication.BundlePointer{Name: identity.PointerName(), ExecutionID: execution.ID}
	client.tables[execution.Outputs[0].PhysicalTable] = []map[string]any{{"id": "1"}}

	if err := store.Reconcile(context.Background(), time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, ok := client.tables[execution.Outputs[0].PhysicalTable]; !ok {
		t.Fatal("reconciliation dropped a table referenced by the visible pointer")
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
	snapshot := catalog.executions[execution.ID]
	if snapshot.OwnerID != "" {
		t.Fatalf("reconciled execution retained lease owner %q", snapshot.OwnerID)
	}
	if len(catalog.savedOwners) == 0 || catalog.savedOwners[len(catalog.savedOwners)-1] == "" {
		t.Fatal("reconciled execution was not saved with a fence owner")
	}
}

func TestClickHouseBundleStoreReconcileProcessesBoundedPages(t *testing.T) {
	catalog := newBundleCatalogFixture()
	client := newBundleClickHouseFixture()
	store, _ := NewBundleStore(client, catalog)
	store.reconcilePageSize = 2
	olderThan := time.Now().Add(-time.Minute)
	for index := 0; index < 5; index++ {
		identity := publication.BundleIdentity{Name: "stale-" + string(rune('a'+index)), EngineVersion: "loom"}
		catalog.executions["stale-"+string(rune('a'+index))] = publication.BundleExecution{
			ID: "stale-" + string(rune('a'+index)), Key: identity.Key(), BundleIdentity: identity,
			State: publication.BundleRunning, UpdatedAt: olderThan.Add(-time.Duration(index+1) * time.Second),
		}
	}
	if err := store.Reconcile(context.Background(), olderThan); err != nil {
		t.Fatal(err)
	}
	if catalog.pageCalls == 0 {
		t.Fatal("reconciliation did not use paged execution scans")
	}
	for _, size := range catalog.pageSizes {
		if size > store.reconcilePageSize {
			t.Fatalf("reconciliation page size = %d, want at most %d", size, store.reconcilePageSize)
		}
	}
	for id, execution := range catalog.executions {
		if execution.State != publication.BundleFailed {
			t.Fatalf("execution %q state = %q, want failed", id, execution.State)
		}
	}
}

func TestPublishedOutputResolutionIsProjectAndGenerationScoped(t *testing.T) {
	catalog := newBundleCatalogFixture()
	identities := []publication.BundleIdentity{
		{Name: "observation", TranslationVersion: "legacy", Project: "project-a", DatasetGeneration: "generation-1"},
		{Name: "observation", TranslationVersion: "legacy", Project: "project-b", DatasetGeneration: "generation-1"},
	}
	verified := time.Now().UTC()
	for index, identity := range identities {
		execution := publication.BundleExecution{
			ID: "execution-" + string(rune('a'+index)), BundleIdentity: identity,
			State: publication.BundleReady, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
			Outputs: []publication.BundleOutputRecord{{Name: "Observation", PhysicalTable: "loom_table_" + identity.Project, Columns: []publication.PhysicalColumn{{Name: "id", ClickHouse: "String"}}, State: publication.BundleReady, VerifiedAt: &verified}},
		}
		catalog.executions[execution.ID] = execution
		catalog.pointers[identity.PointerName()] = publication.BundlePointer{Name: identity.PointerName(), ExecutionID: execution.ID}
	}
	reader := &dfpublished.Reader{Catalog: catalog}
	selector := dfpublished.DataframeSelector{Recipe: "observation", TranslationVersion: "legacy", Output: "Observation"}
	first, err := reader.CurrentProjectDataset(context.Background(), "project-a", selector)
	if err != nil {
		t.Fatal(err)
	}
	second, err := reader.CurrentProjectDataset(context.Background(), "project-b", selector)
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
	if _, err := store.beginBundle(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	if _, err := store.beginBundle(context.Background(), identity); !errors.Is(err, ErrBundleInFlight) {
		t.Fatalf("duplicate execution error = %v", err)
	}
}

func TestClickHouseBundleStoreSerializesBeginsPerBundleKey(t *testing.T) {
	catalog := &blockingFindCatalog{
		bundleCatalogFixture: newBundleCatalogFixture(),
		blockedKey:           publication.BundleIdentity{Name: "blocked-a", EngineVersion: "loom"}.Key(),
		findStarted:          make(chan struct{}),
		releaseFind:          make(chan struct{}),
	}
	store, _ := NewBundleStore(newBundleClickHouseFixture(), catalog)
	type beginResult struct {
		tx  publication.Transaction
		err error
	}
	firstDone := make(chan beginResult, 1)
	go func() {
		tx, err := store.Begin(context.Background(), publication.PublicationIdentity{Name: "blocked-a"}, nil)
		firstDone <- beginResult{tx: tx, err: err}
	}()
	select {
	case <-catalog.findStarted:
	case <-time.After(time.Second):
		t.Fatal("first begin did not reach the catalog")
	}

	secondDone := make(chan beginResult, 1)
	go func() {
		tx, err := store.Begin(context.Background(), publication.PublicationIdentity{Name: "parallel-b"}, nil)
		secondDone <- beginResult{tx: tx, err: err}
	}()
	var second beginResult
	select {
	case second = <-secondDone:
	case <-time.After(time.Second):
		close(catalog.releaseFind)
		<-firstDone
		t.Fatal("different bundle begin was blocked by another key")
	}
	if second.err != nil {
		close(catalog.releaseFind)
		<-firstDone
		t.Fatalf("different bundle begin failed: %v", second.err)
	}
	if second.tx != nil {
		_ = second.tx.Abort(context.Background(), errors.New("test cleanup"))
	}
	close(catalog.releaseFind)
	first := <-firstDone
	if first.err != nil {
		t.Fatalf("blocked bundle begin failed: %v", first.err)
	}
	if first.tx != nil {
		_ = first.tx.Abort(context.Background(), errors.New("test cleanup"))
	}
}

func TestClickHouseBundleStoreDoesNotSwallowPointerFailure(t *testing.T) {
	catalog := &leaseBundleCatalog{bundleCatalogFixture: newBundleCatalogFixture(), acquire: true, pointerErr: errors.New("pointer lookup failed")}
	store, _ := NewBundleStore(newBundleClickHouseFixture(), catalog)
	_, err := store.beginBundle(context.Background(), publication.BundleIdentity{Name: "pointer-failure"})
	if err == nil || !errors.Is(err, catalog.pointerErr) {
		t.Fatalf("beginBundle() error = %v", err)
	}
	if catalog.acquireCalls != 0 || catalog.releaseCalls != 0 {
		t.Fatalf("lease calls = acquire %d release %d, want no lease on pointer failure", catalog.acquireCalls, catalog.releaseCalls)
	}
}

func TestClickHouseBundleTransactionMetadataHonorsContext(t *testing.T) {
	catalog := &leaseBundleCatalog{bundleCatalogFixture: newBundleCatalogFixture(), acquire: true, requireSaveContext: true}
	store, _ := NewBundleStore(newBundleClickHouseFixture(), catalog)
	tx, err := store.beginBundle(context.Background(), publication.BundleIdentity{Name: "metadata-cancel"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.CreateOutput(context.Background(), "one", []clickhouse.Column{{Name: "id", Type: "String"}}); err != nil {
		t.Fatal(err)
	}
	savesBefore := len(catalog.savedSnapshots)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = tx.SetOutputMetadata(ctx, "one", []publication.LogicalColumn{{Name: "id", Kind: "string"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SetOutputMetadata() error = %v, want context cancellation", err)
	}
	if got := len(catalog.savedSnapshots); got != savesBefore {
		t.Fatalf("canceled metadata write saved %d snapshots, want %d", got, savesBefore)
	}
	_ = tx.Abort(context.Background(), err)
}

func TestClickHouseBundleTransactionDoesNotReplayUncertainInsert(t *testing.T) {
	catalog := &leaseBundleCatalog{bundleCatalogFixture: newBundleCatalogFixture(), acquire: true}
	client := newBundleClickHouseFixture()
	store, _ := NewBundleStore(client, catalog)
	tx, err := store.beginBundle(context.Background(), publication.BundleIdentity{Name: "uncertain-insert"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.CreateOutput(context.Background(), "one", []clickhouse.Column{{Name: "id", Type: "String"}}); err != nil {
		t.Fatal(err)
	}
	catalog.saveErr = errors.New("checkpoint failed after insert")
	catalog.saveErrAfter = catalog.saveCalls + 1
	row := map[string]any{"id": "1"}
	err = tx.InsertRows(context.Background(), "one", []clickhouse.Column{{Name: "id", Type: "String"}}, []map[string]any{row})
	if !errors.Is(err, ErrBundleCheckpointUncertain) {
		t.Fatalf("InsertRows() error = %v, want uncertain checkpoint", err)
	}
	output := tx.execution.Outputs[0]
	if got := len(client.tables[output.PhysicalTable]); got != 1 {
		t.Fatalf("accepted rows = %d, want one before cleanup", got)
	}
	if err := tx.InsertRows(context.Background(), "one", []clickhouse.Column{{Name: "id", Type: "String"}}, []map[string]any{row}); !errors.Is(err, ErrBundleCheckpointUncertain) {
		t.Fatalf("replayed InsertRows() error = %v, want uncertain checkpoint", err)
	}
	if client.insertCalls != 1 {
		t.Fatalf("ClickHouse insert calls = %d, want one", client.insertCalls)
	}
	catalog.saveErr = nil
	if abortErr := tx.Abort(context.Background(), err); !errors.Is(abortErr, err) {
		t.Fatalf("Abort() error = %v, want original uncertain checkpoint", abortErr)
	}
	if _, ok := client.tables[output.PhysicalTable]; ok {
		t.Fatal("uncertain staging table survived abort")
	}
	snapshot := catalog.executions[tx.execution.ID]
	if snapshot.State != publication.BundleFailed || len(snapshot.Outputs) != 1 || snapshot.Outputs[0].RowCount != 0 {
		t.Fatalf("failed execution snapshot = %#v, want failed zero-row metadata", snapshot)
	}
}

func TestClickHouseBundleStoreReleasesLeaseWhenInitialSaveFails(t *testing.T) {
	catalog := &leaseBundleCatalog{bundleCatalogFixture: newBundleCatalogFixture(), acquire: true, saveErr: errors.New("save failed")}
	store, _ := NewBundleStore(newBundleClickHouseFixture(), catalog)
	_, err := store.beginBundle(context.Background(), publication.BundleIdentity{Name: "save-failure"})
	if err == nil || !errors.Is(err, catalog.saveErr) {
		t.Fatalf("beginBundle() error = %v", err)
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
	tx, err := store.beginBundle(context.Background(), publication.BundleIdentity{Name: "lease-loss"})
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
	_ = tx.Abort(context.Background(), errors.New("lease lost"))
}

func TestClickHouseBundleTransactionContinuesAfterLeaseRenewal(t *testing.T) {
	catalog := &leaseBundleCatalog{bundleCatalogFixture: newBundleCatalogFixture(), acquire: true, renewResult: true, renewStarted: make(chan struct{})}
	store, _ := NewBundleStore(newBundleClickHouseFixture(), catalog)
	store.leaseRenewInterval = time.Millisecond
	tx, err := store.beginBundle(context.Background(), publication.BundleIdentity{Name: "lease-renewal"})
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
	_ = tx.Abort(context.Background(), errors.New("lease renewal failed"))
}

func TestClickHouseBundleLeaseRenewalStopsWithTransactionCleanup(t *testing.T) {
	catalog := &leaseBundleCatalog{bundleCatalogFixture: newBundleCatalogFixture(), acquire: true, renewBlock: true, renewStarted: make(chan struct{})}
	store, _ := NewBundleStore(newBundleClickHouseFixture(), catalog)
	store.leaseRenewInterval = time.Millisecond
	tx, err := store.beginBundle(context.Background(), publication.BundleIdentity{Name: "lease-cancel"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-catalog.renewStarted:
	case <-time.After(time.Second):
		t.Fatal("lease renewal did not start")
	}
	done := make(chan error, 1)
	go func() { done <- tx.Abort(context.Background(), errors.New("lease cancelled")) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("transaction cleanup did not cancel lease renewal")
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
	tx, err := store.beginBundle(context.Background(), identity)
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
	if _, err := tx.Commit(context.Background()); err != nil {
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

func TestClickHouseBundlePublishesAndReplaysQualityEvidenceAtomically(t *testing.T) {
	catalog := newBundleCatalogFixture()
	client := newBundleClickHouseFixture()
	store, err := NewBundleStore(client, catalog)
	if err != nil {
		t.Fatal(err)
	}
	identity := publication.BundleIdentity{
		Name: "recipe-a", Project: "project-a", DatasetGeneration: "g1",
		ReceiptID: "receipt-a", ScopeDigest: "scope-a",
	}
	tx, err := store.beginBundle(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.CreateOutput(context.Background(), "patients", []clickhouse.Column{{Name: "id", Type: "String"}}); err != nil {
		t.Fatal(err)
	}
	report := publication.QualityReport{
		ID: "quality-a", ReceiptID: identity.ReceiptID, Project: identity.Project,
		DatasetGeneration: identity.DatasetGeneration, ScopeDigest: identity.ScopeDigest,
		Output: "patients", PolicyVersion: "quality-v1",
		Completeness: publication.QualityComplete, Verdict: publication.QualityPassed,
	}
	if err := tx.SetQualityReports(context.Background(), []publication.QualityReport{report}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	retry, err := store.beginBundle(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	if !retry.Idempotent() {
		t.Fatal("published execution was not recognized as idempotent")
	}
	got := retry.ExistingQualityReports()
	if len(got) != 1 || got[0].ID != report.ID {
		t.Fatalf("replayed quality evidence = %#v", got)
	}
}

func TestClickHouseBundleRetainsBoundFailedQualityEvidenceWithoutPublishing(t *testing.T) {
	catalog := newBundleCatalogFixture()
	client := newBundleClickHouseFixture()
	store, err := NewBundleStore(client, catalog)
	if err != nil {
		t.Fatal(err)
	}
	identity := publication.BundleIdentity{Name: "recipe-a", Project: "project-a", DatasetGeneration: "g1", ReceiptID: "receipt-a", ScopeDigest: "scope-a"}
	tx, err := store.beginBundle(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.CreateOutput(context.Background(), "patients", []clickhouse.Column{{Name: "id", Type: "String"}}); err != nil {
		t.Fatal(err)
	}
	report := publication.QualityReport{
		ID: "quality-failed", ReceiptID: identity.ReceiptID, Project: identity.Project, DatasetGeneration: identity.DatasetGeneration,
		ScopeDigest: identity.ScopeDigest, Output: "patients", PolicyVersion: "quality-v1",
		Completeness: publication.QualityComplete, Verdict: publication.QualityFailed,
		KeyIntegrity: publication.KeyIntegrity{Distinct: 1, Duplicate: 1},
	}
	if err := tx.SetQualityReports(context.Background(), []publication.QualityReport{report}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Abort(context.Background(), publication.ErrQualityFailed); !errors.Is(err, publication.ErrQualityFailed) {
		t.Fatalf("abort error = %v", err)
	}
	execution, err := catalog.GetExecution(context.Background(), tx.execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	if execution.State != publication.BundleFailed || len(execution.QualityReports) != 1 || execution.QualityReports[0].ID != report.ID {
		t.Fatalf("failed execution evidence = %#v", execution)
	}
	if _, ok := catalog.pointers[execution.PointerName()]; ok {
		t.Fatal("failed quality candidate changed the publication pointer")
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
	tx, err := store.beginBundle(context.Background(), candidateIdentity)
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
