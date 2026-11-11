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
	"strings"
	"sync"
	"testing"
	"time"

	arangofactory "github.com/calypr/loom/internal/backends/arango"
	pubpublication "github.com/calypr/loom/internal/dataframe/publication"
	pubpublished "github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
)

// rdlDatabasePrefix owns this suite's databases independently of the sibling
// verification workers (ecw_/db_/nlb_/fb1_): residue is greppable as loom_verify_rdl_*.
const rdlDatabasePrefix = "loom_verify_rdl_"

const (
	// rdlSeedAge is how far in the past every seeded execution's updatedAt is
	// parked; Reconcile's enumeration filter is `doc.updatedAt < olderThan`
	// (bundle_registry.go:107-112), so anything younger is invisible to it.
	rdlSeedAge = 90 * time.Minute
	// rdlReconcileOlderThan is the `olderThan` passed to Reconcile. Seed docs
	// are rdlSeedAge(90m) old, strictly older than this 60m bound.
	rdlReconcileOlderThan = 60 * time.Minute
	rdlReconcileMarker    = "stale execution reconciled" // bundle_store.go:1091
)

// ---------------------------------------------------------------------------
// Candidates this file adjudicates on real Arango + real Go services:
//
//   - F-B9: the dataset-release Create/Activate path never takes a reader pin
//     on the execution it binds, so the ONLY thing that can shield an
//     active-release-bound execution from startup Reconcile is the pointer
//     visibility guard at bundle_store.go:1068-1075. (Pre-run audit, reported
//     verbatim in the task's STEP 0a: AcquireExecutionReadPin has exactly two
//     non-test call sites, read_pins.go:63 and export.go:135, ZERO in
//     internal/dataset/**, and there is no internal/backends/arango/internal/explorer/read_pins.go.)
//   - F-B1: ClickHouseBundleStore.Reconcile enumerates RUNNING/VALIDATING/FAILED
//     executions older than the cutoff (bundle_store.go:1046-1053) and, once it
//     claims the key, drops their physical tables and demotes the row to FAILED
//     with marker "stale execution reconciled" (:1080-1093).
//   - F-B7: a single unresolvable binding force-fails the whole
//     published.Reader.CurrentProjectDatasets response (current.go:206-208,
//     `return nil, err`), hiding the independently-valid siblings — the
//     all-or-nothing discovery-list behaviour the sibling
//     dangling_binding_e2e_test.go already source-verifies.
//
// The composite HYPOTHESIS (F-B9 x F-B1 x F-B7): because the release path pins
// nothing (F-B9), a startup Reconcile that races a publish with a stale
// RUNNING enumeration snapshot treats an ACTIVE-release-bound execution as a
// FAILED-candidate (F-B1) and drops/demotes it, and one dangling binding then
// force-fails the current dataset list wholesale (F-B7).
//
// THE LINE THAT BREAKS THE CHAIN: reconcileExecution re-reads the visibility
// pointer AFTER claiming the key, at bundle_store.go:1068, and:
//
//	pointer, pointerErr := s.catalog.GetPointer(cleanupCtx, execution.PointerName())
//	...
//	if pointerErr == nil && pointer.ExecutionID == execution.ID {
//		return s.catalog.ReleaseBundleLease(cleanupCtx, execution.Key, reconcilerID)
//	}
//
// If the pointer names the execution, cleanup is aborted (lease released, no
// DropTable, no SaveExecution demotion). The current-release READ path
// (releaseMaterialization, current.go:94-111) never consults that pointer, so
// the guard is the SOLE defense — and F-B9 tells us a pin is not there.
//
// This test therefore runs ONE controlled experiment with the pointer as the
// single manipulated variable, against real Arango AQL for every guard
// decision and deterministic recorders for the ClickHouse effect:
//
//   - PRIMARY (the task's prescribed scenario): the active-release-bound
//     execution is created via the real publish flow so its pointer names it
//     and its outputs are queryable, then bound to an ACTIVE release via the
//     production dataset.ReleaseService Create+Activate over the real
//     arangofactory.DatasetStore. Inject the stale RUNNING enumeration snapshot, free
//     the lease, and run the production Reconcile. Predicted: the pointer guard
//     at :1073 skips cleanup -> NOT dangled -> current list stays healthy =>
//     PREVENTED. (Verdict gate; FAILED-ON-PURPOSE only if it actually dangles.)
//
//   - LIVE-MACHINERY CONTROL: a second RUNNING, lease-free, PIN-FREE execution
//     that is neither pointer-named nor release-bound sits in the same Reconcile
//     sweep. It MUST be reclaimed (table dropped, row FAILED + marker), proving
//     enumeration + claim + DropTable + fenced save are genuinely live. Without
//     it, the PRIMARY's survival could be dismissed as a no-op reconciler.
//
//   - ABSENT-GUARD CONTROL: a THIRD execution is bound to an ACTIVE release
//     exactly like the PRIMARY but its pointer was repointed to a shadow
//     execution (the F-B6 "release binds an execution the pointer no longer
//     names" shape, which F-B7's pointer-ignoring read happily serves). In the
//     same sweep the guard's precondition is FALSE, so reconcileDrops it: table
//     dropped, row FAILED, and CurrentProjectDatasets for that project fails
//     wholesale. This is the F-B9xF-B1xF-B7 chain materialising ONLY when the
//     guard's pointer condition is removed — it labels the guard as
//     load-bearing, WITHOUT feeding the PRIMARY verdict.
//
// Modeled production interleaving (documented per the task): Reconcile
// enumerates at call time, so a test cannot freeze a mid-Run snapshot without a
// production seam (forbidden here). We instead use the honest equivalent the
// task authorises: park the execution at RUNNING with EXPIRED/FREE lease and
// run Reconcile(olderThan) so it treats the ACTIVE-release-bound execution as a
// FAILED-candidate cleanup, exactly the end-state of the "enumerate-before-
// publish" startup race where the publisher completes PublishExecution+pointer
// (and the release activates) between the reconciler's enumeration pass and its
// per-execution cleanup. Because reconcileExecution reads the pointer at :1068
// AFTER claiming — not from a frozen snapshot — the guard is evaluated against
// the CURRENT (post-publish, pointer-named) pointer in both the frozen-snapshot
// and live-race interleavings; that is what PREVENTS the chain. See TEST RESULT
// logs for the concrete observation.
// ---------------------------------------------------------------------------

