package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
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

const rrdTablePrefix = "ch_rrd"

const rrdDatabasePrefix = "loom_verify_rrd_"

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
	midSeamErrChain   string
	midSeamHealthyIDs []string
	stageErr          error
}

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

func rrdSelect(output, recipe string) dataset.DataframeSelector {
	return dataset.DataframeSelector{Recipe: recipe, TranslationVersion: "v1", Output: output}
}

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

	if claimed, err := h.registry.AcquireBundleLease(h.ctx, h.dangle.Key, owner, time.Now().UTC().Add(-time.Minute)); err != nil || !claimed {
		t.Fatalf("dangle expire lease: claimed=%v err=%v", claimed, err)
	}
	h.dangle = exec
}

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

func TestReconcileDanglesActiveReleaseAgainstArangoClickHouse(t *testing.T) {
	h := rrdOpen(t)

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

	reader := &published.Reader{Catalog: h.registry, ActiveReleaseResolver: h.lifecycle}

	readerHarnessForBaseline := func(bindings []dataset.ReleasePublication) {
		h.activateRelease(t, bindings)
	}

	healthyOnly := []dataset.ReleasePublication{
		{Selector: rrdSelect("out-a", "rrd-recipe-a"), ExecutionID: h.healthy.ID, Generation: h.generation, VerifiedAt: time.Now().UTC()},
	}
	readerHarnessForBaseline(healthyOnly)
	healthBase, baseErr := reader.CurrentProjectDatasets(h.ctx, h.project)
	t.Logf("OBSERVED healthy-baseline (release binds only the healthy exec): list=%d err=[%s]", len(healthBase), rrdErrorChain(baseErr))
	if baseErr != nil || len(healthBase) != 1 || healthBase[0].Name != "out-a" {
		t.Fatalf("healthy-baseline violated: a genuine PUBLISHED binding must resolve: %d %+v err=%v", len(healthBase), healthBase, baseErr)
	}

	wrapper := &rrdCatalog{Registry: h.registry}
	wrapper.onRun = func(executionID string) {
		if executionID != h.dangle.ID {
			return
		}

		h.stagePublishedDocWithoutPointer(t)
		bindings := []dataset.ReleasePublication{
			{Selector: rrdSelect("out-a", "rrd-recipe-a"), ExecutionID: h.healthy.ID, Generation: h.generation, VerifiedAt: time.Now().UTC()},
			{Selector: rrdSelect("out-b", "rrd-recipe-b"), ExecutionID: h.dangle.ID, Generation: h.generation, VerifiedAt: time.Now().UTC()},
		}
		h.activateRelease(t, bindings)

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

	store.SetActiveReleaseReader(h.lifecycle)

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

	healthNow, healthNowErr := h.registry.GetExecution(h.ctx, h.healthy.ID)
	healthyStillServes := healthNowErr == nil && healthNow.State.Successful() && healthyPresent

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

type publishCaptureClient struct {
	testing *testing.T
}

func (c publishCaptureClient) InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error {
	return nil
}

func (c publishCaptureClient) WithTransaction(ctx context.Context, collections store.TransactionCollections, fn store.TransactionFunc) error {
	if len(collections.Write) != 3 || collections.Write[0] != BundleExecutionsCollection || collections.Write[1] != BundlePointersCollection || collections.Write[2] != BundleLeasesCollection {
		c.testing.Fatalf("publish transaction must declare executions+pointers+leases as write collections, got %#v", collections)
	}
	return fn(ctx, c)
}

func (c publishCaptureClient) QueryRows(_ context.Context, query string, _ int, bindVars map[string]interface{}, visit store.RowVisitor) error {
	for _, fragment := range []string{"LET lease = DOCUMENT(@@leases, @leaseKey)", "lease.ownerId == @owner", "lease.expiresAt >= @now"} {
		if !strings.Contains(query, fragment) {
			c.testing.Fatalf("publication query lacks lease guard %q", fragment)
		}
	}
	if bindVars["@leases"] != BundleLeasesCollection || bindVars["leaseKey"] != "bundle-key" || bindVars["owner"] != "publisher-a" {
		c.testing.Fatalf("publication lease bindings = %#v", bindVars)
	}
	return visit(map[string]any{"updated": true})
}

func TestPointerDocumentKeyAcceptsLogicalNamesWithNULSeparators(t *testing.T) {
	name := "HTAN_INT-BForePC\x00\x00aced-meta-default"
	first := pointerDocumentKey(name)
	second := pointerDocumentKey(name)
	if first != second {
		t.Fatalf("pointer key is not deterministic: %q != %q", first, second)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(first) {
		t.Fatalf("pointer key is not an Arango-safe SHA256: %q", first)
	}
}

func TestPublishExecutionRequiresCurrentLeaseOwner(t *testing.T) {
	registry, err := New(publishCaptureClient{testing: t})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	execution := publication.BundleExecution{
		ID: "execution-a", Key: "bundle-key", OwnerID: "publisher-a",
		State: publication.BundlePublished, UpdatedAt: now,
	}
	if err := registry.PublishExecution(context.Background(), "project\x00generation\x00recipe", "", execution); err != nil {
		t.Fatal(err)
	}
}

type saveExecutionClient struct {
	allowSave bool
}

func (c saveExecutionClient) InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error {
	return nil
}

func (c saveExecutionClient) QueryRows(_ context.Context, query string, _ int, bindVars map[string]interface{}, visit store.RowVisitor) error {
	for _, fragment := range []string{"LET lease = DOCUMENT(@@leases, @leaseKey)", "lease.ownerId == @owner", "lease.expiresAt >= @now", "UPSERT {_key: @executionKey}"} {
		if !strings.Contains(query, fragment) {
			return errors.New("fenced execution query is missing " + fragment)
		}
	}
	if bindVars["leaseKey"] != "bundle-key" || bindVars["owner"] != "publisher-a" || bindVars["executionKey"] != "execution-a" {
		return errors.New("fenced execution bindings are incorrect")
	}
	if c.allowSave {
		return visit(map[string]any{"saved": true})
	}
	return nil
}

func TestSaveExecutionRejectsCheckpointAfterLeaseTakeover(t *testing.T) {
	registry, err := New(saveExecutionClient{allowSave: true})
	if err != nil {
		t.Fatal(err)
	}
	execution := publication.BundleExecution{ID: "execution-a", Key: "bundle-key", OwnerID: "publisher-a"}
	if err := registry.SaveExecution(context.Background(), execution, execution.OwnerID); err != nil {
		t.Fatal(err)
	}

	registry, err = New(saveExecutionClient{allowSave: false})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.SaveExecution(context.Background(), execution, execution.OwnerID); !errors.Is(err, publication.ErrBundleLeaseLost) {
		t.Fatalf("SaveExecution() error = %v, want lease loss", err)
	}
}

type legacyExecutionClient struct {
	savedOwner string
}

func (c *legacyExecutionClient) InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error {
	return nil
}

func (c *legacyExecutionClient) QueryRows(_ context.Context, query string, _ int, bindVars map[string]interface{}, visit store.RowVisitor) error {
	if strings.Contains(query, "UPSERT {_key: @executionKey}") {
		if bindVars["owner"] != "publisher-a" {
			return errors.New("legacy recovery did not use the active lease owner")
		}
		c.savedOwner = bindVars["owner"].(string)
		return visit(map[string]any{"saved": true})
	}
	if !strings.Contains(query, "FILTER doc._key == @key") {
		return errors.New("unexpected legacy execution query")
	}
	return visit(map[string]any{
		"id":        "legacy-execution",
		"key":       "legacy-bundle",
		"name":      "legacy-recipe",
		"state":     "RUNNING",
		"updatedAt": time.Now().UTC(),
	})
}

func TestLegacyExecutionWithoutOwnerCanBeReadAndFencedForRecovery(t *testing.T) {
	client := &legacyExecutionClient{}
	registry, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := registry.GetExecution(context.Background(), "legacy-execution")
	if err != nil {
		t.Fatal(err)
	}
	if execution.OwnerID != "" {
		t.Fatalf("legacy execution owner = %q, want empty owner", execution.OwnerID)
	}
	if err := registry.SaveExecution(context.Background(), execution, "publisher-a"); err != nil {
		t.Fatal(err)
	}
	if client.savedOwner != "publisher-a" {
		t.Fatalf("recovery fence owner = %q, want publisher-a", client.savedOwner)
	}
}

type executionPageClient struct {
	rows  [][]map[string]any
	binds []map[string]interface{}
}

func (c *executionPageClient) InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error {
	return nil
}

func (c *executionPageClient) QueryRows(_ context.Context, query string, _ int, bindVars map[string]interface{}, visit store.RowVisitor) error {
	if !strings.Contains(query, "SORT doc.updatedAt ASC, doc._key ASC") || !strings.Contains(query, "LIMIT @limit") {
		return errors.New("execution page query is not stably ordered and bounded")
	}
	copied := make(map[string]interface{}, len(bindVars))
	for key, value := range bindVars {
		copied[key] = value
	}
	c.binds = append(c.binds, copied)
	page := c.rows[0]
	c.rows = c.rows[1:]
	for _, row := range page {
		if err := visit(row); err != nil {
			return err
		}
	}
	return nil
}

func TestVisitExecutionPagesUsesStableContinuation(t *testing.T) {
	firstUpdated := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	secondUpdated := firstUpdated.Add(time.Second)
	thirdUpdated := secondUpdated.Add(time.Second)
	client := &executionPageClient{rows: [][]map[string]any{
		{
			{"_key": "execution-a", "id": "execution-a", "key": "bundle-a", "state": "RUNNING", "updatedAt": firstUpdated},
			{"_key": "execution-b", "id": "execution-b", "key": "bundle-b", "state": "RUNNING", "updatedAt": secondUpdated},
		},
		{{"_key": "execution-c", "id": "execution-c", "key": "bundle-c", "state": "RUNNING", "updatedAt": thirdUpdated}},
	}}
	registry, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	var executions []publication.BundleExecution
	if err := registry.VisitExecutionPages(context.Background(), publication.BundleRunning, thirdUpdated.Add(time.Minute), 2, func(page []publication.BundleExecution) error {
		if len(page) > 2 {
			t.Fatalf("execution page length = %d, want at most 2", len(page))
		}
		executions = append(executions, page...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(executions) != 3 || executions[0].ID != "execution-a" || executions[1].ID != "execution-b" || executions[2].ID != "execution-c" {
		t.Fatalf("paged executions = %#v", executions)
	}
	if len(client.binds) != 2 {
		t.Fatalf("execution page query count = %d, want 2", len(client.binds))
	}
	if got := client.binds[0]["afterKey"]; got != "" {
		t.Fatalf("first continuation key = %v, want empty", got)
	}
	if got := client.binds[1]["afterKey"]; got != "execution-b" {
		t.Fatalf("second continuation key = %v, want execution-b", got)
	}
}

type executionReadPinConflictClient struct {
	mu                     sync.Mutex
	initialAcquires        int
	initialWave            chan struct{}
	acquireCalls           int
	cleanupBlocksAfterRace bool
	cleanupActive          bool
	alwaysConflict         bool
	queryError             error
	afterConflict          func()
	pins                   map[string]map[string]time.Time
	stateKeys              []string
	owners                 []string
}

func (c *executionReadPinConflictClient) InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error {
	return nil
}

func (c *executionReadPinConflictClient) QueryRows(ctx context.Context, query string, _ int, bindVars map[string]interface{}, visit store.RowVisitor) error {
	for _, fragment := range []string{"LET state = DOCUMENT(@@collection, @stateKey)", "FILTER cleanup == null OR cleanup.expiresAt < @now", "UPSERT {_key: @stateKey}", "RETURN {claimed: true}"} {
		if !strings.Contains(query, fragment) {
			return errors.New("execution read pin query lost full state arbitration: " + fragment)
		}
	}
	if bindVars["@collection"] != ExecutionReadPinsCollection {
		return errors.New("execution read pin collection is incorrect")
	}
	executionID, _ := bindVars["executionId"].(string)
	owner, _ := bindVars["owner"].(string)
	stateKey, _ := bindVars["stateKey"].(string)
	if executionID == "" || owner == "" || stateKey != executionStateKey(executionID) {
		return errors.New("execution read pin identity bindings are incorrect")
	}

	c.mu.Lock()
	c.acquireCalls++
	call := c.acquireCalls
	c.stateKeys = append(c.stateKeys, stateKey)
	c.owners = append(c.owners, owner)
	if c.queryError != nil {
		err := c.queryError
		c.mu.Unlock()
		return err
	}
	if call <= c.initialAcquires {
		if call == c.initialAcquires {
			close(c.initialWave)
		}
		wave := c.initialWave
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wave:
		}
		if call == 1 {
			if c.cleanupBlocksAfterRace {
				c.mu.Lock()
				c.cleanupActive = true
				c.mu.Unlock()
			}
			return executionReadPinConflictError()
		}
	} else {
		c.mu.Unlock()
	}
	c.mu.Lock()
	if c.cleanupActive {
		c.mu.Unlock()
		return nil
	}
	if c.alwaysConflict {
		afterConflict := c.afterConflict
		c.mu.Unlock()
		if afterConflict != nil {
			afterConflict()
		}
		return executionReadPinConflictError()
	}
	if c.pins == nil {
		c.pins = make(map[string]map[string]time.Time)
	}
	if c.pins[executionID] == nil {
		c.pins[executionID] = make(map[string]time.Time)
	}
	c.pins[executionID][owner] = bindVars["expiresAt"].(time.Time)
	c.mu.Unlock()
	return visit(map[string]any{"claimed": true})
}

func executionReadPinConflictError() error {
	return shared.ArangoError{HasError: true, Code: 409, ErrorNum: shared.ErrArangoConflict, ErrorMessage: "write-write conflict"}
}

func TestAcquireExecutionReadPinRetriesConcurrentReaderConflictWithoutLosingOwner(t *testing.T) {
	client := &executionReadPinConflictClient{initialAcquires: 2, initialWave: make(chan struct{})}
	registry, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := make(chan struct{})
	type outcome struct {
		owner   string
		claimed bool
		err     error
	}
	results := make(chan outcome, 2)
	for _, owner := range []string{"reader-a", "reader-b"} {
		owner := owner
		go func() {
			<-start
			claimed, err := registry.AcquireExecutionReadPin(ctx, "execution-shared", owner, time.Now().UTC().Add(time.Minute))
			results <- outcome{owner: owner, claimed: claimed, err: err}
		}()
	}
	close(start)
	for range 2 {
		result := <-results
		if result.err != nil || !result.claimed {
			t.Fatalf("AcquireExecutionReadPin(%q) = %v, %v; both concurrent readers must be retained", result.owner, result.claimed, result.err)
		}
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	owners := client.pins["execution-shared"]
	if len(owners) != 2 || owners["reader-a"].IsZero() || owners["reader-b"].IsZero() {
		t.Fatalf("pins after concurrent acquisition = %#v, want both reader owners", owners)
	}
	if client.acquireCalls != 3 {
		t.Fatalf("AQL acquisition count = %d, want one retry after the forced conflict", client.acquireCalls)
	}
	if len(client.stateKeys) != 3 || client.stateKeys[0] != client.stateKeys[1] || client.stateKeys[1] != client.stateKeys[2] {
		t.Fatalf("state keys across conflict retry = %#v, want the same execution state key", client.stateKeys)
	}
}

func TestAcquireExecutionReadPinBoundsConflictRetries(t *testing.T) {
	client := &executionReadPinConflictClient{alwaysConflict: true}
	registry, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := registry.AcquireExecutionReadPin(context.Background(), "execution-conflicts", "reader-a", time.Now().UTC().Add(time.Minute))
	if claimed || !shared.IsArangoErrorWithErrorNum(err, shared.ErrArangoConflict) {
		t.Fatalf("AcquireExecutionReadPin under repeated conflicts = %v, %v; want bounded Arango conflict", claimed, err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.acquireCalls != executionReadPinMaxAttempts {
		t.Fatalf("AQL attempts = %d, want bounded maximum %d", client.acquireCalls, executionReadPinMaxAttempts)
	}
}

func TestAcquireExecutionReadPinPropagatesNonConflictErrorWithoutRetry(t *testing.T) {
	wantErr := errors.New("database unavailable")
	client := &executionReadPinConflictClient{queryError: wantErr}
	registry, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := registry.AcquireExecutionReadPin(context.Background(), "execution-error", "reader-a", time.Now().UTC().Add(time.Minute))
	if claimed || !errors.Is(err, wantErr) {
		t.Fatalf("AcquireExecutionReadPin with non-conflict error = %v, %v; want original error", claimed, err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.acquireCalls != 1 {
		t.Fatalf("AQL attempts for non-conflict error = %d, want 1", client.acquireCalls)
	}
}

func TestAcquireExecutionReadPinRetryWaitHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &executionReadPinConflictClient{alwaysConflict: true, afterConflict: cancel}
	registry, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := registry.AcquireExecutionReadPin(ctx, "execution-cancel", "reader-a", time.Now().UTC().Add(time.Minute))
	if claimed || !errors.Is(err, context.Canceled) {
		t.Fatalf("AcquireExecutionReadPin after cancellation = %v, %v; want context cancellation", claimed, err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.acquireCalls != 1 {
		t.Fatalf("AQL attempts after cancellation = %d, want no retry", client.acquireCalls)
	}
}

func TestAcquireExecutionReadPinRetryRechecksCleanupArbitration(t *testing.T) {
	client := &executionReadPinConflictClient{
		initialAcquires:        1,
		initialWave:            make(chan struct{}),
		cleanupBlocksAfterRace: true,
	}
	registry, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := registry.AcquireExecutionReadPin(context.Background(), "execution-cleanup-race", "reader-a", time.Now().UTC().Add(time.Minute))
	if err != nil || claimed {
		t.Fatalf("AcquireExecutionReadPin after cleanup wins race = %v, %v; want not claimed", claimed, err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.acquireCalls != 2 || !client.cleanupActive || len(client.pins["execution-cleanup-race"]) != 0 {
		t.Fatalf("retry did not replay cleanup arbitration: calls=%d cleanupActive=%v pins=%#v", client.acquireCalls, client.cleanupActive, client.pins)
	}
}

const danglingBindingDatabasePrefix = "loom_verify_db_"

func danglingDatasetDocumentKey(kind string, values ...string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("loom.datasetstore.v1"))
	for _, value := range append([]string{kind}, values...) {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(value))
	}
	return kind + "_" + hex.EncodeToString(hash.Sum(nil))
}

func danglingOpen(t *testing.T, budget time.Duration) (context.Context, *store.Client, *Registry, *datasetstore.Store) {
	t.Helper()
	database := arangotest.Owned(t, danglingBindingDatabasePrefix)
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	t.Cleanup(cancel)
	if err := database.Client.Bootstrap(ctx, BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap bundle collections: %v", err)
	}
	if err := database.Client.Bootstrap(ctx, datasetstore.BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap dataset lifecycle collections: %v", err)
	}
	registry, err := New(database.Client)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	lifecycle, err := datasetstore.New(database.Client)
	if err != nil {
		t.Fatalf("new lifecycle store: %v", err)
	}
	return ctx, database.Client, registry, lifecycle
}

type danglingExecSpec struct {
	id         string
	project    string
	generation string
	recipe     string
	output     string
}

func danglingSeedExecution(t *testing.T, ctx context.Context, registry *Registry, spec danglingExecSpec) publication.BundleExecution {
	t.Helper()
	key := "dbind-key-" + spec.id
	owner := "dbind-publisher-" + spec.id
	if claimed, err := registry.AcquireBundleLease(ctx, key, owner, time.Now().UTC().Add(5*time.Minute)); err != nil || !claimed {
		t.Fatalf("acquire lease for %s: claimed=%v err=%v", spec.id, claimed, err)
	}
	stamp := time.Now().UTC().Truncate(time.Millisecond)
	execution := publication.BundleExecution{
		ID:  spec.id,
		Key: key,
		BundleIdentity: publication.BundleIdentity{
			Name: spec.recipe, Project: spec.project, DatasetGeneration: spec.generation,
			TranslationVersion: "v1", OutputName: spec.output,
			RecipeDigest: "sha256:" + spec.id + "-recipe", SchemaDigest: "sha256:" + spec.id + "-schema",
			ReceiptID: "receipt-" + spec.id, ScopeDigest: "scope-" + spec.id, EngineVersion: "dbind-engine",
		},
		State: publication.BundlePublished,
		Outputs: []publication.BundleOutputRecord{{
			Name: spec.output, PhysicalTable: "ch_" + strings.ReplaceAll(spec.id, "-", "_"),
			Selector:   dataset.DataframeSelector{Recipe: spec.recipe, TranslationVersion: "v1", Output: spec.output},
			RowCount:   11,
			ByteCount:  1100,
			State:      publication.BundlePublished,
			VerifiedAt: &stamp,
		}},
		CreatedAt:   stamp.Add(-time.Minute),
		UpdatedAt:   stamp,
		ReadyAt:     &stamp,
		PublishedAt: &stamp,
		OwnerID:     owner,
	}
	if err := registry.SaveExecution(ctx, execution, owner); err != nil {
		t.Fatalf("SaveExecution %s: %v", spec.id, err)
	}
	return execution
}

func danglingSeedRelease(t *testing.T, ctx context.Context, client *store.Client, lifecycle *datasetstore.Store, project, generation string, bindings []dataset.ReleasePublication) dataset.ProjectRelease {
	t.Helper()
	release := dataset.ProjectRelease{
		ID: "release_" + strings.ReplaceAll(uuid.NewString(), "-", ""), Project: project,
		GitCommit: generation, Generation: generation, Publications: bindings, CreatedAt: time.Now().UTC(),
	}
	saved, err := lifecycle.SaveRelease(ctx, release)
	if err != nil {
		t.Fatalf("SaveRelease: %v", err)
	}
	releaseKey := danglingDatasetDocumentKey("release", release.ID)
	activeKey := danglingDatasetDocumentKey("active_release", project)
	activated := false
	err = client.QueryRows(ctx, `LET pointer = DOCUMENT(@@collection, @activeKey)
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
		t.Fatalf("activate release pointer: activated=%v err=%v", activated, err)
	}
	active, err := lifecycle.ReadActiveRelease(ctx, project)
	if err != nil {
		t.Fatalf("production ReadActiveRelease after activation (key replication check): %v", err)
	}
	if active.Release.ID != saved.ID || active.Revision != 1 || active.Release.Generation != generation {
		t.Fatalf("active release round-trip mismatch: %+v", active)
	}
	return saved
}

func danglingPatchBindingStale(t *testing.T, ctx context.Context, client *store.Client, release dataset.ProjectRelease) {
	t.Helper()
	releaseKey := danglingDatasetDocumentKey("release", release.ID)
	patched := false
	err := client.QueryRows(ctx, `LET doc = DOCUMENT(@@collection, @releaseKey)
FILTER doc != null AND doc.recordType == "project_release"
UPDATE doc WITH {publications: @publications} IN @@collection
RETURN {patched: true}`, 32, map[string]interface{}{
		"@collection": datasetstore.LifecycleCollection, "releaseKey": releaseKey,
		"publications": release.Publications,
	}, func(row map[string]any) error {
		patched, _ = row["patched"].(bool)
		return nil
	})
	if err != nil || !patched {
		t.Fatalf("patch binding stale: patched=%v err=%v", patched, err)
	}
}

type danglingExecutionCorruption string

const (
	danglingDropOutputs     danglingExecutionCorruption = "drop outputs"
	danglingDeleteExecution danglingExecutionCorruption = "delete execution"
)

func danglingCorruptExecution(t *testing.T, ctx context.Context, client *store.Client, executionID string, corruption danglingExecutionCorruption) {
	t.Helper()
	removed := false
	query := `LET doc = DOCUMENT(@@collection, @key)
FILTER doc != null
UPDATE doc WITH {outputs: []} IN @@collection
RETURN {removed: true}`
	if corruption == danglingDeleteExecution {
		query = `LET doc = DOCUMENT(@@collection, @key)
FILTER doc != null
REMOVE doc IN @@collection
RETURN {removed: true}`
	}
	err := client.QueryRows(ctx, query, 32, map[string]interface{}{"@collection": BundleExecutionsCollection, "key": executionID}, func(row map[string]any) error {
		removed, _ = row["removed"].(bool)
		return nil
	})
	if err != nil || !removed {
		t.Fatalf("%s %s: removed=%v err=%v", corruption, executionID, removed, err)
	}
}

func danglingErrorChain(err error) string {
	if err == nil {
		return "<nil>"
	}
	parts := make([]string, 0, 4)
	for current := err; current != nil; current = errors.Unwrap(current) {
		parts = append(parts, current.Error())
	}
	return strings.Join(parts, " <- ")
}

func danglingUserErrorCode(err error) string {
	if user, ok := dataframeerrors.AsUserError(err); ok {
		return user.Code()
	}
	return ""
}

func danglingMaterializationDigest(values []published.Materialization) string {
	summaries := make([]string, 0, len(values))
	for _, value := range values {
		summaries = append(summaries, fmt.Sprintf("{Name=%q ID=%q PhysicalTable=%q State=%q}", value.Name, value.ID, value.PhysicalTable, value.State))
	}
	return fmt.Sprintf("[%s]", strings.Join(summaries, " "))
}

func danglingSelector(output string, recipe string) dataset.DataframeSelector {
	return dataset.DataframeSelector{Recipe: recipe, TranslationVersion: "v1", Output: output}
}

func TestBrokenOptionalBindingDoesNotHideHealthyDatasetAgainstArango(t *testing.T) {
	ctx, client, registry, lifecycle := danglingOpen(t, 120*time.Second)

	project := "dbind-fb7-project"
	generation := "dbind-gen-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	healthyExec := danglingSeedExecution(t, ctx, registry, danglingExecSpec{id: "dbind-exec-healthy", project: project, generation: generation, recipe: "dbind-recipe-a", output: "out-a"})
	badExec := danglingSeedExecution(t, ctx, registry, danglingExecSpec{id: "dbind-exec-bad", project: project, generation: generation, recipe: "dbind-recipe-b", output: "out-b"})

	if err := registry.CompareAndSwapPointer(ctx, healthyExec.PointerName(), "", healthyExec.ID); err != nil {
		t.Fatalf("point healthy pointer: %v", err)
	}
	if err := registry.CompareAndSwapPointer(ctx, badExec.PointerName(), "", badExec.ID); err != nil {
		t.Fatalf("point bad pointer: %v", err)
	}

	bindings := []dataset.ReleasePublication{
		{Selector: danglingSelector("out-a", "dbind-recipe-a"), ExecutionID: healthyExec.ID, Generation: generation, Required: false, Stale: false, VerifiedAt: time.Now().UTC()},
		{Selector: danglingSelector("out-b", "dbind-recipe-b"), ExecutionID: badExec.ID, Generation: generation, Required: false, Stale: false, VerifiedAt: time.Now().UTC()},
	}
	danglingSeedRelease(t, ctx, client, lifecycle, project, generation, bindings)

	reader := &published.Reader{Catalog: registry, ActiveReleaseResolver: lifecycle}
	healthySelector := danglingSelector("out-a", "dbind-recipe-a")
	badSelector := danglingSelector("out-b", "dbind-recipe-b")

	t.Log("EXPECT E1: fresh list = 2 materializations [out-a out-b], State READY, err==nil")
	list, err := reader.CurrentProjectDatasets(ctx, project)
	if err != nil || len(list) != 2 || list[0].Name != "out-a" || list[1].Name != "out-b" {
		t.Fatalf("E1 baseline broken: list=%s err=%v", danglingMaterializationDigest(list), err)
	}
	if list[0].ID != healthyExec.ID+":out-a" || list[0].PhysicalTable != "ch_dbind_exec_healthy" || list[0].State != published.StateReady {
		t.Fatalf("E1 digest mismatch: %s", danglingMaterializationDigest(list))
	}
	t.Logf("OBSERVED E1 fresh baseline: %s", danglingMaterializationDigest(list))

	if err := registry.CompareAndSwapPointer(ctx, badExec.PointerName(), badExec.ID, "dbind-orphan-target"); err != nil {
		t.Fatalf("un-name bad pointer: %v", err)
	}
	if pointer, err := registry.GetPointer(ctx, badExec.PointerName()); err != nil || pointer.ExecutionID != "dbind-orphan-target" {
		t.Fatalf("pointer un-name not observable via GetPointer: %+v err=%v", pointer, err)
	}
	t.Log("EXPECT E2: un-named pointer does NOT affect release reads (releaseMaterialization has no GetPointer call)")
	list, err = reader.CurrentProjectDatasets(ctx, project)
	if err != nil || len(list) != 2 {
		t.Fatalf("E2 expectation broken: pointer naming affected the release read path: list=%s err=%v", danglingMaterializationDigest(list), err)
	}
	t.Logf("OBSERVED E2 pointer-unnamed control: %s (pointer re-check claim has no code behind it)", danglingMaterializationDigest(list))

	danglingCorruptExecution(t, ctx, client, badExec.ID, danglingDropOutputs)
	t.Log("EXPECT E3: healthy out-a remains in the list; only unavailable optional out-b is omitted")
	list, err = reader.CurrentProjectDatasets(ctx, project)
	droppedOutputListKeepsHealthy := err == nil && len(list) == 1 && list[0].Name == "out-a" && list[0].ID == healthyExec.ID+":out-a"
	t.Logf("OBSERVED E3: list=%s err=[%s] healthyRetained=%v", danglingMaterializationDigest(list), danglingErrorChain(err), droppedOutputListKeepsHealthy)
	if !droppedOutputListKeepsHealthy {
		t.Errorf("F-B7 regression: broken optional out-b hid healthy out-a: list=%s err=%v", danglingMaterializationDigest(list), err)
	}

	t.Log("EXPECT E5: healthy single read unaffected by the OTHER binding's corruption")
	single, err := reader.CurrentProjectDataset(ctx, project, healthySelector)
	if err != nil || single.ID != healthyExec.ID+":out-a" || single.PhysicalTable != "ch_dbind_exec_healthy" {
		t.Fatalf("E5 violated: healthy single read degraded by foreign binding: %+v err=%v", single, err)
	}
	t.Logf("OBSERVED E5a: healthy single read while binding out-b corrupt: {Name=%q ID=%q PhysicalTable=%q} -> per-viewer demotion DISPROVEN", single.Name, single.ID, single.PhysicalTable)

	danglingCorruptExecution(t, ctx, client, badExec.ID, danglingDeleteExecution)
	t.Log("EXPECT E4: healthy out-a remains listed after dangling optional out-b is removed")
	list, err = reader.CurrentProjectDatasets(ctx, project)
	danglingListKeepsHealthy := err == nil && len(list) == 1 && list[0].Name == "out-a" && list[0].ID == healthyExec.ID+":out-a"
	t.Logf("OBSERVED E4: list=%s err=[%s] healthyRetained=%v", danglingMaterializationDigest(list), danglingErrorChain(err), danglingListKeepsHealthy)
	if !danglingListKeepsHealthy {
		t.Errorf("F-B7 regression: dangling optional out-b hid healthy out-a: list=%s err=%v", danglingMaterializationDigest(list), err)
	}
	if _, err := registry.GetExecution(ctx, badExec.ID); !errors.Is(err, publication.ErrBundleNotFound) {
		t.Fatalf("E4 precondition: bad execution should be absent: err=%v", err)
	}

	if _, err := reader.CurrentProjectDataset(ctx, project, badSelector); err == nil {
		t.Fatalf("E5b violated: dangling binding resolver returned success")
	} else {
		t.Logf("OBSERVED E5b: bad selector read err=[%s] userCode=%q (loud error, never stale data)", danglingErrorChain(err), danglingUserErrorCode(err))
	}

	if single, err := reader.CurrentProjectDataset(ctx, project, healthySelector); err != nil || single.ID != healthyExec.ID+":out-a" {
		t.Fatalf("E5c violated: healthy single read degraded by foreign dangling binding: %+v err=%v", single, err)
	} else {
		t.Logf("OBSERVED E5c: healthy single read with dangling binding: {Name=%q ID=%q} fresh => per-selector independence CONFIRMED", single.Name, single.ID)
	}

	t.Logf("F-B7 prevented: unavailable optional bindings were omitted while the healthy binding remained current; droppedOutputHealthy=%v danglingHealthy=%v", droppedOutputListKeepsHealthy, danglingListKeepsHealthy)
}

func TestRequiredBindingSilentDemotion(t *testing.T) {
	ctx, client, registry, lifecycle := danglingOpen(t, 120*time.Second)

	project := "dbind-req-project"
	generation := "dbind-gen-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	requiredExec := danglingSeedExecution(t, ctx, registry, danglingExecSpec{id: "dbind-exec-required", project: project, generation: generation, recipe: "dbind-recipe-r", output: "out-r"})
	optionalExec := danglingSeedExecution(t, ctx, registry, danglingExecSpec{id: "dbind-exec-optional", project: project, generation: generation, recipe: "dbind-recipe-o", output: "out-o"})

	requiredSelector := danglingSelector("out-r", "dbind-recipe-r")
	optionalSelector := danglingSelector("out-o", "dbind-recipe-o")
	bindings := []dataset.ReleasePublication{
		{Selector: requiredSelector, ExecutionID: requiredExec.ID, Generation: generation, Required: true, Stale: false, VerifiedAt: time.Now().UTC()},
		{Selector: optionalSelector, ExecutionID: optionalExec.ID, Generation: generation, Required: false, Stale: false, VerifiedAt: time.Now().UTC()},
	}
	release := danglingSeedRelease(t, ctx, client, lifecycle, project, generation, bindings)
	reader := &published.Reader{Catalog: registry, ActiveReleaseResolver: lifecycle}

	t.Log("EXPECT R0: fresh release -> 2 materializations [out-o out-r] (sorted by Name)")
	list, err := reader.CurrentProjectDatasets(ctx, project)
	if err != nil || len(list) != 2 || list[0].Name != "out-o" || list[1].Name != "out-r" {
		t.Fatalf("R0 baseline broken: list=%s err=%v", danglingMaterializationDigest(list), err)
	}

	danglingCorruptExecution(t, ctx, client, requiredExec.ID, danglingDropOutputs)
	t.Log("EXPECT R4: required=true + Stale=false + corrupted execution -> CurrentProjectDatasets returns error (not silent stale)")
	var r4ErrChain string
	list, err = reader.CurrentProjectDatasets(ctx, project)
	r4Loud := err != nil && len(list) == 0 && strings.Contains(danglingErrorChain(err), "has no queryable output")
	r4ErrChain = danglingErrorChain(err)

	danglingCorruptExecution(t, ctx, client, requiredExec.ID, danglingDeleteExecution)
	if !r4Loud {
		t.Fatalf("R4 violated: corrupted required binding did not force a loud whole-list error: list=%s err=[%s]", danglingMaterializationDigest(list), r4ErrChain)
	}
	t.Logf("OBSERVED R4 verbatim: corrupted required binding -> caller behavior = ERROR, chain=[%s] userCode=%q", r4ErrChain, danglingUserErrorCode(err))

	staleBindings := []dataset.ReleasePublication{
		{Selector: requiredSelector, ExecutionID: requiredExec.ID, Generation: generation, Required: true, Stale: true, VerifiedAt: time.Now().UTC()},
		{Selector: optionalSelector, ExecutionID: optionalExec.ID, Generation: generation, Required: false, Stale: false, VerifiedAt: time.Now().UTC()},
	}
	danglingPatchBindingStale(t, ctx, client, dataset.ProjectRelease{ID: release.ID, Publications: staleBindings})

	t.Log("EXPECT R1: required-stale binding silently skipped; list = [out-o] only, err == nil; NO error mentioning required")
	list, err = reader.CurrentProjectDatasets(ctx, project)
	r1SilentSkip := err == nil && len(list) == 1 && list[0].Name == "out-o"
	t.Logf("OBSERVED R1 verbatim: list=%s err=[%s] -> caller %s", danglingMaterializationDigest(list), danglingErrorChain(err), map[bool]string{true: "SILENTLY DROPS the required-stale binding (no error)", false: "did not match predicted silent skip"}[r1SilentSkip])
	if !r1SilentSkip {
		t.Fatalf("R1 violated: expected silently-skipped required-stale binding (nil error, [out-o]); got list=%s err=%v", danglingMaterializationDigest(list), err)
	}

	inertBindings := make([]dataset.ReleasePublication, len(staleBindings))
	copy(inertBindings, staleBindings)
	inertBindings[0].Required = false
	danglingPatchBindingStale(t, ctx, client, dataset.ProjectRelease{ID: release.ID, Publications: inertBindings})
	list, err = reader.CurrentProjectDatasets(ctx, project)
	if err != nil || len(list) != 1 || list[0].Name != "out-o" {
		t.Fatalf("R5 violated: read path consulted Required (flipping it changed behavior): list=%s err=%v", danglingMaterializationDigest(list), err)
	}
	t.Log("OBSERVED R5: Required=true->false on the stale binding changed nothing; stale bindings are filtered before the Required error policy")

	t.Log("EXPECT R2: single required-stale selector -> UserError code DATASET_NOT_FOUND, NOT ErrReleaseRequirementsUnmet; no stale value served")
	single, err := reader.CurrentProjectDataset(ctx, project, requiredSelector)
	code := danglingUserErrorCode(err)
	if err == nil || code != string(dataframeerrors.CodeDatasetNotFound) || single.ID != "" {
		t.Fatalf("R2 violated: expected DATASET_NOT_FOUND and empty materialization; got {ID=%q} err=[%s] code=%q", single.ID, danglingErrorChain(err), code)
	}
	t.Logf("OBSERVED R2 verbatim: required-stale single read -> err=[%s] userCode=%q -> silently serves NOT-FOUND (never stale data)", danglingErrorChain(err), code)

	allStale := make([]dataset.ReleasePublication, len(staleBindings))
	copy(allStale, staleBindings)
	allStale[1].Stale = true
	danglingPatchBindingStale(t, ctx, client, dataset.ProjectRelease{ID: release.ID, Publications: allStale})
	t.Log("EXPECT R3: all-stale release -> CurrentProjectDatasets UserError DATASET_NOT_FOUND, no requirements error")
	list, err = reader.CurrentProjectDatasets(ctx, project)
	code = danglingUserErrorCode(err)
	if err == nil || code != string(dataframeerrors.CodeDatasetNotFound) || len(list) != 0 {
		t.Fatalf("R3 violated: expected DATASET_NOT_FOUND for all-stale release; got list=%s err=[%s] code=%q", danglingMaterializationDigest(list), danglingErrorChain(err), code)
	}
	if errors.Is(err, dataset.ErrReleaseRequirementsUnmet) {
		t.Fatalf("R3 violated: read path surfaced ErrReleaseRequirementsUnmet")
	}
	t.Logf("OBSERVED R3 verbatim: all-stale list -> err=[%s] userCode=%q; ErrReleaseRequirementsUnmet errors.Is = %v", danglingErrorChain(err), code, errors.Is(err, dataset.ErrReleaseRequirementsUnmet))

	t.Log("VERDICT required-set variant: stale required bindings are skipped before catalog access and report DATASET_NOT_FOUND; an active required binding that is deterministically unavailable remains fail-closed. Required verification occurs at Create, and carried-forward bindings lose Required, so Required=true with Stale=true is not reachable through the production Create/Activate path")
}
