package arango

import (
	"context"
	"strings"
	"testing"
	"time"

	storepkg "github.com/calypr/loom/internal/store/arango"
)

type migrationClient struct {
	created map[string]any
}

func (c *migrationClient) WithTransaction(ctx context.Context, _ storepkg.TransactionCollections, fn storepkg.TransactionFunc) error {
	return fn(ctx, c)
}

func (c *migrationClient) QueryRows(_ context.Context, query string, _ int, binds map[string]any, visit storepkg.RowVisitor) error {
	switch {
	case strings.Contains(query, "d.activeRevisionId != null"):
		return visit(map[string]any{
			"project": "project-a", "activeRevisionId": "revision-a", "intentDigest": "sha256:intent",
			"workspace": map[string]any{"apiVersion": "loom.calypr.org/explorer-authoring/v2", "kind": "ExplorerBuilderWorkspace", "explorer": map[string]any{"title": "Patients"}, "documents": []any{}, "tabs": []any{}},
			"updatedAt": time.Unix(10, 0).UTC(),
		})
	case strings.Contains(query, "d._key == @key AND d.project"):
		return nil
	case strings.Contains(query, "FILTER d._key == @key RETURN d"):
		return visit(map[string]any{
			"_key": "revision-a", "id": "revision-a", "project": "project-a", "explorerId": "default",
			"authoringBundle": map[string]any{"apiVersion": "loom.calypr.org/explorer-authoring/v2", "kind": "ExplorerBuilderWorkspace", "explorer": map[string]any{"title": "Fallback"}, "documents": []any{}, "tabs": []any{}},
			"intentDigest":    "sha256:intent", "status": "ACTIVE", "createdAt": time.Unix(5, 0).UTC(),
		})
	case strings.Contains(query, "INSERT @doc INTO"):
		c.created = binds["doc"].(map[string]any)
		return visit(c.created)
	default:
		return nil
	}
}

func TestMigrateLegacyRepositoryConfigCreatesOnlyCanonicalOwner(t *testing.T) {
	client := &migrationClient{}
	store, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateLegacyRepositoryConfigs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.created == nil {
		t.Fatal("legacy owner was not migrated")
	}
	for key, want := range map[string]any{"project": "project-a", "explorerId": "default", "title": "Patients", "managementMode": "REPOSITORY", "activeRevisionId": "revision-a"} {
		if got := client.created[key]; got != want {
			t.Fatalf("%s=%v, want %v", key, got, want)
		}
	}
	for _, duplicate := range []string{"activeConfig", "recipeDigest", "sourceGeneration", "materializations", "dataset", "publication", "diagnostics"} {
		if _, exists := client.created[duplicate]; exists {
			t.Fatalf("migrated owner retained generated field %q", duplicate)
		}
	}
}
