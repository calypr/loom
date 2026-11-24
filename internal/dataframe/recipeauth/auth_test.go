package recipeauth

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestRecipeAuthorizationReadBindsSavedSelectionToAuthorizedProject(t *testing.T) {
	authorizer := Authorization{}
	bindings, err := authorizer.AuthorizeRead(context.Background(), recipe.RuntimeBindings{
		Project: "HTAN_INT-project-a", DatasetGeneration: "generation-a",
		SelectionProject: "another/project", SelectionMembersCollection: "untrusted_collection",
	})
	if err != nil {
		t.Fatal(err)
	}
	if bindings.SelectionProject != "HTAN_INT/project-a" || bindings.SelectionMembersCollection != arangostore.SelectionMembersCollection {
		t.Fatalf("saved selection is not bound to the authorized project and server-owned store: project=%q collection=%q", bindings.SelectionProject, bindings.SelectionMembersCollection)
	}
	if bindings.Project != "HTAN_INT-project-a" || bindings.DatasetGeneration != "generation-a" || bindings.AuthScopeMode != authscope.ReadScopeUnrestricted {
		t.Fatalf("saved-selection binding changed resource scope: %#v", bindings)
	}
}
