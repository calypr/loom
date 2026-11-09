package clickhouse

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
	"strconv"
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

// Candidate finding F-B16 (registered Round 11, FINDINGS). REPAIRED in Repair-1
// (Round 26): PublishExecution now commits both legs inside ONE Arango
// transaction (bundle_registry.go WithTransaction over
// executions+pointers+leases), so the PRODUCT can no longer produce the
// two-answers split; durable post-fix proof lives in
// internal/dataframe/publication/arango/publish_atomicity_e2e_test.go (arms
// A/B: split observable only when the legs are separate requests; repaired
// publish rolls back wholly). Historical pre-fix FAILED-ON-PURPOSE evidence is
// preserved in FINDINGS.md Round 11 and the archived parent runs (artifact
// md5 4ef560d6 at dc9016bc). THIS ARTIFACT NOW SERVES AS A LABELED FAULT
// CONTROL for the same harm class applied to fabricated or legacy
// (pre-repair) rows: it fabricates the split by bypassing the repaired
// production PublishExecution (the wrapper runs the pre-repair legs itself),
// and asserts the consequences an operator would see for such a row —
// selector serves the orphan, the bulk-load gate refuses it, and the
// idempotent replay does not repair the pointer (documented residual property;
// the counterfactual sweep/replay fixes in PublishPartialPointer.tla remain
// unimplemented on purpose). If those products ever land, this artifact's
// VERDICT fails loudly so the fault-control semantics are re-derived, not
// silently weakened.
//
// Verified source facts (re-read and quoted at runtime by
// pppQuoteVerifiedSource, which fails this test if any anchor drifts):
//
//   - Registry.PublishExecution (internal/dataframe/publication/arango/
//     bundle_registry.go) contains the execution UPDATE
//     (`UPDATE execution WITH @execution IN @@executions`) and the
//     visibility pointer UPSERT in the SAME statement, now executed inside
//     one WithTransaction (Repair-1, Round 26); at dc9016bc the same
//     statement ran non-transactionally and a server-side abort between the
//     legs left the execution PUBLISHED while the pointer was never written
//     -- the fault this artifact injects by re-running those legs manually.
//   - The commit-fallback closes the tx uncertain on inconsistent metadata
//     (bundle_store.go:818-821 `t.closed = true // Prevent the runner from
//     deleting possibly published tables.`; confirmCommit :858
//     "publication metadata is inconsistent").
//   - An idempotent replay never repairs the pointer: beginBundle treats a
//     published/ready execution as idempotent (:137-140) and Commit returns
//     ExistingPublishedOutputs without touching the catalog (:762-764);
//     publication.Publish returns early on tx.Idempotent() (runner.go:45-46).
//   - Reconcile EXCLUDES PUBLISHED:
//     `states := []publication.BundleState{publication.BundleRunning,
//     publication.BundleValidating, publication.BundleFailed}`
//     (bundle_store.go:1043).
//   - The bulk-load activation gate refuses non-pointer executions
//     (internal/api/bulk/load/service.go:199-204 "is no longer active",
//     and the GetPointer error branch :200-202).
//   - The name-latest selector has no pointer filter
//     (bundle_registry.go:174-182 `SORT doc.publishedAt DESC ... LIMIT 1`
//     never reads the pointers collection).
//
// Experiment (real Arango + real ClickHouse, production entry points):
//
// CONTROL   publish exec CONTROL fully through production
//           publication.Publish (the entry internal/server/recipe_publication.go:143
//           uses) against the real ClickHouse client and real Registry.
// FAULT-INJECTION a catalog wrapper that passes every BundleCatalog call
//           through to the real Registry EXCEPT PublishExecution, which —
//           POST-R26 — can no longer split on its own, so the wrapper
//           fabricates the pre-fix split itself: it applies ONLY the exec
//           UPDATE leg (same LET/FILTER prelude and the verbatim
//           `UPDATE execution WITH @execution IN @@executions` via
//           client.QueryRows with the same binds) and then returns a
//           write-conflict-style error BEFORE the pointer UPSERT leg. This
//           models a fabricated or legacy (pre-repair) row, not product
//           behavior.
// REPLAY    the SAME production publish entry is called again with the same
//           identity; the documented residual property asserts an idempotent
//           success that leaves the pointer unwritten (counterfactual repair
//           unimplemented; if a product ever ships replay-then-heal, this
//           assertion fails so the fault control is re-derived, not weakened).
// PROBES    FindExecutionBySelector (name-latest) and a verbatim replication
//           of the bulk-load gate service.go:199-204 against real GetPointer.
//
// Opt-in (SKIP-clean without LOOM_TEST_ARANGO_URL + LOOM_CLICKHOUSE_URL):
//
//	docker exec loom-dev-qwen-current-loom-api-1 sh -c \
//	  'cd /workspace && LOOM_TEST_ARANGO_URL=http://arangodb:8529 \
//	   LOOM_CLICKHOUSE_URL=http://clickhouse:8123 LOOM_CLICKHOUSE_DATABASE=loom_dev \
//	   LOOM_CLICKHOUSE_USERNAME=loom_dev LOOM_CLICKHOUSE_PASSWORD=loom_dev_password \
//	   go test -count=1 -run TestPublishPartialPointer -v ./internal/dataframe/publication/clickhouse/'

