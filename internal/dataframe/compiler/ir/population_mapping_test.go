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
			"project":         "project-a",
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
		IdentityParts: []PhysicalPopulationMappingIdentityPart{
			{Name: "project", Expression: PhysicalExpression{
				Kind: PhysicalValueExpression, Cardinality: PhysicalScalarCardinality, NullBehavior: PhysicalPreserveNull,
				Value: &PhysicalValue{BindKey: "project"},
			}},
			{Name: "_key", Expression: PhysicalExpression{
				Kind: PhysicalValueExpression, Cardinality: PhysicalScalarCardinality, NullBehavior: PhysicalPreserveNull,
				Value: &PhysicalValue{Variable: "root", Path: []string{"_key"}},
			}},
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
			name: "identity part array",
			edit: func(terminal *PhysicalPopulationMappingReturn) {
				terminal.IdentityParts[0].Expression.Cardinality = PhysicalArrayCardinality
			},
			want: "mapping identity part \"project\" must be scalar",
		},
		{
			name: "explicit identity array",
			edit: func(terminal *PhysicalPopulationMappingReturn) {
				expression := PhysicalExpression{Kind: PhysicalValueExpression, Cardinality: PhysicalArrayCardinality, NullBehavior: PhysicalPreserveNull, Value: &PhysicalValue{Variable: "root", Path: []string{"id"}}}
				terminal.ExplicitIdentity = &expression
			},
			want: "mapping explicit identity must be scalar",
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
	terminal.IdentityParts[1].Expression.Value.Path[0] = "changed"
	if got := plan.Operations[2].PopulationMappingReturn.Members.Value.Variable; got != PopulationMappingMembersVariable {
		t.Fatalf("clone mutation changed source members variable = %q", got)
	}
	if got := plan.Operations[2].PopulationMappingReturn.IdentityParts[1].Expression.Value.Path[0]; got != "_key" {
		t.Fatalf("clone mutation changed source identity part path = %q", got)
	}
}
