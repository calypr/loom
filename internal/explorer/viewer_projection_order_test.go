package explorer

import (
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestBuildViewerProjectionPreservesPublicContractColumnOrder(t *testing.T) {
	emitted := []EmittedColumn{
		{EmissionID: "subject-emission", OutputID: "observations", PublicColumn: "subject.reference", Label: "Subject", LogicalType: "string", Shape: "scalar"},
		{EmissionID: "status-emission", OutputID: "observations", PublicColumn: "status", Label: "Status", LogicalType: "string", Shape: "scalar"},
	}
	contracts := PublicOutputContracts{Outputs: []PublicOutputContract{{
		OutputID: "observations",
		Columns: []PublicOutputColumn{
			{Column: "subject.reference", Label: "Subject", LogicalType: "string", Shape: "scalar"},
			{Column: "status", Label: "Status", LogicalType: "string", Shape: "scalar"},
		},
	}}}
	contractJSON, err := json.Marshal(contracts)
	if err != nil {
		t.Fatal(err)
	}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "viewer-order",
		TranslationVersion:  "1",
		Outputs: []recipe.Output{{
			Name: "observations", RootResourceType: "Observation", RowGrain: "observation",
		}},
	}
	revision := &Revision{
		Config: mustJSON(t, ConfigV2{
			APIVersion: ConfigV2APIVersion,
			Kind:       "ExplorerConfig",
			Recipe:     mustJSON(t, bundle),
			Views: []ConfigView{{
				ID: "observations", Output: "observations",
				Table: ConfigTable{Columns: []ConfigColumn{
					{Column: "status", Visible: true},
					{Column: "subject.reference", Visible: true},
				}},
			}},
		}),
		Recipe: bundle,
		Dataset: DatasetMetadata{Outputs: []DatasetOutput{{
			Name: "observations", Queryable: true,
			Columns: []publication.PhysicalColumn{
				{Name: "status", LogicalType: "string", ClickHouse: "String"},
				{Name: "subject.reference", LogicalType: "string", ClickHouse: "String"},
			},
		}}},
		EmittedColumns:       emitted,
		PublicOutputContract: contractJSON,
	}

	runtime, err := BuildViewerProjection(revision)
	if err != nil {
		t.Fatal(err)
	}
	if runtime == nil || len(runtime.Outputs) != 1 {
		t.Fatalf("runtime outputs = %#v", runtime)
	}
	columns := runtime.Outputs[0].Columns
	if len(columns) != 2 || columns[0].Column != "subject.reference" || columns[1].Column != "status" {
		t.Fatalf("runtime columns = %#v, want frozen public-contract order", columns)
	}
	table := runtime.Outputs[0].Table.Columns
	if len(table) != 2 || table[0].Column != "subject.reference" || table[1].Column != "status" {
		t.Fatalf("runtime table columns = %#v, want frozen public-contract order", table)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
