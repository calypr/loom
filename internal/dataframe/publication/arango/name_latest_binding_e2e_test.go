package arango

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataset"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/testkit/arangotest"
	"github.com/google/uuid"
)

// These opt-in tests reproduce the registry-level discriminator gap behind
// candidate F-B6 against a real ArangoDB in a fully owned database:
//
//	Registry.FindExecutionBySelector (bundle_registry.go) filters only
//	project/generation/name/translationVersion/output plus state PUBLISHED|READY
//	and returns publishedAt-DESC LIMIT 1. It never binds Key, RecipeDigest,
//	SchemaDigest, or ReceiptID, so two PUBLISHED executions for the same
//	selector are indistinguishable to the verifier path
//	(internal/server/release_verifier.go -> dataset/release.go),
//	which echoes whatever execution the finder returned.
//
// Databases use the loom_verify_nlb_ prefix so this suite's residue is
// checkable independently of other verification workers.

const nameBindingDatabasePrefix = "loom_verify_nlb_"

type nameBindingExecSpec struct {
	id                 string
	key                string
	project            string
	generation         string
	recipe             string
	translationVersion string
	output             string
	recipeDigest       string
	schemaDigest       string
	receiptID          string
	publishedAt        time.Time
}

// nameBindingOpen mirrors the owned-database harness used by
// internal/explorer/arango/selections_cleanup_e2e_test.go: opt-in via
// LOOM_TEST_ARANGO_URL, uuid-suffixed name under the suite prefix, created via
// store.Open (client.go creates the database when absent), dropped by
// HTTP DELETE /_api/database/<name> in t.Cleanup.
func nameBindingOpen(t *testing.T, budget time.Duration) (context.Context, *store.Client, *Registry) {
	t.Helper()
	database := arangotest.Owned(t, nameBindingDatabasePrefix)
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	t.Cleanup(cancel)
	if err := database.Client.Bootstrap(ctx, BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap %s: %v", database.Name, err)
	}
	registry, err := New(database.Client)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	return ctx, database.Client, registry
}

// nameBindingSeedPublished writes one PUBLISHED execution through the
// production catalog path: AcquireBundleLease then SaveExecution. SaveExecution
// is lease-fenced (bundle_registry.go requires a live lease for the
// execution Key), so production lease acquisition is the honest seed, matching
// the lease-guarded flow the fake in bundle_registry_test.go asserts.
func nameBindingSeedPublished(t *testing.T, ctx context.Context, registry *Registry, spec nameBindingExecSpec) publication.BundleExecution {
	t.Helper()
	owner := "nlb-publisher-" + spec.id
	if claimed, err := registry.AcquireBundleLease(ctx, spec.key, owner, time.Now().UTC().Add(time.Minute)); err != nil || !claimed {
		t.Fatalf("acquire lease for %s: claimed=%v err=%v", spec.key, claimed, err)
	}
	publishedAt := spec.publishedAt
	createdAt := publishedAt.Add(-10 * time.Minute)
	execution := publication.BundleExecution{
		ID:  spec.id,
		Key: spec.key,
		BundleIdentity: publication.BundleIdentity{
			Name: spec.recipe, Project: spec.project, DatasetGeneration: spec.generation,
			TranslationVersion: spec.translationVersion, OutputName: spec.output,
			RecipeDigest: spec.recipeDigest, SchemaDigest: spec.schemaDigest, ReceiptID: spec.receiptID,
			ScopeDigest: "scope-" + spec.id, EngineVersion: "nlb-engine",
		},
		State: publication.BundlePublished,
		Outputs: []publication.BundleOutputRecord{{
			Name: spec.output, PhysicalTable: "ch_" + spec.id,
			Selector: publication.DataframeSelector{Recipe: spec.recipe, TranslationVersion: spec.translationVersion, Output: spec.output},
			RowCount: 7, ByteCount: 700, State: publication.BundlePublished,
		}},
		CreatedAt:   createdAt,
		UpdatedAt:   publishedAt,
		ReadyAt:     &publishedAt,
		PublishedAt: &publishedAt,
		OwnerID:     owner,
	}
	if err := registry.SaveExecution(ctx, execution, owner); err != nil {
		t.Fatalf("SaveExecution %s: %v", spec.id, err)
	}
	return execution
}

