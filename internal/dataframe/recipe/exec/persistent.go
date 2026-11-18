package exec

import (
	"context"
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

type Reader interface {
	LoadRecipe(context.Context, string) (Entry, error)
	LoadRecipeVersion(context.Context, string, string) (Entry, error)
}

type Store interface {
	Reader
	SaveRecipe(context.Context, Entry) error
	ReplaceRecipeVersion(context.Context, Entry) error
	RecipeVersionUsed(context.Context, string, string) (bool, error)
}

var ErrRecipeNotFound = errors.New("recipe not found")
var ErrRecipeVersionImmutable = errors.New("recipe version is immutable after its first execution")

type Entry struct {
	Bundle recipe.Bundle
	Digest string
}

type PersistentRegistry struct {
	Store Store
}

func (r PersistentRegistry) RegisterVersion(ctx context.Context, bundle recipe.Bundle) (Entry, error) {
	entry, err := canonicalEntry(bundle)
	if err != nil {
		return Entry{}, err
	}
	existing, err := r.Store.LoadRecipeVersion(ctx, bundle.Name, bundle.TranslationVersion)
	if err == nil {
		if existing.Digest == entry.Digest {
			return existing, nil
		}
		used, err := r.Store.RecipeVersionUsed(ctx, bundle.Name, bundle.TranslationVersion)
		if err != nil {
			return Entry{}, fmt.Errorf("inspect recipe version usage: %w", err)
		}
		if used {
			return Entry{}, fmt.Errorf("%w: %s@%s", ErrRecipeVersionImmutable, bundle.Name, bundle.TranslationVersion)
		}
		if err := r.Store.ReplaceRecipeVersion(ctx, entry); err != nil {
			return Entry{}, err
		}
		return entry, nil
	}
	if !errors.Is(err, ErrRecipeNotFound) {
		return Entry{}, fmt.Errorf("load recipe %q: %w", bundle.Name, err)
	}
	if err := r.Store.SaveRecipe(ctx, entry); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func canonicalEntry(bundle recipe.Bundle) (Entry, error) {
	immutable, err := bundle.CanonicalBundle()
	if err != nil {
		return Entry{}, err
	}
	digest, err := immutable.Digest()
	if err != nil {
		return Entry{}, err
	}
	return Entry{Bundle: immutable, Digest: digest}, nil
}
