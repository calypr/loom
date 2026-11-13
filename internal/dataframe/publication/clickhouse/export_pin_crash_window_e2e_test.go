package clickhouse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	arangofactory "github.com/calypr/loom/internal/backends/arango"
	pubpublication "github.com/calypr/loom/internal/dataframe/publication"
	pubpublished "github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
)

const (
	artifactExportHorizon    = 24 * time.Hour
	productionFirstTickBound = 30 * time.Second
)

type ecwPinCall struct {
	executionID string
	owner       string
	expires     time.Time
}

type ecwCatalog struct {
	*arangofactory.PublishedRegistry
	mu       sync.Mutex
	acquires []ecwPinCall
	renews   []ecwPinCall
	releases []ecwPinCall
}

func (c *ecwCatalog) AcquireExecutionReadPin(ctx context.Context, executionID, owner string, expires time.Time) (bool, error) {
	c.mu.Lock()
	c.acquires = append(c.acquires, ecwPinCall{executionID: executionID, owner: owner, expires: expires})
	c.mu.Unlock()
	return c.PublishedRegistry.AcquireExecutionReadPin(ctx, executionID, owner, expires)
}

func (c *ecwCatalog) RenewExecutionReadPin(ctx context.Context, executionID, owner string, expires time.Time) (bool, error) {
	c.mu.Lock()
	c.renews = append(c.renews, ecwPinCall{executionID: executionID, owner: owner, expires: expires})
	c.mu.Unlock()
	renewed, err := c.PublishedRegistry.RenewExecutionReadPin(ctx, executionID, owner, expires)
	return renewed, err
}

