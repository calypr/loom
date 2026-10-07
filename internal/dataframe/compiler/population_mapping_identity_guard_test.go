package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestFinalConstructionPopulationMappingIdentityRequiresCanonicalProjectKey(t *testing.T) {
	tests := []struct {
		name      string
		value     any
		omit      bool
		wantValid bool
	}{
		{name: "valid", value: "fixture_project", wantValid: true},
		{name: "missing", omit: true},
		{name: "blank", value: " \t"},
		{name: "nonstr", value: 17},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := compilePopulationMappingOutput(t, filterExistsPopulationMappingOutput())
			if test.omit {
				delete(output.Plan.BindVars, "project")
			} else {
				output.Plan.BindVars["project"] = test.value
			}
			_, _, err := finalConstructionPopulationMappingIdentity(output.Plan, output)
			if test.wantValid && err != nil {
				t.Fatalf("valid project binding was rejected: %v", err)
			}
			if !test.wantValid && err == nil {
				t.Fatalf("invalid project binding was accepted: %#v", output.Plan.BindVars["project"])
			}
		})
	}
}

func TestFinalConstructionPopulationMappingIdentityPreservesExactKeyOnlyIdentity(t *testing.T) {
	output := compilePopulationMappingOutput(t, filterExistsPopulationMappingOutput())
	output.RowIdentity = output.RowIdentity.Clone()
	output.RowIdentity.Fields = []string{"_key"}
	terminal, identity, err := finalConstructionPopulationMappingIdentity(output.Plan, output)
	if err != nil {
		t.Fatalf("exact public `_key` identity was rejected: %v", err)
	}
	if terminal == nil || terminal.RowIdentityColumn != "_key" || identity == nil || len(identity.Fields) != 1 || identity.Fields[0] != "_key" {
		t.Fatalf("exact public `_key` identity was not preserved: terminal=%#v identity=%#v", terminal, identity)
	}
}

func TestFinalConstructionPopulationMappingIdentityRejectsWrongContributorResource(t *testing.T) {
	output := compilePopulationMappingOutput(t, filterExistsPopulationMappingOutput())
	for index := range output.Plan.StageSequence.FinalColumns {
		column := &output.Plan.StageSequence.FinalColumns[index]
		if column.Name == "_key" && column.RootContributorResourceType == output.RootResourceType {
			column.RootContributorResourceType = "Patient"
			_, _, err := finalConstructionPopulationMappingIdentity(output.Plan, output)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), "contributor") {
				t.Fatalf("wrong contributor resource was not rejected by the contributor guard: %v", err)
			}
			return
		}
	}
	t.Fatalf("fixture omitted the typed root contributor `_key`: %#v", output.Plan.StageSequence.FinalColumns)
}

func filterExistsPopulationMappingOutput() recipe.Output {
	columns := []recipe.StageColumn{{ID: "observation-id", Name: "observation_id", Label: "Observation ID", Type: "string"}}
	return recipe.Output{
		Name: "FilteredObservations", RootResourceType: "Observation", RowGrain: "observation",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields:           []recipe.Field{{Name: "observation_id", ColumnID: "observation-id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-exists", MembershipDigest: "sha256:exists-members", MemberCount: 3,
			ResourceType: "Observation",
		},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: columns,
			Steps: []recipe.ConstructionStep{{
				ID: "keep_observation_id", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
					ColumnID: "observation-id", Operator: recipe.FilterExists,
				}},
				Outputs: columns,
			}},
		},
	}
}
