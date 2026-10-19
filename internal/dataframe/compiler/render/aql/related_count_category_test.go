package aql_test

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestRelatedEligibilityCategoryScanUsesScopedReverseFrontier(t *testing.T) {
	output := recipe.Output{
		Name: "patient_categories", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{
			{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "active", ColumnID: "active", Expr: recipe.Expression{Select: "root.active"}},
		},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id"}, {ID: "active", Name: "active"}},
			Steps: []recipe.ConstructionStep{{
				ID: "eligible_patients", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedEligibilityOp, RelatedEligibility: &recipe.ConstructionRelatedEligibility{
					AnchorColumnID: "_key", ChoiceID: "patient-observation-choice", TargetNodeID: "observation-node", TargetResourceType: "Observation",
					Route: []recipe.ConstructionRelatedRouteStep{{
						EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
						FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
						StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
					}}, ContributorPolicy: "ALL_MATCHES", MatchKind: recipe.RelatedEligibilityExists,
				}},
				Outputs: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id"}, {ID: "active", Name: "active"}},
			}},
		},
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
	bindings := recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation", AuthScopeMode: authscope.ReadScopeUnrestricted}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "project", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	categoryOutput := compiled.Outputs[0]
	categoryOutput.OptimizedPlan = nil
	categoryOutput.Plan = ir.ClonePhysicalPlan(categoryOutput.Plan)
	returnIndex := len(categoryOutput.Plan.Operations) - 1
	categoryOutput.Plan.Operations = append(categoryOutput.Plan.Operations, ir.PhysicalOperation{})
	copy(categoryOutput.Plan.Operations[returnIndex+1:], categoryOutput.Plan.Operations[returnIndex:])
	categoryOutput.Plan.Operations[returnIndex] = ir.PhysicalOperation{
		Kind: ir.PhysicalDerivedLetOp,
		DerivedLet: &ir.PhysicalDerivedLet{
			Variable: "unused_source_calculation", Operator: "LENGTH",
			Inputs: []ir.PhysicalValue{{Variable: "root", Path: []string{"payload", "name"}}},
		},
	}
	query, err := compiler.CompileCategoryScanStageWithPolicy(categoryOutput, "eligible_patients", "active", "patient_id", compiler.MaxCategoryScanValues, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"FOR __loom_physical_related_category_terminal IN @@__loom_related_category_terminal_collection",
		"._from == __loom_physical_related_category_route_target_1._id",
		".from_type == @related_eligibility_0_hop_1_target_type",
		".to_type == @related_eligibility_0_hop_1_source_type",
		"auth_resource_path IN @auth_resource_paths",
		"HAS(",
		"COLLECT __loom_physical_related_category_group_present = __loom_physical_related_category_category_present, __loom_physical_related_category_group_value = __loom_physical_related_category_category_value",
		"LIMIT 257",
	} {
		if !strings.Contains(query.Query, want) {
			t.Errorf("reverse category query is missing %q:\n%s", want, query.Query)
		}
	}
	if strings.Contains(query.Query, "LET __loom_construction_source_projection = (") {
		t.Fatalf("category scan materialized the source root projection before reversing eligibility:\n%s", query.Query)
	}
	if strings.Contains(query.Query, "unused_source_calculation") {
		t.Fatalf("unselected derived LET leaked into the specialized category plan:\n%s", query.Query)
	}
	if got := query.BindVars["related_eligibility_0_hop_1_source_type"]; got != "Patient" {
		t.Fatalf("typed source discriminator bind = %#v, want Patient", got)
	}
	if got := query.BindVars["@__loom_related_category_terminal_collection"]; got != "Observation" {
		t.Fatalf("terminal collection bind = %#v, want Observation", got)
	}
	if query.OverflowWitness == nil || query.Proof.OverflowWitnessFingerprint == "" {
		t.Fatal("eligible category scan has no bound overflow witness")
	}
	for _, want := range []string{
		"FOR root IN @@root_collection",
		"FILTER LENGTH((",
		"LIMIT 1025",
		"COLLECT present = __loom_witness_present, value = __loom_witness_value",
	} {
		if !strings.Contains(query.OverflowWitness.Query, want) {
			t.Errorf("category overflow witness is missing %q:\n%s", want, query.OverflowWitness.Query)
		}
	}
	if strings.Contains(query.OverflowWitness.Query, "unused_source_calculation") {
		t.Fatal("unselected source calculation leaked into the overflow witness")
	}
}
