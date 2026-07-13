package compilerfixture

import (
	"fmt"
	"slices"
	"strings"

	"github.com/calypr/loom/internal/dataframe"
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
	// Conformance exercises the production compiler entrypoint directly.
	compiled, err := dataframe.CompileRequest(fixture.Builder, fixture.Limit)
	return Result{FixtureID: fixture.ID, Compiled: compiled, Err: err}
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
