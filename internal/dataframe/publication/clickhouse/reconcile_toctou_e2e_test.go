package clickhouse

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	arangofactory "github.com/calypr/loom/internal/backends/arango"
	pubpublication "github.com/calypr/loom/internal/dataframe/publication"
	"github.com/google/uuid"
)

// TestReconcileDropsJustPublishedBundleAgainstArango replays finding F-B1
// end-to-end against a real Arango instance: every guard decision — lease
// takeover (AcquireBundleLease :315), the publish filter set (:287-291), the
// cleanup claim (:414-422), and the fenced save (:44-47) — is made by
// production AQL on the real database. The ClickHouse side stays the
// deterministic effect recorder from reconcile_toctou_test.go: the bug is
// which deletions the reconciler is ALLOWED to reach, decided entirely
// Arango-side, and ClickHouse honors any context it is handed. The wrapper
// only relocates when the racing runner publishes; it decides nothing.
//
// Opt-in like execution_read_pin_integration_test.go; the test owns a UUID
// database and deletes only that database. Run against the owned stack from
// inside the pinned api container, whose network resolves the arangodb
// service:
//
//	docker exec -e LOOM_TEST_ARANGO_URL=http://arangodb:8529 \
//	  loom-dev-qwen-current-loom-api-1 sh -c \
//	  "cd /workspace && go test -count=1 -run TestReconcileDropsJustPublishedBundleAgainstArango -v ./internal/dataframe/publication/clickhouse/"

type fb1Catalog struct {
	*arangofactory.PublishedRegistry
	race    func()
	raceRan bool
}

// ClaimExecutionCleanup injects the racing publish AFTER reconcileExecution's
// GetPointer (:1068) and before the production claim AQL runs — the modelled
// freeze interleaving. Everything else delegates verbatim.
func (c *fb1Catalog) ClaimExecutionCleanup(ctx context.Context, executionID, owner string) (bool, error) {
	if !c.raceRan {
		c.raceRan = true
		if c.race != nil {
			c.race()
		}
	}
	return c.PublishedRegistry.ClaimExecutionCleanup(ctx, executionID, owner)
}

func fb1Setup(t *testing.T) (*arangofactory.PublishedRegistry, publicationSetup) {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	database := "loom_verify_fb1_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	arangoBackend, err := arangofactory.Open(ctx, arangofactory.Config{URL: endpoint, Database: database})
	if err != nil {
		t.Fatalf("open arango: %v", err)
	}
	client := arangoBackend.Client
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		request, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(database), nil)
		if err != nil {
			t.Errorf("build cleanup request: %v", err)
			return
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Errorf("drop database %s: %v", database, err)
			return
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusForbidden {
			t.Errorf("drop database %s: status %d", database, response.StatusCode)
		}
	})
	if err := client.Bootstrap(ctx, arangofactory.PublishedBootstrapSpec()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	registry, err := arangofactory.NewPublishedRegistry(client)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	return registry, publicationSetup{ctx: ctx, suffix: database[len("loom_verify_fb1_"):]}
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

// fb1Seed stores the stale execution row under a seed-held lease, then frees
// the key: the identical registry state a stalled reconciler faces when it
// arrives to a candidate whose previous holder's lease has lapsed.
func fb1Seed(t *testing.T, registry *arangofactory.PublishedRegistry, ctx context.Context, exec pubpublication.BundleExecution) {
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
	wrapper := &fb1Catalog{PublishedRegistry: registry}
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
	// Stages the DB state a >=2min stall inside reconcileExecution (guard read through claim) and
	// the lease-release boundary produces: the lease doc is already expired when the region arms.
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
		// Converted Round 31 (Repair-1b): with the post-claim live pointer
		// re-check shipped, the identical pre-fix race (expired lease +
		// publish landing after the pre-claim guard) now ends in a VETO —
		// reconcile reaches the candidate, claims cleanup, re-reads the
		// pointer, finds it names the execution, releases the lease, and
		// DropTable is never called. This PASS branch is the anti-regression
		// assertion: the table must survive. (The binding veto is proved by
		// the rrd/rdl harnesses, which wire the production reader; this one
		// exercises the pointer arm and keeps a nil reader.)
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

// TestReconcileSkipsPointerNamedExecutionAgainstArango is the real-DB control:
// when the pointer already names the execution before GetPointer, the pointer-skip
// skip must prevent deletion. Proves the wrapper injects no artificial
// permission: the same guard that protects the earlier publish is real.
func TestReconcileSkipsPointerNamedExecutionAgainstArango(t *testing.T) {
	registry, setup := fb1Setup(t)
	exec := fb1Execution(setup.suffix)
	fb1Seed(t, registry, setup.ctx, exec)
	if err := registry.CompareAndSwapPointer(setup.ctx, exec.PointerName(), "", exec.ID); err != nil {
		t.Fatalf("pre-publish pointer: %v", err)
	}
	ch := &tocFakeClickHouse{table: exec.Outputs[0].PhysicalTable}
	store, err := NewBundleStore(ch, &fb1Catalog{PublishedRegistry: registry})
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