func nameBindingIdentity(exec publication.BundleExecution) string {
	publishedAt := "nil"
	if exec.PublishedAt != nil {
		publishedAt = exec.PublishedAt.UTC().Format(time.RFC3339Nano)
	}
	readyAt := "nil"
	if exec.ReadyAt != nil {
		readyAt = exec.ReadyAt.UTC().Format(time.RFC3339Nano)
	}
	outputs := make([]string, 0, len(exec.Outputs))
	for _, output := range exec.Outputs {
		outputs = append(outputs, fmt.Sprintf("{name=%q physicalTable=%q state=%q}", output.Name, output.PhysicalTable, output.State))
	}
	return fmt.Sprintf("{ID=%q Key=%q Name=%q Project=%q DatasetGeneration=%q TranslationVersion=%q RecipeDigest=%q SchemaDigest=%q ReceiptID=%q State=%q publishedAt=%s readyAt=%s updatedAt=%s outputs=%v}",
		exec.ID, exec.Key, exec.Name, exec.Project, exec.DatasetGeneration, exec.TranslationVersion,
		exec.RecipeDigest, exec.SchemaDigest, exec.ReceiptID, exec.State, publishedAt, readyAt,
		exec.UpdatedAt.UTC().Format(time.RFC3339Nano), outputs)
}

// TestFindExecutionBySelectorIgnoresRecipeIdentityAgainstArango checks the
// exact selector contract: after filtering by project, generation, recipe,
// translation version, and output, the finder returns the newest published
// execution. Key, recipe digest, schema digest, and receipt ID are not part of
// DataframeSelector.
func TestFindExecutionBySelectorIgnoresRecipeIdentityAgainstArango(t *testing.T) {
	ctx, _, registry := nameBindingOpen(t, 60*time.Second)

	project := "nlb-project"
	generation := "nlb-gen-" + uuid.NewString()
	selector := publication.DataframeSelector{Recipe: "nlb-recipe", TranslationVersion: "nlb-tv", Output: "nlb-output"}
	t1 := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 10, 30, 13, 0, 0, 0, time.UTC)

	intended := nameBindingSeedPublished(t, ctx, registry, nameBindingExecSpec{
		id: "nlb-intended", key: "nlb-key-intended", project: project, generation: generation,
		recipe: selector.Recipe, translationVersion: selector.TranslationVersion, output: selector.Output,
		recipeDigest: "sha256:intended-recipe", schemaDigest: "sha256:intended-schema",
		receiptID: "receipt-intended", publishedAt: t1,
	})
	newer := nameBindingSeedPublished(t, ctx, registry, nameBindingExecSpec{
		id: "nlb-newer", key: "nlb-key-newer", project: project, generation: generation,
		recipe: selector.Recipe, translationVersion: selector.TranslationVersion, output: selector.Output,
		recipeDigest: "sha256:newer-recipe", schemaDigest: "sha256:newer-schema",
		receiptID: "receipt-newer", publishedAt: t2,
	})
	if intended.RecipeDigest == newer.RecipeDigest || intended.SchemaDigest == newer.SchemaDigest || intended.Key == newer.Key || intended.ReceiptID == newer.ReceiptID {
		t.Fatalf("seed did not produce distinct recipe identities: intended=%s newer=%s", nameBindingIdentity(intended), nameBindingIdentity(newer))
	}

	execution, output, err := registry.FindExecutionBySelector(ctx, project, generation, selector)
	if err != nil {
		t.Fatalf("FindExecutionBySelector: %v", err)
	}
	if execution.ID != newer.ID {
		t.Fatalf("FindExecutionBySelector returned execution %q, want newest published execution %q for exact selector %+v", execution.ID, newer.ID, selector)
	}
	if execution.Project != project || execution.DatasetGeneration != generation || output.Name != selector.Output {
		t.Fatalf("FindExecutionBySelector returned identity %s and output %q, want %s/%s and output %q", nameBindingIdentity(execution), output.Name, project, generation, selector.Output)
	}
	if execution.Key != newer.Key || execution.RecipeDigest != newer.RecipeDigest || execution.SchemaDigest != newer.SchemaDigest || execution.ReceiptID != newer.ReceiptID {
		t.Fatalf("FindExecutionBySelector returned wrong latest execution identity: got %s, want %s", nameBindingIdentity(execution), nameBindingIdentity(newer))
	}
}

