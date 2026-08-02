package exec

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

func (s *memoryRecipeStore) ReplaceRecipe(_ context.Context, entry Entry) error {
	if s.entries == nil {
		s.entries = map[string]Entry{}
	}
	s.entries[entry.Bundle.Name] = entry
	return nil
}

func TestPersistentRegistryUpdatesOnlyTheDefaultRecipe(t *testing.T) {
	store := &memoryRecipeStore{}
	registry := PersistentRegistry{Store: store}
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "default", TranslationVersion: "v", Outputs: []recipe.Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "patient"}}}
	first, err := registry.RegisterDefault(context.Background(), bundle)
	if err != nil {
		t.Fatal(err)
	}
	bundle.TranslationVersion = "changed"
	second, err := registry.RegisterDefault(context.Background(), bundle)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest == second.Digest || store.entries[bundle.Name].Digest != second.Digest {
		t.Fatalf("default recipe was not replaced: first=%#v second=%#v stored=%#v", first, second, store.entries[bundle.Name])
	}
}
