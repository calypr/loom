package compiler

import (
	"os"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/google/uuid"
)

func TestNestedExtensionColumnsKeepValuesUnderTheirAncestorURLsAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_TEST_ARANGO_URL") == "" || os.Getenv("LOOM_TEST_ARANGO_DATABASE") == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, client := openConstructionReshapeArango(t)
	project, generation := "loom_extension_ancestry_"+uuid.NewString(), "generation-extension-ancestry"
	insertConstructionReshapeRows(t, ctx, client, project, generation, []map[string]any{{
		"id": "shared-leaf",
		"extension": []any{
			map[string]any{"url": "urn:left", "extension": []any{map[string]any{"url": "urn:shared:leaf", "valueString": "left-only"}}},
			map[string]any{"url": "urn:right", "extension": []any{map[string]any{"url": "urn:shared:leaf", "valueString": "right-only"}}},
		},
	}})
	output := recipe.Output{
		Name: "Observation", RootResourceType: "Observation", RowGrain: "resource",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		ExtensionColumns: []recipe.ExtensionColumn{{
			Name: "extensions", ColumnPrefix: extensionStringPointer(""), MaxColumns: 4, Source: recipe.Expression{Select: "root.extension[]"},
			Columns: []recipe.ExtensionColumnMapping{
				{Name: "left__leaf", URL: "urn:shared:leaf", URLPath: []string{"urn:left"}, SourcePath: "extension[].extension[]", ValuePath: "valueString", ValueType: "string"},
				{Name: "right__leaf", URL: "urn:shared:leaf", URLPath: []string{"urn:right"}, SourcePath: "extension[].extension[]", ValuePath: "valueString", ValueType: "string"},
			},
		}},
	}
	rows := executeConstructionOutput(t, ctx, client, output, project, generation)
	if len(rows) != 1 {
		t.Fatalf("extension rows = %#v, want one source row", rows)
	}
	row := rows[0]
	if got := row["left__leaf"]; got != "left-only" {
		t.Errorf("left ancestor column = %#v, want left-only", got)
	}
	if got := row["right__leaf"]; got != "right-only" {
		t.Errorf("right ancestor column = %#v, want right-only", got)
	}
}
