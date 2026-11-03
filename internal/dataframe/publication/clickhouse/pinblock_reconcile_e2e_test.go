package clickhouse

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	pubpublication "github.com/calypr/loom/internal/dataframe/publication"
	pubarango "github.com/calypr/loom/internal/dataframe/publication/arango"
	storearango "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
)

// Candidate finding H2-a: a stalled export read-pin makes Reconcile abandon
// every other stale execution and downgrade publication readiness.
//
// Source-verified wiring (unmodified):
//   - internal/explorer/lifecycle/artifact.go:21 defaultArtifactTTL = 24h;
//     :104 identity.ExpiresAt = now + artifactTTL(=24h); :152 the exact-export
//     request carries PinExpiresAt = identity.ExpiresAt.
//   - internal/dataframe/published/export.go:120-126 accepts ANY future
//     PinExpiresAt, so a fresh artifact export pins the execution at now+24h;
//     the renewal ticker (:156/:166 with defaultReadPinInterval=30s,
//     defaultReadPinTTL=2min) is what first pulls it back to now+2min — but
//     only at the FIRST 30s tick. A process crash inside that first window
//     leaves a 24h live pin on the execution.
//
// Hypothesis: startup Reconcile (internal/server/server.go:556-558, a single
// one-shot call: err -> recordDegradation + publicationReady=false) reaches
// claimExecutionCleanup (bundle_store.go:301-313 -> ErrExecutionReadPinActive
// when the real ClaimExecutionCleanup AQL bundle_registry.go:411-427 sees an
// unexpired pin) for the pinned execution; reconcileExecution propagates the
// error (bundle_store.go:1076-1079) and Reconcile's `process` loop aborts the
// ENTIRE run on the first error (bundle_store.go:1033-1052, first error wins),
// so an UNRELATED stale execution is never reclaimed and the whole deployment
// loses publication readiness. Author intent hints otherwise (export.go:57
// "shorter operation budget"); the doc on Reconcile (:1029-1032) advertises
// "safe to call repeatedly during startup", implying a partial sweep must not
// be able to veto the rest.
//
// This file drives the REAL Registry against a real Arango database and the
// production Reconcile/reconcileExecution/claimExecutionCleanup paths verbatim
// — the only fake is a ClickHouse effect recorder (like tocFakeClickHouse /
// fbxClickHouse) that records which physical tables were dropped. No wrapper
// injects any decision: the pin is created through the production catalog API.
//
// Opt-in, same idiom as reconcile_toctou_e2e_test.go / abort_ackloss_e2e_test.go.
// The tests own UUID databases named loom_verify_pbx_<hex> and delete only
// those. Run against the owned stack from inside the api container:
//
//	docker exec loom-dev-qwen-current-loom-api-1 sh -c \
//	  'cd /workspace && LOOM_TEST_ARANGO_URL=http://arangodb:8529 go test \
//	   -count=1 -run "TestStalePin" -v ./internal/dataframe/publication/clickhouse/'

// pbxClickHouse records the physical tables handed to DropTable so the tests
// can observe which stale executions the reconciler actually reclaimed. Every
// other driver method is a no-op; a dead context aborts the drop the way the
// real driver would (matching tocFakeClickHouse.DropTable).
type pbxClickHouse struct {
	mu      sync.Mutex
	dropped []string
}

func (c *pbxClickHouse) CreateTable(context.Context, string, []clickhouse.Column) error { return nil }
func (c *pbxClickHouse) InsertRows(context.Context, string, []clickhouse.Column, []map[string]any) error {
	return nil
}
func (c *pbxClickHouse) VerifyOutput(context.Context, string, []clickhouse.Column, int64) error {
	return nil
}
func (c *pbxClickHouse) DropColumns(context.Context, string, []string) error { return nil }

func (c *pbxClickHouse) DropTable(ctx context.Context, table string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dropped = append(c.dropped, table)
	return nil
}

func (c *pbxClickHouse) droppedTables() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.dropped...)
}

func pbxDropped(set []string, table string) bool {
	for _, name := range set {
		if name == table {
			return true
		}
	}
	return false
}

