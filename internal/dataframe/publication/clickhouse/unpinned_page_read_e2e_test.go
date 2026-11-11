package clickhouse

// F-B18 (adversarial adjudication of the "unpinned GraphQL page read" hypothesis).
//
// This file owns EXACTLY one verification artifact. It never mutates product
// code and never removes a production guard; the honest verdict is asserted
// against unmodified production source read live from disk plus a real Arango +
// real ClickHouse execution driven only through production entry points.
//
// ---------------------------------------------------------------------------
// SOURCE-VERIFIED FACTS (quoted verbatim from the compiled repo at run time by
// the quote gate below; the gate FAILS the test if any anchor drifts, so the
// claim can never survive a silent refactor).
//
//  1. The GraphQL page-read surface resolves the current PUBLISHED execution and
//     immediately queries its ClickHouse table with NO execution read pin:
//       - internal/api/graphql/graph/dataframe/rows.go:62 resolves via
//         `value, access, err := s.currentProjectDataset(...)` and rows.go:67
//         forwards to the reader with `page, err := s.reader.Page(ctx, value, ...)`.
//       - internal/dataframe/published/page.go:36 `func (r *Reader) Page(...)`
//         builds, at :72, `query := fmt.Sprintf("SELECT %s FROM `%s`", selectColumns,
//         materialization.PhysicalTable)` and issues it, at :114, with
//         `rows, err := r.ClickHouse.QueryRowsArgs(ctx, query, readColumns, args...)`
//         — a bare FROM on the resolved table with no pin acquisition anywhere
//         in the resolve->query path.
//     Verified by the quote gate's forgotten-symbol scan (uprQuotes entries with
//     forgotten: AcquireExecutionReadPin / WithExecutionReadPins) that
//     rows.go, page.go and current.go each contain ZERO occurrences of them.
//  2. Resolution is pointer-gated, so a page read can ONLY ever target a
//     pointer-named PUBLISHED output: internal/dataframe/published/current.go:117
//     `pointer, err := r.Catalog.GetPointer(ctx, execution.PointerName())` and
//     current.go:121 `if pointer.ExecutionID != execution.ID {`.
//  3. The two, and only two, non-test execution read pins are on the EXPORT /
//     exact-stream path, not the page-read path:
//       - internal/dataframe/published/read_pins.go:63
//         `owned, err := pins.AcquireExecutionReadPin(ctx, id, owner, ...)`
//         (inside withExecutionReadPins, which resolves revisions INSIDE visit so
//         cleanup cannot drop a table between metadata lookup and pin acquire), and
//       - internal/dataframe/published/export.go:135
//         `acquired, err := pins.AcquireExecutionReadPin(ctx, result.ExecutionID,
//         result.ReaderID, expires)` (StreamExactExport).
//     These are the pinned contrast. (The brief cited internal/dataframe/pinned/
//     executor.go; that package does not exist at this commit — the pins live as
//     Reader methods in published/read_pins.go and published/export.go. Reported
//     as a correction, not an assumption.)
//  4. The production reconcile sweep cannot reach the page read's target:
//       - Reconcile enumerates only RUNNING/VALIDATING/FAILED, EXCLUDING PUBLISHED
//         (bundle_store.go:1043).
//       - reconcileExecution re-reads the visibility pointer AFTER claiming the
//         key and returns after releasing the lease when it still names the
//         execution (bundle_store.go:1068-1075); the DropTable is reachable only
//         when that guard is blind (`stale execution reconciled` at :1090).
//
// EXPERIMENT (real Arango + real ClickHouse + real Reconcile):
//
//   Baseline (PREVENTED expected; FAILED-ON-PURPOSE if a real drop hits the
//   page read's resolved-but-not-yet-queried execution with no fault):
//     - publish E2 through production publication.Publish against the real
//       ClickHouse client (real table, 3 rows, pointer names it, 0 read pins);
//     - simulate a live page-read session: resolve via the same production
//       current.go pointer check (reader.CurrentProjectDataset), then query via
//       reader.Page;
//     - run the production Reconcile sweep CONCURRENTLY in the same process with
//       a hard synchronization point so the sweep runs BETWEEN resolve and query.
//     Companion controls in the same sweep make the result non-vacuous:
//       - KEEP: a stale (old updatedAt) RUNNING doc whose pointer still names it
//         -> the :1073 guard must skip it (table retained, no marker);
//       - DROP: a stale RUNNING doc never named by any pointer -> must be dropped
//         + demoted with the reconcile marker (sweep is live).
//   Verdict PREVENTED, preventing lines: bundle_store.go:1043 (excludes
//   PUBLISHED) and bundle_store.go:1068-1075 (guard, guarded by :1073).
//
//   Absent-guard CONTROL ONLY (never feeds the verdict; the F-B12 / release_dangle
//   pointer-blindness shape, reproduced at the ClickHouse boundary with a
//   page-read victim added): a PUBLISHED pointer-named output's table must be
//   reachable ONLY after both baseline protections are removed by fixture data —
//   the row flipped RUNNING (raw AQL) AND the pointer repointed away
//   (production CompareAndSwapPointer, i.e. the pointer row replaced), so the
//   guard's precondition is false while the page read is mid resolve->query.
//   If the unpinned page read's query then errors on the dropped table, that
//   records the amplifier as a FAULT CONTROL — it does not establish a baseline
//   drop and does not make the test fail.
//
// Opt-in (SKIP-clean without the env):
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
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	arangofactory "github.com/calypr/loom/internal/backends/arango"
	pubpublication "github.com/calypr/loom/internal/dataframe/publication"
	dataframepublished "github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/store/clickhouse"
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
	uprReconcileMark  = "stale execution reconciled" // bundle_store.go:1090
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
	*arangofactory.PublishedRegistry
	mu           sync.Mutex
	claims       map[string]string
	pointerReads map[string]int
	pinsAcquired int
}

