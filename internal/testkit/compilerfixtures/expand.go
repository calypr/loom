package compilerfixtures

import (
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func RelatedExpandOracleOutput(emptyPolicy recipe.ExpansionEmptyPolicy) recipe.Output {
	trueValue := true
	columns := []recipe.StageColumn{
		{ID: "patient-id", Name: "patient_id", Label: "Patient ID"},
		{ID: "patient-active", Name: "patient_active", Label: "Active"},
	}
	filteredColumns := append([]recipe.StageColumn(nil), columns...)
	finalColumns := append(append([]recipe.StageColumn(nil), columns...), recipe.StageColumn{
		ID: "observation-id", Name: "observation_id", Label: "Observation ID", Type: "string", Nullable: emptyPolicy == recipe.ExpansionPreserveParent,
	})
	return recipe.Output{
		Name: "related_expand_oracle", RootResourceType: "Patient", RowGrain: "patient", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "patient_id", ColumnID: "patient-id", Label: "Patient ID", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "patient_active", ColumnID: "patient-active", Label: "Active", Expr: recipe.Expression{Select: "root.active"}},
		},
		Construction: &recipe.Construction{Version: 1, SourceColumns: columns, Steps: []recipe.ConstructionStep{
			{
				ID: "filter_active", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
					ColumnID: "patient-active", Operator: recipe.FilterEquals,
					Values: []spec.FilterValue{{Kind: recipe.FilterBoolean, Boolean: &trueValue}},
				}}, Outputs: filteredColumns,
			},
			{
				ID: "expand_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "filter_active"}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
					AnchorColumnID: "_key", ChoiceID: "related-observation-choice", TargetNodeID: "observation-node", TargetResourceType: "Observation",
					Route: []recipe.ConstructionRelatedRouteStep{{
						EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
						FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
						StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
					}}, ContributorPolicy: "ALL_MATCHES", EmptyPolicy: emptyPolicy, RelatedRecordColumnID: "observation-id",
				}}, Outputs: finalColumns,
			},
		}},
	}
}
