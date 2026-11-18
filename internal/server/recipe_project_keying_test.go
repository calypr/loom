package server

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/projectid"
)

const (
	keyingPaddedProject = "  loom_dev_verify_zzz-padkey-7k  "
	keyingCleanProject  = "loom_dev_verify_zzz-padkey-7k"
)

func TestRecipeAuthorizationWritesBackTrimmedProject(t *testing.T) {
	authorizer := recipeAuthorization{}
	for _, leg := range []struct {
		name      string
		authorize func(context.Context, recipe.RuntimeBindings) (recipe.RuntimeBindings, error)
	}{
		{"read", authorizer.AuthorizeRead},
		{"write", authorizer.AuthorizeWrite},
	} {
		t.Run(leg.name, func(t *testing.T) {
			got, err := leg.authorize(context.Background(), recipe.RuntimeBindings{Project: keyingPaddedProject, DatasetGeneration: "generation-a"})
			if err != nil {
				t.Fatalf("%s: padded project rejected: %v", leg.name, err)
			}
			if got.Project != keyingCleanProject {
				t.Fatalf("%s: bindings.Project=%q want trimmed %q; downstream consumers key off this field (padded/clean divergence)", leg.name, got.Project, keyingCleanProject)
			}
			if got.SelectionProject != projectid.Canonical(keyingCleanProject) {
				t.Fatalf("%s: SelectionProject=%q want canonical of trimmed %q", leg.name, got.SelectionProject, keyingCleanProject)
			}

			gotBlank, blankErr := leg.authorize(context.Background(), recipe.RuntimeBindings{Project: " \t \n ", DatasetGeneration: "generation-a"})
			assertTypedProjectRequired(t, blankErr, leg.name+" blank-after-trim")
			if gotBlank.Project != "" || gotBlank.SelectionProject != "" || gotBlank.AuthScopeMode != "" {
				t.Fatalf("%s: guard must clear bindings on rejection, got %#v", leg.name, gotBlank)
			}
		})
	}
}

type keyingPathLister struct {
	projects []string
	paths    []string
}

func (l *keyingPathLister) list(_ context.Context, options catalog.AuthResourcePathOptions) ([]string, error) {
	l.projects = append(l.projects, options.Project)
	return l.paths, nil
}

func TestRecipeAuthorizationScopeResolutionSeesNormalizedProject(t *testing.T) {
	principal := &authscope.Principal{Subject: "keying-tester", AuthResourcePaths: []string{"myproj/padata"}}
	ctx := authscope.ContextWithPrincipal(context.Background(), principal)
	cleanScoped := "myproj/padata"
	wantListed := projectid.Legacy(projectid.Canonical(cleanScoped))
	for _, leg := range []struct {
		name    string
		isWrite bool
	}{
		{"read", false},
		{"write", true},
	} {
		for _, spelling := range []struct {
			name    string
			project string
		}{
			{"padded", "  " + cleanScoped + " "},
			{"clean", cleanScoped},
		} {
			t.Run(leg.name+" "+spelling.name, func(t *testing.T) {
				lister := &keyingPathLister{paths: []string{"myproj/padata"}}
				authorizer := recipeAuthorization{resolver: authscope.NewScopeResolver(authscope.ScopeResolverConfig{
					ListExistingAuthResourcePaths: lister.list,
				})}
				authorize := authorizer.AuthorizeRead
				if leg.isWrite {
					authorize = authorizer.AuthorizeWrite
				}
				got, err := authorize(ctx, recipe.RuntimeBindings{Project: spelling.project, DatasetGeneration: "generation-a"})
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
				blankAuthorizer := recipeAuthorization{resolver: authscope.NewScopeResolver(authscope.ScopeResolverConfig{
					ListExistingAuthResourcePaths: blankLister.list,
				})}
				blankAuthorize := blankAuthorizer.AuthorizeRead
				if leg.isWrite {
					blankAuthorize = blankAuthorizer.AuthorizeWrite
				}
				_, blankErr := blankAuthorize(ctx, recipe.RuntimeBindings{Project: "  \t "})
				assertTypedProjectRequired(t, blankErr, leg.name+" blank-after-trim scoped")
				if len(blankLister.projects) != 0 {
					t.Fatalf("guard must reject before scope resolution, got %#v", blankLister.projects)
				}
			})
		}
	}
}