func newUprCatalog(registry *arangofactory.PublishedRegistry) *uprCatalog {
	return &uprCatalog{PublishedRegistry: registry, claims: map[string]string{}, pointerReads: map[string]int{}}
}

func (c *uprCatalog) AcquireBundleLease(ctx context.Context, key, owner string, expires time.Time) (bool, error) {
	claimed, err := c.PublishedRegistry.AcquireBundleLease(ctx, key, owner, expires)
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
	return c.PublishedRegistry.GetPointer(ctx, name)
}

func (c *uprCatalog) AcquireExecutionReadPin(ctx context.Context, executionID, owner string, expires time.Time) (bool, error) {
	c.mu.Lock()
	c.pinsAcquired++
	c.mu.Unlock()
	return c.PublishedRegistry.AcquireExecutionReadPin(ctx, executionID, owner, expires)
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
	client   *arangofactory.Client
	registry *arangofactory.PublishedRegistry
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
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	chURL := strings.TrimSpace(os.Getenv("LOOM_CLICKHOUSE_URL"))
	if endpoint == "" || chURL == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_CLICKHOUSE_URL to run the F-B18 unpinned-page-read reproduction")
	}
	database := uprDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(database, uprDatabasePrefix) {
		t.Fatalf("verification database %q escapes the %s prefix", database, uprDatabasePrefix)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	t.Cleanup(cancel)
	arangoBackend, err := arangofactory.Open(ctx, arangofactory.Config{URL: endpoint, Database: database})
	if err != nil {
		t.Fatalf("open %s: %v", database, err)
	}
	client := arangoBackend.Client
	t.Logf("UPR_DATABASE=%s", database)
	// Registered FIRST so it runs LAST: ClickHouse artifacts must be dropped and
	// asserted empty before the Arango database disappears (LIFO cleanup order).
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		uprDropDatabase(t, endpoint, database)
	})
	registry, err := arangofactory.NewPublishedRegistry(client)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if err := client.Bootstrap(ctx, arangofactory.PublishedBootstrapSpec()); err != nil {
		t.Fatalf("bootstrap bundle collections: %v", err)
	}
	ch, err := clickhouse.New(clickhouse.Options{
		URL:      chURL,
		Database: os.Getenv("LOOM_CLICKHOUSE_DATABASE"),
		Username: os.Getenv("LOOM_CLICKHOUSE_USERNAME"),
		Password: os.Getenv("LOOM_CLICKHOUSE_PASSWORD"),
		Timeout:  20 * time.Second,
	})
	if err != nil {
		t.Fatalf("clickhouse client: %v", err)
	}
	if err := ch.Ping(ctx); err != nil {
		t.Fatalf("ping clickhouse: %v", err)
	}
	harness := &uprHarness{
		ctx:      ctx,
		endpoint: endpoint,
		client:   client,
		registry: registry,
		ch:       ch,
		database: database,
		suffix:   database[len(uprDatabasePrefix):],
		log:      &uprLog{},
	}
	t.Cleanup(func() { harness.drainTablesAndClose(t) })
	return harness
}

