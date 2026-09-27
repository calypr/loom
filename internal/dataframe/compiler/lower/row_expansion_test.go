package lower

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestSchemaDefinedSecondShapeKeepsIndependentArraysSeparate(t *testing.T) {
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	memberScope, err := index.ResolveRowPath("Group", "member[]")
	if err != nil || memberScope.Shape != fhirschema.RowPathArray || memberScope.Cardinality != fhirschema.RowCardinalityMany {
		t.Fatalf("Group member scope = %#v, %v", memberScope, err)
	}
	characteristicScope, err := index.ResolveRowPath("Group", "characteristic[]")
	if err != nil || characteristicScope.Shape != fhirschema.RowPathArray || characteristicScope.Cardinality != fhirschema.RowCardinalityMany {
		t.Fatalf("Group characteristic scope = %#v, %v", characteristicScope, err)
	}

	fixture, err := os.ReadFile("../../../../testdata/s03-independent-arrays/Group.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	var resource struct {
		ResourceType   string           `json:"resourceType"`
		ID             string           `json:"id"`
		Member         []map[string]any `json:"member"`
		Characteristic []map[string]any `json:"characteristic"`
	}
	if err := json.Unmarshal(fixture, &resource); err != nil {
		t.Fatal(err)
	}
	if resource.ResourceType != "Group" || len(resource.Member) != 2 || len(resource.Characteristic) != 3 {
		t.Fatalf("independent-array fixture = type %q with %d members and %d characteristics", resource.ResourceType, len(resource.Member), len(resource.Characteristic))
	}

	output := compileExpansionRecipeOutput(t, recipe.Output{
		Name: "ExpandedGroupMembers", RootResourceType: "Group", RootOccurrenceID: "group-root", RowGrain: "expanded",
		Fields: []recipe.Field{
			{Name: "id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "characteristics", Expr: recipe.Expression{Select: "root.characteristic[]"}},
		},
		Expand:   &recipe.Expansion{OwnerOccurrenceID: "group-root", From: recipe.Expression{Select: "root.member[]"}, As: "member", Ordinality: "position", EmptyPolicy: recipe.ExpansionError},
		Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
	})
	var unnestCount int
	for _, operation := range output.Plan.Operations {
		if operation.Kind == ir.PhysicalUnnestOp {
			unnestCount++
		}
	}
	if unnestCount != 1 {
		t.Fatalf("selected member expansion lowered %d UNNEST operations, want exactly one: %#v", unnestCount, output.Plan.Operations)
	}
	unnest := findRecipeUnnest(t, output.Plan)
	if unnest.Owner.OccurrenceID != "group-root" || unnest.Expression.Extract == nil ||
		unnest.Expression.Extract.Source.Variable != "root" || len(unnest.Expression.Extract.Selector.Steps) != 1 ||
		unnest.Expression.Extract.Selector.Steps[0].Field != "member" || !unnest.Expression.Extract.Selector.Steps[0].Iterate {
		t.Fatalf("selected repeated scope does not resolve only Group.member[]: owner=%#v expression=%#v", unnest.Owner, unnest.Expression)
	}
	rendered, err := aql.RenderPhysicalPlan(output.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToUpper(rendered.Query), "ZIP(") || strings.Count(rendered.Query, "FOR __loom_physical_unnest_index_") != 1 {
		t.Fatalf("independent arrays were implicitly zipped or multiplied:\n%s", rendered.Query)
	}
	if !strings.Contains(rendered.Query, "characteristic") || !strings.Contains(rendered.Query, "member") {
		t.Fatalf("rendered expansion did not preserve both independent source arrays:\n%s", rendered.Query)
	}
	if len(resource.Member) != 2 || len(resource.Characteristic) != 3 {
		t.Fatalf("selected member scope changed fixture cardinalities: %d x %d", len(resource.Member), len(resource.Characteristic))
	}
}

func TestCompileRecipeRootExpansionBuildsOccurrenceIdentity(t *testing.T) {
	output := compileExpansionRecipeOutput(t, recipe.Output{
		Name: "ExpandedPatients", RootResourceType: "Patient", RootOccurrenceID: "patient-root", RowGrain: "expanded",
		Fields:   []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Expand:   &recipe.Expansion{OwnerOccurrenceID: "patient-root", From: recipe.Expression{Select: "root.identifier[]"}, As: "item", Ordinality: "position", EmptyPolicy: recipe.ExpansionPreserveParent},
		Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
	})
	if len(output.RowIdentity.Fields) != 3 || output.RowIdentity.Fields[2] != "__loom_expansion_identity" {
		t.Fatalf("row identity fields = %#v", output.RowIdentity.Fields)
	}
	unnest := findRecipeUnnest(t, output.Plan)
	if unnest.Owner.OccurrenceID != "patient-root" || len(unnest.Owner.Route) != 0 || unnest.Owner.OwnerVariable != "root" {
		t.Fatalf("root expansion owner = %#v", unnest.Owner)
	}
	if got := ir.PhysicalUnnestSortKeys(*unnest); len(got) != 3 || got[0].Variable != "root" || got[1].Variable != "__loom_has_expanded_item" || got[2].Variable != "position" {
		t.Fatalf("root expansion sort keys = %#v", got)
	}
	identity := findExpansionIdentityProjection(t, output.Plan)
	fields := make(map[string]ir.PhysicalExpression)
	for _, field := range identity.Object.Fields {
		fields[field.Name] = field.Expression
	}
	if fields["item_present"].Value == nil || fields["item_present"].Value.Variable != "__loom_has_expanded_item" {
		t.Fatalf("item/empty discriminator = %#v", fields["item_present"])
	}
	if fields["ordinal"].Value == nil || fields["ordinal"].Value.Variable != "position" {
		t.Fatalf("ordinal identity = %#v", fields["ordinal"])
	}
	rendered, err := aql.RenderPhysicalPlan(output.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"LENGTH(__loom_physical_unnest_source_0) == 0 ? [null]",
		"LET __loom_has_expanded_item = __loom_physical_unnest_index_0 != null",
		"LET position = __loom_physical_unnest_index_0",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("root expansion query missing %q:\n%s", want, rendered.Query)
		}
	}
	if !containsExpansionIdentityName(rendered.BindVars) {
		t.Fatalf("rendered bind vars do not carry the hidden identity projection name: %#v", rendered.BindVars)
	}
}

