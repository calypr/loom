package arango

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
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

func TestClientExplainPostsTypedRequest(t *testing.T) {
	client := &Client{
		rawURL: "http://arango.test",
		dbName: "fhir_proto",
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s", r.Method)
			}
			if r.URL.Path != "/_db/fhir_proto/_api/explain" {
				t.Fatalf("path = %s", r.URL.Path)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), `"query":"FOR p IN Patient RETURN p"`) {
				t.Fatalf("unexpected request body: %s", body)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewBufferString(`{"plan":{"nodes":[{"id":1,"type":"SingletonNode"}]}}`)),
				Request:    r,
			}, nil
		})},
	}
	result, err := client.Explain(context.Background(), ExplainRequest{Query: "FOR p IN Patient RETURN p"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Plan == nil || len(result.Plan.Nodes) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestClientExplainRejectsHTTPError(t *testing.T) {
	client := &Client{
		rawURL: "http://arango.test",
		dbName: "fhir_proto",
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("bad query")),
				Request:    r,
			}, nil
		})},
	}
	_, err := client.Explain(context.Background(), ExplainRequest{Query: "FOR p IN Patient RETURN p"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("expected HTTP error, got %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

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
