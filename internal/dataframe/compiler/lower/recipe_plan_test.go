package lower

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestLowerResolvedDefaultRecipeProducesOneTypedPhysicalOutputPerRecipeOutput(t *testing.T) {
	bundle, err := recipe.DefaultACEDBundle()
	if err != nil {
		t.Fatal(err)
	}
	semanticPlan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "p", DatasetGeneration: "g"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(context.Background(), semanticPlan, "scope", "g", nil)
	if err != nil {
		t.Fatal(err)
	}
	physical, err := LowerResolvedRecipePlan(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if len(physical.Outputs) != len(bundle.Outputs) || len(physical.BindVars) == 0 {
		t.Fatalf("unexpected physical recipe plan: %#v", physical)
	}
	group := physical.Outputs[len(physical.Outputs)-1]
	if group.Expansion == nil || group.Identity == nil || len(group.Fields) != 2 {
		t.Fatalf("group output did not preserve expansion/identity: %#v", group)
	}
}
