package dataframe

import (
	"reflect"
	"testing"
)

func TestExplainCompilerRequestIncludesNavigationOnlyGenericPhysicalPlan(t *testing.T) {
	explanation, err := ExplainCompilerRequest(Builder{
		Project:           "P1",
		AuthResourcePaths: []string{"/programs/p1"},
		RootResourceType:  "Patient",
	}, 25)
	if err != nil {
		t.Fatal(err)
	}
	if explanation.Version != CompilerExplanationVersion {
		t.Fatalf("explanation version = %d", explanation.Version)
	}
	if explanation.SemanticPlan.RowIdentity == nil || explanation.SemanticPlan.RowIdentity.Grain != RowGrainPatient {
		t.Fatalf("semantic row identity = %#v", explanation.SemanticPlan.RowIdentity)
	}
	if !explanation.GenericPhysicalPlan.Available || explanation.GenericPhysicalPlan.Reason != "" || explanation.GenericPhysicalPlan.Plan == nil {
		t.Fatalf("generic physical plan availability = %#v", explanation.GenericPhysicalPlan)
	}
	if err := explanation.GenericPhysicalPlan.Plan.Validate(); err != nil {
		t.Fatalf("explained generic physical plan does not validate: %v", err)
	}
	if got := explanation.GenericPhysicalPlan.Plan.BindVars["root_collection"]; got != "Patient" {
		t.Fatalf("root collection = %#v", got)
	}
	operations := explanation.GenericPhysicalPlan.Plan.Operations
	if len(operations) < 8 || operations[5].Kind != PhysicalSortOp || operations[6].Kind != PhysicalLimitOp || explanation.GenericPhysicalPlan.Plan.BindVars[genericPhysicalExecutionLimitBind] != 25 {
		t.Fatalf("explained physical execution window = %#v / %#v", operations, explanation.GenericPhysicalPlan.Plan.BindVars)
	}
	if !reflect.DeepEqual(explanation.Compiled.Columns, []string{"_key"}) || explanation.Compiled.Limit != 25 {
		t.Fatalf("compiled metadata = %#v", explanation.Compiled)
	}
}

func TestExplainCompilerRequestReportsSelectionPhysicalUnavailabilityAfterCompile(t *testing.T) {
	explanation, err := ExplainCompilerRequest(Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		Fields: []FieldSelect{{
			Name: "gender", FieldRef: "Patient.gender", Select: "gender",
		}},
	}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if explanation.GenericPhysicalPlan.Available || explanation.GenericPhysicalPlan.Plan != nil {
		t.Fatalf("selection request unexpectedly received generic physical plan: %#v", explanation.GenericPhysicalPlan)
	}
	if explanation.GenericPhysicalPlan.Reason != GenericPhysicalPlanReasonSelections {
		t.Fatalf("physical reason = %q", explanation.GenericPhysicalPlan.Reason)
	}
	if !reflect.DeepEqual(explanation.Compiled.Columns, []string{"_key", "gender"}) {
		t.Fatalf("compiled columns = %#v", explanation.Compiled.Columns)
	}
	if len(explanation.Selections) != 1 || explanation.Selections[0].Alias != "root.gender" || explanation.Selections[0].Projection != ProjectionScalar {
		t.Fatalf("normalized selections = %#v", explanation.Selections)
	}
}

