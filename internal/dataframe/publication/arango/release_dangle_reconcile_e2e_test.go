package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
	pubclickhouse "github.com/calypr/loom/internal/dataframe/publication/clickhouse"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataset"
	datasetstore "github.com/calypr/loom/internal/dataset/arango"
	store "github.com/calypr/loom/internal/store/arango"
	storeclickhouse "github.com/calypr/loom/internal/store/clickhouse"
	"github.com/calypr/loom/internal/testkit/arangotest"
	"github.com/google/uuid"
)

// rrdTablePrefix names the physical ClickHouse tables this suite owns, so the
// residue check greps exactly `ch_rrd%` in system.tables.
const rrdTablePrefix = "ch_rrd"

// rrdDatabasePrefix names the owned Arango database; nothing outside it is
// created or dropped.
const rrdDatabasePrefix = "loom_verify_rrd_"

// rrdDatasetDocumentKey replicates internal/dataset/arango/document_codec.go
// documentKey (kind + sha256 over the loom.datasetstore.v1 salt) so this
// package can address the active-release pointer that SaveRelease inserted.
// Fidelity is validated by a production ReadActiveRelease round-trip: a wrong
// key surfaces as ErrNoActiveRelease and fails the test.
func rrdDatasetDocumentKey(kind string, values ...string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("loom.datasetstore.v1"))
	for _, value := range append([]string{kind}, values...) {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(value))
	}
	return kind + "_" + hex.EncodeToString(hash.Sum(nil))
}

type rrdHarness struct {
	ctx               context.Context
	client            *store.Client
	registry          *Registry
	lifecycle         *datasetstore.Store
	ch                *storeclickhouse.Client
	suffix            string
	project           string
	generation        string
	healthy           publication.BundleExecution
	dangle            publication.BundleExecution
	healthyTable      string
	dangleTable       string
	midSeamList       []published.Materialization
	midSeamErrChain   string
	midSeamHealthyIDs []string
	stageErr          error
}

// rrdOpen is the env-gated owned-database harness. It mirrors the sibling
// dangling/name-latest harnesses (t.Skip when LOOM_TEST_ARANGO_URL is unset,
// an owned loom_verify_rrd_<uuid> database dropped via HTTP DELETE in
// t.Cleanup) AND additionally opens a REAL ClickHouse client (matching
// internal/store/clickhouse/integration_test.go env names) so the physical
// tables are genuinely created and genuinely dropped by production DropTable.
func rrdOpen(t *testing.T) *rrdHarness {
	t.Helper()
	database := arangotest.Owned(t, rrdDatabasePrefix)
	h := &rrdHarness{suffix: database.Name[len(rrdDatabasePrefix):]}
	h.project = "rrd-fb12-project"
	h.generation = "rrd-gen-" + h.suffix
	h.healthyTable = rrdTablePrefix + "h_" + h.suffix
	h.dangleTable = rrdTablePrefix + "_" + h.suffix
	h.healthy = rrdExecution("rrd-exec-healthy-"+h.suffix, "rrd-key-healthy-"+h.suffix, h.project, h.generation, "rrd-recipe-a", "out-a", h.healthyTable)
	h.dangle = rrdExecution("rrd-exec-dangle-"+h.suffix, "rrd-key-dangle-"+h.suffix, h.project, h.generation, "rrd-recipe-b", "out-b", h.dangleTable)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	t.Cleanup(cancel)
	h.ctx = ctx
	h.client = database.Client
	if err := h.client.Bootstrap(ctx, BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap bundle collections: %v", err)
	}
	if err := h.client.Bootstrap(ctx, datasetstore.BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap dataset lifecycle collections: %v", err)
	}
	registry, err := New(h.client)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	h.registry = registry
	lifecycle, err := datasetstore.New(h.client)
	if err != nil {
		t.Fatalf("new lifecycle store: %v", err)
	}
	h.lifecycle = lifecycle
	h.ch = arangotest.ClickHouse(t)
	// This runs before the ClickHouse client closes and before Arango cleanup.
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		for _, table := range []string{h.dangleTable, h.healthyTable} {
			if err := h.ch.DropTable(cleanupCtx, table); err != nil {
				t.Errorf("drop clickhouse table %s: %v", table, err)
			} else {
				t.Logf("RRD_CLICKHOUSE_DROPPED=%s", table)
			}
		}
	})
	return h
}

