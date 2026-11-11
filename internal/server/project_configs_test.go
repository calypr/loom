package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
)

func TestLoadProjectConfigs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project-configs.json")
	if err := os.WriteFile(path, []byte(`{"apiVersion":"loom.calypr.org/project-configs/v1","kind":"ProjectConfigs","configs":[{"id":"files","title":"Files","recipeRef":{"name":"metadata"},"views":[{"id":"files","title":"Files","output":"DocumentReference"}]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	config := ServerConfig{ProjectConfigs: path, ClickHouse: ClickHouseConfig{Enabled: true}}
	got, err := loadProjectConfigs(config, "metadata")
	if err != nil || len(got.Configs) != 1 {
		t.Fatalf("catalog=%#v err=%v", got, err)
	}
	for _, tc := range []struct {
		name   string
		config ServerConfig
		recipe string
	}{
		{"disabled backend", ServerConfig{ProjectConfigs: path}, "metadata"},
		{"no recipe", config, ""},
		{"mismatched recipe", config, "other"},
		{"missing file", ServerConfig{ProjectConfigs: path + "missing", ClickHouse: ClickHouseConfig{Enabled: true}}, "metadata"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := loadProjectConfigs(tc.config, tc.recipe); err == nil {
				t.Fatal("expected startup rejection")
			}
		})
	}
	if _, err := loadProjectConfigs(ServerConfig{}, ""); err != nil {
		t.Fatal(err)
	}
}

func TestPersistedExplorerRequiresExecutionVisibility(t *testing.T) {
	store := newTestExplorerStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateInteractiveFrom(context.Background(), "project-a", "files", "Files", "", "test"); err != nil {
		t.Fatal(err)
	}
	revision := explorer.Revision{ID: "revision-a", Project: "project-a", ExplorerID: "files", Status: explorer.RevisionActive,
		Recipe:      recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "metadata", TranslationVersion: "v1", Outputs: []recipe.Output{{Name: "files", RootResourceType: "DocumentReference", RowGrain: "document"}}},
		Publication: explorer.PublicationMetadata{State: "ACTIVE", ExecutionID: "execution-a"},
	}
	store.mu.Lock()
	store.revisions[revision.ID] = revision
	owner := store.explorers[testExplorerKey("project-a", "files")]
	owner.ActiveRevisionID = revision.ID
	store.explorers[testExplorerKey("project-a", "files")] = owner
	store.mu.Unlock()
	handlers := newExplorerHTTPHandlers(authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service)
	for _, tc := range []struct {
		name    string
		failure error
		status  int
	}{
		{"visible", nil, 0},
		{"concealed", dataframeerrors.NewError(dataframeerrors.CodeRecipeExecutionNotFound, ""), 404},
		{"backend failure", dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, ""), 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			handlers.authorizeExecution = func(_ context.Context, id string) error {
				calls++
				if id != "execution-a" {
					t.Fatalf("execution %q", id)
				}
				return tc.failure
			}
			_, err := handlers.getExplorer(context.Background(), "project-a", "files")
			if calls != 1 {
				t.Fatalf("execution authorizer calls=%d err=%v", calls, err)
			}
			if tc.status == 0 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var failure *explorerLegacyOperationError
			if !errors.As(err, &failure) || failure.status != tc.status {
				t.Fatalf("failure=%v expected status %d", err, tc.status)
			}
		})
	}
}

func TestMissingProjectConfigIsNotCreatedOnRead(t *testing.T) {
	store := newTestExplorerStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	handlers := newExplorerHTTPHandlers(authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service)
	_, err = handlers.getExplorer(context.Background(), "project-a", "default")
	var failure *explorerLegacyOperationError
	if !errors.As(err, &failure) || failure.status != 404 {
		t.Fatalf("missing config response=%v", err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.explorers) != 0 || len(store.revisions) != 0 {
		t.Fatal("reading a missing config wrote state")
	}
}

func TestPersistedExplorerConcealsPartialExecutionScope(t *testing.T) {
	store := newTestExplorerStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateInteractiveFrom(context.Background(), "project-a", "files", "Files", "", "test"); err != nil {
		t.Fatal(err)
	}
	revision := explorer.Revision{ID: "revision-a", Project: "project-a", ExplorerID: "files", Status: explorer.RevisionActive,
		Recipe:      recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "metadata", TranslationVersion: "v1", Outputs: []recipe.Output{{Name: "files", RootResourceType: "DocumentReference", RowGrain: "document"}}},
		Publication: explorer.PublicationMetadata{State: "ACTIVE", ExecutionID: "execution-a"},
	}
	store.mu.Lock()
	store.revisions[revision.ID] = revision
	owner := store.explorers[testExplorerKey("project-a", "files")]
	owner.ActiveRevisionID = revision.ID
	store.explorers[testExplorerKey("project-a", "files")] = owner
	store.mu.Unlock()
	handlers := newExplorerHTTPHandlers(authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service)
	scopes := authscope.NewScopeResolver(authscope.ScopeResolverConfig{ListExistingAuthResourcePaths: func(context.Context, catalog.AuthResourcePathOptions) ([]string, error) {
		return []string{"allowed", "denied"}, nil
	}})
	execution := publication.BundleExecution{ID: "execution-a", BundleIdentity: publication.BundleIdentity{Project: "project-a", DatasetGeneration: "g", AuthResourcePaths: []string{"allowed", "denied"}}, State: publication.BundlePublished}
	for _, tc := range []struct {
		name      string
		principal *authscope.Principal
		resolver  *authscope.ScopeResolver
		status    int
	}{
		{name: "no-auth", status: 0},
		{name: "partial", principal: &authscope.Principal{Projects: []string{"project-a"}, AuthResourcePaths: []string{"allowed"}}, resolver: scopes, status: 404},
		{name: "nonmember", principal: &authscope.Principal{Projects: []string{"other"}}, status: 404},
		{name: "full", principal: &authscope.Principal{Projects: []string{"project-a"}, AuthResourcePaths: []string{"allowed", "denied"}}, resolver: scopes, status: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := graphresolver.NewAuthorizedRecipeExecutionReader(recipeExecutionCatalog{execution: execution}, tc.resolver)
			handlers.authorizeExecution = func(ctx context.Context, id string) error { _, err := reader(ctx, id); return err }
			ctx := authscope.ContextWithPrincipal(context.Background(), tc.principal)
			_, err := handlers.getExplorer(ctx, "project-a", "files")
			if tc.status == 0 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var failure *explorerLegacyOperationError
			if !errors.As(err, &failure) || failure.status != tc.status {
				t.Fatalf("err=%v failure=%#v expected=%d", err, failure, tc.status)
			}
		})
	}
}
