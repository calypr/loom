package execution

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	parserSentinelTable  = "pat_SENTINEL-TBL-9c14"
	parserSentinelColumn = "note_SENTINEL-COL-7a51"
	parserSentinelBind   = "SENTINEL-BIND-13ad"
)

func parserClip(value string) string {
	if len(value) > 200 {
		return value[:200] + "…"
	}
	return value
}

func assertParserSentinelFree(t *testing.T, surface string, value string) {
	t.Helper()
	for _, sentinel := range []string{parserSentinelTable, parserSentinelColumn, parserSentinelBind} {
		if strings.Contains(value, sentinel) {
			t.Fatalf("%s leaks hostile sentinel %q", surface, sentinel)
		}
	}
}

func TestPostgresExplainSanitizerClassifiesSequentialScan(t *testing.T) {
	raw := json.RawMessage(`[{"Plan":{"Node Type":"Limit","Startup Cost":0,"Total Cost":1.5,"Plan Rows":1,"Plan Width":8,"Plans":[{"Node Type":"Seq Scan","Relation Name":"` + parserSentinelTable + `","Filter":"(` + parserSentinelColumn + ` = '` + parserSentinelBind + `')","Startup Cost":0,"Total Cost":1.4,"Plan Rows":1,"Plan Width":8}]}}]`)
	assessment, err := sanitizePostgreSQLPlanJSON(raw)
	if err != nil {
		t.Fatalf("sanitize: %v", err)
	}
	if len(assessment.FullScans) != 1 || assessment.FullScans[0] != (PostgreSQLScanEvidence{Ordinal: 1, NodeType: "Seq Scan", ScanKind: PostgreSQLScanSequential}) {
		t.Fatalf("sequential classification: %+v", assessment.FullScans)
	}
	if len(assessment.IndexScans) != 0 {
		t.Fatalf("unexpected index evidence: %+v", assessment.IndexScans)
	}
	benign, err := sanitizePostgreSQLPlanJSON(json.RawMessage(`[{"Plan":{"Node Type":"Result","Startup Cost":0,"Total Cost":0.01,"Plan Rows":1,"Plan Width":4}}]`))
	if err != nil || len(benign.FullScans) != 0 || len(benign.IndexScans) != 0 {
		t.Fatalf("zero-scan control: %+v err=%v", benign, err)
	}
	rendered, _ := json.Marshal(assessment)
	assertParserSentinelFree(t, "sequential assessment JSON", string(rendered))
}

func TestPostgresExplainSanitizerRejectsMalformedPlans(t *testing.T) {
	nestedChain := func(levels int) string {
		var builder strings.Builder
		for i := 0; i < levels; i++ {
			builder.WriteString(`{"Node Type":"Nested Loop","Startup Cost":0,"Total Cost":1,"Plan Rows":1,"Plan Width":4,"Plans":[`)
		}
		builder.WriteString(`{"Node Type":"Result","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}`)
		builder.WriteString(strings.Repeat(`]}`, levels))
		return builder.String()
	}
	deepNode := nestedChain(postgresExplainMaxDepth + 2)
	wideNodes := func(count int) string {
		var builder strings.Builder
		builder.WriteString(`{"Node Type":"Append","Startup Cost":0,"Total Cost":1,"Plan Rows":1,"Plan Width":4,"Plans":[`)
		for i := 0; i < count; i++ {
			if i > 0 {
				builder.WriteString(",")
			}
			builder.WriteString(`{"Node Type":"Result","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}`)
		}
		builder.WriteString(`]}`)
		return builder.String()
	}
	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ``},
		{name: "not json", raw: `EXPLAIN`},
		{name: "object not array", raw: `{"Plan":{"Node Type":"Result","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}`},
		{name: "empty array", raw: `[]`},
		{name: "two statements", raw: `[{"Plan":{"Node Type":"Result","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}},{"Plan":{"Node Type":"Result","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`},
		{name: "no plan object", raw: `[{"NotPlan":{}}]`},
		{name: "missing node type", raw: `[{"Plan":{"Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`},
		{name: "missing estimate", raw: `[{"Plan":{"Node Type":"Result","Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`},
		{name: "estimate as string", raw: `[{"Plan":{"Node Type":"Result","Startup Cost":"0","Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`},
		{name: "fractional plan width", raw: `[{"Plan":{"Node Type":"Result","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4.5}}]`},
		{name: "negative estimate", raw: `[{"Plan":{"Node Type":"Result","Startup Cost":-1,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`},
		{name: "node type with quotes", raw: `[{"Plan":{"Node Type":"Seq Scan' OR 1=1 --","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`},
		{name: "node type with parens", raw: `[{"Plan":{"Node Type":"Index Scan (secret_idx)","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`},
		{name: "node type with dollar sign", raw: `[{"Plan":{"Node Type":"Result $1","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`},
		{name: "node type empty", raw: `[{"Plan":{"Node Type":"","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`},
		{name: "node type leading digit", raw: `[{"Plan":{"Node Type":"9Result","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`},
		{name: "trailing document", raw: `[{"Plan":{"Node Type":"Result","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}] []`},
		{name: "depth overflow", raw: `[{"Plan":` + deepNode + `}]`},
		{name: "node overflow", raw: `[{"Plan":` + wideNodes(postgresExplainMaxNodes+1) + `}]`},
		{name: "nan literal", raw: `[{"Plan":{"Node Type":"Result","Startup Cost":NaN,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := sanitizePostgreSQLPlanJSON(json.RawMessage(test.raw)); err == nil {
				t.Fatalf("malformed plan accepted: %s", parserClip(test.raw))
			}
		})
	}
	if _, err := sanitizePostgreSQLPlanJSON(json.RawMessage(`[{"Plan":{"Node Type":"Result","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`)); err != nil {
		t.Fatalf("benign minimal plan refused: %v", err)
	}
	if _, err := sanitizePostgreSQLPlanJSON(json.RawMessage(strings.Repeat(" ", postgresExplainMaxRawBytes+1))); err == nil {
		t.Fatal("oversized plan JSON accepted")
	}
	if _, err := sanitizePostgreSQLPlanJSON(json.RawMessage(`[` + strings.Repeat(" ", postgresExplainMaxRawBytes-16) + `]`)); err == nil {
		_ = err
	}
}

func TestPostgresExplainPlanNodeTypeVocabularyAcceptsRealOperators(t *testing.T) {
	for _, nodeType := range []string{"Seq Scan", "Index Scan", "Index Only Scan", "Bitmap Index Scan", "Bitmap Heap Scan", "Hash Join", "Nested Loop", "Merge Join", "WindowAgg", "LockRows", "Tid Range Scan", "Shared Scan", "Limit", "Sort", "Aggregate", "Group Aggregate", "Gather Merge", "Memoize", "Sample Scan"} {
		raw := json.RawMessage(`[{"Plan":{"Node Type":"` + nodeType + `","Startup Cost":0,"Total Cost":0,"Plan Rows":1,"Plan Width":4}}]`)
		assessment, err := sanitizePostgreSQLPlanJSON(raw)
		if err != nil {
			t.Fatalf("real operator %q refused: %v", nodeType, err)
		}
		if assessment.Nodes[0].NodeType != nodeType {
			t.Fatalf("operator %q mangled to %q", nodeType, assessment.Nodes[0].NodeType)
		}
	}
}
