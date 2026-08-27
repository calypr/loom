package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/gofiber/fiber/v3"
)

func TestExplorerStateRouteBuildsDefaultProjectionFromRecipe(t *testing.T) {
	store := newTestExplorerStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateEmptyInteractive(context.Background(), "project-a", "patients", "Patients", "test"); err != nil {
		t.Fatal(err)
	}
	selector := dataset.DataframeSelector{Recipe: "patients-query", TranslationVersion: "v1", Output: "patients"}
	revision := explorer.Revision{
		ID:               "revision-a",
		Project:          "project-a",
		ExplorerID:       "patients",
		Recipe:           recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: selector.Recipe, TranslationVersion: selector.TranslationVersion, Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "patient_id", FieldRef: "Patient.id"}}}}},
		SourceGeneration: "generation-a",
		Dataset: explorer.DatasetMetadata{Generation: "generation-a", Outputs: []explorer.DatasetOutput{{
			Name: "patients", State: "ACTIVE", Queryable: true, Selector: &selector,
			Columns: []publication.PhysicalColumn{{Name: "patient_id", LogicalType: "string", ClickHouse: "String"}},
		}}},
		EmittedColumns:       []explorer.EmittedColumn{{OutputID: "patients", EmissionID: "em_patient_id", PublicColumn: "patient_id", Label: "Patient ID", LogicalType: "string"}},
		PublicOutputContract: json.RawMessage(`{"outputs":[{"outputId":"patients","columns":[{"column":"patient_id","label":"Patient ID","logicalType":"string","filterable":false,"chartable":false}]}]}`),
		Publication:          explorer.PublicationMetadata{State: "ACTIVE", Generation: "generation-a"},
		Status:               explorer.RevisionActive,
	}
	store.mu.Lock()
	store.revisions[revision.ID] = revision
	owner := store.explorers[testExplorerKey("project-a", "patients")]
	owner.ActiveRevisionID = revision.ID
	store.explorers[testExplorerKey("project-a", "patients")] = owner
	store.mu.Unlock()

	app := fiber.New()
	RegisterExplorerLifecycleRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, ExplorerV2LifecycleConfig{})
	response := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/patients", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	var state explorer.ExplorerStateV1
	if err := json.Unmarshal([]byte(response.Body), &state); err != nil {
		t.Fatal(err)
	}
	if state.APIVersion != explorer.ExplorerStateV1APIVersion || state.Kind != explorer.ExplorerStateV1Kind {
		t.Fatalf("state identity = %q/%q", state.APIVersion, state.Kind)
	}
	if state.Runtime == nil {
		t.Fatalf("runtime projection is nil: %s", response.Body)
	}
	if state.Runtime.Status != "ACTIVE" || len(state.Runtime.Outputs) != 1 {
		t.Fatalf("runtime = %#v", state.Runtime)
	}
	output := state.Runtime.Outputs[0]
	if output.OutputID != "patients" || output.Selector != selector || len(output.Columns) != 1 || output.Columns[0].Column != "patient_id" {
		t.Fatalf("default output = %#v", output)
	}
	if !output.Columns[0].Visible || len(output.Table.Columns) != 1 || len(output.Filters) != 0 || len(output.Charts) != 0 {
		t.Fatalf("default presentation = %#v", output)
	}
}

func TestViewerProjectionDoesNotInventColumnsWithoutPublishedSchema(t *testing.T) {
	service, err := explorer.NewService(newTestExplorerStore())
	if err != nil {
		t.Fatal(err)
	}
	selector := dataset.DataframeSelector{Recipe: "patients-query", TranslationVersion: "v1", Output: "patients"}
	revision := explorer.Revision{
		Recipe: recipe.Bundle{
			RecipeSchemaVersion: recipe.CurrentSchemaVersion,
			Name:                selector.Recipe,
			TranslationVersion:  selector.TranslationVersion,
			Outputs:             []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient"}},
		},
		Dataset: explorer.DatasetMetadata{Outputs: []explorer.DatasetOutput{{Name: "patients", Selector: &selector}}},
		EmittedColumns: []explorer.EmittedColumn{{
			OutputID: "patients", EmissionID: "em_patient_id", PublicColumn: "patient_id", Label: "Patient ID", LogicalType: "string",
		}},
	}

	runtime := service.BuildViewerProjection(&revision)
	if runtime == nil || len(runtime.Outputs) != 1 {
		t.Fatalf("runtime = %#v", runtime)
	}
	output := runtime.Outputs[0]
	if len(output.Columns) != 0 {
		t.Fatalf("invented columns = %#v", output.Columns)
	}
}

func TestViewerProjectionRejectsAliasedPhysicalColumns(t *testing.T) {
	service, err := explorer.NewService(newTestExplorerStore())
	if err != nil {
		t.Fatal(err)
	}
	selector := dataset.DataframeSelector{Recipe: "patients-query", TranslationVersion: "v1", Output: "patients"}
	revision := explorer.Revision{
		Recipe: recipe.Bundle{
			RecipeSchemaVersion: recipe.CurrentSchemaVersion,
			Name:                selector.Recipe,
			TranslationVersion:  selector.TranslationVersion,
			Outputs: []recipe.Output{{
				Name: "patients", RootResourceType: "Patient", RowGrain: "patient",
				Fields: []recipe.Field{{Name: "patient_id", FieldRef: "Patient.id"}},
			}},
		},
		Dataset: explorer.DatasetMetadata{Outputs: []explorer.DatasetOutput{{
			Name: "patients", Selector: &selector,
			Columns: []publication.PhysicalColumn{{Name: "route_0__patient_id", LogicalType: "string", ClickHouse: "String"}},
		}}},
		EmittedColumns: []explorer.EmittedColumn{{
			OutputID: "patients", EmissionID: "em_patient_id", PublicColumn: "patient_id", Label: "Patient ID", LogicalType: "string",
		}},
	}

	runtime := service.BuildViewerProjection(&revision)
	if runtime == nil || len(runtime.Outputs) != 1 {
		t.Fatalf("runtime = %#v", runtime)
	}
	if len(runtime.Outputs[0].Columns) != 0 {
		t.Fatalf("aliased columns leaked = %#v", runtime.Outputs[0].Columns)
	}
}

func TestExplorerStateRouteReturnsExplicitUnpublishedProjection(t *testing.T) {
	service, err := explorer.NewService(newTestExplorerStore())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateEmptyInteractive(context.Background(), "project-a", "empty", "Empty", "test"); err != nil {
		t.Fatal(err)
	}

	app := fiber.New()
	RegisterExplorerLifecycleRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, ExplorerV2LifecycleConfig{})
	response := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/empty", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal([]byte(response.Body), &wire); err != nil {
		t.Fatal(err)
	}
	if raw, ok := wire["runtime"]; !ok || string(raw) != "null" {
		t.Fatalf("runtime = %s, want explicit null", raw)
	}
	var state explorer.ExplorerStateV1
	if err := json.Unmarshal([]byte(response.Body), &state); err != nil {
		t.Fatal(err)
	}
	if state.Runtime != nil || state.Generated.Publication.State != explorer.ExplorerRuntimeV1NotPublished {
		t.Fatalf("unpublished state = %#v", state)
	}
}
