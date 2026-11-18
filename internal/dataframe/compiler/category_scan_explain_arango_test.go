package compiler

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestRootCategoryScanUsesSharedTypedIndexContractAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}

	project, generation := "loom_root_category_explain_"+uuid.NewString(), "generation-root-category-explain"
	columns := []recipe.StageColumn{{ID: "observation_id", Name: "observation_id"}, {ID: "category_id", Name: "category"}, {ID: "value_id", Name: "value"}}
	output := recipe.Output{
		Name: "root_category_explain", RootResourceType: "Observation", RowGrain: "observation",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "observation_id", ColumnID: "observation_id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "category", ColumnID: "category_id", Expr: recipe.Expression{Select: "root.valueQuantity.code"}},
			{Name: "value", ColumnID: "value_id", Expr: recipe.Expression{Select: "root.valueQuantity.value"}},
		},
		Construction: &recipe.Construction{Version: 1, SourceColumns: columns},
	}
	bindings := recipe.RuntimeBindings{
		Project: project, SelectionProject: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/category/explain"},
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, project, generation)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	scan, err := CompileCategoryScanStageWithPolicy(compiled.Outputs[0], recipe.ConstructionSourceProjectionID, "category_id", "value_id", 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if scan.CategoryIndex == nil || !reflect.DeepEqual(scan.CategoryIndex.Fields, previewQuantityCategoryFields()) || !reflect.DeepEqual(scan.CategoryIndex.StoredValues, previewQuantityCategoryStoredValues()) {
		t.Fatalf("root quantity index definition = %+v", scan.CategoryIndex)
	}
	assertCategoryScanIndexExplain(t, ctx, client, scan, true)
}

func TestRelatedCategoryScanUsesTypedIndexAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Specimen"}, {Name: "Patient"}, {Name: "Observation"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}

	project, generation := "loom_related_category_explain_"+uuid.NewString(), "generation-related-category-explain"
	bindings := recipe.RuntimeBindings{
		Project: project, SelectionProject: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/category/visible"},
	}

	definition := relatedCategoryScanOutput(relatedCategoryScanOptions{})
	steps := definition.Construction.Steps
	first, second := steps[0].Operation.RelatedExpand, steps[1].Operation.RelatedExpand
	second.AnchorColumnID = "_key"
	second.Route = append(append([]recipe.ConstructionRelatedRouteStep(nil), first.Route...), second.Route...)
	steps[1].Inputs = []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}}
	definition.Construction.Steps = steps[1:]
	for i := range definition.Construction.Steps {
		columns := definition.Construction.Steps[i].Outputs[:0]
		for _, column := range definition.Construction.Steps[i].Outputs {
			if column.ID != "patient_record" {
				columns = append(columns, column)
			}
		}
		definition.Construction.Steps[i].Outputs = columns
	}
	_, _, scan := compileRelatedCategoryRuntimeScansWithBindings(t, definition, bindings, 10)
	if scan.CategoryIndex == nil || !reflect.DeepEqual(scan.CategoryIndex.Fields, []string{"project", "dataset_generation", "payload.valueQuantity.code"}) ||
		!reflect.DeepEqual(scan.CategoryIndex.StoredValues, []string{"auth_resource_path", "resourceType"}) {
		t.Fatalf("related category index fields = %+v", scan.CategoryIndex)
	}
	assertCategoryScanIndexExplain(t, ctx, client, scan, false)
}

