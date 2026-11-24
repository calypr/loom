package load

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	dataframepublication "github.com/calypr/loom/internal/dataframe/publication"
	publicationarango "github.com/calypr/loom/internal/dataframe/publication/arango"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataset"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	"github.com/calypr/loom/internal/releasecommit"
	"github.com/calypr/loom/internal/testkit/arangotest"
)

func TestActivateDatasetGenerationKeepsReleaseAndManifestReadersAlignedAgainstArango(t *testing.T) {
	if strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL")) == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL for the isolated H-17 generation activation replay")
	}
	database := arangotest.Owned(t, "loom_test_h17_")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()

	if err := database.Client.Bootstrap(ctx, datasetarango.BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap dataset lifecycle: %v", err)
	}
	if err := database.Client.Bootstrap(ctx, publicationarango.BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap dataframe publication: %v", err)
	}
	lifecycle, err := datasetarango.New(database.Client)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := publicationarango.New(database.Client)
	if err != nil {
		t.Fatal(err)
	}

	const project = "H17_GENERATION_API"
	const generationA = "h17-generation-a"
	const generationB = "h17-generation-b"
	manifestA := h17CreateStagedManifest(t, ctx, lifecycle, project, generationA)
	manifestB := h17CreateStagedManifest(t, ctx, lifecycle, project, generationB)
	if !manifestB.IsStaged() {
		t.Fatalf("candidate manifest state = %s, want staged", manifestB.State)
	}
	if err := lifecycle.Activate(ctx, manifestA); err != nil {
		t.Fatalf("activate initial generation A: %v", err)
	}

	legacy, err := dataset.ResolveActive(ctx, lifecycle, project)
	if err != nil || legacy.Dataset != manifestA.Dataset {
		t.Fatalf("legacy generation-only read = %+v, err=%v", legacy, err)
	}

	selector := dataset.DataframeSelector{Recipe: "h17-recipe", TranslationVersion: "v1", Output: "Patient"}
	executionA := h17SavePublishedExecution(t, ctx, registry, project, generationA, "h17-execution-a", selector, "h17_patient_a")
	executionB := h17SavePublishedExecution(t, ctx, registry, project, generationB, "h17-execution-b", selector, "h17_patient_b")
	stamp := time.Now().UTC()
	releaseA, err := lifecycle.SaveRelease(ctx, dataset.ProjectRelease{
		ID: "h17-release-a", Project: project, GitCommit: "h17-commit-a", Generation: generationA,
		Publications: []dataset.ReleasePublication{{
			Selector: selector, ExecutionID: executionA.ID, Generation: generationA, VerifiedAt: stamp,
		}}, CreatedAt: stamp,
	})
	if err != nil {
		t.Fatalf("save initial project release: %v", err)
	}
	activeA, err := lifecycle.CompareAndSwapActivateRelease(ctx, releaseA, 0)
	if err != nil {
		t.Fatalf("activate initial project release: %v", err)
	}
	if activeA.Release.Generation != generationA || activeA.Revision != 1 {
		t.Fatalf("release baseline = generation %q revision %d, want %q revision 1", activeA.Release.Generation, activeA.Revision, generationA)
	}

	releaseService := dataset.ReleaseService{Manifests: lifecycle, Releases: lifecycle, Verifier: dataframepublication.NewPublicationVerifier(registry)}
	service, err := NewService(ServiceConfig{
		LoadGeneration: activationGenerationRunner{}.RunGeneration,
		Manifests:      lifecycle,
		ActivateBundle: func(ctx context.Context, manifest dataset.Manifest, execution dataframepublication.BundleExecution) error {
			return releasecommit.ActivateBundle(ctx, database.Client, releaseService, manifest, execution)
		},
		DataframeReleases: registry,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Config{Service: service, Authorizer: authscope.AllowAllAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := handler.ActivateDatasetGeneration(ctx, project, generationB, executionB.ID, "", nil)
	if err != nil {
		t.Fatalf("generation API activation: %v", err)
	}
	if !result.Activated {
		t.Fatalf("generation API response = %#v, want Activated", result)
	}

	activeRelease, err := lifecycle.ReadActiveRelease(ctx, project)
	if err != nil {
		t.Fatalf("read active project release after generation API activation: %v", err)
	}
	activeManifest, err := dataset.ResolveActive(ctx, lifecycle, project)
	if err != nil {
		t.Fatalf("read active manifest after generation API activation: %v", err)
	}
	reader := published.Reader{Catalog: registry, ActiveReleaseResolver: lifecycle}
	current, err := reader.CurrentProjectDataset(ctx, project, selector)
	if err != nil {
		t.Fatalf("read current published output after generation API activation: %v", err)
	}
	t.Logf("H17_OBSERVED release=%s manifest=%s published_generation=%s published_execution=%s", activeRelease.Release.Generation, activeManifest.Dataset.Generation, current.DatasetGeneration, current.Revision)

	if activeManifest.Dataset.Generation != generationB {
		t.Fatalf("generation API did not activate candidate manifest: got %q, want %q", activeManifest.Dataset.Generation, generationB)
	}
	if activeRelease.Release.Generation != activeManifest.Dataset.Generation {
		t.Fatalf("H-17 confirmed: generation API activated manifest %q while active project release remains %q", activeManifest.Dataset.Generation, activeRelease.Release.Generation)
	}
	if current.DatasetGeneration != activeRelease.Release.Generation || current.Revision != executionB.ID {
		t.Fatalf("release-backed output = generation %q execution %q, want release generation %q execution %q", current.DatasetGeneration, current.Revision, activeRelease.Release.Generation, executionB.ID)
	}
	if activeRelease.Revision != activeA.Revision+1 {
		t.Fatalf("release revision = %d, want %d", activeRelease.Revision, activeA.Revision+1)
	}
	historical, err := reader.ExactExecutionMaterialization(ctx, executionA.ID, selector.Output)
	if err != nil || historical.Revision != executionA.ID || historical.DatasetGeneration != generationA {
		t.Fatalf("historical execution changed: %+v, err=%v", historical, err)
	}

	// Essential: Old deployments could advance the derived generation pointer without changing the release.
	err = database.Client.QueryRows(ctx, `LET doc = DOCUMENT(@@lifecycle, @key)
UPDATE doc WITH {dataset: @dataset, manifestKey: @manifest} IN @@lifecycle RETURN NEW`, 1, map[string]any{
		"@lifecycle": datasetarango.LifecycleCollection, "key": datasetarango.ActiveDocumentKey(project),
		"dataset": manifestA.Dataset, "manifest": datasetarango.ManifestDocumentKey(manifestA.Dataset),
	}, func(map[string]any) error { return nil })
	if err != nil {
		t.Fatalf("reproduce the legacy pointer split: %v", err)
	}
	resolved, err := dataset.ResolveActive(ctx, lifecycle, project)
	if err != nil || resolved.Dataset != manifestB.Dataset {
		t.Fatalf("stale generation projection overrode release: manifest=%+v err=%v", resolved, err)
	}

	authoringActive, err := datasetarango.CompareAndSwapPublishRelease(ctx, database.Client, activeRelease.Release, activeRelease.Revision)
	if err != nil || authoringActive.Revision != activeRelease.Revision {
		t.Fatalf("interactive publication rejected the active release generation: active=%+v err=%v", authoringActive, err)
	}

	const generationC = "h17-generation-c"
	manifestC := h17CreateStagedManifest(t, ctx, lifecycle, project, generationC)
	if err := lifecycle.Activate(ctx, manifestC); !errors.Is(err, datasetarango.ErrActivationConflict) {
		t.Fatalf("generation-only activation would bypass the release: err=%v", err)
	}
	if err := releasecommit.ActivateGeneration(ctx, database.Client, lifecycle, manifestC); err != nil {
		t.Fatalf("activate staged generation with no new dataframe bundle: %v", err)
	}
	activeC, err := lifecycle.ReadActiveRelease(ctx, project)
	if err != nil || activeC.Release.Generation != generationC || activeC.Revision != activeRelease.Revision+1 {
		t.Fatalf("staged generation did not advance release: active=%+v err=%v", activeC, err)
	}
	if len(activeC.Release.Publications) != 1 {
		t.Fatalf("staged generation changed the binding count: %+v", activeC.Release.Publications)
	}
	prior := activeC.Release.Publications[0]
	if prior.ExecutionID != executionB.ID || prior.Generation != generationB || !prior.Stale || prior.Required {
		t.Fatalf("staged generation did not retain stale binding for the last verified execution: %+v", prior)
	}

}

func h17CreateStagedManifest(t *testing.T, ctx context.Context, lifecycle *datasetarango.Store, project, generation string) dataset.Manifest {
	t.Helper()
	ref, err := dataset.NewRef(project, generation)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := dataset.NewSchemaSnapshot("urn:h17:test", "R5", strings.Repeat("a", 64), []string{"Patient"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := dataset.NewManifest(ref, schema)
	if err != nil {
		t.Fatal(err)
	}
	created, err := lifecycle.CreateManifest(ctx, manifest)
	if err != nil {
		t.Fatalf("create manifest %s: %v", generation, err)
	}
	staged, err := lifecycle.TransitionManifest(ctx, created, dataset.StateStaged)
	if err != nil {
		t.Fatalf("stage manifest %s: %v", generation, err)
	}
	return staged
}

func h17SavePublishedExecution(t *testing.T, ctx context.Context, registry *publicationarango.Registry, project, generation, id string, selector dataset.DataframeSelector, table string) dataframepublication.BundleExecution {
	t.Helper()
	stamp := time.Now().UTC()
	identity := dataframepublication.BundleIdentity{
		Name: selector.Recipe, TranslationVersion: selector.TranslationVersion,
		Project: project, DatasetGeneration: generation,
		RecipeDigest: "h17-recipe-digest", SchemaDigest: "h17-schema-digest",
		ReceiptID: "h17-receipt-" + id, ScopeDigest: "h17-scope", EngineVersion: "h17-test",
	}
	owner := "h17-owner-" + id
	execution := dataframepublication.BundleExecution{
		ID: id, Key: identity.Key(), BundleIdentity: identity,
		State: dataframepublication.BundlePublished, OwnerID: owner,
		CreatedAt: stamp, UpdatedAt: stamp, ReadyAt: &stamp, PublishedAt: &stamp,
		Outputs: []dataframepublication.BundleOutputRecord{{
			Name: selector.Output, PhysicalTable: table, Selector: selector,
			State: dataframepublication.BundlePublished, VerifiedAt: &stamp,
		}},
	}
	claimed, err := registry.AcquireBundleLease(ctx, execution.Key, owner, stamp.Add(5*time.Minute))
	if err != nil || !claimed {
		t.Fatalf("acquire execution lease %s: claimed=%v err=%v", id, claimed, err)
	}
	if err := registry.SaveExecution(ctx, execution, owner); err != nil {
		t.Fatalf("save execution %s: %v", id, err)
	}
	if err := registry.CompareAndSwapPointer(ctx, execution.PointerName(), "", execution.ID); err != nil {
		t.Fatalf("publish execution pointer %s: %v", id, err)
	}
	if err := registry.ReleaseBundleLease(ctx, execution.Key, owner); err != nil {
		t.Fatalf("release execution lease %s: %v", id, err)
	}
	return execution
}
