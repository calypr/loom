package dataframebuilder

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/discovery"
	"github.com/calypr/loom/internal/graphqlapi/model"
	"github.com/calypr/loom/internal/recipe"
)

type builderActiveManifestResolver struct {
	manifest dataset.Manifest
	projects []string
}

type builderActiveResourceAccess struct{}

func (builderActiveResourceAccess) GetAllowedResources(context.Context, string, string, string) ([]string, error) {
	return []string{"/programs/example/projects/allowed"}, nil
}

func (r *builderActiveManifestResolver) ResolveActiveManifest(_ context.Context, project string) (dataset.Manifest, error) {
	r.projects = append(r.projects, project)
	return r.manifest.Clone(), nil
}

func builderReadyManifest(t *testing.T, project, generation string) dataset.Manifest {
	t.Helper()
	schema, err := dataset.NewSchemaIdentitySnapshot(
		"urn:loom:dataframebuilder-active-test",
		"",
		strings.Repeat("b", 64),
		[]string{"Patient", "Specimen"},
	)
	if err != nil {
		t.Fatalf("NewSchemaIdentitySnapshot() error = %v", err)
	}
	ref, err := dataset.NewDatasetRef(project, generation)
	if err != nil {
		t.Fatalf("NewDatasetRef() error = %v", err)
	}
	manifest, err := dataset.NewManifest(ref, schema)
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	for _, state := range []dataset.ManifestState{
		dataset.ManifestStateLoading,
		dataset.ManifestStateAnalyzing,
		dataset.ManifestStateReady,
	} {
		manifest, err = manifest.Transition(state)
		if err != nil {
			t.Fatalf("Transition(%s) error = %v", state, err)
		}
	}
	return manifest
}

