package clickhouse

// These tests exercise a published page read while production reconciliation
// runs between resolving the dataset and querying its ClickHouse table. A
// second case changes the persisted state and pointer to exercise the fault
// control where the reconciliation guard no longer applies.
//
//	docker exec loom-dev-qwen-current-loom-api-1 sh -c \
//	  'cd /workspace && LOOM_TEST_ARANGO_URL=http://arangodb:8529 \
//	   LOOM_CLICKHOUSE_URL=http://clickhouse:8123 LOOM_CLICKHOUSE_DATABASE=loom_dev \
//	   LOOM_CLICKHOUSE_USERNAME=loom_dev LOOM_CLICKHOUSE_PASSWORD=loom_dev_password \
//	   go test -count=1 -timeout 150s -v -run TestUnpinnedPageRead \
//	     ./internal/dataframe/publication/clickhouse/'

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	pubpublication "github.com/calypr/loom/internal/dataframe/publication"
	pubarango "github.com/calypr/loom/internal/dataframe/publication/arango"
	dataframepublished "github.com/calypr/loom/internal/dataframe/published"
	storearango "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/store/clickhouse"
	"github.com/calypr/loom/internal/testkit/arangotest"
	"github.com/google/uuid"
)

// uprDatabasePrefix owns this suite's databases; residue is greppable as
// loom_verify_upr_*. uprOwnedTag is embedded in every ClickHouse table this
// file creates (owned canaries, hand-seeded reconciler targets, and the
// production-published execution's output name) so every table name contains
// "upr" and is individually deletable + residue-greppable.
const (
	uprDatabasePrefix = "loom_verify_upr_"
	uprOwnedTag       = "upr"
	uprReconcileAge   = 90 * time.Minute
	uprOlderThan      = 60 * time.Minute
	uprReconcileMark  = "stale execution reconciled"
)

// ---------------------------------------------------------------------------
// observed bookkeeping
// ---------------------------------------------------------------------------

type uprLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *uprLog) addf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *uprLog) emit(t *testing.T) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		t.Logf("OBSERVED %s", line)
	}
}

// uprCatalog observes lease claims / pointer reads / read-pin acquisitions so the
// sweep's decisions (not just its effects) are recorded. It embeds the real
// *Registry and overrides only observers — every decision is still made by real
// Arango AQL in the embedded registry; this wrapper never gates or decides.
type uprCatalog struct {
	*pubarango.Registry
	mu           sync.Mutex
	claims       map[string]string
	pointerReads map[string]int
	pinsAcquired int
}

func newUprCatalog(registry *pubarango.Registry) *uprCatalog {
	return &uprCatalog{Registry: registry, claims: map[string]string{}, pointerReads: map[string]int{}}
}

func (c *uprCatalog) AcquireBundleLease(ctx context.Context, key, owner string, expires time.Time) (bool, error) {
	claimed, err := c.Registry.AcquireBundleLease(ctx, key, owner, expires)
	if claimed {
		c.mu.Lock()
		c.claims[key] = owner
		c.mu.Unlock()
	}
	return claimed, err
}

func (c *uprCatalog) GetPointer(ctx context.Context, name string) (pubpublication.BundlePointer, error) {
	c.mu.Lock()
	c.pointerReads[name]++
	c.mu.Unlock()
	return c.Registry.GetPointer(ctx, name)
}

func (c *uprCatalog) AcquireExecutionReadPin(ctx context.Context, executionID, owner string, expires time.Time) (bool, error) {
	c.mu.Lock()
	c.pinsAcquired++
	c.mu.Unlock()
	return c.Registry.AcquireExecutionReadPin(ctx, executionID, owner, expires)
}

func (c *uprCatalog) claimOwner(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	owner, ok := c.claims[key]
	return owner, ok
}

func (c *uprCatalog) pointerReadCount(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pointerReads[name]
}

func (c *uprCatalog) pinAcquireCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pinsAcquired
}

// ---------------------------------------------------------------------------
// harness (owned database + owned ClickHouse tables; SKIP-clean opt-in)
// ---------------------------------------------------------------------------

type uprHarness struct {
	ctx      context.Context
	endpoint string
	client   *storearango.Client
	registry *pubarango.Registry
	ch       *clickhouse.Client
	database string
	suffix   string
	log      *uprLog

	mu            sync.Mutex
	trackedTables []string
}

func (h *uprHarness) trackTable(table string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, existing := range h.trackedTables {
		if existing == table {
			return
		}
	}
	h.trackedTables = append(h.trackedTables, table)
}

func uprOpen(t *testing.T) *uprHarness {
	t.Helper()
	database := arangotest.Owned(t, uprDatabasePrefix)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	t.Cleanup(cancel)
	client := database.Client
	registry, err := pubarango.New(client)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if err := client.Bootstrap(ctx, pubarango.BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap bundle collections: %v", err)
	}
	ch := arangotest.ClickHouse(t)
	harness := &uprHarness{
		ctx: ctx, endpoint: database.URL, client: client, registry: registry, ch: ch,
		database: database.Name, suffix: database.Name[len(uprDatabasePrefix):], log: &uprLog{},
	}
	t.Cleanup(func() { harness.drainTables(t) })
	return harness
}

