package server

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	arangofactory "github.com/calypr/loom/internal/backends/arango"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestRecipeAuthorizationBindsSavedSelectionToAuthorizedProject(t *testing.T) {
	// Witness migrated by arch-backend-08: the stamp moved from a hardcoded
	// constant inside recipeAuthorization to the lane-selected policy field,
	// so this Arango-lane contract now constructs the authorizer exactly the
	// way the Arango lane composes it. The server-owned collection must
	// overwrite any caller-supplied value.
	authorizer := recipeAuthorization{selectionMembersCollection: arangofactory.SelectionMembersCollection}
	for name, authorize := range map[string]func(context.Context, recipe.RuntimeBindings) (recipe.RuntimeBindings, error){
		"read":  authorizer.AuthorizeRead,
		"write": authorizer.AuthorizeWrite,
	} {
		t.Run(name, func(t *testing.T) {
			bindings, err := authorize(context.Background(), recipe.RuntimeBindings{
				Project: "HTAN_INT-project-a", DatasetGeneration: "generation-a",
				SelectionProject: "another/project", SelectionMembersCollection: "untrusted_collection",
			})
			if err != nil {
				t.Fatal(err)
			}
			if bindings.SelectionProject != "HTAN_INT/project-a" || bindings.SelectionMembersCollection != arangofactory.SelectionMembersCollection {
				t.Fatalf("saved selection is not bound to the authorized project and server-owned store: project=%q collection=%q", bindings.SelectionProject, bindings.SelectionMembersCollection)
			}
			if bindings.Project != "HTAN_INT-project-a" || bindings.DatasetGeneration != "generation-a" || bindings.AuthScopeMode != authscope.ReadScopeUnrestricted {
				t.Fatalf("saved-selection binding changed resource scope: %#v", bindings)
			}
		})
	}
	// The PostgreSQL-policy contrast (arch-backend-08): the zero-value
	// construction used by both PostgreSQL lanes must also overwrite a
	// caller-supplied collection -- with the empty string, so an untrusted
	// collection name never survives on the PostgreSQL lane either.
	pgAuthorizer := recipeAuthorization{}
	for name, authorize := range map[string]func(context.Context, recipe.RuntimeBindings) (recipe.RuntimeBindings, error){
		"read":  pgAuthorizer.AuthorizeRead,
		"write": pgAuthorizer.AuthorizeWrite,
	} {
		t.Run("postgres_policy_"+name, func(t *testing.T) {
			bindings, err := authorize(context.Background(), recipe.RuntimeBindings{
				Project: "HTAN_INT-project-a", DatasetGeneration: "generation-a",
				SelectionProject: "another/project", SelectionMembersCollection: "untrusted_collection",
			})
			if err != nil {
				t.Fatal(err)
			}
			if bindings.SelectionMembersCollection != "" {
				t.Fatalf("the PostgreSQL policy must clear a caller-supplied selection-members collection, got %q", bindings.SelectionMembersCollection)
			}
			if bindings.SelectionProject != "HTAN_INT/project-a" {
				t.Fatalf("saved selection is not bound to the authorized project: %q", bindings.SelectionProject)
			}
		})
	}
}
