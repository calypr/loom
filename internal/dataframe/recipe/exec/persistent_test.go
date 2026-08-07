package exec

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

type memoryRecipeStore struct {
	entries map[string]Entry
	used    map[string]bool
}

func recipeKey(name, version string) string { return name + "\x00" + version }

func (s *memoryRecipeStore) SaveRecipe(_ context.Context, entry Entry) error {
	if s.entries == nil {
		s.entries = map[string]Entry{}
	}
	s.entries[recipeKey(entry.Bundle.Name, entry.Bundle.TranslationVersion)] = entry
	return nil
}
func (s *memoryRecipeStore) LoadRecipe(_ context.Context, name string) (Entry, error) {
	for _, entry := range s.entries {
		if entry.Bundle.Name == name {
			return entry, nil
		}
	}
	return Entry{}, ErrRecipeNotFound
}

func (s *memoryRecipeStore) LoadRecipeVersion(_ context.Context, name, version string) (Entry, error) {
	entry, ok := s.entries[recipeKey(name, version)]
	if !ok {
		return Entry{}, ErrRecipeNotFound
	}
	return entry, nil
}

func (s *memoryRecipeStore) ReplaceRecipeVersion(_ context.Context, entry Entry) error {
	if s.entries == nil {
		s.entries = map[string]Entry{}
	}
	s.entries[recipeKey(entry.Bundle.Name, entry.Bundle.TranslationVersion)] = entry
	return nil
}

func (s *memoryRecipeStore) RecipeVersionUsed(_ context.Context, name, version string) (bool, error) {
	return s.used[recipeKey(name, version)], nil
}

func TestPersistentRegistryRetainsVersions(t *testing.T) {
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
	if first.Digest == second.Digest || len(store.entries) != 2 {
		t.Fatalf("recipe history was not retained: first=%#v second=%#v stored=%#v", first, second, store.entries)
	}
	loadedFirst, err := registry.LoadVersion(context.Background(), "default", "v")
	if err != nil || loadedFirst.Digest != first.Digest {
		t.Fatalf("first version is not independently queryable: loaded=%#v err=%v", loadedFirst, err)
	}
	loadedSecond, err := registry.LoadVersion(context.Background(), "default", "changed")
	if err != nil || loadedSecond.Digest != second.Digest {
		t.Fatalf("second version is not independently queryable: loaded=%#v err=%v", loadedSecond, err)
	}
}

func TestPersistentRegistryRejectsChangedDigestAfterUse(t *testing.T) {
	store := &memoryRecipeStore{}
	registry := PersistentRegistry{Store: store}
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "default", TranslationVersion: "v", Outputs: []recipe.Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "patient"}}}
	if _, err := registry.RegisterVersion(context.Background(), bundle); err != nil {
		t.Fatal(err)
	}
	store.used = map[string]bool{recipeKey("default", "v"): true}
	bundle.Outputs[0].RowGrain = "changed"
	if _, err := registry.RegisterVersion(context.Background(), bundle); !errors.Is(err, ErrRecipeVersionImmutable) {
		t.Fatalf("RegisterVersion() error = %v, want immutable", err)
	}
}

func TestPersistentRegistryAllowsChangedDigestBeforeUse(t *testing.T) {
	store := &memoryRecipeStore{}
	registry := PersistentRegistry{Store: store}
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "default", TranslationVersion: "v", Outputs: []recipe.Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "patient"}}}
	first, err := registry.RegisterVersion(context.Background(), bundle)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Outputs[0].RowGrain = "changed"
	second, err := registry.RegisterVersion(context.Background(), bundle)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest == second.Digest || len(store.entries) != 1 {
		t.Fatalf("unused exact version was not replaced: first=%#v second=%#v", first, second)
	}
}
