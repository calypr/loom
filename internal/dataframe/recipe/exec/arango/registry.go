// Package arango provides the durable exec.Store adapter used by Loom
// deployments that already persist dataset metadata in ArangoDB.
package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

const Collection = "loom_dataframe_recipes"

type Client = arangostore.RowBatchClient

type Registry struct {
	client    Client
	batchSize int
}

func New(client Client) (*Registry, error) {
	if client == nil {
		return nil, fmt.Errorf("Arango recipe registry client is required")
	}
	return &Registry{client: client, batchSize: 32}, nil
}

func BootstrapSpec() arangostore.BootstrapSpec {
	return arangostore.BootstrapSpec{Collections: []arangostore.CollectionSpec{{Name: Collection, Indexes: [][]string{{"name"}, {"name", "translationVersion"}, {"digest"}}}}}
}

func (r *Registry) SaveRecipe(ctx context.Context, entry exec.Entry) error {
	return r.saveRecipe(ctx, entry)
}

func (r *Registry) ReplaceRecipeVersion(ctx context.Context, entry exec.Entry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	doc["name"], doc["translationVersion"], doc["digest"] = entry.Bundle.Name, entry.Bundle.TranslationVersion, entry.Digest
	replaced := false
	err = r.client.QueryRows(ctx, `LET used = FIRST(FOR execution IN @@executions
  FILTER (execution.name == @name OR execution.Name == @name)
    AND (execution.translationVersion == @translationVersion OR execution.TranslationVersion == @translationVersion)
  LIMIT 1 RETURN true)
FILTER used == null
REPLACE {_key: @key} WITH @document IN @@recipes
RETURN {replaced: true}`, r.batchSize, map[string]interface{}{
		"@executions": "loom_dataframe_bundle_executions", "@recipes": Collection,
		"name": entry.Bundle.Name, "translationVersion": entry.Bundle.TranslationVersion,
		"key": recipeDocumentKey(entry.Bundle.Name, entry.Bundle.TranslationVersion), "document": doc,
	}, func(map[string]any) error { replaced = true; return nil })
	if err != nil {
		return err
	}
	if !replaced {
		return fmt.Errorf("%w: %s@%s", exec.ErrRecipeVersionImmutable, entry.Bundle.Name, entry.Bundle.TranslationVersion)
	}
	return nil
}

func (r *Registry) saveRecipe(ctx context.Context, entry exec.Entry) error {
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

func (r *Registry) LoadRecipe(ctx context.Context, name string) (exec.Entry, error) {
	return r.loadRecipe(ctx, `FOR doc IN @@collection FILTER doc.name == @name SORT doc.translationVersion DESC LIMIT 1 RETURN doc`, map[string]interface{}{"@collection": Collection, "name": name})
}

func (r *Registry) LoadRecipeVersion(ctx context.Context, name, translationVersion string) (exec.Entry, error) {
	return r.loadRecipe(ctx, `FOR doc IN @@collection FILTER doc.name == @name AND doc.translationVersion == @translationVersion LIMIT 1 RETURN doc`, map[string]interface{}{"@collection": Collection, "name": name, "translationVersion": translationVersion})
}

func (r *Registry) loadRecipe(ctx context.Context, query string, vars map[string]interface{}) (exec.Entry, error) {
	var found *exec.Entry
	err := r.client.QueryRows(ctx, query, r.batchSize, vars, func(row map[string]any) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var entry exec.Entry
		if err := json.Unmarshal(data, &entry); err != nil {
			return err
		}
		found = &entry
		return nil
	})
	if err != nil {
		return exec.Entry{}, err
	}
	if found == nil {
		return exec.Entry{}, fmt.Errorf("%w", exec.ErrRecipeNotFound)
	}
	return *found, nil
}

func (r *Registry) RecipeVersionUsed(ctx context.Context, name, translationVersion string) (bool, error) {
	used := false
	err := r.client.QueryRows(ctx, `FOR doc IN @@collection FILTER (doc.name == @name OR doc.Name == @name) AND (doc.translationVersion == @translationVersion OR doc.TranslationVersion == @translationVersion) LIMIT 1 RETURN {used: true}`, r.batchSize, map[string]interface{}{"@collection": "loom_dataframe_bundle_executions", "name": name, "translationVersion": translationVersion}, func(map[string]any) error {
		used = true
		return nil
	})
	return used, err
}

func recipeDocumentKey(name, translationVersion string) string {
	sum := sha256.Sum256([]byte(name + "\x00" + translationVersion))
	return hex.EncodeToString(sum[:])
}

var _ exec.Store = (*Registry)(nil)
