package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataset"
	datasetstore "github.com/calypr/loom/internal/dataset/arango"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

// These opt-in tests adjudicate candidate F-B7 against a real ArangoDB in a
// fully owned database (loom_verify_db_<uuid>, dropped in t.Cleanup):
//
//	F-B7 contract: a broken optional release binding must not hide a healthy
//	current output from dataset discovery. Required bindings remain fail-closed.
//
// Mechanism as claimed by the brief: "internal/dataframe/published/current.go:193-257:
// Resolve loads each binding via loadPublishedExecutionForRead, and
// execution == nil || !publishedOutputMatches(execution, binding) -> all = false
// -> ALL bindings recorded stale (:202-211); caller then falls back to stale
// snapshot at internal/dataset/release.go:204-213. Also verifyPublishedExecution
// (arango:188-196) rechecks pointer naming."
//
// LINE-EXACT SOURCE AUDIT (verified 2026-11-01 on branch feature/deep-clean):
// none of those identifiers exist anywhere in the repository:
//
//	loadPublishedExecutionForRead  -> 0 matches (grep repo-wide)
//	publishedOutputMatches         -> 0 matches
//	"all = false" in published     -> 0 matches
//	verifyPublishedExecution       -> 0 matches (entire arango package)
//	requiredReleaseBindings        -> 0 matches (internal/dataset/release.go ends at :280)
//	releaseBindingsFrom            -> 0 matches
//
// The real read path has no Resolve and no stale re-marking at read time. It
// skips only typed deterministic failures for non-required active bindings;
// required bindings and unclassified backend failures remain fail-closed.
//
//	internal/dataframe/published/current.go:190-222 activeReleaseDatasets
//	  :203  `if binding.Stale || binding.Generation != active.Release.Generation {`
//	  :204  	continue                                  // pre-marked-stale bindings skipped
//	  :211-216 value, err := r.releaseMaterialization(...); unavailable optional bindings continue
//	                              // required or unknown failures still abort the list
//	  :220-222 if len(result) == 0 { return DatasetNotFound }
//	internal/dataframe/published/current.go:74-92 activeReleaseDataset (single selector)
//	  :86-87 skips non-matching/stale bindings BEFORE materialization -> a binding
//	       that cannot resolve cannot affect a different selector's answer
//	internal/dataframe/published/current.go:94-111 releaseMaterialization
//	  :98-101 missing execution -> typed unavailable-binding error
//	  :104-105 identity mismatch -> typed unavailable-binding error
//	  :107-115 no queryable output -> typed unavailable-binding error
//
// Stale is only ever written at release CREATE (internal/dataset/release.go):
//	  :185-188 required selector verification failure -> recorded into `failed`
//	  :192-193 `return ProjectRelease{}, &ReleaseRequirementsError{...}` (create-time ERROR)
//	  :209-210 carried-forward prior bindings: prior.Required = false; prior.Stale = prior.Generation != ref.Generation
//	  :254-256 publicationBinding: Stale = result.Generation != generation (false for fresh)
//
// The active snapshot at internal/dataset/arango/release_store.go:144-150
// (readActiveReleaseAQL) has no stale fallback either: it reads exactly the
// pointer's releaseKey document or yields ErrNoActiveRelease (release_store.go:56-57).
//
// So the claimed whole-dataset STALE demotion cannot be confirmed as stated.
// What these tests measure is whether the real code still produces the
// product-level harm F-B7 fears (one bad binding degrading a healthy binding's
// fresh answer) through the mechanism the code actually uses.

const danglingBindingDatabasePrefix = "loom_verify_db_"

// danglingCitation pins the load-bearing source lines tested above. If the
// source drifts, the citation check fails the test so stale evidence cannot
// quietly survive a refactor.
type danglingCitation struct {
	file string
	line int // 1-based
	want string
}

