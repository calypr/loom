package clickhouse

// F-B19 durable regression control (Repair-4 shipped). A recipe edit mints a
// new bundle Key (the Key folds RecipeDigest/SchemaDigest/ReceiptID —
// internal/dataframe/publication/bundle.go:99-115) under the SAME PointerName
// (bundle.go:88-97). Pre-repair the superseded PUBLISHED execution was
// immortal (sweep visited only {RUNNING, VALIDATING, FAILED}), producing one
// permanently retained bundle+table per recipe edit and a monotonically
// widening name-latest mis-bind surface (F-B6 amplifier) — archived
// FAILED-ON-PURPOSE witnesses: parent runs e00a7032/e3712285, Round 15.
//
// Post-REPAIR natural-PASS (this artifact's shipped expectation): the
// binding-aware recurring janitor arm enumerates PUBLISHED (only while an
// ActiveReleaseReader is wired, the server.go production shape) and RETIRES a
// superseded orphan: FAILED with the "superseded execution reconciled"
// marker and its ClickHouse table dropped, while the pointer-named live
// execution and its table stay untouched. The REGRESSION direction keeps the
// archived CONFIRMED red text verbatim in structure: an immortal PUBLISHED
// orphan surviving a full sweep fails the test again.
//
// Safety sibling (TestPointerOrphanedPublishedJanitorRespectsActiveRelease-
// BindingsAgainstArango): a binding of the ACTIVE release to the orphan
// vetoes retirement (no claim, no drop); removing the binding and re-sweeping
// converges — the m-po-fixed shape on real stores. Formal companion:
// PointerOrphanLifecycle.tla m-po-bug arm (permanent-orphan temporal
// violation) + m-po-fixed arm (binding-aware janitor converges clean).

import (
	"context"
	"net/http"
	"net/url"
	"os"
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

const orphDatabasePrefix = "loom_verify_orph_"

type orphHarness struct {
	ctx      context.Context
	client   *storearango.Client
	registry *pubarango.Registry
	ch       *clickhouse.Client
	database string
	suffix   string

	mu            sync.Mutex
	trackedTables []string
}

func (h *orphHarness) trackTable(table string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.trackedTables = append(h.trackedTables, table)
}

func orphOpen(t *testing.T) *orphHarness {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	chURL := strings.TrimSpace(os.Getenv("LOOM_CLICKHOUSE_URL"))
	if endpoint == "" || chURL == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_CLICKHOUSE_URL to run the F-B19 orphan-growth reproduction")
	}
	database := orphDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	t.Cleanup(cancel)
	client, err := storearango.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatalf("open %s: %v", database, err)
	}
	t.Logf("ORPH_DATABASE=%s (via production store.Open)", database)
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		orphDropDatabase(t, endpoint, database)
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
	harness := &orphHarness{ctx: ctx, client: client, registry: registry, ch: ch, database: database, suffix: database[len(orphDatabasePrefix):]}
	t.Cleanup(func() { orphDropTables(t, harness) })
	return harness
}

