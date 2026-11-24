package clickhouse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	publication "github.com/calypr/loom/internal/dataframe/publication"
	pubpublication "github.com/calypr/loom/internal/dataframe/publication"
	pubarango "github.com/calypr/loom/internal/dataframe/publication/arango"
	pubpublished "github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataset"
	storearango "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/store/clickhouse"
	storeclickhouse "github.com/calypr/loom/internal/store/clickhouse"
	"github.com/calypr/loom/internal/testkit/arangotest"
)

const (
	artifactExportHorizon = 24 * time.Hour

	productionFirstTickBound = 30 * time.Second
)

type ecwPinCall struct {
	executionID string
	owner       string
	expires     time.Time
}

type ecwCatalog struct {
	*pubarango.Registry
	mu       sync.Mutex
	acquires []ecwPinCall
	renews   []ecwPinCall
	releases []ecwPinCall
}

func (c *ecwCatalog) AcquireExecutionReadPin(ctx context.Context, executionID, owner string, expires time.Time) (bool, error) {
	c.mu.Lock()
	c.acquires = append(c.acquires, ecwPinCall{executionID: executionID, owner: owner, expires: expires})
	c.mu.Unlock()
	return c.Registry.AcquireExecutionReadPin(ctx, executionID, owner, expires)
}

func (c *ecwCatalog) RenewExecutionReadPin(ctx context.Context, executionID, owner string, expires time.Time) (bool, error) {
	c.mu.Lock()
	c.renews = append(c.renews, ecwPinCall{executionID: executionID, owner: owner, expires: expires})
	c.mu.Unlock()
	renewed, err := c.Registry.RenewExecutionReadPin(ctx, executionID, owner, expires)
	return renewed, err
}

func (c *ecwCatalog) ReleaseExecutionReadPin(ctx context.Context, executionID, owner string) error {
	c.mu.Lock()
	c.releases = append(c.releases, ecwPinCall{executionID: executionID, owner: owner})
	c.mu.Unlock()
	return c.Registry.ReleaseExecutionReadPin(ctx, executionID, owner)
}

func (c *ecwCatalog) snapshotAcquires() []ecwPinCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ecwPinCall(nil), c.acquires...)
}

func (c *ecwCatalog) snapshotRenews() []ecwPinCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ecwPinCall(nil), c.renews...)
}

func (c *ecwCatalog) snapshotReleases() []ecwPinCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ecwPinCall(nil), c.releases...)
}

type ecwQueryer struct {
	started chan struct{}
	hold    chan struct{}
	start   sync.Once
	mu      sync.Mutex
	queries []string
}

func (q *ecwQueryer) QueryRowsArgs(context.Context, string, []string, ...any) ([]map[string]any, error) {
	return nil, errors.New("ecw queryer: unexpected paged query from exact export stream")
}

func (q *ecwQueryer) QueryRowsArgsVisit(ctx context.Context, query string, _ []string, visit func(map[string]any) error, _ ...any) error {
	q.start.Do(func() { close(q.started) })
	q.mu.Lock()
	q.queries = append(q.queries, query)
	q.mu.Unlock()
	select {
	case <-q.hold:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_ = visit
	return nil
}

func (q *ecwQueryer) snapshotQueries() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]string(nil), q.queries...)
}

type ecwClickHouse struct {
	mu      sync.Mutex
	dropped []string
}

func (c *ecwClickHouse) CreateTable(context.Context, string, []clickhouse.Column) error { return nil }

func (c *ecwClickHouse) InsertRows(context.Context, string, []clickhouse.Column, []map[string]any) error {
	return nil
}

func (c *ecwClickHouse) VerifyOutput(context.Context, string, []clickhouse.Column, int64) error {
	return nil
}

func (c *ecwClickHouse) DropColumns(context.Context, string, []string) error { return nil }

func (c *ecwClickHouse) DropTable(ctx context.Context, table string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dropped = append(c.dropped, table)
	return nil
}

func (c *ecwClickHouse) droppedTables() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.dropped...)
}

func ecwDropped(set []string, table string) bool {
	for _, name := range set {
		if name == table {
			return true
		}
	}
	return false
}