const pppDatabasePrefix = "loom_verify_ppp_"

// pppChOwnedPrefix owns this suite's ClickHouse residue marker. The canary
// table exercises the same DropTable path the cleanup uses for the
// production-created loom_bundle_* staging tables (whose names are
// loom_bundle_<executionID>_<output>, bundle_store.go:908-910, so they are
// tracked by exact name rather than by prefix).
const pppChOwnedPrefix = "ch_ppp"

// ---------------------------------------------------------------------------
// catalog wrapper: passes every call through except the faulted publish
// ---------------------------------------------------------------------------

type pppPublishCall struct {
	PointerName    string
	Expected       string
	ExecutionID    string
	Delegated      bool // true = ran the real Registry.PublishExecution verbatim
	LegOneApplied  bool // exec UPDATE leg applied (faulted call only)
	LegTwoExecuted bool // pointer UPSERT leg ever executed on this call
	InjectedError  string
}

type pppCatalog struct {
	*pubarango.Registry
	client *storearango.Client

	mu         sync.Mutex
	faultArmed bool
	calls      []pppPublishCall
}

func newPPPCatalog(registry *pubarango.Registry, client *storearango.Client) *pppCatalog {
	return &pppCatalog{Registry: registry, client: client}
}

func (c *pppCatalog) armFault() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.faultArmed = true
}

func (c *pppCatalog) snapshotCalls() []pppPublishCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]pppPublishCall(nil), c.calls...)
}

// pppLegOneAQL is the PRE-FIX (dc9016bc) production PublishExecution AQL
// deconstructed into its two verified legs; this string keeps ONLY leg one:
// the LET/FILTER prelude (:284-291 @ dc9016bc) and the execution UPDATE
// (:292). The pointer UPSERT (:293-296) is deliberately absent on the
// faulted call -- fault-injection only: the repaired production publish runs
// both legs inside one WithTransaction and cannot be split this way without
// bypassing it. @name and @updatedAt are bound verbatim like production
// (:298-301) and merely parked in LET bindings because Arango rejects bind
// parameters no query text declares and only the skipped pointer-UPSERT leg
// references them.
const pppLegOneAQL = `LET pointer = DOCUMENT(@@pointers, @pointerKey)
LET execution = DOCUMENT(@@executions, @executionKey)
LET lease = DOCUMENT(@@leases, @leaseKey)
LET pointerNameParam = @name
LET updatedAtParam = @updatedAt
FILTER execution != null
  AND lease != null
  AND lease.ownerId == @owner
  AND lease.expiresAt >= @now
  AND (pointer == null OR pointer.executionId == @expected)
UPDATE execution WITH @execution IN @@executions
RETURN {updated: true}`