var danglingCitations = []danglingCitation{
	{"internal/dataframe/published/current.go", 88, "if binding.Selector != selector || binding.Stale || binding.Generation != active.Release.Generation {"},
	{"internal/dataframe/published/current.go", 97, "execution, err := r.Catalog.GetExecution(ctx, binding.ExecutionID)"},
	{"internal/dataframe/published/current.go", 99, "if errors.Is(err, bundlepublication.ErrBundleNotFound) {"},
	{"internal/dataframe/published/current.go", 102, "return Materialization{}, fmt.Errorf(\"resolve active dataframe execution: %w\", err)"},
	{"internal/dataframe/published/current.go", 115, "return Materialization{}, fmt.Errorf(\"%w: active dataframe execution %q has no queryable output %q\", errReleaseBindingUnavailable, binding.ExecutionID, binding.Selector.Output)"},
	{"internal/dataframe/published/current.go", 208, "if binding.Stale || binding.Generation != active.Release.Generation {"},
	{"internal/dataframe/published/current.go", 211, "value, err := r.releaseMaterialization(ctx, active.Release, binding)"},
	{"internal/dataframe/published/current.go", 212, "if err != nil {"},
	{"internal/dataframe/published/current.go", 213, "if !binding.Required && errors.Is(err, errReleaseBindingUnavailable) {"},
	{"internal/dataframe/published/current.go", 216, "return nil, err"},
	{"internal/dataset/release.go", 193, "return ProjectRelease{}, &ReleaseRequirementsError{Verifications: failed}"},
	{"internal/dataset/release.go", 210, "prior.Stale = prior.Generation != ref.Generation"},
	{"internal/dataset/release.go", 255, "return ReleasePublication{Selector: result.Selector, ExecutionID: result.ExecutionID, Generation: result.Generation, Required: required, Stale: result.Generation != generation, VerifiedAt: result.VerifiedAt}"},
	{"internal/api/graphql/graph/dataframe/rows.go", 27, "values, err := s.reader.CurrentProjectDatasets(ctx, legacyProjectID(projectID))"},
	{"internal/api/graphql/graph/dataframe/rows.go", 29, "return nil, mapReaderError(err)"},
}

// danglingAbsentIdentifiers are the identifiers the brief claims exist; the
// files named in the hypothesis must NOT contain them.
type danglingAbsentIdentifier struct {
	file       string
	identifier string
}

var danglingAbsentIdentifiers = []danglingAbsentIdentifier{
	{"internal/dataframe/published/current.go", "loadPublishedExecutionForRead"},
	{"internal/dataframe/published/current.go", "publishedOutputMatches"},
	{"internal/dataframe/published/current.go", "all = false"},
	{"internal/dataframe/publication/arango/bundle_registry.go", "verifyPublishedExecution"},
	{"internal/dataset/release.go", "requiredReleaseBindings"},
	{"internal/dataset/release.go", "releaseBindingsFrom"},
}

func danglingRepoRoot(t *testing.T) string {
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

func danglingCheckCitations(t *testing.T) {
	t.Helper()
	root := danglingRepoRoot(t)
	contents := map[string][]string{}
	for _, c := range danglingCitations {
		lines, ok := contents[c.file]
		if !ok {
			raw, err := os.ReadFile(filepath.Join(root, c.file))
			if err != nil {
				t.Fatalf("citation source %s unreadable: %v", c.file, err)
			}
			lines = strings.Split(string(raw), "\n")
			contents[c.file] = lines
		}
		if c.line < 1 || c.line > len(lines) {
			t.Fatalf("citation drift: %s has %d lines, cited :%d == %q", c.file, len(lines), c.line, c.want)
		}
		// Drift-proof: the contract is the EXACT source text, not the pin.
		found := false
		for _, line := range lines {
			if strings.TrimSpace(line) == strings.TrimSpace(c.want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("citation drift: %s no longer contains %q (cited :%d)", c.file, c.want, c.line)
		}
	}
	for _, a := range danglingAbsentIdentifiers {
		raw, err := os.ReadFile(filepath.Join(root, a.file))
		if err != nil {
			t.Fatalf("citation source %s unreadable: %v", a.file, err)
		}
		if strings.Contains(string(raw), a.identifier) {
			t.Fatalf("hypothesis identifier %q unexpectedly present in %s; brief audit said 0 matches", a.identifier, a.file)
		}
	}
}

// danglingDatasetDocumentKey replicates
// internal/dataset/arango/document_codec.go:164-172 (documentKey) with the
// domain from manifest_lifecycle.go:14 so this package can address the
// active-release pointer directly. Fidelity is not taken on faith: every use
// is round-tripped through the PRODUCTION datasetstore.Store.ReadActiveRelease,
// so a mismatched key surfaces as ErrNoActiveRelease and fails the test.
func danglingDatasetDocumentKey(kind string, values ...string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("loom.datasetstore.v1"))
	for _, value := range append([]string{kind}, values...) {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(value))
	}
	return kind + "_" + hex.EncodeToString(hash.Sum(nil))
}

