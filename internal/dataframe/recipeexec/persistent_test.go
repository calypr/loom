package recipeexec

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

type memoryRecipeStore struct{ entries map[string]Entry }

func (s *memoryRecipeStore) SaveRecipe(_ context.Context, entry Entry) error {
	if s.entries == nil {
		s.entries = map[string]Entry{}
	}
	s.entries[entry.Bundle.Name] = entry
	return nil
}
func (s *memoryRecipeStore) LoadRecipe(_ context.Context, name string) (Entry, error) {
	entry, ok := s.entries[name]
	if !ok {
		return Entry{}, ErrRecipeNotFound
	}
	return entry, nil
}

func TestPersistentRegistryIsIdempotentAndDigestLocked(t *testing.T) {
	store := &memoryRecipeStore{}
	registry := PersistentRegistry{Store: store}
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "r", TranslationVersion: "v", Outputs: []recipe.Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "patient"}}}
	first, err := registry.Register(context.Background(), bundle)
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.Register(context.Background(), bundle)
	if err != nil || first.Digest != second.Digest {
		t.Fatalf("not idempotent: %#v %#v %v", first, second, err)
	}
	bundle.TranslationVersion = "changed"
	if _, err := registry.Register(context.Background(), bundle); err == nil {
		t.Fatal("expected digest conflict")
	}
}