// uprOwnedPrefix returns a globally-unique ClickHouse table prefix that contains
// the uprOwnedTag marker, e.g. "loom_bundle_upr5f3a..._".
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

func (h *uprHarness) seedPublishedOutputRecord(name, table, project, generation, recipe string) pubpublication.BundleOutputRecord {
	verified := time.Now().UTC().Add(-uprReconcileAge)
	return pubpublication.BundleOutputRecord{
		Name:          name,
		PhysicalTable: table,
		Selector:      dataframepublished.DataframeSelector{Recipe: recipe, TranslationVersion: "v1", Output: name},
		RowCount:      3,
		ByteCount:     300,
		State:         pubpublication.BundlePublished,
		VerifiedAt:    &verified,
		Columns:       []pubpublication.PhysicalColumn{{Name: "value", ClickHouse: "Int64"}},
	}
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

func (h *uprHarness) drainTablesAndClose(t *testing.T) {
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
	_ = h.ch.Close()
}

func uprDropDatabase(t *testing.T, endpoint, database string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(database), nil)
	if err != nil {
		t.Errorf("build cleanup request for %s: %v", database, err)
		return
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Errorf("drop database %s: %v", database, err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusForbidden {
		t.Errorf("drop database %s: status %d", database, response.StatusCode)
		return
	}
	t.Logf("UPR_DATABASE_REMOVED=%s", database)
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
		"@collection": arangofactory.BundleExecutionsCollection, "key": execID,
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
		"@collection": arangofactory.BundleExecutionsCollection,
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
// source-quote gate (drift-proof)
// ---------------------------------------------------------------------------

func uprRepoRoot(t *testing.T) string {
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

type uprQuote struct {
	file       string
	needles    []string
	forgotten  []string // substrings that must NOT appear (pin never added to this path)
	anchorDesc string
}

var uprQuotes = []uprQuote{
	{
		file:       "internal/api/graphql/graph/dataframe/rows.go",
		anchorDesc: "page-read resolution+query in rows.go",
		needles: []string{
			"value, access, err := s.currentProjectDataset(ctx, input.ProjectID, selector)",
			"page, err := s.reader.Page(ctx, value, dfmaterialization.PageRequest",
			"s.logReadFailure(ctx, \"clickhouse_rows\"",
		},
		// No read pin may ever be acquired on the page-read surface.
		forgotten: []string{"AcquireExecutionReadPin", "WithExecutionReadPins"},
	},
	{
		file:       "internal/dataframe/published/page.go",
		anchorDesc: "Reader.Page builds the physical-table FROM and runs it",
		needles: []string{
			"func (r *Reader) Page(ctx context.Context, materialization Materialization, req PageRequest) (Page, error)",
			"materialization.PhysicalTable",
			"r.ClickHouse.QueryRowsArgs(ctx, query, readColumns, args...)",
		},
		forgotten: []string{"AcquireExecutionReadPin", "WithExecutionReadPins"},
	},
	{
		file:       "internal/dataframe/published/current.go",
		anchorDesc: "pointer-gated resolution (page read targets only pointer-named PUBLISHED rows)",
		needles: []string{
			"pointer, err := r.Catalog.GetPointer(ctx, execution.PointerName())",
			"if pointer.ExecutionID != execution.ID {",
		},
		forgotten: []string{"AcquireExecutionReadPin", "WithExecutionReadPins"},
	},
	{
		file:       "internal/dataframe/published/read_pins.go",
		anchorDesc: "pinned contrast #1 (export/exact-stream path)",
		needles: []string{
			"owned, err := pins.AcquireExecutionReadPin(ctx, id, owner, time.Now().UTC().Add(ttl))",
		},
	},
	{
		file:       "internal/dataframe/published/export.go",
		anchorDesc: "pinned contrast #2 (StreamExactExport)",
		needles: []string{
			"acquired, err := pins.AcquireExecutionReadPin(ctx, result.ExecutionID, result.ReaderID, expires)",
		},
	},
	{
		file:       "internal/dataframe/publication/clickhouse/bundle_store.go",
		anchorDesc: "reconcile sweep excludes PUBLISHED + pointer guard",
		needles: []string{
			"states := []publication.BundleState{publication.BundleRunning, publication.BundleValidating, publication.BundleFailed}",
			"pointer, pointerErr := s.catalog.GetPointer(cleanupCtx, execution.PointerName())",
			"if pointerErr == nil && pointer.ExecutionID == execution.ID {",
			"return s.catalog.ReleaseBundleLease(cleanupCtx, execution.Key, reconcilerID)",
			"execution.Error = \"stale execution reconciled\"",
		},
	},
}

func uprQuoteAndVerifySource(t *testing.T) string {
	t.Helper()
	root := uprRepoRoot(t)
	var builder strings.Builder
	for _, quote := range uprQuotes {
		raw, err := os.ReadFile(filepath.Join(root, quote.file))
		if err != nil {
			t.Fatalf("claim source %s unreadable: %v", quote.file, err)
		}
		text := string(raw)
		lines := strings.Split(text, "\n")
		builder.WriteString(fmt.Sprintf("--- %s [%s] ---\n", quote.file, quote.anchorDesc))
		for _, needle := range quote.needles {
			lineNo := -1
			for i, line := range lines {
				if strings.Contains(line, needle) {
					lineNo = i + 1
					break
				}
			}
			if lineNo < 0 {
				t.Fatalf("claim drift: %s no longer contains %q — re-verify F-B18 against current production before trusting any verdict", quote.file, needle)
			}
			builder.WriteString(fmt.Sprintf("  %d: %s\n", lineNo, strings.TrimSpace(lines[lineNo-1])))
		}
		for _, forbidden := range quote.forgotten {
			if strings.Contains(text, forbidden) {
				t.Fatalf("no-pin claim broken: %s unexpectedly references %q; the page-read path appears to pin now — reassess F-B18", quote.file, forbidden)
			}
		}
	}
	return builder.String()
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
//    because reconcile excludes PUBLISHED (:1043) and skips pointer-named rows
//    (:1068-1075), and the page read can only resolve pointer-named PUBLISHED
//    rows (current.go:117/121). FAILED-ON-PURPOSE only if a real drop hits the
//    page read's resolved-but-not-yet-queried execution with no fault.
// ---------------------------------------------------------------------------

func TestUnpinnedPageReadBaselineAgainstArango(t *testing.T) {
	quote := uprQuoteAndVerifySource(t)
	t.Logf("OBSERVED STEP 0 quoted verified source (anchors located live; drift fails the test):\n%s", quote)

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
	h.log.addf("STEP 1 E2: production.Publish created PUBLISHED exec %q (table %s, 3 real ClickHouse rows), pointer %q names it, key lease is FREE, reader.CurrentProjectDataset resolves it via the current.go:117/121 pointer check and Reader.Page returns 3 rows, all with 0 execution read pins — pinsAcquired=%d",
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
		t.Fatalf("attribution check: reconcile claimed the PUBLISHED E2 key (owner=%q) despite the :1043 exclusion — investigate why PUBLISHED was enumerated", e2Owner)
	}
	if e2PointerReads != 0 {
		t.Logf("NOTE: sweep read the E2 pointer %d time(s); reconcile excluded PUBLISHED via :1043 before GetPointer, so these reads (if any) came from a non-drop code path", e2PointerReads)
	}

	preventing := "bundle_store.go:1043 (Reconcile enumerates only RUNNING/VALIDATING/FAILED, EXCLUDING PUBLISHED) + bundle_store.go:1068-1075 (reconcileExecution re-reads the visibility pointer after claiming and releases the lease when it still names the execution, before any DropTable), with the pointer gate at current.go:117/121 confining a page read to pointer-named PUBLISHED rows"
	t.Logf("VERDICT PREVENTED (F-B18 baseline): an unpinned GraphQL page read (rows.go Rows -> published.Reader.Page, 0 execution read pins) resolved the current PUBLISHED pointer-named output via production current.go and queried it via production reader.Page; a CONCURRENT production reconcile sweep (with the page read resolved) dropped the never-pointer-named stale control %q but left BOTH the page-read target %q (table present=%v, page returned %d rows, no error) and the pointer-named stale control %q intact. Preventing lines: %s",
		dropExec.ID, e2ID, e2StillPresent, race.rows, keepExec.ID, preventing)
}

// ---------------------------------------------------------------------------
// 2) ABSENT-GUARD CONTROL ONLY (never feeds the verdict): reproduce the F-B12 /
//    release_dangle pointer-blindness shape at the ClickHouse boundary with an
//    unpinned page-read victim added. The drop must be reachable ONLY after BOTH
//    baseline protections are removed by fixture data (row flipped RUNNING via
//    raw AQL AND the pointer row replaced via production CompareAndSwapPointer),
//    which makes the :1073 guard's precondition false while the page read is mid
//    resolve->query. If the unpinned query then errors on the dropped table, that
//    records the amplifier as a fault control; it does NOT fail the test.
// ---------------------------------------------------------------------------

func TestUnpinnedPageReadAbsentGuardFaultControlAgainstArango(t *testing.T) {
	quote := uprQuoteAndVerifySource(t)
	t.Logf("OBSERVED STEP 0 quoted verified source (anchors located live; drift fails the test):\n%s", quote)

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
		// (F-B12 / release_dangle) so the :1073 guard's precondition is false.
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
	t.Logf("VERDICT PREVENTED-at-baseline, FAULT-CONTROL-ONLY amplifier note. Baseline protections that must all be removed by fixture data to reach victim %q's table: bundle_store.go:1043 (excludes PUBLISHED — removed by the RUNNING flip) AND bundle_store.go:1068-1075 pointer guard (blinded by replacing the pointer row via production CompareAndSwapPointer, i.e. the F-B12 release_dangle shape). With BOTH gone mid resolve->query, the UNPINNED reader.Page query on the resolved table %s then errors (dropped=%v, queryErr=%s). Absent any fault, the page read cannot be dropped (see the baseline test); this control records the reachable window WITHOUT modifying product code or removing a guard. It never feeds a CONFIRMED amplifier: the fault is guard-REMoval-equivalent, not a page-read-specific baseline defect.",
		victimID, table, !stillPresent, uprErrChain(queryErr))
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
// make the :1073 guard skip it.
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
