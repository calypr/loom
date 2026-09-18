package compiler

import (
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestCompilePopulationMappingDirectKeepsMemberRowWitnessesInternal(t *testing.T) {
	compiled := compilePopulationMappingRecipe(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 3,
			ResourceType: "Specimen",
		},
	})
	if got := strings.Count(compiled.Query, "FOR population_member IN @@population_members_collection"); got != 1 {
		t.Fatalf("mapping member scan count = %d, want 1:\n%s", got, compiled.Query)
	}
	for _, want := range []string{
		"COLLECT __loom_physical_population_root_key = population_source._key INTO __loom_physical_population_member_ids = population_member.id",
		"LET __loom_population_members_value = SORTED_UNIQUE(__loom_physical_population_member_ids)",
		"FOR __loom_physical_population_mapping_member IN (__loom_population_members_value == null ? [] : __loom_population_members_value)",
		"@__loom_physical_population_mapping_member_name",
		"@__loom_physical_population_mapping_identity_name",
		"[@project, root._key]",
	} {
		if !strings.Contains(compiled.Query, want) {
			t.Fatalf("mapping query is missing %q:\n%s", want, compiled.Query)
		}
	}
	if strings.Contains(compiled.Query, "RETURN { [@__loom_projection") {
		t.Fatalf("mapping query used a public projection return:\n%s", compiled.Query)
	}
	if !containsBindValue(compiled.BindVars, ir.PhysicalPopulationMappingMemberField) {
		t.Fatalf("member witness bind is missing from %#v", compiled.BindVars)
	}
	if !containsBindValue(compiled.BindVars, ir.PhysicalPopulationMappingIdentityPartsField) {
		t.Fatalf("identity-parts witness bind is missing from %#v", compiled.BindVars)
	}
	if compiled.IdentityPartsColumn != ir.PhysicalPopulationMappingIdentityPartsField || compiled.ExplicitIdentityColumn != "" {
		t.Fatalf("default mapping identity columns = (%q, %q)", compiled.IdentityPartsColumn, compiled.ExplicitIdentityColumn)
	}
	if compiled.RowIdentity == nil || len(compiled.RowIdentity.Fields) != 2 || compiled.RowIdentity.Fields[0] != "project" || compiled.RowIdentity.Fields[1] != "_key" {
		t.Fatalf("default mapping row identity = %#v", compiled.RowIdentity)
	}
}

func TestCompilePopulationMappingExplicitIdentityUsesCompiledExpression(t *testing.T) {
	compiled := compilePopulationMappingRecipe(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields:   []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Identity: &recipe.Identity{Name: "row", Expr: recipe.Expression{Select: "root.id"}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 3,
			ResourceType: "Specimen",
		},
	})
	if !strings.Contains(compiled.Query, "@__loom_physical_population_mapping_identity_name]: root.payload.id") || strings.Contains(compiled.Query, "@__loom_physical_population_mapping_identity_name]: [root.payload.id]") {
		t.Fatalf("mapping query did not preserve the compiled explicit identity expression:\n%s", compiled.Query)
	}
	if containsBindValue(compiled.BindVars, ir.PhysicalPopulationMappingIdentityPartsField) {
		t.Fatalf("explicit mapping unexpectedly rendered default identity parts: %#v", compiled.BindVars)
	}
	if !containsBindValue(compiled.BindVars, ir.PhysicalPopulationMappingExplicitIdentityField) {
		t.Fatalf("explicit identity bind is missing: %#v", compiled.BindVars)
	}
	if compiled.IdentityPartsColumn != "" || compiled.ExplicitIdentityColumn != ir.PhysicalPopulationMappingExplicitIdentityField {
		t.Fatalf("explicit mapping identity columns = (%q, %q)", compiled.IdentityPartsColumn, compiled.ExplicitIdentityColumn)
	}
}