// TestFindExecutionBySelectorLegacyCaseAsymmetryAgainstArango checks latest
// selector ordering and canonicalization for a legacy READY execution.
func TestFindExecutionBySelectorLegacyCaseAsymmetryAgainstArango(t *testing.T) {
	ctx, client, registry := nameBindingOpen(t, 60*time.Second)

	project := "nlb-case-project"
	generation := "nlb-case-gen-" + uuid.NewString()
	selector := publication.DataframeSelector{Recipe: "nlb-case-recipe", TranslationVersion: "nlb-case-tv", Output: "nlb-case-output"}
	t1 := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)

	nameBindingSeedPublished(t, ctx, registry, nameBindingExecSpec{
		id: "nlb-case-lower", key: "nlb-case-key-lower", project: project, generation: generation,
		recipe: selector.Recipe, translationVersion: selector.TranslationVersion, output: selector.Output,
		recipeDigest: "sha256:case-lower-recipe", schemaDigest: "sha256:case-lower-schema",
		receiptID: "receipt-case-lower", publishedAt: t1,
	})

	// Persist the same lower-case JSON fields emitted by the historical Go
	// serializer, with only its legacy READY state. Canonicalization should
	// expose it as PUBLISHED and derive PublishedAt from ReadyAt.
	legacy := publication.BundleExecution{
		ID: "nlb-case-legacy", Key: "nlb-case-key-legacy",
		BundleIdentity: publication.BundleIdentity{
			Name: selector.Recipe, Project: project, DatasetGeneration: generation,
			TranslationVersion: selector.TranslationVersion,
			RecipeDigest:       "sha256:case-legacy-recipe", SchemaDigest: "sha256:case-legacy-schema",
			ReceiptID: "receipt-case-legacy", ScopeDigest: "scope-legacy", EngineVersion: "engine-legacy",
		},
		State: publication.BundleReady,
		Outputs: []publication.BundleOutputRecord{{
			Name: selector.Output, PhysicalTable: "ch_nlb_case_legacy", RowCount: 3, ByteCount: 300,
			State: publication.BundleReady,
		}},
		CreatedAt: t1, UpdatedAt: t2, ReadyAt: &t2,
	}
	canonicalLegacy := legacy.CanonicalizeLegacy()
	if canonicalLegacy.State != publication.BundlePublished || canonicalLegacy.PublishedAt == nil || !canonicalLegacy.PublishedAt.Equal(t2) || !canonicalLegacy.Outputs[0].Queryable() || canonicalLegacy.Outputs[0].Selector != selector {
		t.Fatalf("legacy codec shape does not canonicalize to a queryable published execution: %+v", canonicalLegacy)
	}
	legacyDoc, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy execution: %v", err)
	}
	var persistedLegacy map[string]interface{}
	if err := json.Unmarshal(legacyDoc, &persistedLegacy); err != nil {
		t.Fatalf("decode legacy execution: %v", err)
	}
	// Match the document key assigned by Registry.SaveExecution after JSON
	// serialization; the execution body above remains the production codec.
	persistedLegacy["_key"] = legacy.ID
	if err := client.ExecuteAQL(ctx, `INSERT @doc INTO @@collection`, map[string]interface{}{"@collection": BundleExecutionsCollection, "doc": persistedLegacy}); err != nil {
		t.Fatalf("insert legacy-shaped doc: %v", err)
	}

	execution, output, err := registry.FindExecutionBySelector(ctx, project, generation, selector)
	if err != nil {
		t.Fatalf("FindExecutionBySelector: %v", err)
	}
	if execution.ID != legacy.ID || execution.State != publication.BundlePublished || execution.PublishedAt == nil || !execution.PublishedAt.Equal(t2) || output.Name != selector.Output {
		t.Fatalf("latest exact-selector lookup returned execution %s (state=%q publishedAt=%v) and output %q; want legacy execution %q canonicalized to PUBLISHED at %s with output %q",
			nameBindingIdentity(execution), execution.State, execution.PublishedAt, output.Name, legacy.ID, t2.Format(time.RFC3339Nano), selector.Output)
	}
}

// nameBindingReplicatedVerificationStore is a line-for-line replica of
// internal/server/release_verifier.go (publicationVerificationStore).
// The original is unreachable from this package because internal/server/
// server.go imports this package, so importing server from this test would
// be an import cycle; the brief authorises replicating these ~15 lines.
type nameBindingReplicatedVerificationStore struct {
	executions publication.ExactExecutionCatalog
}