func pbxCleanupDatabase(t *testing.T, endpoint, database string) {
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

func pbxSetup(t *testing.T) (*pubarango.Registry, context.Context, string) {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	database := "loom_verify_pbx_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	t.Logf("owned arango database: %s", database)
	client, err := storearango.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatalf("open arango: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		pbxCleanupDatabase(t, endpoint, database)
	})
	if err := client.Bootstrap(ctx, pubarango.BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	registry, err := pubarango.New(client)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	return registry, ctx, database[len("loom_verify_pbx_"):]
}

// pbxExecution builds a stale FAILED execution under a distinct identity, so X
// and Y get distinct identity keys (separate bundle leases) and distinct
// pointer names while sharing the single FAILED page visited by Reconcile.
func pbxExecution(tag, suffix, physicalTable string, updatedAt time.Time) pubpublication.BundleExecution {
	identity := pubpublication.BundleIdentity{
		Name: "pbx-" + tag, TranslationVersion: "1", OutputName: "main",
		Project: "loom_verify", DatasetGeneration: "pbx-" + tag + "-" + suffix,
		RecipeDigest: "r-" + tag + "-" + suffix, SchemaDigest: "s-" + tag + "-" + suffix,
		ScopeDigest: "sc-" + tag + "-" + suffix, EngineVersion: "1",
	}
	return pubpublication.BundleExecution{
		ID: "pbx-" + tag + "-" + suffix, Key: identity.Key(), BundleIdentity: identity,
		State:     pubpublication.BundleFailed,
		Outputs:   []pubpublication.BundleOutputRecord{{Name: "main", PhysicalTable: physicalTable, RowCount: 5}},
		CreatedAt: updatedAt,
		UpdatedAt: updatedAt,
	}
}

// pbxSeed stores the stale row under a seed-held lease then frees the key —
// the identical registry state a stale candidate presents to a reconciler
// (same idiom as fb1Seed). No pointer is ever created, so both executions are
// un-advertised and the cleanup slot is free.
func pbxSeed(t *testing.T, registry *pubarango.Registry, ctx context.Context, exec pubpublication.BundleExecution) {
	t.Helper()
	acquired, err := registry.AcquireBundleLease(ctx, exec.Key, "seed", time.Now().UTC().Add(2*time.Minute))
	if err != nil || !acquired {
		t.Fatalf("seed acquire lease %s: acquired=%v err=%v", exec.ID, acquired, err)
	}
	if err := registry.SaveExecution(ctx, exec, "seed"); err != nil {
		t.Fatalf("seed save %s: %v", exec.ID, err)
	}
	if err := registry.ReleaseBundleLease(ctx, exec.Key, "seed"); err != nil {
		t.Fatalf("seed release %s: %v", exec.ID, err)
	}
}

// TestStalePinBlocksEntireReconcileAgainstArango is the FINDING. Two stale
// FAILED executions X (older) and Y, neither pointer-advertised, each with an
// output table. X alone carries a never-released read pin through the real
// catalog API (crash simulation). Production Reconcile is run once.
//
// Expected-buggy outcome (this test therefore fails ON PURPOSE):
//
//	(a) Reconcile returns an error for which errors.Is(err, ErrExecutionReadPinActive)
//	    (bundle_store.go:1076-1079 via claimExecutionCleanup :301-313 /
//	    bundle_registry.go:411-427);
//	(b) X is not reclaimed — its DropTable is never issued (expected, legitimate);
//	(c) DAMAGE: Y — an unrelated, pin-free, equally-stale execution — is ALSO
//	    not reclaimed, because process aborted on X's error (bundle_store.go:1033-1052).
//	    The control TestStalePinAbsentReclaimsAllAgainstArango proves Y's table
//	    WOULD have been dropped with no pin present, so (c) is caused by the pin,
//	    not harness ordering.
func TestStalePinBlocksEntireReconcileAgainstArango(t *testing.T) {
	registry, ctx, suffix := pbxSetup(t)

	base := time.Now().UTC()
	xUpdated := base.Add(-70 * time.Minute)
	yUpdated := base.Add(-40 * time.Minute)
	xTable := "loom_bundle_pbx_x_" + suffix
	yTable := "loom_bundle_pbx_y_" + suffix
	x := pbxExecution("X", suffix, xTable, xUpdated)
	y := pbxExecution("Y", suffix, yTable, yUpdated)
	pbxSeed(t, registry, ctx, x)
	pbxSeed(t, registry, ctx, y)

	// The crash-simulated export pin: acquired on X through the REAL catalog
	// API with a 24h expiry, then never released. This is exactly the pin a
	// process crash leaves behind inside export.go's first 30s pre-renewal
	// window (PinExpiresAt = artifact identity.ExpiresAt = now+24h).
	acquired, err := registry.AcquireExecutionReadPin(ctx, x.ID, "reader-crash-sim", base.Add(24*time.Hour))
	if err != nil || !acquired {
		t.Fatalf("precondition: real AcquireExecutionReadPin(X) failed: acquired=%v err=%v", acquired, err)
	}
	// Deliberately no ReleaseExecutionReadPin — this is the crash.

	ch := &pbxClickHouse{}
	store, err := NewBundleStore(ch, registry)
	if err != nil {
		t.Fatalf("NewBundleStore: %v", err)
	}

	cutoff := time.Now().UTC().Add(-2 * time.Minute)
	reconcileErr := store.Reconcile(ctx, cutoff)

	dropped := ch.droppedTables()
	xRow, xRowErr := registry.GetExecution(ctx, x.ID)
	yRow, yRowErr := registry.GetExecution(ctx, y.ID)
	if xRowErr != nil || yRowErr != nil {
		t.Fatalf("precondition: real rows unreadable after reconcile: X err=%v, Y err=%v", xRowErr, yRowErr)
	}

	pinErr := errors.Is(reconcileErr, pubpublication.ErrExecutionReadPinActive)
	xReclaimed := pbxDropped(dropped, xTable)
	yReclaimed := pbxDropped(dropped, yTable)

	// (a) the pin must be the reason the run errored.
	if !pinErr {
		t.Errorf("FINDING NOT REPRODUCED (abort cause): expected errors.Is(err, ErrExecutionReadPinActive); reconcile returned: %v (dropped=%v)", reconcileErr, dropped)
	}
	// (a)+(c) the collateral: an unrelated, pin-free stale sibling is stranded.
	if pinErr && !xReclaimed && !yReclaimed {
		t.Errorf("CONFIRMED against real Arango (H2-a): one crashed export's never-released read pin on %q aborted the ENTIRE Reconcile run (bundle_store.go:1033-1052 first-error-wins; :1076-1079 propagates claimExecutionCleanup's ErrExecutionReadPinActive). Unrelated, pin-free, equally-stale execution %q was STRANDED: DropTable never issued for it. reconcileErr=%v; X(id=%s table=%s) reclaimed=%v state=%q reconcile-error-field=%q; Y(id=%s table=%s) reclaimed=%v state=%q reconcile-error-field=%q; full dropped set=%v. Contrast control TestStalePinAbsentReclaimsAllAgainstArango: with no pin BOTH %s and %s are dropped by the very same production Reconcile. Startup impact: internal/server/server.go:556-558 turns this one error into recordDegradation + publicationReady=false (bundleTarget stays nil at :560-562, consumed at :823) with NO retry/background re-run of bundle-store Reconcile anywhere in server startup.",
			x.ID, y.ID, reconcileErr, x.ID, xTable, xReclaimed, xRow.State, xRow.Error, y.ID, yTable, yReclaimed, yRow.State, yRow.Error, dropped, xTable, yTable)
	} else if pinErr && !xReclaimed && yReclaimed {
		// A compensating skip-and-continue path would land here. Do not let it
		// pass silently: this would refute the hypothesis and must be visible.
		t.Errorf("UNEXPECTED: pin abort did NOT strand sibling Y (dropped=%v); a compensating path exists in Reconcile — re-examine hypothesis H2-a", dropped)
	}

	if xReclaimed {
		t.Errorf("unexpected: pinned execution X was reclaimed despite the live pin (dropped=%v)", dropped)
	}
	// Y never reached reconcileExecution, so its row is untouched: still FAILED
	// and never stamped with Reconcile's cleanup marker.
	if yRow.State != pubpublication.BundleFailed {
		t.Errorf("unexpected: strayed sibling Y changed state to %q; expected untouched %q", yRow.State, pubpublication.BundleFailed)
	}
	if yRow.Error != "" {
		t.Errorf("unexpected: sibling Y carries reconcile marker %q; expected untouched empty error (proves reconcileExecution never ran for Y)", yRow.Error)
	}
}

// TestStalePinAbsentReclaimsAllAgainstArango is the CONTROL and must PASS: the
// same two stale executions, identical seeding, NO pin. Production Reconcile
// returns nil and BOTH tables are dropped, pinning that the stranding in the
// finding test is caused by the pin rather than harness ordering, paging, or
// execution order.
func TestStalePinAbsentReclaimsAllAgainstArango(t *testing.T) {
	registry, ctx, suffix := pbxSetup(t)

	base := time.Now().UTC()
	xUpdated := base.Add(-70 * time.Minute)
	yUpdated := base.Add(-40 * time.Minute)
	xTable := "loom_bundle_pbx_x_" + suffix
	yTable := "loom_bundle_pbx_y_" + suffix
	x := pbxExecution("X", suffix, xTable, xUpdated)
	y := pbxExecution("Y", suffix, yTable, yUpdated)
	pbxSeed(t, registry, ctx, x)
	pbxSeed(t, registry, ctx, y)

	ch := &pbxClickHouse{}
	store, err := NewBundleStore(ch, registry)
	if err != nil {
		t.Fatalf("NewBundleStore: %v", err)
	}

	cutoff := time.Now().UTC().Add(-2 * time.Minute)
	if err := store.Reconcile(ctx, cutoff); err != nil {
		t.Fatalf("CONTROL: production Reconcile with no pins must succeed, got: %v", err)
	}

	dropped := ch.droppedTables()
	if !pbxDropped(dropped, xTable) {
		t.Fatalf("CONTROL: stale execution X table %q was not reclaimed (dropped=%v)", xTable, dropped)
	}
	if !pbxDropped(dropped, yTable) {
		t.Fatalf("CONTROL: stale execution Y table %q was not reclaimed (dropped=%v)", yTable, dropped)
	}
	xRow, xErr := registry.GetExecution(ctx, x.ID)
	yRow, yErr := registry.GetExecution(ctx, y.ID)
	if xErr != nil || yErr != nil {
		t.Fatalf("CONTROL: real rows unreadable: X err=%v Y err=%v", xErr, yErr)
	}
	if xRow.Error == "" || yRow.Error == "" {
		t.Fatalf("CONTROL: both stale rows must carry the reconcile marker; X.error=%q Y.error=%q", xRow.Error, yRow.Error)
	}
	t.Logf("CONTROL OK: with no pins, the identical production Reconcile reclaimed BOTH stale executions (dropped=%v); each row marked %q. This is the exact sibling that the pin-aborted run in the finding test stranded.", dropped, "stale execution reconciled")
}

// TestLivePinOnOnlyStaleExecutionStillAbortsRunAgainstArango is the
// single-target expected-behavior probe and is EXPECTED TO PASS: only the
// pinned X is stale, so the run abort on ErrExecutionReadPinActive causes no
// collateral. This isolates the collateral in the finding test: it arises only
// when a sibling is present, not from the abort per se.
func TestLivePinOnOnlyStaleExecutionStillAbortsRunAgainstArango(t *testing.T) {
	registry, ctx, suffix := pbxSetup(t)

	base := time.Now().UTC()
	xTable := "loom_bundle_pbx_x_" + suffix
	x := pbxExecution("X", suffix, xTable, base.Add(-70*time.Minute))
	pbxSeed(t, registry, ctx, x)

	acquired, err := registry.AcquireExecutionReadPin(ctx, x.ID, "reader-crash-sim", base.Add(24*time.Hour))
	if err != nil || !acquired {
		t.Fatalf("precondition: real AcquireExecutionReadPin(X) failed: acquired=%v err=%v", acquired, err)
	}
	defer func() { _ = registry.ReleaseExecutionReadPin(context.Background(), x.ID, "reader-crash-sim") }()

	ch := &pbxClickHouse{}
	store, err := NewBundleStore(ch, registry)
	if err != nil {
		t.Fatalf("NewBundleStore: %v", err)
	}

	cutoff := time.Now().UTC().Add(-2 * time.Minute)
	reconcileErr := store.Reconcile(ctx, cutoff)

	dropped := ch.droppedTables()
	if !errors.Is(reconcileErr, pubpublication.ErrExecutionReadPinActive) {
		t.Fatalf("PROBE FAILED: with the only stale execution pinned, Reconcile must return ErrExecutionReadPinActive (claimExecutionCleanup :301-313 refuses while the real ClaimExecutionCleanup AQL :411-427 sees an unexpired pin); got: %v (dropped=%v)", reconcileErr, dropped)
	}
	if pbxDropped(dropped, xTable) {
		t.Fatalf("PROBE FAILED: pinned execution X was reclaimed despite the live pin (dropped=%v)", dropped)
	}
	t.Logf("PROBE OK (single target, no collateral): Reconcile returns ErrExecutionReadPinActive and leaves the pinned stale X untouched. Reconcile's doc (bundle_store.go:1029-1032) says it is 'safe to call repeatedly during startup' — a single legitimately-pinned stale execution that vetoes the entire startup sweep is NOT 'safe' under the finding-test scenario, because the doc implies a repeatable/benign partial sweep. My read: aborting the whole run is defensible ONLY when the pinned execution is the sole candidate (this probe, no collateral). When an unrelated stale sibling exists (finding test) the same all-or-nothing abort converts a legitimate per-execution 'not reclaimable yet' signal (+24h until the pin expires or the process restarts, and re-fails on any restart inside the window) into a deployment-wide publicationReady=false gate (server.go:556-558, no retry path) that strands every other stale execution. Skip-and-continue (collect the pin-active error, keep sweeping, surface it non-fatally) matches the Reconcile doc and the export.go:57 'shorter operation budget' intent")
}
