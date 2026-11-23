package compilation

import (
	"reflect"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestRecipeConstructionMapsRepeatedRelatedContributorContract(t *testing.T) {
	code := "d"
	source := ws.ConstructionRelatedFieldSource{
		Kind: capability.ConstructionChoiceSourceField, CandidateID: "observation-code",
		NodeID: "observation", ResourceType: "Observation", Path: "category[].coding[].code",
		Cardinality: "many", LogicalType: "code",
		RepeatedBoundaries: []capability.RepeatedBoundary{
			{Path: "category[]", MaxItems: 2}, {Path: "category[].coding[]", MaxItems: 4},
		},
	}
	authored := ws.ConstructionOperation{Kind: ws.ConstructionOperationRelatedExpand, RelatedExpand: &ws.ConstructionRelatedExpand{
		AnchorColumnID: "_key", ChoiceID: "route-choice", TargetNodeID: "observation", TargetResourceType: "Observation",
		Route: []capability.ConstructionRouteStep{{
			EdgeID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation",
			FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
			StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
		}},
		ContributorRule: ws.ConstructionRelatedContributorRule{Policy: ws.ConstructionRelatedAllMatches, Predicate: &ws.ContributorPredicate{
			CandidateID: source.CandidateID, Operator: ws.ContributorEquals, Quantifier: ws.ContributorAny,
			Value: &ws.ContributorValue{Kind: ws.ContributorValueCode, Code: &ws.ContributorCode{Code: code}},
		}},
		ContributorSource: &source, ContributorChoiceID: "field-choice",
		EmptyPolicy: ws.ConstructionExpandEmptyExclude, RelatedRecordColumnID: "observation-id",
	}}
	mapped, err := recipeConstructionOperation(authored)
	if err != nil {
		t.Fatal(err)
	}
	if mapped.RelatedExpand == nil || mapped.RelatedExpand.ContributorPredicate == nil || mapped.RelatedExpand.ContributorSource == nil {
		t.Fatalf("mapped related expansion omitted contributor contract: %#v", mapped.RelatedExpand)
	}
	gotSource := mapped.RelatedExpand.ContributorSource
	wantBoundaries := []recipe.ConstructionRelatedRepeatedBoundary{
		{Path: "category[]", MaxItems: 2}, {Path: "category[].coding[]", MaxItems: 4},
	}
	if !reflect.DeepEqual(gotSource.RepeatedBoundaries, wantBoundaries) || gotSource.Cardinality != "many" || gotSource.Path != source.Path {
		t.Fatalf("mapped repeated source = %#v, want cardinality/path/boundaries %q %q %#v", gotSource, "many", source.Path, wantBoundaries)
	}
	predicate := mapped.RelatedExpand.ContributorPredicate
	if predicate.Quantifier != recipe.QuantifierAny || predicate.CandidateID != source.CandidateID ||
		predicate.Operator != recipe.FilterEquals || predicate.Value == nil || predicate.Value.Kind != recipe.FilterCode ||
		predicate.Value.Code == nil || predicate.Value.Code.Code != code {
		t.Fatalf("mapped repeated CODE ANY predicate = %#v", predicate)
	}
}

func TestRecipeConstructionMapsRelatedSourceRowValuePolicy(t *testing.T) {
	authored := ws.ConstructionOperation{
		Kind: ws.ConstructionOperationRelatedSource,
		RelatedSource: &ws.ConstructionRelatedSource{
			AnchorColumnID: "__loom_row_id", ChoiceID: "observation-status-choice", SourceOccurrenceID: "observation-node",
			Source: ws.ConstructionRelatedFieldSource{
				Kind: capability.ConstructionChoiceSourceField, CandidateID: "observation-status", NodeID: "observation-node",
				ResourceType: "Observation", Path: "status", Cardinality: "optional_one", LogicalType: "string",
			},
			Route: []capability.ConstructionRouteStep{{
				EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
				FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
				StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
			}},
			ContributorRule: ws.ConstructionRelatedContributorRule{Policy: ws.ConstructionRelatedAllMatches},
			Form:            capability.ConstructionChoiceAll, RowValuePolicy: ws.ConstructionRowValueOne,
			OutputColumnID: "related-status",
		},
	}
	mapped, err := recipeConstructionOperation(authored)
	if err != nil {
		t.Fatal(err)
	}
	if mapped.RelatedSource == nil || mapped.RelatedSource.Form != "ALL" ||
		mapped.RelatedSource.RowValuePolicy != recipe.ConstructionRowValueOne ||
		mapped.RelatedSource.ContributorPolicy != "ALL_MATCHES" {
		t.Fatalf("mapped related-source policy = %#v", mapped.RelatedSource)
	}
}
