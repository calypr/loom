package compilerfixtures

import (
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func GroupedRelatedSummaryOutput(form, filter string) recipe.Output {
	groupColumns := []recipe.StageColumn{{ID: "gender", Name: "gender", Type: "string"}, {ID: "rows", Name: "rows", Type: "integer"}}
	outputColumns := append([]recipe.StageColumn(nil), groupColumns...)
	outputColumns = append(outputColumns, recipe.StageColumn{ID: "summary", Name: "summary"})
	related := &recipe.ConstructionRelatedSource{
		AnchorColumnID: "__loom_row_id", ChoiceID: "owned-test-choice", SourceOccurrenceID: "observation-node",
		Source: recipe.ConstructionRelatedFieldSource{CandidateID: "observation-status", NodeID: "observation-node",
			ResourceType: "Observation", Path: "Observation.status", Cardinality: "optional_one", LogicalType: "string"},
		Route: []recipe.ConstructionRelatedRouteStep{{EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
			FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL"}},
		ContributorPolicy: "ALL_MATCHES", Form: form, OutputColumnID: "summary",
	}
	if filter != "" {
		related.Predicate = &recipe.ConstructionRelatedPredicate{CandidateID: "observation-status", Operator: recipe.FilterEquals,
			Value: &spec.FilterValue{Kind: recipe.FilterString, String: &filter}}
	}
	return recipe.Output{Name: "group_related_summary", RootResourceType: "Patient", RowGrain: "patient", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{{Name: "gender", ColumnID: "gender", Expr: recipe.Expression{Select: "root.gender"}}},
		Construction: &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{{ID: "gender", Name: "gender", Type: "string"}},
			Steps: []recipe.ConstructionStep{
				{ID: "group", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{ConstructionID: "group",
						Keys:       []recipe.ConstructionGroupKey{{InputColumnID: "gender", OutputColumnID: "gender"}},
						Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows"}}}}, Outputs: groupColumns},
				{ID: "summary", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "group"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: related}, Outputs: outputColumns},
			}}}
}
