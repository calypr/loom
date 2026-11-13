package arango

import (
	"reflect"
	"testing"
)

func TestAssessExplainResultRealisticPlan(t *testing.T) {
	result, err := ParseExplainResult([]byte(`{
  "plan": {
    "nodes": [
      {"type":"EnumerateCollectionNode","id":8,"collection":"Observation","estimatedCost":900,"estimatedNrItems":800},
      {"type":"IndexNode","id":5,"collection":"Patient","indexes":[{"id":"Patient/42","name":"idx_project_auth","type":"persistent","fields":["project","auth_resource_path"]}]},
      {"type":"IndexNode","id":3,"collection":"fhir_edge","indexes":[{"id":"fhir_edge/9","name":"idx_edge_project","type":"persistent","collection":"fhir_edge","fields":["project","label"]}]}
    ],
    "rules":["use-indexes","move-filters-up","use-indexes"],
    "estimatedCost":123.5,
    "estimatedNrItems":27
  },
  "warnings":[{"code":1578,"message":"late"},{"code":1577,"message":"early"}]
}`))
	if err != nil {
		t.Fatal(err)
	}
	assessment := AssessExplainResult(result)
	if !reflect.DeepEqual(assessment.Plans, []ExplainPlanEstimate{{Plan: 0, EstimatedCost: 123.5, EstimatedNrItems: 27}}) {
		t.Fatalf("estimates = %#v", assessment.Plans)
	}
	if !reflect.DeepEqual(assessment.FullCollectionScans, []ExplainCollectionScan{{Plan: 0, NodeID: 8, Collection: "Observation"}}) {
		t.Fatalf("full scans = %#v", assessment.FullCollectionScans)
	}
	if got := []string{assessment.Indexes[0].Collection + "/" + assessment.Indexes[0].Name, assessment.Indexes[1].Collection + "/" + assessment.Indexes[1].Name}; !reflect.DeepEqual(got, []string{"Patient/idx_project_auth", "fhir_edge/idx_edge_project"}) {
		t.Fatalf("index ordering = %#v", got)
	}
	if !reflect.DeepEqual(assessment.AppliedOptimizerRules, []string{"move-filters-up", "use-indexes"}) {
		t.Fatalf("rules = %#v", assessment.AppliedOptimizerRules)
	}
	if assessment.Warnings[0].Code != 1577 {
		t.Fatalf("warnings are unstable: %#v", assessment.Warnings)
	}
}

func TestAssessExplainResultAlternativePlansStableAndGrouped(t *testing.T) {
	result := ExplainResult{Plans: []ExplainPlan{
		{EstimatedCost: 20, EstimatedNrItems: 5, Rules: []string{"z-rule"}, Nodes: []ExplainNode{
			{Type: "IndexNode", ID: 9, Collection: "Patient", Indexes: []ExplainIndex{{ID: "Patient/1", Name: "primary", Type: "primary", Fields: []string{"_key"}}}},
			{Type: "EnumerateCollectionNode", ID: 10, Collection: "Specimen"},
		}},
		{EstimatedCost: 10, EstimatedNrItems: 2, Rules: []string{"a-rule"}, Nodes: []ExplainNode{
			{Type: "IndexNode", ID: 2, Collection: "Patient", Indexes: []ExplainIndex{{ID: "Patient/1", Name: "primary", Type: "primary", Fields: []string{"_key"}}}},
			{Type: "EnumerateCollectionNode", ID: 1, Collection: "Observation"},
		}},
	}}
	assessment := AssessExplainResult(result)
	if len(assessment.Indexes) != 1 || !reflect.DeepEqual(assessment.Indexes[0].Uses, []ExplainIndexLocation{{Plan: 0, NodeID: 9}, {Plan: 1, NodeID: 2}}) {
		t.Fatalf("grouped index uses = %#v", assessment.Indexes)
	}
	if got := assessment.FullCollectionScans; len(got) != 2 || got[0].Collection != "Observation" || got[1].Collection != "Specimen" {
		t.Fatalf("scan ordering = %#v", got)
	}
	if !reflect.DeepEqual(assessment.AppliedOptimizerRules, []string{"a-rule", "z-rule"}) {
		t.Fatalf("rule ordering = %#v", assessment.AppliedOptimizerRules)
	}
}

func TestAssessExplainResultReturnsNonNilEmptyFindings(t *testing.T) {
	assessment := AssessExplainResult(ExplainResult{})
	if assessment.Plans == nil || assessment.FullCollectionScans == nil || assessment.Indexes == nil || assessment.Warnings == nil || assessment.AppliedOptimizerRules == nil {
		t.Fatalf("empty assessment contains nil slices: %#v", assessment)
	}
}
