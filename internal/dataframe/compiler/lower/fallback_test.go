package lower

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/testkit/arangotest"
)

func TestConstructionCodedPivotDisplayFallbackCardinalityAgainstArango(t *testing.T) {
	database := arangotest.Owned(t, "codeddisplay")
	client := database.Client
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}
	text := func(value string) *string { return &value }
	for _, test := range []struct {
		name                 string
		primary              *string
		displays             []string
		fallbackOnBlank      bool
		want                 string
		wantCardinalityError bool
	}{
		{name: "preferred_text", primary: text("preferred"), displays: []string{"ignored", "ignored"}, fallbackOnBlank: true, want: "preferred"},
		{name: "blank_primary", primary: text(" \t"), displays: []string{"fallback"}, fallbackOnBlank: true, want: "fallback"},
		{name: "empty_primary", primary: text(""), displays: []string{"fallback"}, fallbackOnBlank: true, want: "fallback"},
		{name: "missing_primary", displays: []string{"fallback"}, fallbackOnBlank: true, want: "fallback"},
		{name: "identical_repeated_fallback", displays: []string{"same", "same"}, fallbackOnBlank: true, wantCardinalityError: true},
		{name: "distinct_repeated_fallback", displays: []string{"first", "second"}, fallbackOnBlank: true, wantCardinalityError: true},
		{name: "generic_empty_primary", primary: text(""), displays: []string{"unused"}, want: ""},
		{name: "generic_blank_primary", primary: text(" \t"), displays: []string{"unused"}, want: " \t"},
	} {
		t.Run(test.name, func(t *testing.T) {
			project := "coded-display-" + test.name
			bindings := recipe.RuntimeBindings{Project: project, DatasetGeneration: "display-generation"}
			output := codedPivotDisplayOutput(test.fallbackOnBlank)
			bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "coded-display", TranslationVersion: "test", Outputs: []recipe.Output{output}}
			plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := recipeplan.ResolveRecipePlan(plan, project, bindings.DatasetGeneration)
			if err != nil {
				t.Fatal(err)
			}
			compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			rendered, err := aql.Render(compiled.Outputs[0].Plan, aql.Options{})
			if err != nil {
				t.Fatal(err)
			}
			concept := map[string]any{}
			if test.primary != nil {
				concept["text"] = *test.primary
			}
			codings := make([]any, 0, len(test.displays))
			for _, display := range test.displays {
				codings = append(codings, map[string]any{"system": "urn:result", "code": "answer", "display": display})
			}
			concept["coding"] = codings
			document, err := json.Marshal(map[string]any{
				"_key": test.name, "id": test.name, "resourceType": "Observation", "project": project,
				"dataset_generation": bindings.DatasetGeneration, "auth_resource_path": "/visible",
				"payload": map[string]any{"resourceType": "Observation", "id": test.name,
					"code":                 map[string]any{"coding": []any{map[string]any{"system": "urn:study", "code": "stage"}}},
					"valueCodeableConcept": concept},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := client.InsertBatchRaw(ctx, "Observation", []json.RawMessage{document}, false, "document"); err != nil {
				t.Fatal(err)
			}
			var rows []map[string]any
			err = client.QueryRows(ctx, rendered.Query, 10, rendered.BindVars, func(row map[string]any) error { rows = append(rows, row); return nil })
			if test.wantCardinalityError {
				if err == nil || !strings.Contains(err.Error(), "TABLE_PIVOT_CELL_CARDINALITY") {
					t.Fatalf("repeated fallback error = %v, want TABLE_PIVOT_CELL_CARDINALITY; rows=%#v", err, rows)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 {
				t.Fatalf("display rows = %#v, want one", rows)
			}
			value, ok := rows[0]["stage_display"].(string)
			if !ok || value != test.want {
				t.Fatalf("display = %#v, want %q", rows[0]["stage_display"], test.want)
			}
		})
	}
}
