package clickhouse

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	pubpublication "github.com/calypr/loom/internal/dataframe/publication"
	pubarango "github.com/calypr/loom/internal/dataframe/publication/arango"
	"github.com/calypr/loom/internal/store/clickhouse"
	"github.com/calypr/loom/internal/testkit/arangotest"
)

const runKnownFailuresEnv = "LOOM_RUN_KNOWN_FAILURES"

func skipKnownIssueUnlessEnabled(t *testing.T, issueID string) {
	t.Helper()
	if os.Getenv(runKnownFailuresEnv) != "1" {
		t.Skipf("%s: known failure; set %s=1 to run the red regression assertion", issueID, runKnownFailuresEnv)
	}
}

// These tests exercise reconciliation with a live read pin. The intended
// behavior is to retain the pinned execution and continue reclaiming unrelated
// stale executions. ARC-C13 tracks the current production failure to do so.
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

func pbxSetup(t *testing.T) (*pubarango.Registry, context.Context, string) {
	t.Helper()
	database := arangotest.Owned(t, "loom_verify_pbx_")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	client := database.Client
	if err := client.Bootstrap(ctx, pubarango.BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	registry, err := pubarango.New(client)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	return registry, ctx, database.Name[len("loom_verify_pbx_"):]
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

// TestStalePinBlocksEntireReconcileAgainstArango keeps its historical name.
// With two stale executions and a pin on X, Reconcile should retain X, reclaim
// unrelated Y, and return no error.
func TestStalePinBlocksEntireReconcileAgainstArango(t *testing.T) {
	skipKnownIssueUnlessEnabled(t, "ARC-C13")
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
	// Deliberately no ReleaseExecutionReadPin: this models a crashed reader.

	ch := &pbxClickHouse{}
	store, err := NewBundleStore(ch, registry)
	if err != nil {
		t.Fatalf("NewBundleStore: %v", err)
	}
	reconcileErr := store.Reconcile(ctx, time.Now().UTC().Add(-2*time.Minute))
	dropped := ch.droppedTables()
	_, xRowErr := registry.GetExecution(ctx, x.ID)
	_, yRowErr := registry.GetExecution(ctx, y.ID)
	if xRowErr != nil || yRowErr != nil {
		t.Fatalf("precondition: real rows unreadable after reconcile: X err=%v, Y err=%v", xRowErr, yRowErr)
	}

	if reconcileErr != nil {
		t.Errorf("Reconcile must skip pinned X and complete the remaining sweep without error; got %v (dropped=%v)", reconcileErr, dropped)
	}
	if pbxDropped(dropped, xTable) {
		t.Errorf("Reconcile reclaimed pinned execution X; its table must remain until the read pin expires (dropped=%v)", dropped)
	}
	if !pbxDropped(dropped, yTable) {
		t.Errorf("Reconcile did not reclaim unrelated stale execution Y while X was pinned (dropped=%v)", dropped)
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

// TestLivePinOnOnlyStaleExecutionStillAbortsRunAgainstArango keeps its
// historical name while checking that a single pinned candidate does not abort
// reconciliation or cause its table to be dropped.
func TestLivePinOnOnlyStaleExecutionStillAbortsRunAgainstArango(t *testing.T) {
	skipKnownIssueUnlessEnabled(t, "ARC-C13")
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
	reconcileErr := store.Reconcile(ctx, time.Now().UTC().Add(-2*time.Minute))
	if reconcileErr != nil {
		t.Fatalf("Reconcile must skip the pinned stale execution and complete without error; got %v (dropped=%v)", reconcileErr, ch.droppedTables())
	}
	if pbxDropped(ch.droppedTables(), xTable) {
		t.Fatalf("Reconcile reclaimed pinned execution X (dropped=%v)", ch.droppedTables())
	}
	t.Log("Reconcile skipped the only pinned candidate and completed without dropping its table")
}
