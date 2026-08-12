package recipe

import (
	"context"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
)

type projectRegistryFake struct{ calls int }

func (f *projectRegistryFake) RegisterVersionForProject(_ context.Context, _ string, bundle Bundle) (string, error) {
	f.calls++
	return bundleDigestForTest(bundle), nil
}

type projectEnqueuerFake struct {
	identities []publication.BundleIdentity
	execution  publication.BundleExecution
}

func (f *projectEnqueuerFake) Enqueue(_ context.Context, identity publication.BundleIdentity) (publication.BundleExecution, error) {
	f.identities = append(f.identities, identity)
	if f.execution.ID == "" {
		f.execution = publication.BundleExecution{ID: "execution-1", Key: identity.Key(), BundleIdentity: identity, State: publication.BundleQueued}
	}
	return f.execution, nil
}

func bundleDigestForTest(bundle Bundle) string {
	digest, _ := bundle.Digest()
	return digest
}

func TestProjectRecipePublisherEnqueuesImmutableSelectedOutputsAndReusesRetry(t *testing.T) {
	ctx := context.Background()
	drafts := NewMemoryDraftStore()
	revisions := NewMemoryRevisionStore()
	bundle := Bundle{RecipeSchemaVersion: CurrentSchemaVersion, Outputs: []Output{
		{Name: "Patients", RootResourceType: "Patient", RowGrain: "patient"},
		{Name: "Observations", RootResourceType: "Observation", RowGrain: "observation"},
	}}
	draft, err := drafts.SaveDraft(ctx, RecipeDraft{Project: "org/project", Document: bundle}, 0)
	if err != nil {
		t.Fatal(err)
	}
	registry := &projectRegistryFake{}
	enqueuer := &projectEnqueuerFake{}
	publisher := ProjectRecipePublisher{Drafts: drafts, Revisions: revisions, Registry: ProjectRecipeRegistryFunc(registry.RegisterVersionForProject), Enqueuer: enqueuer}
	revision, err := publisher.Publish(ctx, "org/project", draft.DraftVersion, "generation-1", []string{"Observations"})
	if err != nil {
		t.Fatal(err)
	}
	if revision.Status != RecipeRevisionMaterializing || revision.TranslationVersion == "draft" || revision.ExecutionID != "execution-1" {
		t.Fatalf("revision = %#v", revision)
	}
	if len(enqueuer.identities) != 1 || len(enqueuer.identities[0].SelectedOutputs) != 1 || enqueuer.identities[0].SelectedOutputs[0] != "Observations" {
		t.Fatalf("enqueue identities = %#v", enqueuer.identities)
	}
	if enqueuer.identities[0].ScopeProject != "org/project" || enqueuer.identities[0].ProjectRevisionID != revision.ID {
		t.Fatalf("identity = %#v", enqueuer.identities[0])
	}
	if enqueuer.identities[0].Project != "org-project" {
		t.Fatalf("physical project = %q", enqueuer.identities[0].Project)
	}
	// A retry of the same immutable authoring input returns the original
	// revision/execution and does not enqueue another materialization.
	retry, err := publisher.Publish(ctx, "org/project", draft.DraftVersion, "generation-1", []string{"Observations"})
	if err != nil || retry.ID != revision.ID || len(enqueuer.identities) != 1 {
		t.Fatalf("retry=%#v err=%v enqueues=%d", retry, err, len(enqueuer.identities))
	}

	stamp := time.Now().UTC()
	partial := enqueuer.execution
	partial.State = publication.BundlePublished
	partial.Outputs = []publication.BundleOutputRecord{{Name: "Observations", MaterializationID: "mat-1", PhysicalTable: "table-1", State: publication.BundlePublished, VerifiedAt: &stamp}}
	if err := publisher.ObserveExecution(ctx, partial); err != nil {
		t.Fatal(err)
	}
	loaded, err := revisions.GetProjectRevision(ctx, "org/project", revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != RecipeRevisionReady || loaded.Outputs[0].MaterializationID != "mat-1" {
		t.Fatalf("partial completion incorrectly handled: %#v", loaded)
	}
}

func TestProjectRecipePublisherDoesNotMarkReadyWhenAnOutputIsMissing(t *testing.T) {
	ctx := context.Background()
	drafts := NewMemoryDraftStore()
	revisions := NewMemoryRevisionStore()
	bundle := Bundle{RecipeSchemaVersion: CurrentSchemaVersion, Outputs: []Output{
		{Name: "Patients", RootResourceType: "Patient", RowGrain: "patient"},
		{Name: "Observations", RootResourceType: "Observation", RowGrain: "observation"},
	}}
	draft, err := drafts.SaveDraft(ctx, RecipeDraft{Project: "p", Document: bundle}, 0)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer := &projectEnqueuerFake{}
	publisher := ProjectRecipePublisher{Drafts: drafts, Revisions: revisions, Registry: ProjectRecipeRegistryFunc(func(_ context.Context, _ string, b Bundle) (string, error) { return bundleDigestForTest(b), nil }), Enqueuer: enqueuer}
	revision, err := publisher.Publish(ctx, "p", draft.DraftVersion, "g", nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	execution := enqueuer.execution
	execution.State = publication.BundlePublished
	execution.Outputs = []publication.BundleOutputRecord{{Name: "Patients", PhysicalTable: "patients", State: publication.BundlePublished, VerifiedAt: &now}}
	if err := publisher.ObserveExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	loaded, err := revisions.GetProjectRevision(ctx, "p", revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status == RecipeRevisionReady {
		t.Fatalf("missing selected output became READY: %#v", loaded)
	}
}