func (h *uprHarness) uprOwnedPrefix(label string) string {
	return "loom_bundle_" + uprOwnedTag + h.suffix + "_" + label
}

func (h *uprHarness) seedTable(ctx context.Context, table string, rows int) error {
	columns := []clickhouse.Column{
		{Name: "value", Type: "Int64"},
		{Name: "__loom_row_id", Type: "String"},
	}
	if err := h.ch.CreateTable(ctx, table, columns); err != nil {
		return err
	}
	values := make([]map[string]any, 0, rows)
	for i := 1; i <= rows; i++ {
		values = append(values, map[string]any{"value": int64(i), "__loom_row_id": fmt.Sprint(i)})
	}
	return h.ch.InsertRows(ctx, table, columns, values)
}

// tablePresent reports whether a ClickHouse table exists via system.tables.
func (h *uprHarness) tablePresent(ctx context.Context, table string) bool {
	rows, err := h.ch.QueryRowsArgs(ctx, "SELECT count() AS c FROM system.tables WHERE database = currentDatabase() AND name = ?", []string{"c"}, table)
	if err != nil || len(rows) != 1 {
		return false
	}
	count, ok := uprCount(rows[0]["c"])
	return ok && count == 1
}

func (h *uprHarness) drainTables(t *testing.T) {
	t.Helper()
	h.mu.Lock()
	tables := append([]string(nil), h.trackedTables...)
	h.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, table := range tables {
		if err := h.ch.DropTable(ctx, table); err != nil {
			t.Errorf("UPR CH drop %s: %v", table, err)
		}
	}
	// Residue gate scoped to THIS file's unique prefix so it cannot collide with
	// the sibling workers sharing loom_dev.
	tagged := "%" + uprOwnedTag + h.suffix + "%"
	names, err := h.ch.QueryRowsArgs(ctx, fmt.Sprintf("SELECT name FROM system.tables WHERE database = currentDatabase() AND name LIKE '%s' ORDER BY name", tagged), []string{"name"})
	if err != nil {
		t.Errorf("UPR CH residue query (%s): %v", tagged, err)
		return
	}
	if len(names) != 0 {
		t.Errorf("UPR CH residue under this file's prefix is not empty: %v", names)
		return
	}
	for _, table := range tables {
		if h.tablePresent(ctx, table) {
			t.Errorf("UPR CH residue: tracked table %s still present", table)
		}
	}
	t.Logf("UPR_CH_CLEANUP_VERIFIED dropped=%v and this-file prefix empty in system.tables", tables)
}

// ---------------------------------------------------------------------------
// direct-AQL fact readers / state fixture helpers
// ---------------------------------------------------------------------------