func TestActiveGenerationPropagatesThroughBuilderCatalogEntrypoints(t *testing.T) {
	const project = "project-a"
	const generation = "generation-a"
	active := &builderActiveManifestResolver{manifest: builderReadyManifest(t, project, generation)}
	var fieldCalls []catalog.PopulatedFieldOptions
	var referenceCalls []catalog.PopulatedReferenceOptions

	discoverFields := func(_ context.Context, options catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
		fieldCalls = append(fieldCalls, options)
		if options.PivotOnly {
			return []catalog.PopulatedField{}, nil
		}
		switch options.ResourceType {
		case "", "Patient":
			return []catalog.PopulatedField{{
				Project: project, ResourceType: "Patient", Path: "gender", Kind: "scalar", DocCount: 2,
				DistinctValues: []string{"female", "male"},
			}}, nil
		case "Specimen":
			return []catalog.PopulatedField{{Project: project, ResourceType: "Specimen", Path: "id", Kind: "scalar", DocCount: 1}}, nil
		default:
			return []catalog.PopulatedField{}, nil
		}
	}
	discoverReferences := func(_ context.Context, options catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
		referenceCalls = append(referenceCalls, options)
		return []catalog.PopulatedReference{{FromType: "Patient", Label: "subject_Patient", ToType: "Specimen", EdgeCount: 1}}, nil
	}
	dataframes := dataframe.NewService(dataframe.ServiceConfig{
		ActiveManifestResolver: active,
		DiscoverFields:         discoverFields,
		DiscoverReferences:     discoverReferences,
		ExecuteRows: func(_ context.Context, _ dataframe.ExecuteQueryOptions, _ string, bindVars map[string]any, _ func(map[string]any) error) error {
			if got := bindVars["dataset_generation"]; got != generation {
				t.Fatalf("dataframe execution generation = %#v, want %q", got, generation)
			}
			return nil
		},
	})
	service := NewService(Config{
		ActiveManifestResolver: active,
		DiscoverFields:         discoverFields,
		DiscoverReferences:     discoverReferences,
		Dataframes:             dataframes,
	})

	if _, err := service.Run(context.Background(), model.FhirDataframeInput{
		Project: project, RootResourceType: "Patient",
	}, nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := service.PrepareRunInput(context.Background(), model.FhirDataframeInput{
		Project: project, RootResourceType: "Patient",
	}); err != nil {
		t.Fatalf("PrepareRunInput() error = %v", err)
	}
	if _, err := service.Introspect(context.Background(), IntrospectionRequest{
		Project: project, RootResourceType: "Patient",
	}); err != nil {
		t.Fatalf("Introspect() error = %v", err)
	}
	if _, err := service.DiscoverGuided(context.Background(), GuidedDiscoveryRequest{Project: project}); err != nil {
		t.Fatalf("DiscoverGuided() error = %v", err)
	}

	facts := discovery.CatalogFacts{
		Project: project,
		Fields: []catalog.PopulatedField{{
			Project: project, ResourceType: "Patient", Path: "gender", Kind: "scalar", DocCount: 2,
			DistinctValues: []string{"female", "male"},
		}},
	}
	snapshot, err := discovery.BuildSnapshot(facts)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	if len(snapshot.Columns) != 1 {
		t.Fatalf("snapshot columns = %#v, want one gender capability", snapshot.Columns)
	}
	plan, err := service.PrepareRecipe(context.Background(), RecipeRequest{Recipe: recipe.Recipe{
		Version:          recipe.VersionV1,
		Template:         recipe.TemplatePatientCohort,
		TemplateVersion:  1,
		Project:          project,
		GenerationPolicy: recipe.GenerationLatest,
		Grain:            recipe.GrainPatient,
		Columns:          []recipe.ColumnSelection{{ID: string(snapshot.Columns[0].ID)}},
		Destination:      recipe.Destination{Type: recipe.DestinationPreview},
	}})
	if err != nil {
		t.Fatalf("PrepareRecipe() error = %v", err)
	}
	if plan.Builder.DatasetGeneration != generation {
		t.Fatalf("prepared recipe generation = %q, want %q", plan.Builder.DatasetGeneration, generation)
	}

	if len(active.projects) < 6 {
		t.Fatalf("active resolver calls = %#v, want one selection for each builder/dataframe entrypoint", active.projects)
	}
	if len(fieldCalls) == 0 || len(referenceCalls) == 0 {
		t.Fatalf("catalog calls fields=%d references=%d, want both", len(fieldCalls), len(referenceCalls))
	}
	for index, options := range fieldCalls {
		if options.Project != project || options.DatasetGeneration != generation {
			t.Fatalf("field catalog call %d = %+v, want %s/%s", index, options, project, generation)
		}
	}
	for index, options := range referenceCalls {
		if options.Project != project || options.DatasetGeneration != generation {
			t.Fatalf("reference catalog call %d = %+v, want %s/%s", index, options, project, generation)
		}
	}
}

func TestBuilderActiveGenerationKeepsRestrictedEmptyScopeForGuidedCatalog(t *testing.T) {
	const project = "project-a"
	const generation = "generation-a"
	scopeResolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ResourceAccess: builderActiveResourceAccess{},
		ListExistingAuthResourcePaths: func(_ context.Context, options catalog.AuthResourcePathOptions) ([]string, error) {
			if options.Project != project || options.DatasetGeneration != generation {
				t.Fatalf("generation-aware scope options = %+v, want %s/%s", options, project, generation)
			}
			return []string{"example-unrelated"}, nil
		},
	})
	assertRestricted := func(unrestricted *bool, paths []string, gotGeneration string) {
		if gotGeneration != generation || unrestricted == nil || *unrestricted || len(paths) != 0 {
			t.Fatalf("restricted empty catalog scope generation=%q unrestricted=%#v paths=%#v", gotGeneration, unrestricted, paths)
		}
	}
	service := NewService(Config{
		ActiveManifestResolver: &builderActiveManifestResolver{manifest: builderReadyManifest(t, project, generation)},
		ScopeResolver:          scopeResolver,
		DiscoverFields: func(_ context.Context, options catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			assertRestricted(options.AuthResourcePathsUnrestricted, options.AuthResourcePaths, options.DatasetGeneration)
			return []catalog.PopulatedField{}, nil
		},
		DiscoverReferences: func(_ context.Context, options catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
			assertRestricted(options.AuthResourcePathsUnrestricted, options.AuthResourcePaths, options.DatasetGeneration)
			return []catalog.PopulatedReference{}, nil
		},
	})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{AuthorizationHeader: "Bearer header.payload.signature"})
	if _, err := service.DiscoverGuided(ctx, GuidedDiscoveryRequest{Project: project}); err != nil {
		t.Fatalf("DiscoverGuided() error = %v", err)
	}
}
