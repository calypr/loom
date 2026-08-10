package recipe

import (
	"context"
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
