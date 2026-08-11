package recipe

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryRevisionStoreRegistersImmutableProjectRevision(t *testing.T) {
	store := NewMemoryRevisionStore()
	bundle := Bundle{RecipeSchemaVersion: CurrentSchemaVersion, Name: "files", TranslationVersion: "1", Outputs: []Output{{Name: "DocumentReference", RootResourceType: "DocumentReference", RowGrain: "document_reference"}}}
	revision, err := store.Register(context.Background(), "project-a", bundle, "")
	if err != nil {
		t.Fatal(err)
	}
	if revision.Digest == "" || revision.Project != "project-a" {
		t.Fatalf("revision = %+v", revision)
	}
	loaded, err := store.Get(context.Background(), "project-a", "files", revision.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Digest != revision.Digest || loaded.Bundle.Name != "files" {
		t.Fatalf("loaded revision = %+v", loaded)
	}
	if _, err := store.Get(context.Background(), "project-b", "files", revision.Digest); err != ErrRecipeRevisionNotFound {
		t.Fatalf("cross-project lookup error = %v", err)
	}
}

func TestProjectRecipeNameAndDraftCAS(t *testing.T) {
	store := NewMemoryDraftStore()
	bundle := Bundle{RecipeSchemaVersion: CurrentSchemaVersion, Outputs: []Output{{Name: "Patients", RootResourceType: "Patient", RowGrain: "patient"}}}
	draft, err := store.SaveDraft(context.Background(), RecipeDraft{Project: "acme/cancer-study", Document: bundle}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if draft.DraftVersion != 1 || draft.Document.Name != ProjectRecipeName("acme/cancer-study") || draft.Document.TranslationVersion != "draft" {
		t.Fatalf("draft = %#v", draft)
	}
	if _, err := store.SaveDraft(context.Background(), RecipeDraft{Project: draft.Project, Document: bundle}, 0); !errors.Is(err, ErrDraftConflict) {
		t.Fatalf("stale save error = %v", err)
	}
	loaded, err := store.GetDraft(context.Background(), draft.Project)
	if err != nil || loaded.AuthoringDigest != draft.AuthoringDigest {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
}

func TestNormalizeProjectBundleUsesNonPersistedManagedIdentity(t *testing.T) {
	defaultBundle := Bundle{RecipeSchemaVersion: CurrentSchemaVersion, Name: "default", TranslationVersion: "v1", Outputs: []Output{{Name: "Patients", RootResourceType: "Patient", RowGrain: "patient"}}}
	draft, err := NormalizeProjectBundle("acme/cancer-study", defaultBundle)
	if err != nil {
		t.Fatal(err)
	}
	if draft.DraftVersion != 0 || draft.Document.Name != ProjectRecipeName("acme/cancer-study") || draft.Document.TranslationVersion != "draft" {
		t.Fatalf("normalized draft = %#v", draft)
	}
}
