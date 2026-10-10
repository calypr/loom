package compiler

import (
	"context"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/google/uuid"
)

func TestExpandedItemFirstAndAllValuesAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	project, generation := "loom_expanded_item_"+uuid.NewString(), "generation-expanded-item"
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.ExecuteAQL(cleanupCtx,
			"FOR document IN Observation FILTER document.project == @project REMOVE document IN Observation",
			map[string]any{"project": project},
		); err != nil {
			t.Errorf("remove expanded-item fixtures: %v", err)
		}
	})

	insertConstructionReshapeRows(t, ctx, client, project, generation, []map[string]any{
		{
			"id": "o-codes", "resourceType": "Observation",
			"component": []any{map[string]any{"code": map[string]any{"coding": []any{
				map[string]any{"system": "https://example.test", "code": "days_to_collection"},
				map[string]any{"system": "https://example.test", "code": "specimen_type"},
				map[string]any{"system": "https://example.test", "code": "primary_disease_type"},
			}}}},
		},
		{
			"id": "o-empty", "resourceType": "Observation",
			"component": []any{map[string]any{"code": map[string]any{"coding": []any{}}}},
		},
	})

	output := recipe.Output{
		Name: "ExpandedObservationCodings", RootResourceType: "Observation", RootOccurrenceID: "observation-root", RowGrain: "expanded",
		Fields: []recipe.Field{
			{Name: "observation_id", FieldRef: "id", Expr: recipe.Expression{Select: "root.id"}, ValueMode: recipe.ValueModeFirst},
			{Name: "first_code", FieldRef: "component[].code.coding[].code", Expr: recipe.Expression{Select: "item.code"}, ValueMode: recipe.ValueModeFirst},
			{Name: "all_codes", FieldRef: "component[].code.coding[].code", Expr: recipe.Expression{Select: "item.code"}, ValueMode: recipe.ValueModeAll},
		},
		Expand:   &recipe.Expansion{OwnerOccurrenceID: "observation-root", From: recipe.Expression{Select: "root.component[].code.coding[]"}, As: "item", Ordinality: "position", EmptyPolicy: recipe.ExpansionPreserveParent},
		Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
	}
	_, query, err := compileReshapeOracle(output, project, generation, 20)
	if err != nil {
		t.Fatal(err)
	}
	rows := executeReshapeOracleQuery(t, ctx, client, query)
	if len(rows) != 4 {
		t.Fatalf("expanded rows = %d, want three coding items and one preserved empty row: %#v", len(rows), rows)
	}
	want := []string{"days_to_collection", "specimen_type", "primary_disease_type"}
	position := 0
	emptyRows := 0
	for _, row := range rows {
		id, _ := row["observation_id"].(string)
		switch id {
		case "o-codes":
			if position >= len(want) || row["first_code"] != want[position] {
				t.Fatalf("FIRST item %d = %#v, want %q (row=%#v)", position, row["first_code"], want[position], row)
			}
			values, ok := row["all_codes"].([]any)
			if !ok || len(values) != 1 || values[0] != want[position] {
				t.Fatalf("ALL item %d = %#v, want one-item array [%q]", position, row["all_codes"], want[position])
			}
			position++
		case "o-empty":
			if row["first_code"] != nil {
				t.Errorf("FIRST on preserved empty item = %#v, want null", row["first_code"])
			}
			values, ok := row["all_codes"].([]any)
			if !ok || len(values) != 0 {
				t.Errorf("ALL on preserved empty item = %#v, want empty array", row["all_codes"])
			}
			emptyRows++
		default:
			t.Fatalf("unexpected expanded output row: %#v", row)
		}
	}
	if position != len(want) || emptyRows != 1 {
		t.Fatalf("per-item rows consumed %d codes and %d preserved empty rows; want %d and 1", position, emptyRows, len(want))
	}
}