func TestExpandedItemProjectionUsesItemDefinitionAndPreservesModes(t *testing.T) {
	output := compileExpansionRecipeOutput(t, recipe.Output{
		Name: "ExpandedObservationCodings", RootResourceType: "Observation", RootOccurrenceID: "observation-root", RowGrain: "expanded",
		Fields: []recipe.Field{
			{Name: "first_code", FieldRef: "component[].code.coding[].code", Expr: recipe.Expression{Select: "item.code"}, ValueMode: recipe.ValueModeFirst},
			{Name: "all_codes", FieldRef: "component[].code.coding[].code", Expr: recipe.Expression{Select: "item.code"}, ValueMode: recipe.ValueModeAll},
			{Name: "observation_id", FieldRef: "id", Expr: recipe.Expression{Select: "root.id"}, ValueMode: recipe.ValueModeFirst},
		},
		Expand:   &recipe.Expansion{OwnerOccurrenceID: "observation-root", From: recipe.Expression{Select: "root.component[].code.coding[]"}, As: "item", Ordinality: "position", EmptyPolicy: recipe.ExpansionPreserveParent},
		Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
	})
	projections := map[string]ir.PhysicalProjection{}
	for _, operation := range output.Plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			projections[projection.Name] = projection
		}
	}
	for name, want := range map[string]struct {
		cardinality  ir.PhysicalCardinality
		nullBehavior ir.PhysicalNullBehavior
		resource     string
		selector     string
	}{
		"first_code": {cardinality: ir.PhysicalScalarCardinality, nullBehavior: ir.PhysicalPreserveNull, resource: "Coding", selector: "code"},
		"all_codes":  {cardinality: ir.PhysicalArrayCardinality, nullBehavior: ir.PhysicalEmptyOnNull, resource: "Coding", selector: "code"},
	} {
		projection, ok := projections[name]
		if !ok || projection.Expression == nil || projection.Expression.Extract == nil {
			t.Fatalf("projection %q is not a physical extract: %#v", name, projection)
		}
		extract := projection.Expression.Extract
		if projection.Expression.Cardinality != want.cardinality || projection.Expression.NullBehavior != want.nullBehavior || extract.ResourceType != want.resource || extract.Source.Variable != "item" || len(extract.Source.Path) != 0 || extract.Selector.CanonicalPath() != want.selector {
			t.Errorf("projection %q = cardinality %q/null %q, resource %q, source %#v, selector %q; want %q/%q, %q, item, %q", name, projection.Expression.Cardinality, projection.Expression.NullBehavior, extract.ResourceType, extract.Source, extract.Selector.CanonicalPath(), want.cardinality, want.nullBehavior, want.resource, want.selector)
		}
	}
	if unnest := findRecipeUnnest(t, output.Plan); unnest.EmptyPolicy != ir.PhysicalUnnestPreserveParent {
		t.Fatalf("expanded empty/missing policy = %q, want preserve parent", unnest.EmptyPolicy)
	}
	id, ok := projections["observation_id"]
	if !ok || id.Expression == nil || id.Expression.Extract == nil || id.Expression.Extract.ResourceType != "Observation" || id.Expression.Extract.Source.Variable != "root" || id.Expression.Extract.Selector.CanonicalPath() != "id" {
		t.Fatalf("unrelated root field was rebound to expanded item: %#v", id)
	}
}