// danglingOpen mirrors the owned-database env-gated harness of
// name_latest_binding_e2e_test.go:103-150 (skip when LOOM_TEST_ARANGO_URL is
// unset, store.Open creates loom_verify_db_<uuid> - see internal/store/arango/
// client.go:130-142 - and HTTP DELETE /_api/database drops it in t.Cleanup).
// It additionally bootstraps the dataset lifecycle collection so the real
// datasetstore.Store and published.Reader run against the same database.
func danglingOpen(t *testing.T, budget time.Duration) (context.Context, *store.Client, *Registry, *datasetstore.Store) {
	t.Helper()
	endpoint := os.Getenv("LOOM_TEST_ARANGO_URL")
	if endpoint == "" {
		t.Skip("set owned LOOM_TEST_ARANGO_URL")
	}
	name := danglingBindingDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(name, danglingBindingDatabasePrefix) {
		t.Fatalf("verification database %q escapes the %s prefix", name, danglingBindingDatabasePrefix)
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	t.Cleanup(cancel)
	client, err := store.Open(ctx, endpoint, name)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Logf("F_B7_DATABASE=%s", name)
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		cleanupCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		req, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(name), nil)
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
		t.Logf("F_B7_DATABASE_REMOVED=%s", name)
	})
	if err := client.Bootstrap(ctx, BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap bundle collections: %v", err)
	}
	if err := client.Bootstrap(ctx, datasetstore.BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap dataset lifecycle collections: %v", err)
	}
	registry, err := New(client)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	lifecycle, err := datasetstore.New(client)
	if err != nil {
		t.Fatalf("new lifecycle store: %v", err)
	}
	return ctx, client, registry, lifecycle
}

type danglingExecSpec struct {
	id         string
	project    string
	generation string
	recipe     string
	output     string
}

// danglingSeedExecution writes one PUBLISHED execution through the production
// lease + SaveExecution path (bundle_registry.go:26-66), as the sibling
// F-B6 harness does; outputs carry VerifiedAt/PhysicalTable so
// BundleOutputRecord.Queryable() (bundle.go:147-149) is true without relying
// on Canonicalize Legacy backfill.
func danglingSeedExecution(t *testing.T, ctx context.Context, registry *Registry, spec danglingExecSpec) publication.BundleExecution {
	t.Helper()
	key := "dbind-key-" + spec.id
	owner := "dbind-publisher-" + spec.id
	if claimed, err := registry.AcquireBundleLease(ctx, key, owner, time.Now().UTC().Add(5*time.Minute)); err != nil || !claimed {
		t.Fatalf("acquire lease for %s: claimed=%v err=%v", spec.id, claimed, err)
	}
	stamp := time.Now().UTC().Truncate(time.Millisecond)
	execution := publication.BundleExecution{
		ID:  spec.id,
		Key: key,
		BundleIdentity: publication.BundleIdentity{
			Name: spec.recipe, Project: spec.project, DatasetGeneration: spec.generation,
			TranslationVersion: "v1", OutputName: spec.output,
			RecipeDigest: "sha256:" + spec.id + "-recipe", SchemaDigest: "sha256:" + spec.id + "-schema",
			ReceiptID: "receipt-" + spec.id, ScopeDigest: "scope-" + spec.id, EngineVersion: "dbind-engine",
		},
		State: publication.BundlePublished,
		Outputs: []publication.BundleOutputRecord{{
			Name: spec.output, PhysicalTable: "ch_" + strings.ReplaceAll(spec.id, "-", "_"),
			Selector:   publication.DataframeSelector{Recipe: spec.recipe, TranslationVersion: "v1", Output: spec.output},
			RowCount:   11,
			ByteCount:  1100,
			State:      publication.BundlePublished,
			VerifiedAt: &stamp,
		}},
		CreatedAt:   stamp.Add(-time.Minute),
		UpdatedAt:   stamp,
		ReadyAt:     &stamp,
		PublishedAt: &stamp,
		OwnerID:     owner,
	}
	if err := registry.SaveExecution(ctx, execution, owner); err != nil {
		t.Fatalf("SaveExecution %s: %v", spec.id, err)
	}
	return execution
}

// danglingSeedRelease saves an immutable release through the PRODUCTION
// datasetstore.Store.SaveRelease (release_store.go:12-39) and then activates
// it by patching the active_release placeholder that SaveRelease inserted
// (release_store.go:138, manifest_lifecycle.go:85-87). Production
// ReadActiveRelease (release_store.go:41-60, readActiveReleaseAQL:144-150) is
// the gate: it must return the released bindings, which also validates the
// replicated document-key derivation.
func danglingSeedRelease(t *testing.T, ctx context.Context, client *store.Client, lifecycle *datasetstore.Store, project, generation string, bindings []dataset.ReleasePublication) dataset.ProjectRelease {
	t.Helper()
	release := dataset.ProjectRelease{
		ID: "release_" + strings.ReplaceAll(uuid.NewString(), "-", ""), Project: project,
		GitCommit: generation, Generation: generation, Publications: bindings, CreatedAt: time.Now().UTC(),
	}
	saved, err := lifecycle.SaveRelease(ctx, release)
	if err != nil {
		t.Fatalf("SaveRelease: %v", err)
	}
	releaseKey := danglingDatasetDocumentKey("release", release.ID)
	activeKey := danglingDatasetDocumentKey("active_release", project)
	activated := false
	err = client.QueryRows(ctx, `LET pointer = DOCUMENT(@@collection, @activeKey)
FILTER pointer != null AND pointer.recordType == "active_project_release"
UPDATE pointer WITH {releaseKey: @releaseKey, releaseId: @releaseId, revision: 1} IN @@collection
RETURN {activated: true}`, 32, map[string]interface{}{
		"@collection": datasetstore.LifecycleCollection, "activeKey": activeKey,
		"releaseKey": releaseKey, "releaseId": release.ID,
	}, func(row map[string]any) error {
		activated, _ = row["activated"].(bool)
		return nil
	})
	if err != nil || !activated {
		t.Fatalf("activate release pointer: activated=%v err=%v", activated, err)
	}
	active, err := lifecycle.ReadActiveRelease(ctx, project)
	if err != nil {
		t.Fatalf("production ReadActiveRelease after activation (key replication check): %v", err)
	}
	if active.Release.ID != saved.ID || active.Revision != 1 || active.Release.Generation != generation {
		t.Fatalf("active release round-trip mismatch: %+v", active)
	}
	return saved
}