func rrdHex() string { return strings.ReplaceAll(uuid.NewString(), "-", "") }

func rrdExecution(id, key, project, generation, recipe, output, table string) publication.BundleExecution {
	stamp := time.Now().UTC().Truncate(time.Millisecond)
	return publication.BundleExecution{
		ID:  id,
		Key: key,
		BundleIdentity: publication.BundleIdentity{
			Name: recipe, Project: project, DatasetGeneration: generation,
			TranslationVersion: "v1", OutputName: output,
			RecipeDigest: "sha256:" + id + "-recipe", SchemaDigest: "sha256:" + id + "-schema",
			ReceiptID: "receipt-" + id, ScopeDigest: "scope-" + id, EngineVersion: "rrd-engine",
		},
		CreatedAt: stamp.Add(-time.Hour),
		UpdatedAt: stamp.Add(-time.Hour),
	}
}

func rrdSelect(output, recipe string) published.DataframeSelector {
	return published.DataframeSelector{Recipe: recipe, TranslationVersion: "v1", Output: output}
}

// rrdSeedHealthy publishes the healthy execution for real: a genuinely
// queryable PUBLISHED row plus a real ClickHouse table plus its visibility
// pointer. Reconcile leaves it alone (PUBLISHED is not an enumerated state).
func (h *rrdHarness) seedHealthy(t *testing.T) {
	t.Helper()
	owner := "rrd-pub-healthy-" + h.suffix
	if claimed, err := h.registry.AcquireBundleLease(h.ctx, h.healthy.Key, owner, time.Now().UTC().Add(5*time.Minute)); err != nil || !claimed {
		t.Fatalf("healthy acquire lease: claimed=%v err=%v", claimed, err)
	}
	exec := h.healthy
	stamp := time.Now().UTC()
	exec.State = publication.BundlePublished
	exec.OwnerID = owner
	exec.PublishedAt = &stamp
	exec.ReadyAt = &stamp
	exec.UpdatedAt = stamp
	exec.Outputs = []publication.BundleOutputRecord{{
		Name: "out-a", PhysicalTable: h.healthyTable, RowCount: 42, ByteCount: 4200,
		State: publication.BundlePublished, VerifiedAt: &stamp,
		Selector: rrdSelect("out-a", "rrd-recipe-a"),
	}}
	if err := h.registry.SaveExecution(h.ctx, exec, owner); err != nil {
		t.Fatalf("healthy SaveExecution: %v", err)
	}
	if err := h.registry.CompareAndSwapPointer(h.ctx, exec.PointerName(), "", exec.ID); err != nil {
		t.Fatalf("healthy pointer: %v", err)
	}
	_ = h.registry.ReleaseBundleLease(h.ctx, h.healthy.Key, owner)
	h.healthy = exec
}

