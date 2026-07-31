package schema

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

type fakeDiscovery struct {
	fields []FieldCandidate
	byType map[string][]FieldCandidate
	calls  map[string]int
}

func (f fakeDiscovery) Fields(_ context.Context, _ Scope, resourceType string) ([]FieldCandidate, error) {
	if f.calls != nil {
		f.calls[resourceType]++
	}
	if f.byType != nil {
		return append([]FieldCandidate(nil), f.byType[resourceType]...), nil
	}
	return append([]FieldCandidate(nil), f.fields...), nil
}

type countingDiscovery struct {
	delegate fakeDiscovery
	calls    map[string]int
}

func (d *countingDiscovery) Fields(ctx context.Context, scope Scope, resourceType string) ([]FieldCandidate, error) {
	d.calls[resourceType]++
	return d.delegate.Fields(ctx, scope, resourceType)
}

func TestResolveCatalogProjectionBeforeSemanticTyping(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "catalog", TranslationVersion: "1", Outputs: []recipe.Output{{
		Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
		CatalogProjections: []recipe.CatalogProjection{{Name: "common", IncludePaths: []string{"identifier[].value", "gender"}, Kinds: []string{"scalar"}, MaxColumns: 4, ValueMode: recipe.ValueModeFirst}},
	}}}
	resolved, err := Resolve(context.Background(), bundle, Scope{Project: "p", DatasetGeneration: "g"}, fakeDiscovery{fields: []FieldCandidate{
		{ResourceType: "Patient", Path: "gender", Kind: "scalar"},
		{ResourceType: "Patient", Path: "identifier[].value", Kind: "scalar"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Bundle.Outputs[0].Fields) != 2 || resolved.Bundle.Outputs[0].Fields[0].Name != "gender" {
		t.Fatalf("resolved fields = %#v", resolved.Bundle.Outputs[0].Fields)
	}
	if _, err := semantic.BuildRecipePlan(resolved.Bundle, recipe.RuntimeBindings{Project: "p", DatasetGeneration: "g"}); err != nil {
		t.Fatalf("resolved fields did not type-check: %v", err)
	}
}

func TestResolveDiscoveryFreezesPivotAndKeyedColumns(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "catalog", TranslationVersion: "1", Outputs: []recipe.Output{{
		Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
		Pivots:         []recipe.Pivot{{Name: "observations", ColumnExpr: recipe.Expression{Select: "root.gender"}, ValueExpr: recipe.Expression{Select: "root.gender"}, Discovery: &recipe.PivotDiscovery{Family: "observation_code_value", MaxColumns: 4}}},
		DynamicColumns: []recipe.DynamicColumn{{Name: "identifiers", Source: recipe.Expression{Select: "root.identifier[]"}, Key: &recipe.Expression{Select: "item.system"}, Value: &recipe.Expression{Select: "item.value"}, MaxColumns: 4}},
	}}}
	resolved, err := Resolve(context.Background(), bundle, Scope{Project: "p", DatasetGeneration: "g"}, fakeDiscovery{fields: []FieldCandidate{
		{ResourceType: "Patient", Path: "code", Kind: "codeable_concept", PivotCandidate: true, PivotFamily: "observation_code_value", PivotColumns: []string{"a", "b"}},
		{ResourceType: "Patient", Path: "identifier[].system", Kind: "scalar", DistinctValues: []string{"urn:z", "urn:a"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	output := resolved.Bundle.Outputs[0]
	if len(output.Pivots) != 1 || len(output.Pivots[0].Columns) != 2 || output.Pivots[0].Discovery != nil {
		t.Fatalf("pivot was not frozen: %#v", output.Pivots)
	}
	if len(output.DynamicColumns) != 1 || len(output.DynamicColumns[0].Columns) != 2 || output.DynamicColumns[0].Columns[0] != "urn:a" {
		t.Fatalf("dynamic columns were not frozen: %#v", output.DynamicColumns)
	}
}

func TestResolveFillsDiscoveredPivotSelectorsInTraversalScope(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "pivot-selectors", TranslationVersion: "1", Outputs: []recipe.Output{{
		Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
		Traversals: []recipe.Traversal{{Name: "focus_Observation", ToResourceType: "Observation", Alias: "observation", Pivots: []recipe.Pivot{{Name: "values", Discovery: &recipe.PivotDiscovery{Family: "observation_code_value", MaxColumns: 4}}}}},
	}}}
	resolved, err := Resolve(context.Background(), bundle, Scope{Project: "p"}, fakeDiscovery{byType: map[string][]FieldCandidate{
		"Observation": {{ResourceType: "Observation", Path: "code", Kind: "codeable_concept", PivotCandidate: true, PivotFamily: "observation_code_value", PivotColumns: []string{"A"}, PivotColumnSelect: "code.coding[].display", PivotValueSelect: "valueString"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	pivot := resolved.Bundle.Outputs[0].Traversals[0].Pivots[0]
	if pivot.Discovery != nil || pivot.ColumnExpr.Select != "observation.code.coding[].display" || pivot.ValueExpr.Select != "observation.valueString" {
		t.Fatalf("resolved traversal pivot selectors = %#v", pivot)
	}
}

func TestResolveSharesOneCatalogSnapshotPerResourceType(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "memo", TranslationVersion: "v", Outputs: []recipe.Output{{
		Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
		CatalogProjections: []recipe.CatalogProjection{{Name: "a", IncludePaths: []string{"gender"}, Kinds: []string{"scalar"}, MaxColumns: 4}, {Name: "b", IncludePaths: []string{"id"}, Kinds: []string{"scalar"}, MaxColumns: 4}},
	}}}
	d := &countingDiscovery{delegate: fakeDiscovery{byType: map[string][]FieldCandidate{"Patient": {{ResourceType: "Patient", Path: "gender", Kind: "scalar"}, {ResourceType: "Patient", Path: "id", Kind: "scalar"}}}, calls: map[string]int{}}, calls: map[string]int{}}
	if _, err := Resolve(context.Background(), bundle, Scope{Project: "p"}, d); err != nil {
		t.Fatal(err)
	}
	if d.calls["Patient"] != 1 {
		t.Fatalf("catalog calls for Patient = %d, want one snapshot", d.calls["Patient"])
	}
}

func TestResolveRequiresDiscoveryForCatalogDeclarations(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "catalog", TranslationVersion: "1", Outputs: []recipe.Output{{
		Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
		CatalogProjections: []recipe.CatalogProjection{{Name: "fields", IncludePaths: []string{"*"}, MaxColumns: 4}},
	}}}
	if _, err := Resolve(context.Background(), bundle, Scope{}, nil); err == nil {
		t.Fatal("expected missing discovery error")
	}
}

func TestResolveRejectsTruncatedDynamicKeyDiscovery(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "truncated", TranslationVersion: "v", Outputs: []recipe.Output{{
		Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
		DynamicColumns: []recipe.DynamicColumn{{Name: "identifiers", Source: recipe.Expression{Select: "root.identifier[]"}, Key: &recipe.Expression{Select: "item.system"}, Value: &recipe.Expression{Select: "item.value"}, MaxColumns: 8}},
	}}}
	_, err := Resolve(context.Background(), bundle, Scope{Project: "p"}, fakeDiscovery{fields: []FieldCandidate{{Path: "identifier[].system", Kind: "scalar", DistinctValues: []string{"a"}, DistinctTruncated: true}}})
	if err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("expected truncated discovery rejection, got %v", err)
	}
}

func TestResolveRejectsDynamicColumnsAboveRecipeLimit(t *testing.T) {
	bundle := recipe.Bundle{
		RecipeSchemaVersion: 1,
		Name:                "limited",
		TranslationVersion:  "v",
		Outputs: []recipe.Output{{
			Name:             "Patient",
			RootResourceType: "Patient",
			RowGrain:         "patient",
			DynamicColumns: []recipe.DynamicColumn{{
				Name: "identifiers", Source: recipe.Expression{Select: "root.identifier[]"},
				Key: &recipe.Expression{Select: "item.system"}, Value: &recipe.Expression{Select: "item.value"}, MaxColumns: 1,
			}},
		}},
	}
	_, err := Resolve(context.Background(), bundle, Scope{Project: "p"}, fakeDiscovery{fields: []FieldCandidate{{
		Path: "identifier[].system", Kind: "scalar", DistinctValues: []string{"a", "b"},
	}}})
	if err == nil || !strings.Contains(err.Error(), "exceeding maxColumns") {
		t.Fatalf("expected maxColumns rejection, got %v", err)
	}
}

func TestResolveRecordsStoredAndScopedSchemaDigests(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "digest", TranslationVersion: "v", Outputs: []recipe.Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}}}}}
	first, err := Resolve(context.Background(), bundle, Scope{Project: "p", DatasetGeneration: "g", AuthScopeMode: "restricted", AuthResourcePaths: []string{"/b", "/a"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Resolve(context.Background(), bundle, Scope{Project: "p", DatasetGeneration: "g", AuthScopeMode: "restricted", AuthResourcePaths: []string{"/a", "/b"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.StoredRecipeDigest == "" || first.ResolvedSchemaDigest == "" || first.ScopeDigest == "" || first.SourceSnapshot.LegacyNull {
		t.Fatalf("incomplete immutable snapshot: %#v", first)
	}
	if first.StoredRecipeDigest != second.StoredRecipeDigest || first.ResolvedSchemaDigest != second.ResolvedSchemaDigest || first.ScopeDigest != second.ScopeDigest {
		t.Fatalf("scope ordering changed resolution identity: first=%#v second=%#v", first, second)
	}
}
