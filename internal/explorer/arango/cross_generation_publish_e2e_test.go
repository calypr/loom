package arango

// VERIFICATION-ONLY artifact owned by the F-B13 cross-generation publish audit.
// It never mutates product code. It creates a fully owned ArangoDB database
// (loom_verify_cgp_<uuid>) through the production store.Open path and drops it
// with an HTTP DELETE in t.Cleanup, logging removal.
//
// F-B13 regression: publishing a retained OLD-generation Explorer receipt
// after a NEWER generation has been activated must not repoint the project's
// active_generation to the old generation. An explicit generation activation
// first selects the rollback target and remains valid.
// The publish preflight does not compare receipt.SourceGeneration to the active
// generation pointer. The release CAS now supplies that transactional check.
//
//   - internal/explorer/lifecycle/publish.go:33 only checks the receipt's
//     capability snapshot is self-consistent with receipt.SourceGeneration,
//     never the active pointer.
//   - internal/explorer/lifecycle/publish.go:58 validates only that
//     receipt.SourceGeneration is a staged manifest (ValidateReleaseGeneration
//     -> ReadManifest(...).IsStaged), never that it is the active generation.
//   - internal/dataset/arango/release_store.go:153-179 (activateReleaseAQL) is
//     reached by datasetarango.CompareAndSwapPublishRelease at
//     internal/explorer/arango/publication.go:57, the exact call the Explorer
//     publish binding makes. It resolves `manifest` from the RELEASE's own
//     generation (manifest_key from release.Generation, release_store.go:80)
//     and patches the shared active_generation pointer only when its project,
//     generation and manifest key still match the selected manifest.
//
// REACHABILITY / LAYER HONESTY. The full lifecycle.Service.Publish and the
// Explorer capability resolver live in internal/server (compileExplorerReceipt,
// explorerCapabilityResolver). import "internal/server" would form an import
// cycle for this package (internal/server already imports internal/explorer/
// arango), so the compile step and the receipt-authority gate are NOT driven;
// they are replicated at the level the hypothesis actually needs. Store.
// PublishAuthoring (publication.go:13) IS reachable in this package, but it
// requires a production-valid explorer.CompilationReceipt that can only be
// produced by the internal/server compile stack, and it merely wraps the same
// datasetarango.CompareAndSwapPublishRelease call at publication.go:57 plus
// unrelated explorer-revision bookkeeping. So this test drives the real
// production binding surface that owns the bug: dataset.ReleaseService.Create
// (the publish-path preflight at release.go:130-229) plus the real
// datasetarango.CompareAndSwapPublishRelease (the CAS publication.go:57
// invokes), against real ArangoDB. This is the release-CAS / publish-binding
// layer, labelled as such in the verdict. The ResolveForExecution vs
// ResolveForCompilation asymmetry lives in the server file; the equivalent
// asymmetry that repoints the pointer here is that the binding path resolves
// the manifest for release.Generation instead of the active generation.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dataframepublication "github.com/calypr/loom/internal/dataframe/publication"
	publicationarango "github.com/calypr/loom/internal/dataframe/publication/arango"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataset"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

const cgpDatabasePrefix = "loom_verify_cgp_"

// cgpCitation pins the load-bearing source lines the verdict relies on. A drift
// fails the run so a stale conclusion cannot silently survive a refactor.
type cgpCitation struct {
	file string
	line int // 1-based
	want string
}