// rdlClickHouse is the reconciler's deterministic effect recorder. Every drop
// is recorded and a dead context aborts, mirroring BundleClickHouseStore
// (bundle_store.go:53-61) so the observation "did reconcile drop table X" is
// trustworthy.
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

// rdlCatalog wraps the real Registry so the reconciler's guard decisions run on
// REAL Arango AQL while we observe that reconcileExecution really reached the
// candidate (lease claim) and really re-read the pointer (:1068). The wrapper
// decides nothing; every method delegates verbatim to the embedded Registry.
type rdlCatalog struct {
	*arangofactory.PublishedRegistry
	mu           sync.Mutex
	leaseClaims  map[string]string // execution key -> reconciler owner observed
	pointerReads map[string]int    // pointer name -> times reconcileExecution read it
}

func newRDLCatalog(registry *arangofactory.PublishedRegistry) *rdlCatalog {
	return &rdlCatalog{
		PublishedRegistry: registry,
		leaseClaims:       map[string]string{},
		pointerReads:      map[string]int{},
	}
}

func (c *rdlCatalog) AcquireBundleLease(ctx context.Context, key, owner string, expires time.Time) (bool, error) {
	claimed, err := c.PublishedRegistry.AcquireBundleLease(ctx, key, owner, expires)
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
	return c.PublishedRegistry.GetPointer(ctx, name)
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

// rdlVerifier is the publication verifier for the production release path. It
// echoes back a matching-selector verification for ONE selector, so
// dataset.ReleaseService.Create binds the execution we chose — the same shape
// the F-B6 chain artifact (name_latest_binding_e2e_test.go) used, but with a
// controlled verifier instead of a live FindExecutionBySelector.
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

// ---------------------------------------------------------------------------
// owned-database harness (LOOM_TEST_ARANGO_URL opt-in, SKIP-clean)
// ---------------------------------------------------------------------------

func rdlOpen(t *testing.T, budget time.Duration) (context.Context, *arangofactory.Client, *arangofactory.PublishedRegistry, *arangofactory.DatasetStore, string) {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL to run the F-B9xF-B1xF-B7 reconcile/release reproduction")
	}
	name := rdlDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(name, rdlDatabasePrefix) {
		t.Fatalf("verification database %q escapes the %s prefix", name, rdlDatabasePrefix)
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	t.Cleanup(cancel)
	arangoBackend, err := arangofactory.Open(ctx, arangofactory.Config{URL: endpoint, Database: name})
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	client := arangoBackend.Client
	t.Logf("RDL_DATABASE=%s", name)
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		req, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete,
			strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(name), nil)
		if err != nil {
			t.Errorf("build cleanup request for %s: %v", name, err)
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Errorf("drop database %s: %v", name, err)
			return
		}
		defer resp.Body.Close()
		// 403 = already-gone race tolerated, matching the sibling workers.
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusForbidden {
			t.Errorf("drop database %s: status %d", name, resp.StatusCode)
			return
		}
		t.Logf("RDL_DATABASE_REMOVED=%s", name)
	})
	if err := client.Bootstrap(ctx, arangofactory.PublishedBootstrapSpec()); err != nil {
		t.Fatalf("bootstrap bundle collections: %v", err)
	}
	if err := client.Bootstrap(ctx, arangofactory.DatasetBootstrapSpec()); err != nil {
		t.Fatalf("bootstrap dataset lifecycle collections: %v", err)
	}
	registry, err := arangofactory.NewPublishedRegistry(client)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	lifecycle, err := arangofactory.NewDatasetStore(client)
	if err != nil {
		t.Fatalf("new dataset lifecycle store: %v", err)
	}
	return ctx, client, registry, lifecycle, name[len(rdlDatabasePrefix):]
}

