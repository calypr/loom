package explorer

import (
	"encoding/json"
	explorerconfig "github.com/calypr/loom/internal/explorer/config"
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
		Config: mustJSON(t, explorerconfig.ConfigV2{
			APIVersion: explorerconfig.ConfigV2APIVersion,
			Kind:       "ExplorerConfig",
			Recipe:     mustJSON(t, bundle),
			Views: []explorerconfig.ConfigView{{
				ID: "observations", Output: "observations",
				Table: explorerconfig.ConfigTable{Columns: []explorerconfig.ConfigColumn{
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

	saved := &ProjectConfig{Views: []explorerconfig.ConfigView{{
		ID: "observations", Output: "observations",
		Table: explorerconfig.ConfigTable{Columns: []explorerconfig.ConfigColumn{
			{Column: "status", Label: "Saved status", Visible: true, Pinned: true},
		}},
	}}}
	runtime, err = buildViewerProjection(revision, saved)
	if err != nil {
		t.Fatal(err)
	}
	output := runtime.Outputs[0]
	if len(output.Table.Columns) != 1 || output.Table.Columns[0].Column != "status" || !output.Table.Columns[0].Pinned {
		t.Fatalf("runtime table does not match the saved view: %#v", output.Table.Columns)
	}
	if output.Columns[1].Label != "Saved status" {
		t.Fatalf("runtime status label = %q", output.Columns[1].Label)
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