var cgpCitations = []cgpCitation{
	{"internal/explorer/lifecycle/publish.go", 33, `if err != nil || strings.TrimSpace(snapshot.Identity.Generation) != strings.TrimSpace(receipt.SourceGeneration) {`},
	{"internal/explorer/lifecycle/publish.go", 58, `if err := s.config.ValidateReleaseGeneration(ctx, projectid.Legacy(receipt.Project), receipt.SourceGeneration); err != nil {`},
	{"internal/explorer/lifecycle/publish.go", 87, `release, expectedReleaseRevision, err := s.config.PrepareRelease(ctx, projectid.Legacy(receipt.Project), receipt.SourceGeneration, selectorsForBundle(receipt.Bundle))`},
	{"internal/explorer/arango/publication.go", 57, `if _, err := datasetarango.CompareAndSwapPublishRelease(txCtx, tx, release, expectedReleaseRevision); err != nil {`},
	{"internal/dataset/arango/release_store.go", 178, `LET generationPointer = DOCUMENT(@@lifecycle_collection, @active_key)`},
	{"internal/dataset/arango/release_store.go", 192, `  AND generationPointer.dataset.generation == @generation`},
	{"internal/dataset/arango/release_store.go", 202, `  {document: generationPointer, patch: {dataset: manifest.dataset, manifestKey: manifest._key, releaseId: @release_id, releaseRevision: selectedRevision}}`},
	{"internal/server/explorer_capability.go", 177, `func (r *explorerCapabilityResolver) ResolveForExecution(ctx context.Context, project, token string) (lifecycle.AuthorizedCapability, error) {`},
	{"internal/server/explorer_capability.go", 208, `func (r *explorerCapabilityResolver) ResolveForCompilation(ctx context.Context, project, token string) (lifecycle.AuthorizedCapability, error) {`},
	{"internal/server/explorer_capability.go", 218, `if authorized.Snapshot.Identity.Generation != manifest.Dataset.Generation {`},
}

// cgpAbsentIdentifiers prove the Explorer publish preflight does not substitute
// its own active-generation lookup for the transactional store guard.
type cgpAbsentIdentifier struct {
	file       string
	identifier string
}

var cgpAbsentIdentifiers = []cgpAbsentIdentifier{
	{"internal/explorer/lifecycle/publish.go", "ResolveActive"},
	{"internal/explorer/lifecycle/publish.go", "activeGeneration"},
	{"internal/explorer/lifecycle/publish.go", "Dataset.Generation"},
}

func cgpRepoRoot(t *testing.T) string {
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

func cgpCheckCitations(t *testing.T) {
	t.Helper()
	root := cgpRepoRoot(t)
	contents := map[string][]string{}
	for _, c := range cgpCitations {
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
			t.Fatalf("citation drift: %s has %d lines, want :%d == %q", c.file, len(lines), c.line, c.want)
		}
		if got := strings.TrimSpace(lines[c.line-1]); got != strings.TrimSpace(c.want) {
			t.Fatalf("citation drift: %s:%d = %q, want %q", c.file, c.line, got, c.want)
		}
	}
	for _, a := range cgpAbsentIdentifiers {
		raw, err := os.ReadFile(filepath.Join(root, a.file))
		if err != nil {
			t.Fatalf("citation source %s unreadable: %v", a.file, err)
		}
		if strings.Contains(string(raw), a.identifier) {
			t.Fatalf("guard identifier %q unexpectedly present in %s", a.identifier, a.file)
		}
	}
}

// cgpVerifier mirrors the production publicationVerificationStore
// (internal/server/release_verifier.go:18-34) exactly, over the real ArangoDB
// publication Registry, so the ReleaseService binding gate consumes a genuine
// PUBLISHED/queryable execution rather than a hand-made success.
type cgpVerifier struct {
	executions dataframepublication.ExactExecutionCatalog
}

func (s cgpVerifier) VerifyPublication(ctx context.Context, project, generation string, selector dataset.DataframeSelector) (dataset.PublicationVerification, error) {
	if s.executions == nil {
		return dataset.PublicationVerification{}, fmt.Errorf("publication verification store is unavailable")
	}
	execution, output, err := s.executions.FindExecutionBySelector(ctx, project, generation, selector)
	if err != nil {
		return dataset.PublicationVerification{}, err
	}
	verifiedAt := execution.UpdatedAt
	if output.VerifiedAt != nil {
		verifiedAt = *output.VerifiedAt
	}
	return dataset.PublicationVerification{
		Selector: selector, ExecutionID: execution.ID, Generation: execution.DatasetGeneration,
		State: string(execution.State.Canonical()), Queryable: execution.State.Successful() && output.Queryable(),
		VerifiedAt: verifiedAt, PhysicalTable: output.PhysicalTable,
	}, nil
}

var _ dataset.PublicationVerifier = cgpVerifier{}

