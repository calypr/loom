package server

import (
	"context"
	"testing"

	"github.com/calypr/loom/generated/graphql/graph/model"
	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/projectid"
)

// Padded spelling of the live RED project (verification/publication-bundle/
// evidence/recipe-identity-keying). The keying contract is surrounding-space
// trimming only: the registry key keeps the raw trimmed spelling, so a
// boundary canonicalization (program-project rewrite) would break these
// assertions and is deliberately not the contract.
const (
	keyingPaddedProject = "  loom_dev_verify_zzz-padkey-7k  "
	keyingCleanProject  = "loom_dev_verify_zzz-padkey-7k"
	keyingDigest        = "sha256:keying-one-key"
)

const (
	keyingBundleFiles    = `{"recipeSchemaVersion":1,"name":"files","translationVersion":"1","fragments":{"fragments":{}},"outputs":[]}`
	keyingBundlePatients = `{"recipeSchemaVersion":1,"name":"patients","translationVersion":"1","fragments":{"fragments":{}},"outputs":[]}`
)

// keyingRevisionStore mirrors a key-exact store: it keys solely by the
// project argument it receives, so padded and clean spellings become two
// revision sets unless the boundary normalizes first.
type keyingRevisionStore struct {
	registeredProjects []string
	listedProjects     []string
	gotProjects        []string
	revisions          []recipe.RecipeRevision
}

func (s *keyingRevisionStore) Register(_ context.Context, project string, bundle recipe.Bundle, parent string) (recipe.RecipeRevision, error) {
	s.registeredProjects = append(s.registeredProjects, project)
	value := recipe.RecipeRevision{Project: project, Name: bundle.Name, Digest: keyingDigest, Parent: parent, Bundle: bundle}
	s.revisions = append(s.revisions, value)
	return value, nil
}

func (s *keyingRevisionStore) Get(_ context.Context, project, name, digest string) (recipe.RecipeRevision, error) {
	s.gotProjects = append(s.gotProjects, project)
	for _, value := range s.revisions {
		if value.Project == project && value.Name == name && value.Digest == digest {
			return value, nil
		}
	}
	return recipe.RecipeRevision{}, recipe.ErrRecipeRevisionNotFound
}

func (s *keyingRevisionStore) List(_ context.Context, project, name string) ([]recipe.RecipeRevision, error) {
	s.listedProjects = append(s.listedProjects, project)
	var values []recipe.RecipeRevision
	for _, value := range s.revisions {
		if value.Project == project && value.Name == name {
			values = append(values, value)
		}
	}
	return values, nil
}

func keyingResolver(store *keyingRevisionStore) graphresolver.Resolver {
	return *graphresolver.NewResolver(graphresolver.ResolverConfig{
		RecipeRevisions:  store,
		RecipeAuthorizer: recipeAuthorization{},
	})
}

func assertKeyedOnce(t *testing.T, keys []string, want string, site string) {
	t.Helper()
	if len(keys) != 1 {
		t.Fatalf("%s: consult count=%d want 1 (%#v)", site, len(keys), keys)
	}
	if keys[0] != want {
		t.Fatalf("%s: store consulted with project %q want %q (padded/clean divergence)", site, keys[0], want)
	}
}

// The live RED: register under the padded spelling and query with the clean
// spelling. Both spellings must address ONE revision set, keyed clean at
// every register/Get/List consult.
func TestRecipeRevisionResolversAddressOneProjectKey(t *testing.T) {
	store := &keyingRevisionStore{}
	resolver := keyingResolver(store)
	ctx := context.Background()

	registered, err := resolver.Mutation().RegisterDataframeRecipeRevision(ctx, model.RegisterDataframeRecipeRevisionInput{
		ProjectID: keyingPaddedProject,
		Recipe:    []byte(keyingBundleFiles),
	})
	if err != nil {
		t.Fatalf("register padded: %v", err)
	}
	if registered.ProjectID != keyingCleanProject {
		t.Fatalf("register padded: model project=%q want %q", registered.ProjectID, keyingCleanProject)
	}
	assertKeyedOnce(t, store.registeredProjects, keyingCleanProject, "register padded")

	listedPadded, err := resolver.Query().DataframeRecipeRevisions(ctx, keyingPaddedProject, "files")
	if err != nil {
		t.Fatalf("revisions padded: %v", err)
	}
	listedClean, err := resolver.Query().DataframeRecipeRevisions(ctx, keyingCleanProject, "files")
	if err != nil {
		t.Fatalf("revisions clean: %v", err)
	}
	if len(store.listedProjects) != 2 {
		t.Fatalf("revisions consult count=%d want 2 (%#v)", len(store.listedProjects), store.listedProjects)
	}
	for _, key := range store.listedProjects {
		if key != keyingCleanProject {
			t.Fatalf("revisions consult with project %q want %q (padded/clean divergence)", key, keyingCleanProject)
		}
	}
	if len(listedPadded) != 1 || listedPadded[0].Digest != keyingDigest {
		t.Fatalf("padded spelling must address the registered set, got %#v", listedPadded)
	}
	if len(listedClean) != len(listedPadded) {
		t.Fatalf("clean spelling sees %d revisions, padded sees %d: same project keyed two ways", len(listedClean), len(listedPadded))
	}

	revision, err := resolver.Query().DataframeRecipeRevision(ctx, keyingPaddedProject, "files", keyingDigest)
	if err != nil {
		t.Fatalf("revision padded: %v", err)
	}
	if revision == nil || revision.ProjectID != keyingCleanProject || revision.Digest != keyingDigest {
		t.Fatalf("revision padded: got %#v want project %q digest %q", revision, keyingCleanProject, keyingDigest)
	}
	keys := store.gotProjects[len(store.gotProjects)-1:]
	assertKeyedOnce(t, keys, keyingCleanProject, "revision padded")

	// Valid clean path still works and folds onto the same key.
	second, err := resolver.Mutation().RegisterDataframeRecipeRevision(ctx, model.RegisterDataframeRecipeRevisionInput{
		ProjectID: keyingCleanProject,
		Recipe:    []byte(keyingBundlePatients),
	})
	if err != nil {
		t.Fatalf("register clean: %v", err)
	}
	if second.Name != "patients" {
		t.Fatalf("register clean: name=%q", second.Name)
	}
	for i, key := range store.registeredProjects {
		if key != keyingCleanProject {
			t.Fatalf("register consult %d used project %q want %q (padded/clean divergence)", i, key, keyingCleanProject)
		}
	}
	patients, err := resolver.Query().DataframeRecipeRevisions(ctx, keyingPaddedProject, "patients")
	if err != nil {
		t.Fatalf("revisions patients: %v", err)
	}
	if len(patients) != 1 || patients[0].Name != "patients" {
		t.Fatalf("padded query must address clean-keyed patients set, got %#v", patients)
	}
}

// The authorizer must hand every downstream consumer (scope resolution,
// SelectionProject derivation, engine, registry) one key:
// bindings.Project normalized to the trimmed spelling, while the guard keeps
// rejecting blank-after-trim with the typed PROJECT_REQUIRED error.
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

// Scope resolution receives the same spelling the registry keys off: the
// padded and clean inputs must produce an identical store consult.
// (listExistingPaths canonicalizes internally, so byte equality there is a
// contract lock, not a mutation detector; the detector is bindings.Project.)
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