// rrdSeedDangle writes the candidate as RUNNING (an enumerated state) with a
// queryable-shaped output row carrying the physical table, then leaves its
// bundle lease EXPIRED so the reconciler can claim it (the "stale RUNNING with
// an expired lease" precondition). No pointer is written: that is the whole
// point of the guard.
func (h *rrdHarness) seedDangle(t *testing.T) {
	t.Helper()
	owner := "rrd-pub-dangle-" + h.suffix
	if claimed, err := h.registry.AcquireBundleLease(h.ctx, h.dangle.Key, owner, time.Now().UTC().Add(5*time.Minute)); err != nil || !claimed {
		t.Fatalf("dangle acquire lease: claimed=%v err=%v", claimed, err)
	}
	stamp := time.Now().UTC().Truncate(time.Millisecond)
	exec := h.dangle
	exec.State = publication.BundleRunning
	exec.OwnerID = owner
	exec.UpdatedAt = stamp.Add(-time.Hour)
	exec.Outputs = []publication.BundleOutputRecord{{
		Name: "out-b", PhysicalTable: h.dangleTable, RowCount: 7, ByteCount: 700,
		State: publication.BundleRunning, Selector: rrdSelect("out-b", "rrd-recipe-b"),
	}}
	if err := h.registry.SaveExecution(h.ctx, exec, owner); err != nil {
		t.Fatalf("dangle SaveExecution: %v", err)
	}
	// Make the seed-held lease expired (same owner refresh) and leave it in
	// place: reconcileExecution's AcquireBundleLease steals an expired lease.
	if claimed, err := h.registry.AcquireBundleLease(h.ctx, h.dangle.Key, owner, time.Now().UTC().Add(-time.Minute)); err != nil || !claimed {
		t.Fatalf("dangle expire lease: claimed=%v err=%v", claimed, err)
	}
	h.dangle = exec
}

// rrdCreatePhysicalTable creates the real MergeTree table so the reconciler's
// DropTable has something real to physically remove.
func (h *rrdHarness) createPhysicalTable(t *testing.T, table string) {
	t.Helper()
	if err := h.ch.CreateTable(h.ctx, table, []storeclickhouse.Column{{Name: "__loom_row_id", Type: "Int64"}}); err != nil {
		t.Fatalf("create clickhouse table %s: %v", table, err)
	}
}

func (h *rrdHarness) listRRDTables(t *testing.T) []string {
	t.Helper()
	var names []string
	err := h.ch.QueryRowsArgsVisit(h.ctx,
		fmt.Sprintf("SELECT name FROM system.tables WHERE database = currentDatabase() AND startsWith(name, '%s')", rrdTablePrefix),
		[]string{"name"}, func(row map[string]any) error {
			if name, ok := row["name"].(string); ok {
				names = append(names, name)
			}
			return nil
		})
	if err != nil {
		t.Fatalf("query system.tables: %v", err)
	}
	return names
}

// rrdActivateRelease saves an immutable release that binds BOTH executions and
// activates it by patching the active_release placeholder SaveRelease created
// (release_store.go saveReleaseAQL / readActiveReleaseAQL). It
// is validated by production ReadActiveRelease, so a bad key/round-trip fails.
func (h *rrdHarness) activateRelease(t *testing.T, bindings []dataset.ReleasePublication) dataset.ProjectRelease {
	t.Helper()
	release := dataset.ProjectRelease{
		ID: "release_" + rrdHex(), Project: h.project,
		GitCommit: h.generation, Generation: h.generation,
		Publications: bindings, CreatedAt: time.Now().UTC(),
	}
	saved, err := h.lifecycle.SaveRelease(h.ctx, release)
	if err != nil {
		t.Fatalf("SaveRelease: %v", err)
	}
	releaseKey := rrdDatasetDocumentKey("release", release.ID)
	activeKey := rrdDatasetDocumentKey("active_release", release.Project)
	activated := false
	err = h.client.QueryRows(h.ctx, `LET pointer = DOCUMENT(@@collection, @activeKey)
FILTER pointer != null AND pointer.recordType == "active_project_release"
UPDATE pointer WITH {releaseKey: @releaseKey, releaseId: @releaseId, revision: 1} IN @@collection
RETURN {activated: true}`, 32, map[string]interface{}{
		"@collection": datasetstore.LifecycleCollection, "activeKey": activeKey,
		"releaseKey": releaseKey, "releaseId": release.ID,
	}, func(row map[string]any) error {
		activated, _ = row["activated"].(bool)
		return nil
	})
	if err != nil || !activated {
		t.Fatalf("activate release: activated=%v err=%v", activated, err)
	}
	active, err := h.lifecycle.ReadActiveRelease(h.ctx, h.project)
	if err != nil {
		t.Fatalf("ReadActiveRelease round-trip (key derivation check): %v", err)
	}
	if active.Release.ID != saved.ID || active.Release.Generation != h.generation {
		t.Fatalf("active release round-trip mismatch: %+v", active)
	}
	return saved
}