// cgpOpen is the env-gated owned-database harness (skip-clean when
// LOOM_TEST_ARANGO_URL is unset). It bootstraps only the dataset lifecycle and
// dataframe-publication collections, which is all the F-B13 binding path needs.
func cgpOpen(t *testing.T, budget time.Duration) (context.Context, *store.Client, *datasetarango.Store, *publicationarango.Registry) {
	t.Helper()
	endpoint := os.Getenv("LOOM_TEST_ARANGO_URL")
	if endpoint == "" {
		t.Skip("set owned LOOM_TEST_ARANGO_URL")
	}
	name := cgpDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(name, cgpDatabasePrefix) {
		t.Fatalf("verification database %q escapes the %s prefix", name, cgpDatabasePrefix)
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	t.Cleanup(cancel)
	client, err := store.Open(ctx, endpoint, name)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Logf("CGP_DATABASE=%s", name)
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
		t.Logf("CGP_DATABASE_REMOVED=%s", name)
	})
	if err := client.Bootstrap(ctx, datasetarango.BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap dataset lifecycle collections: %v", err)
	}
	if err := client.Bootstrap(ctx, publicationarango.BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap dataframe publication collections: %v", err)
	}
	lifecycle, err := datasetarango.New(client)
	if err != nil {
		t.Fatalf("new dataset lifecycle store: %v", err)
	}
	registry, err := publicationarango.New(client)
	if err != nil {
		t.Fatalf("new publication registry: %v", err)
	}
	return ctx, client, lifecycle, registry
}

// cgpLoadAndActivate drives the real dataset lifecycle Store: create a LOADING
// manifest, transition it to STAGED (graph-loaded), and Activate it so the
// shared active_generation pointer moves to this generation. Returns the staged
// manifest. Mirrors the dataset store tests' load/activate surface.
func cgpLoadAndActivate(t *testing.T, ctx context.Context, lifecycle *datasetarango.Store, project, generation, digest string) dataset.Manifest {
	t.Helper()
	ref, err := dataset.NewRef(project, generation)
	if err != nil {
		t.Fatalf("new ref %s/%s: %v", project, generation, err)
	}
	schema, err := dataset.NewSchemaSnapshot("urn:cgp-fb13-"+generation, "", digest, []string{"Patient"})
	if err != nil {
		t.Fatalf("schema snapshot %s: %v", generation, err)
	}
	manifest, err := dataset.NewManifest(ref, schema)
	if err != nil {
		t.Fatalf("new manifest %s: %v", generation, err)
	}
	if manifest.State != dataset.StateLoading {
		t.Fatalf("manifest %s not LOADING on construct: %s", generation, manifest.State)
	}
	if _, err := lifecycle.CreateManifest(ctx, manifest); err != nil {
		t.Fatalf("CreateManifest %s: %v", generation, err)
	}
	staged, err := lifecycle.TransitionManifest(ctx, manifest, dataset.StateStaged)
	if err != nil {
		t.Fatalf("TransitionManifest %s -> STAGED: %v", generation, err)
	}
	if !staged.IsStaged() {
		t.Fatalf("manifest %s not staged after transition: %s", generation, staged.State)
	}
	if err := lifecycle.Activate(ctx, staged); err != nil {
		t.Fatalf("Activate %s: %v", generation, err)
	}
	return staged
}

// cgpSeedPublished writes one PUBLISHED, queryable execution through the
// production lease + SaveExecution path and names it via CompareAndSwapPointer,
// mirroring publication_rows_integration_test.go / dangling_binding_e2e_test.go.
// recipe/version/output are the selector identity that a generation releases;
// unique isolates the execution id/key/lease per generation so two generations
// publishing the SAME selector do not collide on _key.
func cgpSeedPublished(t *testing.T, ctx context.Context, registry *publicationarango.Registry, project, generation, recipe, version, output, unique string) (dataframepublication.BundleExecution, dataset.DataframeSelector) {
	t.Helper()
	id := "cgp-exec-" + unique
	key := "cgp-key-" + unique
	owner := "cgp-publisher-" + unique
	if claimed, err := registry.AcquireBundleLease(ctx, key, owner, time.Now().UTC().Add(5*time.Minute)); err != nil || !claimed {
		t.Fatalf("acquire publication lease for %s: claimed=%v err=%v", recipe, claimed, err)
	}
	stamp := time.Now().UTC().Truncate(time.Millisecond)
	selector := dataset.DataframeSelector{Recipe: recipe, TranslationVersion: version, Output: output}
	execution := dataframepublication.BundleExecution{
		ID:  id,
		Key: key,
		BundleIdentity: dataframepublication.BundleIdentity{
			Name: recipe, TranslationVersion: version, OutputName: output,
			Project: project, DatasetGeneration: generation,
			RecipeDigest: "sha256:" + recipe + "-r", SchemaDigest: "sha256:" + recipe + "-s",
			ReceiptID: "cgp-receipt-" + recipe, ScopeDigest: "cgp-scope-" + recipe, EngineVersion: "cgp-engine",
		},
		State: dataframepublication.BundlePublished,
		Outputs: []dataframepublication.BundleOutputRecord{{
			Name: output, PhysicalTable: "ch_" + strings.ReplaceAll(recipe, "-", "_"),
			Selector: selector, RowCount: 7, ByteCount: 700,
			State: dataframepublication.BundlePublished, VerifiedAt: &stamp,
		}},
		CreatedAt:   stamp.Add(-time.Minute),
		UpdatedAt:   stamp,
		ReadyAt:     &stamp,
		PublishedAt: &stamp,
		OwnerID:     owner,
	}
	if err := registry.SaveExecution(ctx, execution, owner); err != nil {
		t.Fatalf("SaveExecution %s: %v", recipe, err)
	}
	if err := registry.CompareAndSwapPointer(ctx, execution.PointerName(), "", id); err != nil {
		t.Fatalf("CompareAndSwapPointer %s: %v", recipe, err)
	}
	return execution, selector
}

