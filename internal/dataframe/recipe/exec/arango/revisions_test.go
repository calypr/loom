package arango

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type revisionClient struct {
	docs             []map[string]any
	insertBatchError error
}

func (c *revisionClient) InsertBatchRaw(_ context.Context, _ string, data []json.RawMessage, _ bool, _ string) error {
	if c.insertBatchError != nil {
		return c.insertBatchError
	}
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

func TestRevisionRegistryRejectsParentForFirstRevision(t *testing.T) {
	client := &revisionClient{}
	registry, err := NewRevisionRegistry(client)
	if err != nil {
		t.Fatal(err)
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "files", TranslationVersion: "1", Outputs: []recipe.Output{{Name: "DocumentReference", RootResourceType: "DocumentReference", RowGrain: "document_reference"}}}

	if _, err := registry.Register(context.Background(), "project-a", bundle, "missing"); err == nil {
		t.Fatal("Register accepted a parent for the first revision")
	}
	if len(client.docs) != 0 {
		t.Fatalf("Register persisted a dangling first revision: %#v", client.docs)
	}
}

func TestRevisionRegistryRequiresCurrentParentForLaterRevisions(t *testing.T) {
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

	bundle.TranslationVersion = "2"
	second, err := registry.Register(context.Background(), "project-a", bundle, first.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if second.Parent != first.Digest {
		t.Fatalf("second.Parent = %q, want %q", second.Parent, first.Digest)
	}

	bundle.TranslationVersion = "3"
	if _, err := registry.Register(context.Background(), "project-a", bundle, ""); err == nil {
		t.Fatal("Register accepted an empty parent for a later revision")
	}
	bundle.TranslationVersion = "4"
	if _, err := registry.Register(context.Background(), "project-a", bundle, first.Digest); err == nil {
		t.Fatal("Register accepted a stale parent for a later revision")
	}
}

func TestRevisionRegistryClassifiesRegisterFaults(t *testing.T) {
	registryWith := func() (*RevisionRegistry, *revisionClient) {
		client := &revisionClient{}
		registry, err := NewRevisionRegistry(client)
		if err != nil {
			t.Fatal(err)
		}
		return registry, client
	}
	valid := func(name string) recipe.Bundle {
		return recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: name, TranslationVersion: "1", Outputs: []recipe.Output{{Name: "DocumentReference", RootResourceType: "DocumentReference", RowGrain: "document_reference"}}}
	}
	userFault := func(t *testing.T, err error, want dataframeerrors.ErrorCode) dataframeerrors.UserError {
		t.Helper()
		if err == nil {
			t.Fatalf("expected typed %s, got nil", want)
		}
		userErr, ok := dataframeerrors.AsUserError(err)
		if !ok {
			t.Fatalf("expected typed %s, got untyped %v", want, err)
		}
		if userErr.Code() != string(want) {
			t.Fatalf("code=%s want %s (err=%v)", userErr.Code(), want, err)
		}
		return userErr
	}

	registry, client := registryWith()
	if _, err := registry.Register(context.Background(), "project-a", valid(""), ""); err == nil || userFault(t, err, dataframeerrors.CodeInvalidRequest) == nil {
		t.Fatal("missing recipe name not typed")
	}
	registry, client = registryWith()
	if _, err := registry.Register(context.Background(), "", valid("files"), ""); err == nil || userFault(t, err, dataframeerrors.CodeInvalidRequest) == nil {
		t.Fatal("missing project not typed")
	}

	registry, client = registryWith()
	if _, err := registry.Register(context.Background(), "project-a", valid("files"), ""); err != nil {
		t.Fatal(err)
	}
	stale := valid("files")
	stale.TranslationVersion = "2"
	_, staleErr := registry.Register(context.Background(), "project-a", stale, "wrong-digest")
	userErr := userFault(t, staleErr, dataframeerrors.CodeStaleCursor)
	if userErr.Retryable() {
		t.Fatal("stale parent error must not be retryable (matches sibling STALE_CURSOR defaults, internal/dataframe/published/errors.go:39)")
	}

	registry, client = registryWith()
	if _, err := registry.Register(context.Background(), "project-a", valid("files"), "some-digest"); err == nil || userFault(t, err, dataframeerrors.CodeInvalidRequest) == nil {
		t.Fatal("first revision with parent not typed")
	}
	if len(client.docs) != 0 {
		t.Fatalf("dangling first revision persisted: %#v", client.docs)
	}

	registry, client = registryWith()
	client.insertBatchError = errors.New("transport exploded")
	if _, err := registry.Register(context.Background(), "project-a", valid("files"), ""); err == nil {
		t.Fatal("InsertBatchRaw failure swallowed")
	} else if _, typed := dataframeerrors.AsUserError(err); typed {
		t.Fatalf("store transport fault must stay untyped (5xx semantics), got %#v", err)
	}
}
