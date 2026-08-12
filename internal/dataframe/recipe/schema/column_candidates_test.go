package schema

import (
	"context"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestSelectedColumnModeMakesEmptyAndSubsetAuthoritative(t *testing.T) {
	empty := recipe.Bundle{RecipeSchemaVersion: 1, Name: "selected", TranslationVersion: "1", Outputs: []recipe.Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "patient", DynamicColumns: []recipe.DynamicColumn{{Name: "ids", Source: recipe.Expression{Select: "root.identifier[]"}, Key: &recipe.Expression{Select: "item.system"}, Value: &recipe.Expression{Select: "item.value"}, Columns: []string{}, ColumnMode: recipe.ColumnModeSelected, MaxColumns: 8}}}}}
	discovery := fakeDiscovery{fields: []FieldCandidate{{ResourceType: "Patient", Path: "identifier[].system", DistinctValues: []string{"a", "b"}}}}
	resolved, err := Resolve(context.Background(), empty, Scope{}, discovery)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved.Bundle.Outputs[0].DynamicColumns[0].Columns; got == nil || len(got) != 0 {
		t.Fatalf("selected empty columns = %#v", got)
	}
	empty.Outputs[0].DynamicColumns[0].Columns = []string{"b"}
	resolved, err = Resolve(context.Background(), empty, Scope{}, discovery)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved.Bundle.Outputs[0].DynamicColumns[0].Columns; !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("selected subset = %#v", got)
	}
	for _, selected := range [][]string{{}, {"b"}, {"a", "b"}} {
		empty.Outputs[0].DynamicColumns[0].Columns = selected
		resolved, err = Resolve(context.Background(), empty, Scope{}, discovery)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := semantic.BuildRecipePlan(resolved.Bundle, recipe.RuntimeBindings{})
		if err != nil {
			t.Fatal(err)
		}
		frozen, err := semantic.ResolveRecipePlan(plan, "scope", "")
		if err != nil {
			t.Fatal(err)
		}
		got := 0
		for _, columns := range frozen.ResolvedColumns {
			got += len(columns)
		}
		if got != len(selected) {
			t.Fatalf("selected %v produced %d columns", selected, got)
		}
	}
}

func TestRecipeColumnCandidatesAreSemanticKeysNotExampleValues(t *testing.T) {
	emptyPrefix := ""
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "columns", TranslationVersion: "1", Outputs: []recipe.Output{{Name: "files", RootResourceType: "DocumentReference", RowGrain: "file", DynamicColumns: []recipe.DynamicColumn{{Name: "category", ColumnPrefix: &emptyPrefix, Source: recipe.Expression{Select: "root.category[].coding[]"}, Key: &recipe.Expression{Select: "item.code"}, Value: &recipe.Expression{Select: "item.display"}, Columns: []string{"FILE_FORMAT"}, ColumnMode: recipe.ColumnModeSelected, MaxColumns: 8}}}}}
	fields := []FieldCandidate{{ResourceType: "DocumentReference", Path: "category[].coding[].code", Kind: "code", Population: 9, DistinctValues: []string{"FILE_FORMAT", "PLATFORM"}, SemanticObservations: []SemanticObservation{{SourcePath: "category[]", KeySystem: "urn:htan", KeyCode: "FILE_FORMAT", ValueSelector: "category[].coding[].display", ValueType: "string", Cardinality: "single", Population: 6, Examples: []string{"NDPI", "OME-TIFF", "SVS"}}, {SourcePath: "category[]", KeySystem: "urn:htan", KeyCode: "PLATFORM", ValueSelector: "category[].coding[].display", ValueType: "string", Cardinality: "single", Population: 3, Examples: []string{"Leica"}}}}}
	candidates, err := ColumnCandidates(bundle, "files", nil, fields)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidate count=%d: %#v", len(candidates), candidates)
	}
	byName := map[string]ColumnCandidate{}
	for _, candidate := range candidates {
		byName[candidate.PublicName] = candidate
	}
	format := byName["FILE_FORMAT"]
	if !format.Selected || format.RawCode != "FILE_FORMAT" || !reflect.DeepEqual(format.Examples, []string{"NDPI", "OME-TIFF", "SVS"}) {
		t.Fatalf("FILE_FORMAT candidate=%#v", format)
	}
	if _, exists := byName["NDPI"]; exists {
		t.Fatal("example value became a column")
	}
	if byName["PLATFORM"].Selected {
		t.Fatal("unselected key reported selected")
	}
}

func TestRecipeColumnCandidatesResolveNestedAliasAndKeepDistinctIdentities(t *testing.T) {
	emptyPrefix := ""
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "nested", TranslationVersion: "1", Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Traversals: []recipe.Traversal{{Name: "documents", Alias: "docs", ToResourceType: "DocumentReference", DynamicColumns: []recipe.DynamicColumn{{Name: "category", ColumnPrefix: &emptyPrefix, Source: recipe.Expression{Select: "docs.category[].coding[]"}, Key: &recipe.Expression{Select: "item.code"}, Value: &recipe.Expression{Select: "item.display"}, ColumnMode: recipe.ColumnModeSelected, MaxColumns: 8}}}}}}}
	fields := []FieldCandidate{{ResourceType: "DocumentReference", Path: "category[].coding[].code", Kind: "code", DistinctValues: []string{"FILE_FORMAT"}, SemanticObservations: []SemanticObservation{{SourcePath: "category[]", KeySystem: "system-a", KeyCode: "FILE_FORMAT", Population: 1, Examples: []string{"SVS"}}, {SourcePath: "category[]", KeySystem: "system-b", KeyCode: "FILE_FORMAT", Population: 1, Examples: []string{"NDPI"}}}}}
	candidates, err := ColumnCandidates(bundle, "patients", []string{"docs"}, fields)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].NodePath != "root.docs" || candidates[0].PatchPath != "outputs[0].traversals[0].dynamicColumns[0].columns" {
		t.Fatalf("nested candidate=%#v", candidates)
	}
	if candidates[0].ID == candidates[1].ID || candidates[0].PublicName != candidates[1].PublicName {
		t.Fatal("different raw systems were deduplicated or renamed")
	}
}
