package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	publicationarango "github.com/calypr/loom/internal/dataframe/publication/arango"
	"github.com/calypr/loom/internal/dataframe/recipe"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

const Collection = "loom_dataframe_recipes"

type Registry struct {
	client      arangostore.RowBatchClient
	publication *publicationarango.Registry
	batchSize   int
}

func New(client arangostore.RowBatchClient) (*Registry, error) {
	if client == nil {
		return nil, fmt.Errorf("Arango recipe registry client is required")
	}
	publication, err := publicationarango.New(client)
	if err != nil {
		return nil, fmt.Errorf("create publication registry for recipe usage: %w", err)
	}
	return &Registry{client: client, publication: publication, batchSize: 32}, nil
}

func BootstrapSpec() arangostore.BootstrapSpec {
	return arangostore.BootstrapSpec{Collections: []arangostore.CollectionSpec{{Name: Collection, Indexes: [][]string{{"name"}, {"name", "translationVersion"}, {"digest"}}}}}
}

func (r *Registry) SaveRecipe(ctx context.Context, entry recipe.Entry) error {
	return r.saveRecipe(ctx, entry)
}

func (r *Registry) ReplaceRecipeVersion(ctx context.Context, entry recipe.Entry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	doc["name"], doc["translationVersion"], doc["digest"] = entry.Bundle.Name, entry.Bundle.TranslationVersion, entry.Digest
	replaced, err := r.publication.ReplaceRecipeVersionIfUnused(
		ctx, Collection, recipeDocumentKey(entry.Bundle.Name, entry.Bundle.TranslationVersion),
		entry.Bundle.Name, entry.Bundle.TranslationVersion, doc,
	)
	if err != nil {
		return err
	}
	if !replaced {
		return fmt.Errorf("%w: %s@%s", recipe.ErrRecipeVersionImmutable, entry.Bundle.Name, entry.Bundle.TranslationVersion)
	}
	return nil
}

func (r *Registry) saveRecipe(ctx context.Context, entry recipe.Entry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	doc["name"] = entry.Bundle.Name
	doc["translationVersion"] = entry.Bundle.TranslationVersion
	doc["digest"] = entry.Digest
	doc["_key"] = recipeDocumentKey(entry.Bundle.Name, entry.Bundle.TranslationVersion)
	data, err = json.Marshal(doc)
	if err != nil {
		return err
	}
	return r.client.InsertBatchRaw(ctx, Collection, []json.RawMessage{data}, true, "document")
}

func (r *Registry) LoadRecipe(ctx context.Context, name string) (recipe.Entry, error) {
	return r.loadRecipe(ctx, `FOR doc IN @@collection FILTER doc.name == @name SORT doc.translationVersion DESC LIMIT 1 RETURN doc`, map[string]interface{}{"@collection": Collection, "name": name})
}

func (r *Registry) LoadRecipeVersion(ctx context.Context, name, translationVersion string) (recipe.Entry, error) {
	return r.loadRecipe(ctx, `FOR doc IN @@collection FILTER doc.name == @name AND doc.translationVersion == @translationVersion LIMIT 1 RETURN doc`, map[string]interface{}{"@collection": Collection, "name": name, "translationVersion": translationVersion})
}

func (r *Registry) loadRecipe(ctx context.Context, query string, vars map[string]interface{}) (recipe.Entry, error) {
	entry, found, err := arangostore.QueryLast[recipe.Entry](ctx, r.client, query, r.batchSize, vars)
	if err != nil {
		return recipe.Entry{}, err
	}
	if !found {
		return recipe.Entry{}, fmt.Errorf("%w", recipe.ErrRecipeNotFound)
	}
	return entry, nil
}

func (r *Registry) RecipeVersionUsed(ctx context.Context, name, translationVersion string) (bool, error) {
	return r.publication.RecipeVersionUsed(ctx, name, translationVersion)
}

func recipeDocumentKey(name, translationVersion string) string {
	sum := sha256.Sum256([]byte(name + "\x00" + translationVersion))
	return hex.EncodeToString(sum[:])
}

var _ recipe.Store = (*Registry)(nil)