func TestCompilePopulationMappingDefaultIdentityIsStableAcrossInnerOuterExpansion(t *testing.T) {
	queries := make(map[string]CompiledPopulationMappingQuery, 2)
	for _, mode := range []string{"INNER", "OUTER"} {
		output := compilePopulationMappingOutput(t, recipe.Output{
			Name: "Specimens", RootResourceType: "Specimen", RowGrain: "expanded",
			Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
			Expand: &recipe.Expansion{From: recipe.Expression{Select: "root.extension[]"}, As: "item"},
			Population: &recipe.PopulationConstraint{
				SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 2,
				ResourceType: "Specimen",
			},
		})
		if mode == "OUTER" {
			for index := range output.Plan.Operations {
				if output.Plan.Operations[index].Kind == ir.PhysicalUnnestOp && output.Plan.Operations[index].Unnest != nil {
					output.Plan.Operations[index].Unnest.JoinMode = ir.PhysicalUnnestOuter
				}
			}
		}
		compiled, err := CompilePopulationMappingOutputWithPolicy(output, recipe.RuntimeBindings{}, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(compiled.Query, "[@project, root._key]") {
			t.Fatalf("%s mapping query does not carry canonical project/key identity parts:\n%s", mode, compiled.Query)
		}
		if compiled.IdentityPartsColumn != ir.PhysicalPopulationMappingIdentityPartsField || compiled.ExplicitIdentityColumn != "" {
			t.Fatalf("%s mapping identity columns = (%q, %q)", mode, compiled.IdentityPartsColumn, compiled.ExplicitIdentityColumn)
		}
		queries[mode] = compiled
	}
	if !reflect.DeepEqual(queries["INNER"].RowIdentity, queries["OUTER"].RowIdentity) {
		t.Fatalf("inner/outer mapping identities differ: %#v != %#v", queries["INNER"].RowIdentity, queries["OUTER"].RowIdentity)
	}
}

func TestCompilePopulationMappingRejectsMissingIdentityField(t *testing.T) {
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 3,
			ResourceType: "Specimen",
		},
	})
	output.RowIdentity.Fields = []string{"project", "missing_identity_field"}
	if _, err := CompilePopulationMappingOutputWithPolicy(output, recipe.RuntimeBindings{}, ir.DefaultPhysicalOptimizationPolicy()); err == nil || !strings.Contains(err.Error(), "missing_identity_field") {
		t.Fatalf("missing identity field error = %v", err)
	}
}

func TestPopulationMappingPhysicalPlanCollectsMatchedIDsOnceAndUsesFinalTerminal(t *testing.T) {
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 3,
			ResourceType: "Specimen",
		},
	})
	physical, err := mappingPhysicalPlan(output, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	var terminal *ir.PhysicalPopulationMappingReturn
	var rootSource *ir.PhysicalPopulationRootSource
	for index := range physical.Operations {
		operation := &physical.Operations[index]
		if operation.Kind == ir.PhysicalRootScanOp && operation.RootScan != nil {
			rootSource = operation.RootScan.Population
		}
		if operation.Kind == ir.PhysicalPopulationMappingReturnOp {
			terminal = operation.PopulationMappingReturn
		}
	}
	if rootSource == nil || terminal == nil {
		t.Fatalf("mapping plan missing population root source or witness terminal: %#v", physical.Operations)
	}
	if rootSource.MemberScan.Variable != "population_member" || rootSource.MemberScan.CollectionBindKey != "population_members_collection" {
		t.Fatalf("mapping root source has wrong member scan: %#v", rootSource.MemberScan)
	}
	if rootSource.CollectMembersVariable != ir.PopulationMappingMembersVariable {
		t.Fatalf("mapping root source does not collect member witnesses: %#v", rootSource)
	}
	if rootSource.MemberID.Variable != "population_member" || len(rootSource.MemberID.Path) != 1 || rootSource.MemberID.Path[0] != "id" {
		t.Fatalf("mapping root source does not collect selected IDs: %#v", rootSource.MemberID)
	}
	if terminal.Members.Value == nil || terminal.Members.Value.Variable != ir.PopulationMappingMembersVariable {
		t.Fatalf("witness terminal does not reuse matched-member value: %#v", terminal.Members)
	}
	if len(terminal.IdentityParts) != 2 || terminal.IdentityParts[0].Name != "project" || terminal.IdentityParts[1].Name != "_key" {
		t.Fatalf("witness terminal identity parts are not canonical: %#v", terminal.IdentityParts)
	}
	if terminal.IdentityParts[0].Expression.Value == nil || terminal.IdentityParts[0].Expression.Value.BindKey != "project" {
		t.Fatalf("witness terminal project identity is not compiler-bound: %#v", terminal.IdentityParts[0])
	}
	if terminal.IdentityParts[1].Expression.Value == nil || terminal.IdentityParts[1].Expression.Value.Variable != "root" || len(terminal.IdentityParts[1].Expression.Value.Path) != 1 || terminal.IdentityParts[1].Expression.Value.Path[0] != "_key" {
		t.Fatalf("witness terminal key identity is not final root key: %#v", terminal.IdentityParts[1])
	}
}

func TestCompilePopulationMappingReversedRouteRunsOnceBeforeWitnessTerminal(t *testing.T) {
	compiled := compilePopulationMappingRecipe(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-documents", MembershipDigest: "sha256:members", MemberCount: 3,
			ResourceType: "DocumentReference",
			Route:        []recipe.PopulationRouteStep{{ResourceType: "DocumentReference", Relationship: "subject_Specimen"}},
		},
	})
	if got := strings.Count(compiled.Query, "FOR population_root_edge_0 IN @@population_root_route_0_edge_collection"); got != 1 {
		t.Fatalf("mapping route traversal count = %d, want 1:\n%s", got, compiled.Query)
	}
	if strings.Index(compiled.Query, "population_source.id == population_member.id") > strings.Index(compiled.Query, "FOR __loom_physical_population_mapping_member") {
		t.Fatalf("mapping witness terminal preceded member matching:\n%s", compiled.Query)
	}
}