// rrdStagePublicDocVisibleWithoutPointer writes the candidate's exec doc to a
// queryable PUBLISHED row via a direct AQL patch. This is PublishExecution's
// FIRST doc of its two-doc (exec-state + pointer) publication: the exec-state
// is made visible while the visibility POINTER doc is deliberately left
// ABSENT, exactly the non-atomic window that lets reconcileExecution's
// pre-claim GetPointer guard observe nothing to skip on.
func (h *rrdHarness) stagePublishedDocWithoutPointer(t *testing.T) {
	t.Helper()
	stamp := time.Now().UTC()
	published := h.dangle
	published.State = publication.BundlePublished
	published.PublishedAt = &stamp
	published.ReadyAt = &stamp
	published.UpdatedAt = stamp
	published.OwnerID = ""
	published.Outputs = []publication.BundleOutputRecord{{
		Name: "out-b", PhysicalTable: h.dangleTable, RowCount: 7, ByteCount: 700,
		State: publication.BundlePublished, VerifiedAt: &stamp,
		Selector: rrdSelect("out-b", "rrd-recipe-b"),
	}}
	doc, err := json.Marshal(published)
	if err != nil {
		t.Fatalf("marshal published doc: %v", err)
	}
	var patch map[string]any
	if err := json.Unmarshal(doc, &patch); err != nil {
		t.Fatalf("unmarshal published doc: %v", err)
	}
	delete(patch, "_key")
	updated := false
	err = h.client.QueryRows(h.ctx, `LET doc = DOCUMENT(@@collection, @key)
FILTER doc != null
UPDATE doc WITH @patch IN @@collection
RETURN {updated: true}`, 32, map[string]interface{}{
		"@collection": BundleExecutionsCollection, "key": published.ID, "patch": patch,
	}, func(map[string]any) error { updated = true; return nil })
	if err != nil || !updated {
		t.Fatalf("stage published exec doc: updated=%v err=%v", updated, err)
	}
	if pointer, err := h.registry.GetPointer(h.ctx, published.PointerName()); !errors.Is(err, publication.ErrBundleNotFound) {
		t.Fatalf("precondition: pointer must remain absent during the two-doc window, got %+v err=%v", pointer, err)
	}
}

// rrdCatalog is the seam hook. It embeds the real *Registry and overrides
// exactly the exported enumerate/candidate cleanup claim: reconcileExecution's
// pre-claim pointer guard has ALREADY passed on the absent pointer by the
// time ClaimExecutionCleanup runs, so staging the racing publish + release
// Create+Activate here reproduces the F-B12 interleaving deterministically,
// with NO goroutine. Every other capability is the production AQL verbatim.
type rrdCatalog struct {
	*Registry
	fired bool
	onRun func(executionID string)
}

func (c *rrdCatalog) ClaimExecutionCleanup(ctx context.Context, executionID, owner string) (bool, error) {
	if !c.fired && c.onRun != nil {
		c.fired = true
		c.onRun(executionID)
	}
	return c.Registry.ClaimExecutionCleanup(ctx, executionID, owner)
}

func rrdErrorChain(err error) string {
	if err == nil {
		return "<nil>"
	}
	parts := make([]string, 0, 4)
	for cur := err; cur != nil; cur = errors.Unwrap(cur) {
		parts = append(parts, cur.Error())
	}
	return strings.Join(parts, " <- ")
}

