package runtime

import (
	"context"
	"github.com/calypr/loom/internal/explorer/selection"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipeauth"

	"github.com/calypr/loom/internal/testkit/recipefixtures"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/projectid"
)

const (
	keyingCleanProject = "loom_dev_verify_zzz-padkey-7k"
)

type keyingPathLister struct {
	projects []string
	paths    []string
}

func (l *keyingPathLister) list(_ context.Context, options authscope.AuthResourcePathOptions) ([]string, error) {
	l.projects = append(l.projects, options.Project)
	return l.paths, nil
}

func TestRecipeAuthorizationScopeResolutionSeesNormalizedProject(t *testing.T) {
	principal := &authscope.Principal{Subject: "keying-tester", AuthResourcePaths: []string{"myproj/padata"}}
	ctx := authscope.ContextWithPrincipal(context.Background(), principal)
	cleanScoped := "myproj/padata"
	wantListed := projectid.Legacy(projectid.Canonical(cleanScoped))
	for _, spelling := range []struct {
		name    string
		project string
	}{
		{"padded", "  " + cleanScoped + " "},
		{"clean", cleanScoped},
	} {
		t.Run(spelling.name, func(t *testing.T) {
			lister := &keyingPathLister{paths: []string{"myproj/padata"}}
			authorizer := recipeauth.NewAuthorization(authscope.NewScopeResolver(authscope.ScopeResolverConfig{
				ListExistingAuthResourcePaths: lister.list,
			}), selection.MembersCollection)

			got, err := authorizer.AuthorizeRead(ctx, recipe.RuntimeBindings{Project: spelling.project, DatasetGeneration: "generation-a"})
			if err != nil {
				t.Fatalf("%s: %v", spelling.name, err)
			}
			if got.Project != cleanScoped {
				t.Fatalf("bindings.Project=%q want %q (padded/clean divergence)", got.Project, cleanScoped)
			}
			if len(lister.projects) != 1 {
				t.Fatalf("scope resolution consult count=%d want 1 (%#v)", len(lister.projects), lister.projects)
			}
			if lister.projects[0] != wantListed {
				t.Fatalf("scope resolver keyed %q want %q for both spellings", lister.projects[0], wantListed)
			}

			blankLister := &keyingPathLister{paths: []string{"myproj/padata"}}
			blankAuthorizer := recipeauth.NewAuthorization(authscope.NewScopeResolver(authscope.ScopeResolverConfig{
				ListExistingAuthResourcePaths: blankLister.list,
			}), selection.MembersCollection)

			_, blankErr := blankAuthorizer.AuthorizeRead(ctx, recipe.RuntimeBindings{Project: "  \t "})
			recipefixtures.AssertTypedProjectRequired(t, blankErr, "read blank-after-trim scoped")
			if len(blankLister.projects) != 0 {
				t.Fatalf("guard must reject before scope resolution, got %#v", blankLister.projects)
			}
		})
	}
}
