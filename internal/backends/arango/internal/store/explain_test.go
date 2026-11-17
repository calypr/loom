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