func rrdUserErrorCode(err error) string {
	if user, ok := dataframeerrors.AsUserError(err); ok {
		return user.Code()
	}
	return ""
}

// Active-release bindings must protect execution tables during reconciliation.
// The controlled interleaving also covers retained non-lease-fenced records.
func TestReconcileDanglesActiveReleaseAgainstArangoClickHouse(t *testing.T) {
	h := rrdOpen(t)

	// Seed everything for real.
	h.seedHealthy(t)
	h.seedDangle(t)
	h.createPhysicalTable(t, h.healthyTable)
	h.createPhysicalTable(t, h.dangleTable)

	t.Log("EXPECT O-TABLES: both owned ClickHouse tables exist before reconcile")
	before := h.listRRDTables(t)
	t.Logf("OBSERVED O-TABLES before reconcile: %v", before)
	if !contains(before, h.dangleTable) || !contains(before, h.healthyTable) {
		t.Fatalf("precondition: both owned tables must exist, got %v", before)
	}

	// (2a) Capture the enumerate/candidate state through the EXPORTED enumerate
	// (not a goroutine): the same VisitExecutionPages Reconcile drives per state.
	t.Log("EXPECT O-ENUM: exported RUNNING enumerate yields exactly the dangle execution (healthy is PUBLISHED, never enumerated)")
	candidates := rrdEnumerate(t, h.registry, publication.BundleRunning)
	var candidateStates []string
	for _, c := range candidates {
		candidateStates = append(candidateStates, fmt.Sprintf("{ID=%q State=%q Table=%q}", c.ID, c.State, tableOf(c)))
	}
	_, sawDangle := candidates[h.dangle.ID]
	_, sawHealthy := candidates[h.healthy.ID]
	t.Logf("OBSERVED O-ENUM RUNNING candidates: %v", candidateStates)
	if !sawDangle {
		t.Fatalf("O-ENUM violated: reconciler would never enumerate the stale RUNNING candidate: %v", candidateStates)
	}
	if sawHealthy {
		t.Fatalf("O-ENUM violated: healthy PUBLISHED execution was enumerated as a drop candidate: %v", candidateStates)
	}

	// Independent healthy baseline: prove a PUBLISHED + queryable execution
	// served by the active release WOULD resolve, so any later whole-list
	// failure is provably caused by the dangle drop, not by a bad seed.
	reader := &published.Reader{Catalog: h.registry, ActiveReleaseResolver: h.lifecycle}

	readerHarnessForBaseline := func(bindings []dataset.ReleasePublication) {
		h.activateRelease(t, bindings)
	}

	// Bindings reused for the mid-seam release. Capture the healthy baseline
	// first using a throwaway release activation to confirm the healthy binding
	// alone resolves (this activation is later overwritten by the seam).
	healthyOnly := []dataset.ReleasePublication{
		{Selector: rrdSelect("out-a", "rrd-recipe-a"), ExecutionID: h.healthy.ID, Generation: h.generation, VerifiedAt: time.Now().UTC()},
	}
	readerHarnessForBaseline(healthyOnly)
	healthBase, baseErr := reader.CurrentProjectDatasets(h.ctx, h.project)
	t.Logf("OBSERVED healthy-baseline (release binds only the healthy exec): list=%d err=[%s]", len(healthBase), rrdErrorChain(baseErr))
	if baseErr != nil || len(healthBase) != 1 || healthBase[0].Name != "out-a" {
		t.Fatalf("healthy-baseline violated: a genuine PUBLISHED binding must resolve: %d %+v err=%v", len(healthBase), healthBase, baseErr)
	}

	// Build the store over the seam-wrapped catalog. The candidate is RUNNING
	// with an expired lease, so reconcileExecution proceeds to the drop path.
	// Lease TTL stays at its 2-minute default so the fenced SaveExecution(FAILED)
	// at the claim step actually lands (the demotion must be durable, not a lost fence).
	wrapper := &rrdCatalog{Registry: h.registry}
	wrapper.onRun = func(executionID string) {
		if executionID != h.dangle.ID {
			return
		}
		// This runs AFTER reconcileExecution's GetPointer guard has already
		// returned ABSENT (so the guard has nothing to skip on) and BEFORE the
		// real claim + drop + demotion. Simulate PublishExecution's first doc +
		// the release Create+Activate landing on top of the in-flight reconcile.
		h.stagePublishedDocWithoutPointer(t)
		bindings := []dataset.ReleasePublication{
			{Selector: rrdSelect("out-a", "rrd-recipe-a"), ExecutionID: h.healthy.ID, Generation: h.generation, VerifiedAt: time.Now().UTC()},
			{Selector: rrdSelect("out-b", "rrd-recipe-b"), ExecutionID: h.dangle.ID, Generation: h.generation, VerifiedAt: time.Now().UTC()},
		}
		h.activateRelease(t, bindings)

		// Mid-seam read: both bindings genuinely resolvable right now (candidate
		// published + queryable, its real table not yet dropped).
		mid, midErr := reader.CurrentProjectDatasets(h.ctx, h.project)
		h.midSeamErrChain = rrdErrorChain(midErr)
		for _, m := range mid {
			h.midSeamHealthyIDs = append(h.midSeamHealthyIDs, m.Name)
		}
		if midErr != nil || len(mid) != 2 {
			h.stageErr = fmt.Errorf("O-MID violated: mid-seam read expected 2 materializations/nil, got %d err=[%s]", len(mid), rrdErrorChain(midErr))
		}
	}

	store, err := pubclickhouse.NewBundleStore(h.ch, wrapper)
	if err != nil {
		t.Fatalf("NewBundleStore: %v", err)
	}
	// Production wiring mirror (server.go): the reconciler re-checks the
	// ACTIVE release bindings immediately before any drop (Repair-1b).
	store.SetActiveReleaseReader(h.lifecycle)

	// The candidate's pointer is absent when the pass arms: the guard reads
	// nothing to skip on. This is the guard's failure mode (an absent pointer is
	// treated as "not currently advertised" -> proceed), not an error.
	t.Log("EXPECT pre-reconcile: candidate pointer is ABSENT, so the pre-claim pointer guard cannot skip the drop")
	if p, err := h.registry.GetPointer(h.ctx, h.dangle.PointerName()); !errors.Is(err, publication.ErrBundleNotFound) {
		t.Fatalf("precondition: candidate pointer must be absent before reconcile, got %+v err=%v", p, err)
	}
	t.Log("OBSERVED pre-reconcile: candidate pointer ABSENT (GetPointer -> ErrBundleNotFound)")

	t.Log("ACTION: run production Reconcile over the expired-lease RUNNING candidate")
	reconcileErr := store.Reconcile(h.ctx, time.Now().UTC().Add(2*time.Minute))
	t.Logf("OBSERVED Reconcile returned: %v (staged seam error: %v)", rrdErrorChain(reconcileErr), h.stageErr)
	if h.stageErr != nil {
		t.Fatalf("%v", h.stageErr)
	}
	if reconcileErr != nil {
		t.Fatalf("production Reconcile failed: %v", reconcileErr)
	}

	if !wrapper.fired {
		t.Errorf("DISPROVEN (staging): reconcileExecution never reached the exported ClaimExecutionCleanup seam for the candidate - the drop path was never reached; verify the candidate enumerated RUNNING and its lease was claimable")
		return
	}

	// OBSERVED after reconcile.
	execNow, execErr := h.registry.GetExecution(h.ctx, h.dangle.ID)
	if execErr != nil {
		t.Fatalf("read candidate execution after reconcile: %v", execErr)
	}
	after := h.listRRDTables(t)
	dangleGone := !contains(after, h.dangleTable)
	healthyPresent := contains(after, h.healthyTable)
	t.Log("EXPECT O-CONF: candidate State==FAILED, Outputs==[], its real CH table is physically gone, active release still binds it, CurrentProjectDatasets -> nil+error")
	active, activeErr := h.lifecycle.ReadActiveRelease(h.ctx, h.project)
	if activeErr != nil {
		t.Fatalf("ReadActiveRelease after reconcile: %v", activeErr)
	}
	releaseBindsDangle := false
	releaseBindsHealthy := false
	for _, b := range active.Release.Publications {
		if !b.Stale && b.Generation == active.Release.Generation {
			if b.ExecutionID == h.dangle.ID {
				releaseBindsDangle = true
			}
			if b.ExecutionID == h.healthy.ID {
				releaseBindsHealthy = true
			}
		}
	}
	finalList, finalErr := reader.CurrentProjectDatasets(h.ctx, h.project)
	t.Logf("OBSERVED candidate after reconcile: State=%q Outputs=%d err=[%s]; candidate table %q gone=%v; healthy table %q present=%v; remaining rrd tables=%v",
		execNow.State, len(execNow.Outputs), rrdErrorChain(execErr), h.dangleTable, dangleGone, h.healthyTable, healthyPresent, after)
	t.Logf("OBSERVED active release still binds candidate=%v healthy=%v (bound ExecutionIDs: %s, revision=%d)",
		releaseBindsDangle, releaseBindsHealthy, rrdBoundIDs(active), active.Revision)
	t.Logf("OBSERVED final CurrentProjectDatasets: list=%d err=[%s] userCode=%q", len(finalList), rrdErrorChain(finalErr), rrdUserErrorCode(finalErr))

	// Independent confirmation that the whole-list loss is attributable to the
	// dangle binding, not to the healthy one: the healthy row + table survive.
	healthNow, healthNowErr := h.registry.GetExecution(h.ctx, h.healthy.ID)
	healthyStillServes := healthNowErr == nil && healthNow.State.Successful() && healthyPresent

	// Prove the release-read path never consults the pointer: write the
	// candidate's pointer LAST (PublishExecution's second doc eventually
	// completes) naming the now-FAILED execution. The read still must fail.
	if err := h.registry.CompareAndSwapPointer(h.ctx, h.dangle.PointerName(), "", h.dangle.ID); err != nil {
		t.Fatalf("release-dangle pointer-last write: %v", err)
	}
	if p, err := h.registry.GetPointer(h.ctx, h.dangle.PointerName()); err != nil || p.ExecutionID != h.dangle.ID {
		t.Fatalf("pointer-last write not observable: %+v err=%v", p, err)
	}
	finalAfterPointer, errAfterPointer := reader.CurrentProjectDatasets(h.ctx, h.project)
	t.Logf("OBSERVED after pointer-LAST wrote the FAILED candidate: list=%d err=[%s] (reader ignored the pointer; current.go has only one GetPointer call, in the non-release path)",
		len(finalAfterPointer), rrdErrorChain(errAfterPointer))

	activeBindingDamaged := releaseBindsDangle && (dangleGone || execNow.State != publication.BundlePublished || len(execNow.Outputs) == 0)

	if activeBindingDamaged {
		t.Errorf("F-B12 regression: active release revision=%d still binds execution %q, but reconcile left state=%q outputs=%d tableGone=%v (table=%s); bound IDs=%s",
			active.Revision, h.dangle.ID, execNow.State, len(execNow.Outputs), dangleGone, h.dangleTable, rrdBoundIDs(active))
	}
	if !releaseBindsHealthy || !healthyStillServes || !healthyPresent {
		t.Errorf("healthy release binding was damaged: binds=%v serves=%v tablePresent=%v", releaseBindsHealthy, healthyStillServes, healthyPresent)
	}
	if finalErr != nil {
		t.Errorf("CurrentProjectDatasets failed despite optional-binding isolation: list=%d err=[%s]", len(finalList), rrdErrorChain(finalErr))
	}
	if !activeBindingDamaged && releaseBindsHealthy && healthyStillServes && healthyPresent && finalErr == nil {
		prevent, detail := rrdDiagnose(execNow, execErr, dangleGone, healthyPresent, releaseBindsDangle, finalList, finalErr, reconcileErr)
		t.Logf("PASS: active-release-bound execution remained queryable and current after reconciliation. Guard=%s (%s); candidate state=%q outputs=%d tableGone=%v active release binds candidate=%v final list=%d err=[%s]",
			prevent, detail, execNow.State, len(execNow.Outputs), dangleGone, releaseBindsDangle, len(finalList), rrdErrorChain(finalErr))
	}

	// Residue: after this function the cleanup handlers drop the tables; log the
	// live state so a reviewer sees exactly what remains to be reported.
	t.Logf("RRD_RESIDUE_CLICKHOUSE_TABLES=%v", h.listRRDTables(t))
}

