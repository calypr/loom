package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestCompileRecipeOutputPreservesMixedChildDependencyOrder(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []recipeplan.SemanticNode{{
			Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
			Fields: []recipeplan.SemanticField{{
				Name: "specimen_id", FieldRef: "Specimen.id",
				Expr:       recipeplan.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "id"})},
				Projection: spec.ProjectionFirst,
			}},
			Children: []recipeplan.SemanticNode{{
				Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Specimen",
			}},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	compiled, err := CompileRecipeOutputWithPolicy(lower.CompiledRecipeOutput{
		Name:             "patients",
		RootResourceType: "Patient",
		RowGrain:         "patient",
		Plan:             plan,
	}, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"}, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}

	parentSet := strings.Index(compiled.Query, "LET child_set_1 =")
	nestedTraversal := strings.Index(compiled.Query, "FOR __loom_physical_parent_1 IN child_set_1")
	if parentSet < 0 || nestedTraversal < 0 || parentSet >= nestedTraversal {
		t.Fatalf("mixed child dependencies rendered out of order:\n%s", compiled.Query)
	}
}