// cgpActiveGenerationPointer reads the shared active_generation pointer doc
// directly from the lifecycle collection by role, not by a replicated key, so
// the OBSERVED pointer cannot be biased by a key-deribration guess.
func cgpActiveGenerationPointer(t *testing.T, ctx context.Context, client *store.Client, project string) map[string]any {
	t.Helper()
	var pointer map[string]any
	err := client.QueryRows(ctx, `FOR d IN @@c FILTER d.recordType == "active_generation" AND d.project == @project RETURN d`, 1,
		map[string]interface{}{"@c": datasetarango.LifecycleCollection, "project": project},
		func(row map[string]any) error {
			pointer = row
			return nil
		})
	if err != nil {
		t.Fatalf("read active_generation pointer: %v", err)
	}
	if pointer == nil {
		t.Fatalf("no active_generation pointer document for %s", project)
	}
	return pointer
}

func cgpPointerGeneration(pointer map[string]any) string {
	datasetValue, _ := pointer["dataset"].(map[string]any)
	generation, _ := datasetValue["generation"].(string)
	return generation
}

func cgpPointerJSON(pointer map[string]any) string {
	raw, err := json.Marshal(pointer)
	if err != nil {
		return fmt.Sprintf("%v", pointer)
	}
	return string(raw)
}

func cgpMaterializationDigest(values []published.Materialization) string {
	summaries := make([]string, 0, len(values))
	for _, value := range values {
		summaries = append(summaries, fmt.Sprintf("{Name=%q ID=%q Generation=%q State=%q}", value.Name, value.ID, value.DatasetGeneration, value.State))
	}
	return "[" + strings.Join(summaries, " ") + "]"
}