func (c *ecwCatalog) ReleaseExecutionReadPin(ctx context.Context, executionID, owner string) error {
	c.mu.Lock()
	c.releases = append(c.releases, ecwPinCall{executionID: executionID, owner: owner})
	c.mu.Unlock()
	return c.PublishedRegistry.ReleaseExecutionReadPin(ctx, executionID, owner)
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

func ecwCleanupDatabase(t *testing.T, endpoint, database string) {
	t.Helper()
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cleanupCancel()
	request, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(database), nil)
	if err != nil {
		t.Errorf("build cleanup request: %v", err)
		return
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Errorf("drop database %s: %v", database, err)
		return
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusForbidden {
		t.Errorf("drop database %s: status %d", database, response.StatusCode)
	}
}

func ecwSetup(t *testing.T) (*arangofactory.Client, *arangofactory.PublishedRegistry, context.Context, string) {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	database := "loom_verify_ecw_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	t.Logf("owned arango database: %s", database)
	arangoBackend, err := arangofactory.Open(ctx, arangofactory.Config{URL: endpoint, Database: database})
	if err != nil {
		t.Fatalf("open arango: %v", err)
	}
	client := arangoBackend.Client
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		ecwCleanupDatabase(t, endpoint, database)
	})
	if err := client.Bootstrap(ctx, arangofactory.PublishedBootstrapSpec()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	registry, err := arangofactory.NewPublishedRegistry(client)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	return client, registry, ctx, database[len("loom_verify_ecw_"):]
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

func ecwUpsertWithLease(t *testing.T, registry *arangofactory.PublishedRegistry, ctx context.Context, exec pubpublication.BundleExecution, leaseOwner string) {
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
	QueryRows(ctx context.Context, query string, batchSize int, bindVars map[string]interface{}, visit arangofactory.RowVisitor) error
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
			"@collection": arangofactory.ExecutionReadPinsCollection,
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
		t.Fatalf("direct AQL read of %s for %s: %v", arangofactory.ExecutionReadPinsCollection, executionID, err)
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

func ecwStartExactExport(t *testing.T, registry *arangofactory.PublishedRegistry, ctx context.Context, exec pubpublication.BundleExecution, readerID string, horizon, renewalInterval time.Duration) (*ecwCatalog, *ecwQueryer, context.CancelFunc, <-chan error, time.Time) {
	t.Helper()
	catalog := &ecwCatalog{PublishedRegistry: registry}
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
	t.Fatalf("production StreamExactExport never issued its AcquireExecutionReadPin (export.go:135) for %s/%s: %#v", executionID, readerID, catalog.snapshotAcquires())
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
		t.Fatalf("acquire expiry %v not the artifact-passed caller TTL %v (export.go:120-126 must pass it through)", acquire.expires, pinExpiresAt)
	}

	early := ecwReadPins(t, client, x.ID, time.Now().UTC(), time.Now().UTC().Add(23*time.Hour))
	if !early.Found || early.Active < 1 || early.Far < 1 || !ecwHasOwner(early.Owners, readerID) {
		t.Fatalf("crash-window pin not observed on %s for %s/%s: %+v", arangofactory.ExecutionReadPinsCollection, x.ID, readerID, early)
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
	xRow, xErr := registry.GetExecution(ctx, x.ID)
	yRow, yErr := registry.GetExecution(ctx, y.ID)
	if xErr != nil || yErr != nil {
		t.Fatalf("precondition: rows unreadable after reconcile: X=%v Y=%v", xErr, yErr)
	}
	after := ecwReadPins(t, client, x.ID, time.Now().UTC(), time.Now().UTC().Add(23*time.Hour))
	if !after.Found || after.Active < 1 || after.Far < 1 || !ecwHasOwner(after.Owners, readerID) {
		t.Fatalf("crash-window pin vanished across Reconcile (expected it to survive to its 24h horizon): %+v", after)
	}

	pinErr := errors.Is(reconcileErr, pubpublication.ErrExecutionReadPinActive)
	xReclaimed := ecwDropped(dropped, xTable)
	yReclaimed := ecwDropped(dropped, yTable)

	if !pinErr {
		t.Errorf("FINDING NOT REPRODUCED (abort cause): production Reconcile over the crashed-export-pinned execution must fail with errors.Is(err, ErrExecutionReadPinActive); got: %v (dropped=%v)", reconcileErr, dropped)
	}
	if pinErr && !xReclaimed && !yReclaimed {
		t.Errorf("CONFIRMED with live crash realism against real Arango (F-B3): the REAL published.Reader.StreamExactExport entry point issued its own AcquireExecutionReadPin (export.go:135, recording wrapper delegating to real Registry AQL bundle_registry.go:343-376) with the artifact-supplied caller TTL %v (artifact.go:21/:104/:152 identity.ExpiresAt=now+24h); the export was abandoned in flight before its FIRST 30s renewal tick (export.go:156 ticker never fired: zero RenewExecutionReadPin calls observed; the only non-test renewers export.go:168/read_pins.go:90 are in-goroutine tickers of the owning process, bundle_store.go:266 renews the cleanup slot) and the deferred release (export.go:142-146) never ran, leaving a live 24h pin (direct AQL on %s: active=%d far=%d owners=%v). Startup Reconcile then aborted on %q with errors.Is ErrExecutionReadPinActive and the unrelated, pin-free sibling %q was STRANDED (bundle_store.go:1033-1052 first-error-wins, :1076-1079 propagation; server.go:556-558 publicationReady=false for the process lifetime). reconcileErr=%v; X table %s reclaimed=%v; Y table %s reclaimed=%v state=%q error-field=%q; dropped=%v. Contrast control TestExportCrashWithoutCrashRenewedPinExpiresFast: after ONE real ticker tick pulled the same caller 24h down to ~defaultReadPinTTL and the stream completed+released, the identical production Reconcile reclaimed BOTH executions.",
			pinExpiresAt.UTC().Format(time.RFC3339), arangofactory.ExecutionReadPinsCollection, after.Active, after.Far, after.Owners, x.ID, y.ID, reconcileErr, xTable, xReclaimed, yTable, yReclaimed, yRow.State, yRow.Error, dropped)
	} else if pinErr && !xReclaimed && yReclaimed {
		t.Errorf("UNEXPECTED: pin abort did NOT strand sibling Y (dropped=%v): a compensating skip-and-continue path exists — re-examine F-B3 blast radius", dropped)
	}
	if xReclaimed {
		t.Errorf("unexpected: the crashed-export-pinned execution X was reclaimed despite the live 24h pin (dropped=%v)", dropped)
	}
	if yRow.State != pubpublication.BundleFailed || yRow.Error != "" {
		t.Errorf("unexpected: sibling Y was touched (state=%q error=%q); expected untouched FAILED with empty marker, proving reconcileExecution never reached it", yRow.State, yRow.Error)
	}
	if xRow.Error != "" {
		t.Errorf("unexpected: pinned X carries reconcile marker %q; the claim declined before reconcileExecution reached its fenced SaveExecution (:1097-1099), so the row must keep the empty marker it was transitioned with", xRow.Error)
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
		t.Fatalf("CONTROL FAILED: ticker renewal expiry %v is not ~defaultReadPinTTL(2min) from now (%v): export.go:166 semantics changed", lastRenew.expires, now)
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
