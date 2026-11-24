package clickhouse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	pubpublication "github.com/calypr/loom/internal/dataframe/publication"
	pubarango "github.com/calypr/loom/internal/dataframe/publication/arango"
	publishedpkg "github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataset"
	datasetstore "github.com/calypr/loom/internal/dataset/arango"
	storearango "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/store/clickhouse"
	"github.com/calypr/loom/internal/testkit/arangotest"
	"github.com/google/uuid"
)

const (
	uprDatabasePrefix = "loom_verify_upr_"
	uprOwnedTag       = "upr"
	uprReconcileAge   = 90 * time.Minute
	uprOlderThan      = 60 * time.Minute
	uprReconcileMark  = "stale execution reconciled"
)

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

type tableTracker struct {
	mu     sync.Mutex
	tables []string
}

func (h *tableTracker) trackTable(table string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !slices.Contains(h.tables, table) {
		h.tables = append(h.tables, table)
	}
}

func (h *tableTracker) tablesForCleanup() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.tables)
}

type uprHarness struct {
	ctx      context.Context
	endpoint string
	client   *storearango.Client
	registry *pubarango.Registry
	ch       *clickhouse.Client
	database string
	suffix   string
	log      *uprLog

	tableTracker
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
	tables := h.tablesForCleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, table := range tables {
		if err := h.ch.DropTable(ctx, table); err != nil {
			t.Errorf("UPR CH drop %s: %v", table, err)
		}
	}

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

type uprReader interface {
	CurrentProjectDataset(ctx context.Context, project string, selector dataset.DataframeSelector) (publishedpkg.Materialization, error)
	CurrentProjectDatasets(ctx context.Context, project string) ([]publishedpkg.Materialization, error)
	Page(ctx context.Context, materialization publishedpkg.Materialization, req publishedpkg.PageRequest) (publishedpkg.Page, error)
}

func uprNewReader(h *uprHarness) *publishedpkg.Reader {
	return &publishedpkg.Reader{
		ClickHouse: h.ch,
		Catalog:    h.registry,
	}
}

func uprPageRequest() publishedpkg.PageRequest {
	return publishedpkg.PageRequest{Unrestricted: true, First: 5}
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

func uprMaterializationDigest(value publishedpkg.Materialization) string {
	return fmt.Sprintf("{ID=%q Name=%q PhysicalTable=%q State=%q}", value.ID, value.Name, value.PhysicalTable, value.State)
}

func TestUnpinnedPageReadBaselineAgainstArango(t *testing.T) {

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

	_ = h.registry.ReleaseBundleLease(ctx, e2Key, "upr-e2-seed")
	_ = h.registry.ReleaseBundleLease(ctx, e2Key, "owner")

	e2Exec, err := h.registry.GetExecution(ctx, mustE2ID(t, h, e2Key))
	if err != nil {
		t.Fatalf("E2 GetExecution: %v", err)
	}
	e2ID := e2Exec.ID
	if !e2Exec.State.Successful() {
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

	selector := dataset.DataframeSelector{Recipe: e2Recipe, TranslationVersion: "v1", Output: e2Output}
	reader := uprNewReader(h)
	baselineMat, err := reader.CurrentProjectDataset(ctx, e2Project, selector)
	if err != nil || baselineMat.PhysicalTable != e2Table || baselineMat.State != publishedpkg.StateReady {
		t.Fatalf("E2 resolvability precondition: mat=%s err=%v", uprMaterializationDigest(baselineMat), err)
	}
	baselinePage, baseErr := reader.Page(ctx, baselineMat, uprPageRequest())
	if baseErr != nil || len(baselinePage.Rows) != 3 {
		t.Fatalf("E2 baseline page-read precondition: err=%v rows=%d", uprErrChain(baseErr), len(baselinePage.Rows))
	}
	h.log.addf("STEP 1 E2: production.Publish created PUBLISHED exec %q (table %s, 3 real ClickHouse rows), pointer %q names it, key lease is FREE, reader.CurrentProjectDataset resolves it and Reader.Page returns 3 rows, all with 0 execution read pins — pinsAcquired=%d",
		e2ID, e2Table, e2Pointer, observeCatalog.pinAcquireCount())

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
		resolvedMat publishedpkg.Materialization
		race        pageOutcome
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		resolvedMat, resolveErr = reader.CurrentProjectDataset(ctx, e2Project, selector)
		close(resolveDone)
		if resolveErr != nil {

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

	if !dropDropped {
		t.Fatalf("INCONCLUSIVE: production Reconcile did not drop the never-pointer-named stale control %q (state=%q marker=%q) — the sweep may have been a no-op, which would make any E2 survival vacuous", dropExec.ID, dropState, dropErrField)
	}
	if dropErrField != uprReconcileMark || pubpublication.DecodeBundleState(pubpublication.BundleState(dropState)) != pubpublication.BundleFailed {
		t.Fatalf("INCONCLUSIVE: the stale control %q was not demoted with the reconcile marker (state=%q marker=%q) — the drop path did not run as production", dropExec.ID, dropState, dropErrField)
	}
	if !dropClaimed || !strings.HasPrefix(dropOwner, "reconciler-") {
		t.Fatalf("INCONCLUSIVE: reconcile never claimed the stale control under a reconciler ID (owner=%q claimed=%v) — the sweep may not have reached cleanup", dropOwner, dropClaimed)
	}

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

	if !e2StillPresent || race.err != nil {
		_, hardErr := reader.Page(ctx, resolvedMat, uprPageRequest())
		_, e2Marker := h.rawExecutionRow(ctx, e2ID)
		t.Fatalf("FAILED-ON-PURPOSE CONFIRMED (F-B18 amplifier): an UNPINNED GraphQL page read resolved E2's current published output and, after the concurrent production reconcile sweep, its table %s was gone (present=%v) and the reader.Page query errored [%s]; re-query now [%v]; reconcile marker on E2=%q claimed=%v. No product code was modified and no guard was removed — the page read is a genuine victim of the reconcile DropTable path.",
			e2Table, e2StillPresent, uprErrChain(race.err), hardErr, e2Marker, e2Claimed)
	}
	if race.phase != "query" || race.rows != 3 {
		t.Fatalf("baseline page read did not complete the resolve->query cycle: phase=%s rows=%d err=%s", race.phase, race.rows, uprErrChain(race.err))
	}

	if e2Claimed {
		t.Fatalf("attribution check: reconcile claimed the PUBLISHED E2 key (owner=%q) despite the published-state exclusion", e2Owner)
	}
	if e2PointerReads != 0 {
		t.Logf("NOTE: sweep read the E2 pointer %d time(s); these reads came from a non-drop code path", e2PointerReads)
	}

	t.Logf("VERDICT PREVENTED (F-B18 baseline): an unpinned GraphQL page read (rows.go Rows -> published.Reader.Page, 0 execution read pins) resolved the current PUBLISHED pointer-named output and queried it via production reader.Page; a CONCURRENT production reconcile sweep (with the page read resolved) dropped the never-pointer-named stale control %q but left BOTH the page-read target %q (table present=%v, page returned %d rows, no error) and the pointer-named stale control %q intact",
		dropExec.ID, e2ID, e2StillPresent, race.rows, keepExec.ID)
}

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

	_ = h.registry.ReleaseBundleLease(ctx, key, "upr-fault-seed")
	_ = h.registry.ReleaseBundleLease(ctx, key, "owner")

	victimID := mustE2ID(t, h, key)
	selector := dataset.DataframeSelector{Recipe: recipe, TranslationVersion: "v1", Output: output}
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

	resolveDone := make(chan struct{})
	sweepDone := make(chan struct{})
	var (
		wg          sync.WaitGroup
		resolvedMat publishedpkg.Materialization
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
			Selector: dataset.DataframeSelector{Recipe: spec.recipe, TranslationVersion: "v1", Output: spec.output},
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

var _ uprReader = (*publishedpkg.Reader)(nil)

const rdlDatabasePrefix = "loom_verify_rdl_"

const (
	rdlSeedAge = 90 * time.Minute

	rdlReconcileOlderThan = 60 * time.Minute
	rdlReconcileMarker    = "stale execution reconciled"
)

type rdlClickHouse struct {
	mu       sync.Mutex
	dropped  map[string]int
	dropCall map[string]bool
}

func newRDLClickHouse() *rdlClickHouse {
	return &rdlClickHouse{dropped: map[string]int{}, dropCall: map[string]bool{}}
}

func (c *rdlClickHouse) DropTable(ctx context.Context, table string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dropCall[table] = true
	if err := ctx.Err(); err != nil {
		return err
	}
	c.dropped[table]++
	return nil
}

func (c *rdlClickHouse) CreateTable(context.Context, string, []clickhouse.Column) error { return nil }

func (c *rdlClickHouse) InsertRows(context.Context, string, []clickhouse.Column, []map[string]any) error {
	return nil
}

func (c *rdlClickHouse) VerifyOutput(context.Context, string, []clickhouse.Column, int64) error {
	return nil
}

func (c *rdlClickHouse) DropColumns(context.Context, string, []string) error { return nil }

func (c *rdlClickHouse) snapshotDropped() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.dropped))
	for k, v := range c.dropped {
		out[k] = v
	}
	return out
}

func (c *rdlClickHouse) didDrop(table string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dropped[table] > 0
}

type rdlCatalog struct {
	*pubarango.Registry
	mu           sync.Mutex
	leaseClaims  map[string]string
	pointerReads map[string]int
}

func newRDLCatalog(registry *pubarango.Registry) *rdlCatalog {
	return &rdlCatalog{
		Registry:     registry,
		leaseClaims:  map[string]string{},
		pointerReads: map[string]int{},
	}
}

func (c *rdlCatalog) AcquireBundleLease(ctx context.Context, key, owner string, expires time.Time) (bool, error) {
	claimed, err := c.Registry.AcquireBundleLease(ctx, key, owner, expires)
	if claimed {
		c.mu.Lock()
		c.leaseClaims[key] = owner
		c.mu.Unlock()
	}
	return claimed, err
}

func (c *rdlCatalog) GetPointer(ctx context.Context, name string) (pubpublication.BundlePointer, error) {
	c.mu.Lock()
	c.pointerReads[name]++
	c.mu.Unlock()
	return c.Registry.GetPointer(ctx, name)
}

func (c *rdlCatalog) claimedOwner(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	owner, ok := c.leaseClaims[key]
	return owner, ok
}

func (c *rdlCatalog) pointerReadCount(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pointerReads[name]
}

type rdlVerifier struct {
	want       dataset.DataframeSelector
	execution  string
	generation string
	verifiedAt time.Time
}

func (v rdlVerifier) VerifyPublication(_ context.Context, _, generation string, selector dataset.DataframeSelector) (dataset.PublicationVerification, error) {
	if selector != v.want {
		return dataset.PublicationVerification{}, fmt.Errorf("rdl verifier: unexpected selector %+v", selector)
	}
	return dataset.PublicationVerification{
		Selector:    selector,
		ExecutionID: v.execution,
		Generation:  generation,
		State:       "PUBLISHED",
		Queryable:   true,
		VerifiedAt:  v.verifiedAt,
	}, nil
}

func rdlOpen(t *testing.T, budget time.Duration) (context.Context, *storearango.Client, *pubarango.Registry, *datasetstore.Store, string) {
	t.Helper()
	database := arangotest.Owned(t, rdlDatabasePrefix)
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	t.Cleanup(cancel)
	client := database.Client
	if err := client.Bootstrap(ctx, pubarango.BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap bundle collections: %v", err)
	}
	if err := client.Bootstrap(ctx, datasetstore.BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap dataset lifecycle collections: %v", err)
	}
	registry, err := pubarango.New(client)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	lifecycle, err := datasetstore.New(client)
	if err != nil {
		t.Fatalf("new dataset lifecycle store: %v", err)
	}
	return ctx, client, registry, lifecycle, database.Name[len(rdlDatabasePrefix):]
}

type rdlExecSpec struct {
	id         string
	key        string
	project    string
	generation string
	recipe     string
	output     string
	table      string
}

func rdlSelector(recipe, output string) dataset.DataframeSelector {
	return dataset.DataframeSelector{Recipe: recipe, TranslationVersion: "v1", Output: output}
}

func rdlBuildExecution(spec rdlExecSpec, owner string, age time.Time) pubpublication.BundleExecution {
	stamp := age
	verified := age.Add(-time.Minute)
	identity := pubpublication.BundleIdentity{
		Name: spec.recipe, TranslationVersion: "v1", OutputName: spec.output,
		Project: spec.project, DatasetGeneration: spec.generation,
		RecipeDigest: "sha256:" + spec.id + "-recipe", SchemaDigest: "sha256:" + spec.id + "-schema",
		ReceiptID: "receipt-" + spec.id, ScopeDigest: "scope-" + spec.id, EngineVersion: "rdl-engine",
	}
	return pubpublication.BundleExecution{
		ID: spec.id, Key: spec.key, BundleIdentity: identity,
		State: pubpublication.BundleRunning,
		Outputs: []pubpublication.BundleOutputRecord{{
			Name: spec.output, PhysicalTable: spec.table,
			Selector:   rdlSelector(spec.recipe, spec.output),
			RowCount:   11,
			ByteCount:  1100,
			State:      pubpublication.BundlePublished,
			VerifiedAt: &verified,
			Columns:    []pubpublication.PhysicalColumn{{Name: "value", ClickHouse: "Int64"}},
		}},
		CreatedAt: stamp.Add(-2 * time.Hour),
		UpdatedAt: stamp,
		OwnerID:   owner,
	}
}

func rdlSeedRunning(t *testing.T, registry *pubarango.Registry, ctx context.Context, spec rdlExecSpec, owner string) pubpublication.BundleExecution {
	t.Helper()
	if acquired, err := registry.AcquireBundleLease(ctx, spec.key, owner, time.Now().UTC().Add(5*time.Minute)); err != nil || !acquired {
		t.Fatalf("rdl seed acquire lease %s: acquired=%v err=%v", spec.key, acquired, err)
	}
	exec := rdlBuildExecution(spec, owner, time.Now().UTC().Add(-rdlSeedAge))
	if err := registry.SaveExecution(ctx, exec, owner); err != nil {
		t.Fatalf("rdl seed save %s: %v", spec.id, err)
	}
	if err := registry.ReleaseBundleLease(ctx, spec.key, owner); err != nil {
		t.Fatalf("rdl seed release lease %s: %v", spec.key, err)
	}
	return exec
}

func rdlSeedPublished(t *testing.T, registry *pubarango.Registry, ctx context.Context, spec rdlExecSpec, owner string) pubpublication.BundleExecution {
	t.Helper()
	publishedAt := time.Now().UTC().Add(-rdlSeedAge)
	if acquired, err := registry.AcquireBundleLease(ctx, spec.key, owner, time.Now().UTC().Add(5*time.Minute)); err != nil || !acquired {
		t.Fatalf("rdl publish acquire lease %s: acquired=%v err=%v", spec.key, acquired, err)
	}
	running := rdlBuildExecution(spec, owner, publishedAt)
	if err := registry.SaveExecution(ctx, running, owner); err != nil {
		t.Fatalf("rdl publish seed save %s: %v", spec.id, err)
	}
	pub := running
	pub.State = pubpublication.BundlePublished
	pub.PublishedAt = &publishedAt
	pub.ReadyAt = &publishedAt
	pub.UpdatedAt = publishedAt
	pub.Outputs = append([]pubpublication.BundleOutputRecord(nil), running.Outputs...)
	if err := registry.PublishExecution(ctx, pub.PointerName(), "", pub); err != nil {
		_ = registry.ReleaseBundleLease(context.Background(), spec.key, owner)
		t.Fatalf("rdl production PublishExecution %s: %v", spec.id, err)
	}
	if err := registry.ReleaseBundleLease(ctx, spec.key, owner); err != nil {
		t.Fatalf("rdl publish release lease %s: %v", spec.key, err)
	}
	return pub
}

func rdlSetExecutionState(t *testing.T, client *storearango.Client, ctx context.Context, execID, state string) {
	t.Helper()
	patched := false
	err := client.QueryRows(ctx, `LET doc = DOCUMENT(@@collection, @key)
FILTER doc != null
UPDATE doc WITH {state: @state, updatedAt: @updatedAt} IN @@collection
RETURN {patched: true}`, 32, map[string]interface{}{
		"@collection": pubarango.BundleExecutionsCollection,
		"key":         execID,
		"state":       state,
		"updatedAt":   time.Now().UTC().Add(-rdlSeedAge),
	}, func(row map[string]any) error {
		patched, _ = row["patched"].(bool)
		return nil
	})
	if err != nil || !patched {
		t.Fatalf("rdl set %s to %s: patched=%v err=%v", execID, state, patched, err)
	}
}

func rdlCreateAndActivateRelease(t *testing.T, ctx context.Context, lifecycle *datasetstore.Store, project, generation, execID string, selector dataset.DataframeSelector) dataset.ActiveRelease {
	t.Helper()
	ref, err := dataset.NewRef(project, generation)
	if err != nil {
		t.Fatalf("rdl NewRef: %v", err)
	}
	schema, err := dataset.NewSchemaSnapshot("rdl-schema", "4.6.0", strings.Repeat("a", 64), []string{"Patient"})
	if err != nil {
		t.Fatalf("rdl NewSchemaSnapshot: %v", err)
	}
	loading, err := dataset.NewManifest(ref, schema)
	if err != nil {
		t.Fatalf("rdl NewManifest: %v", err)
	}
	if _, err := lifecycle.CreateManifest(ctx, loading); err != nil {
		t.Fatalf("rdl CreateManifest: %v", err)
	}
	if _, err := lifecycle.TransitionManifest(ctx, loading, dataset.StateStaged); err != nil {
		t.Fatalf("rdl TransitionManifest(staged): %v", err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	svc := dataset.ReleaseService{
		Manifests: lifecycle,
		Releases:  lifecycle,
		Verifier:  rdlVerifier{want: selector, execution: execID, generation: generation, verifiedAt: now},
		Now:       func() time.Time { return now },
	}
	if _, err := svc.Create(ctx, dataset.ActivationRequest{
		Project: project, Generation: generation, GitCommit: generation,
		OptionalSelectors: []dataset.DataframeSelector{selector},
	}); err != nil {
		t.Fatalf("rdl ReleaseService.Create: %v", err)
	}
	active, err := svc.Activate(ctx, dataset.ActivationRequest{
		Project: project, Generation: generation, GitCommit: generation,
		ExpectedRevision: 0, OptionalSelectors: []dataset.DataframeSelector{selector},
	})
	if err != nil {
		t.Fatalf("rdl production ReleaseService.Activate (CompareAndSwapActivateRelease): %v", err)
	}

	roundTrip, err := lifecycle.ReadActiveRelease(ctx, project)
	if err != nil {
		t.Fatalf("rdl ReadActiveRelease after activate: %v", err)
	}
	if roundTrip.Release.ID != active.Release.ID || roundTrip.Revision != active.Revision {
		t.Fatalf("rdl active release round-trip mismatch: %+v vs %+v", roundTrip, active)
	}
	found := false
	for _, b := range roundTrip.Release.Publications {
		if b.ExecutionID == execID && b.Selector == selector && !b.Stale && b.Generation == generation {
			found = true
		}
	}
	if !found {
		t.Fatalf("rdl release did not bind exec %s verbatim for selector %+v: %+v", execID, selector, roundTrip.Release.Publications)
	}
	return roundTrip
}

func rdlExecutionRow(t *testing.T, client *storearango.Client, ctx context.Context, execID string) (string, string) {
	t.Helper()
	state := ""
	errField := ""
	err := client.QueryRows(ctx, `RETURN DOCUMENT(@@collection, @key)`, 32, map[string]interface{}{
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
		t.Fatalf("rdl read execution %s: %v", execID, err)
	}
	return state, errField
}

func rdlActivePins(t *testing.T, client *storearango.Client, ctx context.Context, execID string) int {
	t.Helper()
	sum := sha256.Sum256([]byte("state\x00" + execID))
	stateKey := "state_" + hex.EncodeToString(sum[:])
	active := -1
	err := client.QueryRows(ctx, `LET state = DOCUMENT(@@collection, @stateKey)
LET pins = state == null ? [] : NOT_NULL(state.pins, [])
RETURN {active: LENGTH(FOR pin IN pins FILTER pin.expiresAt >= @now RETURN pin), found: state != null}`, 32, map[string]interface{}{
		"@collection": pubarango.ExecutionReadPinsCollection, "stateKey": stateKey, "now": time.Now().UTC(),
	}, func(row map[string]any) error {
		if a, ok := row["active"].(float64); ok {
			active = int(a)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("rdl active pins for %s: %v", execID, err)
	}
	if active < 0 {
		t.Fatalf("rdl active pins for %s: unreadable row", execID)
	}
	return active
}

func rdlMaterializationDigest(values []publishedpkg.Materialization) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("{Name=%q ID=%q PhysicalTable=%q State=%q}", value.Name, value.ID, value.PhysicalTable, value.State))
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func rdlErrChain(err error) string {
	if err == nil {
		return "<nil>"
	}
	parts := make([]string, 0, 4)
	for current := err; current != nil; current = errors.Unwrap(current) {
		parts = append(parts, current.Error())
	}
	return strings.Join(parts, " <- ")
}

func TestReconcileDanglesActiveReleaseBindingAgainstArango(t *testing.T) {
	ctx, client, registry, lifecycle, suffix := rdlOpen(t, 150*time.Second)

	projectActive := "rdl-active-project"
	projectShadow := "rdl-shadow-project"
	genActive := "rdl-gen-" + suffix + "-a"
	genShadow := "rdl-gen-" + suffix + "-b"

	specActive := rdlExecSpec{id: "rdl-active-" + suffix, key: "rdl-key-active-" + suffix, project: projectActive, generation: genActive, recipe: "rdl-recipe-a", output: "out-a", table: "ch_rdl_active_" + suffix}
	specStale := rdlExecSpec{id: "rdl-stale-" + suffix, key: "rdl-key-stale-" + suffix, project: "rdl-stale-project", generation: "rdl-gen-" + suffix + "-s", recipe: "rdl-recipe-s", output: "out-s", table: "ch_rdl_stale_" + suffix}
	specShadow := rdlExecSpec{id: "rdl-shadowed-" + suffix, key: "rdl-key-shadowed-" + suffix, project: projectShadow, generation: genShadow, recipe: "rdl-recipe-b", output: "out-b", table: "ch_rdl_shadow_" + suffix}

	selectorActive := rdlSelector("rdl-recipe-a", "out-a")
	selectorShadow := rdlSelector("rdl-recipe-b", "out-b")

	pubActive := rdlSeedPublished(t, registry, ctx, specActive, "rdl-pub-active")
	if got := rdlActivePins(t, client, ctx, pubActive.ID); got != 0 {
		t.Fatalf("E1/F-B9 precondition: active-release-bound exec %s unexpectedly carries %d live pins at baseline", pubActive.ID, got)
	}
	rdlCreateAndActivateRelease(t, ctx, lifecycle, projectActive, genActive, pubActive.ID, selectorActive)

	reader := &publishedpkg.Reader{Catalog: registry, ActiveReleaseResolver: lifecycle}
	list, err := reader.CurrentProjectDatasets(ctx, projectActive)
	if err != nil || len(list) != 1 || list[0].ID != pubActive.ID+":out-a" || list[0].PhysicalTable != specActive.table {
		t.Fatalf("E1 healthy baseline broken: list=%s err=%v", rdlMaterializationDigest(list), err)
	}
	if single, sErr := reader.CurrentProjectDataset(ctx, projectActive, selectorActive); sErr != nil || single.ID != pubActive.ID+":out-a" {
		t.Fatalf("E1 healthy single-selector baseline broken: %+v err=%v", single, sErr)
	}
	t.Logf("E1 OBSERVED healthy baseline (pre-fault): CurrentProjectDatasets=%s err=%v; F-B9 live pins on active=%d", rdlMaterializationDigest(list), err, rdlActivePins(t, client, ctx, pubActive.ID))

	stale := rdlSeedRunning(t, registry, ctx, specStale, "rdl-seed-stale")

	shadow := rdlSeedPublished(t, registry, ctx, specShadow, "rdl-pub-shadow")
	savedShadow, err := registry.GetExecution(ctx, shadow.ID)
	if err != nil {
		t.Fatalf("shadow precondition unreadable: %v", err)
	}
	if err := registry.CompareAndSwapPointer(ctx, shadow.PointerName(), shadow.ID, "rdl-shadow-newest"); err != nil {
		t.Fatalf("shadow precondition: repoint pointer away failed: %v", err)
	}
	t.Logf("ABSENT-GUARD CONTROL setup verbatim: shadow execution ID=%s Key=%s State=%q Output=%q PhysicalTable=%q PointerName=%q Pointer->%q (F-B7 serves it despite the un-naming because releaseMaterialization/current.go never calls GetPointer)", savedShadow.ID, savedShadow.Key, savedShadow.State, specShadow.output, specShadow.table, shadow.PointerName(), "rdl-shadow-newest")

	rdlCreateAndActivateRelease(t, ctx, lifecycle, projectShadow, genShadow, shadow.ID, selectorShadow)
	shadowBaseline, shadowBaselineErr := reader.CurrentProjectDatasets(ctx, projectShadow)
	if shadowBaselineErr != nil || len(shadowBaseline) != 1 || shadowBaseline[0].PhysicalTable != specShadow.table {
		t.Fatalf("ABSENT-GUARD CONTROL baseline broken: un-named active-release-bound exec not listed before fault: list=%s err=%v", rdlMaterializationDigest(shadowBaseline), shadowBaselineErr)
	}

	rdlSetExecutionState(t, client, ctx, pubActive.ID, "RUNNING")
	rdlSetExecutionState(t, client, ctx, shadow.ID, "RUNNING")

	catalog := newRDLCatalog(registry)
	ch := newRDLClickHouse()
	store, err := NewBundleStore(ch, catalog)
	if err != nil {
		t.Fatalf("NewBundleStore: %v", err)
	}

	store.SetActiveReleaseReader(lifecycle)
	olderThan := time.Now().UTC().Add(-rdlReconcileOlderThan)
	reconcileErr := store.Reconcile(ctx, olderThan)
	if reconcileErr != nil {

		t.Logf("NOTE: production Reconcile returned %v (expected nil for all three candidates)", reconcileErr)
	}
	dropped := ch.snapshotDropped()

	activeState, activeErr := rdlExecutionRow(t, client, ctx, pubActive.ID)
	staleState, staleErr := rdlExecutionRow(t, client, ctx, stale.ID)
	shadowState, shadowErr := rdlExecutionRow(t, client, ctx, shadow.ID)

	primaryTableDropped := ch.didDrop(specActive.table)
	primaryDemotedByReconcile := activeErr == rdlReconcileMarker ||
		(pubpublication.DecodeBundleState(pubpublication.BundleState(activeState)) == pubpublication.BundleFailed)

	if primaryTableDropped || primaryDemotedByReconcile {

		hardList, hardErr := reader.CurrentProjectDatasets(ctx, projectActive)
		listBrokeHard := hardErr != nil && len(hardList) == 0
		if listBrokeHard {
			t.Fatalf("FAILED-ON-PURPOSE CONFIRMED of the composite F-B9 x F-B1 x F-B7 against real Arango: an ACTIVE-release-bound + pointer-named execution was dangled by startup Reconcile and its current dataset list failed wholesale. active=%q state=%q reconcileMarker=%q tableDropped=%v (dropped=%v); F-B9 live pins=%d (release path pins nothing → only the pointer re-check + active-release binding veto shield); catalog observed reconcile lease-claim owner=%q and %d pointer re-read(s) of %q; CurrentProjectDatasets post-fault list=[%d] err=[%s]",
				pubActive.ID, activeState, activeErr, primaryTableDropped, dropped,
				rdlActivePins(t, client, ctx, pubActive.ID), mustOwner(t, catalog, specActive.key), catalog.pointerReadCount(pubActive.PointerName()), pubActive.PointerName(),
				len(hardList), rdlErrChain(hardErr))
		} else {
			t.Fatalf("inconsistent CONFIRMED: active exec looked dangled (dropped=%v marker=%q state=%q) but the current list survived: list=[%d] err=%v — re-examine attribution", primaryTableDropped, activeErr, activeState, len(hardList), rdlErrChain(hardErr))
		}
	}

	if primaryTableDropped {
		t.Fatalf("REGRESSION: production Reconcile dropped the physical table of a pointer-named, ACTIVE-release-bound execution (dropped=%v)", dropped)
	}
	if activeErr == rdlReconcileMarker {
		t.Fatalf("REGRESSION: production Reconcile wrote the %q demotion marker on a pointer-named, ACTIVE-release-bound execution (error=%q)", rdlReconcileMarker, activeErr)
	}
	if owner, ok := catalog.claimedOwner(specActive.key); !ok || !strings.HasPrefix(owner, "reconciler-") {
		t.Fatalf("guard-skip not proven: reconcile never claimed the active candidate under a reconciler ID (owner=%q claimed=%v); the sweep may have been a no-op, which would make the PREVENTED verdict vacuous", owner, ok)
	}
	if reads := catalog.pointerReadCount(pubActive.PointerName()); reads == 0 {
		t.Fatalf("guard-skip not proven: reconcile never reached GetPointer on %q, so it may have skipped for a non-guard reason (reads=%d)", pubActive.PointerName(), reads)
	}
	if pins := rdlActivePins(t, client, ctx, pubActive.ID); pins != 0 {
		t.Fatalf("F-B9 evidence violated: the active-release-bound exec unexpectedly carries pins post-Reconcile: count=%d", pins)
	}

	rdlSetExecutionState(t, client, ctx, pubActive.ID, "PUBLISHED")
	healedList, healedErr := reader.CurrentProjectDatasets(ctx, projectActive)
	if healedErr != nil || len(healedList) != 1 || healedList[0].ID != pubActive.ID+":out-a" || healedList[0].PhysicalTable != specActive.table {
		t.Fatalf("PREVENTED claim broken: after Reconcile (which must have skipped the active exec) and restoring only the harness's RUNNING flag, the release read degraded: list=%s err=%v", rdlMaterializationDigest(healedList), healedErr)
	}
	singleAfter, singleErr := reader.CurrentProjectDataset(ctx, projectActive, selectorActive)
	if singleErr != nil || singleAfter.ID != pubActive.ID+":out-a" {
		t.Fatalf("PREVENTED single-selector regression: %+v err=%v", singleAfter, singleErr)
	}
	t.Logf("E2 OBSERVED PREVENTED: reconcile enumerated+claimed (owner=%s) and re-read the pointer %q naming %q %d time(s), and its production guard branch skipped cleanup; active=%q post state=%q reconcileErrorField=%q table=%q dropped=%v; CurrentProjectDatasets post-fault =1=%s err=%v; single=%q err=%v",
		mustOwner(t, catalog, specActive.key), pubActive.PointerName(), pubActive.ID, catalog.pointerReadCount(pubActive.PointerName()), pubActive.ID, activeState, activeErr, specActive.table, ch.didDrop(specActive.table), rdlMaterializationDigest(healedList), healedErr, singleAfter.ID, singleErr)

	if !ch.didDrop(specStale.table) || staleErr != rdlReconcileMarker || pubpublication.DecodeBundleState(pubpublication.BundleState(staleState)) != pubpublication.BundleFailed {
		t.Fatalf("LIVE-MACHINERY control broken: reconcile did not reclaim the unpointered/unbound stale control (staleState=%q staleErr=%q dropped=%v) — the sweep was not genuinely live, so the PREVENTED verdict above would be vacuous", staleState, staleErr, dropped)
	}
	t.Logf("E3 OBSERVED live machinery: the unpointered stale RUNNING control %s was reclaimed (state=%q marker=%q tableDropped=%v)", stale.ID, staleState, staleErr, ch.didDrop(specStale.table))

	if ch.didDrop(specShadow.table) || shadowErr == rdlReconcileMarker || pubpublication.DecodeBundleState(pubpublication.BundleState(shadowState)) != pubpublication.BundleRunning {
		t.Errorf("REGRESSION (F-B12): the unpointered ACTIVE-release-bound execution %s was reclaimed by Reconcile (state=%q marker=%q tableDropped=%v) — the post-claim active-release binding veto must deny this drop; the ACTIVE release still binds it (archived FAILED-ON-PURPOSE red: this drop destroyed advertised data and failed CurrentProjectDatasets wholesale)", shadow.ID, shadowState, shadowErr, ch.didDrop(specShadow.table))
	}
	rdlSetExecutionState(t, client, ctx, shadow.ID, "PUBLISHED")
	shadowAfter, shadowListErr := reader.CurrentProjectDatasets(ctx, projectShadow)
	if shadowListErr != nil || len(shadowAfter) != 1 || shadowAfter[0].ID != shadow.ID+":out-b" {
		t.Fatalf("binding-veto claim broken: with the drop denied and only the harness's RUNNING flag undone, the shadow release read is not healthy: list=%s err=[%s]", rdlMaterializationDigest(shadowAfter), rdlErrChain(shadowListErr))
	}

	t.Logf("VERDICT PREVENTED for the composite F-B9 x F-B1 x F-B7: the pointer-named active-release-bound exec survived startup Reconcile via the pointer re-check, and the unpointered bound exec survived via the active-release binding veto (Repair-1b); live pins=%d; guard branch is enforced by the live post-reconcile state assertions", rdlActivePins(t, client, ctx, pubActive.ID))

	t.Logf("DISPROVEN log-claim: production has no 'skipping bundle with pointer' log string; the pointer/binding skip is silent (releases the lease and returns), so the skip is proven behaviorally: reconcile reached candidate %q, claimed it under reconciler ID %q and observed %d pointer re-read(s), then dropped NOTHING for %q (active table not dropped: %v) while reclaiming %q (stale control; control dropped=%v; shadow dropped=%v)",
		pubActive.ID, mustOwner(t, catalog, specActive.key), catalog.pointerReadCount(pubActive.PointerName()), pubActive.ID, ch.didDrop(specActive.table), stale.ID, ch.didDrop(specStale.table), ch.didDrop(specShadow.table))

	t.Logf("BINDING-VETO ARM OBSERVED (Repair-1b; the archived F-B12 red direction): unpointered ACTIVE-release-bound shadow=%s state=%q marker=%q tableDropped=%v; with only the harness RUNNING flag undone CurrentProjectDatasets(shadow project) is healthy =%d entries err=[%s] — the binding veto, not the pointer, shielded this one",
		shadow.ID, shadowState, shadowErr, ch.didDrop(specShadow.table), len(shadowAfter), rdlErrChain(shadowListErr))
}

func mustOwner(t *testing.T, c *rdlCatalog, key string) string {
	t.Helper()
	owner, ok := c.claimedOwner(key)
	if !ok {
		return "<never-claimed>"
	}
	return owner
}

type fb1Catalog struct {
	*pubarango.Registry
	race    func()
	raceRan bool
}

func (c *fb1Catalog) ClaimExecutionCleanup(ctx context.Context, executionID, owner string) (bool, error) {
	if !c.raceRan {
		c.raceRan = true
		if c.race != nil {
			c.race()
		}
	}
	return c.Registry.ClaimExecutionCleanup(ctx, executionID, owner)
}

func fb1Setup(t *testing.T) (*pubarango.Registry, publicationSetup) {
	t.Helper()
	database := arangotest.Owned(t, "loom_verify_fb1_")
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
	return registry, publicationSetup{ctx: ctx, suffix: database.Name[len("loom_verify_fb1_"):]}
}

type publicationSetup struct {
	ctx    context.Context
	suffix string
}

func fb1Execution(suffix string) pubpublication.BundleExecution {
	identity := pubpublication.BundleIdentity{
		Name: "fb1-e2e", TranslationVersion: "1", OutputName: "main",
		Project: "loom_verify", DatasetGeneration: "fb1-" + suffix,
		RecipeDigest: "r-" + suffix, SchemaDigest: "s-" + suffix,
		ScopeDigest: "sc-" + suffix, EngineVersion: "1",
	}
	now := time.Now().UTC()
	return pubpublication.BundleExecution{
		ID: "fb1-" + suffix, Key: "fb1-key-" + suffix, BundleIdentity: identity,
		State:     pubpublication.BundleRunning,
		Outputs:   []pubpublication.BundleOutputRecord{{Name: "main", PhysicalTable: "loom_bundle_fb1_" + suffix, RowCount: 10}},
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-time.Hour),
	}
}

func fb1Seed(t *testing.T, registry *pubarango.Registry, ctx context.Context, exec pubpublication.BundleExecution) {
	t.Helper()
	acquired, err := registry.AcquireBundleLease(ctx, exec.Key, "seed", time.Now().UTC().Add(2*time.Minute))
	if err != nil || !acquired {
		t.Fatalf("seed acquire lease: acquired=%v err=%v", acquired, err)
	}
	if err := registry.SaveExecution(ctx, exec, "seed"); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	if err := registry.ReleaseBundleLease(ctx, exec.Key, "seed"); err != nil {
		t.Fatalf("seed release: %v", err)
	}
}

func TestReconcileDropsJustPublishedBundleAgainstArango(t *testing.T) {
	registry, setup := fb1Setup(t)
	exec := fb1Execution(setup.suffix)
	fb1Seed(t, registry, setup.ctx, exec)

	pointerName := exec.PointerName()
	runner := "runner-" + uuid.NewString()
	wrapper := &fb1Catalog{Registry: registry}
	wrapper.race = func() {
		ctx := context.Background()
		acquired, err := registry.AcquireBundleLease(ctx, exec.Key, runner, time.Now().UTC().Add(2*time.Minute))
		if err != nil || !acquired {
			t.Errorf("racing runner could not take over through real AcquireBundleLease: acquired=%v err=%v", acquired, err)
			return
		}
		published := exec
		published.State = pubpublication.BundlePublished
		published.OwnerID = runner
		now := time.Now().UTC()
		published.PublishedAt = &now
		published.UpdatedAt = now
		if err := registry.PublishExecution(ctx, pointerName, exec.ID, published); err != nil {
			t.Errorf("racing publish rejected by real AQL: %v", err)
		}
	}

	ch := &tocFakeClickHouse{table: exec.Outputs[0].PhysicalTable}
	store, err := NewBundleStore(ch, wrapper)
	if err != nil {
		t.Fatalf("NewBundleStore: %v", err)
	}

	store.leaseTTL = -time.Second

	reconcileErr := store.Reconcile(setup.ctx, time.Now().UTC().Add(-time.Minute))

	pointer, err := registry.GetPointer(setup.ctx, pointerName)
	if err != nil {
		t.Fatalf("real GetPointer after reconcile: %v", err)
	}
	if pointer.ExecutionID != exec.ID {
		t.Fatalf("precondition: live pointer does not name the published execution: %+v", pointer)
	}
	if row, err := registry.GetExecution(setup.ctx, exec.ID); err != nil || row.State != pubpublication.BundlePublished {
		t.Fatalf("precondition: row is not PUBLISHED in Arango: %+v err=%v", row, err)
	}
	if ch.table == "" {
		t.Errorf("REGRESSION (F-B1, real Arango): reconcileExecution dropped the physical tables of a bundle the pointer advertises (pointer names %q, row PUBLISHED, tables deleted); the fenced SaveExecution error chain was then: %v (errors.Is ErrBundleLeaseLost=%v) — this is the archived CONFIRMED red returning", pointer.ExecutionID, reconcileErr, errors.Is(reconcileErr, pubpublication.ErrBundleLeaseLost))
	} else {

		if reconcileErr != nil {
			t.Fatalf("prevented drop must reconcile cleanly, got %v", reconcileErr)
		}
		t.Log("PREVENTED (Repair-1b): reconcile reached+claimed the candidate and the post-claim live pointer re-check vetoed DropTable; table and PUBLISHED state survive")
	}
	t.Cleanup(func() {
		_ = registry.ReleaseBundleLease(context.Background(), exec.Key, runner)
		_ = registry.ReleaseExecutionCleanup(context.Background(), exec.ID, "reconciler")
	})
}

func TestReconcileSkipsPointerNamedExecutionAgainstArango(t *testing.T) {
	registry, setup := fb1Setup(t)
	exec := fb1Execution(setup.suffix)
	fb1Seed(t, registry, setup.ctx, exec)
	if err := registry.CompareAndSwapPointer(setup.ctx, exec.PointerName(), "", exec.ID); err != nil {
		t.Fatalf("pre-publish pointer: %v", err)
	}
	ch := &tocFakeClickHouse{table: exec.Outputs[0].PhysicalTable}
	store, err := NewBundleStore(ch, &fb1Catalog{Registry: registry})
	if err != nil {
		t.Fatalf("NewBundleStore: %v", err)
	}
	store.leaseTTL = -time.Second
	if err := store.Reconcile(setup.ctx, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if ch.table != exec.Outputs[0].PhysicalTable {
		t.Fatalf("regression: the pointer skip failed and dropped a pointer-named execution")
	}
	t.Cleanup(func() {
		_ = registry.ReleaseBundleLease(context.Background(), exec.Key, "reconciler")
	})
}

type retryStateCatalog struct {
	pubpublication.BundleCatalog
	execution pubpublication.BundleExecution
	claims    int
}

func (c *retryStateCatalog) FindExecutionByKey(context.Context, string) (pubpublication.BundleExecution, error) {
	return c.execution, nil
}

func (c *retryStateCatalog) AcquireBundleLease(context.Context, string, string, time.Time) (bool, error) {
	c.claims++
	return false, nil
}

func TestBeginBundleClassifiesLegacyRetryState(t *testing.T) {
	now := time.Now().UTC()
	active, expired := now.Add(time.Hour), now.Add(-time.Hour)
	for _, tc := range []struct {
		name      string
		state     pubpublication.BundleState
		expires   *time.Time
		published bool
		claims    int
	}{
		{name: "already published", state: "READY", published: true},
		{name: "active running lease", state: "LOADING", expires: &active},
		{name: "expired running lease", state: "LOADING", expires: &expired, claims: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog := &retryStateCatalog{execution: pubpublication.BundleExecution{
				ID: "legacy-execution", State: tc.state, LeaseExpiresAt: tc.expires,
			}}
			service := &ClickHouseBundleStore{catalog: catalog, keyLocks: make(map[string]*bundleKeyLock)}
			tx, err := service.beginBundle(context.Background(), pubpublication.BundleIdentity{Name: "legacy-recipe"})
			if tc.published {
				if err != nil || tx == nil || !tx.Idempotent() || tx.execution.ID != catalog.execution.ID {
					t.Fatalf("begin published execution = (%+v, %v)", tx, err)
				}
			} else if !errors.Is(err, ErrBundleInFlight) {
				t.Fatalf("begin in-flight execution error = %v, want %v", err, ErrBundleInFlight)
			}
			if catalog.claims != tc.claims {
				t.Fatalf("cleanup lease claims = %d, want %d", catalog.claims, tc.claims)
			}
		})
	}
}
