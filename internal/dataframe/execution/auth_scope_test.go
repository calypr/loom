package execution

import (
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	aql "github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestGenericPhysicalPlanRestrictedEmptyScopeBindsFalse(t *testing.T) {
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(
		semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient"}},
		semantic.ExecutionContext{Project: "P1", AuthScopeMode: authscope.ReadScopeRestricted},
		ir.DefaultPhysicalOptimizationPolicy(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := plan.BindVars["auth_resource_paths_unrestricted"].(bool); !ok || got {
		t.Fatalf("physical unrestricted bind = %#v, want false", plan.BindVars["auth_resource_paths_unrestricted"])
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := rendered.BindVars["auth_resource_paths_unrestricted"].(bool); !ok || got {
		t.Fatalf("rendered physical unrestricted bind = %#v, want false", rendered.BindVars["auth_resource_paths_unrestricted"])
	}
}
