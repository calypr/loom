package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestRelatedEligibilityCountRowsUsesScopedReverseRoute(t *testing.T) {
	output := recipe.Output{
		Name: "related_count", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id"}},
			Steps: []recipe.ConstructionStep{
				{
					ID: "eligible_patients", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedEligibilityOp, RelatedEligibility: &recipe.ConstructionRelatedEligibility{
						AnchorColumnID: "_key", ChoiceID: "patient-observation-choice", TargetNodeID: "observation-node", TargetResourceType: "Observation",
						Route: []recipe.ConstructionRelatedRouteStep{{
							EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
							FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
							StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
						}}, ContributorPolicy: "ALL_MATCHES", MatchKind: recipe.RelatedEligibilityExists,
					}},
					Outputs: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id"}},
				},
				{
					ID: "count_eligible", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "eligible_patients"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
						ConstructionID: "count_eligible", Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
					}},
					Outputs: []recipe.StageColumn{{ID: "rows_id", Name: "rows"}},
				},
			},
		},
	}
	compiled := compileDerivedTestOutput(t, output)
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"FOR __loom_physical_related_count_terminal IN @@__loom_related_count_terminal_collection",
		"._from == __loom_physical_related_count_route_target_1._id",
		".from_type == @related_eligibility_0_hop_1_target_type",
		".to_type == @related_eligibility_0_hop_1_source_type",
		"COLLECT _id = __loom_construction_related_eligibility_0_anchor_1._id",
		"COLLECT WITH COUNT INTO",
		"@auth_resource_paths_unrestricted == true",
		"auth_resource_path IN @auth_resource_paths",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Errorf("reverse query is missing %q:\n%s", want, rendered.Query)
		}
	}
	if strings.Contains(rendered.Query, "LET __loom_construction_source_projection = (") {
		t.Fatalf("eligible root scan was materialized before reverse count:\n%s", rendered.Query)
	}
	if rendered.BindVars["related_eligibility_0_hop_1_source_type"] != "Patient" {
		t.Fatalf("typed source discriminator bind = %#v", rendered.BindVars["related_eligibility_0_hop_1_source_type"])
	}

	restricted := ir.ClonePhysicalPlan(compiled.Plan)
	restricted.BindVars["auth_resource_paths_unrestricted"] = false
	restricted.BindVars["auth_resource_paths"] = []string{"/project/Patient/42"}
	restrictedRendered, err := aql.RenderPhysicalPlan(restricted)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(restrictedRendered.Query, "auth_resource_path IN @auth_resource_paths") ||
		restrictedRendered.BindVars["auth_resource_paths_unrestricted"] != false ||
		!strings.Contains(restrictedRendered.Query, "FOR __loom_physical_related_count_terminal IN @@") {
		t.Fatalf("restricted auth did not retain the reverse plan and exact scope binds:\n%s\n%#v", restrictedRendered.Query, restrictedRendered.BindVars)
	}

	denied := ir.ClonePhysicalPlan(compiled.Plan)
	denied.BindVars["scope_allowed"] = false
	deniedRendered, err := aql.RenderPhysicalPlan(denied)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(deniedRendered.Query, "__loom_related_count_terminal_collection") || !strings.Contains(deniedRendered.Query, "LET __loom_construction_source_projection = (") {
		t.Fatalf("unsupported scope_allowed=false shape should use the existing renderer:\n%s", deniedRendered.Query)
	}
}
