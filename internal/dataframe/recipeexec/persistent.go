package recipeexec

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

var ErrRecipeNotFound = errors.New("recipe not found")

// PersistentRegistry provides the same immutable/idempotent contract as the
// process-local Registry while making persistence explicit for production
// wiring. It never evaluates recipe data.
type PersistentRegistry struct {
	Store Store
}

func (r PersistentRegistry) Register(ctx context.Context, bundle recipe.Bundle) (Entry, error) {
	entry, err := canonicalEntry(bundle)
	if err != nil {
		return Entry{}, err
	}
	if r.Store == nil {
		return Entry{}, fmt.Errorf("persistent recipe store is required")
	}
	if existing, err := r.Store.LoadRecipe(ctx, bundle.Name); err == nil {
		if existing.Digest != entry.Digest {
			return Entry{}, fmt.Errorf("recipe %q is already registered with a different digest", bundle.Name)
		}
		return existing, nil
	} else if !errors.Is(err, ErrRecipeNotFound) {
		return Entry{}, fmt.Errorf("load recipe %q: %w", bundle.Name, err)
	}
	if err := r.Store.SaveRecipe(ctx, entry); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func (r PersistentRegistry) Get(ctx context.Context, name string) (Entry, bool) {
	if r.Store == nil {
		return Entry{}, false
	}
	entry, err := r.Store.LoadRecipe(ctx, name)
	return entry, err == nil
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
