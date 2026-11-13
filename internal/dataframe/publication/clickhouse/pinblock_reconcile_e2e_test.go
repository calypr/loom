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

	arangofactory "github.com/calypr/loom/internal/backends/arango"
	pubpublication "github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
)

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

func pbxSetup(t *testing.T) (*arangofactory.PublishedRegistry, context.Context, string) {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	database := "loom_verify_pbx_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	t.Logf("owned arango database: %s", database)
	arangoBackend, err := arangofactory.Open(ctx, arangofactory.Config{URL: endpoint, Database: database})
	if err != nil {
		t.Fatalf("open arango: %v", err)
	}
	client := arangoBackend.Client
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		pbxCleanupDatabase(t, endpoint, database)
	})
	if err := client.Bootstrap(ctx, arangofactory.PublishedBootstrapSpec()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	registry, err := arangofactory.NewPublishedRegistry(client)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	return registry, ctx, database[len("loom_verify_pbx_"):]
}

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

func pbxSeed(t *testing.T, registry *arangofactory.PublishedRegistry, ctx context.Context, exec pubpublication.BundleExecution) {
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

	acquired, err := registry.AcquireExecutionReadPin(ctx, x.ID, "reader-crash-sim", base.Add(24*time.Hour))
	if err != nil || !acquired {
		t.Fatalf("precondition: real AcquireExecutionReadPin(X) failed: acquired=%v err=%v", acquired, err)
	}

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

	if !pinErr {
		t.Errorf("FINDING NOT REPRODUCED (abort cause): expected errors.Is(err, ErrExecutionReadPinActive); reconcile returned: %v (dropped=%v)", reconcileErr, dropped)
	}
	if pinErr && !xReclaimed && !yReclaimed {
		t.Errorf("CONFIRMED against real Arango (H2-a): one crashed export's never-released read pin on %q aborted the ENTIRE Reconcile run (bundle_store.go:1033-1052 first-error-wins; :1076-1079 propagates claimExecutionCleanup's ErrExecutionReadPinActive). Unrelated, pin-free, equally-stale execution %q was STRANDED: DropTable never issued for it. reconcileErr=%v; X(id=%s table=%s) reclaimed=%v state=%q reconcile-error-field=%q; Y(id=%s table=%s) reclaimed=%v state=%q reconcile-error-field=%q; full dropped set=%v. Contrast control TestStalePinAbsentReclaimsAllAgainstArango: with no pin BOTH %s and %s are dropped by the very same production Reconcile. Startup impact: internal/server/server.go:556-558 turns this one error into recordDegradation + publicationReady=false (bundleTarget stays nil at :560-562, consumed at :823) with NO retry/background re-run of bundle-store Reconcile anywhere in server startup.",
			x.ID, y.ID, reconcileErr, x.ID, xTable, xReclaimed, xRow.State, xRow.Error, y.ID, yTable, yReclaimed, yRow.State, yRow.Error, dropped, xTable, yTable)
	} else if pinErr && !xReclaimed && yReclaimed {
		t.Errorf("UNEXPECTED: pin abort did NOT strand sibling Y (dropped=%v); a compensating path exists in Reconcile — re-examine hypothesis H2-a", dropped)
	}

	if xReclaimed {
		t.Errorf("unexpected: pinned execution X was reclaimed despite the live pin (dropped=%v)", dropped)
	}
	if yRow.State != pubpublication.BundleFailed {
		t.Errorf("unexpected: strayed sibling Y changed state to %q; expected untouched %q", yRow.State, pubpublication.BundleFailed)
	}
	if yRow.Error != "" {
		t.Errorf("unexpected: sibling Y carries reconcile marker %q; expected untouched empty error (proves reconcileExecution never ran for Y)", yRow.Error)
	}
}

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
