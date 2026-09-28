package compiler

import (
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestConstructionCodedGroupCountsDistinctRootCodingTuplesAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	project, generation := "loom_coded_group_"+t.Name(), "generation-coded-group"
	insertConstructionReshapeRows(t, ctx, client, project, generation, []map[string]any{
		{
			"id": "coded-a",
			"component": []any{map[string]any{"code": map[string]any{"coding": []any{
				map[string]any{"system": "system-a", "code": "shared", "display": "first"},
				map[string]any{"system": "system-a", "code": "shared", "display": "duplicate"},
				map[string]any{"system": "system-b", "code": "shared"},
				map[string]any{"system": "system-a"},
			}}}},
		},
		{
			"id": "coded-b",
			"component": []any{map[string]any{"code": map[string]any{"coding": []any{
				map[string]any{"system": "system-a", "code": "shared"},
			}}}},
		},
		{"id": "coded-missing"},
	})

	output := constructionCodedGroupTestOutput()
	rows := executeConstructionOutput(t, ctx, client, output, project, generation)
	if len(rows) != 3 {
		t.Fatalf("coded grouping rows = %#v, want system-a, system-b, and one missing tuple", rows)
	}
	rowIDs := make(map[string]bool, len(rows))
	for _, row := range rows {
		id, ok := row["__loom_row_id"].(string)
		if !ok || id == "" || rowIDs[id] {
			t.Errorf("coded grouping row ID is missing or duplicated: %#v", row["__loom_row_id"])
		}
		rowIDs[id] = true
		switch {
		case row["code_system"] == "system-a" && row["code"] == "shared":
			if !constructionNumericEqual(row["source_records"], 2) {
				t.Errorf("system-a/shared source count = %#v, want 2 roots despite duplicate Coding in one root", row["source_records"])
			}
		case row["code_system"] == "system-b" && row["code"] == "shared":
			if !constructionNumericEqual(row["source_records"], 1) {
				t.Errorf("system-b/shared source count = %#v, want 1", row["source_records"])
			}
		case row["code_system"] == nil && row["code"] == nil:
			if row["code_version"] != nil || !constructionNumericEqual(row["source_records"], 2) {
				t.Errorf("missing tuple = %#v, want one count per root with absent or incomplete Coding", row)
			}
		default:
			t.Errorf("unexpected coded grouping row: %#v", row)
		}
	}
	if repeated := executeConstructionOutput(t, ctx, client, output, project, generation); !reflect.DeepEqual(constructionRowIdentities(repeated), constructionRowIdentities(rows)) {
		t.Fatalf("coded grouping row IDs changed between executions: %#v then %#v", constructionRowIdentities(rows), constructionRowIdentities(repeated))
	}
}

func constructionCodedGroupTestOutput() recipe.Output {
	const idColumn = "resource_id"
	columns := []recipe.StageColumn{
		{ID: "system_id", Name: "code_system", Type: "string", Nullable: true},
		{ID: "version_id", Name: "code_version", Type: "string", Nullable: true},
		{ID: "code_id", Name: "code", Type: "string", Nullable: true},
		{ID: "count_id", Name: "source_records", Type: "integer"},
	}
	return recipe.Output{
		Name: "coded_group_rows", RootResourceType: "Observation", RowGrain: "resource",
		Fields: []recipe.Field{{Name: idColumn, ColumnID: idColumn, Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{{ID: idColumn, Name: idColumn}},
			Steps: []recipe.ConstructionStep{{
				ID: "group_codes", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionCodedGroupOp, CodedGroup: &recipe.ConstructionCodedGroup{
					ConstructionID: "group_codes",
					Source: recipe.ConstructionCodedGroupSource{
						OccurrenceID: "base", ResourceType: "Observation", CodingPath: "component[].code.coding[]",
						FHIRType: "Coding", Cardinality: "MANY", Shape: "ARRAY", Route: []recipe.ConstructionRelatedRouteStep{},
					},
					MissingKeyPolicy:     recipe.ConstructionGroupMissingKeyGroup,
					SystemOutputColumnID: "system_id", VersionOutputColumnID: "version_id",
					CodeOutputColumnID: "code_id", DistinctSourceCountOutputColumnID: "count_id",
				}}, Outputs: columns,
			}},
		},
	}
}