func orphDropDatabase(t *testing.T, endpoint, database string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := storearango.Open(ctx, endpoint, "_system")
	if err != nil {
		t.Logf("ORPH_CLEANUP_WARN: open _system for drop: %v", err)
		return
	}
	defer func() { _ = client.Close(context.Background()) }()
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(database), nil)
	if err != nil {
		t.Logf("ORPH_CLEANUP_WARN: build drop request for %s: %v", database, err)
		return
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Logf("ORPH_CLEANUP_WARN: drop %s: %v", database, err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusForbidden {
		t.Logf("ORPH_CLEANUP_WARN: drop %s status %d", database, response.StatusCode)
		return
	}
	t.Logf("ORPH_DATABASE_REMOVED=%s", database)
}

func orphDropTables(t *testing.T, h *orphHarness) {
	t.Helper()
	h.mu.Lock()
	tables := append([]string(nil), h.trackedTables...)
	h.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, table := range tables {
		if err := h.ch.DropTable(ctx, table); err != nil {
			t.Logf("ORPH_CLEANUP_WARN: drop table %s: %v", table, err)
		}
	}
}

type orphPublication struct {
	identity pubpublication.PublicationIdentity
	outputs  []pubpublication.OutputStream
	rows     int
}

// orphNewPublication builds a publication whose PointerName is fixed by
// (project, generation, recipe, version, output) while recipeDigest/
// schemaDigest/receiptId vary per edit — the shipped two identities then map
// to the same pointer but different Keys exactly as a real recipe edit does
// (bundle.go:88-115).
func orphNewPublication(recipeDigest, schemaDigest, receiptID string) orphPublication {
	identity := pubpublication.PublicationIdentity{
		Name: "orph-recipe", TranslationVersion: "v1", OutputName: "orph_out",
		Project: "orph-project", DatasetGeneration: "orph-gen-" + uuid.NewString()[:8],
		RecipeDigest: recipeDigest, SchemaDigest: schemaDigest,
		ScopeDigest: "sha256:orph-scope", EngineVersion: "orph-engine",
		ReceiptID: receiptID,
	}
	outputs := []pubpublication.OutputStream{{
		Name: identity.OutputName,
		Columns: []pubpublication.LogicalColumn{{
			ID: "col-orph-value", Name: "value", Kind: "integer", Provenance: pubpublication.ColumnExplicit,
		}},
		Stream: func(ctx context.Context, visit func(map[string]any) error) error {
			for i := 1; i <= 3; i++ {
				if err := visit(map[string]any{"value": int64(i)}); err != nil {
					return err
				}
			}
			return nil
		},
	}}
	return orphPublication{identity: identity, outputs: outputs, rows: 3}
}

// orphBundleIdentity mirrors the production identity->bundle mapping used by
// the publication runner (same field set as publish_partial_pointer's
// factory) so Key/PointerName derive from shipped code.
func orphBundleIdentity(id pubpublication.PublicationIdentity) pubpublication.BundleIdentity {
	return pubpublication.BundleIdentity{
		Name: id.Name, TranslationVersion: id.TranslationVersion, OutputName: id.OutputName,
		Project: id.Project, DatasetGeneration: id.DatasetGeneration,
		RecipeDigest: id.RecipeDigest, SchemaDigest: id.SchemaDigest,
		ScopeDigest: id.ScopeDigest, EngineVersion: id.EngineVersion,
		ReceiptID: id.ReceiptID,
	}
}

func orphTablePresent(t *testing.T, h *orphHarness, ctx context.Context, table string) bool {
	t.Helper()
	rows, err := h.ch.QueryRowsArgs(ctx, "SELECT count() AS c FROM system.tables WHERE database = currentDatabase() AND name = ?", []string{"c"}, table)
	if err != nil {
		t.Fatalf("orph system.tables check for %s: %v", table, err)
	}
	if len(rows) != 1 {
		t.Fatalf("orph system.tables check for %s returned %d rows", table, len(rows))
	}
	switch v := rows[0]["c"].(type) {
	case uint64:
		return v == 1
	case int64:
		return v == 1
	case string:
		return v == "1"
	default:
		t.Fatalf("orph system.tables count for %s unreadable: %#v", table, rows[0]["c"])
		return false
	}
}

func orphReadFile(t *testing.T, repoRel string) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	const marker = "/internal/"
	i := strings.LastIndex(root, marker)
	if i < 0 {
		t.Fatalf("cannot locate repo root from %s", root)
	}
	data, err := os.ReadFile(root[:i] + "/" + repoRel)
	if err != nil {
		t.Fatalf("read %s: %v", repoRel, err)
	}
	return string(data)
}

func orphProductionPublish(ctx context.Context, store *ClickHouseBundleStore, p orphPublication) (pubpublication.Result, error) {
	return pubpublication.Publish(ctx, store, p.identity, p.outputs, pubpublication.Limits{})
}

