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

const rdlDatabasePrefix = "loom_verify_rdl_"

const (
	rdlSeedAge            = 90 * time.Minute
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
	*arangofactory.PublishedRegistry
	mu           sync.Mutex
	leaseClaims  map[string]string
	pointerReads map[string]int
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

	rdlFlipToRunning(t, client, ctx, pubActive.ID)
	rdlFlipToRunning(t, client, ctx, shadow.ID)

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
		(pubpublication.BundleState(activeState).Canonical() == pubpublication.BundleFailed)

	if primaryTableDropped || primaryDemotedByReconcile {
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

	if !ch.didDrop(specStale.table) || staleErr != rdlReconcileMarker || pubpublication.BundleState(staleState).Canonical() != pubpublication.BundleFailed {
		t.Fatalf("LIVE-MACHINERY control broken: reconcile did not reclaim the unpointered/unbound stale control (staleState=%q staleErr=%q dropped=%v) — the sweep was not genuinely live, so the PREVENTED verdict above would be vacuous", staleState, staleErr, dropped)
	}
	t.Logf("E3 OBSERVED live machinery: the unpointered stale RUNNING control %s was reclaimed (state=%q marker=%q tableDropped=%v)", stale.ID, staleState, staleErr, ch.didDrop(specStale.table))

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
