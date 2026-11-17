package schema

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
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
	if len(resolved.Bundle.Outputs[0].Fields) != 2 || resolved.Bundle.Outputs[0].Fields[0].Name != "gender" || resolved.Bundle.Outputs[0].Fields[0].SourceKey != "common" {
		t.Fatalf("resolved fields = %#v", resolved.Bundle.Outputs[0].Fields)
	}
	if _, err := semantic.BuildRecipePlan(resolved.Bundle, recipe.RuntimeBindings{Project: "p", DatasetGeneration: "g"}); err != nil {
		t.Fatalf("resolved fields did not type-check: %v", err)
	}
}

func TestResolveCatalogProjectionKeepsPathsThatNormalizeToTheSameColumn(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "catalog", TranslationVersion: "1", Outputs: []recipe.Output{{
		Name: "DocumentReference", RootResourceType: "DocumentReference", RowGrain: "file",
		CatalogProjections: []recipe.CatalogProjection{{Name: "fields", IncludePaths: []string{"author*"}, Kinds: []string{"scalar"}, MaxColumns: 4, ValueMode: recipe.ValueModeFirst}},
	}}}
	resolved, err := Resolve(context.Background(), bundle, Scope{Project: "p"}, fakeDiscovery{fields: []FieldCandidate{
		{ResourceType: "DocumentReference", Path: "author.reference", Kind: "scalar"},
		{ResourceType: "DocumentReference", Path: "author[].reference", Kind: "scalar"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	fields := resolved.Bundle.Outputs[0].Fields
	if len(fields) != 2 || fields[0].Name != "author_reference" || fields[1].Name != "author_reference__repeated" {
		t.Fatalf("resolved fields = %#v", fields)
	}
}

func TestResolveCatalogProjectionDisablingSiblingPreservesNaturalCollisionName(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "catalog", TranslationVersion: "1", Outputs: []recipe.Output{{
		Name: "DocumentReference", RootResourceType: "DocumentReference", RowGrain: "file",
		CatalogProjections: []recipe.CatalogProjection{{Name: "fields", IncludePaths: []string{"author*"}, Kinds: []string{"scalar"}, MaxColumns: 4, ValueMode: recipe.ValueModeFirst, OutputOverrides: []recipe.CatalogProjectionOutputOverride{{FieldPath: "author.reference", Enabled: false}}}},
	}}}
	resolved, err := Resolve(context.Background(), bundle, Scope{Project: "p"}, fakeDiscovery{fields: []FieldCandidate{
		{ResourceType: "DocumentReference", Path: "author.reference", Kind: "scalar"},
		{ResourceType: "DocumentReference", Path: "author[].reference", Kind: "scalar"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	fields := resolved.Bundle.Outputs[0].Fields
	if len(fields) != 1 || fields[0].FieldRef != "author[].reference" || fields[0].Name != "author_reference__repeated" {
		t.Fatalf("disabling a sibling changed the remaining natural name: %#v", fields)
	}
}

func TestResolveCatalogProjectionAppliesStablePerOutputOverrides(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "catalog", TranslationVersion: "1", Outputs: []recipe.Output{{
		Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
		CatalogProjections: []recipe.CatalogProjection{{Name: "fields", IncludePaths: []string{"*"}, MaxColumns: 4, OutputOverrides: []recipe.CatalogProjectionOutputOverride{
			{FieldPath: "gender", Enabled: false},
			{FieldPath: "birthDate", Enabled: true, Name: "date_of_birth"},
		}}},
	}}}
	resolved, err := Resolve(context.Background(), bundle, Scope{Project: "p"}, fakeDiscovery{fields: []FieldCandidate{
		{ResourceType: "Patient", Path: "gender", Kind: "scalar"},
		{ResourceType: "Patient", Path: "birthDate", Kind: "scalar"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	fields := resolved.Bundle.Outputs[0].Fields
	if len(fields) != 1 || fields[0].FieldRef != "birthDate" || fields[0].Name != "date_of_birth" {
		t.Fatalf("resolved fields = %#v", fields)
	}
	if fields[0].SourceKey != "fields" {
		t.Fatalf("resolved field lost its owning projection identity: %#v", fields[0])
	}
	semanticPlan, err := semantic.BuildRecipePlan(resolved.Bundle, recipe.RuntimeBindings{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	resolvedPlan, err := semantic.ResolveRecipePlan(semanticPlan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolvedPlan, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range compiled.Outputs[0].OutputSchema {
		if column.Name != "date_of_birth" {
			continue
		}
		if column.SourceOrigin == nil || column.SourceOrigin.SourceKind != "CATALOG_PROJECTION" || column.SourceOrigin.SourceKey != "fields" || column.SourceOrigin.OutputKey != "birthDate" {
			t.Fatalf("compiled output lost stable catalog identity: %#v", column.SourceOrigin)
		}
		return
	}
	t.Fatal("renamed catalog output was not compiled")
}

func TestResolveCatalogProjectionRejectsOverrideCollisionRegardlessOfPathOrder(t *testing.T) {
	cases := []struct {
		name, overridePath, naturalPath, outputName string
	}{
		{name: "override sorts first", overridePath: "birthDate", naturalPath: "date_of_birth", outputName: "date_of_birth"},
		{name: "natural sorts first", overridePath: "z_last", naturalPath: "birth_date", outputName: "birth_date"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "catalog", TranslationVersion: "1", Outputs: []recipe.Output{{
				Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
				CatalogProjections: []recipe.CatalogProjection{{Name: "fields", IncludePaths: []string{"*"}, MaxColumns: 4, OutputOverrides: []recipe.CatalogProjectionOutputOverride{{FieldPath: tc.overridePath, Enabled: true, Name: tc.outputName}}}},
			}}}
			_, err := Resolve(context.Background(), bundle, Scope{Project: "p"}, fakeDiscovery{fields: []FieldCandidate{
				{ResourceType: "Patient", Path: tc.overridePath, Kind: "scalar"},
				{ResourceType: "Patient", Path: tc.naturalPath, Kind: "scalar"},
			}})
			if err == nil || !strings.Contains(err.Error(), "collides") {
				t.Fatalf("override collision was not rejected: %v", err)
			}
		})
	}
}

func TestResolveDiscoveredPivotOutputOverridesByRawKeyAndRetainsMissingKeys(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "pivot-output-editor", TranslationVersion: "1", Outputs: []recipe.Output{{
		Name: "Observation", RootResourceType: "Observation", RowGrain: "resource",
		Pivots: []recipe.Pivot{{
			Name: "observation_values", Discovery: &recipe.PivotDiscovery{Family: "observation_code_value", MaxColumns: 4},
			ColumnExpr: recipe.Expression{Select: "root.code"}, ValueExpr: recipe.Expression{Select: "root.valueString"},
			OutputOverrides: []recipe.PivotOutputOverride{
				{Key: "code-a", Enabled: false},
				{Key: "code-b", Enabled: true, Name: stringPointer("blood_pressure")},
				{Key: "code-c", Enabled: false},
			},
		}},
	}}}
	resolve := func(codes ...string) recipe.Output {
		t.Helper()
		resolved, err := Resolve(context.Background(), bundle, Scope{Project: "p"}, fakeDiscovery{fields: []FieldCandidate{{
			ResourceType: "Observation", Path: "code", Kind: "codeable_concept", PivotCandidate: true,
			PivotFamily: "observation_code_value", PivotColumns: codes, PivotColumnSelect: "root.code", PivotValueSelect: "root.valueString",
		}}})
		if err != nil {
			t.Fatal(err)
		}
		return resolved.Bundle.Outputs[0]
	}
	first := resolve("code-a", "code-b")
	if len(first.Pivots) != 1 || !reflect.DeepEqual(first.Pivots[0].Columns, []string{"code-b"}) || first.Pivots[0].ColumnAliases["code-b"] != "blood_pressure" {
		t.Fatalf("first resolved pivot = %#v", first.Pivots)
	}
	if first.Pivots[0].OutputOverrides != nil {
		t.Fatalf("resolved output retained authoring overrides: %#v", first.Pivots[0].OutputOverrides)
	}
	second := resolve("code-a", "code-b", "code-c")
	if len(second.Pivots) != 1 || !reflect.DeepEqual(second.Pivots[0].Columns, []string{"code-b"}) || second.Pivots[0].ColumnAliases["code-b"] != "blood_pressure" {
		t.Fatalf("later discovery did not reapply saved raw-key choices: %#v", second.Pivots)
	}
	plan, err := semantic.BuildRecipePlan(recipe.Bundle{RecipeSchemaVersion: 1, Name: "pivot-output-editor", TranslationVersion: "1", Outputs: []recipe.Output{second}}, recipe.RuntimeBindings{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	resolvedPlan, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolvedPlan, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	foundRawKey := false
	for _, column := range compiled.Outputs[0].OutputSchema {
		if column.SourceOrigin != nil && column.SourceOrigin.SourceKind == "PIVOT" && column.SourceOrigin.OutputKey == "code-b" {
			foundRawKey = true
			if column.Name != "blood_pressure" {
				t.Fatalf("raw key alias was not applied to the public output name: %q", column.Name)
			}
		}
	}
	if !foundRawKey {
		t.Fatal("compiled discovered pivot lost raw outputKey provenance")
	}
}

func TestResolveStaticPivotOutputOverridesPreserveOrExplicitlyClearPinnedAliases(t *testing.T) {
	for _, tc := range []struct {
		name             string
		nameOverride     *string
		wantAlias        string
		wantAliasPresent bool
	}{
		{name: "omitted name preserves pinned alias", wantAlias: "existing_name", wantAliasPresent: true},
		{name: "empty name clears pinned alias", nameOverride: stringPointer(""), wantAliasPresent: false},
		{name: "new name replaces pinned alias", nameOverride: stringPointer("new_name"), wantAlias: "new_name", wantAliasPresent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "static-pivot-editor", TranslationVersion: "1", Outputs: []recipe.Output{{
				Name: "Observation", RootResourceType: "Observation", RowGrain: "resource",
				Pivots: []recipe.Pivot{{Name: "values", Columns: []string{"code-a", "code-b"}, ColumnAliases: map[string]string{"code-a": "existing_name"}, ColumnExpr: recipe.Expression{Select: "root.code"}, ValueExpr: recipe.Expression{Select: "root.valueString"}, OutputOverrides: []recipe.PivotOutputOverride{{Key: "code-a", Enabled: true, Name: tc.nameOverride}, {Key: "code-b", Enabled: false}}}},
			}}}
			resolved, err := Resolve(context.Background(), bundle, Scope{Project: "p"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			pivot := resolved.Bundle.Outputs[0].Pivots[0]
			if !reflect.DeepEqual(pivot.Columns, []string{"code-a"}) {
				t.Fatalf("enabled raw keys = %v", pivot.Columns)
			}
			name, ok := pivot.ColumnAliases["code-a"]
			if ok != tc.wantAliasPresent || (ok && name != tc.wantAlias) {
				t.Fatalf("alias = %q, present=%v", name, ok)
			}
		})
	}
}

func TestResolvePivotOutputOverrideUsesNativeNamesForCollision(t *testing.T) {
	cases := []struct {
		name, rawKey, alias string
		wantCollision       bool
	}{
		{name: "family prefix means no collision", rawKey: "other", alias: "other"},
		{name: "alias collides with prefixed output", rawKey: "other", alias: "values__other", wantCollision: true},
		{name: "sanitized raw key collision", rawKey: "other-key", alias: "values__other_key", wantCollision: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			aliases := map[string]string{"code-a": tc.alias}
			firstName := lower.NativePivotColumnName("", "values", "code-a", aliases)
			secondName := lower.NativePivotColumnName("", "values", tc.rawKey, aliases)
			if (firstName == secondName) != tc.wantCollision {
				t.Fatalf("test fixture names %q and %q do not match expected collision=%v", firstName, secondName, tc.wantCollision)
			}
			bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "pivot-output-collision", TranslationVersion: "1", Outputs: []recipe.Output{{
				Name: "Observation", RootResourceType: "Observation", RowGrain: "resource",
				Pivots: []recipe.Pivot{{Name: "values", Columns: []string{"code-a", tc.rawKey}, ColumnExpr: recipe.Expression{Select: "root.code"}, ValueExpr: recipe.Expression{Select: "root.valueString"}, OutputOverrides: []recipe.PivotOutputOverride{{Key: "code-a", Enabled: true, Name: stringPointer(tc.alias)}}}},
			}}}
			_, err := Resolve(context.Background(), bundle, Scope{Project: "p"}, nil)
			if tc.wantCollision && (err == nil || !strings.Contains(err.Error(), "collides")) {
				t.Fatalf("actual output-name collision was not rejected: %v", err)
			}
			if !tc.wantCollision && err != nil {
				t.Fatalf("distinct native output names were rejected: %v", err)
			}
		})
	}
}

func TestResolvePivotOutputOverridePreservesWhitespaceInRawKeyIdentity(t *testing.T) {
	const rawKey = "  code-a  "
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "pivot-output-whitespace", TranslationVersion: "1", Outputs: []recipe.Output{{
		Name: "Observation", RootResourceType: "Observation", RowGrain: "resource",
		Pivots: []recipe.Pivot{{Name: "values", Columns: []string{rawKey, "code-b"}, ColumnExpr: recipe.Expression{Select: "root.code"}, ValueExpr: recipe.Expression{Select: "root.valueString"}, OutputOverrides: []recipe.PivotOutputOverride{{Key: rawKey, Enabled: false}}}},
	}}}
	resolved, err := Resolve(context.Background(), bundle, Scope{Project: "p"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved.Bundle.Outputs[0].Pivots[0].Columns; !reflect.DeepEqual(got, []string{"code-b"}) {
		t.Fatalf("raw key override was normalized or missed: %q", got)
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

func TestResolveTransformsDiscoveredDynamicKeys(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "catalog", TranslationVersion: "1", Outputs: []recipe.Output{{
		Name: "DocumentReference", RootResourceType: "DocumentReference", RowGrain: "file",
		DynamicColumns: []recipe.DynamicColumn{{
			Name: "attachment_extensions", ColumnPrefix: stringPointer(""),
			Source: recipe.Expression{Select: "root.content[].attachment.extension[]"},
			Key:    &recipe.Expression{Call: "last_segment", Args: []recipe.Expression{{Select: "item.url"}}},
			Value:  &recipe.Expression{Select: "item.valueString"}, MaxColumns: 4,
		}},
	}}}
	resolved, err := Resolve(context.Background(), bundle, Scope{Project: "p", DatasetGeneration: "g"}, fakeDiscovery{fields: []FieldCandidate{{
		ResourceType: "DocumentReference", Path: "content[].attachment.extension[].url", Kind: "scalar",
		DistinctValues: []string{"http://aced-idp.org/fhir/StructureDefinition/sha256", "http://aced-idp.org/fhir/StructureDefinition/source_path"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	columns := resolved.Bundle.Outputs[0].DynamicColumns[0].Columns
	if got, want := strings.Join(columns, ","), "sha256,source_path"; got != want {
		t.Fatalf("transformed dynamic columns = %q, want %q", got, want)
	}
}

func TestResolveDynamicOutputOverridesPreserveSiblingsAndFreezeExclusions(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "dynamic-removal", TranslationVersion: "1", Outputs: []recipe.Output{{
		Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
		DynamicColumns: []recipe.DynamicColumn{{Name: "category", Source: recipe.Expression{Select: "root.identifier[]"}, Key: &recipe.Expression{Select: "item.system"}, MaxColumns: 4, OutputOverrides: []recipe.DynamicColumnOutputOverride{{Key: "CELL_SEGMENTATION_METHOD", Enabled: false}}}},
	}}}
	discovery := &countingDiscovery{delegate: fakeDiscovery{fields: []FieldCandidate{{
		ResourceType: "Patient", Path: "identifier[].system", Kind: "scalar", DistinctValues: []string{"CELL_MORPHOLOGY_ASSESSMENT", "CELL_SEGMENTATION_METHOD", "CLUSTERING_METHOD"},
	}}}, calls: map[string]int{}}
	resolved, err := Resolve(context.Background(), bundle, Scope{Project: "p", DatasetGeneration: "g"}, discovery)
	if err != nil {
		t.Fatal(err)
	}
	dynamic := resolved.Bundle.Outputs[0].DynamicColumns[0]
	if got, want := strings.Join(dynamic.Columns, ","), "CELL_MORPHOLOGY_ASSESSMENT,CELL_SEGMENTATION_METHOD,CLUSTERING_METHOD"; got != want {
		t.Fatalf("frozen dynamic keys = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(dynamic.OutputOverrides, []recipe.DynamicColumnOutputOverride{{Key: "CELL_SEGMENTATION_METHOD", Enabled: false}}) {
		t.Fatalf("dynamic output overrides were not retained: %#v", dynamic.OutputOverrides)
	}
	if len(discovery.calls) != 1 || discovery.calls["Patient"] != 1 {
		t.Fatalf("discovery calls = %#v", discovery.calls)
	}
	// A JSON save/reload must retain both the full discovered key inventory and
	// the override that semantic resolution applies at compile time.
	encoded, err := json.Marshal(resolved.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded recipe.Bundle
	if err := json.Unmarshal(encoded, &reloaded); err != nil {
		t.Fatal(err)
	}
	resolvedAgain, err := Resolve(context.Background(), reloaded, Scope{Project: "p", DatasetGeneration: "g"}, fakeDiscovery{})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(resolvedAgain.Bundle.Outputs[0].DynamicColumns[0].Columns, ","); got != "CELL_MORPHOLOGY_ASSESSMENT,CELL_SEGMENTATION_METHOD,CLUSTERING_METHOD" {
		t.Fatalf("second resolution changed frozen keys: %q", got)
	}
	plan, err := semantic.BuildRecipePlan(resolvedAgain.Bundle, recipe.RuntimeBindings{Project: "p", DatasetGeneration: "g"})
	if err != nil {
		t.Fatal(err)
	}
	resolvedPlan, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolvedPlan, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	var visible, runtimeInventory, excludedValidationOnly bool
	for _, column := range compiled.Outputs[0].OutputSchema {
		if strings.HasSuffix(column.Name, "CELL_SEGMENTATION_METHOD") {
			t.Fatal("excluded key appeared in public schema after JSON reload")
		}
		if strings.HasSuffix(column.Name, "CELL_MORPHOLOGY_ASSESSMENT") || strings.HasSuffix(column.Name, "CLUSTERING_METHOD") {
			visible = true
		}
		if column.Name == "__loom_dynamic_runtime_keys" {
			runtimeInventory = true
		}
	}
	for _, metadata := range compiled.Outputs[0].DynamicColumns {
		if metadata.SourceKey == "CELL_SEGMENTATION_METHOD" && metadata.Name == "" {
			excludedValidationOnly = true
		}
	}
	if !visible || !runtimeInventory || !excludedValidationOnly {
		t.Fatalf("compiled outputs do not preserve removal and drift checks: schema=%#v metadata=%#v", compiled.Outputs[0].OutputSchema, compiled.Outputs[0].DynamicColumns)
	}
}

func TestResolveAllExcludedDynamicOutputsKeepsExplicitEmptyFamily(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "dynamic-empty", TranslationVersion: "1", Outputs: []recipe.Output{{
		Name: "DocumentReference", RootResourceType: "DocumentReference", RowGrain: "file",
		DynamicColumns: []recipe.DynamicColumn{{Name: "category", Source: recipe.Expression{Select: "root.category[]"}, Key: &recipe.Expression{Select: "item.code"}, MaxColumns: 4, OutputOverrides: []recipe.DynamicColumnOutputOverride{{Key: "a", Enabled: false}, {Key: "b", Enabled: false}}}},
	}}}
	discovery := &countingDiscovery{delegate: fakeDiscovery{fields: []FieldCandidate{{ResourceType: "DocumentReference", Path: "category[].code", Kind: "scalar", DistinctValues: []string{"a", "b"}}}}, calls: map[string]int{}}
	resolved, err := Resolve(context.Background(), bundle, Scope{Project: "p", DatasetGeneration: "g"}, discovery)
	if err != nil {
		t.Fatal(err)
	}
	dynamic := resolved.Bundle.Outputs[0].DynamicColumns[0]
	if dynamic.Columns == nil || !reflect.DeepEqual(dynamic.Columns, []string{"a", "b"}) || len(dynamic.OutputOverrides) != 2 {
		t.Fatalf("all-excluded family did not retain frozen keys and overrides: %#v", dynamic)
	}
	resolvedAgain, err := Resolve(context.Background(), resolved.Bundle, Scope{Project: "p", DatasetGeneration: "g"}, discovery)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolvedAgain.Bundle.Outputs[0].DynamicColumns[0]; got.Columns == nil || !reflect.DeepEqual(got.Columns, []string{"a", "b"}) || len(got.OutputOverrides) != 2 {
		t.Fatalf("all-excluded family was not stable: %#v", got)
	}
}

func TestResolveDynamicOutputOverrideIgnoresKeyMissingFromCurrentDiscovery(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "dynamic-stale-removal", TranslationVersion: "1", Outputs: []recipe.Output{{
		Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
		DynamicColumns: []recipe.DynamicColumn{{Name: "identifier", Source: recipe.Expression{Select: "root.identifier[]"}, Key: &recipe.Expression{Select: "item.system"}, MaxColumns: 4, OutputOverrides: []recipe.DynamicColumnOutputOverride{{Key: "urn:gone", Enabled: false}}}},
	}}}
	discovery := &countingDiscovery{delegate: fakeDiscovery{fields: []FieldCandidate{{ResourceType: "Patient", Path: "identifier[].system", Kind: "scalar", DistinctValues: []string{"urn:current"}}}}, calls: map[string]int{}}
	resolved, err := Resolve(context.Background(), bundle, Scope{Project: "p", DatasetGeneration: "g"}, discovery)
	if err != nil {
		t.Fatalf("stale removal key blocked discovery: %v", err)
	}
	got := resolved.Bundle.Outputs[0].DynamicColumns[0]
	if !reflect.DeepEqual(got.Columns, []string{"urn:current"}) || len(got.OutputOverrides) != 1 {
		t.Fatalf("resolved dynamic with stale override = %#v", got)
	}
	resolvedAgain, err := Resolve(context.Background(), resolved.Bundle, Scope{Project: "p", DatasetGeneration: "g"}, discovery)
	if err != nil {
		t.Fatalf("second resolution of stale override failed: %v", err)
	}
	if dynamic := resolvedAgain.Bundle.Outputs[0].DynamicColumns[0]; !reflect.DeepEqual(dynamic.Columns, []string{"urn:current"}) || len(dynamic.OutputOverrides) != 1 {
		t.Fatalf("stale override changed current keys on second resolution: %#v", dynamic)
	}
}

func TestCompileDynamicOutputRemovalKeepsValidationInventoryOutOfPublicSchema(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "dynamic-compile-removal", TranslationVersion: "1", Outputs: []recipe.Output{{
		Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
		DynamicColumns: []recipe.DynamicColumn{{Name: "category", Source: recipe.Expression{Select: "root.identifier[]"}, Key: &recipe.Expression{Select: "item.system"}, Value: &recipe.Expression{Select: "item.value"}, MaxColumns: 4, Columns: []string{"a", "b"}, OutputOverrides: []recipe.DynamicColumnOutputOverride{{Key: "a", Enabled: false}}}},
	}}}
	resolvedBundle, err := Resolve(context.Background(), bundle, Scope{Project: "p", DatasetGeneration: "g"}, fakeDiscovery{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := semantic.BuildRecipePlan(resolvedBundle.Bundle, recipe.RuntimeBindings{Project: "p", DatasetGeneration: "g"})
	if err != nil {
		t.Fatal(err)
	}
	resolvedPlan, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolvedPlan, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	var visible, runtimeInventory bool
	for _, column := range compiled.Outputs[0].OutputSchema {
		if column.Name == "category_a" {
			t.Fatal("excluded output appeared in compiled public schema")
		}
		if column.Name == "category_b" {
			visible = true
		}
		if column.Name == "__loom_dynamic_runtime_keys" {
			runtimeInventory = true
		}
	}
	if !visible || !runtimeInventory {
		t.Fatalf("public schema = %#v; visible=%v runtimeInventory=%v", compiled.Outputs[0].OutputSchema, visible, runtimeInventory)
	}
	var excludedValidationOnly bool
	for _, metadata := range compiled.Outputs[0].DynamicColumns {
		if metadata.SourceKey == "a" && metadata.Name == "" {
			excludedValidationOnly = true
		}
	}
	if !excludedValidationOnly {
		t.Fatalf("compiled drift metadata did not retain excluded key: %#v", compiled.Outputs[0].DynamicColumns)
	}
}

func TestResolveExtensionColumnsFreezesURLMappings(t *testing.T) {
	prefix := ""
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "extensions", TranslationVersion: "1", Outputs: []recipe.Output{{
		Name: "DocumentReference", RootResourceType: "DocumentReference", RowGrain: "file",
		ExtensionColumns: []recipe.ExtensionColumn{{Name: "attachment", ColumnPrefix: &prefix, MaxColumns: 4, Source: recipe.Expression{Select: "root.content[].attachment.extension[]"}}},
	}}}
	resolved, err := Resolve(context.Background(), bundle, Scope{Project: "p", DatasetGeneration: "g"}, fakeDiscovery{fields: []FieldCandidate{
		{ResourceType: "DocumentReference", Path: "content[].attachment.extension[].url", DistinctValues: []string{"http://example.org/source_path", "http://example.org/sha256"}},
		{ResourceType: "DocumentReference", Path: "content[].attachment.extension[].valueString", Kind: "scalar"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	columns := resolved.Bundle.Outputs[0].ExtensionColumns[0].Columns
	if len(columns) != 2 || columns[0].Name != "sha256" || columns[1].Name != "source_path" {
		t.Fatalf("unexpected extension mappings: %#v", columns)
	}
	if _, err := semantic.BuildRecipePlan(resolved.Bundle, recipe.RuntimeBindings{Project: "p", DatasetGeneration: "g"}); err != nil {
		t.Fatalf("resolved extension columns did not type-check: %v", err)
	}
}

func TestResolveExtensionColumnsPreservesPrimitiveMapping(t *testing.T) {
	prefix := ""
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "extensions", TranslationVersion: "1", Outputs: []recipe.Output{{
		Name: "DocumentReference", RootResourceType: "DocumentReference", RowGrain: "file",
		ExtensionColumns: []recipe.ExtensionColumn{{Name: "attachment", ColumnPrefix: &prefix, MaxColumns: 4, Source: recipe.Expression{Select: "root.content[].attachment.extension[]"}}},
	}}}
	resolved, err := Resolve(context.Background(), bundle, Scope{Project: "p"}, fakeDiscovery{fields: []FieldCandidate{
		{ResourceType: "DocumentReference", Path: "content[].attachment.extension[].url", ExtensionValues: []ExtensionValueObservation{{URL: "http://example.org/source_path", SourcePath: "content[].attachment.extension[]", ValuePath: "valueUrl", ValueType: "string"}, {URL: "http://example.org/sha256", SourcePath: "content[].attachment.extension[]", ValuePath: "valueString", ValueType: "string"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	columns := resolved.Bundle.Outputs[0].ExtensionColumns[0].Columns
	if columns[0].Name != "sha256" || columns[1].Name != "source_path" || columns[0].ValuePath != "valueString" || columns[1].ValuePath != "valueUrl" {
		t.Fatalf("primitive mappings were not retained: %#v", columns)
	}
}

func TestNestedExtensionRemovalUsesRawMappingIdentityAndKeepsSibling(t *testing.T) {
	ancestorA := "https://example.org/parent-a"
	ancestorB := "https://example.org/parent-b"
	leafURL := "https://example.org/value"
	base := recipe.ExtensionColumnMapping{URL: leafURL, SourcePath: "extension[].extension[]", ValuePath: "valueString", ValueType: "string"}
	mappingA := base
	mappingA.URLPath = []string{ancestorA}
	mappingB := base
	mappingB.URLPath = []string{ancestorB}
	keyA := recipe.ExtensionColumnOutputKey(mappingA)
	keyB := recipe.ExtensionColumnOutputKey(mappingB)
	if keyA == keyB {
		t.Fatal("equal leaf URLs under different ancestors must have distinct output identities")
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "nested-extension-removal", TranslationVersion: "1", Outputs: []recipe.Output{{
		Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
		ExtensionColumns: []recipe.ExtensionColumn{{Name: "extension", ColumnPrefix: stringPointer(""), MaxColumns: 4, Source: recipe.Expression{Select: "root.extension[]"}, OutputOverrides: []recipe.ExtensionColumnOutputOverride{{Key: keyA, Enabled: false}}}},
	}}}
	resolved, err := Resolve(context.Background(), bundle, Scope{Project: "p", DatasetGeneration: "g"}, fakeDiscovery{fields: []FieldCandidate{{
		ResourceType: "Patient", Path: "extension[].extension[].url", ExtensionValues: []ExtensionValueObservation{
			{URL: leafURL, SourcePath: "extension[].extension[]", ValuePath: "valueString", ValueType: "string", URLPath: []string{ancestorA}},
			{URL: leafURL, SourcePath: "extension[].extension[]", ValuePath: "valueString", ValueType: "string", URLPath: []string{ancestorB}},
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	mappings := resolved.Bundle.Outputs[0].ExtensionColumns[0].Columns
	if len(mappings) != 2 {
		t.Fatalf("nested mappings = %#v", mappings)
	}
	for _, mapping := range mappings {
		if len(mapping.URLPath) != 1 || mapping.URLPath[0] != ancestorA && mapping.URLPath[0] != ancestorB {
			t.Fatalf("raw ancestor URL was not retained: %#v", mapping)
		}
	}
	plan, err := semantic.BuildRecipePlan(resolved.Bundle, recipe.RuntimeBindings{Project: "p", DatasetGeneration: "g"})
	if err != nil {
		t.Fatal(err)
	}
	resolvedPlan, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolvedPlan, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	visible := 0
	var siblingProvenance bool
	for _, column := range compiled.Outputs[0].OutputSchema {
		if strings.HasSuffix(column.Name, "value") {
			visible++
		}
		if column.SourceOrigin != nil && column.SourceOrigin.SourceKind == "EXTENSION_COLUMN" {
			if column.SourceOrigin.OutputKey != recipe.ExtensionColumnOutputKey(mappingB) {
				t.Fatalf("sibling provenance key = %q", column.SourceOrigin.OutputKey)
			}
			siblingProvenance = true
		}
	}
	if visible != 1 || !siblingProvenance || len(compiled.Outputs[0].DynamicColumns) != 0 {
		t.Fatalf("extension outputs visible=%d sibling provenance=%v schema=%#v dynamic=%#v", visible, siblingProvenance, compiled.Outputs[0].OutputSchema, compiled.Outputs[0].DynamicColumns)
	}
	var exactSiblingCorrelation bool
	for _, operation := range compiled.Outputs[0].Plan.Operations {
		if operation.Kind != ir.PhysicalExpressionLetOp || operation.ExpressionLet == nil || operation.ExpressionLet.Expression.Pivot == nil || operation.ExpressionLet.Expression.Pivot.Correlation == nil {
			continue
		}
		correlation := operation.ExpressionLet.Expression.Pivot.Correlation
		if len(correlation.ExtensionURLBindKeys) != 2 {
			continue
		}
		exactSiblingCorrelation = compiled.Outputs[0].Plan.BindVars[correlation.ExtensionURLBindKeys[0]] == ancestorB && compiled.Outputs[0].Plan.BindVars[correlation.ExtensionURLBindKeys[1]] == leafURL
	}
	if !exactSiblingCorrelation {
		t.Fatal("the retained sibling output was not correlated through its exact parent and leaf URLs")
	}
}

func stringPointer(value string) *string { return &value }

func TestResolveOmitsPivotWithNoDiscoveredColumns(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "document-reference", TranslationVersion: "1", Outputs: []recipe.Output{{
		Name: "DocumentReference", RootResourceType: "DocumentReference", RowGrain: "document",
		CatalogProjections: []recipe.CatalogProjection{{Name: "fields", IncludePaths: []string{"status"}, Kinds: []string{"scalar"}, MaxColumns: 4}},
		Pivots:             []recipe.Pivot{{Name: "empty", Discovery: &recipe.PivotDiscovery{Family: "observation_code_value", MaxColumns: 4}}},
	}}}
	resolved, err := Resolve(context.Background(), bundle, Scope{Project: "p"}, fakeDiscovery{fields: []FieldCandidate{{ResourceType: "DocumentReference", Path: "status", Kind: "scalar"}}})
	if err != nil {
		t.Fatal(err)
	}
	output := resolved.Bundle.Outputs[0]
	if len(output.Pivots) != 0 || len(output.Fields) != 1 || output.Fields[0].Name != "status" {
		t.Fatalf("resolved output = %#v", output)
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

func TestResolveCanonicalizesFragmentsBeforeCompilation(t *testing.T) {
	fragment := recipe.FragmentLibrary{Fragments: map[string]recipe.Fragment{
		"id": {Name: "id", Version: "1", Params: []string{"value"}, Expr: recipe.Expression{Call: "path_segment", Args: []recipe.Expression{{Select: "$value"}}}},
	}}
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "fragmented", TranslationVersion: "1", Fragments: &fragment, Outputs: []recipe.Output{{
		Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Call: "fragment:id", Args: []recipe.Expression{{Select: "root.id"}}}}},
	}}}
	resolved, err := Resolve(context.Background(), bundle, Scope{Project: "p"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Bundle.Fragments != nil || resolved.Bundle.Outputs[0].Fields[0].Expr.Call != "path_segment" {
		t.Fatalf("resolved bundle was not canonicalized: %#v", resolved.Bundle)
	}
	if resolved.StoredRecipeDigest == resolved.ExpandedRecipeDigest || resolved.ExpandedRecipeDigest == "" {
		t.Fatalf("fragment identity digests were not separated: stored=%q expanded=%q", resolved.StoredRecipeDigest, resolved.ExpandedRecipeDigest)
	}
	if _, err := semantic.BuildRecipePlan(resolved.Bundle, recipe.RuntimeBindings{Project: "p"}); err != nil {
		t.Fatal(err)
	}
}