// TestStaleReceiptPublishDowngradesActiveGenerationAgainstArango is the F-B13
// finding-assert at the release-CAS / publish-binding layer.
func TestStaleReceiptPublishDowngradesActiveGenerationAgainstArango(t *testing.T) {
	ctx, client, lifecycle, registry := cgpOpen(t, 180*time.Second)
	cgpCheckCitations(t)

	const project = "cgp-fb13-project"
	g1 := "cgp-fb13-gen-1"
	g2 := "cgp-fb13-gen-2"
	const version = "v1"
	const recipe = "fb13-recipe"
	const output = "out"

	// Step 2: PUBLISHED, queryable execution + pointer for BOTH generations.
	// Both generations release the same selector identity (recipe/version/output);
	// only the generation and the execution id/lease differ. The shared selector
	// lets one ReleaseService.Required set resolve in either generation, exactly
	// as publication_rows_integration_test.go binds recipe+translationVersion+output.
	_, selectorG1 := cgpSeedPublished(t, ctx, registry, project, g1, recipe, version, output, "g1")
	cgpSeedPublished(t, ctx, registry, project, g2, recipe, version, output, "g2")
	t.Logf("OBSERVED publications: G1 selector=%+v (exec id cgp-exec-g1) G2 selector identical recipe (exec id cgp-exec-g2)", selectorG1)

	// Step 1: two loaded+activated generations G1 then G2 (production lifecycle
	// store load/activate). Deterministic ordering, single writer.
	cgpLoadAndActivate(t, ctx, lifecycle, project, g1, strings.Repeat("a", 64))
	cgpLoadAndActivate(t, ctx, lifecycle, project, g2, strings.Repeat("b", 64))

	// Step 4 (baseline, PRE-STATED): active generation reads == G2.
	t.Log(`PRE-STATED EXPECT: baseline active generation reads == G2 (activation is last-writer-wins; G2 activated second).`)
	readerManifest := &published.Reader{Catalog: registry, ActiveManifestResolver: lifecycle}
	readerRelease := &published.Reader{Catalog: registry, ActiveReleaseResolver: lifecycle}

	// The general release activation is allowed to select a generation. Create
	// G1 first, then switch to G2 through ReleaseService.Activate to prove the
	// same CAS remains available for legitimate generation changes.
	releaseService := dataset.ReleaseService{
		Manifests: lifecycle, Releases: lifecycle,
		Verifier: cgpVerifier{executions: registry},
		Required: []dataset.DataframeSelector{selectorG1},
	}
	g1Active, err := releaseService.Activate(ctx, dataset.ActivationRequest{Project: project, Generation: g1, ExpectedRevision: 0})
	if err != nil {
		t.Fatalf("ReleaseService.Activate(G1): %v", err)
	}
	g2Active, err := releaseService.Activate(ctx, dataset.ActivationRequest{Project: project, Generation: g2, ExpectedRevision: g1Active.Revision})
	if err != nil || g2Active.Revision != g1Active.Revision+1 {
		t.Fatalf("ReleaseService.Activate(G2): active=%+v err=%v", g2Active, err)
	}

	baselinePointer := cgpActiveGenerationPointer(t, ctx, client, project)
	baselineGeneration := cgpPointerGeneration(baselinePointer)
	baselineManifest, manifestErr := dataset.ResolveActive(ctx, lifecycle, project)
	baselineActive, releaseErr := lifecycle.ReadActiveRelease(ctx, project)
	manifestList, manifestListErr := readerManifest.CurrentProjectDatasets(ctx, project)
	releaseList, releaseListErr := readerRelease.CurrentProjectDatasets(ctx, project)
	t.Logf("OBSERVED baseline pointer.doc=%s", cgpPointerJSON(baselinePointer))
	t.Logf("OBSERVED baseline generation-pointer=%q ResolveActiveManifest(generation=%q,err=%v) ReadActiveRelease(generation=%q,err=%v)", baselineGeneration, baselineManifest.Dataset.Generation, manifestErr, baselineActive.Release.Generation, releaseErr)
	t.Logf("OBSERVED baseline CurrentProjectDatasets manifest-path=%s err=%v", cgpMaterializationDigest(manifestList), manifestListErr)
	t.Logf("OBSERVED baseline CurrentProjectDatasets release-path=%s err=%v", cgpMaterializationDigest(releaseList), releaseListErr)
	if baselineGeneration != g2 || manifestErr != nil || baselineManifest.Dataset.Generation != g2 || releaseErr != nil || baselineActive.Release.Generation != g2 {
		t.Fatalf("BASELINE PRECONDITION BROKEN: active generation should read G2=%s; pointer=%q ResolveActive=%q err=%v release=%q err=%v", g2, baselineGeneration, baselineManifest.Dataset.Generation, manifestErr, baselineActive.Release.Generation, releaseErr)
	}
	if len(manifestList) != 1 || manifestList[0].DatasetGeneration != g2 || len(releaseList) != 1 || releaseList[0].DatasetGeneration != g2 {
		t.Fatalf("BASELINE PRECONDITION BROKEN: discovery must report only G2 generation; manifest=%s release=%s", cgpMaterializationDigest(manifestList), cgpMaterializationDigest(releaseList))
	}

	// Step 5b: the F-B13 action. Publish the retained G1 receipt post-G2
	// activation through the same strict current-generation CAS used by the
	// production Explorer PublishAuthoring transaction. The current release
	// revision is read dynamically so the revision CAS itself is not stale.
	current, err := lifecycle.ReadActiveRelease(ctx, project)
	if err != nil {
		t.Fatalf("read current active release before G1 publish: %v", err)
	}
	g1Release, err := releaseService.Create(ctx, dataset.ActivationRequest{Project: project, Generation: g1})
	if err != nil {
		t.Fatalf("ReleaseService.Create(G1): %v", err)
	}
	if g1Release.ID != g1Active.Release.ID {
		t.Fatalf("G1 candidate was not retained before G2 activation: initial=%q later=%q", g1Active.Release.ID, g1Release.ID)
	}
	t.Logf("OBSERVED G1 release candidate: id=%s generation=%s bindings=%d (Create preflight REJECTED nothing though G2 is active)", g1Release.ID, g1Release.Generation, len(g1Release.Publications))

	// G1 was an active release before G2 and its immutable candidate is retained.
	// Publishing that retained candidate must fail the current-generation CAS.
	// The general ReleaseService activation remains the explicit generation
	// selection path and must still permit rollback to G1.
	t.Log("PRE-STATED EXPECT: the retained G1 candidate conflicts on the publish CAS while G2 is active; the general ReleaseService activation can explicitly roll back to G1.")

	casActive, casErr := datasetarango.CompareAndSwapPublishRelease(ctx, client, g1Release, current.Revision)
	observedPointer := cgpActiveGenerationPointer(t, ctx, client, project)
	observedGeneration := cgpPointerGeneration(observedPointer)
	postManifest, postManifestErr := dataset.ResolveActive(ctx, lifecycle, project)
	postActive, postReleaseErr := lifecycle.ReadActiveRelease(ctx, project)
	postManifestList, postManifestListErr := readerManifest.CurrentProjectDatasets(ctx, project)
	postReleaseList, postReleaseListErr := readerRelease.CurrentProjectDatasets(ctx, project)
	t.Logf("OBSERVED post-CAS: CompareAndSwap err=%v returnedRevision=%v returnedGeneration=%q", casErr, casActive.Revision, casActive.Release.Generation)
	t.Logf("OBSERVED post-CAS pointer.doc=%s", cgpPointerJSON(observedPointer))
	t.Logf("OBSERVED post-CAS generation-pointer=%q ResolveActiveManifest(generation=%q,err=%v) ReadActiveRelease(generation=%q,err=%v)", observedGeneration, postManifest.Dataset.Generation, postManifestErr, postActive.Release.Generation, postReleaseErr)
	t.Logf("OBSERVED post-CAS CurrentProjectDatasets manifest-path=%s err=%v", cgpMaterializationDigest(postManifestList), postManifestListErr)
	t.Logf("OBSERVED post-CAS CurrentProjectDatasets release-path=%s err=%v", cgpMaterializationDigest(postReleaseList), postReleaseListErr)

	// Step 6 verdict, entirely from OBSERVED values.
	silentlyRepointed := casErr == nil && observedGeneration == g1 && postManifestErr == nil && postManifest.Dataset.Generation == g1 && postActive.Release.Generation == g1
	prevented := errors.Is(casErr, dataset.ErrReleaseActivationConflict) && observedGeneration == g2 && postManifestErr == nil && postManifest.Dataset.Generation == g2 && postReleaseErr == nil && postActive.Release.Generation == g2

	switch {
	case silentlyRepointed:
		t.Fatalf("FAILED-ON-PURPOSE F-B13 CONFIRMED against real Arango: after publishing retained G1 receipt post-G2 activation, activeGeneration=%q, expected G2=%q, err=nil; pointer doc: %s. Layer: strict publication release CAS (datasetarango.CompareAndSwapPublishRelease, the CAS publication.go:57 invokes) allowed a mismatched active_generation pointer.", observedGeneration, g2, cgpPointerJSON(observedPointer))
	case prevented:
		t.Logf("F-B13 PREVENTED: the retained G1 release returned ErrReleaseActivationConflict while the active manifest, release and generation pointer remained G2; err=%v.", casErr)
		t.Log("OBSERVED PREVENTED confirms stale-receipt generation stability.")
	default:
		t.Fatalf("INCONCLUSIVE F-B13 adjudication: stale G1 CAS did not return a generation conflict while G2 remained active. casErr=%v observed generation-pointer=%q (want %q) ResolveActive=%q err=%v", casErr, observedGeneration, g2, postManifest.Dataset.Generation, postManifestErr)
	}

	rolledBack, err := releaseService.Activate(ctx, dataset.ActivationRequest{Project: project, Generation: g1, ExpectedRevision: current.Revision})
	if err != nil {
		t.Fatalf("explicit ReleaseService rollback to G1: %v", err)
	}
	rolledBackPointer := cgpActiveGenerationPointer(t, ctx, client, project)
	rolledBackActive, err := lifecycle.ReadActiveRelease(ctx, project)
	if err != nil || cgpPointerGeneration(rolledBackPointer) != g1 || rolledBack.Release.Generation != g1 || rolledBack.Revision != current.Revision+1 || rolledBackActive.Release.Generation != g1 {
		t.Fatalf("explicit G1 rollback did not activate the matching release: pointer=%q CAS=%+v active=%+v err=%v", cgpPointerGeneration(rolledBackPointer), rolledBack, rolledBackActive, err)
	}
	t.Logf("OBSERVED explicit rollback: generation=%q release=%q revision=%d", cgpPointerGeneration(rolledBackPointer), rolledBackActive.Release.Generation, rolledBackActive.Revision)
}

