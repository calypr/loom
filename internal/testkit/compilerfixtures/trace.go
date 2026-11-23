package compilerfixtures

import (
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func ConstructionRelatedSourceCellTraceRecipeOutput() recipe.Output {
	columns := []recipe.StageColumn{{ID: "patient_id", Name: "patient_id"}}
	outputs := append(append([]recipe.StageColumn(nil), columns...), recipe.StageColumn{ID: "observation_status", Name: "observation_status"})
	return recipe.Output{
		Name: "related_source_cell_trace", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{Version: 1, SourceColumns: columns, Steps: []recipe.ConstructionStep{
			{
				ID: "keep_patients", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{ColumnID: "patient_id", Operator: recipe.FilterExists}},
				Outputs:   columns,
			},
			{
				ID: "add_observation_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "keep_patients"}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: &recipe.ConstructionRelatedSource{
					AnchorColumnID: "_key", ChoiceID: "trace_route_choice", SourceOccurrenceID: "observation-node",
					Source:            recipe.ConstructionRelatedFieldSource{CandidateID: "observation-status", NodeID: "observation-node", ResourceType: "Observation", Path: "Observation.status", Cardinality: "optional_one", LogicalType: "string"},
					Route:             []recipe.ConstructionRelatedRouteStep{{EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node", FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL"}},
					ContributorPolicy: "ALL_MATCHES", Form: "ALL", OutputColumnID: "observation_status",
				}},
				Outputs: outputs,
			},
		}},
	}
}
