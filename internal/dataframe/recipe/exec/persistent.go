package exec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

// Store is the persistence boundary for recipe registration. Implementations
// may use Arango, a file-backed registry, or another durable store; the
// registry itself owns validation, canonicalization, and idempotency rules.
type Store interface {
	SaveRecipe(context.Context, Entry) error
	LoadRecipe(context.Context, string) (Entry, error)
}

// VersionedStore is the durable contract used by new recipe workflows. The
// name-only Store methods remain above for the compatibility window only.
type VersionedStore interface {
	Store
	LoadRecipeVersion(context.Context, string, string) (Entry, error)
	ReplaceRecipeVersion(context.Context, Entry) error
	RecipeVersionUsed(context.Context, string, string) (bool, error)
}

var ErrRecipeNotFound = errors.New("recipe not found")
var ErrRecipeVersionImmutable = errors.New("recipe version is immutable after its first execution")

type Entry struct {
	Bundle recipe.Bundle
	Digest string
}

// PersistentRegistry owns immutable/idempotent recipe registration. It never
// evaluates recipe data.
type PersistentRegistry struct {
	Store Store
}

// RegisterVersion retains recipe history by (name, translationVersion). An
// exact version is replaceable only until its first durable execution exists.
func (r PersistentRegistry) RegisterVersion(ctx context.Context, bundle recipe.Bundle) (Entry, error) {
	entry, err := canonicalEntry(bundle)
	if err != nil {
		return Entry{}, err
	}
	store, ok := r.Store.(VersionedStore)
	if !ok {
		return Entry{}, fmt.Errorf("persistent recipe store does not support versioned recipes")
	}
	existing, err := store.LoadRecipeVersion(ctx, bundle.Name, bundle.TranslationVersion)
	if err == nil {
		if existing.Digest == entry.Digest {
			return existing, nil
		}
		used, err := store.RecipeVersionUsed(ctx, bundle.Name, bundle.TranslationVersion)
		if err != nil {
			return Entry{}, fmt.Errorf("inspect recipe version usage: %w", err)
		}
		if used {
			return Entry{}, fmt.Errorf("%w: %s@%s", ErrRecipeVersionImmutable, bundle.Name, bundle.TranslationVersion)
		}
		if err := store.ReplaceRecipeVersion(ctx, entry); err != nil {
			return Entry{}, err
		}
		return entry, nil
	}
	if !errors.Is(err, ErrRecipeNotFound) {
		return Entry{}, fmt.Errorf("load recipe %q: %w", bundle.Name, err)
	}
	if err := store.SaveRecipe(ctx, entry); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

// RegisterDefault is retained as an additive compatibility alias. It no
// longer replaces name-only rows: the bundle's translationVersion is exact.
func (r PersistentRegistry) RegisterDefault(ctx context.Context, bundle recipe.Bundle) (Entry, error) {
	return r.RegisterVersion(ctx, bundle)
}

func (r PersistentRegistry) LoadVersion(ctx context.Context, name, translationVersion string) (Entry, error) {
	store, ok := r.Store.(VersionedStore)
	if !ok {
		return Entry{}, fmt.Errorf("persistent recipe store does not support versioned recipes")
	}
	return store.LoadRecipeVersion(ctx, name, translationVersion)
}

func canonicalEntry(bundle recipe.Bundle) (Entry, error) {
	if bundle.Fragments != nil {
		expanded, err := bundle.ExpandFragments()
		if err != nil {
			return Entry{}, err
		}
		bundle = expanded
	}
	if err := bundle.Validate(); err != nil {
		return Entry{}, err
	}
	canonical, err := bundle.CanonicalJSON()
	if err != nil {
		return Entry{}, err
	}
	var immutable recipe.Bundle
	if err := json.Unmarshal(canonical, &immutable); err != nil {
		return Entry{}, fmt.Errorf("clone recipe: %w", err)
	}
	digest, err := immutable.Digest()
	if err != nil {
		return Entry{}, err
	}
	return Entry{Bundle: immutable, Digest: digest}, nil
}
