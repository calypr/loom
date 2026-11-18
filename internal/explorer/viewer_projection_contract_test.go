package explorer_test

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
)

func TestViewerProjectionDoesNotInventColumnsWithoutPublishedSchema(t *testing.T) {
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

	runtime, err := explorer.BuildViewerProjection(&revision)
	if err != nil {
		t.Fatal(err)
	}
	if runtime == nil || len(runtime.Outputs) != 1 {
		t.Fatalf("runtime = %#v", runtime)
	}
	output := runtime.Outputs[0]
	if len(output.Columns) != 0 {
		t.Fatalf("invented columns = %#v", output.Columns)
	}
}

func TestViewerProjectionRejectsAliasedPhysicalColumns(t *testing.T) {
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

	runtime, err := explorer.BuildViewerProjection(&revision)
	if err != nil {
		t.Fatal(err)
	}
	if runtime == nil || len(runtime.Outputs) != 1 {
		t.Fatalf("runtime = %#v", runtime)
	}
	if len(runtime.Outputs[0].Columns) != 0 {
		t.Fatalf("aliased columns leaked = %#v", runtime.Outputs[0].Columns)
	}
}

func TestViewerProjectionKeepsRecipeOnlyLegacyFallback(t *testing.T) {
	selector := dataset.DataframeSelector{Recipe: "legacy-query", TranslationVersion: "v1", Output: "patients"}
	revision := explorer.Revision{
		Recipe:  recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: selector.Recipe, TranslationVersion: selector.TranslationVersion, Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient"}}},
		Dataset: explorer.DatasetMetadata{Outputs: []explorer.DatasetOutput{{Name: "patients", Selector: &selector}}}, Status: explorer.RevisionActive,
	}
	runtime, err := explorer.BuildViewerProjection(&revision)
	if err != nil || runtime == nil || len(runtime.Outputs) != 1 || runtime.Outputs[0].OutputID != "patients" {
		t.Fatalf("legacy runtime=%#v, err=%v", runtime, err)
	}
}
