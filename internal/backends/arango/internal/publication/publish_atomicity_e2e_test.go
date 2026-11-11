package arango_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	pubarango "github.com/calypr/loom/internal/backends/arango/internal/publication"
	storearango "github.com/calypr/loom/internal/backends/arango/internal/store"
	"github.com/calypr/loom/internal/dataframe/publication"
)

// Repair evidence for F-B16 (Round 11 CONFIRMED, FINDINGS.md): the execution
// UPDATE and the visibility-pointer UPSERT of Registry.PublishExecution were
// two AQL legs whose intermediate state was externally observable, so a
// crash/failure between the legs left an execution PUBLISHED while its
// pointer was never written ("two-answers" class: the name-latest selector
// served the orphan while the bulk-load activation gate refused it). The
// repair commits both legs inside ONE Arango transaction.
//
// The promise under test is the production comment on PublishExecution
// ("atomically commits both the visibility pointer and the PUBLISHED
// metadata. A failed compare-and-swap leaves both documents intact."):
//
//	A. Legacy split control (pre-fix shape: the two legs as two separate
//	   requests, stopping after leg 1). The intermediate PUBLISHED-without-
//	   pointer state MUST be observable. This is the before-fail witness AND
//	   the sensitivity proof: the probe really sees a split when one exists.
//	B. Repaired path under mid-failure: the publish transaction is aborted
//	   by the harness AFTER the statement succeeded inside it (models any
//	   failure after Arango applied writes-to-date). Nothing may remain
//	   observable: state stays RUNNING, pointer stays absent; and a clean
//	   retry then lands both halves together. Pre-fix code observable the
//	   half state here -- proved by arm A executing those same statements.
//	C. Clean publish lands together; a stale compare-and-swap is refused
//	   with both documents intact.
//
// Opt-in (SKIP-clean without LOOM_TEST_ARANGO_URL):
//
//	docker exec loom-dev-qwen-current-loom-api-1 sh -c \
//	  'cd /workspace && LOOM_TEST_ARANGO_URL=http://arangodb:8529 \
//	   go test -count=1 -run TestPublishExecutionAtomicity -v ./internal/backends/arango/internal/publication/'

const patDatabasePrefix = "loom_verify_patom_"

// patAbortError is returned by the harness from INSIDE the publish
// transaction callback after the publish statement already succeeded there;
// go-driver aborts (rolls back) the transaction when the callback errors.
var patAbortError = errors.New("injected abort after successful publish statement")

func patOpen(t *testing.T) (*storearango.Client, *pubarango.Registry) {
	t.Helper()
	endpoint := os.Getenv("LOOM_TEST_ARANGO_URL")
	if endpoint == "" {
		t.Skip("set owned LOOM_TEST_ARANGO_URL")
	}
	name := patDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(name, patDatabasePrefix) {
		t.Fatalf("verification database %q escapes the %s prefix", name, patDatabasePrefix)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	client, err := storearango.Open(ctx, endpoint, name)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Logf("PAT_DATABASE=%s", name)
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		cleanupCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		req, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, strings.TrimRight(endpoint, "/")+"/_api/database/"+name, nil)
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
		t.Logf("PAT_DATABASE_REMOVED=%s", name)
	})
	if err := client.Bootstrap(ctx, pubarango.BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap %s: %v", name, err)
	}
	registry, err := pubarango.New(client)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	return client, registry
}

// patSeed creates a RUNNING execution with a live lease through production
// registry entry points only.
func patSeed(ctx context.Context, t *testing.T, registry *pubarango.Registry, execID, bundleKey, pointerName string) publication.BundleExecution {
	t.Helper()
	acquired, err := registry.AcquireBundleLease(ctx, bundleKey, "publisher-a", time.Now().UTC().Add(time.Hour))
	if err != nil || !acquired {
		t.Fatalf("acquire lease: acquired=%v err=%v", acquired, err)
	}
	execution := publication.BundleExecution{
		ID: execID, Key: bundleKey,
		BundleIdentity: publication.BundleIdentity{
			Name: pointerName, Project: "pat-project", DatasetGeneration: "pat-generation",
			TranslationVersion: "pat-tv", OutputName: "pat-out",
		},
		State:     publication.BundleRunning,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		OwnerID: "publisher-a",
	}
	if err := registry.SaveExecution(ctx, execution, "publisher-a"); err != nil {
		t.Fatalf("save execution: %v", err)
	}
	return execution
}

