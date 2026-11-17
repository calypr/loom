package arango

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProfileRequestJSONKeepsBindVarsAndProfileLevel(t *testing.T) {
	request := ProfileRequest{
		Query:     "FOR p IN @@collection FILTER p.project == @project RETURN p",
		BindVars:  map[string]any{"@collection": "Patient", "project": "demo"},
		BatchSize: 1000,
		Options:   ProfileOptions{Profile: 2},
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`"query"`, `"bindVars"`, `"batchSize":1000`, `"profile":2`} {
		if !strings.Contains(string(data), fragment) {
			t.Errorf("request JSON %s does not contain %s", data, fragment)
		}
	}
}

func TestParseProfileResultAndSummarizeNodes(t *testing.T) {
	result, err := ParseProfileResult([]byte(`{
  "result":[{"_key":"p1"}], "hasMore":false, "count":1,
  "extra": {
    "profile": {"initializing":0.001,"executing":1.25,"finalizing":0.002},
    "stats": {"scannedFull":3,"scannedIndex":11,"peakMemoryUsage":4096,
      "nodes":[{"id":2,"calls":4,"items":100,"filtered":7,"runtime":0.75},
               {"id":3,"calls":4,"items":50,"filtered":2,"runtime":0.25}]},
    "plan": {"nodes":[{"id":1,"type":"SingletonNode"},{"id":2,"type":"TraversalNode"},{"id":3,"type":"FilterNode"}]}
  }
}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Result) != 1 || result.Extra.Profile.Executing != 1.25 || len(result.Extra.Stats.Nodes) != 2 {
		t.Fatalf("unexpected profile fixture: %#v", result)
	}
	summary := SummarizeProfile(result)
	if summary.RuntimeSeconds != 1.0 || summary.ScannedFull != 3 || summary.ScannedIndex != 11 || len(summary.Nodes) != 2 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if summary.Nodes[0].Type != "TraversalNode" || summary.Nodes[0].Runtime != 0.75 {
		t.Fatalf("nodes not sorted or correlated with plan: %#v", summary.Nodes)
	}
	if len(summary.ByType) != 2 || summary.ByType[0].Type != "TraversalNode" {
		t.Fatalf("groups not stable: %#v", summary.ByType)
	}
}

func TestParseProfileResultRejectsErrorsAndTrailingJSON(t *testing.T) {
	for _, test := range []struct {
		body string
		want string
	}{
		{`{"error":true,"errorNum":1501,"errorMessage":"parse error","code":400}`, "1501"},
		{`{"result":[]} {}`, "trailing JSON"},
		{`{`, "decode Arango"},
	} {
		_, err := ParseProfileResult([]byte(test.body))
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("error = %v; want %q", err, test.want)
		}
	}
}
