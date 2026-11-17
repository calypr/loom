package clickhouse

// This suite drives exact export through the production reader while a real
// Arango registry stores read pins. It checks that an abandoned export leaves
// its caller-expiry pin, a completed export releases its pin, and reconciliation
// preserves pinned outputs while cleaning unrelated stale executions.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	pubpublication "github.com/calypr/loom/internal/dataframe/publication"
	pubarango "github.com/calypr/loom/internal/dataframe/publication/arango"
	pubpublished "github.com/calypr/loom/internal/dataframe/published"
	storearango "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/store/clickhouse"
	"github.com/calypr/loom/internal/testkit/arangotest"
)

// TestExportCrashInsideFirstTickLeavesCrashWindowPinAgainstArango exercises
// the real exact-export entry point against an owned Arango database and a
// controlled ClickHouse queryer. It parks the export before its first renewal,
// verifies the caller-expiry pin remains stored, then checks reconciliation
// preserves the pinned execution while reclaiming an unrelated stale sibling.
// A companion case allows renewal and release to finish, then verifies cleanup
// succeeds for both executions.
//
// Each test uses a UUID-named loom_verify_ecw_<hex> database and removes it in
// t.Cleanup. Required service URLs are set by the invoking integration profile.

const (
	// artifactExportHorizon is the caller-provided export pin horizon.
	artifactExportHorizon = 24 * time.Hour
	// This is the first-tick bound. The crash test's renewal
	// interval sits far beyond it so the first tick cannot fire before the
	// crash verdict is recorded.
	productionFirstTickBound = 30 * time.Second
)

// ecwPinCall records one live pin operation the production entry point issued
// through the wrapper. The wrapper decides nothing: every method delegates
// verbatim to the real Registry AQL.
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

// ecwQueryer is the published.Reader's ClickHouse seam. It records the stream
// SQL and parks at the first scan visit so StreamExactExport stays in flight
// (the crash window); releasing hold models graceful completion.
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
	_ = visit // zero-row scan: production Stream succeeds on an empty row set
	return nil
}

func (q *ecwQueryer) snapshotQueries() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]string(nil), q.queries...)
}

// ecwClickHouse records physical drops so the blast radius (which stale
// executions the reconciler actually reclaimed) is observable, matching the
// pbxClickHouse semantics including dead-context aborts.
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

// ecwExecution builds an execution under a distinct identity. published=true
// makes it export-materializable (it must be in a successful state,
// queryable output, receipt+schema); published=false seeds the stale FAILED
// sibling that Reconcile visits.
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

// ecwUpsertWithLease stores an execution row through the real leased
// SaveExecution AQL, then frees the key (fb1Seed/pbxSeed idiom).
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

// ecwRowQuerier is the narrow QueryRows surface of *storearango.Client used to
// read the pins collection directly. There is no catalog GetExecutionReadPins
// API, so the pin truth is the AQL document itself.
type ecwRowQuerier interface {
	QueryRows(ctx context.Context, query string, batchSize int, bindVars map[string]interface{}, visit storearango.RowVisitor) error
}

// ecwStateKey reproduces the catalog state key used by the pin queries:
// the pins collection document key for one execution.
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

// ecwReadPins performs the direct AQL document read of the real
// ExecutionReadPinsCollection entry: active counts pins with expiresAt >= now
// (same comparison the production ClaimExecutionCleanup AQL applies at
// far counts pins still live at horizon, used to
// distinguish the caller's 24h horizon from the ticker's 2min horizon.
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

