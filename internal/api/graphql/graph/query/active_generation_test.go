package queryapi

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/loom/generated/graphql/graph/model"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/runtime"
	publication "github.com/calypr/loom/internal/dataset"
)

type builderActiveManifestResolver struct {
	manifest publication.Manifest
	projects []string
}

func (r *builderActiveManifestResolver) ResolveActiveManifest(_ context.Context, project string) (publication.Manifest, error) {
	r.projects = append(r.projects, project)
	return r.manifest, nil
}

func builderReadyManifest(t *testing.T, project, generationID string) publication.Manifest {
	t.Helper()
	schema, err := publication.NewSchemaSnapshot(
		"urn:loom:dataframebuilder-active-test",
		"",
		strings.Repeat("b", 64),
		[]string{"Patient", "Specimen"},
	)
	if err != nil {
		t.Fatalf("NewSchemaIdentitySnapshot() error = %v", err)
	}
	ref, err := publication.NewRef(project, generationID)
	if err != nil {
		t.Fatalf("NewDatasetRef() error = %v", err)
	}
	manifest, err := publication.NewManifest(ref, schema)
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	manifest, err = manifest.Transition(publication.StateReady)
	if err != nil {
		t.Fatalf("Transition(READY) error = %v", err)
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
	dataframes := runtime.NewService(runtime.ServiceConfig{
		ActiveManifestResolver: active,
		QueryRows: func(_ context.Context, _ string, _ int, bindVars map[string]any, _ func(map[string]any) error) error {
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
	if len(active.projects) < 3 {
		t.Fatalf("active resolver calls = %#v, want one selection for each live dataframe entrypoint", active.projects)
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
