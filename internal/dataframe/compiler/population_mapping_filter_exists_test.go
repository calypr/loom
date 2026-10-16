package compiler

import (
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestCompilePopulationMappingFilterExistsUsesPinnedRootKey(t *testing.T) {
	output := recipe.Output{
		Name: "FilteredObservations", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{{Name: "observation_id", ColumnID: "observation_id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-filter-exists", MembershipDigest: "sha256:filter-exists-members",
			MemberCount: 3, ResourceType: "Observation",
		},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "observation_id", Name: "observation_id", Type: "string"}},
			Steps: []recipe.ConstructionStep{{
				ID: "filter_existing_observation_id", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
					ColumnID: "observation_id", Operator: recipe.FilterExists,
				}},
				Outputs: []recipe.StageColumn{{ID: "observation_id", Name: "observation_id", Type: "string"}},
			}},
		},
	}

	compiledOutput := compilePopulationMappingOutput(t, output)
	if compiledOutput.Plan.StageSequence == nil || compiledOutput.Plan.StageSequence.FinalRowIdentity != "_key" {
		t.Fatalf("FILTER-only final identity = %#v, want the preserved root _key", compiledOutput.Plan.StageSequence)
	}
	if len(compiledOutput.Plan.StageSequence.Stages) != 1 || compiledOutput.Plan.StageSequence.Stages[0].Kind != ir.PhysicalStageFilterOp {
		t.Fatalf("compiled construction stages = %#v, want the authored FILTER EXISTS stage", compiledOutput.Plan.StageSequence.Stages)
	}

	mapping, err := CompilePopulationMappingOutputWithPolicy(compiledOutput, populationMappingTestBindings(), ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile population mapping for FILTER EXISTS: %v", err)
	}
	if mapping.RowIdentity == nil || !reflect.DeepEqual(mapping.RowIdentity.Fields, []string{"_key"}) {
		t.Fatalf("mapping row identity = %#v, want the single final _key used by the witness terminal", mapping.RowIdentity)
	}
	if mapping.IdentityPartsColumn != ir.PhysicalPopulationMappingIdentityPartsField || mapping.ExplicitIdentityColumn != "" {
		t.Fatalf("mapping witness identity columns = (%q, %q)", mapping.IdentityPartsColumn, mapping.ExplicitIdentityColumn)
	}
	filterAt := strings.Index(mapping.Query, "FILTER __loom_construction_input_1.observation_id != null")
	witnessAt := strings.Index(mapping.Query, "FOR __loom_physical_construction_population_mapping_source_row")
	if filterAt < 0 || witnessAt < 0 || filterAt >= witnessAt {
		t.Fatalf("population mapping did not run the authored filter before its final-row witness terminal:\n%s", mapping.Query)
	}
}
