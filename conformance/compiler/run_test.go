package compilerfixture

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

// Result is the pure-compiler outcome for one oracle fixture. It intentionally
// stops before execution; result-row parity against META belongs to the Arango
// integration corpus.
type Result struct {
	FixtureID string
	Compiled  dataframe.CompiledQuery
	Err       error
}

func Run(fixture Fixture) Result {
	compiled, err := compileRecipe(fixture.Recipe, fixture.Project, fixture.Limit, dataframe.DefaultPhysicalOptimizationPolicy())
	return Result{FixtureID: fixture.ID, Compiled: compiled, Err: err}
}

func compileRecipe(bundle recipe.Bundle, project string, limit int, policy dataframe.PhysicalOptimizationPolicy) (dataframe.CompiledQuery, error) {
	bindings := recipe.RuntimeBindings{Project: project}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		return dataframe.CompiledQuery{}, err
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "conformance", "")
	if err != nil {
		return dataframe.CompiledQuery{}, err
	}
	queries, err := dataframe.CompileResolvedRecipePlanWithPolicy(resolved, limit, policy)
	if err != nil {
		return dataframe.CompiledQuery{}, err
	}
	if len(queries) != 1 {
		return dataframe.CompiledQuery{}, fmt.Errorf("recipe outputs = %d, want one", len(queries))
	}
	return queries[0], nil
}

func recipeWithTraversal(from, label, to string) recipe.Bundle {
	grain, _ := dataframe.InferRowGrain(from)
	return recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "traversal", TranslationVersion: "conformance", Outputs: []recipe.Output{{Name: from, RootResourceType: from, RowGrain: string(grain), Traversals: []recipe.Traversal{{Name: label, ToResourceType: to, Alias: "related"}}}}}
}

func Verify(fixture Fixture) error {
	result := Run(fixture)
	if !fixture.Expected.Supported {
		if result.Err == nil {
			return fmt.Errorf("expected compiler failure containing %q, got success", fixture.Expected.ErrorContains)
		}
		if !strings.Contains(result.Err.Error(), fixture.Expected.ErrorContains) {
			return fmt.Errorf("compiler error %q does not contain %q", result.Err, fixture.Expected.ErrorContains)
		}
		return nil
	}
	if result.Err != nil {
		return result.Err
	}
	if profile := fixture.Expected.PlanProfile; profile != "" && result.Compiled.PlanProfile != profile {
		return fmt.Errorf("plan profile = %q, want %q", result.Compiled.PlanProfile, profile)
	}
	for _, want := range fixture.Expected.OptimizerRules {
		if !slices.Contains(result.Compiled.OptimizationRules, want) {
			return fmt.Errorf("compiled optimizer rules %v do not contain %q", result.Compiled.OptimizationRules, want)
		}
	}
	for _, fragment := range fixture.Expected.QueryContains {
		if !strings.Contains(result.Compiled.Query, fragment) {
			return fmt.Errorf("compiled query does not contain %q", fragment)
		}
	}
	for _, fragment := range fixture.Expected.QueryNotContains {
		if strings.Contains(result.Compiled.Query, fragment) {
			return fmt.Errorf("compiled query unexpectedly contains %q", fragment)
		}
	}
	for key, want := range fixture.Expected.BindVars {
		if got, ok := result.Compiled.BindVars[key]; !ok || fmt.Sprint(got) != fmt.Sprint(want) {
			return fmt.Errorf("bind variable %q = %#v, want %#v", key, got, want)
		}
	}
	for _, want := range fixture.Expected.OutputColumns {
		if !slices.Contains(result.Compiled.Columns, want) {
			return fmt.Errorf("compiled output columns %v do not contain %q", result.Compiled.Columns, want)
		}
	}
	if fixture.Expected.ExpectedTraversalSets != nil && result.Compiled.PlanDiagnostics.TraversalSets != *fixture.Expected.ExpectedTraversalSets {
		return fmt.Errorf("physical traversal sets = %d, want %d", result.Compiled.PlanDiagnostics.TraversalSets, *fixture.Expected.ExpectedTraversalSets)
	}
	return nil
}

func TestCompilerOracleFixtures(t *testing.T) {
	fixtures, err := LoadDir(filepath.Join("fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.ID, func(t *testing.T) {
			if err := Verify(fixture); err != nil {
				t.Fatal(err)
			}
		})
	}
}
