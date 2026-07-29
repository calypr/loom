package compilerfixture

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

// TestGDCFixtureCoversRichShape keeps the checked-in benchmark representative
// of the dataframe users actually want to build. It is intentionally a corpus
// test, not a compiler test: as physical feature families land, the same
// request remains the parity and Arango-cost benchmark target.
func TestGDCFixtureCoversRichShape(t *testing.T) {
	fixtures, err := LoadDir(filepath.Join("fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture Fixture
	for _, candidate := range fixtures {
		if candidate.ID == "gdc-case-matrix" {
			fixture = candidate
			break
		}
	}
	if fixture.ID == "" {
		t.Fatal("gdc-case-matrix fixture is missing")
	}
	if !hasField(fixture.Recipe.Outputs[0].Fields, "patient_id") || !hasField(fixture.Recipe.Outputs[0].Fields, "case_identifier") {
		t.Fatal("GDC fixture must retain patient identity and case identifier fields")
	}
	var hasPivot, hasSlice, hasAggregate bool
	maxDepth := 0
	var walk func([]recipe.Traversal, int)
	walk = func(steps []recipe.Traversal, depth int) {
		if depth > maxDepth {
			maxDepth = depth
		}
		for _, step := range steps {
			if len(step.Pivots) != 0 {
				hasPivot = true
			}
			if len(step.Slices) != 0 {
				hasSlice = true
			}
			if len(step.Aggregates) != 0 {
				hasAggregate = true
			}
			walk(step.Traversals, depth+1)
		}
	}
	walk(fixture.Recipe.Outputs[0].Traversals, 1)
	if !hasPivot || !hasSlice || !hasAggregate {
		t.Fatalf("GDC fixture coverage pivot=%t slice=%t aggregate=%t", hasPivot, hasSlice, hasAggregate)
	}
	if maxDepth < 3 {
		t.Fatalf("GDC fixture max traversal depth = %d, want at least 3", maxDepth)
	}
	if !strings.Contains(fixture.Description, "nested") {
		t.Fatal("GDC fixture description should identify nested shaping")
	}
	compiled, err := compileRecipe(fixture.Recipe, fixture.Project, 1000, dataframe.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile rich GDC fixture: %v", err)
	}
	diagnostics := compiled.PlanDiagnostics
	t.Logf("GDC compiler plan: traversal_sets=%d shared=%d scope_safe_sharing_groups=%d scope_safe_sharing_sets=%d potential_sharing_groups=%d potential_sharing_sets=%d rich_reuse=%#v", diagnostics.TraversalSets, diagnostics.SharedTraversalCount, diagnostics.ScopedSharingCandidateGroups, diagnostics.ScopedSharingCandidateSets, diagnostics.PotentialSharingOpportunityGroups, diagnostics.PotentialSharingOpportunitySets, diagnostics.RichSourceReuse)
	if diagnostics.TraversalSets == 0 {
		t.Fatal("rich GDC fixture produced no physical traversal sets")
	}
	if diagnostics.SharedTraversalCount == 0 && diagnostics.PotentialSharingOpportunityGroups == 0 {
		t.Fatal("rich GDC fixture should expose traversal-sharing work")
	}
	if len(diagnostics.RichSourceReuse) == 0 {
		t.Fatal("rich GDC fixture should expose repeated rich source consumers")
	}
}

func hasField(fields []recipe.Field, name string) bool {
	for _, field := range fields {
		if field.Name == name {
			return true
		}
	}
	return false
}