func (s nameBindingReplicatedVerificationStore) VerifyPublication(ctx context.Context, project, generation string, selector dataset.DataframeSelector) (dataset.PublicationVerification, error) {
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

type nameBindingManifestReader struct {
	manifest dataset.Manifest
}

func (r nameBindingManifestReader) ReadManifest(context.Context, dataset.Ref) (dataset.Manifest, error) {
	return r.manifest, nil
}

type nameBindingReleaseRepository struct {
	mu    sync.Mutex
	saved dataset.ProjectRelease
}

func (r *nameBindingReleaseRepository) SaveRelease(_ context.Context, release dataset.ProjectRelease) (dataset.ProjectRelease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saved = release
	return release, nil
}

func (r *nameBindingReleaseRepository) ReadActiveRelease(context.Context, string) (dataset.ActiveRelease, error) {
	return dataset.ActiveRelease{}, dataset.ErrNoActiveRelease
}

func (r *nameBindingReleaseRepository) CompareAndSwapActivateRelease(context.Context, dataset.ProjectRelease, int64) (dataset.ActiveRelease, error) {
	return dataset.ActiveRelease{}, errors.New("CompareAndSwapActivateRelease must not run during Create")
}

// TestVerifierToBindingChainEchoAgainstArango drives the seam the candidate
// says is load-bearing: the (replicated) publicationVerificationStore over the
// production registry, then production dataset.ReleaseService.Create, whose
// publicationBinding echoes verification.ExecutionID into the release that
// PublishAuthoring CAS-activates (explorer/arango/publication.go). It shows
// the binding follows the newest doc while a distinct intended execution exists.
func TestVerifierToBindingChainEchoAgainstArango(t *testing.T) {
	ctx, _, registry := nameBindingOpen(t, 60*time.Second)

	project := "nlb-chain-project"
	generation := "nlb-chain-gen-" + uuid.NewString()
	selector := publication.DataframeSelector{Recipe: "nlb-chain-recipe", TranslationVersion: "nlb-chain-tv", Output: "nlb-chain-output"}
	t1 := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 10, 30, 18, 0, 0, 0, time.UTC)

	intended := nameBindingSeedPublished(t, ctx, registry, nameBindingExecSpec{
		id: "nlb-chain-intended", key: "nlb-chain-key-intended", project: project, generation: generation,
		recipe: selector.Recipe, translationVersion: selector.TranslationVersion, output: selector.Output,
		recipeDigest: "sha256:chain-intended-recipe", schemaDigest: "sha256:chain-intended-schema",
		receiptID: "receipt-chain-intended", publishedAt: t1,
	})
	newer := nameBindingSeedPublished(t, ctx, registry, nameBindingExecSpec{
		id: "nlb-chain-newer", key: "nlb-chain-key-newer", project: project, generation: generation,
		recipe: selector.Recipe, translationVersion: selector.TranslationVersion, output: selector.Output,
		recipeDigest: "sha256:chain-newer-recipe", schemaDigest: "sha256:chain-newer-schema",
		receiptID: "receipt-chain-newer", publishedAt: t2,
	})

	verifier := nameBindingReplicatedVerificationStore{executions: registry}
	verification, err := verifier.VerifyPublication(ctx, project, generation, selector)
	if err != nil {
		t.Fatalf("VerifyPublication: %v", err)
	}
	// The intended distinct execution must still exist for the echo to mean
	// anything: it is shadowed at the finder, not absent.
	storedIntended, err := registry.GetExecution(ctx, intended.ID)
	if err != nil {
		t.Fatalf("intended execution %s missing: %v", intended.ID, err)
	}

	ref, err := dataset.NewRef(project, generation)
	if err != nil {
		t.Fatalf("NewRef: %v", err)
	}
	snapshot, err := dataset.NewSchemaSnapshot("nlb-schema", "4.6.0", strings.Repeat("a", 64), []string{"Patient"})
	if err != nil {
		t.Fatalf("NewSchemaSnapshot (skip condition: snapshot validation): %v", err)
	}
	manifest, err := dataset.NewManifest(ref, snapshot)
	if err != nil {
		t.Fatalf("NewManifest (skip condition: manifest validation): %v", err)
	}
	manifest, err = manifest.Transition(dataset.StateStaged)
	if err != nil {
		t.Fatalf("manifest Transition (skip condition: staging transition): %v", err)
	}
	releases := &nameBindingReleaseRepository{}
	releaseService := dataset.ReleaseService{
		Manifests: nameBindingManifestReader{manifest: manifest},
		Releases:  releases,
		Verifier:  verifier,
	}
	release, err := releaseService.Create(ctx, dataset.ActivationRequest{
		Project: project, Generation: generation, GitCommit: generation,
		OptionalSelectors: []dataset.DataframeSelector{selector},
	})
	if err != nil {
		t.Fatalf("ReleaseService.Create: %v", err)
	}
	if len(release.Publications) != 1 {
		t.Fatalf("release publications = %+v, want exactly one binding", release.Publications)
	}
	binding := release.Publications[0]

	if verification.ExecutionID != newer.ID || binding.ExecutionID != newer.ID {
		t.Fatalf("release verification and immutable binding must follow the newest exact-selector execution: verification=%q binding=%q newest=%q intended=%q",
			verification.ExecutionID, binding.ExecutionID, newer.ID, storedIntended.ID)
	}
}