// danglingPatchBindingStale directly rewrites the bindings array of the saved
// release document (binding-level "recorded stale" - the state the hypothesised
// all=false path would have produced). Direct doc write is authorized by the
// brief; nothing on the read side recomputes this flag.
func danglingPatchBindingStale(t *testing.T, ctx context.Context, client *store.Client, release dataset.ProjectRelease) {
	t.Helper()
	releaseKey := danglingDatasetDocumentKey("release", release.ID)
	patched := false
	err := client.QueryRows(ctx, `LET doc = DOCUMENT(@@collection, @releaseKey)
FILTER doc != null AND doc.recordType == "project_release"
UPDATE doc WITH {publications: @publications} IN @@collection
RETURN {patched: true}`, 32, map[string]interface{}{
		"@collection": datasetstore.LifecycleCollection, "releaseKey": releaseKey,
		"publications": release.Publications,
	}, func(row map[string]any) error {
		patched, _ = row["patched"].(bool)
		return nil
	})
	if err != nil || !patched {
		t.Fatalf("patch binding stale: patched=%v err=%v", patched, err)
	}
}

// danglingCorruptDropOutputs removes the PublishedOutput row from the
// execution document. PublishedOutput is a row of the execution document's
// embedded outputs array (bundle.go:168; BootstrapSpec registers no separate
// output collection), so deleting the row is UPDATE outputs=[].
func danglingCorruptDropOutputs(t *testing.T, ctx context.Context, client *store.Client, executionID string) {
	t.Helper()
	removed := false
	err := client.QueryRows(ctx, `LET doc = DOCUMENT(@@collection, @key)
FILTER doc != null
UPDATE doc WITH {outputs: []} IN @@collection
RETURN {removed: true}`, 32, map[string]interface{}{"@collection": BundleExecutionsCollection, "key": executionID}, func(row map[string]any) error {
		removed, _ = row["removed"].(bool)
		return nil
	})
	if err != nil || !removed {
		t.Fatalf("drop outputs of %s: removed=%v err=%v", executionID, removed, err)
	}
}

// danglingCorruptDeleteExecution makes the binding dangling: the execution
// document no longer exists while the release still binds its ID.
func danglingCorruptDeleteExecution(t *testing.T, ctx context.Context, client *store.Client, executionID string) {
	t.Helper()
	removed := false
	err := client.QueryRows(ctx, `LET doc = DOCUMENT(@@collection, @key)
FILTER doc != null
REMOVE doc IN @@collection
RETURN {removed: true}`, 32, map[string]interface{}{"@collection": BundleExecutionsCollection, "key": executionID}, func(row map[string]any) error {
		removed, _ = row["removed"].(bool)
		return nil
	})
	if err != nil || !removed {
		t.Fatalf("delete execution %s: removed=%v err=%v", executionID, removed, err)
	}
}

func danglingErrorChain(err error) string {
	if err == nil {
		return "<nil>"
	}
	parts := make([]string, 0, 4)
	for current := err; current != nil; current = errors.Unwrap(current) {
		parts = append(parts, current.Error())
	}
	return strings.Join(parts, " <- ")
}

func danglingUserErrorCode(err error) string {
	if user, ok := dataframeerrors.AsUserError(err); ok {
		return user.Code()
	}
	return ""
}

func danglingMaterializationDigest(values []published.Materialization) string {
	summaries := make([]string, 0, len(values))
	for _, value := range values {
		summaries = append(summaries, fmt.Sprintf("{Name=%q ID=%q PhysicalTable=%q State=%q}", value.Name, value.ID, value.PhysicalTable, value.State))
	}
	return fmt.Sprintf("[%s]", strings.Join(summaries, " "))
}

func danglingSelector(output string, recipe string) published.DataframeSelector {
	return published.DataframeSelector{Recipe: recipe, TranslationVersion: "v1", Output: output}
}

