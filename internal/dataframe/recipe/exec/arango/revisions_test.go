package arango

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type revisionClient struct{ docs []map[string]any }

func (c *revisionClient) InsertBatchRaw(_ context.Context, _ string, data []json.RawMessage, _ bool, _ string) error {
	for _, raw := range data {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return err
		}
		key := doc["_key"]
		replaced := false
		for i := range c.docs {
			if c.docs[i]["_key"] == key {
				c.docs[i] = doc
				replaced = true
				break
			}
		}
		if !replaced {
			c.docs = append(c.docs, doc)
		}
	}
	return nil
}

func (c *revisionClient) QueryRows(_ context.Context, query string, _ int, vars map[string]interface{}, visit arangostore.RowVisitor) error {
	for _, doc := range c.docs {
		if doc["project"] != vars["project"] || doc["name"] != vars["name"] {
			continue
		}
		if digest, ok := vars["digest"]; ok && doc["digest"] != digest {
			continue
		}
		if strings.Contains(query, "RETURN doc.digest") {
			if err := visit(map[string]any{"digest": doc["digest"]}); err != nil {
				return err
			}
			continue
		}
		if err := visit(doc); err != nil {
			return err
		}
	}
	return nil
}

func TestRevisionRegistryRoundTrip(t *testing.T) {
	client := &revisionClient{}
	registry, err := NewRevisionRegistry(client)
	if err != nil {
		t.Fatal(err)
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "files", TranslationVersion: "1", Outputs: []recipe.Output{{Name: "DocumentReference", RootResourceType: "DocumentReference", RowGrain: "document_reference"}}}
	first, err := registry.Register(context.Background(), "project-a", bundle, "")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := registry.Get(context.Background(), "project-a", "files", first.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Digest != first.Digest || loaded.Project != "project-a" || loaded.Bundle.Name != "files" {
		t.Fatalf("loaded=%+v", loaded)
	}
	list, err := registry.List(context.Background(), "project-a", "files")
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%d err=%v", len(list), err)
	}
	if _, err := registry.Get(context.Background(), "project-b", "files", first.Digest); err != recipe.ErrRecipeRevisionNotFound {
		t.Fatalf("cross-project err=%v", err)
	}
}
