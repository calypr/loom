package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	datasetstore "github.com/calypr/loom/internal/backends/arango/internal/dataset"
	store "github.com/calypr/loom/internal/backends/arango/internal/store"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
	pubclickhouse "github.com/calypr/loom/internal/dataframe/publication/clickhouse"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataset"
	storeclickhouse "github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
)

const rrdTablePrefix = "ch_rrd"

const rrdDatabasePrefix = "loom_verify_rrd_"

type rrdCitation struct {
	file string
	line int
	want string
}

var rrdCitations = []rrdCitation{
	{"internal/dataframe/publication/clickhouse/bundle_store.go", 1068, "pointer, pointerErr := s.catalog.GetPointer(cleanupCtx, execution.PointerName())"},
	{"internal/dataframe/publication/clickhouse/bundle_store.go", 1069, "if pointerErr != nil && !errors.Is(pointerErr, publication.ErrBundleNotFound) {"},
	{"internal/dataframe/publication/clickhouse/bundle_store.go", 1073, "if pointerErr == nil && pointer.ExecutionID == execution.ID {"},
	{"internal/dataframe/publication/clickhouse/bundle_store.go", 1084, "if err := s.clickHouse.DropTable(leaseCtx, output.PhysicalTable); err != nil {"},
	{"internal/dataframe/publication/clickhouse/bundle_store.go", 1089, "execution.State = publication.BundleFailed"},
	{"internal/dataframe/publication/clickhouse/bundle_store.go", 1097, "if err := s.catalog.SaveExecution(leaseCtx, execution, reconcilerID); err != nil {"},
	{"internal/dataframe/published/current.go", 95, "execution, err := r.Catalog.GetExecution(ctx, binding.ExecutionID)"},
	{"internal/dataframe/published/current.go", 99, "if execution.ID != binding.ExecutionID || execution.Project != release.Project || execution.DatasetGeneration != binding.Generation || !execution.State.Successful() {"},
	{"internal/dataframe/published/current.go", 105, "return Materialization{}, fmt.Errorf(\"%w: active dataframe execution %q does not match release %q\", errReleaseBindingUnavailable, binding.ExecutionID, release.ID)"},
	{"internal/dataframe/published/current.go", 206, "value, err := r.releaseMaterialization(ctx, active.Release, binding)"},
	{"internal/dataframe/published/current.go", 207, "if err != nil {"},
	{"internal/dataframe/published/current.go", 208, "return nil, err"},
	{"internal/backends/arango/internal/publication/bundle_registry.go", 431, "FILTER LENGTH(active) == 0 AND (cleanup == null OR cleanup.expiresAt < @now OR cleanup.ownerId == @owner)"},
	{"internal/dataframe/published/read_pins.go", 63, "owned, err := pins.AcquireExecutionReadPin(ctx, id, owner, time.Now().UTC().Add(ttl))"},
	{"internal/dataframe/published/export.go", 135, "acquired, err := pins.AcquireExecutionReadPin(ctx, result.ExecutionID, result.ReaderID, expires)"},
}

type rrdAbsent struct {
	file       string
	identifier string
}

var rrdAbsentIdentifiers = []rrdAbsent{
	{"internal/backends/arango/internal/dataset/release_store.go", "AcquireExecutionReadPin"},
	{"internal/backends/arango/internal/dataset/release_store.go", "ReadPin"},
}

func rrdRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

