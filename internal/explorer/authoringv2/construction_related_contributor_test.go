package authoringv2

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/capability"
)

func TestRelatedExpandContributorQuantifierMatchesSourceCardinality(t *testing.T) {
	repeatedSource := ConstructionRelatedFieldSource{
		Kind: capability.ConstructionChoiceSourceField, CandidateID: "observation-code",
		NodeID: "observation", ResourceType: "Observation", Path: "category[].coding[].code",
		Cardinality: "many", LogicalType: "code",
		RepeatedBoundaries: []capability.RepeatedBoundary{
			{Path: "category[]", MaxItems: 2},
			{Path: "category[].coding[]", MaxItems: 4},
		},
	}
	code := &ContributorValue{Kind: ContributorValueCode, Code: &ContributorCode{Code: "d"}}
	makeStep := func(source ConstructionRelatedFieldSource, predicate *ContributorPredicate) ConstructionStep {
		input := []StageColumn{{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"}}
		outputs := append(append([]StageColumn(nil), input...), StageColumn{
			ID: "observation-id", Name: "observation_id", Label: "Observation ID", Type: "string", Nullable: true,
		})
		return ConstructionStep{
			ID: "expand_observations", Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
			Operation: ConstructionOperation{Kind: ConstructionOperationRelatedExpand, RelatedExpand: &ConstructionRelatedExpand{
				AnchorColumnID: "_key", ChoiceID: "route-choice", TargetNodeID: "observation", TargetResourceType: "Observation",
				Route: []capability.ConstructionRouteStep{{
					EdgeID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation",
					FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
					StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
				}},
				ContributorRule:   ConstructionRelatedContributorRule{Policy: ConstructionRelatedAllMatches, Predicate: predicate},
				ContributorSource: &source, ContributorChoiceID: "field-choice",
				EmptyPolicy: ConstructionExpandEmptyPreserveParent, RelatedRecordColumnID: "observation-id",
			}},
			Outputs: outputs,
		}
	}
	inputColumns := []StageColumn{{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"}}
	input := map[string]StageColumn{"patient-id": inputColumns[0]}

	for _, test := range []struct {
		name      string
		predicate *ContributorPredicate
		wantError string
	}{
		{
			name: "repeated EQUALS requires ANY and code literal",
			predicate: &ContributorPredicate{
				CandidateID: "observation-code", Operator: ContributorEquals, Quantifier: ContributorAny, Value: code,
			},
		},
		{
			name:      "repeated EXISTS requires ANY",
			predicate: &ContributorPredicate{CandidateID: "observation-code", Operator: ContributorExists, Quantifier: ContributorAny},
		},
		{
			name:      "repeated predicate cannot omit quantifier",
			predicate: &ContributorPredicate{CandidateID: "observation-code", Operator: ContributorExists},
			wantError: "requires the ANY quantifier",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			step := makeStep(repeatedSource, test.predicate)
			err := validateConstructionRelatedExpand(step, input, inputColumns)
			if test.wantError == "" && err != nil {
				t.Fatalf("valid repeated contributor rejected: %v", err)
			}
			if test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("error = %v, want substring %q", err, test.wantError)
			}
		})
	}
	eligibilityStep := ConstructionStep{
		ID: "keep_patients", Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
		Operation: ConstructionOperation{Kind: ConstructionOperationRelatedEligibility, RelatedEligibility: &ConstructionRelatedEligibility{
			AnchorColumnID: "patient-id", ChoiceID: "route-choice", TargetNodeID: "observation", TargetResourceType: "Observation",
			Route: []capability.ConstructionRouteStep{{
				EdgeID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation",
				FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
				StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
			}},
			ContributorRule: ConstructionRelatedContributorRule{Policy: ConstructionRelatedAllMatches, Predicate: &ContributorPredicate{
				CandidateID: "observation-code", Operator: ContributorEquals, Quantifier: ContributorAny, Value: code,
			}},
			ContributorSource: &repeatedSource, ContributorChoiceID: "field-choice",
			Match: ConstructionRelatedEligibilityMatch{Kind: ConstructionRelatedEligibilityExists},
		}},
		Outputs: inputColumns,
	}
	if err := validateConstructionRelatedEligibility(eligibilityStep, input, inputColumns); err == nil || !strings.Contains(err.Error(), "one exact scalar field") {
		t.Fatalf("related eligibility accepted repeated contributor source: %v", err)
	}
	scalarEligibility := *eligibilityStep.Operation.RelatedEligibility
	eligibilitySource := repeatedSource
	eligibilitySource.Path, eligibilitySource.Cardinality, eligibilitySource.LogicalType = "status", "optional_one", "code"
	eligibilitySource.RepeatedBoundaries = nil
	scalarEligibility.ContributorSource = &eligibilitySource
	scalarEligibility.ContributorRule.Predicate = &ContributorPredicate{CandidateID: "observation-code", Operator: ContributorExists}
	eligibilityStep.Operation.RelatedEligibility = &scalarEligibility
	if err := validateConstructionRelatedEligibility(eligibilityStep, input, inputColumns); err != nil {
		t.Fatalf("existing scalar eligibility contributor rejected: %v", err)
	}

	scalarSource := repeatedSource
	scalarSource.Path, scalarSource.Cardinality, scalarSource.LogicalType = "status", "optional_one", "code"
	scalarSource.RepeatedBoundaries = nil
	if err := validateConstructionRelatedExpand(makeStep(scalarSource, &ContributorPredicate{
		CandidateID: "observation-code", Operator: ContributorExists,
	}), input, inputColumns); err != nil {
		t.Fatalf("scalar contributor without quantifier rejected: %v", err)
	}
	if err := validateConstructionRelatedExpand(makeStep(scalarSource, &ContributorPredicate{
		CandidateID: "observation-code", Operator: ContributorExists, Quantifier: ContributorAny,
	}), input, inputColumns); err == nil || !strings.Contains(err.Error(), "scalar contributor predicate must not specify a quantifier") {
		t.Fatalf("scalar contributor ANY error = %v", err)
	}

	badBoundaries := repeatedSource
	badBoundaries.RepeatedBoundaries = append([]capability.RepeatedBoundary(nil), repeatedSource.RepeatedBoundaries...)
	badBoundaries.RepeatedBoundaries[1].Path = "category[].code[]"
	if err := validateConstructionRelatedExpand(makeStep(badBoundaries, &ContributorPredicate{
		CandidateID: "observation-code", Operator: ContributorExists, Quantifier: ContributorAny,
	}), input, inputColumns); err == nil || !strings.Contains(err.Error(), "boundaries must match") {
		t.Fatalf("mismatched repeated boundary error = %v", err)
	}
}
