package compilation

import (
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestRecipeConstructionMapsRepeatedRelatedContributorContract(t *testing.T) {
	code := "d"
	source := authoringv2.ConstructionRelatedFieldSource{
		Kind: capability.ConstructionChoiceSourceField, CandidateID: "observation-code",
		NodeID: "observation", ResourceType: "Observation", Path: "category[].coding[].code",
		Cardinality: "many", LogicalType: "code",
		RepeatedBoundaries: []capability.RepeatedBoundary{
			{Path: "category[]", MaxItems: 2}, {Path: "category[].coding[]", MaxItems: 4},
		},
	}
	authored := authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationRelatedExpand, RelatedExpand: &authoringv2.ConstructionRelatedExpand{
		AnchorColumnID: "_key", ChoiceID: "route-choice", TargetNodeID: "observation", TargetResourceType: "Observation",
		Route: []capability.ConstructionRouteStep{{
			EdgeID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation",
			FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
			StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
		}},
		ContributorRule: authoringv2.ConstructionRelatedContributorRule{Policy: authoringv2.ConstructionRelatedAllMatches, Predicate: &authoringv2.ContributorPredicate{
			CandidateID: source.CandidateID, Operator: authoringv2.ContributorEquals, Quantifier: authoringv2.ContributorAny,
			Value: &authoringv2.ContributorValue{Kind: authoringv2.ContributorValueCode, Code: &authoringv2.ContributorCode{Code: code}},
		}},
		ContributorSource: &source, ContributorChoiceID: "field-choice",
		EmptyPolicy: authoringv2.ConstructionExpandEmptyExclude, RelatedRecordColumnID: "observation-id",
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