func rrdCheckCitations(t *testing.T) {
	t.Helper()
	root := rrdRepoRoot(t)
	contents := map[string][]string{}
	for _, c := range rrdCitations {
		lines, ok := contents[c.file]
		if !ok {
			raw, err := os.ReadFile(filepath.Join(root, c.file))
			if err != nil {
				t.Fatalf("citation source %s unreadable: %v", c.file, err)
			}
			lines = strings.Split(string(raw), "\n")
			contents[c.file] = lines
		}
		if c.line < 1 || c.line > len(lines) {
			t.Fatalf("citation drift: %s has %d lines, cited :%d == %q", c.file, len(lines), c.line, c.want)
		}
		found := false
		for _, line := range lines {
			if strings.TrimSpace(line) == strings.TrimSpace(c.want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("citation drift: %s no longer contains %q (cited :%d)", c.file, c.want, c.line)
		}
	}
	for _, a := range rrdAbsentIdentifiers {
		raw, err := os.ReadFile(filepath.Join(root, a.file))
		if err != nil {
			t.Fatalf("citation source %s unreadable: %v", a.file, err)
		}
		if strings.Contains(string(raw), a.identifier) {
			t.Fatalf("hypothesis invariant broken: %q unexpectedly present in %s (release activation must not read-pin)", a.identifier, a.file)
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, "internal/backends/arango/internal/publication/bundle_registry.go"))
	if err != nil {
		t.Fatalf("citation source bundle_registry.go unreadable: %v", err)
	}
	body := strings.Split(string(raw), "\n")
	if len(body) < 441 {
		t.Fatalf("citation drift: bundle_registry.go shrank below the ClaimExecutionCleanup region")
	}
	region := strings.ToLower(strings.Join(body[424:441], "\n"))
	for _, forbidden := range []string{"release", "lifecycle", "project_release"} {
		if strings.Contains(region, forbidden) {
			t.Fatalf("hypothesis invariant broken: ClaimExecutionCleanup region (:425-441) mentions %q, so a release-binding signal now guards the cleanup claim", forbidden)
		}
	}
}

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

func rrdOpen(t *testing.T) *rrdHarness {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL to run the release-dangle reconcile e2e against a real ArangoDB")
	}
	chURL := strings.TrimSpace(os.Getenv("LOOM_CLICKHOUSE_URL"))
	if chURL == "" {
		t.Skip("set LOOM_CLICKHOUSE_URL to run the release-dangle reconcile e2e against a real ClickHouse")
	}

	h := &rrdHarness{suffix: rrdHex()}
	h.project = "rrd-fb12-project"
	h.generation = "rrd-gen-" + h.suffix
	h.healthyTable = rrdTablePrefix + "h_" + h.suffix
	h.dangleTable = rrdTablePrefix + "_" + h.suffix
	h.healthy = rrdExecution("rrd-exec-healthy-"+h.suffix, "rrd-key-healthy-"+h.suffix, h.project, h.generation, "rrd-recipe-a", "out-a", h.healthyTable)
	h.dangle = rrdExecution("rrd-exec-dangle-"+h.suffix, "rrd-key-dangle-"+h.suffix, h.project, h.generation, "rrd-recipe-b", "out-b", h.dangleTable)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	t.Cleanup(cancel)
	h.ctx = ctx

	name := rrdDatabasePrefix + h.suffix
	if !strings.HasPrefix(name, rrdDatabasePrefix) || len(name) > 63 {
		t.Fatalf("verification database %q escapes the %s prefix or exceeds the Arango name limit", name, rrdDatabasePrefix)
	}
	client, err := store.Open(ctx, endpoint, name)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Logf("RRD_DATABASE=%s", name)
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		cleanupCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		req, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(name), nil)
		if err != nil {
			t.Errorf("build cleanup request for %s: %v", name, err)
			return
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Errorf("cleanup %s: %v", name, err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("cleanup %s status=%d", name, response.StatusCode)
			return
		}
		t.Logf("RRD_DATABASE_REMOVED=%s", name)
	})
	if err := client.Bootstrap(ctx, BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap bundle collections: %v", err)
	}
	if err := client.Bootstrap(ctx, datasetstore.BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap dataset lifecycle collections: %v", err)
	}
	registry, err := New(client)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	h.registry = registry
	lifecycle, err := datasetstore.New(client)
	if err != nil {
		t.Fatalf("new lifecycle store: %v", err)
	}
	h.lifecycle = lifecycle
	h.client = client

	ch, err := storeclickhouse.New(storeclickhouse.Options{
		URL:      chURL,
		Database: strings.TrimSpace(os.Getenv("LOOM_CLICKHOUSE_DATABASE")),
		Username: os.Getenv("LOOM_CLICKHOUSE_USERNAME"),
		Password: os.Getenv("LOOM_CLICKHOUSE_PASSWORD"),
		Timeout:  60 * time.Second,
	})
	if err != nil {
		t.Fatalf("open clickhouse: %v", err)
	}
	if err := ch.Ping(ctx); err != nil {
		t.Fatalf("ping clickhouse: %v", err)
	}
	h.ch = ch
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		for _, table := range []string{h.dangleTable, h.healthyTable} {
			if err := ch.DropTable(cleanupCtx, table); err != nil {
				t.Errorf("drop clickhouse table %s: %v", table, err)
			} else {
				t.Logf("RRD_CLICKHOUSE_DROPPED=%s", table)
			}
		}
		_ = ch.Close()
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
	rrdCheckCitations(t)
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
