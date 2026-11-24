package arango

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/recipe"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

type legacyRegistryClient struct {
	recipes         []map[string]any
	executions      []map[string]any
	queries         []string
	afterUsageQuery func()
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
		key := arangostore.StringValue(doc["_key"])
		for i := range c.recipes {
			if arangostore.StringValue(c.recipes[i]["_key"]) == key {
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
	c.queries = append(c.queries, query)
	if strings.Contains(query, "RETURN {replaced: true}") {
		for _, execution := range c.executions {
			if matchesRecipeIdentity(execution, vars) {
				return nil
			}
		}
		key := arangostore.StringValue(vars["key"])
		document, ok := vars["document"].(map[string]any)
		if !ok {
			return errors.New("replacement document has unexpected type")
		}
		for i := range c.recipes {
			if arangostore.StringValue(c.recipes[i]["_key"]) == key {
				c.recipes[i] = document
				if err := visit(map[string]any{"replaced": true}); err != nil {
					return err
				}
				return nil
			}
		}
		return nil
	}
	collection := arangostore.StringValue(vars["@collection"])
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
		if strings.Contains(query, "RETURN {used: true}") && c.afterUsageQuery != nil {
			afterUsageQuery := c.afterUsageQuery
			c.afterUsageQuery = nil
			afterUsageQuery()
		}
		return nil
	}

	matching := make([]map[string]any, 0, len(c.recipes))
	for _, doc := range c.recipes {
		if !matchesRecipeIdentity(doc, vars) {
			continue
		}
		if version, ok := vars["translationVersion"]; ok && arangostore.StringValue(doc["translationVersion"]) != arangostore.StringValue(version) {
			continue
		}
		matching = append(matching, doc)
	}
	sort.Slice(matching, func(i, j int) bool {
		return arangostore.StringValue(matching[i]["translationVersion"]) > arangostore.StringValue(matching[j]["translationVersion"])
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
		if arangostore.StringValue(doc["_key"]) == key {
			return true
		}
	}
	return false
}

func matchesRecipeIdentity(doc map[string]any, vars map[string]interface{}) bool {
	name := arangostore.StringValue(vars["name"])
	version := arangostore.StringValue(vars["translationVersion"])
	docName := arangostore.StringValue(doc["name"])
	if docName == "" {
		docName = arangostore.StringValue(doc["Name"])
	}
	docVersion := arangostore.StringValue(doc["translationVersion"])
	if docVersion == "" {
		docVersion = arangostore.StringValue(doc["TranslationVersion"])
	}
	return docName == name && (version == "" || docVersion == version)
}

func legacyEntry(version, digest, rowGrain string) recipe.Entry {
	return recipe.Entry{
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
	if _, err := registry.LoadRecipeVersion(context.Background(), "files", "missing"); !errors.Is(err, recipe.ErrRecipeNotFound) {
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
	if err := registry.ReplaceRecipeVersion(context.Background(), original); !errors.Is(err, recipe.ErrRecipeVersionImmutable) {
		t.Fatalf("ReplaceRecipeVersion() error = %v, want ErrRecipeVersionImmutable", err)
	}
}

func TestRecipeVersionUsageReadsBothStoredSpellings(t *testing.T) {
	for name, execution := range map[string]map[string]any{
		"current": {"name": "files", "translationVersion": "1"},
		"legacy":  {"Name": "files", "TranslationVersion": "1"},
	} {
		t.Run(name, func(t *testing.T) {
			client := &legacyRegistryClient{executions: []map[string]any{execution}}
			registry, err := New(client)
			if err != nil {
				t.Fatal(err)
			}
			used, err := registry.RecipeVersionUsed(context.Background(), "files", "1")
			if err != nil {
				t.Fatal(err)
			}
			if !used {
				t.Fatalf("RecipeVersionUsed() = false for %s spelling", name)
			}
			if len(client.queries) != 1 || !strings.Contains(client.queries[0], "doc.Name == @name") || !strings.Contains(client.queries[0], "doc.TranslationVersion == @translationVersion") {
				t.Fatalf("publication usage query dropped a legacy spelling: %v", client.queries)
			}
		})
	}
}

func TestRecipeVersionReplacementKeepsAtomicUsageGuard(t *testing.T) {
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
	client.afterUsageQuery = func() {
		client.executions = append(client.executions, map[string]any{"Name": "files", "TranslationVersion": "1"})
	}
	_, err = (recipe.PersistentRegistry{Store: registry}).RegisterVersion(context.Background(), replacement.Bundle)
	if !errors.Is(err, recipe.ErrRecipeVersionImmutable) {
		t.Fatalf("RegisterVersion() error = %v, want ErrRecipeVersionImmutable", err)
	}
	loaded, err := registry.LoadRecipeVersion(context.Background(), "files", "1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Digest != original.Digest {
		t.Fatalf("recipe changed after a concurrent execution appeared: digest = %q, want %q", loaded.Digest, original.Digest)
	}
}

func TestRecipeRegistryReadRoundTripAgainstArango(t *testing.T) {
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL to run recipe registry against Arango")
	}
	database := "loom_cleanup_wp4_recipe_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := arangostore.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = client.Close(context.Background())
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		request, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(database), nil)
		if err != nil {
			t.Errorf("build database cleanup request: %v", err)
			return
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Errorf("delete test database %s: %v", database, err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("delete test database %s returned HTTP %d", database, response.StatusCode)
		}
	}()
	if err := client.Bootstrap(ctx, BootstrapSpec()); err != nil {
		t.Fatal(err)
	}
	registry, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	first := legacyEntry("1", "digest-1", "document_reference")
	latest := legacyEntry("2", "digest-2", "document_reference")
	if err := registry.SaveRecipe(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := registry.SaveRecipe(ctx, latest); err != nil {
		t.Fatal(err)
	}
	got, err := registry.LoadRecipe(ctx, "files")
	if err != nil || got.Digest != latest.Digest {
		t.Fatalf("LoadRecipe() = %q, %v; want latest digest %q", got.Digest, err, latest.Digest)
	}
	got, err = registry.LoadRecipeVersion(ctx, "files", "1")
	if err != nil || got.Digest != first.Digest {
		t.Fatalf("LoadRecipeVersion() = %q, %v; want first digest %q", got.Digest, err, first.Digest)
	}
	if _, err := registry.LoadRecipeVersion(ctx, "files", "missing"); !errors.Is(err, recipe.ErrRecipeNotFound) {
		t.Fatalf("LoadRecipeVersion(missing) error = %v, want ErrRecipeNotFound", err)
	}

	if err := client.Bootstrap(ctx, RevisionBootstrapSpec()); err != nil {
		t.Fatal(err)
	}
	revisions, err := NewRevisionRegistry(client)
	if err != nil {
		t.Fatal(err)
	}
	bundle1 := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "revisions", TranslationVersion: "1", Outputs: []recipe.Output{{Name: "DocumentReference", RootResourceType: "DocumentReference", RowGrain: "document_reference"}}}
	revision1, err := revisions.Register(ctx, "project", bundle1, "")
	if err != nil {
		t.Fatal(err)
	}
	bundle2 := bundle1
	bundle2.TranslationVersion = "2"
	revision2, err := revisions.Register(ctx, "project", bundle2, revision1.Digest)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := revisions.Get(ctx, "project", "revisions", revision2.Digest)
	if err != nil || loaded.Digest != revision2.Digest {
		t.Fatalf("RevisionRegistry.Get() = %q, %v; want %q", loaded.Digest, err, revision2.Digest)
	}
	if _, err := revisions.Get(ctx, "project", "revisions", "missing"); !errors.Is(err, recipe.ErrRecipeRevisionNotFound) {
		t.Fatalf("RevisionRegistry.Get(missing) error = %v, want ErrRecipeRevisionNotFound", err)
	}
	listed, err := revisions.List(ctx, "project", "revisions")
	if err != nil || len(listed) != 2 || listed[0].Digest != revision2.Digest || listed[1].Digest != revision1.Digest {
		t.Fatalf("RevisionRegistry.List() = %#v, %v; want both decoded revisions newest first", listed, err)
	}
}
