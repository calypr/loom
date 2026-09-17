package compiler

import (
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
		"SORTED_UNIQUE((",
		"SORT population_member.id",
		"RETURN population_member.id",
		"FOR __loom_physical_population_mapping_member IN (__loom_population_members_value == null ? [] : __loom_population_members_value)",
		"@__loom_physical_population_mapping_member_name",
		"@__loom_physical_population_mapping_row_id_name",
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
	if !containsBindValue(compiled.BindVars, ir.PhysicalPopulationMappingRowIDField) {
		t.Fatalf("row identity witness bind is missing from %#v", compiled.BindVars)
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
	var matched *ir.PhysicalSubplan
	var terminal *ir.PhysicalPopulationMappingReturn
	for index := range physical.Operations {
		operation := &physical.Operations[index]
		if operation.Kind == ir.PhysicalExpressionLetOp && operation.ExpressionLet != nil && operation.ExpressionLet.Variable == ir.PopulationMappingMembersVariable {
			if operation.ExpressionLet.Expression.Subplan == nil {
				t.Fatal("matched-member LET has no typed subplan")
			}
			matched = operation.ExpressionLet.Expression.Subplan
		}
		if operation.Kind == ir.PhysicalPopulationMappingReturnOp {
			terminal = operation.PopulationMappingReturn
		}
	}
	if matched == nil || terminal == nil {
		t.Fatalf("mapping plan missing matched-member LET or witness terminal: %#v", physical.Operations)
	}
	scans := 0
	for _, operation := range matched.Operations {
		if operation.Kind == ir.PhysicalCollectionScanOp {
			scans++
		}
	}
	if scans != 1 {
		t.Fatalf("matched-member collection scans = %d, want 1", scans)
	}
	if !matched.Unique || matched.Sort == nil || matched.Sort.Variable != "population_member" || len(matched.Sort.Path) != 1 || matched.Sort.Path[0] != "id" {
		t.Fatalf("matched-member subplan is not stable sorted unique: %#v", matched)
	}
	if matched.Return.Value == nil || matched.Return.Value.Variable != "population_member" || len(matched.Return.Value.Path) != 1 || matched.Return.Value.Path[0] != "id" {
		t.Fatalf("matched-member subplan does not return selected IDs: %#v", matched.Return)
	}
	if terminal.Members.Value == nil || terminal.Members.Value.Variable != ir.PopulationMappingMembersVariable {
		t.Fatalf("witness terminal does not reuse matched-member value: %#v", terminal.Members)
	}
	if terminal.RowID.Value == nil || terminal.RowID.Value.Variable != "root" || len(terminal.RowID.Value.Path) != 1 || terminal.RowID.Value.Path[0] != "_key" {
		t.Fatalf("witness terminal does not use final stable root identity: %#v", terminal.RowID)
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
	if got := strings.Count(compiled.Query, "FOR population_node_0, population_edge_0 IN 1..1 INBOUND root"); got != 1 {
		t.Fatalf("mapping route traversal count = %d, want 1:\n%s", got, compiled.Query)
	}
	if strings.Index(compiled.Query, "population_member.id == population_node_0.id") > strings.Index(compiled.Query, "FOR __loom_physical_population_mapping_member") {
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