func TestCompilePopulationMappingRequiredNavigationRemainsBeforeWitnesses(t *testing.T) {
	compiled := compilePopulationMappingRecipe(t, recipe.Output{
		Name: "Patients", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Traversals: []recipe.Traversal{{
			Name: "subject_Patient", ToResourceType: "Condition", MatchMode: recipe.MatchRequired,
			Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "id"}}},
		}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 3,
			ResourceType: "Patient",
		},
	})
	if !strings.Contains(compiled.Query, "required_0_node_0") {
		t.Fatalf("mapping query lost required downstream elimination:\n%s", compiled.Query)
	}
	if strings.Index(compiled.Query, "required_0_node_0") > strings.Index(compiled.Query, "FOR __loom_physical_population_mapping_member") {
		t.Fatalf("required match was evaluated after witnesses:\n%s", compiled.Query)
	}
}

func TestCompilePopulationMappingPreservesInnerAndOuterFinalRowSemantics(t *testing.T) {
	for _, mode := range []string{"INNER", "OUTER"} {
		t.Run(mode, func(t *testing.T) {
			output := compilePopulationMappingOutput(t, recipe.Output{
				Name: "Specimens", RootResourceType: "Specimen", RowGrain: "expanded",
				Fields:   []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
				Expand:   &recipe.Expansion{From: recipe.Expression{Select: "root.extension[]"}, As: "item"},
				Identity: &recipe.Identity{Name: "row", Expr: recipe.Expression{Select: "root.id"}},
				Population: &recipe.PopulationConstraint{
					SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 2,
					ResourceType: "Specimen",
				},
			})
			if mode == "OUTER" {
				found := false
				for index := range output.Plan.Operations {
					if output.Plan.Operations[index].Kind != ir.PhysicalUnnestOp || output.Plan.Operations[index].Unnest == nil {
						continue
					}
					output.Plan.Operations[index].Unnest.JoinMode = ir.PhysicalUnnestOuter
					found = true
				}
				if !found {
					t.Fatal("compiled expansion has no UNNEST operation")
				}
			}
			compiled, err := CompilePopulationMappingOutputWithPolicy(output, recipe.RuntimeBindings{Project: "project-a", SelectionProject: "project/a", DatasetGeneration: "generation-a", SelectionMembersCollection: "loom_explorer_selection_members"}, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			if mode == "INNER" && !strings.Contains(compiled.Query, "FOR item IN") {
				t.Fatalf("inner mapping query lost expansion:\n%s", compiled.Query)
			}
			if mode == "OUTER" && !strings.Contains(compiled.Query, "LET item =") {
				t.Fatalf("outer mapping query lost expansion:\n%s", compiled.Query)
			}
			if mode == "INNER" && !strings.Contains(compiled.Query, "FOR item IN __loom_physical_unnest_source_0") {
				t.Fatalf("inner mapping query did not preserve inner expansion:\n%s", compiled.Query)
			}
			if mode == "OUTER" && !strings.Contains(compiled.Query, "LENGTH(__loom_physical_unnest_source_0) == 0 ? [null]") {
				t.Fatalf("outer mapping query did not preserve outer expansion:\n%s", compiled.Query)
			}
			if strings.Index(compiled.Query, "FOR item IN") > strings.Index(compiled.Query, "FOR __loom_physical_population_mapping_member") {
				t.Fatalf("%s witness terminal ran before final expansion:\n%s", mode, compiled.Query)
			}
		})
	}
}

func containsBindValue(bindVars map[string]any, want string) bool {
	for _, value := range bindVars {
		if value == want {
			return true
		}
	}
	return false
}

func compilePopulationMappingRecipe(t *testing.T, output recipe.Output) CompiledPopulationMappingQuery {
	t.Helper()
	compiled := compilePopulationMappingOutput(t, output)
	mapping, err := CompilePopulationMappingOutputWithPolicy(compiled, recipe.RuntimeBindings{Project: "project-a", SelectionProject: "project/a", DatasetGeneration: "generation-a", SelectionMembersCollection: "loom_explorer_selection_members"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return mapping
}

func compilePopulationMappingOutput(t *testing.T, output recipe.Output) lower.CompiledRecipeOutput {
	t.Helper()
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "population-mapping-test",
		TranslationVersion:  "population-mapping-test",
		Outputs:             []recipe.Output{output},
	}
	bindings := recipe.RuntimeBindings{Project: "project-a", SelectionProject: "project/a", DatasetGeneration: "generation-a", SelectionMembersCollection: "loom_explorer_selection_members"}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return compiled.Outputs[0]
}
