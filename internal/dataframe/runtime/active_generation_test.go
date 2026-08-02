package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	publication "github.com/calypr/loom/internal/dataset"
)

type dataframeActiveManifestResolver struct {
	manifest publication.Manifest
	err      error
	projects []string
}

func (r *dataframeActiveManifestResolver) ResolveActiveManifest(_ context.Context, project string) (publication.Manifest, error) {
	r.projects = append(r.projects, project)
	if r.err != nil {
		return publication.Manifest{}, r.err
	}
	return r.manifest, nil
}

func dataframeReadyManifest(t *testing.T, project, generationID string) publication.Manifest {
	t.Helper()
	schema, err := publication.NewSchemaSnapshot("urn:loom:dataframe-active-test", "", strings.Repeat("a", 64), []string{"Patient"})
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

func patientRecipe() recipe.Bundle {
	return recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "test", TranslationVersion: "test", Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "gender", Expr: recipe.Expression{Select: "gender"}}}}}}
}

func TestServiceActiveManifestPinsCompilerGeneration(t *testing.T) {
	const project, generation = "P1", "generation-a"
	active := &dataframeActiveManifestResolver{manifest: dataframeReadyManifest(t, project, generation)}
	// The runtime service accepts a pre-resolved unrestricted scope in this
	// recipe test; active-generation selection is the behavior under test.
	svc := NewService(ServiceConfig{
		ActiveManifestResolver: active,
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
	result, err := svc.Run(context.Background(), RunRequest{Recipe: patientRecipe(), Bindings: recipe.RuntimeBindings{Project: project, AuthScopeMode: authscope.ReadScopeUnrestricted}, Limit: 1})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.RowCount != 1 || len(active.projects) != 1 || active.projects[0] != project {
		t.Fatalf("result=%#v active projects=%#v", result, active.projects)
	}
}

func TestServiceRejectsRecipeGenerationThatConflictsWithActiveManifest(t *testing.T) {
	svc := NewService(ServiceConfig{ActiveManifestResolver: &dataframeActiveManifestResolver{manifest: dataframeReadyManifest(t, "P1", "generation-a")}})
	_, err := svc.Run(context.Background(), RunRequest{Recipe: patientRecipe(), Bindings: recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "generation-b"}})
	if !errors.Is(err, ErrActiveGenerationConflict) {
		t.Fatalf("Run() error = %v, want ErrActiveGenerationConflict", err)
	}
}
