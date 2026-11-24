package arango

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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

	legacy := publication.BundleExecution{
		ID: "nlb-case-legacy", Key: "nlb-case-key-legacy",
		BundleIdentity: publication.BundleIdentity{
			Name: selector.Recipe, Project: project, DatasetGeneration: generation,
			TranslationVersion: selector.TranslationVersion,
			RecipeDigest:       "sha256:case-legacy-recipe", SchemaDigest: "sha256:case-legacy-schema",
			ReceiptID: "receipt-case-legacy", ScopeDigest: "scope-legacy", EngineVersion: "engine-legacy",
		},
		State: "READY",
		Outputs: []publication.BundleOutputRecord{{
			Name: selector.Output, PhysicalTable: "ch_nlb_case_legacy", RowCount: 3, ByteCount: 300,
			State: "READY",
		}},
		CreatedAt: t1, UpdatedAt: t2, ReadyAt: &t2,
	}
	legacyDoc, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy execution: %v", err)
	}
	var persistedLegacy map[string]interface{}
	if err := json.Unmarshal(legacyDoc, &persistedLegacy); err != nil {
		t.Fatalf("decode legacy execution: %v", err)
	}

	persistedLegacy["_key"] = legacy.ID
	if err := client.ExecuteAQL(ctx, `INSERT @doc INTO @@collection`, map[string]interface{}{"@collection": BundleExecutionsCollection, "doc": persistedLegacy}); err != nil {
		t.Fatalf("insert legacy-shaped doc: %v", err)
	}

	assertLegacy := func(execution publication.BundleExecution) {
		t.Helper()
		if execution.ID != legacy.ID || execution.State != publication.BundlePublished || execution.PublishedAt == nil || !execution.PublishedAt.Equal(t2) {
			t.Fatalf("legacy execution did not decode as published: %+v", execution)
		}
		if len(execution.Outputs) != 1 || execution.Outputs[0].State != publication.BundlePublished || !execution.Outputs[0].Queryable() || execution.Outputs[0].Selector != selector {
			t.Fatalf("legacy output did not decode as queryable: %+v", execution.Outputs)
		}
	}
	byID, err := registry.GetExecution(ctx, legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertLegacy(byID)
	byKey, err := registry.FindExecutionByKey(ctx, legacy.Key)
	if err != nil {
		t.Fatal(err)
	}
	assertLegacy(byKey)

	before := t2.Add(time.Minute)
	list, err := registry.ListExecutions(ctx, publication.BundlePublished, before)
	if err != nil {
		t.Fatal(err)
	}
	var paged []publication.BundleExecution
	if err := registry.VisitExecutionPages(ctx, publication.BundlePublished, before, 1, func(page []publication.BundleExecution) error {
		paged = append(paged, page...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, executions := range [][]publication.BundleExecution{list, paged} {
		if len(executions) != 2 {
			t.Fatalf("published execution reads returned %d rows, want 2: %+v", len(executions), executions)
		}
		assertLegacy(executions[1])
	}

	execution, output, err := registry.FindExecutionBySelector(ctx, project, generation, selector)
	if err != nil {
		t.Fatalf("FindExecutionBySelector: %v", err)
	}
	if execution.ID != legacy.ID || execution.State != publication.BundlePublished || execution.PublishedAt == nil || !execution.PublishedAt.Equal(t2) || output.Name != selector.Output {
		t.Fatalf("latest exact-selector lookup returned execution %s (state=%q publishedAt=%v) and output %q; want legacy execution %q canonicalized to PUBLISHED at %s with output %q",
			nameBindingIdentity(execution), execution.State, execution.PublishedAt, output.Name, legacy.ID, t2.Format(time.RFC3339Nano), selector.Output)
	}

	t3 := t2.Add(time.Hour)
	newest := nameBindingSeedPublished(t, ctx, registry, nameBindingExecSpec{
		id: "nlb-case-newest", key: "nlb-case-key-newest", project: project, generation: generation,
		recipe: selector.Recipe, translationVersion: selector.TranslationVersion, output: selector.Output,
		recipeDigest: "sha256:case-newest-recipe", schemaDigest: "sha256:case-newest-schema",
		receiptID: "receipt-case-newest", publishedAt: t3,
	})
	execution, output, err = registry.FindExecutionBySelector(ctx, project, generation, selector)
	if err != nil {
		t.Fatalf("FindExecutionBySelector after newer publication: %v", err)
	}
	if execution.ID != newest.ID || execution.PublishedAt == nil || !execution.PublishedAt.Equal(t3) || output.Name != selector.Output {
		t.Fatalf("latest exact-selector lookup returned execution %q at %v and output %q; want %q at %s with output %q",
			execution.ID, execution.PublishedAt, output.Name, newest.ID, t3.Format(time.RFC3339Nano), selector.Output)
	}
}

func TestExecutionStateFiltersIncludeLegacyDocumentsAgainstArango(t *testing.T) {
	ctx, client, registry := nameBindingOpen(t, 60*time.Second)
	cases := []struct {
		state  publication.BundleState
		stored []string
	}{
		{publication.BundleQueued, []string{"QUEUED", "PENDING"}},
		{publication.BundleRunning, []string{"RUNNING", "PREFLIGHT", "LOADING"}},
		{publication.BundleValidating, []string{"VALIDATING"}},
		{publication.BundlePublished, []string{"PUBLISHED", "READY"}},
		{publication.BundleFailed, []string{"FAILED"}},
	}
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	for _, tc := range cases {
		for _, state := range tc.stored {
			if err := client.ExecuteAQL(ctx, `INSERT {_key: @state, id: @state, state: @state, updatedAt: @now} INTO @@collection`, map[string]interface{}{
				"@collection": BundleExecutionsCollection, "state": state, "now": now,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, tc := range cases {
		t.Run(string(tc.state), func(t *testing.T) {
			before := now.Add(time.Minute)
			list, err := registry.ListExecutions(ctx, tc.state, before)
			if err != nil {
				t.Fatal(err)
			}
			var paged []publication.BundleExecution
			if err := registry.VisitExecutionPages(ctx, tc.state, before, 1, func(page []publication.BundleExecution) error {
				paged = append(paged, page...)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for _, executions := range [][]publication.BundleExecution{list, paged} {
				var ids []string
				for _, execution := range executions {
					if execution.State != tc.state {
						t.Fatalf("execution %q state = %q, want %q", execution.ID, execution.State, tc.state)
					}
					ids = append(ids, execution.ID)
				}
				slices.Sort(ids)
				want := slices.Clone(tc.stored)
				slices.Sort(want)
				if !slices.Equal(ids, want) {
					t.Fatalf("state filter read execution IDs %v, want %v", ids, want)
				}
			}
		})
	}
}

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
		State: string(publication.DecodeBundleState(execution.State)), Queryable: execution.State.Successful() && output.Queryable(),
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