// pppPointerDocumentKey mirrors pubarango pointerDocumentKey (bundle_registry.go:
// 473-476): hex(sha256(pointerName)). Reproduced because the production helper
// is unexported and this harness must hand the faulted leg the same @pointerKey
// bind the real query would use.
func pppPointerDocumentKey(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

// PublishExecution delegates verbatim while disarmed. When armed it executes
// ONLY the exec UPDATE leg of the real query (same binds) and returns a
// write-conflict-style error BEFORE the pointer UPSERT leg, simulating the
// server-side partial application of the one non-transactional AQL.
func (c *pppCatalog) PublishExecution(ctx context.Context, name, expected string, execution pubpublication.BundleExecution) error {
	data, err := json.Marshal(execution)
	if err != nil {
		return err
	}
	var executionDoc map[string]any
	if err := json.Unmarshal(data, &executionDoc); err != nil {
		return err
	}
	c.mu.Lock()
	call := pppPublishCall{PointerName: name, Expected: expected, ExecutionID: execution.ID}
	armed := c.faultArmed
	c.faultArmed = false
	c.mu.Unlock()

	if !armed {
		call.Delegated = true
		err = c.Registry.PublishExecution(ctx, name, expected, execution)
		if err == nil {
			call.LegTwoExecuted = true // the real single-AQL publish covers both legs
		}
		c.mu.Lock()
		c.calls = append(c.calls, call)
		c.mu.Unlock()
		return err
	}

	// Leg ONE only: the exec UPDATE, same binds as bundle_registry.go:298-301.
	updated := false
	err = c.client.QueryRows(ctx, pppLegOneAQL, 32, map[string]interface{}{
		"@pointers":    pubarango.BundlePointersCollection,
		"@executions":  pubarango.BundleExecutionsCollection,
		"@leases":      pubarango.BundleLeasesCollection,
		"pointerKey":   pppPointerDocumentKey(name),
		"executionKey": execution.ID,
		"leaseKey":     execution.Key,
		"owner":        execution.OwnerID,
		"now":          time.Now().UTC(),
		"name":         name,
		"expected":     expected,
		"updatedAt":    execution.UpdatedAt,
		"execution":    executionDoc,
	}, func(map[string]any) error { updated = true; return nil })
	c.mu.Lock()
	call.LegOneApplied = updated
	if err != nil {
		call.InjectedError = err.Error()
	}
	c.calls = append(c.calls, call)
	c.mu.Unlock()
	if err != nil {
		return err
	}
	if !updated {
		return errors.New("ppp fault precondition: the exec UPDATE leg was filtered out; execution/lease/pointer guards did not hold")
	}
	// Leg TWO (pointer UPSERT, bundle_registry.go:293-296) is deliberately
	// NOT executed. Return a write-conflict-style error, which is NOT
	// publication.ErrBundlePointerConflict, so Commit takes the confirmCommit
	// fallback at bundle_store.go:813-822 rather than the conflict branch.
	injected := errors.New("write conflict: document revision conflict while applying visibility pointer UPSERT (injected partial-application fault)")
	c.mu.Lock()
	c.calls[len(c.calls)-1].InjectedError = injected.Error()
	c.mu.Unlock()
	return injected
}

// ---------------------------------------------------------------------------
// owned-database + owned-ClickHouse harness (SKIP-clean opt-in)
// ---------------------------------------------------------------------------

type pppHarness struct {
	ctx      context.Context
	client   *storearango.Client
	registry *pubarango.Registry
	catalog  *pppCatalog
	ch       *clickhouse.Client
	database string
	suffix   string

	mu            sync.Mutex
	trackedTables []string
	canaryTable   string
}

func (h *pppHarness) trackTable(table string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, existing := range h.trackedTables {
		if existing == table {
			return
		}
	}
	h.trackedTables = append(h.trackedTables, table)
}

func pppOpen(t *testing.T) *pppHarness {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	chURL := strings.TrimSpace(os.Getenv("LOOM_CLICKHOUSE_URL"))
	if endpoint == "" || chURL == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_CLICKHOUSE_URL to run the F-B16 partial-pointer reproduction")
	}
	database := pppDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(database, pppDatabasePrefix) {
		t.Fatalf("verification database %q escapes the %s prefix", database, pppDatabasePrefix)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	t.Cleanup(cancel)
	client, err := storearango.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatalf("open %s: %v", database, err)
	}
	t.Logf("PPP_DATABASE=%s", database)
	// Registered FIRST so it runs LAST: ClickHouse artifacts must be dropped
	// before the Arango database disappearance (LIFO cleanup order).
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		pppDropDatabase(t, endpoint, database)
	})
	registry, err := pubarango.New(client)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if err := client.Bootstrap(ctx, pubarango.BootstrapSpec()); err != nil {
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
	harness := &pppHarness{
		ctx:      ctx,
		client:   client,
		registry: registry,
		catalog:  newPPPCatalog(registry, client),
		ch:       ch,
		database: database,
		suffix:   database[len(pppDatabasePrefix):],
	}
	// Registered second: drops every tracked ClickHouse table (production
	// staging tables by exact name plus the owned-prefix canary) and asserts
	// residue emptiness before LIFO continues to the Arango database removal.
	t.Cleanup(func() {
		harness.dropAndVerifyClickHouse(t)
	})
	// Owned-prefix canary: proves the cleanup's DropTable path works and the
	// system.tables residue gate on the owned prefix is live.
	harness.canaryTable = pppChOwnedPrefix + "_canary_" + harness.suffix
	if err := ch.CreateTable(ctx, harness.canaryTable, []clickhouse.Column{{Name: "__loom_row_id", Type: "UInt64"}}); err != nil {
		t.Fatalf("create canary table %s: %v", harness.canaryTable, err)
	}
	harness.trackTable(harness.canaryTable)
	t.Logf("PPP_CLICKHOUSE_DATABASE=%s PPP_CANARY_TABLE=%s", os.Getenv("LOOM_CLICKHOUSE_DATABASE"), harness.canaryTable)
	return harness
}