// patObserve reads exactly what outside readers read: GetExecution for the
// state, GetPointer for the bulk-load activation gate's surface.
func patObserve(ctx context.Context, t *testing.T, registry *pubarango.Registry, execID, pointerName string) (string, string) {
	t.Helper()
	execution, err := registry.GetExecution(ctx, execID)
	if err != nil {
		t.Fatalf("get execution %s: %v", execID, err)
	}
	pointerErr := "nil"
	if _, err := registry.GetPointer(ctx, pointerName); err != nil {
		pointerErr = err.Error()
	}
	return string(execution.State), pointerErr
}

func TestPublishExecutionAtomicity(t *testing.T) {
	ctx := context.Background()

	t.Run("A_legacy_split_is_observable", func(t *testing.T) {
		// Pre-fix product shape quoted from bundle_registry.go @ dc9016bc:
		// execution UPDATE then pointer UPSERT in one non-transactional AQL;
		// the crash between the legs is modelled by running leg 1 as its own
		// request and stopping.
		client, registry := patOpen(t)
		execution := patSeed(ctx, t, registry, "pat-exec-a", "pat-bundle-a", "pat-project\x00pat-generation\x00pat-recipe-a")
		doc := patDoc(t, execution, publication.BundlePublished)
		applied := false
		// Leg 1 verbatim from the pre-fix statement (@ dc9016bc) minus the
		// pointer leg; @name/@updatedAt stay parked in LET bindings exactly
		// as the sibling ppp harness does for the skipped leg.
		if err := client.QueryRows(ctx, `LET execution = DOCUMENT(@@executions, @executionKey)
LET lease = DOCUMENT(@@leases, @leaseKey)
LET pointerNameParam = @name
LET updatedAtParam = @updatedAt
FILTER execution != null
  AND lease != null
  AND lease.ownerId == @owner
  AND lease.expiresAt >= @now
UPDATE execution WITH @execution IN @@executions
RETURN {updated: true}`, 32, map[string]interface{}{
			"@executions": pubarango.BundleExecutionsCollection,
			"@leases":     pubarango.BundleLeasesCollection,
			"execution":   doc, "executionKey": execution.ID, "leaseKey": execution.Key,
			"name": execution.Name, "updatedAt": doc["updatedAt"], "owner": "publisher-a", "now": time.Now().UTC(),
		}, func(map[string]any) error { applied = true; return nil }); err != nil || !applied {
			t.Fatalf("legacy leg 1: applied=%v err=%v", applied, err)
		}
		state, pointerErr := patObserve(ctx, t, registry, execution.ID, execution.Name)
		t.Logf("OBS A (pre-fix witness, fault control): state=%q pointerErr=%q", state, pointerErr)
		t.Log("EXPECT A: PUBLISHED + pointer not found -- the probe sees the split when one exists (sensitivity)")
		if state != string(publication.BundlePublished) || !strings.Contains(pointerErr, "not found") {
			t.Fatalf("sensitivity failure: probe must observe the split state, got state=%q pointerErr=%q", state, pointerErr)
		}
	})

	t.Run("B_repaired_publish_rolls_back_on_mid_failure", func(t *testing.T) {
		client, registry := patOpen(t)
		execution := patSeed(ctx, t, registry, "pat-exec-b", "pat-bundle-b", "pat-project\x00pat-generation\x00pat-recipe-b")
		wrapper := &patTxAborter{Client: client}
		abortRegistry, err := pubarango.New(wrapper)
		if err != nil {
			t.Fatalf("new wrapped registry: %v", err)
		}
		published := execution
		published.State = publication.BundlePublished
		now := time.Now().UTC()
		published.PublishedAt = &now
		published.UpdatedAt = now
		if err := abortRegistry.PublishExecution(ctx, published.Name, "", published); !errors.Is(err, patAbortError) {
			t.Fatalf("expected the injected abort to surface, got %v", err)
		}
		state, pointerErr := patObserve(ctx, t, registry, execution.ID, execution.Name)
		t.Logf("OBS B (repaired, mid-publish abort): state=%q pointerErr=%q", state, pointerErr)
		t.Log("EXPECT B: RUNNING + pointer not found -- the transaction rolled the whole publish back")
		if state != string(publication.BundleRunning) || !strings.Contains(pointerErr, "not found") {
			t.Fatalf("REGRESSION (F-B16 two-answers class): mid-publish failure left state=%q pointerErr=%q", state, pointerErr)
		}
		if err := registry.PublishExecution(ctx, published.Name, "", published); err != nil {
			t.Fatalf("recovery publish after rollback: %v", err)
		}
		state, pointerErr = patObserve(ctx, t, registry, execution.ID, execution.Name)
		if state != string(publication.BundlePublished) || pointerErr != "nil" {
			t.Fatalf("recovery publish incomplete: state=%q pointerErr=%q", state, pointerErr)
		}
		t.Log("OBS B2: rollback was total and recoverable; the clean retry lands both halves together")
	})

	t.Run("C_clean_publish_and_refused_stale_cas", func(t *testing.T) {
		_, registry := patOpen(t)
		execution := patSeed(ctx, t, registry, "pat-exec-c", "pat-bundle-c", "pat-project\x00pat-generation\x00pat-recipe-c")
		published := execution
		published.State = publication.BundlePublished
		now := time.Now().UTC()
		published.PublishedAt = &now
		published.UpdatedAt = now
		if err := registry.PublishExecution(ctx, published.Name, "", published); err != nil {
			t.Fatalf("clean publish: %v", err)
		}
		state, pointerErr := patObserve(ctx, t, registry, execution.ID, execution.Name)
		if state != string(publication.BundlePublished) || pointerErr != "nil" {
			t.Fatalf("clean publish must land both halves: state=%q pointerErr=%q", state, pointerErr)
		}
		// Stale CAS: pointer already names pat-exec-c; a publish expecting
		// something else must be refused and change nothing.
		rival := execution
		rival.ID = "pat-exec-c2"
		rival.State = publication.BundleRunning
		rival.PublishedAt = nil
		if err := registry.SaveExecution(ctx, rival, "publisher-a"); err != nil {
			t.Fatalf("save rival: %v", err)
		}
		conflict := rival
		conflict.State = publication.BundlePublished
		conflict.PublishedAt = &now
		if err := registry.PublishExecution(ctx, published.Name, "pat-exec-rival", conflict); !errors.Is(err, publication.ErrBundlePointerConflict) {
			t.Fatalf("stale CAS error = %v, want ErrBundlePointerConflict", err)
		}
		pointer, err := registry.GetPointer(ctx, published.Name)
		if err != nil || pointer.ExecutionID != execution.ID {
			t.Fatalf("refused CAS disturbed the pointer: %q err=%v", pointer.ExecutionID, err)
		}
		state, pointerErr = patObserve(ctx, t, registry, execution.ID, execution.Name)
		if state != string(publication.BundlePublished) || pointerErr != "nil" {
			t.Fatalf("refused CAS disturbed the published execution: state=%q pointerErr=%q", state, pointerErr)
		}
		t.Log("OBS C: clean publish atomic; refused stale CAS leaves execution+pointer intact")
	})
}

func patDoc(t *testing.T, execution publication.BundleExecution, state publication.BundleState) map[string]any {
	t.Helper()
	execution.State = state
	raw, err := json.Marshal(execution)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["_key"] = execution.ID
	return doc
}

// patTxAborter delegates every call to the real client but aborts the first
// transaction AFTER its callback completed successfully, so the callback's
// writes (applied inside the transaction) are rolled back -- the exact class
// of mid-request failure that split the pre-fix publish.
type patTxAborter struct {
	*storearango.Client
	armed bool
}

func (a *patTxAborter) WithTransaction(ctx context.Context, collections storearango.TransactionCollections, fn storearango.TransactionFunc) error {
	if !a.armed {
		a.armed = true
		return a.Client.WithTransaction(ctx, collections, func(txCtx context.Context, tx storearango.RowQueryer) error {
			if err := fn(txCtx, tx); err != nil {
				return err
			}
			return patAbortError
		})
	}
	return a.Client.WithTransaction(ctx, collections, fn)
}
