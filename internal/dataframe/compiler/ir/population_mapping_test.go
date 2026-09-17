package ir

import (
	"strings"
	"testing"
)

func populationMappingReturnPlan(terminal PhysicalPopulationMappingReturn) PhysicalPlan {
	return PhysicalPlan{
		Version: 1,
		BindVars: map[string]any{
			"root_collection": "Specimen",
		},
		Operations: []PhysicalOperation{
			{Kind: PhysicalRootScanOp, RootScan: &PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: PhysicalExpressionLetOp, ExpressionLet: &PhysicalExpressionLet{
				Variable: PopulationMappingMembersVariable,
				Expression: PhysicalExpression{
					Kind: PhysicalValueExpression, Cardinality: PhysicalArrayCardinality, NullBehavior: PhysicalEmptyOnNull,
					Value: &PhysicalValue{Variable: "root", Path: []string{"members"}},
				},
			}},
			{Kind: PhysicalPopulationMappingReturnOp, PopulationMappingReturn: &terminal},
		},
	}
}

func validPopulationMappingReturn() PhysicalPopulationMappingReturn {
	return PhysicalPopulationMappingReturn{
		Members: PhysicalExpression{
			Kind: PhysicalValueExpression, Cardinality: PhysicalArrayCardinality, NullBehavior: PhysicalEmptyOnNull,
			Value: &PhysicalValue{Variable: PopulationMappingMembersVariable},
		},
		RowID: PhysicalExpression{
			Kind: PhysicalValueExpression, Cardinality: PhysicalScalarCardinality, NullBehavior: PhysicalPreserveNull,
			Value: &PhysicalValue{Variable: "root", Path: []string{"_key"}},
		},
	}
}

func TestPhysicalPopulationMappingReturnValidatesTypedWitnessTerminal(t *testing.T) {
	if err := populationMappingReturnPlan(validPopulationMappingReturn()).Validate(); err != nil {
		t.Fatalf("valid mapping terminal rejected: %v", err)
	}
}

func TestPhysicalPopulationMappingReturnRejectsIllegalShapes(t *testing.T) {
	tests := []struct {
		name string
		edit func(*PhysicalPopulationMappingReturn)
		want string
	}{
		{
			name: "members scalar",
			edit: func(terminal *PhysicalPopulationMappingReturn) {
				terminal.Members.Cardinality = PhysicalScalarCardinality
			},
			want: "EMPTY_ON_NULL array",
		},
		{
			name: "members preserve null",
			edit: func(terminal *PhysicalPopulationMappingReturn) { terminal.Members.NullBehavior = PhysicalPreserveNull },
			want: "EMPTY_ON_NULL array",
		},
		{
			name: "row identity array",
			edit: func(terminal *PhysicalPopulationMappingReturn) { terminal.RowID.Cardinality = PhysicalArrayCardinality },
			want: "row identity must be scalar",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			terminal := validPopulationMappingReturn()
			test.edit(&terminal)
			err := populationMappingReturnPlan(terminal).Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() = %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestClonePhysicalPopulationMappingReturnClonesWitnessExpressions(t *testing.T) {
	plan := populationMappingReturnPlan(validPopulationMappingReturn())
	clone := ClonePhysicalPlan(plan)
	terminal := clone.Operations[2].PopulationMappingReturn
	terminal.Members.Value.Variable = "changed"
	terminal.RowID.Value.Path[0] = "changed"
	if got := plan.Operations[2].PopulationMappingReturn.Members.Value.Variable; got != PopulationMappingMembersVariable {
		t.Fatalf("clone mutation changed source members variable = %q", got)
	}
	if got := plan.Operations[2].PopulationMappingReturn.RowID.Value.Path[0]; got != "_key" {
		t.Fatalf("clone mutation changed source row identity path = %q", got)
	}
}
