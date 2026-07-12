package arango

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExplainRequestJSON(t *testing.T) {
	request := ExplainRequest{
		Query:    "FOR d IN @@collection RETURN d",
		BindVars: map[string]any{"@collection": "Patient"},
		Options:  ExplainOptions{AllPlans: true, MaxNumberOfPlans: 4, Optimizer: OptimizerOptions{Rules: []string{"+use-indexes"}}},
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`"query"`, `"bindVars"`, `"allPlans":true`, `"maxNumberOfPlans":4`, `"rules":["+use-indexes"]`} {
		if !strings.Contains(string(data), fragment) {
			t.Errorf("request JSON %s does not contain %s", data, fragment)
		}
	}
}

func TestParseExplainResultAndExtractIndexes(t *testing.T) {
	payload := []byte(`{
  "plan": {
    "nodes": [
      {"type":"IndexNode","id":7,"collection":"Patient","estimatedCost":2.5,"estimatedNrItems":1,"indexes":[
        {"id":"Patient/123","name":"idx_project","type":"persistent","fields":["project"],"unique":false,"sparse":false,"selectivityEstimate":0.2},
        {"id":"Patient/123","name":"idx_project","type":"persistent","fields":["project"]}
      ]}
    ],
    "rules":["use-indexes"],
    "collections":[{"name":"Patient","type":"read"}],
    "estimatedCost":3.5,
    "estimatedNrItems":1
  },
  "warnings":[{"code":1577,"message":"example"}],
  "stats":{"plansCreated":1,"rulesExecuted":8,"rulesSkipped":2,"peakMemoryUsage":4096},
  "cacheable":true
}`)
	result, err := ParseExplainResult(payload)
	if err != nil {
		t.Fatal(err)
	}
	if result.Plan == nil || result.Plan.EstimatedCost != 3.5 || len(result.Warnings) != 1 || result.Stats.PeakMemoryUsage != 4096 {
		t.Fatalf("unexpected parsed explain result: %#v", result)
	}
	uses := ExtractPlanIndexes(result)
	if len(uses) != 1 {
		t.Fatalf("index uses = %#v", uses)
	}
	if uses[0].Plan != 0 || uses[0].NodeID != 7 || uses[0].Collection != "Patient" || uses[0].Index.Name != "idx_project" {
		t.Fatalf("unexpected index use: %#v", uses[0])
	}
	if uses[0].Index.SelectivityEstimate == nil || *uses[0].Index.SelectivityEstimate != 0.2 {
		t.Fatalf("missing selectivity estimate: %#v", uses[0].Index)
	}
}

func TestExtractIndexesFromAlternativePlans(t *testing.T) {
	result := ExplainResult{Plans: []ExplainPlan{
		{Nodes: []ExplainNode{{ID: 9, Type: "IndexNode", Collection: "Observation", Indexes: ExplainIndexes{{Name: "z"}}}}},
		{Nodes: []ExplainNode{{ID: 2, Type: "IndexNode", Indexes: ExplainIndexes{{Name: "a", Collection: "Specimen"}}}}},
	}}
	uses := ExtractPlanIndexes(result)
	if len(uses) != 2 || uses[0].Plan != 0 || uses[1].Plan != 1 || uses[1].Collection != "Specimen" {
		t.Fatalf("unexpected alternative-plan indexes: %#v", uses)
	}
}

func TestParseExplainResultRejectsErrorsAndInvalidBodies(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"Arango error", `{"error":true,"errorNum":1501,"errorMessage":"parse error","code":400}`, "1501"},
		{"missing plan", `{"warnings":[]}`, "no plan"},
		{"trailing JSON", `{"plan":{"nodes":[]}} {}`, "trailing JSON"},
		{"invalid JSON", `{`, "decode Arango"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseExplainResult([]byte(test.body))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v; want substring %q", err, test.want)
			}
		})
	}
}

func TestParseExplainResultAcceptsObjectIndexes(t *testing.T) {
	result, err := ParseExplainResult([]byte(`{
  "plan": {"nodes": [
    {"type":"TraversalNode","id":9,"collection":"fhir_edge","indexes":{"edge":{"id":"edge/1","name":"edge_index","type":"edge","fields":["_from"]}}}
  ]}
}`))
	if err != nil {
		t.Fatal(err)
	}
	uses := ExtractPlanIndexes(result)
	if len(uses) != 1 || uses[0].Index.Name != "edge_index" {
		t.Fatalf("unexpected object indexes: %#v", uses)
	}
}

func TestParseExplainResultAcceptsNestedTraversalIndexes(t *testing.T) {
	result, err := ParseExplainResult([]byte(`{
  "plan": {"nodes": [
    {"type":"TraversalNode","id":9,"edgeCollections":["fhir_edge"],"indexes":{
      "base":[{"id":"fhir_edge/2","name":"edge","type":"edge","fields":["_to"]}],
      "levels":{"1":[{"id":"fhir_edge/12","name":"project_label","type":"persistent","fields":["project","label"]}]}
    }}
  ]}
}`))
	if err != nil {
		t.Fatal(err)
	}
	uses := ExtractPlanIndexes(result)
	if len(uses) != 2 {
		t.Fatalf("nested traversal index uses = %#v", uses)
	}
	if uses[0].Index.Name != "edge" || uses[1].Index.Name != "project_label" || uses[0].Collection != "fhir_edge" || uses[1].Collection != "fhir_edge" {
		t.Fatalf("nested traversal indexes = %#v", uses)
	}
}