func (h *pppHarness) dropAndVerifyClickHouse(t *testing.T) {
	t.Helper()
	h.mu.Lock()
	tables := append([]string(nil), h.trackedTables...)
	h.mu.Unlock()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, table := range tables {
		if err := h.ch.DropTable(cleanupCtx, table); err != nil {
			t.Errorf("PPP CH drop %s: %v", table, err)
		}
	}
	names, err := h.ch.QueryRowsArgs(cleanupCtx, fmt.Sprintf("SELECT name FROM system.tables WHERE database = currentDatabase() AND (name LIKE '%s%%' OR name LIKE 'loom_bundle_ppp%%')", pppChOwnedPrefix), []string{"name"})
	if err != nil {
		t.Errorf("PPP CH residue query (prefixes %s%%, loom_bundle_ppp%%): %v", pppChOwnedPrefix, err)
		return
	}
	if len(names) != 0 {
		t.Errorf("PPP CH residue under owned prefixes is not empty: %v", names)
		return
	}
	for _, table := range tables {
		rows, err := h.ch.QueryRowsArgs(cleanupCtx, "SELECT count() AS c FROM system.tables WHERE database = currentDatabase() AND name = ?", []string{"c"}, table)
		if err != nil {
			t.Errorf("PPP CH exact check %s: %v", table, err)
			continue
		}
		if len(rows) != 1 {
			t.Errorf("PPP CH exact check %s returned %d rows", table, len(rows))
			continue
		}
		if count, ok := pppCount(rows[0]["c"]); !ok {
			t.Errorf("PPP CH exact check %s unreadable: %#v", table, rows[0]["c"])
		} else if count != 0 {
			t.Errorf("PPP CH residue: tracked table %s still present", table)
		}
	}
	t.Logf("PPP_CH_CLEANUP_VERIFIED dropped=%v and prefixes '%s%%'/'loom_bundle_ppp%%' empty in system.tables", tables, pppChOwnedPrefix)
	_ = h.ch.Close()
}

func pppDropDatabase(t *testing.T, endpoint, database string) {
	t.Helper()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete,
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
	t.Logf("PPP_DATABASE_REMOVED=%s", database)
}

// ---------------------------------------------------------------------------
// fixtures
// ---------------------------------------------------------------------------

type pppPublication struct {
	identity  pubpublication.PublicationIdentity
	key       string
	pointer   string
	outputs   []pubpublication.OutputStream
	tableName string // set from the execution record once published
	// row values carried by every output.
	rows int
}

func pppNewPublication(label, project string, h *pppHarness) pppPublication {
	identity := pubpublication.PublicationIdentity{
		Name: "ppp-recipe-" + label, TranslationVersion: "v1", OutputName: "ppp_out_" + label,
		Project: project, DatasetGeneration: "ppp-gen-" + h.suffix + "-" + label,
		RecipeDigest:  "sha256:ppp-recipe-" + label + "-" + h.suffix,
		SchemaDigest:  "sha256:ppp-schema-" + label + "-" + h.suffix,
		ScopeDigest:   "sha256:ppp-scope-" + label + "-" + h.suffix,
		EngineVersion: "ppp-engine",
	}
	bundle := pubpublication.BundleIdentity{
		Name: identity.Name, TranslationVersion: identity.TranslationVersion, OutputName: identity.OutputName,
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration,
		RecipeDigest: identity.RecipeDigest, SchemaDigest: identity.SchemaDigest,
		ScopeDigest: identity.ScopeDigest, EngineVersion: identity.EngineVersion,
	}
	outputs := []pubpublication.OutputStream{{
		Name:    identity.OutputName,
		Columns: []pubpublication.LogicalColumn{{ID: "col-ppp-value", Name: "value", Kind: "integer", Provenance: pubpublication.ColumnExplicit}},
		Stream: func(ctx context.Context, visit func(map[string]any) error) error {
			for i := 1; i <= 3; i++ {
				if err := visit(map[string]any{"value": int64(i)}); err != nil {
					return err
				}
			}
			return nil
		},
	}}
	return pppPublication{identity: identity, key: bundle.Key(), pointer: bundle.PointerName(), outputs: outputs, rows: 3}
}

func (p pppPublication) selector() pubpublication.DataframeSelector {
	return pubpublication.DataframeSelector{Recipe: p.identity.Name, TranslationVersion: p.identity.TranslationVersion, Output: p.identity.OutputName}
}

// ---------------------------------------------------------------------------
// production-path publish entry (same call recipe_publication.go:143 uses)
// ---------------------------------------------------------------------------

func pppProductionPublish(ctx context.Context, store *ClickHouseBundleStore, p pppPublication) (pubpublication.Result, error) {
	return pubpublication.Publish(ctx, store, p.identity, p.outputs, pubpublication.Limits{})
}

// ---------------------------------------------------------------------------
// direct fact readers
// ---------------------------------------------------------------------------

func pppRawExecutionRow(t *testing.T, client *storearango.Client, ctx context.Context, execID string) (state, errField string) {
	t.Helper()
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
		t.Fatalf("ppp raw read of execution %s: %v", execID, err)
	}
	return state, errField
}

