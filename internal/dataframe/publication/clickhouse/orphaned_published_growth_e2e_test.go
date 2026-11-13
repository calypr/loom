package clickhouse

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	arangofactory "github.com/calypr/loom/internal/backends/arango"
	pubpublication "github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
)

const orphDatabasePrefix = "loom_verify_orph_"

type orphHarness struct {
	ctx      context.Context
	client   *arangofactory.Client
	registry *arangofactory.PublishedRegistry
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
	arangoBackend, err := arangofactory.Open(ctx, arangofactory.Config{URL: endpoint, Database: database})
	if err != nil {
		t.Fatalf("open %s: %v", database, err)
	}
	client := arangoBackend.Client
	t.Logf("ORPH_DATABASE=%s (via production store.Open)", database)
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		orphDropDatabase(t, endpoint, database)
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
	harness := &orphHarness{ctx: ctx, client: client, registry: registry, ch: ch, database: database, suffix: database[len(orphDatabasePrefix):]}
	t.Cleanup(func() { orphDropTables(t, harness) })
	return harness
}

func orphDropDatabase(t *testing.T, endpoint, database string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	arangoBackend, err := arangofactory.Open(ctx, arangofactory.Config{URL: endpoint, Database: "_system"})
	if err != nil {
		t.Logf("ORPH_CLEANUP_WARN: open _system for drop: %v", err)
		return
	}
	client := arangoBackend.Client
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

type orphReleaseReader struct {
	active dataset.ActiveRelease
	err    error
	calls  int
}

func (r *orphReleaseReader) ReadActiveRelease(context.Context, string) (dataset.ActiveRelease, error) {
	r.calls++
	return r.active, r.err
}

func orphPublishRecipeEdit(t *testing.T, h *orphHarness, store *ClickHouseBundleStore) (firstExec, secondExec pubpublication.BundleExecution, firstTable, secondTable, firstKey, secondKey, firstPointer string) {
	t.Helper()
	first := orphNewPublication("sha256:orph-recipe-v1", "sha256:orph-schema-v1", "orph-receipt-1")
	firstResult, err := orphProductionPublish(h.ctx, store, first)
	if err != nil {
		t.Fatalf("edit-1 publication failed: %v", err)
	}
	firstBundle := orphBundleIdentity(first.identity)
	firstKey = firstBundle.Key()
	firstPointer = firstBundle.PointerName()
	firstExec, err = h.registry.FindExecutionByKey(h.ctx, firstKey)
	if err != nil {
		t.Fatalf("edit-1 FindExecutionByKey(%s): %v", firstKey, err)
	}
	if firstExec.State.Canonical() != pubpublication.BundlePublished {
		t.Fatalf("edit-1 precondition: state %q, want PUBLISHED", firstExec.State)
	}
	firstTable = firstResult.Outputs[0].PhysicalName
	h.trackTable(firstTable)
	pointer, err := h.registry.GetPointer(h.ctx, firstPointer)
	if err != nil || pointer.ExecutionID != firstExec.ID {
		t.Fatalf("edit-1 precondition: pointer %q does not name edit-1 exec %q (pointer=%+v err=%v)", firstPointer, firstExec.ID, pointer, err)
	}
	t.Logf("ORPH_OBSERVED edit-1: key=%s state=%q pointer names it; table %s present=%v", firstKey, firstExec.State, firstTable, orphTablePresent(t, h, h.ctx, firstTable))

	second := first
	second.identity.RecipeDigest = "sha256:orph-recipe-v2"
	second.identity.SchemaDigest = "sha256:orph-schema-v2"
	second.identity.ReceiptID = "orph-receipt-2"
	secondBundle := orphBundleIdentity(second.identity)
	secondKey = secondBundle.Key()
	if secondKey == firstKey {
		t.Fatalf("precondition: recipe edit must mint a new Key (first==second==%s)", firstKey)
	}
	if secondBundle.PointerName() != firstPointer {
		t.Fatalf("precondition: recipe edit must KEEP the PointerName (first=%q second=%q)", firstPointer, secondBundle.PointerName())
	}
	secondResult, err := orphProductionPublish(h.ctx, store, second)
	if err != nil {
		t.Fatalf("edit-2 publication failed: %v", err)
	}
	secondTable = secondResult.Outputs[0].PhysicalName
	h.trackTable(secondTable)
	secondExec, err = h.registry.FindExecutionByKey(h.ctx, secondKey)
	if err != nil {
		t.Fatalf("edit-2 FindExecutionByKey(%s): %v", secondKey, err)
	}
	pointer, err = h.registry.GetPointer(h.ctx, firstPointer)
	if err != nil || pointer.ExecutionID != secondExec.ID {
		t.Fatalf("edit-2 precondition: pointer does not name edit-2 exec %q (pointer=%+v err=%v)", secondExec.ID, pointer, err)
	}
	t.Logf("ORPH_OBSERVED edit-2 (recipe edit): new key=%s now pointer-named; edit-1 key %s superseded => orphaned", secondKey, firstKey)
	return
}

func TestPointerOrphanedPublishedNeverReclaimedAgainstArango(t *testing.T) {
	sweepSrc := orphReadFile(t, "internal/dataframe/publication/clickhouse/bundle_store.go")
	if !strings.Contains(sweepSrc, "states := []publication.BundleState{publication.BundleRunning, publication.BundleValidating, publication.BundleFailed}") {
		t.Fatalf("drift gate: stale-arm sweep state list changed — reassess the {RUNNING,VALIDATING,FAILED} baseline before ruling")
	}
	for _, needle := range []string{
		"states = append(states, publication.BundlePublished)",
		"superseded := execution.State.Canonical() == publication.BundlePublished",
		`"superseded execution reconciled (no pointer or active-release reference)"`,
	} {
		if !strings.Contains(sweepSrc, needle) {
			t.Fatalf("REGRESSION drift: Repair-4 janitor text %q no longer present in bundle_store.go — F-B19 growth returns; the REGRESSION arm below must fire against product behavior too", needle)
		}
	}
	registrySrc := orphReadFile(t, "internal/backends/arango/internal/publication/bundle_registry.go")
	if !strings.Contains(registrySrc, "SORT doc.publishedAt DESC") {
		t.Fatalf("drift gate: name-latest selector shape changed; reassess F-B19 name-latest eligibility claim")
	}

	h := orphOpen(t)
	ctx := h.ctx

	store, err := NewBundleStore(h.ch, struct {
		*arangofactory.PublishedRegistry
	}{h.registry})
	if err != nil {
		t.Fatalf("NewBundleStore: %v", err)
	}
	store.SetActiveReleaseReader(&orphReleaseReader{err: dataset.ErrNoActiveRelease})

	_, secondExec, firstTable, secondTable, firstKey, secondKey, _ := orphPublishRecipeEdit(t, h, store)

	selector := pubpublication.DataframeSelector{Recipe: "orph-recipe", TranslationVersion: "v1", Output: "orph_out"}

	if err := store.Reconcile(ctx, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("production Reconcile returned error: %v", err)
	}
	t.Log("ORPH_OBSERVED production Reconcile(olderThan=+24h) completed with no error")

	orphanExec, err := h.registry.FindExecutionByKey(ctx, firstKey)
	if err != nil {
		t.Fatalf("post-sweep FindExecutionByKey(orphan %s): %v", firstKey, err)
	}
	orphanTablePresent := orphTablePresent(t, h, ctx, firstTable)
	t.Logf("ORPH_OBSERVED post-sweep: orphan state=%q error=%q, orphan table %s present=%v", orphanExec.State, orphanExec.Error, firstTable, orphanTablePresent)

	if orphanExec.State.Canonical() == pubpublication.BundlePublished && orphanTablePresent {
		t.Fatalf("REGRESSION (F-B19 growth returns; archived CONFIRMED text): recipe edit #1 left key %q as PUBLISHED but no pointer names it; after one FULL production reconcile sweep it remains state=%q with ClickHouse table %q still present. The sweep cannot reach it and no janitor reclaims pointer-orphaned PUBLISHED rows or their tables => one permanently retained bundle+table per recipe edit (unbounded ClickHouse/Arango growth, widening name-latest mis-bind surface per FindExecutionBySelector publishedAt DESC at bundle_registry.go). Formal arms: PointerOrphanLifecycle m-po-bug violates, m-po-fixed complete-clean.", firstKey, orphanExec.State, firstTable)
	}
	if orphanExec.State.Canonical() != pubpublication.BundleFailed {
		t.Fatalf("unexpected: orphan %q post-sweep state=%q, want FAILED via the janitor arm", firstKey, orphanExec.State)
	}
	if orphanTablePresent {
		t.Fatalf("unexpected: orphan %q demoted but ClickHouse table %s SURVIVED — janitor must drop superseded outputs too", firstKey, firstTable)
	}
	if !strings.Contains(orphanExec.Error, "superseded execution reconciled") {
		t.Fatalf("unexpected: orphan marker %q lacks the superseded janitor marker", orphanExec.Error)
	}

	live, err := h.registry.FindExecutionByKey(ctx, secondKey)
	if err != nil || live.State.Canonical() != pubpublication.BundlePublished {
		t.Fatalf("janitor arm harmed the live execution %s: state=%+v err=%v", secondKey, live, err)
	}
	if !orphTablePresent(t, h, ctx, secondTable) {
		t.Fatalf("janitor arm dropped the LIVE table %s", secondTable)
	}
	selExec2, selOutput2, err := h.registry.FindExecutionBySelector(ctx, "orph-project", secondExec.BundleIdentity.DatasetGeneration, selector)
	if err != nil {
		t.Fatalf("post-sweep name-latest resolution: %v", err)
	}
	if selExec2.ID != secondExec.ID {
		t.Fatalf("name-latest serves %q, want the pointer-named edit-2 %q", selExec2.ID, secondExec.ID)
	}
	t.Logf("VERDICT PREVENTED (Repair-4): pointer-orphaned PUBLISHED execution %q retired by the binding-aware janitor arm (FAILED, marker %q, table dropped) while the pointer-named live execution %s (rows=%d) and its table survived; name-latest resolves to the live execution.", firstKey, orphanExec.Error, secondKey, selOutput2.RowCount)
}

func TestPointerOrphanedPublishedJanitorRespectsActiveReleaseBindingsAgainstArango(t *testing.T) {
	h := orphOpen(t)
	ctx := h.ctx
	store, err := NewBundleStore(h.ch, struct {
		*arangofactory.PublishedRegistry
	}{h.registry})
	if err != nil {
		t.Fatalf("NewBundleStore: %v", err)
	}
	reader := &orphReleaseReader{err: dataset.ErrNoActiveRelease}
	store.SetActiveReleaseReader(reader)

	firstExec, _, firstTable, _, firstKey, _, _ := orphPublishRecipeEdit(t, h, store)

	reader.active = dataset.ActiveRelease{
		Release: dataset.ProjectRelease{
			Project:      "orph-project",
			Publications: []dataset.ReleasePublication{{ExecutionID: firstExec.ID, Generation: firstExec.BundleIdentity.DatasetGeneration}},
		},
		Revision: 1,
	}
	reader.err = nil
	if err := store.Reconcile(ctx, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("sweep with binding: %v", err)
	}
	bound, err := h.registry.FindExecutionByKey(ctx, firstKey)
	if err != nil || bound.State.Canonical() != pubpublication.BundlePublished {
		t.Fatalf("REGRESSION (bound data loss): janitor touched the ACTIVE-release-bound orphan %s: state=%+v err=%v", firstKey, bound, err)
	}
	if !orphTablePresent(t, h, ctx, firstTable) {
		t.Fatalf("REGRESSION (bound data loss): janitor dropped the ACTIVE-release-bound orphan table %s", firstTable)
	}
	t.Logf("ORPH_OBSERVED bound sweep: orphan %s untouched (binding veto, pre-claim filter), reader calls=%d", firstKey, reader.calls)

	reader.active = dataset.ActiveRelease{}
	reader.err = dataset.ErrNoActiveRelease
	if err := store.Reconcile(ctx, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("sweep without binding: %v", err)
	}
	retired, err := h.registry.FindExecutionByKey(ctx, firstKey)
	if err != nil {
		t.Fatalf("post-unbind FindExecutionByKey: %v", err)
	}
	if retired.State.Canonical() != pubpublication.BundleFailed || orphTablePresent(t, h, ctx, firstTable) {
		t.Fatalf("non-convergence: after unbinding, orphan %s stayed state=%q tablePresent=%v — recurring janitor must converge", firstKey, retired.State, orphTablePresent(t, h, ctx, firstTable))
	}
	t.Logf("VERDICT PREVENTED (Repair-4 safety): binding veto held (no claim/drop), unbinding converged to FAILED+dropped on the next sweep — binding-aware recurring janitor on real stores.")
}