func TestExplainCompilerRequestCarriesFilterRowGrainAndOptimizerProvenance(t *testing.T) {
	female := "female"
	explanation, err := ExplainCompilerRequest(Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		RowGrain:         RowGrainPatient,
		Filters: []TypedFilter{{
			FieldRef: "Patient.gender", Selector: "gender", FieldKind: FilterString, Operator: FilterEquals,
			Values: []FilterValue{{Kind: FilterString, String: &female}},
		}},
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, identity := range []*RowIdentity{
		explanation.SemanticPlan.RowIdentity,
		explanation.Lowered.RowIdentity,
		explanation.Compiled.RowIdentity,
	} {
		if identity == nil || identity.Grain != RowGrainPatient || !reflect.DeepEqual(identity.Fields, []string{"project", "_key"}) {
			t.Fatalf("row identity provenance = %#v", identity)
		}
	}
	if explanation.Lowered.PlanProfile != "generic_fhir_graph" || explanation.Compiled.PlanProfile != "generic_fhir_graph" {
		t.Fatalf("plan profile provenance = %#v / %#v", explanation.Lowered, explanation.Compiled)
	}
	if !reflect.DeepEqual(explanation.Lowered.OptimizationRules, []string{OptimizerRuleFilterPushdown}) ||
		!reflect.DeepEqual(explanation.Compiled.OptimizationRules, []string{OptimizerRuleFilterPushdown}) {
		t.Fatalf("optimizer provenance = %#v / %#v", explanation.Lowered.OptimizationRules, explanation.Compiled.OptimizationRules)
	}
	if explanation.GenericPhysicalPlan.Available || explanation.GenericPhysicalPlan.Reason != GenericPhysicalPlanReasonFilters {
		t.Fatalf("filter request physical availability = %#v", explanation.GenericPhysicalPlan)
	}
}

func TestExplainCompilerRequestReportsRequiredRelationshipPhysicalUnavailability(t *testing.T) {
	explanation, err := ExplainCompilerRequest(Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		Traversals: []TraversalStep{{
			Label: "subject_Patient", ToResourceType: "Condition", Alias: "diagnosis", MatchMode: TraversalMatchRequired,
		}},
	}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if explanation.GenericPhysicalPlan.Available || explanation.GenericPhysicalPlan.Reason != GenericPhysicalPlanReasonRelationshipMatch || explanation.GenericPhysicalPlan.Plan != nil {
		t.Fatalf("required relationship physical availability = %#v", explanation.GenericPhysicalPlan)
	}
	if !reflect.DeepEqual(explanation.Compiled.OptimizationRules, []string{OptimizerRuleRelationshipSemiJoin}) {
		t.Fatalf("required relationship optimizer provenance = %#v", explanation.Compiled.OptimizationRules)
	}
}

func TestExplainCompilerRequestReturnsDeterministicDefensiveCopies(t *testing.T) {
	female := "female"
	builder := Builder{
		Project:           "P1",
		AuthResourcePaths: []string{"/programs/p1"},
		RootResourceType:  "Patient",
		Filters: []TypedFilter{{
			FieldRef: "Patient.gender", Selector: "gender", FieldKind: FilterString, Operator: FilterEquals,
			Values: []FilterValue{{Kind: FilterString, String: &female}},
		}},
		Fields: []FieldSelect{
			{Name: "z_gender", FieldRef: "Patient.gender", Select: "gender"},
			{Name: "a_birth_date", FieldRef: "Patient.birth_date", Select: "birthDate"},
		},
	}
	first, err := ExplainCompilerRequest(builder, 3)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ExplainCompilerRequest(builder, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same request produced non-deterministic explanations:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if got := []string{first.Selections[0].Alias, first.Selections[1].Alias}; !reflect.DeepEqual(got, []string{"root.a_birth_date", "root.z_gender"}) {
		t.Fatalf("selection order = %#v", got)
	}

	*first.SemanticPlan.Root.Filters[0].Values[0].String = "mutated"
	first.SemanticPlan.AuthResourcePaths[0] = "mutated"
	first.Selections[0].Selector.Steps[0].Field = "mutated"
	first.Lowered.RowIdentity.Fields[0] = "mutated"
	first.Compiled.Columns[0] = "mutated"
	if *builder.Filters[0].Values[0].String != "female" || builder.AuthResourcePaths[0] != "/programs/p1" {
		t.Fatalf("explanation mutation escaped into input builder: %#v", builder)
	}
	if second.SemanticPlan.Root.Fields[1].Selector.Steps[0].Field == "mutated" ||
		second.Compiled.Columns[0] != "_key" ||
		second.Lowered.RowIdentity.Fields[0] != "project" {
		t.Fatalf("explanation mutation escaped into another result: %#v", second)
	}

	rootOnly, err := ExplainCompilerRequest(Builder{Project: "P1", RootResourceType: "Patient", AuthResourcePaths: []string{"/programs/p1"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	rootOnly.GenericPhysicalPlan.Plan.BindVars["project"] = "mutated"
	rootOnly.GenericPhysicalPlan.Plan.Operations[0].RootScan.Variable = "mutated"
	freshRootOnly, err := ExplainCompilerRequest(Builder{Project: "P1", RootResourceType: "Patient", AuthResourcePaths: []string{"/programs/p1"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if freshRootOnly.GenericPhysicalPlan.Plan.BindVars["project"] != "P1" || freshRootOnly.GenericPhysicalPlan.Plan.Operations[0].RootScan.Variable != "root" {
		t.Fatalf("physical plan mutation escaped into a fresh result: %#v", freshRootOnly.GenericPhysicalPlan.Plan)
	}
}

func TestExplainCompilerRequestReturnsNoPartialResultOnInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		builder Builder
	}{
		{
			name: "semantic validation",
			builder: Builder{
				Project:          "P1",
				RootResourceType: "Patient",
				Fields:           []FieldSelect{{Name: "bad", Select: "identifier[nope]"}},
			},
		},
		{
			// Semantic aliases are valid FHIR graph identifiers, but these two
			// distinct aliases collide in the current lowered AQL identifier
			// scheme. This exercises the all-or-nothing lower/compile boundary.
			name: "lowering validation",
			builder: Builder{
				Project:          "P1",
				RootResourceType: "Specimen",
				Traversals: []TraversalStep{
					{Label: "subject_Specimen", ToResourceType: "DocumentReference", Alias: "file-a"},
					{Label: "subject_Specimen", ToResourceType: "DocumentReference", Alias: "file_a"},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			explanation, err := ExplainCompilerRequest(test.builder, 1)
			if err == nil {
				t.Fatal("invalid request unexpectedly explained")
			}
			if !reflect.DeepEqual(explanation, CompilerExplanation{}) {
				t.Fatalf("invalid request returned partial explanation: %#v", explanation)
			}
		})
	}
}
