package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataset"
)

type dataframeActiveManifestResolver struct {
	manifest dataset.Manifest
	err      error
	projects []string
}

func (r *dataframeActiveManifestResolver) ResolveActiveManifest(_ context.Context, project string) (dataset.Manifest, error) {
	r.projects = append(r.projects, project)
	if r.err != nil {
		return dataset.Manifest{}, r.err
	}
	return r.manifest.Clone(), nil
}

type dataframeActiveResourceAccess struct{}

func (dataframeActiveResourceAccess) GetAllowedResources(context.Context, string, string, string) ([]string, error) {
	return []string{"/programs/example/projects/allowed"}, nil
}

func dataframeReadyManifest(t *testing.T, project, generation string) dataset.Manifest {
	t.Helper()
	schema, err := dataset.NewSchemaIdentitySnapshot(
		"urn:loom:dataframe-active-test",
		"",
		strings.Repeat("a", 64),
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

func TestServiceActiveManifestPinsScopeCatalogAndCompilerGeneration(t *testing.T) {
	const project = "P1"
	const generation = "generation-a"
	active := &dataframeActiveManifestResolver{manifest: dataframeReadyManifest(t, project, generation)}
	var existingCalls []catalog.AuthResourcePathOptions
	var fieldCalls []catalog.PopulatedFieldOptions
	var referenceCalls []catalog.PopulatedReferenceOptions

	scopeResolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ResourceAccess: dataframeActiveResourceAccess{},
		ListExistingAuthResourcePaths: func(_ context.Context, options catalog.AuthResourcePathOptions) ([]string, error) {
			existingCalls = append(existingCalls, options)
			return []string{"example-allowed"}, nil
		},
	})
	svc := NewService(ServiceConfig{
		ActiveManifestResolver: active,
		ScopeResolver:          scopeResolver,
		DiscoverFields: func(_ context.Context, options catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			fieldCalls = append(fieldCalls, options)
			if options.PivotOnly {
				return []catalog.PopulatedField{}, nil
			}
			switch options.ResourceType {
			case "Patient":
				return []catalog.PopulatedField{{Project: project, ResourceType: "Patient", Path: "gender", Kind: "scalar"}}, nil
			case "Specimen":
				return []catalog.PopulatedField{{Project: project, ResourceType: "Specimen", Path: "id", Kind: "scalar"}}, nil
			default:
				return []catalog.PopulatedField{}, nil
			}
		},
		DiscoverReferences: func(_ context.Context, options catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
			referenceCalls = append(referenceCalls, options)
			return []catalog.PopulatedReference{{FromType: "Patient", Label: "subject_Patient", ToType: "Specimen", EdgeCount: 1}}, nil
		},
		ExecuteRows: func(_ context.Context, _ ExecuteQueryOptions, query string, binds map[string]any, visit func(map[string]any) error) error {
			if got := binds[datasetGenerationBindKey]; got != generation {
				t.Fatalf("compiled dataset generation bind = %#v, want %q", got, generation)
			}
			if !strings.Contains(query, "root.dataset_generation == @dataset_generation") {
				t.Fatalf("compiled query omitted generation scope:\n%s", query)
			}
			return visit(map[string]any{"_key": "patient-1", "gender": "female"})
		},
	})

	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
		AuthorizationHeader: "Bearer header.payload.signature",
	})
	result, err := svc.Run(ctx, RunRequest{Builder: Builder{
		Project:          project,
		RootResourceType: "Patient",
		Fields:           []FieldSelect{{Name: "gender", Select: "gender"}},
		Traversals: []TraversalStep{{
			Label:          "subject_Patient",
			ToResourceType: "Specimen",
			Alias:          "specimen",
			Fields:         []FieldSelect{{Name: "id", Select: "id"}},
		}},
	}, Limit: 1})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.RowCount != 1 {
		t.Fatalf("row count = %d, want 1", result.RowCount)
	}
	if len(active.projects) != 1 || active.projects[0] != project {
		t.Fatalf("active resolver projects = %#v, want %q", active.projects, project)
	}
	if len(existingCalls) != 1 || existingCalls[0].Project != project || existingCalls[0].DatasetGeneration != generation {
		t.Fatalf("scope existing-path calls = %#v, want %s/%s", existingCalls, project, generation)
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

func TestServiceActiveManifestKeepsRestrictedEmptyScopeWithinGeneration(t *testing.T) {
	const project = "P1"
	const generation = "generation-a"
	scopeResolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ResourceAccess: dataframeActiveResourceAccess{},
		ListExistingAuthResourcePaths: func(_ context.Context, options catalog.AuthResourcePathOptions) ([]string, error) {
			if options.DatasetGeneration != generation {
				t.Fatalf("scope generation = %q, want %q", options.DatasetGeneration, generation)
			}
			return []string{"example-unrelated"}, nil
		},
	})
	assertRestricted := func(options catalog.PopulatedFieldOptions) {
		if options.DatasetGeneration != generation {
			t.Fatalf("field generation = %q, want %q", options.DatasetGeneration, generation)
		}
		if options.AuthResourcePathsUnrestricted == nil || *options.AuthResourcePathsUnrestricted || len(options.AuthResourcePaths) != 0 {
			t.Fatalf("field restricted-empty options = %+v", options)
		}
	}
	svc := NewService(ServiceConfig{
		ActiveManifestResolver: &dataframeActiveManifestResolver{manifest: dataframeReadyManifest(t, project, generation)},
		ScopeResolver:          scopeResolver,
		DiscoverFields: func(_ context.Context, options catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			assertRestricted(options)
			return []catalog.PopulatedField{}, nil
		},
		DiscoverReferences: func(_ context.Context, options catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
			if options.DatasetGeneration != generation || options.AuthResourcePathsUnrestricted == nil || *options.AuthResourcePathsUnrestricted || len(options.AuthResourcePaths) != 0 {
				t.Fatalf("reference restricted-empty options = %+v", options)
			}
			return []catalog.PopulatedReference{}, nil
		},
		ExecuteRows: func(_ context.Context, _ ExecuteQueryOptions, _ string, binds map[string]any, _ func(map[string]any) error) error {
			if got := binds[datasetGenerationBindKey]; got != generation {
				t.Fatalf("compiled generation bind = %#v, want %q", got, generation)
			}
			if got, ok := binds["auth_resource_paths_unrestricted"].(bool); !ok || got {
				t.Fatalf("compiled unrestricted bind = %#v, want false", binds["auth_resource_paths_unrestricted"])
			}
			return nil
		},
	})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{AuthorizationHeader: "Bearer header.payload.signature"})
	if _, err := svc.Run(ctx, RunRequest{Builder: Builder{Project: project, RootResourceType: "Patient"}}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestServiceRejectsBuilderGenerationThatConflictsWithActiveManifest(t *testing.T) {
	catalogCalled := false
	svc := NewService(ServiceConfig{
		ActiveManifestResolver: &dataframeActiveManifestResolver{manifest: dataframeReadyManifest(t, "P1", "generation-a")},
		DiscoverFields: func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			catalogCalled = true
			return nil, nil
		},
	})

	_, err := svc.Run(context.Background(), RunRequest{Builder: Builder{
		Project:           "P1",
		DatasetGeneration: "generation-b",
		RootResourceType:  "Patient",
	}})
	if !errors.Is(err, ErrActiveGenerationConflict) {
		t.Fatalf("Run() error = %v, want ErrActiveGenerationConflict", err)
	}
	if catalogCalled {
		t.Fatal("catalog work occurred after active-generation conflict")
	}
}
