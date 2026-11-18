package arango

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

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
	if _, err := registry.LoadRecipeVersion(ctx, "files", "missing"); !errors.Is(err, exec.ErrRecipeNotFound) {
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