func ecwSetup(t *testing.T) (*storearango.Client, *pubarango.Registry, context.Context, string) {
	t.Helper()
	database := arangotest.Owned(t, "loom_verify_ecw_")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	client := database.Client
	if err := client.Bootstrap(ctx, pubarango.BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	registry, err := pubarango.New(client)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	return client, registry, ctx, database.Name[len("loom_verify_ecw_"):]
}

func ecwExecution(tag, suffix, physicalTable string, updatedAt time.Time, published bool) pubpublication.BundleExecution {
	identity := pubpublication.BundleIdentity{
		Name: "ecw-" + tag, TranslationVersion: "1", OutputName: "main",
		Project: "loom_verify", DatasetGeneration: "ecw-" + tag + "-" + suffix,
		RecipeDigest: "r-" + tag + "-" + suffix, SchemaDigest: "s-" + tag + "-" + suffix,
		ReceiptID:   "rc-" + tag + "-" + suffix,
		ScopeDigest: "sc-" + tag + "-" + suffix, EngineVersion: "1",
	}
	verifiedAt := updatedAt.Add(-time.Hour)
	state := pubpublication.BundleFailed
	if published {
		state = pubpublication.BundlePublished
	}
	return pubpublication.BundleExecution{
		ID: "ecw-" + tag + "-" + suffix, Key: identity.Key(), BundleIdentity: identity,
		State: state,
		Outputs: []pubpublication.BundleOutputRecord{{
			Name: "main", PhysicalTable: physicalTable, RowCount: 5,
			State: pubpublication.BundlePublished, VerifiedAt: &verifiedAt,
			Columns: []pubpublication.PhysicalColumn{{Name: "value", ClickHouse: "Int64"}},
		}},
		CreatedAt: updatedAt,
		UpdatedAt: updatedAt,
	}
}

func ecwUpsertWithLease(t *testing.T, registry *pubarango.Registry, ctx context.Context, exec pubpublication.BundleExecution, leaseOwner string) {
	t.Helper()
	acquired, err := registry.AcquireBundleLease(ctx, exec.Key, leaseOwner, time.Now().UTC().Add(2*time.Minute))
	if err != nil || !acquired {
		t.Fatalf("seed acquire lease %s: acquired=%v err=%v", exec.ID, acquired, err)
	}
	exec.OwnerID = leaseOwner
	if err := registry.SaveExecution(ctx, exec, leaseOwner); err != nil {
		t.Fatalf("seed save %s: %v", exec.ID, err)
	}
	if err := registry.ReleaseBundleLease(ctx, exec.Key, leaseOwner); err != nil {
		t.Fatalf("seed release %s: %v", exec.ID, err)
	}
}

type ecwRowQuerier interface {
	QueryRows(ctx context.Context, query string, batchSize int, bindVars map[string]interface{}, visit storearango.RowVisitor) error
}

func ecwStateKey(executionID string) string {
	sum := sha256.Sum256([]byte("state\x00" + executionID))
	return "state_" + hex.EncodeToString(sum[:])
}

type ecwPinRow struct {
	Found    bool     `json:"found"`
	Active   int      `json:"active"`
	Far      int      `json:"far"`
	Owners   []string `json:"owners"`
	Expiries []string `json:"expiries"`
}

func ecwReadPins(t *testing.T, querier ecwRowQuerier, executionID string, now, horizon time.Time) ecwPinRow {
	t.Helper()
	var row ecwPinRow
	err := querier.QueryRows(context.Background(), `LET state = DOCUMENT(@@collection, @stateKey)
LET pins = state == null ? [] : NOT_NULL(state.pins, [])
LET active = LENGTH(FOR pin IN pins FILTER pin.expiresAt >= @now RETURN pin)
LET far = LENGTH(FOR pin IN pins FILTER pin.expiresAt >= @horizon RETURN pin)
RETURN {found: state != null, active: active, far: far, owners: (FOR pin IN pins RETURN pin.ownerId), expiries: (FOR pin IN pins RETURN TO_STRING(pin.expiresAt))}`,
		1, map[string]interface{}{
			"@collection": pubarango.ExecutionReadPinsCollection,
			"stateKey":    ecwStateKey(executionID),
			"now":         now,
			"horizon":     horizon,
		}, func(raw map[string]any) error {
			data, err := json.Marshal(raw)
			if err != nil {
				return err
			}
			return json.Unmarshal(data, &row)
		})
	if err != nil {
		t.Fatalf("direct AQL read of %s for %s: %v", pubarango.ExecutionReadPinsCollection, executionID, err)
	}
	return row
}

func ecwHasOwner(owners []string, owner string) bool {
	for _, name := range owners {
		if name == owner {
			return true
		}
	}
	return false
}

func ecwStartExactExport(t *testing.T, registry *pubarango.Registry, ctx context.Context, exec pubpublication.BundleExecution, readerID string, horizon, renewalInterval time.Duration) (*ecwCatalog, *ecwQueryer, context.CancelFunc, <-chan error, time.Time) {
	t.Helper()
	catalog := &ecwCatalog{Registry: registry}
	queryer := &ecwQueryer{started: make(chan struct{}), hold: make(chan struct{})}
	reader := &pubpublished.Reader{Catalog: catalog, ClickHouse: queryer}
	pinExpiresAt := time.Now().UTC().Add(horizon)

	request := pubpublished.ExactExportRequest{
		ExecutionID:        exec.ID,
		OutputID:           "main",
		Project:            exec.Project,
		DatasetGeneration:  exec.DatasetGeneration,
		ReceiptID:          exec.ReceiptID,
		SchemaDigest:       exec.SchemaDigest,
		Columns:            []string{"value"},
		Unrestricted:       true,
		IncludeRowIdentity: false,
		ReaderID:           readerID,
		PinExpiresAt:       pinExpiresAt,
		PinRenewalInterval: renewalInterval,
	}
	exportCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := reader.StreamExactExport(exportCtx, request, func(map[string]any) error { return nil })
		done <- err
	}()
	select {
	case <-queryer.started:
	case err := <-done:
		t.Fatalf("precondition: StreamExactExport returned before reaching its scan: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatalf("precondition: StreamExactExport never reached its ClickHouse scan visit")
	}
	return catalog, queryer, cancel, done, pinExpiresAt
}

func ecwFindAcquire(t *testing.T, catalog *ecwCatalog, executionID, readerID string) ecwPinCall {
	t.Helper()
	for _, call := range catalog.snapshotAcquires() {
		if call.executionID == executionID && call.owner == readerID {
			return call
		}
	}
	t.Fatalf("production StreamExactExport never issued its AcquireExecutionReadPin for %s/%s: %#v", executionID, readerID, catalog.snapshotAcquires())
	return ecwPinCall{}
}

func TestExportCrashInsideFirstTickLeavesCrashWindowPinAgainstArango(t *testing.T) {
	client, registry, ctx, suffix := ecwSetup(t)

	base := time.Now().UTC()
	xTable := "loom_bundle_ecw_x_" + suffix
	yTable := "loom_bundle_ecw_y_" + suffix
	x := ecwExecution("X", suffix, xTable, base.Add(-70*time.Minute), true)
	ecwUpsertWithLease(t, registry, ctx, x, "ecw-seed")

	const readerID = "artifact-export-crash-window"

	catalog, queryer, cancelExport, _, pinExpiresAt := ecwStartExactExport(t, registry, ctx, x, readerID, artifactExportHorizon, 10*time.Minute)
	defer cancelExport()

	time.Sleep(250 * time.Millisecond)

	acquire := ecwFindAcquire(t, catalog, x.ID, readerID)
	if acquire.expires.Before(pinExpiresAt.Add(-time.Minute)) || acquire.expires.After(pinExpiresAt.Add(time.Minute)) {
		t.Fatalf("acquire expiry %v not the artifact-passed caller TTL %v", acquire.expires, pinExpiresAt)
	}

	early := ecwReadPins(t, client, x.ID, time.Now().UTC(), time.Now().UTC().Add(23*time.Hour))
	if !early.Found || early.Active < 1 || early.Far < 1 || !ecwHasOwner(early.Owners, readerID) {
		t.Fatalf("crash-window pin not observed on %s for %s/%s: %+v", pubarango.ExecutionReadPinsCollection, x.ID, readerID, early)
	}
	t.Logf("crash-window pin observed by direct AQL: active=%d far(>=23h)=%d owners=%v expiries=%v", early.Active, early.Far, early.Owners, early.Expiries)

	if renews := catalog.snapshotRenews(); len(renews) != 0 {
		t.Fatalf("renewal ticker fired inside the simulated crash window (%v): not a pre-first-tick crash", renews)
	}
	if releases := catalog.snapshotReleases(); len(releases) != 0 {
		t.Fatalf("release executed while the export is still in flight (%v): crash not modeled", releases)
	}
	if queries := queryer.snapshotQueries(); len(queries) == 0 || !strings.Contains(queries[0], xTable) {
		t.Fatalf("precondition: parked stream never scanned the exported physical table %q: %v", xTable, queries)
	}

	staleX := x
	staleX.State = pubpublication.BundleFailed
	staleX.UpdatedAt = base.Add(-70 * time.Minute)
	ecwUpsertWithLease(t, registry, ctx, staleX, "ecw-failed")
	y := ecwExecution("Y", suffix, yTable, base.Add(-40*time.Minute), false)
	ecwUpsertWithLease(t, registry, ctx, y, "ecw-seed")

	ch := &ecwClickHouse{}
	store, err := NewBundleStore(ch, registry)
	if err != nil {
		t.Fatalf("NewBundleStore: %v", err)
	}
	cutoff := time.Now().UTC().Add(-2 * time.Minute)
	reconcileErr := store.Reconcile(ctx, cutoff)

	dropped := ch.droppedTables()
	_, xErr := registry.GetExecution(ctx, x.ID)
	_, yErr := registry.GetExecution(ctx, y.ID)
	if xErr != nil || yErr != nil {
		t.Fatalf("precondition: rows unreadable after reconcile: X=%v Y=%v", xErr, yErr)
	}
	after := ecwReadPins(t, client, x.ID, time.Now().UTC(), time.Now().UTC().Add(23*time.Hour))
	if !after.Found || after.Active < 1 || after.Far < 1 || !ecwHasOwner(after.Owners, readerID) {
		t.Fatalf("crash-window pin vanished across Reconcile (expected it to survive to its 24h horizon): %+v", after)
	}

	xReclaimed := ecwDropped(dropped, xTable)
	yReclaimed := ecwDropped(dropped, yTable)

	if reconcileErr != nil {
		t.Errorf("Reconcile must skip pinned X and complete the remaining sweep without error; got %v (dropped=%v)", reconcileErr, dropped)
	}
	if xReclaimed {
		t.Errorf("Reconcile reclaimed crash-pinned execution X despite its live 24h read pin (dropped=%v)", dropped)
	}
	if !yReclaimed {
		t.Errorf("Reconcile did not reclaim unrelated stale execution Y while X was pinned (dropped=%v)", dropped)
	}
}

func TestExportCrashWithoutCrashRenewedPinExpiresFast(t *testing.T) {
	client, registry, ctx, suffix := ecwSetup(t)

	base := time.Now().UTC()
	xTable := "loom_bundle_ecw_x_" + suffix
	yTable := "loom_bundle_ecw_y_" + suffix
	x := ecwExecution("X", suffix, xTable, base.Add(-70*time.Minute), true)
	ecwUpsertWithLease(t, registry, ctx, x, "ecw-seed")

	const readerID = "artifact-export-renewed"

	catalog, queryer, cancelExport, exportDone, pinExpiresAt := ecwStartExactExport(t, registry, ctx, x, readerID, artifactExportHorizon, 10*time.Millisecond)
	defer cancelExport()

	acquire := ecwFindAcquire(t, catalog, x.ID, readerID)
	if acquire.expires.Before(pinExpiresAt.Add(-time.Minute)) {
		t.Fatalf("control precondition: caller TTL %v not honored at acquire: %v", pinExpiresAt, acquire.expires)
	}

	deadline := time.Now().Add(30 * time.Second)
	var pulled ecwPinRow
	for {
		pulled = ecwReadPins(t, client, x.ID, time.Now().UTC(), time.Now().UTC().Add(23*time.Hour))
		if pulled.Found && pulled.Active >= 1 && pulled.Far == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("CONTROL FAILED: renewal ticker never pulled the caller's 24h pin down to ~defaultReadPinTTL while in flight: %+v", pulled)
		}
		select {
		case err := <-exportDone:
			t.Fatalf("CONTROL FAILED: export ended before renewal pulled the pin: %v (pin=%+v)", err, pulled)
		default:
		}
		time.Sleep(25 * time.Millisecond)
	}
	renews := catalog.snapshotRenews()
	if len(renews) == 0 {
		t.Fatalf("CONTROL FAILED: direct AQL shows the pulled-down horizon but the wrapper recorded zero RenewExecutionReadPin calls: harness split-brain")
	}
	lastRenew := renews[len(renews)-1]
	now := time.Now().UTC()
	if lastRenew.expires.Before(now.Add(time.Minute)) || lastRenew.expires.After(now.Add(4*time.Minute)) {
		t.Fatalf("CONTROL FAILED: ticker renewal expiry %v is not ~defaultReadPinTTL(2min) from now (%v)", lastRenew.expires, now)
	}
	t.Logf("renewal realism observed: %d ticker renewals, latest expiresAt=%v (caller TTL was %v); AQL pin now active=%d far(>=23h)=%d owners=%v expiries=%v",
		len(renews), lastRenew.expires.UTC().Format(time.RFC3339), pinExpiresAt.UTC().Format(time.RFC3339), pulled.Active, pulled.Far, pulled.Owners, pulled.Expiries)

	close(queryer.hold)
	select {
	case err := <-exportDone:
		if err != nil {
			t.Fatalf("CONTROL FAILED: un-crashed StreamExactExport returned error: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("CONTROL FAILED: StreamExactExport did not return after hold release")
	}
	if releases := catalog.snapshotReleases(); len(releases) != 1 || releases[0].owner != readerID {
		t.Fatalf("CONTROL FAILED: graceful export must release its pin exactly once, got %v", releases)
	}
	deadline = time.Now().Add(30 * time.Second)
	var released ecwPinRow
	for {
		released = ecwReadPins(t, client, x.ID, time.Now().UTC(), time.Now().UTC().Add(23*time.Hour))
		if !released.Found || released.Active == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("CONTROL FAILED: pin still active after the graceful release: %+v", released)
		}
		time.Sleep(25 * time.Millisecond)
	}

	staleX := x
	staleX.State = pubpublication.BundleFailed
	staleX.UpdatedAt = base.Add(-70 * time.Minute)
	ecwUpsertWithLease(t, registry, ctx, staleX, "ecw-failed")
	y := ecwExecution("Y", suffix, yTable, base.Add(-40*time.Minute), false)
	ecwUpsertWithLease(t, registry, ctx, y, "ecw-seed")

	ch := &ecwClickHouse{}
	store, err := NewBundleStore(ch, registry)
	if err != nil {
		t.Fatalf("NewBundleStore: %v", err)
	}
	if err := store.Reconcile(ctx, time.Now().UTC().Add(-2*time.Minute)); err != nil {
		t.Fatalf("CONTROL FAILED: production Reconcile with a renewed-then-released pin must succeed, got: %v (dropped=%v)", err, ch.droppedTables())
	}
	dropped := ch.droppedTables()
	if !ecwDropped(dropped, xTable) {
		t.Fatalf("CONTROL FAILED: stale X table %q not reclaimed (dropped=%v)", xTable, dropped)
	}
	if !ecwDropped(dropped, yTable) {
		t.Fatalf("CONTROL FAILED: stale Y table %q not reclaimed (dropped=%v)", yTable, dropped)
	}
	xRow, xErr := registry.GetExecution(ctx, x.ID)
	yRow, yErr := registry.GetExecution(ctx, y.ID)
	if xErr != nil || yErr != nil || xRow.Error == "" || yRow.Error == "" {
		t.Fatalf("CONTROL FAILED: reconcile markers missing: X=%+v err=%v Y=%+v err=%v", xRow, xErr, yRow, yErr)
	}
	t.Logf("CONTROL OK: one real ticker tick pulled the artifact 24h horizon to %v and the completed export released it (release calls=%d); the identical production Reconcile then reclaimed BOTH stale executions (dropped=%v)", lastRenew.expires.UTC().Format(time.RFC3339), len(catalog.snapshotReleases()), dropped)
}

func TestToColumnsStoresLogicalObjectsAsLosslessJSONText(t *testing.T) {
	got, err := toColumns([]publication.LogicalColumn{
		{Name: "profile", Kind: "object", Nullable: true},
		{Name: "owners", Kind: "object", Repeated: true},
		{Name: "label", Kind: "string"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []storeclickhouse.Column{
		{Name: "profile", Type: "Nullable(String)", LogicalType: "object"},
		{Name: "owners", Type: "Array(Nullable(String))", LogicalType: "object"},
		{Name: "label", Type: "String"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("physical columns = %#v, want %#v", got, want)
	}
}

type reconcilePinClaimCatalog struct {
	*reconcileRecheckCatalog
	releaseErr error
}

func (c *reconcilePinClaimCatalog) ReleaseBundleLease(context.Context, string, string) error {
	c.released++
	return c.releaseErr
}

func (*reconcilePinClaimCatalog) AcquireExecutionReadPin(context.Context, string, string, time.Time) (bool, error) {
	return false, nil
}

func (*reconcilePinClaimCatalog) RenewExecutionReadPin(context.Context, string, string, time.Time) (bool, error) {
	return false, nil
}

func (*reconcilePinClaimCatalog) ReleaseExecutionReadPin(context.Context, string, string) error {
	return nil
}

func (*reconcilePinClaimCatalog) ClaimExecutionCleanup(context.Context, string, string) (bool, error) {
	return false, nil
}

func (*reconcilePinClaimCatalog) RenewExecutionCleanup(context.Context, string, string, time.Time) (bool, error) {
	return false, nil
}

func (*reconcilePinClaimCatalog) ReleaseExecutionCleanup(context.Context, string, string) error {
	return nil
}

type reconcileRecheckCatalog struct {
	execution     publication.BundleExecution
	publishedExec *publication.BundleExecution
	pointers      []publication.BundlePointer
	pointerErrs   []error
	pointerCalls  int
	released      int
	acquiredKeys  []string
	savedStates   []publication.BundleState
	savedErrors   []string
}

func (c *reconcileRecheckCatalog) SaveExecution(_ context.Context, execution publication.BundleExecution, _ string) error {
	c.savedStates = append(c.savedStates, execution.State)
	c.savedErrors = append(c.savedErrors, execution.Error)
	return nil
}

func (c *reconcileRecheckCatalog) GetExecution(context.Context, string) (publication.BundleExecution, error) {
	return publication.BundleExecution{}, publication.ErrBundleNotFound
}

func (c *reconcileRecheckCatalog) FindExecutionByKey(context.Context, string) (publication.BundleExecution, error) {
	return publication.BundleExecution{}, publication.ErrBundleNotFound
}

func (c *reconcileRecheckCatalog) GetPointer(context.Context, string) (publication.BundlePointer, error) {
	index := c.pointerCalls
	c.pointerCalls++
	if index >= len(c.pointers) {
		index = len(c.pointers) - 1
	}
	return c.pointers[index], c.pointerErrs[index]
}

func (c *reconcileRecheckCatalog) CompareAndSwapPointer(context.Context, string, string, string) error {
	return nil
}

func (c *reconcileRecheckCatalog) PublishExecution(context.Context, string, string, publication.BundleExecution) error {
	return nil
}

func (c *reconcileRecheckCatalog) ListExecutions(context.Context, publication.BundleState, time.Time) ([]publication.BundleExecution, error) {
	return nil, nil
}

func (c *reconcileRecheckCatalog) AcquireBundleLease(_ context.Context, key, _ string, _ time.Time) (bool, error) {
	c.acquiredKeys = append(c.acquiredKeys, key)
	return true, nil
}

func (c *reconcileRecheckCatalog) RenewBundleLease(context.Context, string, string, time.Time) (bool, error) {
	return true, nil
}

func (c *reconcileRecheckCatalog) ReleaseBundleLease(_ context.Context, _ string, _ string) error {
	c.released++
	return nil
}

func (c *reconcileRecheckCatalog) VisitExecutionPages(_ context.Context, state publication.BundleState, _ time.Time, _ int, visit publication.BundleExecutionPageFunc) error {
	if state == publication.BundlePublished {
		if c.publishedExec == nil {
			return nil
		}
		return visit([]publication.BundleExecution{*c.publishedExec})
	}
	if state != publication.BundleRunning || c.execution.ID == "" {
		return nil
	}
	return visit([]publication.BundleExecution{c.execution})
}

func TestReconcileSkipsPinnedCandidateButSurfacesBundleLeaseReleaseFailure(t *testing.T) {
	releaseFailure := errors.New("bundle lease release failed")
	for _, test := range []struct {
		name       string
		releaseErr error
		wantErr    error
	}{
		{name: "pinned candidate is skipped"},
		{name: "release failure remains visible", releaseErr: releaseFailure, wantErr: releaseFailure},
	} {
		t.Run(test.name, func(t *testing.T) {
			execution := publication.BundleExecution{
				ID: "exec-pinned-reconcile", Key: "bundle:exec-pinned-reconcile",
				BundleIdentity: publication.BundleIdentity{Project: "reconcile-pin", Name: "orders", DatasetGeneration: "gen", OutputName: "main"},
				State:          publication.BundleRunning,
				Outputs:        []publication.BundleOutputRecord{{Name: "main", PhysicalTable: "loom_bundle_exec_pinned_reconcile"}},
			}
			catalog := &reconcilePinClaimCatalog{
				reconcileRecheckCatalog: &reconcileRecheckCatalog{
					execution:   execution,
					pointers:    []publication.BundlePointer{{}},
					pointerErrs: []error{publication.ErrBundleNotFound},
				},
				releaseErr: test.releaseErr,
			}
			store, err := NewBundleStore(&reconcileRecheckClickHouse{}, catalog)
			if err != nil {
				t.Fatalf("NewBundleStore: %v", err)
			}
			err = store.Reconcile(context.Background(), time.Now().UTC().Add(time.Hour))
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Reconcile error = %v, want %v", err, test.wantErr)
			}
			if catalog.released != 1 {
				t.Fatalf("ReleaseBundleLease calls = %d, want 1", catalog.released)
			}
		})
	}
}

type reconcileRecheckClickHouse struct {
	dropped []string
}

func (f *reconcileRecheckClickHouse) CreateTable(context.Context, string, []storeclickhouse.Column) error {
	return nil
}

func (f *reconcileRecheckClickHouse) InsertRows(context.Context, string, []storeclickhouse.Column, []map[string]any) error {
	return nil
}

func (f *reconcileRecheckClickHouse) VerifyOutput(context.Context, string, []storeclickhouse.Column, int64) error {
	return nil
}

func (f *reconcileRecheckClickHouse) DropTable(_ context.Context, table string) error {
	f.dropped = append(f.dropped, table)
	return nil
}

func (f *reconcileRecheckClickHouse) DropColumns(context.Context, string, []string) error {
	return nil
}

type reconcileRecheckReleaseReader struct {
	active dataset.ActiveRelease
	err    error
	calls  int
}

func (r *reconcileRecheckReleaseReader) ReadActiveRelease(context.Context, string) (dataset.ActiveRelease, error) {
	r.calls++
	return r.active, r.err
}

func TestReconcileDeniesDropOfActiveReleaseBoundExecution(t *testing.T) {
	bind := func(executionID string) dataset.ActiveRelease {
		return dataset.ActiveRelease{
			Release:  dataset.ProjectRelease{Publications: []dataset.ReleasePublication{{ExecutionID: executionID}}},
			Revision: 1,
		}
	}
	tests := []struct {
		name        string
		reader      *reconcileRecheckReleaseReader
		wireReader  bool
		latePointer *publication.BundlePointer
		wantDropped int
		wantSaved   bool
		wantError   bool
	}{
		{
			name:        "active release binds the candidate -> drop vetoed",
			reader:      &reconcileRecheckReleaseReader{active: bind("exec-reconcile-recheck")},
			wireReader:  true,
			wantDropped: 0,
		},
		{
			name:        "release binds only other executions -> drop proceeds",
			reader:      &reconcileRecheckReleaseReader{active: bind("exec-someone-else")},
			wireReader:  true,
			wantDropped: 1,
			wantSaved:   true,
		},
		{
			name:        "late pointer re-read names the candidate -> drop vetoed",
			reader:      &reconcileRecheckReleaseReader{active: bind("exec-someone-else")},
			wireReader:  true,
			latePointer: &publication.BundlePointer{ExecutionID: "exec-reconcile-recheck"},
			wantDropped: 0,
		},
		{
			name:        "active release read fails -> drop vetoed fail-closed with error",
			reader:      &reconcileRecheckReleaseReader{err: errors.New("release store down")},
			wireReader:  true,
			wantDropped: 0,
			wantError:   true,
		},
		{
			name:        "no active release -> drop proceeds",
			reader:      &reconcileRecheckReleaseReader{err: dataset.ErrNoActiveRelease},
			wireReader:  true,
			wantDropped: 1,
			wantSaved:   true,
		},
		{
			name:        "unwired reader keeps historical sweep behavior",
			wireReader:  false,
			wantDropped: 1,
			wantSaved:   true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			execution := publication.BundleExecution{
				ID:  "exec-reconcile-recheck",
				Key: "bundle:exec-reconcile-recheck",
				BundleIdentity: publication.BundleIdentity{
					Project: "reconcile-recheck", Name: "orders", DatasetGeneration: "gen", OutputName: "main",
				},
				State: publication.BundleRunning,
				Outputs: []publication.BundleOutputRecord{
					{Name: "main", PhysicalTable: "loom_bundle_exec_reconcile_recheck"},
				},
			}
			catalog := &reconcileRecheckCatalog{
				execution:   execution,
				pointers:    []publication.BundlePointer{{}, {}},
				pointerErrs: []error{publication.ErrBundleNotFound, publication.ErrBundleNotFound},
			}
			clickhouseClient := &reconcileRecheckClickHouse{}
			store, err := NewBundleStore(clickhouseClient, catalog)
			if err != nil {
				t.Fatalf("NewBundleStore: %v", err)
			}
			if test.wireReader {
				store.SetActiveReleaseReader(test.reader)
			}
			if test.latePointer != nil {
				catalog.pointers[1] = *test.latePointer
				catalog.pointerErrs[1] = nil
			}
			err = store.Reconcile(context.Background(), time.Now().UTC().Add(time.Hour))
			if (err != nil) != test.wantError {
				t.Fatalf("Reconcile error = %v, wantError = %v", err, test.wantError)
			}
			if len(clickhouseClient.dropped) != test.wantDropped {
				t.Fatalf("dropped tables = %v, want %d drops", clickhouseClient.dropped, test.wantDropped)
			}
			if gotSaved := len(catalog.savedStates) > 0; gotSaved != test.wantSaved {
				t.Fatalf("SaveExecution states = %v, wantSaved = %v", catalog.savedStates, test.wantSaved)
			}
			if test.wantSaved && catalog.savedStates[0] != publication.BundleFailed {
				t.Fatalf("demoted state = %q, want FAILED", catalog.savedStates[0])
			}
			if catalog.released != 1 {
				t.Fatalf("ReleaseBundleLease calls = %d, want the bundle lease released exactly once", catalog.released)
			}
		})
	}
}

func TestReconcileRetiresSupersededPublishedExecutions(t *testing.T) {
	const candidateID = "exec-superseded"
	published := func() publication.BundleExecution {
		return publication.BundleExecution{
			ID:  candidateID,
			Key: "bundle:" + candidateID,
			BundleIdentity: publication.BundleIdentity{
				Project: "orphans", Name: "orders", DatasetGeneration: "gen", OutputName: "main",
			},
			State: publication.BundlePublished,
			Outputs: []publication.BundleOutputRecord{
				{Name: "main", PhysicalTable: "loom_bundle_exec_superseded"},
			},
		}
	}
	bind := func(executionID string) dataset.ActiveRelease {
		return dataset.ActiveRelease{
			Release:  dataset.ProjectRelease{Publications: []dataset.ReleasePublication{{ExecutionID: executionID}}},
			Revision: 3,
		}
	}
	namedByPointer := &publication.BundlePointer{ExecutionID: candidateID}
	tests := []struct {
		name         string
		reader       *reconcileRecheckReleaseReader
		wireReader   bool
		firstPointer *publication.BundlePointer
		active       *dataset.ActiveRelease
		wantErr      bool
		wantAcquired int
		wantDropped  int
		wantSaved    bool
	}{
		{
			name:         "superseded+unbound+unpublished-by-pointer -> retired",
			reader:       &reconcileRecheckReleaseReader{err: dataset.ErrNoActiveRelease},
			wireReader:   true,
			wantAcquired: 1,
			wantDropped:  1,
			wantSaved:    true,
		},
		{
			name:         "pointer names the published candidate -> never claimed",
			reader:       &reconcileRecheckReleaseReader{err: dataset.ErrNoActiveRelease},
			wireReader:   true,
			firstPointer: namedByPointer,
		},
		{
			name:       "active release binds the candidate -> never claimed",
			reader:     &reconcileRecheckReleaseReader{},
			wireReader: true,
			active:     &dataset.ActiveRelease{Release: bind(candidateID).Release, Revision: 3},
		},
		{
			name:       "active release read fails -> fail-closed, never claimed",
			reader:     &reconcileRecheckReleaseReader{err: errors.New("release store down")},
			wireReader: true,
			wantErr:    true,
		},
		{
			name:       "unwired reader -> PUBLISHED not enumerated at all (blind janitor forbidden)",
			wireReader: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := published()
			catalog := &reconcileRecheckCatalog{
				publishedExec: &candidate,
				pointers:      []publication.BundlePointer{{}, {}},
				pointerErrs:   []error{publication.ErrBundleNotFound, publication.ErrBundleNotFound},
			}
			if test.firstPointer != nil {
				catalog.pointers[0] = *test.firstPointer
				catalog.pointerErrs[0] = nil
			}
			clickhouseClient := &reconcileRecheckClickHouse{}
			store, err := NewBundleStore(clickhouseClient, catalog)
			if err != nil {
				t.Fatalf("NewBundleStore: %v", err)
			}
			reader := test.reader
			if reader == nil {
				reader = &reconcileRecheckReleaseReader{}
			}
			if test.active != nil {
				reader.active = *test.active
				reader.err = nil
			}
			if test.wireReader {
				store.SetActiveReleaseReader(reader)
			}
			err = store.Reconcile(context.Background(), time.Now().UTC().Add(time.Hour))
			if (err != nil) != test.wantErr {
				t.Fatalf("Reconcile error = %v, wantErr = %v", err, test.wantErr)
			}
			if len(catalog.acquiredKeys) != test.wantAcquired {
				t.Fatalf("claimed keys = %v, want %d claims", catalog.acquiredKeys, test.wantAcquired)
			}
			if len(clickhouseClient.dropped) != test.wantDropped {
				t.Fatalf("dropped tables = %v, want %d drops", clickhouseClient.dropped, test.wantDropped)
			}
			if gotSaved := len(catalog.savedStates) > 0; gotSaved != test.wantSaved {
				t.Fatalf("SaveExecution states = %v, wantSaved = %v", catalog.savedStates, test.wantSaved)
			}
			if test.wantSaved {
				if catalog.savedStates[0] != publication.BundleFailed {
					t.Fatalf("retired state = %q, want FAILED", catalog.savedStates[0])
				}
				if !strings.Contains(catalog.savedErrors[0], "superseded execution reconciled") {
					t.Fatalf("retired marker = %q, want the superseded janitor marker", catalog.savedErrors[0])
				}
			}
			if catalog.released != test.wantAcquired {
				t.Fatalf("ReleaseBundleLease calls = %d, want one per claim (%d)", catalog.released, test.wantAcquired)
			}
		})
	}

	t.Run("stale arm marker unchanged", func(t *testing.T) {
		catalog := &reconcileRecheckCatalog{
			execution: publication.BundleExecution{
				ID:  "exec-stale",
				Key: "bundle:exec-stale",
				BundleIdentity: publication.BundleIdentity{
					Project: "orphans", Name: "orders", DatasetGeneration: "gen", OutputName: "main",
				},
				State: publication.BundleRunning,
				Outputs: []publication.BundleOutputRecord{
					{Name: "main", PhysicalTable: "loom_bundle_exec_stale"},
				},
			},
			pointers:    []publication.BundlePointer{{}, {}},
			pointerErrs: []error{publication.ErrBundleNotFound, publication.ErrBundleNotFound},
		}
		clickhouseClient := &reconcileRecheckClickHouse{}
		store, err := NewBundleStore(clickhouseClient, catalog)
		if err != nil {
			t.Fatalf("NewBundleStore: %v", err)
		}
		store.SetActiveReleaseReader(&reconcileRecheckReleaseReader{err: dataset.ErrNoActiveRelease})
		if err := store.Reconcile(context.Background(), time.Now().UTC().Add(time.Hour)); err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if len(catalog.savedStates) != 1 || catalog.savedStates[0] != publication.BundleFailed {
			t.Fatalf("saved = %v, want single FAILED", catalog.savedStates)
		}
		if catalog.savedErrors[0] != "stale execution reconciled" {
			t.Fatalf("marker = %q, want the verbatim stale marker", catalog.savedErrors[0])
		}
	})
}

type tocFakeCatalog struct {
	mu         sync.Mutex
	leases     map[string]tocLease
	pointers   map[string]publication.BundlePointer
	executions map[string]publication.BundleExecution
	states     map[string]*tocExecState
	race       func()
	raceRan    bool
	dropped    []string
	savedFence []string
}

type tocLease struct {
	owner   string
	expires time.Time
}

type tocPin struct {
	owner   string
	expires time.Time
}

type tocExecState struct {
	pins    []tocPin
	cleanup *tocLease
}

func newTOCCatalog() *tocFakeCatalog {
	return &tocFakeCatalog{
		leases:     map[string]tocLease{},
		pointers:   map[string]publication.BundlePointer{},
		executions: map[string]publication.BundleExecution{},
		states:     map[string]*tocExecState{},
	}
}

func (f *tocFakeCatalog) state(execID string) *tocExecState {
	if s := f.states[execID]; s != nil {
		return s
	}
	s := &tocExecState{}
	f.states[execID] = s
	return s
}

func (f *tocFakeCatalog) AcquireBundleLease(_ context.Context, key, owner string, expires time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	cur, ok := f.leases[key]
	if !ok || cur.expires.Before(now) || cur.owner == owner {
		f.leases[key] = tocLease{owner: owner, expires: expires}
		return true, nil
	}
	return false, nil
}

func (f *tocFakeCatalog) RenewBundleLease(_ context.Context, key, owner string, expires time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.leases[key]
	if ok && cur.owner == owner && !cur.expires.Before(time.Now().UTC()) {
		f.leases[key] = tocLease{owner: owner, expires: expires}
		return true, nil
	}
	return false, nil
}

func (f *tocFakeCatalog) ReleaseBundleLease(_ context.Context, key, owner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if cur, ok := f.leases[key]; ok && cur.owner == owner {
		delete(f.leases, key)
	}
	return nil
}

func (f *tocFakeCatalog) GetPointer(_ context.Context, name string) (publication.BundlePointer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.pointers[name]; ok {
		return p, nil
	}
	return publication.BundlePointer{}, publication.ErrBundleNotFound
}

func (f *tocFakeCatalog) CompareAndSwapPointer(_ context.Context, name, expected, next string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.pointers[name]; ok && p.ExecutionID != expected {
		return publication.ErrBundlePointerConflict
	}
	f.pointers[name] = publication.BundlePointer{Name: name, ExecutionID: next, UpdatedAt: time.Now().UTC()}
	return nil
}

func (f *tocFakeCatalog) PublishExecution(_ context.Context, name, expected string, execution publication.BundleExecution) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	stored, ok := f.executions[execution.ID]
	if !ok {
		return publication.ErrBundleNotFound
	}
	lease, ok := f.leases[stored.Key]
	if !ok || lease.owner != execution.OwnerID || lease.expires.Before(now) {
		return publication.ErrBundlePointerConflict
	}
	if p, ok := f.pointers[name]; ok && p.ExecutionID != expected {
		return publication.ErrBundlePointerConflict
	}
	f.executions[execution.ID] = execution
	f.pointers[name] = publication.BundlePointer{Name: name, ExecutionID: execution.ID, UpdatedAt: now}
	return nil
}

func (f *tocFakeCatalog) SaveExecution(_ context.Context, execution publication.BundleExecution, fenceOwner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	lease, ok := f.leases[execution.Key]
	if !ok || lease.owner != fenceOwner || lease.expires.Before(now) {
		return publication.ErrBundleLeaseLost
	}
	f.savedFence = append(f.savedFence, fenceOwner)
	f.executions[execution.ID] = execution
	return nil
}

func (f *tocFakeCatalog) GetExecution(_ context.Context, id string) (publication.BundleExecution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.executions[id]; ok {
		return e, nil
	}
	return publication.BundleExecution{}, publication.ErrBundleNotFound
}

func (f *tocFakeCatalog) FindExecutionByKey(_ context.Context, key string) (publication.BundleExecution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.executions {
		if e.Key == key {
			return e, nil
		}
	}
	return publication.BundleExecution{}, publication.ErrBundleNotFound
}

func (f *tocFakeCatalog) ListExecutions(_ context.Context, _ publication.BundleState, _ time.Time) ([]publication.BundleExecution, error) {
	return nil, nil
}

func (f *tocFakeCatalog) VisitExecutionPages(_ context.Context, state publication.BundleState, _ time.Time, _ int, visit publication.BundleExecutionPageFunc) error {
	f.mu.Lock()
	page := make([]publication.BundleExecution, 0, 1)
	for _, e := range f.executions {
		if e.State == state {
			page = append(page, e)
		}
	}
	f.mu.Unlock()
	if len(page) == 0 {
		return nil
	}
	return visit(page)
}

func (f *tocFakeCatalog) AcquireExecutionReadPin(_ context.Context, execID, owner string, expires time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.state(execID)
	now := time.Now().UTC()
	if s.cleanup != nil && !s.cleanup.expires.Before(now) {
		return false, nil
	}
	s.pins = append(s.pins, tocPin{owner: owner, expires: expires})
	return true, nil
}

func (f *tocFakeCatalog) RenewExecutionReadPin(_ context.Context, execID, owner string, expires time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.state(execID)
	for i, p := range s.pins {
		if p.owner == owner {
			s.pins[i].expires = expires
			return true, nil
		}
	}
	return false, nil
}

func (f *tocFakeCatalog) ReleaseExecutionReadPin(_ context.Context, execID, owner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.state(execID)
	kept := s.pins[:0]
	for _, p := range s.pins {
		if p.owner != owner {
			kept = append(kept, p)
		}
	}
	s.pins = kept
	return nil
}

func (f *tocFakeCatalog) ClaimExecutionCleanup(_ context.Context, execID, owner string) (bool, error) {
	if f.race != nil && !f.raceRan {
		f.raceRan = true
		f.race()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	s := f.state(execID)
	for _, p := range s.pins {
		if !p.expires.Before(now) {
			return false, nil
		}
	}
	if s.cleanup != nil && !s.cleanup.expires.Before(now) && s.cleanup.owner != owner {
		return false, nil
	}
	s.pins = nil
	s.cleanup = &tocLease{owner: owner, expires: now.Add(time.Minute)}
	return true, nil
}

func (f *tocFakeCatalog) RenewExecutionCleanup(_ context.Context, execID, owner string, expires time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.state(execID)
	now := time.Now().UTC()
	if s.cleanup == nil || s.cleanup.owner != owner || s.cleanup.expires.Before(now) {
		return false, nil
	}
	for _, p := range s.pins {
		if !p.expires.Before(now) {
			return false, nil
		}
	}
	s.cleanup.expires = expires
	return true, nil
}

func (f *tocFakeCatalog) ReleaseExecutionCleanup(_ context.Context, execID, owner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s := f.states[execID]; s != nil && s.cleanup != nil && s.cleanup.owner == owner {
		s.cleanup = nil
	}
	return nil
}

type tocFakeClickHouse struct {
	mu    sync.Mutex
	table string
	drop  func(ctx context.Context) error
}

func (c *tocFakeClickHouse) DropTable(ctx context.Context, table string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.table == table {
		c.table = ""
	}
	if c.drop != nil {
		return c.drop(ctx)
	}
	return nil
}

func (c *tocFakeClickHouse) CreateTable(context.Context, string, []clickhouse.Column) error {
	return nil
}

func (c *tocFakeClickHouse) InsertRows(context.Context, string, []clickhouse.Column, []map[string]any) error {
	return nil
}

func (c *tocFakeClickHouse) VerifyOutput(context.Context, string, []clickhouse.Column, int64) error {
	return nil
}

func (c *tocFakeClickHouse) DropColumns(context.Context, string, []string) error { return nil }

const tocExecID = "exec-toc-1"

const tocTable = "loom_bundle_toc_output"

func tocExecution(state publication.BundleState) publication.BundleExecution {
	now := time.Now().UTC()
	return publication.BundleExecution{
		ID:    tocExecID,
		Key:   "bundle-key-toc",
		State: state,
		Outputs: []publication.BundleOutputRecord{
			{Name: "output", PhysicalTable: tocTable, RowCount: 10},
		},
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-time.Hour),
	}
}

func tocStore(t *testing.T, ch *tocFakeClickHouse, cat *tocFakeCatalog, leaseTTL time.Duration) *ClickHouseBundleStore {
	t.Helper()
	s, err := NewBundleStore(ch, cat)
	if err != nil {
		t.Fatalf("NewBundleStore: %v", err)
	}
	s.leaseTTL = leaseTTL
	return s
}

func TestReconcileDropsJustPublishedBundleStagesLeaseLoss(t *testing.T) {
	cat := newTOCCatalog()
	ch := &tocFakeClickHouse{table: tocTable}
	exec := tocExecution(publication.BundleRunning)
	cat.executions[tocExecID] = exec

	cat.race = func() {
		now := time.Now().UTC()
		acquired, err := cat.AcquireBundleLease(context.Background(), exec.Key, "runner", now.Add(2*time.Minute))
		if err != nil || !acquired {
			t.Errorf("racing runner could not take over the reconciler lease: acquired=%v err=%v", acquired, err)
			return
		}
		published := exec
		published.State = publication.BundlePublished
		published.OwnerID = "runner"
		published.PublishedAt = &now
		if err := cat.PublishExecution(context.Background(), exec.PointerName(), tocExecID, published); err != nil {
			t.Errorf("racing publish failed: %v", err)
		}
	}

	s := tocStore(t, ch, cat, -time.Second)
	err := s.Reconcile(context.Background(), time.Now().UTC().Add(-time.Minute))

	cat.mu.Lock()
	pointer, pointerLive := cat.pointers[exec.PointerName()]
	published := cat.executions[tocExecID].State == publication.BundlePublished
	cat.mu.Unlock()

	if !published {
		t.Fatalf("precondition: racing publish did not land")
	}
	if !pointerLive || pointer.ExecutionID != tocExecID {
		t.Fatalf("precondition: pointer does not name the published execution: %+v", pointer)
	}
	if ch.table == "" {
		t.Errorf("REGRESSION (F-B1 TOCTOU): reconcileExecution deleted the physical tables of a bundle the pointer advertises as live; pointer %q still names %q; tables gone. Reconcile error chain: %v (errors.Is ErrBundleLeaseLost=%v)",
			pointer.Name, pointer.ExecutionID, err, errors.Is(err, publication.ErrBundleLeaseLost))
	}
	if ch.table != tocTable {
		t.Fatalf("post-condition: table state unexpected, want the live table %q preserved, got %q", tocTable, ch.table)
	}
	if err != nil {
		t.Fatalf("post-condition: prevented drop must reconcile cleanly, got %v", err)
	}
}

func TestReconcileSkipsPointerNamedExecution(t *testing.T) {
	cat := newTOCCatalog()
	ch := &tocFakeClickHouse{table: tocTable}
	exec := tocExecution(publication.BundleRunning)
	cat.executions[tocExecID] = exec
	cat.pointers[exec.PointerName()] = publication.BundlePointer{Name: exec.PointerName(), ExecutionID: tocExecID}

	s := tocStore(t, ch, cat, -time.Second)
	if err := s.Reconcile(context.Background(), time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if ch.table != tocTable {
		t.Fatalf("regression: :1073 pointer skip dropped a pointer-named execution")
	}
}

func TestReconcileDropsLongStaleUnpublishedExecution(t *testing.T) {
	cat := newTOCCatalog()
	ch := &tocFakeClickHouse{table: tocTable}
	exec := tocExecution(publication.BundleFailed)
	cat.executions[tocExecID] = exec

	s := tocStore(t, ch, cat, 2*time.Minute)
	if err := s.Reconcile(context.Background(), time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if ch.table != "" {
		t.Fatalf("ordinary stale cleanup did not drop the stale table")
	}
	if len(cat.savedFence) != 1 {
		t.Fatalf("expected exactly one fenced SaveExecution, got %v", cat.savedFence)
	}
}
