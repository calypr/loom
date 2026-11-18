package explorer

import (
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestProjectConfigsForSourcePublicationSeedsActualOutputsOnce(t *testing.T) {
	snapshot := Revision{Project: "project-a", Recipe: recipe.Bundle{
		Name: "metadata-v1",
		Outputs: []recipe.Output{
			{Name: "Metadata"},
			{Name: "Files"},
		},
	}}
	configs, expected, err := ProjectConfigsForSourcePublication(nil, "metadata-v1", false, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 {
		t.Fatalf("initial configs = %d, want one generic user config", len(configs))
	}
	if len(configs[0].Views) != len(snapshot.Recipe.Outputs) {
		t.Fatalf("initial tabs = %#v, want one for every compiled recipe output", configs[0].Views)
	}
	for index, output := range snapshot.Recipe.Outputs {
		view := configs[0].Views[index]
		if view.Output != output.Name || view.ID != output.Name {
			t.Fatalf("initial tab %d = %#v, want output %q", index, view, output.Name)
		}
	}
	if got := expected[configs[0].ID]; got != "" {
		t.Fatalf("initial config expected revision = %q, want absent", got)
	}

	configs, expected, err = ProjectConfigsForSourcePublication(nil, "metadata-v1", true, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 0 || len(expected) != 0 {
		t.Fatalf("anchored empty catalog reseeded configs=%#v expected=%#v", configs, expected)
	}

	owners := []Explorer{
		{Project: "project-a", ExplorerID: "metadata-v1"},
		{Project: "project-a", ExplorerID: "metadata-v1-views"},
	}
	configs, expected, err = ProjectConfigsForSourcePublication(owners, "metadata-v1", false, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 || configs[0].ID != "metadata-v1-views-2" || expected[configs[0].ID] != "" {
		t.Fatalf("initial config collided with another Explorer: configs=%#v expected=%#v", configs, expected)
	}
}

func TestProjectConfigsForSourcePublicationPreservesStoredDefinitions(t *testing.T) {
	saved := ProjectConfig{
		ID: "my-view", Title: "My view", RecipeRef: ProjectConfigRecipeRef{Name: "metadata-v1"},
		SharedFilters: map[string][]SharedFilter{
			"status": {{Output: "Metadata", Column: "status"}},
		},
		Views: []ConfigView{
			{
				ID: "metadata-tab", Title: "Chosen fields", Output: "Metadata",
				Table:        ConfigTable{Columns: []ConfigColumn{{Column: "status", Label: "Current status", Visible: true, Pinned: true}}},
				Filters:      []ConfigFilter{{Column: "status", Label: "Status filter"}},
				Charts:       []ConfigChart{{Column: "status", Type: "pie", Title: "Status"}},
				FixedFilters: map[string][]string{"status": {"active"}},
			},
			{ID: "files-tab", Title: "My files", Output: "Files"},
		},
	}
	owners := []Explorer{
		{Project: "project-a", ExplorerID: "my-view", ActiveRevisionID: "revision-7", ManagementMode: ManagementRepository, ProjectConfig: &saved},
		{Project: "project-a", ExplorerID: "unrelated", ActiveRevisionID: "revision-3", ManagementMode: ManagementRepository,
			ProjectConfig: &ProjectConfig{ID: "unrelated", Title: "Other", RecipeRef: ProjectConfigRecipeRef{Name: "other-recipe"}}},
	}
	snapshot := Revision{Project: "project-a", Recipe: recipe.Bundle{Name: "metadata-v1", Outputs: []recipe.Output{{Name: "Metadata"}, {Name: "Files"}}}}
	configs, expected, err := ProjectConfigsForSourcePublication(owners, "metadata-v1", false, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 || !reflect.DeepEqual(configs[0], saved) {
		t.Fatalf("source publication changed stored config: got %#v want %#v", configs, saved)
	}
	if got := expected["my-view"]; got != "revision-7" {
		t.Fatalf("expected config revision = %q, want revision-7", got)
	}
	if _, exists := expected["unrelated"]; exists {
		t.Fatalf("included another recipe's config head: %#v", expected)
	}
}