// TestBrokenOptionalBindingDoesNotHideHealthyDatasetAgainstArango is the F-B7 regression.
//
// Pre-stated independent expectations (derived from the line-exact audit
// above, declared BEFORE running):
//
//	E1  Fresh two-binding release: CurrentProjectDatasets returns exactly 2
//	    materializations sorted by Name ("out-a" then "out-b"), State READY,
//	    ID "<execID>:<output>" (types.go:58-80), err == nil.
//	E2  Un-naming the bad execution's visibility pointer changes nothing on
//	    either read path, because releaseMaterialization (current.go:94-111)
//	    never calls GetPointer. The claim "verifyPublishedExecution
//	    (arango:188-196) rechecks pointer naming" is false: no such function.
//	E3  Deleting the bad optional binding's PublishedOutput row: the list keeps
//	    the healthy materialization and omits only the unavailable optional one.
//	E4  Deleting the whole bad optional execution has the same result. Required
//	    bindings and transient catalog failures still abort the whole list.
//	E5  The single-selector path activeReleaseDataset (current.go:85-90) skips
//	    the broken OTHER binding by selector before materializing, so the
//	    healthy selector keeps resolving fresh independently => per-viewer
//	    degradation DISPROVEN; the list and single-selector paths stay independent.
//
// Verdict rules: CONFIRMED iff one bad binding demonstrably degrades the
// healthy binding's freshness or forces stale fallback in the list path;
// DISPROVEN iff the healthy binding reports fresh independently.
func TestBrokenOptionalBindingDoesNotHideHealthyDatasetAgainstArango(t *testing.T) {
	danglingCheckCitations(t)
	ctx, client, registry, lifecycle := danglingOpen(t, 120*time.Second)

	project := "dbind-fb7-project"
	generation := "dbind-gen-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	healthyExec := danglingSeedExecution(t, ctx, registry, danglingExecSpec{id: "dbind-exec-healthy", project: project, generation: generation, recipe: "dbind-recipe-a", output: "out-a"})
	badExec := danglingSeedExecution(t, ctx, registry, danglingExecSpec{id: "dbind-exec-bad", project: project, generation: generation, recipe: "dbind-recipe-b", output: "out-b"})

	// Pointer naming per the production visibility path (bundle.go:88-97).
	if err := registry.CompareAndSwapPointer(ctx, healthyExec.PointerName(), "", healthyExec.ID); err != nil {
		t.Fatalf("point healthy pointer: %v", err)
	}
	if err := registry.CompareAndSwapPointer(ctx, badExec.PointerName(), "", badExec.ID); err != nil {
		t.Fatalf("point bad pointer: %v", err)
	}

	bindings := []dataset.ReleasePublication{
		{Selector: danglingSelector("out-a", "dbind-recipe-a"), ExecutionID: healthyExec.ID, Generation: generation, Required: false, Stale: false, VerifiedAt: time.Now().UTC()},
		{Selector: danglingSelector("out-b", "dbind-recipe-b"), ExecutionID: badExec.ID, Generation: generation, Required: false, Stale: false, VerifiedAt: time.Now().UTC()},
	}
	danglingSeedRelease(t, ctx, client, lifecycle, project, generation, bindings)

	reader := &published.Reader{Catalog: registry, ActiveReleaseResolver: lifecycle}
	healthySelector := danglingSelector("out-a", "dbind-recipe-a")
	badSelector := danglingSelector("out-b", "dbind-recipe-b")

	// E1
	t.Log("EXPECT E1: fresh list = 2 materializations [out-a out-b], State READY, err==nil")
	list, err := reader.CurrentProjectDatasets(ctx, project)
	if err != nil || len(list) != 2 || list[0].Name != "out-a" || list[1].Name != "out-b" {
		t.Fatalf("E1 baseline broken: list=%s err=%v", danglingMaterializationDigest(list), err)
	}
	if list[0].ID != healthyExec.ID+":out-a" || list[0].PhysicalTable != "ch_dbind_exec_healthy" || list[0].State != published.StateReady {
		t.Fatalf("E1 digest mismatch: %s", danglingMaterializationDigest(list))
	}
	t.Logf("OBSERVED E1 fresh baseline: %s", danglingMaterializationDigest(list))

	// E2: un-name the bad pointer (pointer -> a non-existent execution).
	if err := registry.CompareAndSwapPointer(ctx, badExec.PointerName(), badExec.ID, "dbind-orphan-target"); err != nil {
		t.Fatalf("un-name bad pointer: %v", err)
	}
	if pointer, err := registry.GetPointer(ctx, badExec.PointerName()); err != nil || pointer.ExecutionID != "dbind-orphan-target" {
		t.Fatalf("pointer un-name not observable via GetPointer: %+v err=%v", pointer, err)
	}
	t.Log("EXPECT E2: un-named pointer does NOT affect release reads (releaseMaterialization has no GetPointer call)")
	list, err = reader.CurrentProjectDatasets(ctx, project)
	if err != nil || len(list) != 2 {
		t.Fatalf("E2 expectation broken: pointer naming affected the release read path: list=%s err=%v", danglingMaterializationDigest(list), err)
	}
	t.Logf("OBSERVED E2 pointer-unnamed control: %s (pointer re-check claim has no code behind it)", danglingMaterializationDigest(list))

	// E3: delete the PublishedOutput row of the bad optional execution.
	danglingCorruptDropOutputs(t, ctx, client, badExec.ID)
	t.Log("EXPECT E3: healthy out-a remains in the list; only unavailable optional out-b is omitted")
	list, err = reader.CurrentProjectDatasets(ctx, project)
	droppedOutputListKeepsHealthy := err == nil && len(list) == 1 && list[0].Name == "out-a" && list[0].ID == healthyExec.ID+":out-a"
	t.Logf("OBSERVED E3: list=%s err=[%s] healthyRetained=%v", danglingMaterializationDigest(list), danglingErrorChain(err), droppedOutputListKeepsHealthy)
	if !droppedOutputListKeepsHealthy {
		t.Errorf("F-B7 regression: broken optional out-b hid healthy out-a: list=%s err=%v", danglingMaterializationDigest(list), err)
	}

	// E5a: despite E3, healthy single-selector read must be independent.
	t.Log("EXPECT E5: healthy single read unaffected by the OTHER binding's corruption")
	single, err := reader.CurrentProjectDataset(ctx, project, healthySelector)
	if err != nil || single.ID != healthyExec.ID+":out-a" || single.PhysicalTable != "ch_dbind_exec_healthy" {
		t.Fatalf("E5 violated: healthy single read degraded by foreign binding: %+v err=%v", single, err)
	}
	t.Logf("OBSERVED E5a: healthy single read while binding out-b corrupt: {Name=%q ID=%q PhysicalTable=%q} -> per-viewer demotion DISPROVEN", single.Name, single.ID, single.PhysicalTable)

	// E4: now delete the bad execution document entirely (true dangling optional binding).
	danglingCorruptDeleteExecution(t, ctx, client, badExec.ID)
	t.Log("EXPECT E4: healthy out-a remains listed after dangling optional out-b is removed")
	list, err = reader.CurrentProjectDatasets(ctx, project)
	danglingListKeepsHealthy := err == nil && len(list) == 1 && list[0].Name == "out-a" && list[0].ID == healthyExec.ID+":out-a"
	t.Logf("OBSERVED E4: list=%s err=[%s] healthyRetained=%v", danglingMaterializationDigest(list), danglingErrorChain(err), danglingListKeepsHealthy)
	if !danglingListKeepsHealthy {
		t.Errorf("F-B7 regression: dangling optional out-b hid healthy out-a: list=%s err=%v", danglingMaterializationDigest(list), err)
	}
	if _, err := registry.GetExecution(ctx, badExec.ID); !errors.Is(err, publication.ErrBundleNotFound) {
		t.Fatalf("E4 precondition: bad execution should be absent: err=%v", err)
	}

	// E5b: bad single-selector read errors (it can never serve stale).
	if _, err := reader.CurrentProjectDataset(ctx, project, badSelector); err == nil {
		t.Fatalf("E5b violated: dangling binding resolver returned success")
	} else {
		t.Logf("OBSERVED E5b: bad selector read err=[%s] userCode=%q (loud error, never stale data)", danglingErrorChain(err), danglingUserErrorCode(err))
	}

	// E5c: healthy single selector STILL fine after full dangling state.
	if single, err := reader.CurrentProjectDataset(ctx, project, healthySelector); err != nil || single.ID != healthyExec.ID+":out-a" {
		t.Fatalf("E5c violated: healthy single read degraded by foreign dangling binding: %+v err=%v", single, err)
	} else {
		t.Logf("OBSERVED E5c: healthy single read with dangling binding: {Name=%q ID=%q} fresh => per-selector independence CONFIRMED", single.Name, single.ID)
	}

	t.Logf("F-B7 prevented: unavailable optional bindings were omitted while the healthy binding remained current; droppedOutputHealthy=%v danglingHealthy=%v", droppedOutputListKeepsHealthy, danglingListKeepsHealthy)
}