func uprCount(value any) (int64, bool) {
	switch typed := value.(type) {
	case uint64:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		if typed == float64(int64(typed)) {
			return int64(typed), true
		}
	case string:
		if parsed, err := strconv.ParseInt(typed, 10, 64); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

// uprRawExecutionRow reads the doc verbatim (no GetExecution canonicalization) to
// observe the raw reconcile-written `state` and `error` marker.
func (h *uprHarness) rawExecutionRow(ctx context.Context, execID string) (state, errField string) {
	err := h.client.QueryRows(ctx, `RETURN DOCUMENT(@@collection, @key)`, 32, map[string]interface{}{
		"@collection": pubarango.BundleExecutionsCollection, "key": execID,
	}, func(row map[string]any) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var decoded struct {
			State string `json:"state"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(data, &decoded); err != nil {
			return err
		}
		state, errField = decoded.State, decoded.Error
		return nil
	})
	if err != nil {
		return "", "read-error:" + err.Error()
	}
	return state, errField
}

// flipToRunningStale parks an execution at RUNNING with an old updatedAt, the
// end state of a stalled publisher that reconcile is meant to reclaim. It is the
// identical direct-data technique release_dangle's rdlFlipToRunning /
// dangling_binding / export_pin_crash_window harnesses use (a data-plane fixture
// for an otherwise-unreachable state; it does not touch product code or any
// guard).
func (h *uprHarness) flipToRunningStale(t *testing.T, ctx context.Context, execID string) {
	t.Helper()
	patched := false
	err := h.client.QueryRows(ctx, `LET doc = DOCUMENT(@@collection, @key)
FILTER doc != null
UPDATE doc WITH {state: "RUNNING", updatedAt: @updatedAt} IN @@collection
RETURN {patched: true}`, 32, map[string]interface{}{
		"@collection": pubarango.BundleExecutionsCollection,
		"key":         execID,
		"updatedAt":   time.Now().UTC().Add(-uprReconcileAge),
	}, func(row map[string]any) error {
		patched, _ = row["patched"].(bool)
		return nil
	})
	if err != nil || !patched {
		t.Fatalf("flip %s to stale RUNNING: patched=%v err=%v", execID, patched, err)
	}
}

// ---------------------------------------------------------------------------
// page-read session driven through the same primitives rows.go uses
// ---------------------------------------------------------------------------

type uprReader interface {
	CurrentProjectDataset(ctx context.Context, project string, selector dataframepublished.DataframeSelector) (dataframepublished.Materialization, error)
	CurrentProjectDatasets(ctx context.Context, project string) ([]dataframepublished.Materialization, error)
	Page(ctx context.Context, materialization dataframepublished.Materialization, req dataframepublished.PageRequest) (dataframepublished.Page, error)
}

func uprNewReader(h *uprHarness) *dataframepublished.Reader {
	return &dataframepublished.Reader{
		ClickHouse: h.ch,
		Catalog:    h.registry,
	}
}

func uprPageRequest() dataframepublished.PageRequest {
	return dataframepublished.PageRequest{Unrestricted: true, First: 5}
}

func uprErrChain(err error) string {
	if err == nil {
		return "<nil>"
	}
	parts := make([]string, 0, 6)
	for current := err; current != nil; current = errors.Unwrap(current) {
		parts = append(parts, current.Error())
	}
	return strings.Join(parts, " <- ")
}

func uprMaterializationDigest(value dataframepublished.Materialization) string {
	return fmt.Sprintf("{ID=%q Name=%q PhysicalTable=%q State=%q}", value.ID, value.Name, value.PhysicalTable, value.State)
}

// ---------------------------------------------------------------------------
// 1) BASELINE: unpinned GraphQL page read vs concurrent production reconcile.
//    Pre-stated expectation: the page read is PREVENTED from being a victim,
//    because reconcile excludes PUBLISHED () and skips pointer-named rows
//    (), and the page read can only resolve pointer-named PUBLISHED
//    rows (current.go/121). FAILED-ON-PURPOSE only if a real drop hits the
//    page read's resolved-but-not-yet-queried execution with no fault.
// ---------------------------------------------------------------------------

func TestUnpinnedPageReadBaselineAgainstArango(t *testing.T) {

	h := uprOpen(t)
	ctx := h.ctx

	// Two stores: one wraps the catalog for observation (the sweep is driven
	// through it), one is plain (the page-read resolution store mirrors rows.go).
	observeCatalog := newUprCatalog(h.registry)
	sweepStore, err := NewBundleStore(h.ch, observeCatalog)
	if err != nil {
		t.Fatalf("NewBundleStore(sweep): %v", err)
	}
	publishStore, err := NewBundleStore(h.ch, h.registry)
	if err != nil {
		t.Fatalf("NewBundleStore(publish): %v", err)
	}

	// =========================================================================
	// STEP 1 — publish E2 (the page-read victim) through production.Publish.
	// Real ClickHouse client creates a real table with 3 rows; PublishExecution
	// names the pointer; the bundle lease is then released (frees the key), which
	// is the ONLY extra step vs the production commit path and merely drops a
	// completed publisher's lease. 0 execution read pins are present anywhere.
	// =========================================================================
	e2Project := "upr.e2.project"
	e2Recipe := "upr.e2.recipe"
	e2Output := uprOwnedTag + h.suffix + "_out"
	e2Identity := pubpublication.PublicationIdentity{
		Name: e2Recipe, TranslationVersion: "v1", OutputName: e2Output,
		Project: e2Project, DatasetGeneration: uprOwnedTag + h.suffix + "_gen",
		RecipeDigest: "sha256:upr-e2-recipe-" + h.suffix,
		SchemaDigest: "sha256:upr-e2-schema-" + h.suffix,
		ScopeDigest:  "upr-e2-scope", EngineVersion: "loom",
	}
	e2Outputs := []pubpublication.OutputStream{{
		Name: e2Output,
		Columns: []pubpublication.LogicalColumn{{
			ID: "col-upr-value", Name: "value", Kind: "integer", Provenance: pubpublication.ColumnExplicit,
		}},
		Stream: func(streamCtx context.Context, visit func(map[string]any) error) error {
			for i := 1; i <= 3; i++ {
				if err := visit(map[string]any{"value": int64(i)}); err != nil {
					return err
				}
			}
			return nil
		},
	}}
	e2Result, err := pubpublication.Publish(ctx, publishStore, e2Identity, e2Outputs, pubpublication.Limits{})
	if err != nil {
		t.Fatalf("production.Publish(E2) failed: %v", err)
	}
	if len(e2Result.Outputs) != 1 || e2Result.Outputs[0].RowCount != 3 {
		t.Fatalf("E2 publish result malformed: %+v", e2Result.Outputs)
	}
	e2Table := e2Result.Outputs[0].PhysicalName
	if !strings.Contains(e2Table, uprOwnedTag) {
		t.Fatalf("E2 owned-prefix contract violated: production table %q lacks the %q tag", e2Table, uprOwnedTag)
	}
	h.trackTable(e2Table)
	e2Bundle := pubpublication.BundleIdentity{
		Name: e2Recipe, TranslationVersion: "v1", OutputName: e2Output,
		Project: e2Project, DatasetGeneration: e2Identity.DatasetGeneration,
		RecipeDigest: e2Identity.RecipeDigest, SchemaDigest: e2Identity.SchemaDigest,
		ScopeDigest: e2Identity.ScopeDigest, EngineVersion: e2Identity.EngineVersion,
	}
	e2Key := e2Bundle.Key()
	e2Pointer := e2Bundle.PointerName()
	// Free the completed publisher's lease so the sweep COULD claim the key if it
	// were ever eligible (proves survival is the guard, not a held lease).
	_ = h.registry.ReleaseBundleLease(ctx, e2Key, "upr-e2-seed")
	_ = h.registry.ReleaseBundleLease(ctx, e2Key, "owner")

	e2Exec, err := h.registry.GetExecution(ctx, mustE2ID(t, h, e2Key))
	if err != nil {
		t.Fatalf("E2 GetExecution: %v", err)
	}
	e2ID := e2Exec.ID
	if e2Exec.State.Canonical() != pubpublication.BundlePublished {
		t.Fatalf("E2 precondition: state %q, want PUBLISHED", e2Exec.State)
	}
	if !h.tablePresent(ctx, e2Table) {
		t.Fatalf("E2 precondition: physical table %s missing", e2Table)
	}
	leaseActive, leaseErr := h.registry.AcquireBundleLease(ctx, e2Key, "upr-precheck", time.Now().UTC().Add(time.Minute))
	if leaseErr != nil {
		t.Fatalf("E2 lease precondition probe: %v", leaseErr)
	}
	if !leaseActive {
		t.Fatalf("E2 precondition: key %s still lease-held; survival would be vacuous (guard attribution would be unproven)", e2Key)
	}
	_ = h.registry.ReleaseBundleLease(ctx, e2Key, "upr-precheck")

	// Resolution must use the production current.go pointer check; assert
	// pointer-named, state PUBLISHED, queryable, matching selector.
	selector := dataframepublished.DataframeSelector{Recipe: e2Recipe, TranslationVersion: "v1", Output: e2Output}
	reader := uprNewReader(h)
	baselineMat, err := reader.CurrentProjectDataset(ctx, e2Project, selector)
	if err != nil || baselineMat.PhysicalTable != e2Table || baselineMat.State != dataframepublished.StateReady {
		t.Fatalf("E2 resolvability precondition: mat=%s err=%v", uprMaterializationDigest(baselineMat), err)
	}
	baselinePage, baseErr := reader.Page(ctx, baselineMat, uprPageRequest())
	if baseErr != nil || len(baselinePage.Rows) != 3 {
		t.Fatalf("E2 baseline page-read precondition: err=%v rows=%d", uprErrChain(baseErr), len(baselinePage.Rows))
	}
	h.log.addf("STEP 1 E2: production.Publish created PUBLISHED exec %q (table %s, 3 real ClickHouse rows), pointer %q names it, key lease is FREE, reader.CurrentProjectDataset resolves it and Reader.Page returns 3 rows, all with 0 execution read pins — pinsAcquired=%d",
		e2ID, e2Table, e2Pointer, observeCatalog.pinAcquireCount())

	// =========================================================================
	// STEP 2 — companion controls so the sweep is demonstrably LIVE:
	//   KEEP: stale RUNNING doc whose pointer still names it -> guard skips it;
	//   DROP: stale RUNNING doc never pointer-named           -> reclaimed.
	// =========================================================================
	keepPrefix := h.uprOwnedPrefix("keep")
	keepTable := keepPrefix + "t"
	if err := h.seedTable(ctx, keepTable, 3); err != nil {
		t.Fatalf("seed KEEP table: %v", err)
	}
	h.trackTable(keepTable)
	keepRecipe := "upr.keep.recipe"
	keepOutput := uprOwnedTag + h.suffix + "_keep_out"
	keepSpec := uprExecSpec{
		id: keepPrefix + "exec", project: "upr.keep.project", generation: uprOwnedTag + h.suffix + "_keep_gen",
		recipe: keepRecipe, output: keepOutput,
	}
	keepExec := h.seedRunningWithPointer(t, ctx, keepSpec, keepTable)

	dropPrefix := h.uprOwnedPrefix("drop")
	dropTable := dropPrefix + "t"
	if err := h.seedTable(ctx, dropTable, 3); err != nil {
		t.Fatalf("seed DROP table: %v", err)
	}
	h.trackTable(dropTable)
	dropSpec := uprExecSpec{
		id: dropPrefix + "exec", project: "upr.drop.project", generation: uprOwnedTag + h.suffix + "_drop_gen",
		recipe: "upr.drop.recipe", output: uprOwnedTag + h.suffix + "_drop_out",
	}
	dropExec := h.seedRunningNoPointer(t, ctx, dropSpec, dropTable)

	h.log.addf("STEP 2 controls: KEEP=%s (state RUNNING stale via fixture, pointer names it), DROP=%s (stale RUNNING, never pointer-named); both are stale-enumeration snapshots reconcile enumerates", keepExec.ID, dropExec.ID)

	// =========================================================================
	// STEP 3 — CONCURRENT page read vs production Reconcile, with a hard
	// synchronization point so the sweep runs strictly BETWEEN the page read's
	// resolve (reader.CurrentProjectDataset) and its query (reader.Page).
	// (Controlled interleaving — labeled scope limit: this is a controlled
	// interleaving, not a real scheduler race.)
	// =========================================================================
	type pageOutcome struct {
		phase string
		table string
		rows  int
		err   error
	}
	resolveDone := make(chan struct{})
	sweepDone := make(chan struct{})
	var (
		wg          sync.WaitGroup
		resolveErr  error
		resolvedMat dataframepublished.Materialization
		race        pageOutcome
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		resolvedMat, resolveErr = reader.CurrentProjectDataset(ctx, e2Project, selector)
		close(resolveDone)
		if resolveErr != nil {
			// Never block the sweep if resolution itself failed.
			close(sweepDone)
			race.phase = "resolve"
			race.err = resolveErr
			return
		}
		race.table = resolvedMat.PhysicalTable
		<-sweepDone
		page, err := reader.Page(ctx, resolvedMat, uprPageRequest())
		race.phase = "query"
		race.rows = len(page.Rows)
		race.err = err
	}()
	var sweepErr error
	go func() {
		defer wg.Done()
		<-resolveDone
		sweepErr = sweepStore.Reconcile(ctx, time.Now().UTC().Add(-uprOlderThan))
		close(sweepDone)
	}()
	wg.Wait()

	// =========================================================================
	// STEP 4 — classify.
	// =========================================================================
	e2StillPresent := h.tablePresent(ctx, e2Table)
	keepStillPresent := h.tablePresent(ctx, keepTable)
	dropDropped := !h.tablePresent(ctx, dropTable)
	keepState, keepErrField := h.rawExecutionRow(ctx, keepExec.ID)
	dropState, dropErrField := h.rawExecutionRow(ctx, dropExec.ID)
	keepOwner, keepClaimed := observeCatalog.claimOwner(keepSpec.key())
	observeCatalog.pointerReadCount(keepExec.PointerName())
	dropOwner, dropClaimed := observeCatalog.claimOwner(dropSpec.key())
	e2Owner, e2Claimed := observeCatalog.claimOwner(e2Key)
	e2PointerReads := observeCatalog.pointerReadCount(e2Pointer)

	h.log.addf("STEP 4 concurrent sweep: Reconcile err=%v; E2 table %s present=%v; KEEP table %s present=%v (raw state=%q marker=%q, claimed-by=%q claimed=%v); DROP table %s dropped=%v (raw state=%q marker=%q, claimed-by=%q claimed=%v); E2 key claim-by=%q claimed=%v and its pointer read by the sweep %d times",
		uprErrChain(sweepErr), e2Table, e2StillPresent, keepTable, keepStillPresent, keepState, keepErrField, keepOwner, keepClaimed,
		dropTable, dropDropped, dropState, dropErrField, dropOwner, dropClaimed, e2Owner, e2Claimed, e2PointerReads)
	h.log.addf("STEP 4 page read (resolved %q then queried AFTER the whole sweep): phase=%s rows=%d err=%s; sweep never acquired any read pin (pinsAcquired=%d)",
		race.table, race.phase, race.rows, uprErrChain(race.err), observeCatalog.pinAcquireCount())
	h.log.emit(t)

	// Non-vacuity: the sweep must be genuinely live (DROP reclaimed and marked).
	if !dropDropped {
		t.Fatalf("INCONCLUSIVE: production Reconcile did not drop the never-pointer-named stale control %q (state=%q marker=%q) — the sweep may have been a no-op, which would make any E2 survival vacuous", dropExec.ID, dropState, dropErrField)
	}
	if dropErrField != uprReconcileMark || pubpublication.BundleState(dropState).Canonical() != pubpublication.BundleFailed {
		t.Fatalf("INCONCLUSIVE: the stale control %q was not demoted with the reconcile marker (state=%q marker=%q) — the drop path did not run as production", dropExec.ID, dropState, dropErrField)
	}
	if !dropClaimed || !strings.HasPrefix(dropOwner, "reconciler-") {
		t.Fatalf("INCONCLUSIVE: reconcile never claimed the stale control under a reconciler ID (owner=%q claimed=%v) — the sweep may not have reached cleanup", dropOwner, dropClaimed)
	}

	// KEEP must not be dropped; the guard skips it. Attributed live because the
	// concurrent DROP in the SAME sweep was reclaimed.
	if !keepStillPresent {
		t.Fatalf("REGRESSION: the pointer guard did not hold — a stale RUNNING execution still named by its pointer (%s) was dropped by reconcile", keepExec.ID)
	}
	if keepErrField == uprReconcileMark {
		t.Fatalf("REGRESSION: reconcile wrote the %q marker on the pointer-named stale execution %s", uprReconcileMark, keepExec.ID)
	}
	if !keepClaimed || !strings.HasPrefix(keepOwner, "reconciler-") {
		t.Fatalf("guard-skip not proven: reconcile never claimed the KEEP candidate under a reconciler ID (owner=%q claimed=%v) — the KEEP survival may be for a non-guard reason", keepOwner, keepClaimed)
	}
	if reads := observeCatalog.pointerReadCount(keepExec.PointerName()); reads == 0 {
		t.Fatalf("guard-skip not proven: reconcile never reached GetPointer on the KEEP pointer %q (reads=%d)", keepExec.PointerName(), reads)
	}

	// PRIMARY page-read verdict gate: FAILED-ON-PURPOSE only if a real drop hit
	// the resolved-but-not-yet-queried page read with no product change / guard
	// removal.
	if !e2StillPresent || race.err != nil {
		_, hardErr := reader.Page(ctx, resolvedMat, uprPageRequest())
		_, e2Marker := h.rawExecutionRow(ctx, e2ID)
		t.Fatalf("FAILED-ON-PURPOSE CONFIRMED (F-B18 amplifier): an UNPINNED GraphQL page read resolved E2's current published output and, after the concurrent production reconcile sweep, its table %s was gone (present=%v) and the reader.Page query errored [%s]; re-query now [%v]; reconcile marker on E2=%q claimed=%v. No product code was modified and no guard was removed — the page read is a genuine victim of the reconcile DropTable path.",
			e2Table, e2StillPresent, uprErrChain(race.err), hardErr, e2Marker, e2Claimed)
	}
	if race.phase != "query" || race.rows != 3 {
		t.Fatalf("baseline page read did not complete the resolve->query cycle: phase=%s rows=%d err=%s", race.phase, race.rows, uprErrChain(race.err))
	}
	// The sweep must never even have reached the PUBLISHED page-read target.
	if e2Claimed {
		t.Fatalf("attribution check: reconcile claimed the PUBLISHED E2 key (owner=%q) despite the published-state exclusion", e2Owner)
	}
	if e2PointerReads != 0 {
		t.Logf("NOTE: sweep read the E2 pointer %d time(s); these reads came from a non-drop code path", e2PointerReads)
	}

	t.Logf("VERDICT PREVENTED (F-B18 baseline): an unpinned GraphQL page read (rows.go Rows -> published.Reader.Page, 0 execution read pins) resolved the current PUBLISHED pointer-named output and queried it via production reader.Page; a CONCURRENT production reconcile sweep (with the page read resolved) dropped the never-pointer-named stale control %q but left BOTH the page-read target %q (table present=%v, page returned %d rows, no error) and the pointer-named stale control %q intact",
		dropExec.ID, e2ID, e2StillPresent, race.rows, keepExec.ID)
}

// ---------------------------------------------------------------------------
// 2) ABSENT-GUARD CONTROL ONLY (never feeds the verdict): reproduce the F-B12 /
//    release_dangle pointer-blindness shape at the ClickHouse boundary with an
//    unpinned page-read victim added. The drop must be reachable ONLY after BOTH
//    baseline protections are removed by fixture data (row flipped RUNNING via
//    raw AQL AND the pointer row replaced via production CompareAndSwapPointer),
//    which makes the  guard's precondition false while the page read is mid
//    resolve->query. If the unpinned query then errors on the dropped table, that
//    records the amplifier as a fault control; it does NOT fail the test.
// ---------------------------------------------------------------------------

func TestUnpinnedPageReadAbsentGuardFaultControlAgainstArango(t *testing.T) {

	h := uprOpen(t)
	ctx := h.ctx

	observeCatalog := newUprCatalog(h.registry)
	sweepStore, err := NewBundleStore(h.ch, observeCatalog)
	if err != nil {
		t.Fatalf("NewBundleStore(sweep): %v", err)
	}
	publishStore, err := NewBundleStore(h.ch, h.registry)
	if err != nil {
		t.Fatalf("NewBundleStore(publish): %v", err)
	}

	// Publish the victim through production so its table is a real product table.
	proj := "upr.fault.project"
	recipe := "upr.fault.recipe"
	output := uprOwnedTag + h.suffix + "_fault_out"
	identity := pubpublication.PublicationIdentity{
		Name: recipe, TranslationVersion: "v1", OutputName: output,
		Project: proj, DatasetGeneration: uprOwnedTag + h.suffix + "_fault_gen",
		RecipeDigest: "sha256:upr-fault-recipe-" + h.suffix,
		SchemaDigest: "sha256:upr-fault-schema-" + h.suffix,
		ScopeDigest:  "upr-fault-scope", EngineVersion: "loom",
	}
	outStream := []pubpublication.OutputStream{{
		Name: output,
		Columns: []pubpublication.LogicalColumn{{
			ID: "col-upr-value", Name: "value", Kind: "integer", Provenance: pubpublication.ColumnExplicit,
		}},
		Stream: func(streamCtx context.Context, visit func(map[string]any) error) error {
			for i := 1; i <= 3; i++ {
				if err := visit(map[string]any{"value": int64(i)}); err != nil {
					return err
				}
			}
			return nil
		},
	}}
	result, err := pubpublication.Publish(ctx, publishStore, identity, outStream, pubpublication.Limits{})
	if err != nil {
		t.Fatalf("production.Publish(fault victim) failed: %v", err)
	}
	table := result.Outputs[0].PhysicalName
	h.trackTable(table)
	bundle := pubpublication.BundleIdentity{
		Name: recipe, TranslationVersion: "v1", OutputName: output,
		Project: proj, DatasetGeneration: identity.DatasetGeneration,
		RecipeDigest: identity.RecipeDigest, SchemaDigest: identity.SchemaDigest,
		ScopeDigest: identity.ScopeDigest, EngineVersion: identity.EngineVersion,
	}
	key := bundle.Key()
	pointerName := bundle.PointerName()
	// Free the completed publisher's lease so a (blind) sweep could claim it.
	_ = h.registry.ReleaseBundleLease(ctx, key, "upr-fault-seed")
	_ = h.registry.ReleaseBundleLease(ctx, key, "owner")

	victimID := mustE2ID(t, h, key)
	selector := dataframepublished.DataframeSelector{Recipe: recipe, TranslationVersion: "v1", Output: output}
	reader := uprNewReader(h)
	baselineMat, err := reader.CurrentProjectDataset(ctx, proj, selector)
	if err != nil || baselineMat.PhysicalTable != table {
		t.Fatalf("fault-victim resolvability precondition: mat=%s err=%v", uprMaterializationDigest(baselineMat), err)
	}
	if _, perr := reader.Page(ctx, baselineMat, uprPageRequest()); perr != nil {
		t.Fatalf("fault-victim baseline page precondition: %v", uprErrChain(perr))
	}
	h.log.addf("CONTROL setup: PUBLISHED pointer-named victim %q (table %s) resolves and pages 3 rows via current.go pointer check, 0 read pins (pinsAcquired=%d)",
		victimID, table, observeCatalog.pinAcquireCount())

	// Resolve -> [sweep] -> query, with the absent-guard fault injected by the
	// sweep side between resolve and query.
	resolveDone := make(chan struct{})
	sweepDone := make(chan struct{})
	var (
		wg          sync.WaitGroup
		resolvedMat dataframepublished.Materialization
		resolveErr  error
		racePhase   string
		raceRows    int
		queryErr    error
		sweepErr    error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		resolvedMat, resolveErr = reader.CurrentProjectDataset(ctx, proj, selector)
		close(resolveDone)
		if resolveErr != nil {
			close(sweepDone)
			racePhase = "resolve"
			queryErr = resolveErr
			return
		}
		<-sweepDone
		page, err := reader.Page(ctx, resolvedMat, uprPageRequest())
		racePhase = "query"
		raceRows = len(page.Rows)
		queryErr = err
	}()
	go func() {
		defer wg.Done()
		<-resolveDone
		// ABSENT-GUARD FAULT (fixture data only; no product-code change):
		// (a) remove the PUBLISHED-state protection, (b) replace the pointer row
		// (F-B12 / release_dangle) so the  guard's precondition is false.
		h.flipToRunningStale(t, ctx, victimID)
		if csErr := h.registry.CompareAndSwapPointer(ctx, pointerName, victimID, uprOwnedTag+"-shadow-"+uuid.NewString()[:8]); csErr != nil {
			h.log.addf("CONTROL repoint pointer %q away from victim failed: %v", pointerName, uprErrChain(csErr))
		}
		sweepErr = sweepStore.Reconcile(ctx, time.Now().UTC().Add(-uprOlderThan))
		close(sweepDone)
	}()
	wg.Wait()

	stillPresent := h.tablePresent(ctx, table)
	state, errField := h.rawExecutionRow(ctx, victimID)
	owner, claimed := observeCatalog.claimOwner(key)
	h.log.addf("CONTROL post-sweep: victim table %s present=%v (raw state=%q marker=%q), key claimed=%v by=%q; sweep err=%v; page read phase=%s rows=%d queryErr=%s; pinsAcquired=%d",
		table, stillPresent, state, errField, claimed, owner, uprErrChain(sweepErr), racePhase, raceRows, uprErrChain(queryErr), observeCatalog.pinAcquireCount())

	// Preconditions of the fault being truly "absent guard" (not the baseline
	// protections standing): sweep must have claimed the key and re-read (and
	// then ignored) the pointer because it no longer named the victim.
	if !claimed || !strings.HasPrefix(owner, "reconciler-") {
		t.Errorf("fault-control precondition unmet: sweep never claimed the victim key under a reconciler ID (owner=%q claimed=%v)", owner, claimed)
	}
	if reads := observeCatalog.pointerReadCount(pointerName); reads == 0 {
		t.Errorf("fault-control precondition unmet: sweep never re-read the victim pointer %q (reads=%d) — :1073 was not even evaluated", pointerName, reads)
	}
	if stillPresent {
		t.Errorf("fault control did not reproduce: table %s still present after the absent-guard sweep (state=%q marker=%q) — investigate whether the guard really requires the pointer, or whether the flip/repoint fixture was ineffective", table, state, errField)
	} else if queryErr == nil {
		t.Errorf("fault control incomplete: table %s dropped but the UNPINNED page read still succeeded (rows=%d) — the resolve->query window did not overlap the drop", table, raceRows)
	}
	h.log.emit(t)
	if errField != uprReconcileMark {
		t.Logf("NOTE CONTROL: reconcile marker on victim is %q, not %q; classify carefully", errField, uprReconcileMark)
	}
	t.Logf("VERDICT PREVENTED-at-baseline, FAULT-CONTROL-ONLY amplifier note. The fixture flips the execution to RUNNING and replaces its pointer, allowing Reconcile to reach the table while the page read is between resolve and query. The reader query errors on the dropped table (dropped=%v, queryErr=%s). This control records the fault window; it does not establish a baseline drop or a page-read-specific defect.",
		!stillPresent, uprErrChain(queryErr))
}

// ---------------------------------------------------------------------------
// shared seeded-execution helpers (RUNNING docs the sweep claims)
// ---------------------------------------------------------------------------

type uprExecSpec struct {
	id         string
	project    string
	generation string
	recipe     string
	output     string
}

func (s uprExecSpec) key() string { return s.identity().Key() }

func (s uprExecSpec) identity() pubpublication.BundleIdentity {
	return pubpublication.BundleIdentity{
		Name: s.recipe, TranslationVersion: "v1", OutputName: s.output,
		Project: s.project, DatasetGeneration: s.generation,
		RecipeDigest: "sha256:" + s.id + "-recipe", SchemaDigest: "sha256:" + s.id + "-schema",
		ReceiptID: "receipt-" + s.id, ScopeDigest: "scope-" + s.id, EngineVersion: "loom",
	}
}

func (h *uprHarness) buildRunningExec(spec uprExecSpec, owner, table string) pubpublication.BundleExecution {
	stamp := time.Now().UTC().Add(-uprReconcileAge)
	verified := stamp.Add(-time.Minute)
	return pubpublication.BundleExecution{
		ID: spec.id, Key: spec.key(), BundleIdentity: spec.identity(),
		State: pubpublication.BundleRunning,
		Outputs: []pubpublication.BundleOutputRecord{{
			Name: spec.output, PhysicalTable: table,
			Selector: dataframepublished.DataframeSelector{Recipe: spec.recipe, TranslationVersion: "v1", Output: spec.output},
			RowCount: 3, ByteCount: 300,
			State:      pubpublication.BundlePublished,
			VerifiedAt: &verified,
			Columns:    []pubpublication.PhysicalColumn{{Name: "value", ClickHouse: "Int64"}},
		}},
		CreatedAt: stamp.Add(-2 * time.Hour),
		UpdatedAt: stamp,
		OwnerID:   owner,
	}
}

// seedRunningWithPointer: RUNNING doc (old updatedAt) under a live seed lease,
// pointer created naming it via production CompareAndSwapPointer, then lease
// freed. State stays RUNNING so reconcile enumerates it; the LIVE pointer must
// make the  guard skip it.
func (h *uprHarness) seedRunningWithPointer(t *testing.T, ctx context.Context, spec uprExecSpec, table string) pubpublication.BundleExecution {
	t.Helper()
	owner := "upr-seed-" + uuid.NewString()[:8]
	if acquired, err := h.registry.AcquireBundleLease(ctx, spec.key(), owner, time.Now().UTC().Add(5*time.Minute)); err != nil || !acquired {
		t.Fatalf("seed RUNNING acquire lease %s: acquired=%v err=%v", spec.key(), acquired, err)
	}
	exec := h.buildRunningExec(spec, owner, table)
	if err := h.registry.SaveExecution(ctx, exec, owner); err != nil {
		t.Fatalf("seed RUNNING save %s: %v", spec.id, err)
	}
	if err := h.registry.CompareAndSwapPointer(ctx, exec.PointerName(), "", exec.ID); err != nil {
		t.Fatalf("seed RUNNING pointer-name %s: %v", exec.ID, err)
	}
	if err := h.registry.ReleaseBundleLease(ctx, spec.key(), owner); err != nil {
		t.Fatalf("seed RUNNING release lease %s: %v", spec.key(), err)
	}
	return exec
}

// seedRunningNoPointer: RUNNING doc (old updatedAt) never named by any pointer,
// lease freed. Reconcile must reclaim it (live-machinery control).
func (h *uprHarness) seedRunningNoPointer(t *testing.T, ctx context.Context, spec uprExecSpec, table string) pubpublication.BundleExecution {
	t.Helper()
	owner := "upr-seed-" + uuid.NewString()[:8]
	if acquired, err := h.registry.AcquireBundleLease(ctx, spec.key(), owner, time.Now().UTC().Add(5*time.Minute)); err != nil || !acquired {
		t.Fatalf("seed RUNNING(noptr) acquire lease %s: acquired=%v err=%v", spec.key(), acquired, err)
	}
	exec := h.buildRunningExec(spec, owner, table)
	if err := h.registry.SaveExecution(ctx, exec, owner); err != nil {
		t.Fatalf("seed RUNNING(noptr) save %s: %v", spec.id, err)
	}
	if err := h.registry.ReleaseBundleLease(ctx, spec.key(), owner); err != nil {
		t.Fatalf("seed RUNNING(noptr) release lease %s: %v", spec.key(), err)
	}
	return exec
}

// ---------------------------------------------------------------------------
// misc helpers
// ---------------------------------------------------------------------------

// mustE2ID resolves the execution ID for a bundle Key via production
// FindExecutionByKey (the store generates an opaque ID we cannot predict).
func mustE2ID(t *testing.T, h *uprHarness, key string) string {
	t.Helper()
	exec, err := h.registry.FindExecutionByKey(h.ctx, key)
	if err != nil {
		t.Fatalf("FindExecutionByKey(%s): %v", key, err)
	}
	if strings.TrimSpace(exec.ID) == "" {
		t.Fatalf("FindExecutionByKey(%s) returned an empty ID", key)
	}
	return exec.ID
}

var _ uprReader = (*dataframepublished.Reader)(nil)
