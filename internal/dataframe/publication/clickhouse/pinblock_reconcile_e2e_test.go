package clickhouse

import (
	"context"
	"sync"
	"testing"
	"time"

	pubpublication "github.com/calypr/loom/internal/dataframe/publication"
	pubarango "github.com/calypr/loom/internal/dataframe/publication/arango"
	"github.com/calypr/loom/internal/store/clickhouse"
	"github.com/calypr/loom/internal/testkit/arangotest"
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
	reconcileErr := store.Reconcile(ctx, time.Now().UTC().Add(-2*time.Minute))
	if reconcileErr != nil {
		t.Fatalf("Reconcile must skip the pinned stale execution and complete without error; got %v (dropped=%v)", reconcileErr, ch.droppedTables())
	}
	if pbxDropped(ch.droppedTables(), xTable) {
		t.Fatalf("Reconcile reclaimed pinned execution X (dropped=%v)", ch.droppedTables())
	}
	t.Log("Reconcile skipped the only pinned candidate and completed without dropping its table")
}