// ecwStartExactExport drives the REAL production entry point
// published.Reader.StreamExactExport through a recording
// catalog wrapper that delegates every catalog call verbatim to the real
// Registry, and parks the export in flight at its first ClickHouse scan visit.
// renewalInterval configures the caller-visible PinRenewalInterval; pass a
// value >> productionFirstTickBound to model a crash inside the default
// pre-first-tick window, or a millisecond-scale value to force real ticks.
func ecwStartExactExport(t *testing.T, registry *pubarango.Registry, ctx context.Context, exec pubpublication.BundleExecution, readerID string, horizon, renewalInterval time.Duration) (*ecwCatalog, *ecwQueryer, context.CancelFunc, <-chan error, time.Time) {
	t.Helper()
	catalog := &ecwCatalog{Registry: registry}
	queryer := &ecwQueryer{started: make(chan struct{}), hold: make(chan struct{})}
	reader := &pubpublished.Reader{Catalog: catalog, ClickHouse: queryer}
	pinExpiresAt := time.Now().UTC().Add(horizon)
	// Identity fields copied verbatim from the seeded identity so
	// validateExactExportIdentity matches the real
	// materialization the registry returns.
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

// ecwFindAcquire locates the acquisition the production entry point issued for
// this execution/reader, or fails.
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

// TestExportCrashInsideFirstTickLeavesCrashWindowPinAgainstArango retains its
// historical name. It should prove that the crash-window pin survives, the
// pinned execution is retained, unrelated stale work is reclaimed, and the
// reconcile pass completes without error.
func TestExportCrashInsideFirstTickLeavesCrashWindowPinAgainstArango(t *testing.T) {
	skipKnownIssueUnlessEnabled(t, "ARC-C13")
	client, registry, ctx, suffix := ecwSetup(t)

	base := time.Now().UTC()
	xTable := "loom_bundle_ecw_x_" + suffix
	yTable := "loom_bundle_ecw_y_" + suffix
	x := ecwExecution("X", suffix, xTable, base.Add(-70*time.Minute), true)
	ecwUpsertWithLease(t, registry, ctx, x, "ecw-seed")

	const readerID = "artifact-export-crash-window"
	// Renewal interval far beyond the 30s production first tick: the crash
	// lands before the ticker's first case can ever run.
	catalog, queryer, cancelExport, _, pinExpiresAt := ecwStartExactExport(t, registry, ctx, x, readerID, artifactExportHorizon, 10*time.Minute)
	defer cancelExport() // models test-process teardown AFTER the crash verdict, not the crash itself

	// Sleep far beyond any realistic first-tick arrival at 10min interval
	// relative to the 250ms below: if a ticker could fire spuriously it
	// would already have, so renewCount==0 measures the genuine window.
	time.Sleep(250 * time.Millisecond)

	// Realism evidence 1: the REAL entry point's own acquire call executed
	// with the caller TTL the artifact would pass (PinExpiresAt is honored
	// verbatim).
	acquire := ecwFindAcquire(t, catalog, x.ID, readerID)
	if acquire.expires.Before(pinExpiresAt.Add(-time.Minute)) || acquire.expires.After(pinExpiresAt.Add(time.Minute)) {
		t.Fatalf("acquire expiry %v not the artifact-passed caller TTL %v", acquire.expires, pinExpiresAt)
	}

	// Realism evidence 2: direct AQL document read proves the crash-window
	// pin is on the REAL pins collection under the caller's 24h horizon.
	early := ecwReadPins(t, client, x.ID, time.Now().UTC(), time.Now().UTC().Add(23*time.Hour))
	if !early.Found || early.Active < 1 || early.Far < 1 || !ecwHasOwner(early.Owners, readerID) {
		t.Fatalf("crash-window pin not observed on %s for %s/%s: %+v", pubarango.ExecutionReadPinsCollection, x.ID, readerID, early)
	}
	t.Logf("crash-window pin observed by direct AQL: active=%d far(>=23h)=%d owners=%v expiries=%v", early.Active, early.Far, early.Owners, early.Expiries)

	// Realism evidence 3: no renewal ever ticked during the window and no
	// other component targets this readerId (header rg evidence).
	if renews := catalog.snapshotRenews(); len(renews) != 0 {
		t.Fatalf("renewal ticker fired inside the simulated crash window (%v): not a pre-first-tick crash", renews)
	}
	if releases := catalog.snapshotReleases(); len(releases) != 0 {
		t.Fatalf("release executed while the export is still in flight (%v): crash not modeled", releases)
	}
	if queries := queryer.snapshotQueries(); len(queries) == 0 || !strings.Contains(queries[0], xTable) {
		t.Fatalf("precondition: parked stream never scanned the exported physical table %q: %v", xTable, queries)
	}

	// The crashed export's execution subsequently goes stale/FAILED (a later
	// run under the same identity failed), making it a Reconcile candidate
	// while the orphaned pin is still live; Y is an unrelated pin-free stale
	// sibling, exactly worker B's shape.
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

// TestExportCrashWithoutCrashRenewedPinExpiresFast is the must-PASS control:
// the same real entry-point flow, but the export survives long enough for the
// production renewal ticker to tick — observed in the
// wrapper AND in the direct AQL read as the caller's 24h horizon being pulled
// down to ~defaultReadPinTTL. The stream then completes and
// the deferred release removes the pin; the identical
// production Reconcile reclaims both stale executions with nil error.
func TestExportCrashWithoutCrashRenewedPinExpiresFast(t *testing.T) {
	client, registry, ctx, suffix := ecwSetup(t)

	base := time.Now().UTC()
	xTable := "loom_bundle_ecw_x_" + suffix
	yTable := "loom_bundle_ecw_y_" + suffix
	x := ecwExecution("X", suffix, xTable, base.Add(-70*time.Minute), true)
	ecwUpsertWithLease(t, registry, ctx, x, "ecw-seed")

	const readerID = "artifact-export-renewed"
	// Millisecond renewal interval forces the production ticker to take its
	// production path: next = now.Add(defaultReadPinTTL).
	catalog, queryer, cancelExport, exportDone, pinExpiresAt := ecwStartExactExport(t, registry, ctx, x, readerID, artifactExportHorizon, 10*time.Millisecond)
	defer cancelExport()

	acquire := ecwFindAcquire(t, catalog, x.ID, readerID)
	if acquire.expires.Before(pinExpiresAt.Add(-time.Minute)) {
		t.Fatalf("control precondition: caller TTL %v not honored at acquire: %v", pinExpiresAt, acquire.expires)
	}

	// The ticker must pull the 24h caller horizon down below the 23h bound
	// (i.e. to ~defaultReadPinTTL) while the stream is still in flight.
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

	// Graceful stream end runs the deferred release.
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

	// Same stale transition as the crash test, then production Reconcile:
	// with the renewed-then-released pin gone, the sweep completes normally.
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