// ---------------------------------------------------------------------------
// seeding through the production registry paths
// ---------------------------------------------------------------------------

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

// rdlBuildExecution assembles a RUNNING execution whose single output is fully
// queryable (State PUBLISHED + VerifiedAt + PhysicalTable) so both the release
// read and the reconciler see a valid materialization target. The output
// Selector is set explicitly to the binding selector so
// releaseMaterialization's outputSelector comparison (current.go:103) is exact.
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

// rdlSeedRunning stores a RUNNING execution under a live seed lease then frees
// the key, so a later reconciler can claim it (the stalled-publisher end state
// reconcileExecution expects).
func rdlSeedRunning(t *testing.T, registry *arangofactory.PublishedRegistry, ctx context.Context, spec rdlExecSpec, owner string) pubpublication.BundleExecution {
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

// rdlSeedPublished completes the PUBLICISH flow through the production entry
// points: AcquireBundleLease (owner), SaveExecution (RUNNING doc exists so
// PublishExecution's `execution != null` guard holds), then PublishExecution
// which atomically flips the doc to PUBLISHED and creates the visibility
// pointer naming exec in one AQL (bundle_registry.go:274-310), then frees the
// lease so a reconcile can claim the key. Returns the published execution.
func rdlSeedPublished(t *testing.T, registry *arangofactory.PublishedRegistry, ctx context.Context, spec rdlExecSpec, owner string) pubpublication.BundleExecution {
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

// rdlFlipToRunning direct-patches ONLY the top-level `state` and `updatedAt`
// of the execution document (the "stale enumeration snapshot" the reconciler
// is tricked into seeing), leaving outputs, key, identity fields, the pointer
// (separate collection) and any reconcile-written marker untouched. Same
// direct-AQL UPDATE technique the sibling export_pin_crash_window /
// dangling_binding harnesses use to force an otherwise-unreachable state.
func rdlFlipToRunning(t *testing.T, client *arangofactory.Client, ctx context.Context, execID string) {
	t.Helper()
	patched := false
	err := client.QueryRows(ctx, `LET doc = DOCUMENT(@@collection, @key)
FILTER doc != null
UPDATE doc WITH {state: "RUNNING", updatedAt: @updatedAt} IN @@collection
RETURN {patched: true}`, 32, map[string]interface{}{
		"@collection": arangofactory.BundleExecutionsCollection,
		"key":         execID,
		"updatedAt":   time.Now().UTC().Add(-rdlSeedAge),
	}, func(row map[string]any) error {
		patched, _ = row["patched"].(bool)
		return nil
	})
	if err != nil || !patched {
		t.Fatalf("rdl flip %s to RUNNING: patched=%v err=%v", execID, patched, err)
	}
}

// rdlRestorePublished undoes ONLY this harness's own simulated RUNNING flag,
// returning the document to the PUBLISHED materialization it genuinely was
// (reconcile never wrote it, verified by the empty Error marker). This is the
// honest way to re-check the current-release READ result: with the injected
// stale-enumeration flag removed, the release read's health reflects exactly
// what reconcile did (which, under the guard, is nothing).
func rdlRestorePublished(t *testing.T, client *arangofactory.Client, ctx context.Context, execID string) {
	t.Helper()
	patched := false
	err := client.QueryRows(ctx, `LET doc = DOCUMENT(@@collection, @key)
FILTER doc != null
UPDATE doc WITH {state: "PUBLISHED", updatedAt: @updatedAt} IN @@collection
RETURN {patched: true}`, 32, map[string]interface{}{
		"@collection": arangofactory.BundleExecutionsCollection,
		"key":         execID,
		"updatedAt":   time.Now().UTC().Add(-rdlSeedAge),
	}, func(row map[string]any) error {
		patched, _ = row["patched"].(bool)
		return nil
	})
	if err != nil || !patched {
		t.Fatalf("rdl restore %s to PUBLISHED: patched=%v err=%v", execID, patched, err)
	}
}

// ---------------------------------------------------------------------------
// production dataset release Create + Activate over the real arangofactory.DatasetStore
// ---------------------------------------------------------------------------

// rdlCreateAndActivateRelease runs the PRODUCTION release path so the binding
// is written the way the product writes it: persist a STAGED manifest through
// arangofactory.CreateManifest/TransitionManifest, then dataset.ReleaseService
// with Manifests=Releases=lifecycle and a controlled verifier, and finally its
// Activate (Create + the real CompareAndSwapActivateRelease AQL).
//
// Premise STEP 0b is demonstrated here at runtime, not just quoted: the CAS
// Activate writes the release VERBATIM. release_store.go's activateReleaseAQL
// (:152-176) reads only @@lifecycle_collection documents and patches just the
// active-release + active-generation pointers; the release document itself was
// persisted byte-for-byte from Create's publicationBinding by SaveRelease via
// lifecycleDocument (release_store.go:105-115). It never opens the executions
// collection and never looks the execution up.
func rdlCreateAndActivateRelease(t *testing.T, ctx context.Context, lifecycle *arangofactory.DatasetStore, project, generation, execID string, selector dataset.DataframeSelector) dataset.ActiveRelease {
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
	// Independent round-trip through the production reader: the CAS wrote the
	// binding verbatim and activated the pointer.
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

// ---------------------------------------------------------------------------
// direct AQL fact readers
// ---------------------------------------------------------------------------

// rdlExecutionRow reads an execution document verbatim (raw, no GetExecution
// canonicalization) so the reconcile-written marker is observed exactly as the
// reconciler wrote it.
func rdlExecutionRow(t *testing.T, client *arangofactory.Client, ctx context.Context, execID string) (string, string) {
	t.Helper()
	state := ""
	errField := ""
	err := client.QueryRows(ctx, `RETURN DOCUMENT(@@collection, @key)`, 32, map[string]interface{}{
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
		t.Fatalf("rdl read execution %s: %v", execID, err)
	}
	return state, errField
}

// rdlActivePins reads the ExecutionReadPinsCollection document for an execution
// directly, returning the count of live pins (expiresAt >= now). This is the
// live probe for the F-B9 premise: an active-release-bound execution carries NO
// reader pin, so a pin can never be the reason it survives Reconcile.
func rdlActivePins(t *testing.T, client *arangofactory.Client, ctx context.Context, execID string) int {
	t.Helper()
	sum := sha256.Sum256([]byte("state\x00" + execID))
	stateKey := "state_" + hex.EncodeToString(sum[:])
	active := -1
	err := client.QueryRows(ctx, `LET state = DOCUMENT(@@collection, @stateKey)
LET pins = state == null ? [] : NOT_NULL(state.pins, [])
RETURN {active: LENGTH(FOR pin IN pins FILTER pin.expiresAt >= @now RETURN pin), found: state != null}`, 32, map[string]interface{}{
		"@collection": arangofactory.ExecutionReadPinsCollection, "stateKey": stateKey, "now": time.Now().UTC(),
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

// ---------------------------------------------------------------------------
// guard-source citation: fails the test if the :1068-1075 branch drifts, so the
// PREVENTED verdict can never survive a silent refactor of the guard.
// ---------------------------------------------------------------------------

func rdlRepoRoot(t *testing.T) string {
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

var rdlGuardRequiredLines = []string{
	"pointer, pointerErr := s.catalog.GetPointer(cleanupCtx, execution.PointerName())",
	"if pointerErr == nil && pointer.ExecutionID == execution.ID {",
	"return s.catalog.ReleaseBundleLease(cleanupCtx, execution.Key, reconcilerID)",
}

// rdlQuoteGuardBranch locates the reconcileExecution pointer-visibility guard in
// the real source, asserts its shape is intact, prints the quoted :1068-1075
// block, and proves the brief's "skipping bundle with pointer" log line does NOT
// exist (the skip is silent — the guard returns after releasing the lease), so
// the skip is proven behaviorally, never by a log.
func rdlQuoteGuardBranch(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(rdlRepoRoot(t), "internal/dataframe/publication/clickhouse/bundle_store.go"))
	if err != nil {
		t.Fatalf("guard source bundle_store.go unreadable: %v", err)
	}
	text := string(raw)
	lines := strings.Split(text, "\n")
	guardIdx := -1
	for i, line := range lines {
		if strings.Contains(line, "if pointerErr == nil && pointer.ExecutionID == execution.ID {") {
			guardIdx = i
			break
		}
	}
	if guardIdx < 0 {
		t.Fatalf("guard citation drift: the :1073 pointer-visibility branch is gone from bundle_store.go")
	}
	for _, need := range rdlGuardRequiredLines {
		if !strings.Contains(text, need) {
			t.Fatalf("guard citation drift: bundle_store.go no longer contains %q", need)
		}
	}
	if strings.Contains(text, "skipping bundle with pointer") {
		t.Fatalf("unexpected: production does log 'skipping bundle with pointer'; update this test's DISPROVEN claim")
	}
	low := max(0, guardIdx-4)
	high := min(len(lines), guardIdx+2)
	return strings.TrimRight(strings.Join(lines[low:high], "\n"), " \t")
}

func rdlMaterializationDigest(values []pubpublished.Materialization) string {
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

// ---------------------------------------------------------------------------
// the composite reproduction
// ---------------------------------------------------------------------------

// TestReconcileDanglesActiveReleaseBindingAgainstArango is the composite
// F-B9 x F-B1 x F-B7 adjudication. Pre-stated expectations (declared before it
// ever runs):
//
//	E1 healthy baseline: CurrentProjectDatasets(active project) == 1
//	  materialization {ID=<exec>:out-a, PhysicalTable=<active table>, State=READY},
//	  err == nil. Pins on the active exec == 0 (F-B9 live).
//	E2 guard skip (PRIMARY): after the stale-RUNNING flip + free lease +
//	  production Reconcile, reconcile enumerates+claims the candidate
//	  (rdlCatalog recorded the Reconcile's AcquireBundleLease and its
//	  GetPointer of the pointer that names the exec) and skips cleanup:
//	  active physical table NOT dropped, active row Error != reconcile marker.
//	  The current-release read, with ONLY the test's own RUNNING flag undone,
//	  is healthy again (reconcile did not degrade the binding). => PREVENTED.
//	E3 live machinery: the unpointered/unbound stale RUNNING control in the SAME
//	  sweep IS reclaimed (table dropped, row FAILED + marker), so E2's survival
//	  is attributable to the guard, not to an inert reconciler.
//	E4 binding-veto arm: the ACTIVE-release-bound execution whose pointer was
//	  repointed to a shadow must SURVIVE the same sweep: the post-claim live
//	  re-read finds no naming pointer but the ACTIVE release binds it, so the
//	  binding veto (Repair-1b) denies the drop. Pre-Fix this arm dropped the
//	  table and CurrentProjectDatasets(shadow project) failed wholesale
//	  (nil + error) — the archived F-B12 red (FAILED-ON-PURPOSE). If the table
//	  ever disappears under an active binding again, this arm fails as the
//	  regression direction. The verdict gates on E1-E3 alongside this arm.
//
// Verdict: CONFIRMED iff the PRIMARY active-release-bound execution ends
// dangling (its table dropped OR reconcile wrote the FAILED marker) AND its
// current-list call would fail wholesale; then t.Fatalf FAILED-ON-PURPOSE.
// PREVENTED (the expected outcome): quote :1068-1075, log the guard skip, PASS.
func TestReconcileDanglesActiveReleaseBindingAgainstArango(t *testing.T) {
	quote := rdlQuoteGuardBranch(t)
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

	// ---- PRIMARY: publish + activate + baseline -------------------------------
	pubActive := rdlSeedPublished(t, registry, ctx, specActive, "rdl-pub-active")
	if got := rdlActivePins(t, client, ctx, pubActive.ID); got != 0 {
		t.Fatalf("E1/F-B9 precondition: active-release-bound exec %s unexpectedly carries %d live pins at baseline", pubActive.ID, got)
	}
	rdlCreateAndActivateRelease(t, ctx, lifecycle, projectActive, genActive, pubActive.ID, selectorActive)

	reader := &pubpublished.Reader{Catalog: registry, ActiveReleaseResolver: lifecycle}
	list, err := reader.CurrentProjectDatasets(ctx, projectActive)
	if err != nil || len(list) != 1 || list[0].ID != pubActive.ID+":out-a" || list[0].PhysicalTable != specActive.table {
		t.Fatalf("E1 healthy baseline broken: list=%s err=%v", rdlMaterializationDigest(list), err)
	}
	if single, sErr := reader.CurrentProjectDataset(ctx, projectActive, selectorActive); sErr != nil || single.ID != pubActive.ID+":out-a" {
		t.Fatalf("E1 healthy single-selector baseline broken: %+v err=%v", single, sErr)
	}
	t.Logf("E1 OBSERVED healthy baseline (pre-fault): CurrentProjectDatasets=%s err=%v; F-B9 live pins on active=%d", rdlMaterializationDigest(list), err, rdlActivePins(t, client, ctx, pubActive.ID))

	// ---- LIVE-MACHINERY CONTROL: stale, not pointer-named, not release-bound ---
	stale := rdlSeedRunning(t, registry, ctx, specStale, "rdl-seed-stale")

	// ---- ABSENT-GUARD CONTROL: publish the shadow, then repoint its pointer away
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

	// ---- force the faults: every candidate looks like a stale RUNNING doc -------
	rdlFlipToRunning(t, client, ctx, pubActive.ID)
	rdlFlipToRunning(t, client, ctx, shadow.ID)
	// stale was seeded RUNNING already and its lease was freed on seed.

	// ---- one production Reconcile sweep covers all three candidates ----------------
	catalog := newRDLCatalog(registry)
	ch := newRDLClickHouse()
	store, err := NewBundleStore(ch, catalog)
	if err != nil {
		t.Fatalf("NewBundleStore: %v", err)
	}
	// Production wiring mirror (server.go): the reconciler re-checks the
	// ACTIVE release bindings immediately before any drop (Repair-1b). The
	// PREVENTED verdict gate then holds instead of FAILED-ON-PURPOSE.
	store.SetActiveReleaseReader(lifecycle)
	olderThan := time.Now().UTC().Add(-rdlReconcileOlderThan)
	reconcileErr := store.Reconcile(ctx, olderThan)
	if reconcileErr != nil {
		// The scenario must reconcile cleanly: the guard returns nil for the
		// candidate, and the two cleanups fenced-save nil. A non-nil return is a
		// real observation, not a pass/fail of the composite.
		t.Logf("NOTE: production Reconcile returned %v (expected nil for all three candidates)", reconcileErr)
	}
	dropped := ch.snapshotDropped()

	// read every candidate's post-reconcile doc verbatim (raw: the reconcile
	// "stale execution reconciled" marker is only observable un-canonicalized).
	activeState, activeErr := rdlExecutionRow(t, client, ctx, pubActive.ID)
	staleState, staleErr := rdlExecutionRow(t, client, ctx, stale.ID)
	shadowState, shadowErr := rdlExecutionRow(t, client, ctx, shadow.ID)

	primaryTableDropped := ch.didDrop(specActive.table)
	primaryDemotedByReconcile := activeErr == rdlReconcileMarker ||
		(pubpublication.BundleState(activeState).Canonical() == pubpublication.BundleFailed)

	// ---- (1) verdict gate: the PRIMARY composite reproducing means FAILED -------
	if primaryTableDropped || primaryDemotedByReconcile {
		// Before declaring CONFIRMED, confirm the current-list actually broke,
		// i.e. F-B7 is triggered by this dangling.
		hardList, hardErr := reader.CurrentProjectDatasets(ctx, projectActive)
		listBrokeHard := hardErr != nil && len(hardList) == 0
		if listBrokeHard {
			t.Fatalf("FAILED-ON-PURPOSE CONFIRMED of the composite F-B9 x F-B1 x F-B7 against real Arango: an ACTIVE-release-bound + pointer-named execution was dangled by startup Reconcile and its current dataset list failed wholesale. active=%q state=%q reconcileMarker=%q tableDropped=%v (dropped=%v); F-B9 live pins=%d (release path pins nothing → only the pointer re-check + active-release binding veto shield); catalog observed reconcile lease-claim owner=%q and %d pointer re-read(s) of %q; CurrentProjectDatasets post-fault list=[%d] err=[%s]; guard that should have skipped is:\n%s",
				pubActive.ID, activeState, activeErr, primaryTableDropped, dropped,
				rdlActivePins(t, client, ctx, pubActive.ID), mustOwner(t, catalog, specActive.key), catalog.pointerReadCount(pubActive.PointerName()), pubActive.PointerName(),
				len(hardList), rdlErrChain(hardErr), quote)
		} else {
			t.Fatalf("inconsistent CONFIRMED: active exec looked dangled (dropped=%v marker=%q state=%q) but the current list survived: list=[%d] err=%v — re-examine attribution", primaryTableDropped, activeErr, activeState, len(hardList), rdlErrChain(hardErr))
		}
	}

	// ---- (2) PREVENTED: guard must have held the PRIMARY ------------------------
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
	// Undo the test's own simulated RUNNING flag; reconcile wrote nothing, so the
	// current-release read's health now reflects the reconciler's true non-effect.
	rdlRestorePublished(t, client, ctx, pubActive.ID)
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

	// ---- (3) live-machinery control ---------------------------------------------
	if !ch.didDrop(specStale.table) || staleErr != rdlReconcileMarker || pubpublication.BundleState(staleState).Canonical() != pubpublication.BundleFailed {
		t.Fatalf("LIVE-MACHINERY control broken: reconcile did not reclaim the unpointered/unbound stale control (staleState=%q staleErr=%q dropped=%v) — the sweep was not genuinely live, so the PREVENTED verdict above would be vacuous", staleState, staleErr, dropped)
	}
	t.Logf("E3 OBSERVED live machinery: the unpointered stale RUNNING control %s was reclaimed (state=%q marker=%q tableDropped=%v)", stale.ID, staleState, staleErr, ch.didDrop(specStale.table))

	// ---- (4) binding-veto arm: unpointered ACTIVE-bound exec must survive ----
	if ch.didDrop(specShadow.table) || shadowErr == rdlReconcileMarker || pubpublication.BundleState(shadowState).Canonical() != pubpublication.BundleRunning {
		t.Errorf("REGRESSION (F-B12): the unpointered ACTIVE-release-bound execution %s was reclaimed by Reconcile (state=%q marker=%q tableDropped=%v) — the post-claim active-release binding veto must deny this drop; the ACTIVE release still binds it (archived FAILED-ON-PURPOSE red: this drop destroyed advertised data and failed CurrentProjectDatasets wholesale)", shadow.ID, shadowState, shadowErr, ch.didDrop(specShadow.table))
	}
	rdlRestorePublished(t, client, ctx, shadow.ID)
	shadowAfter, shadowListErr := reader.CurrentProjectDatasets(ctx, projectShadow)
	if shadowListErr != nil || len(shadowAfter) != 1 || shadowAfter[0].ID != shadow.ID+":out-b" {
		t.Fatalf("binding-veto claim broken: with the drop denied and only the harness's RUNNING flag undone, the shadow release read is not healthy: list=%s err=[%s]", rdlMaterializationDigest(shadowAfter), rdlErrChain(shadowListErr))
	}

	t.Logf("VERDICT PREVENTED for the composite F-B9 x F-B1 x F-B7: the pointer-named active-release-bound exec survived startup Reconcile via the pointer re-check, and the unpointered bound exec survived via the active-release binding veto (Repair-1b); live pins=%d; guard branch:\n%s", rdlActivePins(t, client, ctx, pubActive.ID), quote)

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