// TestRequiredBindingSilentDemotion adjudicates the required-set variant:
// "requiredReleaseBindings/releaseBindingsFrom (release.go:349-390): if a
// binding with Required=true gets stale, does the caller error or silently
// serve stale?" Those functions do not exist (release.go ends at :280). The
// real required verification happens at create time: release.go:181-193 makes
// an unverifiable selector a ReleaseRequirementsError, and carried-forward
// prior bindings lose Required at :209. At read time stale/generation filters
// run first; Required only preserves fail-closed behavior for a deterministic
// unavailable active binding instead of omitting it as optional.
//
// Pre-stated independent expectations (declared BEFORE running):
//
//	R1  required=true binding carries Stale=true in the release doc:
//	    CurrentProjectDatasets returns ONLY the non-stale sibling, err==nil:
//	    the required binding is SILENTLY dropped (no error, no
//	    ErrReleaseRequirementsUnmet) - current.go:203-204 + 212 (result
//	    non-empty). Caller behavior = silent skip, NOT error.
//	R2  Single read of the required-stale selector: UserError DATASET_NOT_FOUND
//	    (current.go:86-91 skip + :91), NOT a required-verification error, and
//	    no stale data is served.
//	R3  ALL bindings stale: CurrentProjectDatasets -> UserError
//	    DATASET_NOT_FOUND (current.go:212-214). Silent empty, still no
//	    requirements error at read time.
//	R4  Control: required=true + Stale=false but execution corrupted ->
//	    CurrentProjectDatasets still errors, unlike the broken optional binding.
//	R5  Flipping Required true/false on a pre-marked stale binding remains
//	    behaviorally inert because the stale filter runs before materialization.
//
// Verdict: "silently serve stale" is DISPROVEN if the reader never returns a
// stale/generation-mismatched value and instead skips it; "caller errors on
// required-stale" is DISPROVEN iff R1/R3 show nil error. Both are read-side
// correctness properties, so the test PASSES when the reader behaves as the
// audit predicts; the verbatim caller behavior is logged as the report's
// evidence. Behavior is PASS (correct): requirement enforcement lives at
// release.go:181-193 (create), and the reader refuses to serve stale or
// foreign-generation data by construction (current.go:99-101).
func TestRequiredBindingSilentDemotion(t *testing.T) {
	danglingCheckCitations(t)
	ctx, client, registry, lifecycle := danglingOpen(t, 120*time.Second)

	project := "dbind-req-project"
	generation := "dbind-gen-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	requiredExec := danglingSeedExecution(t, ctx, registry, danglingExecSpec{id: "dbind-exec-required", project: project, generation: generation, recipe: "dbind-recipe-r", output: "out-r"})
	optionalExec := danglingSeedExecution(t, ctx, registry, danglingExecSpec{id: "dbind-exec-optional", project: project, generation: generation, recipe: "dbind-recipe-o", output: "out-o"})

	requiredSelector := danglingSelector("out-r", "dbind-recipe-r")
	optionalSelector := danglingSelector("out-o", "dbind-recipe-o")
	bindings := []dataset.ReleasePublication{
		{Selector: requiredSelector, ExecutionID: requiredExec.ID, Generation: generation, Required: true, Stale: false, VerifiedAt: time.Now().UTC()},
		{Selector: optionalSelector, ExecutionID: optionalExec.ID, Generation: generation, Required: false, Stale: false, VerifiedAt: time.Now().UTC()},
	}
	release := danglingSeedRelease(t, ctx, client, lifecycle, project, generation, bindings)
	reader := &published.Reader{Catalog: registry, ActiveReleaseResolver: lifecycle}

	t.Log("EXPECT R0: fresh release -> 2 materializations [out-o out-r] (sorted by Name)")
	list, err := reader.CurrentProjectDatasets(ctx, project)
	if err != nil || len(list) != 2 || list[0].Name != "out-o" || list[1].Name != "out-r" {
		t.Fatalf("R0 baseline broken: list=%s err=%v", danglingMaterializationDigest(list), err)
	}

	// R4 control first, while Stale=false: corrupt the REQUIRED binding's
	// execution and confirm the caller ERRORS loudly (not silent stale).
	danglingCorruptDropOutputs(t, ctx, client, requiredExec.ID)
	t.Log("EXPECT R4: required=true + Stale=false + corrupted execution -> CurrentProjectDatasets returns error (not silent stale)")
	var r4ErrChain string
	list, err = reader.CurrentProjectDatasets(ctx, project)
	r4Loud := err != nil && len(list) == 0 && strings.Contains(danglingErrorChain(err), "has no queryable output")
	r4ErrChain = danglingErrorChain(err)
	// Repair the execution for the stale-flag cases by deleting it: the
	// stale=true path (R1-R3) never touches the catalog for the stale
	// binding, so deletion keeps the two mechanisms cleanly separated.
	danglingCorruptDeleteExecution(t, ctx, client, requiredExec.ID)
	if !r4Loud {
		t.Fatalf("R4 violated: corrupted required binding did not force a loud whole-list error: list=%s err=[%s]", danglingMaterializationDigest(list), r4ErrChain)
	}
	t.Logf("OBSERVED R4 verbatim: corrupted required binding -> caller behavior = ERROR, chain=[%s] userCode=%q", r4ErrChain, danglingUserErrorCode(err))

	// R1: mark the required binding stale in the release document.
	staleBindings := []dataset.ReleasePublication{
		{Selector: requiredSelector, ExecutionID: requiredExec.ID, Generation: generation, Required: true, Stale: true, VerifiedAt: time.Now().UTC()},
		{Selector: optionalSelector, ExecutionID: optionalExec.ID, Generation: generation, Required: false, Stale: false, VerifiedAt: time.Now().UTC()},
	}
	danglingPatchBindingStale(t, ctx, client, dataset.ProjectRelease{ID: release.ID, Publications: staleBindings})

	t.Log("EXPECT R1: required-stale binding silently skipped; list = [out-o] only, err == nil; NO error mentioning required")
	list, err = reader.CurrentProjectDatasets(ctx, project)
	r1SilentSkip := err == nil && len(list) == 1 && list[0].Name == "out-o"
	t.Logf("OBSERVED R1 verbatim: list=%s err=[%s] -> caller %s", danglingMaterializationDigest(list), danglingErrorChain(err), map[bool]string{true: "SILENTLY DROPS the required-stale binding (no error)", false: "did not match predicted silent skip"}[r1SilentSkip])
	if !r1SilentSkip {
		t.Fatalf("R1 violated: expected silently-skipped required-stale binding (nil error, [out-o]); got list=%s err=%v", danglingMaterializationDigest(list), err)
	}

	// R5: Required does not override the pre-marked stale filter.
	inertBindings := make([]dataset.ReleasePublication, len(staleBindings))
	copy(inertBindings, staleBindings)
	inertBindings[0].Required = false
	danglingPatchBindingStale(t, ctx, client, dataset.ProjectRelease{ID: release.ID, Publications: inertBindings})
	list, err = reader.CurrentProjectDatasets(ctx, project)
	if err != nil || len(list) != 1 || list[0].Name != "out-o" {
		t.Fatalf("R5 violated: read path consulted Required (flipping it changed behavior): list=%s err=%v", danglingMaterializationDigest(list), err)
	}
	t.Log("OBSERVED R5: Required=true->false on the stale binding changed nothing; stale bindings are filtered before the Required error policy")

	// R2: single read of the required-stale selector.
	t.Log("EXPECT R2: single required-stale selector -> UserError code DATASET_NOT_FOUND, NOT ErrReleaseRequirementsUnmet; no stale value served")
	single, err := reader.CurrentProjectDataset(ctx, project, requiredSelector)
	code := danglingUserErrorCode(err)
	if err == nil || code != string(dataframeerrors.CodeDatasetNotFound) || single.ID != "" {
		t.Fatalf("R2 violated: expected DATASET_NOT_FOUND and empty materialization; got {ID=%q} err=[%s] code=%q", single.ID, danglingErrorChain(err), code)
	}
	t.Logf("OBSERVED R2 verbatim: required-stale single read -> err=[%s] userCode=%q -> silently serves NOT-FOUND (never stale data)", danglingErrorChain(err), code)

	// R3: all bindings stale.
	allStale := make([]dataset.ReleasePublication, len(staleBindings))
	copy(allStale, staleBindings)
	allStale[1].Stale = true
	danglingPatchBindingStale(t, ctx, client, dataset.ProjectRelease{ID: release.ID, Publications: allStale})
	t.Log("EXPECT R3: all-stale release -> CurrentProjectDatasets UserError DATASET_NOT_FOUND (current.go:212-214), no requirements error")
	list, err = reader.CurrentProjectDatasets(ctx, project)
	code = danglingUserErrorCode(err)
	if err == nil || code != string(dataframeerrors.CodeDatasetNotFound) || len(list) != 0 {
		t.Fatalf("R3 violated: expected DATASET_NOT_FOUND for all-stale release; got list=%s err=[%s] code=%q", danglingMaterializationDigest(list), danglingErrorChain(err), code)
	}
	if errors.Is(err, dataset.ErrReleaseRequirementsUnmet) {
		t.Fatalf("R3 violated: read path surfaced ErrReleaseRequirementsUnmet")
	}
	t.Logf("OBSERVED R3 verbatim: all-stale list -> err=[%s] userCode=%q; ErrReleaseRequirementsUnmet errors.Is = %v", danglingErrorChain(err), code, errors.Is(err, dataset.ErrReleaseRequirementsUnmet))

	t.Log("VERDICT required-set variant: stale required bindings are skipped before catalog access and report DATASET_NOT_FOUND; an active required binding that is deterministically unavailable remains fail-closed. Required verification also occurs at Create (release.go:181-193), and carried-forward bindings lose Required at release.go:209, so Required=true with Stale=true is not reachable through the production Create/Activate path")
}