func rrdEnumerate(t *testing.T, registry *Registry, state publication.BundleState) map[string]publication.BundleExecution {
	t.Helper()
	out := map[string]publication.BundleExecution{}
	err := registry.VisitExecutionPages(context.Background(), state, time.Now().UTC().Add(time.Minute), 64, func(page []publication.BundleExecution) error {
		for _, e := range page {
			out[e.ID] = e
		}
		return nil
	})
	if err != nil {
		t.Fatalf("exported VisitExecutionPages(%s): %v", state, err)
	}
	return out
}

func tableOf(e publication.BundleExecution) string {
	if len(e.Outputs) == 0 {
		return ""
	}
	return e.Outputs[0].PhysicalTable
}

func rrdBoundIDs(active dataset.ActiveRelease) string {
	parts := make([]string, 0, len(active.Release.Publications))
	for _, b := range active.Release.Publications {
		parts = append(parts, fmt.Sprintf("%s->%s", b.Selector.Output, b.ExecutionID))
	}
	return strings.Join(parts, " ")
}

// rrdDiagnose names the exact load-bearing line that prevented the drop so a
// DISPROVEN verdict cites the mechanism rather than merely asserting it.
func rrdDiagnose(exec publication.BundleExecution, execErr error, dangleGone, healthyPresent, releaseBindsDangle bool, list []published.Materialization, listErr, reconcileErr error) (string, string) {
	switch {
	case dangleGone && exec.State == publication.BundleFailed && releaseBindsDangle:
		return "active-release cleanup veto",
			"the active release still binds an execution whose ClickHouse table was physically deleted and whose state is FAILED"
	case !releaseBindsDangle:
		return "release activation (release_store.go)",
			"the active release no longer binds the candidate after reconcile, so the read path cannot total-fail; inspect the seam's activateRelease round-trip"
	case exec.State != publication.BundleFailed:
		return "reconcileExecution guard chain: pre-claim GetPointer skip, cleanup-lease claim, or the post-claim live re-check (re-read pointer + ACTIVE-release binding veto, Repair-1b)",
			fmt.Sprintf("candidate was NOT demoted (state=%q): reconcileExecution skipped it before DropTable. If the seam made GetPointer name it, the guard skipped; if the pointer stayed absent, the post-claim live release-binding veto (reconcileExecution, Repair-1b) is the preventer quoting this line; if neither, the cleanup lease was not claimable - the staging premise (absent pointer + expired claimable lease) did not hold", exec.State)
	case !dangleGone:
		return "reconcileExecution DropTable loop (post-claim)",
			"the candidate's real ClickHouse table is still present, so DropTable never removed it; inspect the physical-table create and the claim path"
	default:
		return "unclassified",
			"none of the expected guard conditions produced a clean classification; inspect OBSERVED lines above"
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
