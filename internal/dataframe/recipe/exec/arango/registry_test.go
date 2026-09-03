package arango

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type legacyRegistryClient struct {
	recipes    []map[string]any
	executions []map[string]any
}

func (c *legacyRegistryClient) InsertBatchRaw(_ context.Context, collection string, data []json.RawMessage, _ bool, _ string) error {
	if collection != Collection {
		return errors.New("unexpected collection")
	}
	for _, raw := range data {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return err
		}
		key := stringValue(doc["_key"])
		for i := range c.recipes {
			if stringValue(c.recipes[i]["_key"]) == key {
				c.recipes[i] = doc
				continue
			}
		}
		if !containsRecipeKey(c.recipes, key) {
			c.recipes = append(c.recipes, doc)
		}
	}
	return nil
}

func (c *legacyRegistryClient) QueryRows(_ context.Context, query string, _ int, vars map[string]interface{}, visit arangostore.RowVisitor) error {
	if strings.Contains(query, "RETURN {replaced: true}") {
		for _, execution := range c.executions {
			if matchesRecipeIdentity(execution, vars) {
				return nil
			}
		}
		key := stringValue(vars["key"])
		document, ok := vars["document"].(map[string]any)
		if !ok {
			return errors.New("replacement document has unexpected type")
		}
		for i := range c.recipes {
			if stringValue(c.recipes[i]["_key"]) == key {
				c.recipes[i] = document
				if err := visit(map[string]any{"replaced": true}); err != nil {
					return err
				}
				return nil
			}
		}
		return nil
	}
	collection := stringValue(vars["@collection"])
	if collection == "loom_dataframe_bundle_executions" {
		for _, doc := range c.executions {
			if !matchesRecipeIdentity(doc, vars) {
				continue
			}
			if strings.Contains(query, "RETURN {used: true}") {
				if err := visit(map[string]any{"used": true}); err != nil {
					return err
				}
				continue
			}
			if err := visit(map[string]any{"replaced": true}); err != nil {
				return err
			}
		}
		return nil
	}

	matching := make([]map[string]any, 0, len(c.recipes))
	for _, doc := range c.recipes {
		if !matchesRecipeIdentity(doc, vars) {
			continue
		}
		if version, ok := vars["translationVersion"]; ok && stringValue(doc["translationVersion"]) != stringValue(version) {
			continue
		}
		matching = append(matching, doc)
	}
	sort.Slice(matching, func(i, j int) bool {
		return stringValue(matching[i]["translationVersion"]) > stringValue(matching[j]["translationVersion"])
	})
	if len(matching) > 0 {
		if err := visit(matching[0]); err != nil {
			return err
		}
	}
	return nil
}

func containsRecipeKey(docs []map[string]any, key string) bool {
	for _, doc := range docs {
		if stringValue(doc["_key"]) == key {
			return true
		}
	}
	return false
}

func matchesRecipeIdentity(doc map[string]any, vars map[string]interface{}) bool {
	name := stringValue(vars["name"])
	version := stringValue(vars["translationVersion"])
	docName := stringValue(doc["name"])
	if docName == "" {
		docName = stringValue(doc["Name"])
	}
	docVersion := stringValue(doc["translationVersion"])
	if docVersion == "" {
		docVersion = stringValue(doc["TranslationVersion"])
	}
	return docName == name && (version == "" || docVersion == version)
}

func legacyEntry(version, digest, rowGrain string) exec.Entry {
	return exec.Entry{
		Bundle: recipe.Bundle{
			RecipeSchemaVersion: recipe.CurrentSchemaVersion,
			Name:                "files",
			TranslationVersion:  version,
			Outputs: []recipe.Output{{
				Name:             "DocumentReference",
				RootResourceType: "DocumentReference",
				RowGrain:         rowGrain,
			}},
		},
		Digest: digest,
	}
}

func TestRegistrySaveAndLoadRecipes(t *testing.T) {
	client := &legacyRegistryClient{}
	registry, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	first := legacyEntry("1", "digest-1", "document_reference")
	latest := legacyEntry("2", "digest-2", "document_reference")
	if err := registry.SaveRecipe(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := registry.SaveRecipe(context.Background(), latest); err != nil {
		t.Fatal(err)
	}

	loaded, err := registry.LoadRecipe(context.Background(), "files")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Digest != latest.Digest {
		t.Fatalf("LoadRecipe() digest = %q, want %q", loaded.Digest, latest.Digest)
	}
	loaded, err = registry.LoadRecipeVersion(context.Background(), "files", "1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Digest != first.Digest {
		t.Fatalf("LoadRecipeVersion() digest = %q, want %q", loaded.Digest, first.Digest)
	}
	if _, err := registry.LoadRecipeVersion(context.Background(), "files", "missing"); !errors.Is(err, exec.ErrRecipeNotFound) {
		t.Fatalf("LoadRecipeVersion() error = %v, want ErrRecipeNotFound", err)
	}
}

func TestRegistryReplaceRecipeVersionAndUsage(t *testing.T) {
	client := &legacyRegistryClient{}
	registry, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	original := legacyEntry("1", "digest-1", "document_reference")
	replacement := legacyEntry("1", "digest-2", "document_reference_changed")
	if err := registry.SaveRecipe(context.Background(), original); err != nil {
		t.Fatal(err)
	}
	used, err := registry.RecipeVersionUsed(context.Background(), "files", "1")
	if err != nil {
		t.Fatal(err)
	}
	if used {
		t.Fatal("RecipeVersionUsed() = true before an execution exists")
	}
	if err := registry.ReplaceRecipeVersion(context.Background(), replacement); err != nil {
		t.Fatal(err)
	}
	loaded, err := registry.LoadRecipeVersion(context.Background(), "files", "1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Digest != replacement.Digest || loaded.Bundle.Outputs[0].RowGrain != replacement.Bundle.Outputs[0].RowGrain {
		t.Fatalf("replacement was not loaded: %#v", loaded)
	}

	client.executions = []map[string]any{{"Name": "files", "TranslationVersion": "1"}}
	used, err = registry.RecipeVersionUsed(context.Background(), "files", "1")
	if err != nil {
		t.Fatal(err)
	}
	if !used {
		t.Fatal("RecipeVersionUsed() = false after an execution exists")
	}
	if err := registry.ReplaceRecipeVersion(context.Background(), original); !errors.Is(err, exec.ErrRecipeVersionImmutable) {
		t.Fatalf("ReplaceRecipeVersion() error = %v, want ErrRecipeVersionImmutable", err)
	}
}
