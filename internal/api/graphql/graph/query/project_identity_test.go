package queryapi

import (
	"context"
	"reflect"
	"testing"

	"github.com/calypr/loom/generated/graphql/graph/model"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
)

func TestProjectFormsShareOneShotCatalogAndIntrospectionIdentity(t *testing.T) {
	const (
		canonical  = "HTAN_INT/BForePC"
		legacy     = "HTAN_INT-BForePC"
		generation = "generation-1"
		scopePath  = "HTAN_INT-BForePC"
	)

	active := &builderActiveManifestResolver{manifest: builderReadyManifest(t, canonical, generation)}
	var fieldCalls []catalog.PopulatedFieldOptions
	var referenceCalls []catalog.PopulatedReferenceOptions
	var scopeCatalogCalls []catalog.AuthResourcePathOptions
	scopeResolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ListExistingAuthResourcePaths: func(_ context.Context, options catalog.AuthResourcePathOptions) ([]string, error) {
			scopeCatalogCalls = append(scopeCatalogCalls, options)
			return []string{scopePath}, nil
		},
	})

	checkScope := func(unrestricted *bool, paths []string) {
		t.Helper()
		if unrestricted == nil || *unrestricted {
			t.Fatalf("catalog scope bypass = %#v, want explicit restricted mode", unrestricted)
		}
		if !reflect.DeepEqual(paths, []string{scopePath}) {
			t.Fatalf("catalog auth paths = %#v, want %#v", paths, []string{scopePath})
		}
	}
	service := NewService(Config{
		ScopeResolver:          scopeResolver,
		ActiveManifestResolver: active,
		DiscoverFields: func(_ context.Context, options catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			fieldCalls = append(fieldCalls, options)
			if options.Project != canonical || options.DatasetGeneration != generation {
				t.Fatalf("field catalog identity = %+v, want %s/%s", options, canonical, generation)
			}
			checkScope(options.AuthResourcePathsUnrestricted, options.AuthResourcePaths)
			return []catalog.PopulatedField{{Project: canonical, DatasetGeneration: generation, ResourceType: options.ResourceType, Path: "gender", Kind: "scalar"}}, nil
		},
		DiscoverReferences: func(_ context.Context, options catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
			referenceCalls = append(referenceCalls, options)
			if options.Project != canonical || options.DatasetGeneration != generation {
				t.Fatalf("reference catalog identity = %+v, want %s/%s", options, canonical, generation)
			}
			checkScope(options.AuthResourcePathsUnrestricted, options.AuthResourcePaths)
			return []catalog.PopulatedReference{}, nil
		},
		Dataframes: dataframeexecution.NewService(dataframeexecution.ServiceConfig{
			QueryRows: func(_ context.Context, _ string, _ int, bindVars map[string]any, _ func(map[string]any) error) error {
				if unrestricted, ok := bindVars["auth_resource_paths_unrestricted"].(bool); !ok || unrestricted {
					t.Fatalf("dataframe scope bypass = %#v, want false", bindVars["auth_resource_paths_unrestricted"])
				}
				if !reflect.DeepEqual(bindVars["auth_resource_paths"], []string{scopePath}) {
					t.Fatalf("dataframe auth paths = %#v, want %#v", bindVars["auth_resource_paths"], []string{scopePath})
				}
				return nil
			},
		}),
	})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
		Subject:           "user",
		AuthResourcePaths: []string{scopePath},
	})

	for _, project := range []string{canonical, legacy} {
		t.Run(project, func(t *testing.T) {
			if _, err := service.Run(ctx, model.FhirDataframeInput{Project: project, RootResourceType: "Patient"}, nil); err != nil {
				t.Fatalf("Run(%q) error = %v", project, err)
			}
			response, err := service.Introspect(ctx, IntrospectionRequest{Project: project, RootResourceType: "Patient"})
			if err != nil {
				t.Fatalf("Introspect(%q) error = %v", project, err)
			}
			if response.Project != canonical || !reflect.DeepEqual(response.AuthResourcePaths, []string{scopePath}) {
				t.Fatalf("Introspect(%q) response = %+v, want canonical project and restricted path", project, response)
			}
		})
	}

	if len(scopeCatalogCalls) != 1 || scopeCatalogCalls[0].Project != legacy || scopeCatalogCalls[0].DatasetGeneration != generation {
		t.Fatalf("scope catalog calls = %#v, want one legacy storage lookup", scopeCatalogCalls)
	}
	if len(active.projects) != 4 {
		t.Fatalf("active resolver projects = %#v, want one canonical lookup per Run/Introspect", active.projects)
	}
	for _, project := range active.projects {
		if project != canonical {
			t.Fatalf("active resolver project = %q, want %q", project, canonical)
		}
	}
	if len(fieldCalls) == 0 || len(referenceCalls) != 2 {
		t.Fatalf("catalog call counts = fields:%d references:%d, want fields and one reference call per introspection", len(fieldCalls), len(referenceCalls))
	}
}
