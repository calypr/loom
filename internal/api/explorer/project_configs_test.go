package explorerapi

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/testkit/explorerfixtures"

	recipeexec "github.com/calypr/loom/internal/api/dataframe/recipeexec"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
)

type recipeExecutionCatalogFixture struct {
	execution publication.BundleExecution
}

func (c recipeExecutionCatalogFixture) GetExecution(context.Context, string) (publication.BundleExecution, error) {
	return c.execution, nil
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
		Publication: viewer.PublicationMetadata{State: "ACTIVE", ExecutionID: "execution-a"},
	}
	store.Mu.Lock()
	store.Revisions[revision.ID] = revision
	owner := store.Explorers[explorerfixtures.Key("project-a", "files")]
	owner.ActiveRevisionID = revision.ID
	store.Explorers[explorerfixtures.Key("project-a", "files")] = owner
	store.Mu.Unlock()
	handlers := newExplorerHTTPHandlers(authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service)
	for _, tc := range []struct {
		name    string
		failure error
		status  int
	}{
		{"visible", nil, 0},
		{"concealed", dataframeerrors.NewError(dataframeerrors.CodeRecipeExecutionNotFound, ""), 404},
		{"backend failure", dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, ""), 503},
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
	store.Mu.Lock()
	defer store.Mu.Unlock()
	if len(store.Explorers) != 0 || len(store.Revisions) != 0 {
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
		Publication: viewer.PublicationMetadata{State: "ACTIVE", ExecutionID: "execution-a"},
	}
	store.Mu.Lock()
	store.Revisions[revision.ID] = revision
	owner := store.Explorers[explorerfixtures.Key("project-a", "files")]
	owner.ActiveRevisionID = revision.ID
	store.Explorers[explorerfixtures.Key("project-a", "files")] = owner
	store.Mu.Unlock()
	handlers := newExplorerHTTPHandlers(authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service)
	scopes := authscope.NewScopeResolver(authscope.ScopeResolverConfig{ListExistingAuthResourcePaths: func(context.Context, authscope.AuthResourcePathOptions) ([]string, error) {
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
			reader := recipeexec.NewRecipeExecutionAuthorizer(recipeExecutionCatalogFixture{execution: execution}, tc.resolver)
			handlers.authorizeExecution = reader
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