func assertCategoryScanIndexExplain(t *testing.T, ctx context.Context, client *store.Client, scan CompiledCategoryScanQuery, requireProjectionCoverage bool) {
	t.Helper()
	index := scan.CategoryIndex
	if index == nil {
		t.Fatal("compiled category scan omitted its typed index definition")
	}
	var provisionErr error
	if len(index.StoredValues) != 0 {
		provisionErr = client.EnsurePreviewCoveringIndexWithStoredValues(ctx, index.Collection, index.Name, index.Fields, index.StoredValues)
	} else {
		provisionErr = client.EnsurePreviewCoveringIndex(ctx, index.Collection, index.Name, index.Fields)
	}
	if provisionErr != nil {
		t.Fatalf("provision compiler-selected category index: %v", provisionErr)
	}
	indexes, err := client.ListPreviewCoveringIndexes(ctx, index.Collection)
	if err != nil {
		t.Fatalf("list category indexes: %v", err)
	}
	foundDefinition := false
	for _, actual := range indexes {
		if actual.Name == index.Name {
			foundDefinition = true
			if actual.Collection != index.Collection || !reflect.DeepEqual(actual.Fields, index.Fields) || !reflect.DeepEqual(actual.StoredValues, index.StoredValues) {
				t.Fatalf("stored category index = %+v, compiler metadata = %+v", actual, index)
			}
		}
	}
	if !foundDefinition {
		t.Fatalf("compiler-selected category index %q missing from %s inventory: %+v", index.Name, index.Collection, indexes)
	}

	plans := explainCategoryScan(t, ctx, client, scan)
	selected := false
	for _, plan := range plans {
		for _, node := range plan.Nodes {
			if node.Type == "EnumerateCollectionNode" && node.Collection == index.Collection {
				t.Fatalf("category plan fully scans indexed collection %s: %+v\nAQL:\n%s", index.Collection, node, scan.Query)
			}
			if node.Type != "IndexNode" || node.Collection != index.Collection {
				continue
			}
			for _, used := range node.Indexes {
				if used.Name != index.Name {
					continue
				}
				if !reflect.DeepEqual(used.Fields, index.Fields) {
					t.Fatalf("EXPLAIN selected index fields %v, compiler emitted %v", used.Fields, index.Fields)
				}
				if requireProjectionCoverage && (node.IndexCoversProjections == nil || !*node.IndexCoversProjections || node.IndexCoversOutProjections == nil || !*node.IndexCoversOutProjections) {
					t.Fatalf("root category IndexNode does not report covering projections: %+v", node)
				}
				selected = true
			}
		}
	}
	if !selected {
		t.Fatalf("EXPLAIN did not select compiler index %s/%s; plans=%+v\nAQL:\n%s", index.Collection, index.Name, plans, scan.Query)
	}
}

func explainCategoryScan(t *testing.T, ctx context.Context, client *store.Client, scan CompiledCategoryScanQuery) []store.ExplainPlan {
	t.Helper()
	explained, err := client.Explain(ctx, store.ExplainRequest{Query: scan.Query, BindVars: scan.BindVars})
	if err != nil {
		t.Fatalf("EXPLAIN compiled category query: %v\nAQL:\n%s\nBindVars: %#v", err, scan.Query, scan.BindVars)
	}
	plans := append([]store.ExplainPlan(nil), explained.Plans...)
	if explained.Plan != nil {
		plans = append(plans, *explained.Plan)
	}
	if len(plans) == 0 {
		t.Fatalf("EXPLAIN returned no plans for category query:\n%s", scan.Query)
	}
	return plans
}

func TestRelatedStreamingCategoryScanUsesEdgeIndexesAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("local Arango test URL and database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Specimen"}, {Name: "Patient"}, {Name: "Observation"}, {Name: "fhir_edge", Edge: true}}}); err != nil {
		t.Fatal(err)
	}
	project := "loom_stream_category_explain_" + uuid.NewString()
	bindings := recipe.RuntimeBindings{Project: project, SelectionProject: project, DatasetGeneration: "generation-stream-category-explain", AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/category/visible"}}
	_, _, scan := compileRelatedCategoryRuntimeScansWithBindings(t, relatedPresencePivotOutput(), bindings, 10)
	if scan.CategoryIndex != nil {
		t.Fatalf("streaming route unexpectedly requests terminal category index: %+v", scan.CategoryIndex)
	}
	from, to := false, false
	for _, plan := range explainCategoryScan(t, ctx, client, scan) {
		for _, node := range plan.Nodes {
			if node.Collection != "fhir_edge" {
				continue
			}
			if node.Type == "EnumerateCollectionNode" {
				t.Fatalf("streaming category scan fully enumerates edges: %+v", node)
			}
			if node.Type != "IndexNode" {
				continue
			}
			for _, index := range node.Indexes {
				for _, field := range index.Fields {
					from = from || field == "_from"
					to = to || field == "_to"
				}
			}
		}
	}
	if !from || !to {
		t.Fatalf("streaming category EXPLAIN omitted edge direction indexes: _from=%t _to=%t", from, to)
	}
}
