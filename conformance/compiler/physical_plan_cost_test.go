package compilerfixture

import (
	"path/filepath"
	"testing"

	"github.com/calypr/loom/internal/dataframe"
)

// TestPhysicalPlanOptimizationCorpus is a structural regression gate. It does
// not pretend to replace live Arango PROFILE; it prevents future compiler
// changes from silently removing the generic rewrites that PROFILE is meant
// to measure.
func TestPhysicalPlanOptimizationCorpus(t *testing.T) {
	fixtures, err := LoadDir(filepath.Join("fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		if !fixture.Expected.Supported {
			continue
		}
		fixture := fixture
		t.Run(fixture.ID, func(t *testing.T) {
			compiled, err := compileRecipe(fixture.Recipe, fixture.Project, fixture.Limit, dataframe.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatalf("compile recipe error = %v", err)
			}
			diagnostics := compiled.PlanDiagnostics
			t.Logf("plan traversal_sets=%d shared=%d required_reuse=%d rich_sources=%d", diagnostics.TraversalSets, diagnostics.SharedTraversalCount, diagnostics.RequiredMatchReuseCount, len(diagnostics.RichSourceReuse))
			if fixture.ID == "patient-sibling-targets" && diagnostics.SharedTraversalCount == 0 {
				t.Fatal("generic sibling fixture lost traversal sharing")
			}
			if fixture.ID == "specimen-aggregate-slice" && len(diagnostics.RichSourceReuse) == 0 {
				t.Fatal("aggregate/slice fixture lost rich-source reuse diagnostics")
			}
		})
	}
}