func pppCount(value any) (int64, bool) {
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
		parsed, err := strconv.ParseInt(typed, 10, 64)
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func pppTablePresent(t *testing.T, ch *clickhouse.Client, ctx context.Context, table string) bool {
	t.Helper()
	rows, err := ch.QueryRowsArgs(ctx, "SELECT count() AS c FROM system.tables WHERE database = currentDatabase() AND name = ?", []string{"c"}, table)
	if err != nil {
		t.Fatalf("ppp system.tables check for %s: %v", table, err)
	}
	if len(rows) != 1 {
		t.Fatalf("ppp system.tables check for %s returned %d rows", table, len(rows))
	}
	count, ok := pppCount(rows[0]["c"])
	if !ok {
		t.Fatalf("ppp system.tables count for %s unreadable: %#v", table, rows[0]["c"])
	}
	return count == 1
}

func pppTableRows(t *testing.T, ch *clickhouse.Client, ctx context.Context, table string) int64 {
	t.Helper()
	rows, err := ch.QueryRowsArgs(ctx, fmt.Sprintf("SELECT count() AS c FROM `%s`", table), []string{"c"})
	if err != nil {
		t.Fatalf("ppp row count for %s: %v", table, err)
	}
	if len(rows) != 1 {
		t.Fatalf("ppp row count for %s returned %d rows", table, len(rows))
	}
	count, ok := pppCount(rows[0]["c"])
	if !ok {
		t.Fatalf("ppp row count for %s unreadable: %#v", table, rows[0]["c"])
	}
	return count
}

// ---------------------------------------------------------------------------
// verbatim replication of the bulk-load activation gate
// (internal/api/bulk/load/service.go activateGeneration :187-205, the part
// that consults GetExecution, the publication predicates, and GetPointer).
// ---------------------------------------------------------------------------

type pppReleaseStore interface {
	GetExecution(context.Context, string) (pubpublication.BundleExecution, error)
	GetPointer(context.Context, string) (pubpublication.BundlePointer, error)
}

func pppBulkGateCheck(ctx context.Context, store pppReleaseStore, project, generation, executionID string) (string, error) {
	execution, err := store.GetExecution(ctx, executionID) // service.go:187
	if err != nil {
		return "service.go:188-190", fmt.Errorf("load dataframe release %q: %w", executionID, err)
	}
	if execution.Project != project || execution.DatasetGeneration != generation || !execution.State.Successful() || len(execution.Outputs) == 0 { // service.go:191
		return "service.go:191-193", fmt.Errorf("dataframe release %q is not a published bundle for %s/%s", executionID, project, generation)
	}
	for _, output := range execution.Outputs { // service.go:194-197
		if !output.State.Successful() {
			return "service.go:195-197", fmt.Errorf("dataframe release %q output %q is not published", executionID, output.Name)
		}
	}
	pointer, err := store.GetPointer(ctx, execution.PointerName()) // service.go:199
	if err != nil {                                                // service.go:200-202
		return "service.go:200-202", fmt.Errorf("resolve dataframe release pointer: %w", err)
	}
	if pointer.ExecutionID != execution.ID { // service.go:203-204
		return "service.go:203-204", fmt.Errorf("dataframe release %q is no longer active", executionID)
	}
	return "service.go:205 (gate passed)", nil
}

// ---------------------------------------------------------------------------
// drift-proof source citations: the claim dies if production moves the lines
// ---------------------------------------------------------------------------

var pppRequiredQuotations = []struct {
	file    string
	needles []string
}{
	{
		file: "internal/dataframe/publication/arango/bundle_registry.go",
		needles: []string{
			"UPDATE execution WITH @execution IN @@executions",
			"UPSERT {_key: @pointerKey}",
			"IN @@pointers",
			"SORT doc.publishedAt DESC, doc.readyAt DESC, doc.updatedAt DESC LIMIT 1 RETURN doc",
		},
	},
	{
		file: "internal/dataframe/publication/clickhouse/bundle_store.go",
		needles: []string{
			"t.closed = true // Prevent the runner from deleting possibly published tables.",
			"publication metadata is inconsistent",
			"states := []publication.BundleState{publication.BundleRunning, publication.BundleValidating, publication.BundleFailed}",
			"case publication.BundlePublished, publication.BundleReady:",
			"if t.idempotent {",
			"return t.ExistingPublishedOutputs(), nil",
		},
	},
	{
		file: "internal/api/bulk/load/service.go",
		needles: []string{
			"resolve dataframe release pointer: %w",
			"is no longer active",
			"pointer, err := s.dataframeReleases.GetPointer(ctx, execution.PointerName())",
		},
	},
}

func pppRepoRoot(t *testing.T) string {
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

func pppQuoteVerifiedSource(t *testing.T) string {
	t.Helper()
	root := pppRepoRoot(t)
	var builder strings.Builder
	for _, entry := range pppRequiredQuotations {
		raw, err := os.ReadFile(filepath.Join(root, entry.file))
		if err != nil {
			t.Fatalf("claim source %s unreadable: %v", entry.file, err)
		}
		text := string(raw)
		lines := strings.Split(text, "\n")
		builder.WriteString(fmt.Sprintf("--- %s ---\n", entry.file))
		for _, needle := range entry.needles {
			found := false
			for i, line := range lines {
				if strings.Contains(line, needle) {
					builder.WriteString(fmt.Sprintf("  %d: %s\n", i+1, strings.TrimSpace(line)))
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("claim source drift: %s no longer contains %q — re-verify F-B16 against current production before trusting any verdict", entry.file, needle)
			}
		}
	}
	return builder.String()
}

// ---------------------------------------------------------------------------
// the reproduction
// ---------------------------------------------------------------------------

func TestPublishPartialPointerNeverAdvertisedAgainstArango(t *testing.T) {
	quotations := pppQuoteVerifiedSource(t)
	t.Logf("OBSERVED STEP 0 quoted verified source (anchor lines located live; drift fails the test):\n%s", quotations)
	h := pppOpen(t)
	ctx := h.ctx

	store, err := NewBundleStore(h.ch, h.catalog)
	if err != nil {
		t.Fatalf("NewBundleStore: %v", err)
	}

	// =========================================================================
	// STEP 1 — CONTROL: production publication.Publish publishes exec CONTROL
	// fully through the real ClickHouse client and real Registry.
	// =========================================================================
	t.Logf("PRE-STATED EXPECT (control): publication.Publish leaves the pointer naming exec CONTROL, state PUBLISHED, and the physical ClickHouse table present")
	control := pppNewPublication("control", "ppp-control-project", h)
	controlResult, controlErr := pppProductionPublish(ctx, store, control)
	if controlErr != nil {
		t.Fatalf("control publication through production entry failed: %v", controlErr)
	}
	if len(controlResult.Outputs) != 1 || controlResult.Outputs[0].RowCount != int64(control.rows) {
		t.Fatalf("control publication result malformed: %+v", controlResult.Outputs)
	}
	calls := h.catalog.snapshotCalls()
	if len(calls) != 1 || !calls[0].Delegated || calls[0].ExecutionID == "" {
		t.Fatalf("control precondition: expected exactly one delegated real PublishExecution, got %+v", calls)
	}
	controlID := calls[0].ExecutionID
	controlTable := controlResult.Outputs[0].PhysicalName
	h.trackTable(controlTable)
	controlExec, err := h.registry.GetExecution(ctx, controlID)
	if err != nil {
		t.Fatalf("control GetExecution %s: %v", controlID, err)
	}
	if controlExec.State.Canonical() != pubpublication.BundlePublished {
		t.Fatalf("control precondition: exec CONTROL state %q, want PUBLISHED", controlExec.State)
	}
	controlPointer, err := h.registry.GetPointer(ctx, control.pointer)
	if err != nil || controlPointer.ExecutionID != controlID {
		t.Fatalf("control precondition: pointer %q does not name exec CONTROL %s (pointer=%+v err=%v)", control.pointer, controlID, controlPointer, err)
	}
	if !pppTablePresent(t, h.ch, ctx, controlTable) {
		t.Fatalf("control precondition: physical table %s missing from ClickHouse system.tables", controlTable)
	}
	t.Logf("OBSERVED STEP 1 CONTROL: production publication.Publish published exec %q state=%q pointer->%q physical table %q present with %d rows via real ClickHouse", controlID, controlExec.State, controlPointer.ExecutionID, controlTable, pppTableRows(t, h.ch, ctx, controlTable))

	// =========================================================================
	// STEP 2 — FAULT: the wrapper applies ONLY the exec UPDATE leg and fails
	// before the pointer UPSERT leg; a real Commit runs through it.
	// =========================================================================
	t.Logf("PRE-STATED EXPECT (fault-injection control): Commit returns ErrBundleCommitUncertain (errors.Is) and the tx is closed; the execution remains queryable PUBLISHED (FindExecutionByKey/GetExecution) while GetPointer -> ErrBundleNotFound")
	faulty := pppNewPublication("fault", "ppp-fault-project", h)
	h.catalog.armFault()
	faultResult, faultErr := pppProductionPublish(ctx, store, faulty)
	faultID := ""
	faultTable := ""
	calls = h.catalog.snapshotCalls()
	if len(calls) == 2 && calls[1].ExecutionID != "" {
		faultID = calls[1].ExecutionID
	}
	if faultID != "" {
		if exec, execErr := h.registry.GetExecution(ctx, faultID); execErr == nil && len(exec.Outputs) == 1 {
			faultTable = exec.Outputs[0].PhysicalTable
			h.trackTable(faultTable)
		}
	}
	if !errors.Is(faultErr, ErrBundleCommitUncertain) {
		t.Fatalf("fault scenario did not end in ErrBundleCommitUncertain: err=%v result=%+v calls=%+v", faultErr, faultResult, calls)
	}
	if len(calls) != 2 {
		t.Fatalf("fault precondition: expected the armed PublishExecution to fire once, calls=%+v", calls)
	}
	if !calls[1].LegOneApplied || calls[1].LegTwoExecuted {
		t.Fatalf("fault precondition: expected ONLY the exec UPDATE leg applied and the pointer UPSERT leg skipped, got %+v", calls[1])
	}
	if faultID == "" {
		t.Fatalf("fault precondition: execution id never observed")
	}
	viaGet := mustGetExecution(t, h, ctx, faultID, "GetExecution")
	viaKey := mustGetExecution(t, h, ctx, faulty.key, "FindExecutionByKey")
	if viaGet.State.Canonical() != pubpublication.BundlePublished || viaKey.State.Canonical() != pubpublication.BundlePublished {
		t.Fatalf("fault precondition: partial Application left the row state GetExecution=%q FindExecutionByKey=%q, want PUBLISHED on both", viaGet.State, viaKey.State)
	}
	faultPointer, pointerErr := h.registry.GetPointer(ctx, faulty.pointer)
	if !errors.Is(pointerErr, pubpublication.ErrBundleNotFound) {
		t.Fatalf("fault precondition: pointer under a partial application must be missing; GetPointer returned pointer=%+v err=%v", faultPointer, pointerErr)
	}
	// Behavioral fingerprint of t.closed=true at bundle_store.go:819: the
	// runner's Abort path (publication.Publish fail -> tx.Abort) refused to
	// delete the staging table and never demoted the row.
	if !pppTablePresent(t, h.ch, ctx, faultTable) {
		t.Fatalf("fault precondition: the possibly-published table %q was deleted despite the uncertain-commit closed flag (bundle_store.go:819 comment)", faultTable)
	}
	rawState, rawError := pppRawExecutionRow(t, h.client, ctx, faultID)
	if strings.TrimSpace(rawError) != "" {
		t.Fatalf("fault precondition: the runner demoted the execution (raw error field %q); the closed-fault guard did not hold", rawError)
	}
	t.Logf("OBSERVED STEP 2 FAULT: Commit path returned errors.Is(err, ErrBundleCommitUncertain)=true errChain=[%s]; exec UPDATE leg applied=%v pointer UPSERT leg executed=%v; exec %q queryable PUBLISHED via GetExecution and FindExecutionByKey (raw state=%q); GetPointer(%q) -> ErrBundleNotFound; possibly-published table %q preserved (%d rows) -> t.closed=true at bundle_store.go:819 held",
		errChain(faultErr), calls[1].LegOneApplied, calls[1].LegTwoExecuted, faultID, rawState, faulty.pointer, faultTable, pppTableRows(t, h.ch, ctx, faultTable))

	// =========================================================================
	// STEP 3 — REPAIR ATTEMPT: same production publish entry, same identity.
	// =========================================================================
	t.Logf("PRE-STATED EXPECT (fault-injection residual property): the identical retry succeeds idempotently (Result from ExistingPublishedOutputs) yet the pointer STILL does not name the fabricated split row; the counterfactual replay-heal (PublishPartialPointer.tla RepairOnIdempotent) is unimplemented, so non-repair is the CURRENT correct behavior for fabricated/legacy rows; the repair-then-verify step lives in publish_atomicity_e2e_test.go")
	repairResult, repairErr := pppProductionPublish(ctx, store, faulty)
	if repairErr != nil {
		t.Fatalf("repair attempt: production retry of the identical identity errored instead of succeeding idempotently: %v", repairErr)
	}
	if len(repairResult.Outputs) != 1 || repairResult.Outputs[0].PhysicalName != faultTable || repairResult.Outputs[0].RowCount != int64(faulty.rows) {
		t.Fatalf("repair attempt: idempotent Result did not come from ExistingPublishedOutputs: %+v", repairResult.Outputs)
	}
	calls = h.catalog.snapshotCalls()
	if len(calls) != 2 {
		t.Fatalf("repair attempt: the idempotent retry unexpectedly invoked PublishExecution: %+v", calls)
	}
	pointerAfterRetry, pointerAfterErr := h.registry.GetPointer(ctx, faulty.pointer)
	repaired := pointerAfterErr == nil && pointerAfterRetry.ExecutionID == faultID
	t.Logf("OBSERVED STEP 3 REPAIR: identical retry published.Result={Outputs:%+v} err=%v; PublishExecution invocations still %d (idempotent path never publishes); GetPointer after retry -> pointer=%+v err=%v; repaired=%v",
		repairResult.Outputs, repairErr, len(calls), pointerAfterRetry, pointerAfterErr, repaired)

	// =========================================================================
	// STEP 4 — CONSEQUENCE probes on the orphan (real production functions).
	// =========================================================================
	selectorExec, selectorOutput, selectorErr := h.registry.FindExecutionBySelector(ctx, faulty.identity.Project, faulty.identity.DatasetGeneration, faulty.selector())
	t.Logf("OBSERVED STEP 4a name-latest selector (bundle_registry.go:174-182 has no pointer filter): FindExecutionBySelector=%q state=%q output=%q physicalTable=%q err=%v (matches orphan %v) while GetPointer for the same namespace errors=%v (repaired=%v)",
		selectorExec.ID, selectorExec.State, selectorOutput.Name, selectorOutput.PhysicalTable, selectorErr, selectorExec.ID == faultID, pointerAfterErr, repaired)
	if selectorErr != nil || selectorExec.ID != faultID {
		t.Fatalf("STEP 4a broken: name-latest selector must return the orphan %q: got %q err=%v", faultID, selectorExec.ID, selectorErr)
	}
	orphanBranch, orphanGateErr := pppBulkGateCheck(ctx, h.catalog, faulty.identity.Project, faulty.identity.DatasetGeneration, faultID)
	t.Logf("OBSERVED STEP 4b bulk-load gate on the orphan (activation refused per service.go simulation): branch taken=%q error=[%v]", orphanBranch, orphanGateErr)
	controlBranch, controlGateErr := pppBulkGateCheck(ctx, h.catalog, control.identity.Project, control.identity.DatasetGeneration, controlID)
	t.Logf("OBSERVED STEP 4c bulk-load gate on the control (liveness counterpart): branch taken=%q error=%v", controlBranch, controlGateErr)
	if controlBranch != "service.go:205 (gate passed)" || controlGateErr != nil {
		t.Fatalf("STEP 4c broken: the gate must pass a fully published, pointer-named execution; got branch=%q err=%v", controlBranch, controlGateErr)
	}
	if orphanGateErr == nil {
		t.Fatalf("STEP 4b broken: the bulk-load gate accepted an execution the pointer never named; branch=%q", orphanBranch)
	}

	// =========================================================================
	// VERDICT
	// =========================================================================
	if repaired {
		t.Fatalf("VERDICT CHANGE REQUIRED: the identical production replay repaired the pointer for a fabricated split row — the counterfactual replay-heal (PublishPartialPointer.tla RepairOnIdempotent/SweepPublished) has shipped; re-derive this fault control against the new product promise instead of deleting it")
	}
	if calls[1].Delegated {
		t.Fatalf("VERDICT INVALID: the split was produced by the REAL Registry.PublishExecution, not the harness bypass — the r26 atomic-publish repair has regressed; investigate via publish_atomicity_e2e_test.go arms A/B first")
	}
	t.Logf("VERDICT (r26 repaired; fault-control verified): the PUBLISHED-never-advertised state for %q exists ONLY because the harness bypassed the repaired production PublishExecution (wrapper ran the pre-fix legs itself; Delegated=false). For such fabricated/legacy rows the harm is real and persists exactly as originally documented: name-latest selector serves the orphan (%v), the bulk-load gate refuses it, the idempotent replay does not heal the pointer, and reconcile never visits PUBLISHED (:1043). The product-level guarantee (production publish cannot produce this state) is asserted by STEP 1's delegated real publish here and proved with rollback in publish_atomicity_e2e_test.go; the pre-fix FAILED-ON-PURPOSE history is preserved in FINDINGS.md Round 11.", faultID, selectorExec.ID == faultID)
	quoting := strings.Builder{}
	quoting.WriteString("repairing lines (bundle_registry.go:292-296):\nUPDATE execution WITH @execution IN @@executions\nUPSERT {_key: @pointerKey}\nINSERT {_key: @pointerKey, name: @name, executionId: @executionKey, updatedAt: @updatedAt}\nUPDATE {executionId: @executionKey, updatedAt: @updatedAt}\nIN @@pointers\n")
	quoting.WriteString(fmt.Sprintf("and the retry's PublishExecution call log: %+v", h.catalog.snapshotCalls()))
	t.Logf("FAULT-CONTROL QUOTE RECORD: split legs + replay call log (pointer %q unwritten by replay; executionId=%q):\n%s", faulty.pointer, pointerAfterRetry.ExecutionID, quoting.String())
}

// helpers --------------------------------------------------------------------------------

func mustGetExecution(t *testing.T, h *pppHarness, ctx context.Context, idOrKey, via string) pubpublication.BundleExecution {
	t.Helper()
	var (
		execution pubpublication.BundleExecution
		err       error
	)
	if via == "GetExecution" {
		execution, err = h.registry.GetExecution(ctx, idOrKey)
	} else {
		execution, err = h.registry.FindExecutionByKey(ctx, idOrKey)
	}
	if err != nil {
		t.Fatalf("%s of %s: %v", via, idOrKey, err)
	}
	return execution
}

func errChain(err error) string {
	if err == nil {
		return "<nil>"
	}
	parts := make([]string, 0, 6)
	for current := err; current != nil; current = errors.Unwrap(current) {
		parts = append(parts, current.Error())
	}
	return strings.Join(parts, " <- ")
}