func TestCompileRecipeRelatedExpansionReusesDeepOwnerRoute(t *testing.T) {
	output := compileExpansionRecipeOutput(t, recipe.Output{
		Name: "ExpandedDocuments", RootResourceType: "Patient", RootOccurrenceID: "patient-root", RowGrain: "expanded",
		TraversalColumnNaming: recipe.TraversalColumnNamingAlias,
		Fields:                []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Expand:                &recipe.Expansion{OwnerOccurrenceID: "document-owner", From: recipe.Expression{Select: "document.identifier[]"}, As: "item", Ordinality: "position", EmptyPolicy: recipe.ExpansionError},
		Identity:              &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
		Traversals: []recipe.Traversal{
			{
				Name: "subject_Patient", OccurrenceID: "specimen-owner", Alias: "specimen", ToResourceType: "Specimen",
				Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "specimen.id"}}},
				Traversals: []recipe.Traversal{{
					Name: "subject_Specimen", OccurrenceID: "document-owner", Alias: "document", ToResourceType: "DocumentReference",
					Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "document.id"}}},
				}},
			},
			{
				Name: "focus_Patient", OccurrenceID: "focus-owner", Alias: "focus", ToResourceType: "Observation",
				Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "focus.id"}}},
			},
		},
	})
	var expansion *ir.PhysicalUnnest
	sets := map[string]bool{}
	for _, operation := range output.Plan.Operations {
		if operation.Kind == ir.PhysicalUnnestOp {
			expansion = operation.Unnest
		}
		if operation.Kind == ir.PhysicalSetOp && operation.Set != nil {
			sets[operation.Source.SemanticNode] = true
		}
	}
	if expansion == nil || len(expansion.Owner.Route) != 2 || expansion.Owner.OccurrenceID != "document-owner" || expansion.Owner.OwnerVariable != "__loom_expansion_node_2" {
		t.Fatalf("compiled related expansion = %#v", expansion)
	}
	if expansion.Owner.Route[0].OccurrenceID != "specimen-owner" || expansion.Owner.Route[1].OccurrenceID != "document-owner" {
		t.Fatalf("route occurrence order = %#v", expansion.Owner.Route)
	}
	if sets["specimen"] || sets["document"] || !sets["focus"] {
		t.Fatalf("selected-path reuse / sibling row-set layout = %#v", sets)
	}
	identity := findExpansionIdentityProjection(t, output.Plan)
	var edgeWitnesses []string
	for _, field := range identity.Object.Fields {
		if strings.HasPrefix(field.Name, "edge_") {
			edgeWitnesses = append(edgeWitnesses, field.Name)
		}
	}
	if len(edgeWitnesses) != 2 || edgeWitnesses[0] != "edge_0_id" || edgeWitnesses[1] != "edge_1_id" {
		t.Fatalf("ordered route witnesses = %#v", edgeWitnesses)
	}
	rendered, err := aql.RenderPhysicalPlan(output.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"FOR __loom_expansion_edge_1 IN @@expansion_route_1_edge_collection",
		"FOR __loom_expansion_edge_2 IN @@expansion_route_2_edge_collection",
		"FILTER ASSERT(LENGTH(__loom_physical_unnest_source_0) > 0",
		"__loom_expansion_edge_1._id",
		"__loom_expansion_edge_2._id",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("related expansion query missing %q:\n%s", want, rendered.Query)
		}
	}
}

func compileExpansionRecipeOutput(t *testing.T, output recipe.Output) CompiledRecipeOutput {
	t.Helper()
	plan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "row-expansion", TranslationVersion: "test", Outputs: []recipe.Output{output},
	}, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Outputs) != 1 {
		t.Fatalf("compiled output count = %d", len(compiled.Outputs))
	}
	return compiled.Outputs[0]
}

func findRecipeUnnest(t *testing.T, plan ir.PhysicalPlan) *ir.PhysicalUnnest {
	t.Helper()
	for index := range plan.Operations {
		if plan.Operations[index].Kind == ir.PhysicalUnnestOp && plan.Operations[index].Unnest != nil {
			return plan.Operations[index].Unnest
		}
	}
	t.Fatal("compiled plan has no row expansion")
	return nil
}

func findExpansionIdentityProjection(t *testing.T, plan ir.PhysicalPlan) *ir.PhysicalExpression {
	t.Helper()
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name == "__loom_expansion_identity" {
				if !projection.Hidden || projection.Expression == nil || projection.Expression.Object == nil {
					t.Fatalf("expansion identity projection = %#v", projection)
				}
				return projection.Expression
			}
		}
	}
	t.Fatal("compiled plan has no hidden expansion identity object")
	return nil
}

func containsExpansionIdentityName(bindVars map[string]any) bool {
	for _, value := range bindVars {
		if value == "__loom_expansion_identity" {
			return true
		}
	}
	return false
}