func TestPointerOrphanedPublishedNeverReclaimedAgainstArango(t *testing.T) {
	// Drift gate: shipped sweep-state list must exclude PUBLISHED and the
	// name-latest selector must have no state/pointer filter — verified live
	// from the compiled-package sources, per the program's quote-first rule.
	sweepSrc := orphReadFile(t, "internal/dataframe/publication/clickhouse/bundle_store.go")
	if !strings.Contains(sweepSrc, "states := []publication.BundleState{publication.BundleRunning, publication.BundleValidating, publication.BundleFailed}") {
		t.Fatalf("drift gate: shipped sweep exclude PUBLISHED (bundle_store.go:1041) — F-B19 claim source changed, reassess before ruling")
	}
	registrySrc := orphReadFile(t, "internal/dataframe/publication/arango/bundle_registry.go")
	if !strings.Contains(registrySrc, "SORT doc.publishedAt DESC") {
		t.Fatalf("drift gate: name-latest selector shape changed; reassess F-B19 name-latest eligibility claim")
	}

	h := orphOpen(t)
	ctx := h.ctx

	store, err := NewBundleStore(h.ch, struct{ *pubarango.Registry }{h.registry})
	if err != nil {
		t.Fatalf("NewBundleStore: %v", err)
	}

	// EDIT 1 — publish recipe revision 1 through production publication.Publish.
	first := orphNewPublication("sha256:orph-recipe-v1", "sha256:orph-schema-v1", "orph-receipt-1")
	firstResult, err := orphProductionPublish(ctx, store, first)
	if err != nil {
		t.Fatalf("edit-1 publication failed: %v", err)
	}
	firstBundle := orphBundleIdentity(first.identity)
	firstKey := firstBundle.Key()
	firstPointer := firstBundle.PointerName()
	firstExec, err := h.registry.FindExecutionByKey(ctx, firstKey)
	if err != nil {
		t.Fatalf("edit-1 FindExecutionByKey(%s): %v", firstKey, err)
	}
	if firstExec.State.Canonical() != pubpublication.BundlePublished {
		t.Fatalf("edit-1 precondition: state %q, want PUBLISHED", firstExec.State)
	}
	firstTable := firstResult.Outputs[0].PhysicalName
	h.trackTable(firstTable)
	pointer, err := h.registry.GetPointer(ctx, firstPointer)
	if err != nil || pointer.ExecutionID != firstExec.ID {
		t.Fatalf("edit-1 precondition: pointer %q does not name edit-1 exec %q (pointer=%+v err=%v)", firstPointer, firstExec.ID, pointer, err)
	}
	t.Logf("ORPH_OBSERVED edit-1: key=%s state=%q pointer names it; orphan table %s present=%v", firstKey, firstExec.State, firstTable, orphTablePresent(t, h, ctx, firstTable))

	// EDIT 2 — the recipe edit: same PointerName, new digests => new Key.
	second := first
	second.identity.RecipeDigest = "sha256:orph-recipe-v2"
	second.identity.SchemaDigest = "sha256:orph-schema-v2"
	second.identity.ReceiptID = "orph-receipt-2"
	secondBundle := orphBundleIdentity(second.identity)
	secondKey := secondBundle.Key()
	if secondKey == firstKey {
		t.Fatalf("precondition: recipe edit must mint a new Key (first==second==%s)", firstKey)
	}
	if secondBundle.PointerName() != firstPointer {
		t.Fatalf("precondition: recipe edit must KEEP the PointerName (first=%q second=%q)", firstPointer, secondBundle.PointerName())
	}
	secondResult, err := orphProductionPublish(ctx, store, second)
	if err != nil {
		t.Fatalf("edit-2 publication failed: %v", err)
	}
	secondTable := secondResult.Outputs[0].PhysicalName
	h.trackTable(secondTable)
	secondExec, err := h.registry.FindExecutionByKey(ctx, secondKey)
	if err != nil {
		t.Fatalf("edit-2 FindExecutionByKey(%s): %v", secondKey, err)
	}
	pointer, err = h.registry.GetPointer(ctx, firstPointer)
	if err != nil || pointer.ExecutionID != secondExec.ID {
		t.Fatalf("edit-2 precondition: pointer does not name edit-2 exec %q (pointer=%+v err=%v)", secondExec.ID, pointer, err)
	}
	t.Logf("ORPH_OBSERVED edit-2 (recipe edit): new key=%s now pointer-named; edit-1 key %s superseded => orphaned", secondKey, firstKey)

	// Selector eligibility of the ORPHAN (mis-bind surface, pre-supersession
	// evidence re-checked post-supersession below): name-latest resolution
	// with no pointer/state filter.
	selector := pubpublication.DataframeSelector{Recipe: first.identity.Name, TranslationVersion: first.identity.TranslationVersion, Output: first.identity.OutputName}
	selExec, selOutput, err := h.registry.FindExecutionBySelector(ctx, first.identity.Project, first.identity.DatasetGeneration, selector)
	if err != nil {
		t.Fatalf("name-latest selector resolution: %v", err)
	}
	t.Logf("ORPH_OBSERVED name-latest selector resolves to key=%q state=%q physical=%q rows=%d (publishedAt DESC => edit-2 expected)", selExec.ID, selExec.State, selOutput.PhysicalTable, selOutput.RowCount)

	// PRODUCTION SWEEP — one full Reconcile with a far-future cutoff.
	if err := store.Reconcile(ctx, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("production Reconcile returned error: %v", err)
	}
	t.Log("ORPH_OBSERVED production Reconcile(olderThan=+24h) completed with no error")

	orphanExec, err := h.registry.FindExecutionByKey(ctx, firstKey)
	if err != nil {
		t.Fatalf("post-sweep FindExecutionByKey(orphan %s): %v", firstKey, err)
	}
	orphanTablePresent := orphTablePresent(t, h, ctx, firstTable)
	t.Logf("ORPH_OBSERVED post-sweep: orphan state=%q, orphan table %s present=%v, pointer still names edit-2=%v", orphanExec.State, firstTable, orphanTablePresent, pointer.ExecutionID)

	if orphanExec.State.Canonical() == pubpublication.BundlePublished && orphanTablePresent {
		t.Fatalf("FAILED-ON-PURPOSE F-B19 CONFIRMED: recipe edit #1 left key %q as PUBLISHED but no pointer names it; after one FULL production reconcile sweep it remains state=%q with ClickHouse table %q still present. The sweep cannot reach it (shipped states list excludes PUBLISHED — bundle_store.go:1041) and no janitor reclaims pointer-orphaned PUBLISHED rows or their tables => one permanently retained bundle+table per recipe edit (unbounded ClickHouse/Arango growth, widening name-latest mis-bind surface per FindExecutionBySelector publishedAt DESC at bundle_registry.go:174-182). Formal arms: PointerOrphanLifecycle m-po-bug violates, m-po-fixed (recurring binding-aware janitor) complete-clean.", firstKey, orphanExec.State, firstTable)
	}
	// If we get here the orphan WAS reclaimed — the growth bug is fixed; this
	// artifact must not pass silently.
	t.Fatalf("unexpected: orphan %q reclaimed (state=%q tablePresent=%v) — shipped sweep was supposed to be unable to reach PUBLISHED; investigate what changed before trusting this result", firstKey, orphanExec.State, orphanTablePresent)
}