func TestReleaseActivationCASConcurrentWritersAgainstArango(t *testing.T) {
	ctx, client, lifecycle, registry := cgpOpen(t, 180*time.Second)
	const project = "cgp-release-cas-race"
	const generation = "cgp-release-cas-generation"

	required, requiredSelector := cgpSeedPublished(t, ctx, registry, project, generation, "cas-required", "v1", "required", "cas-required")
	_ = required
	_, optionalA := cgpSeedPublished(t, ctx, registry, project, generation, "cas-optional-a", "v1", "optional-a", "cas-optional-a")
	_, optionalB := cgpSeedPublished(t, ctx, registry, project, generation, "cas-optional-b", "v1", "optional-b", "cas-optional-b")
	cgpLoadAndActivate(t, ctx, lifecycle, project, generation, strings.Repeat("c", 64))
	service := dataset.ReleaseService{
		Manifests: lifecycle, Releases: lifecycle,
		Verifier: cgpVerifier{executions: registry},
		Required: []dataset.DataframeSelector{requiredSelector},
	}
	initial, err := service.Create(ctx, dataset.ActivationRequest{Project: project, Generation: generation})
	if err != nil {
		t.Fatalf("create initial release: %v", err)
	}
	active, err := lifecycle.CompareAndSwapActivateRelease(ctx, initial, 0)
	if err != nil || active.Revision != 1 {
		t.Fatalf("activate initial release = %+v, %v", active, err)
	}
	candidateA, err := service.Create(ctx, dataset.ActivationRequest{Project: project, Generation: generation, OptionalSelectors: []dataset.DataframeSelector{optionalA}})
	if err != nil {
		t.Fatalf("create candidate A: %v", err)
	}
	candidateB, err := service.Create(ctx, dataset.ActivationRequest{Project: project, Generation: generation, OptionalSelectors: []dataset.DataframeSelector{optionalB}})
	if err != nil {
		t.Fatalf("create candidate B: %v", err)
	}
	if candidateA.ID == candidateB.ID {
		t.Fatalf("competing release candidates share ID %q", candidateA.ID)
	}

	type result struct {
		active dataset.ActiveRelease
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for _, candidate := range []dataset.ProjectRelease{candidateA, candidateB} {
		candidate := candidate
		go func() {
			<-start
			updated, err := datasetarango.CompareAndSwapPublishRelease(ctx, client, candidate, active.Revision)
			results <- result{active: updated, err: err}
		}()
	}
	close(start)
	var winners, conflicts int
	for range 2 {
		outcome := <-results
		switch {
		case outcome.err == nil:
			winners++
		case errors.Is(outcome.err, dataset.ErrReleaseActivationConflict):
			conflicts++
		default:
			t.Fatalf("concurrent release CAS returned unexpected error: %v", outcome.err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("same-revision writers: winners=%d conflicts=%d, want one each", winners, conflicts)
	}
	final, err := lifecycle.ReadActiveRelease(ctx, project)
	if err != nil || final.Revision != 2 || (final.Release.ID != candidateA.ID && final.Release.ID != candidateB.ID) {
		t.Fatalf("final active release = %+v, %v; want exactly one revision-2 candidate", final, err)
	}
	if pointer := cgpActiveGenerationPointer(t, ctx, client, project); cgpPointerGeneration(pointer) != generation {
		t.Fatalf("competing release CAS moved active generation: %q", cgpPointerGeneration(pointer))
	}
}
