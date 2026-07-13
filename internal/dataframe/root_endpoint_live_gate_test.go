package dataframe_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	dataframe "github.com/calypr/loom/internal/dataframe"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type rootEndpointGateShape struct {
	Name          string                        `json:"name"`
	Limit         int                           `json:"limit"`
	Endpoint      bool                          `json:"endpoint"`
	AQLSHA256     string                        `json:"aql_sha256"`
	ResultSHA256  string                        `json:"result_sha256"`
	Rows          int                           `json:"rows"`
	Bytes         []int                         `json:"bytes"`
	WarmSeconds   []float64                     `json:"warm_seconds"`
	MedianSeconds float64                       `json:"median_seconds"`
	Explain       arangostore.ExplainAssessment `json:"explain"`
	Profile       rootEndpointGateProfile       `json:"profile"`
	RawProfile    arangostore.ProfileResult     `json:"-"`
	Query         string                        `json:"-"`
	BindVars      map[string]any                `json:"-"`
}

type rootEndpointGateProfile struct {
	ScannedFull     int                              `json:"scanned_full"`
	ScannedIndex    int                              `json:"scanned_index"`
	PeakMemoryBytes uint64                           `json:"peak_memory_bytes"`
	Phases          arangostore.ProfilePhases        `json:"phases"`
	TopNodes        []arangostore.ProfileNodeSummary `json:"top_nodes"`
}

// TestRootEndpointProductionGateAgainstArango is opt-in because it reads the
// provisioned META database. It compares endpoint policy on/off at both
// required limits through BuilderFromInput and consumes every result row.
func TestRootEndpointProductionGateAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run root endpoint production gate")
	}
	url, database, _ := compilerArangoTarget()
	client, err := arangostore.Open(context.Background(), url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	results := make([]rootEndpointGateShape, 0, 4)
	for _, limit := range []int{25, 1000} {
		for _, endpoint := range []bool{false, true} {
			compiled := compileRootEndpointGateRequest(t, limit, endpoint)
			shape, err := runRootEndpointGateShape(ctx, client, compiled, limit, endpoint)
			if err != nil {
				t.Fatalf("limit=%d endpoint=%t: %v", limit, endpoint, err)
			}
			results = append(results, shape)
			t.Logf("root endpoint limit=%d endpoint=%t median=%.6fs executing=%.6fs rows=%d hash=%s indexes=%#v profile=%+v", limit, endpoint, shape.MedianSeconds, shape.Profile.Phases.Executing, shape.Rows, shape.ResultSHA256, shape.Explain.Indexes, shape.Profile)
		}
	}
	for _, limit := range []int{25, 1000} {
		control := findRootGateShape(results, limit, false)
		candidate := findRootGateShape(results, limit, true)
		if control.ResultSHA256 != candidate.ResultSHA256 {
			t.Fatalf("limit=%d result parity mismatch control=%s candidate=%s", limit, control.ResultSHA256, candidate.ResultSHA256)
		}
		if control.Rows != candidate.Rows || control.Bytes[0] != candidate.Bytes[0] {
			t.Fatalf("limit=%d row/response parity mismatch control rows/bytes=%d/%d candidate=%d/%d", limit, control.Rows, control.Bytes[0], candidate.Rows, candidate.Bytes[0])
		}
		if candidate.Profile.PeakMemoryBytes >= 200000000 {
			t.Fatalf("limit=%d candidate memory exceeds 200 MB: %d", limit, candidate.Profile.PeakMemoryBytes)
		}
		if len(candidate.Explain.FullCollectionScans) != 0 {
			t.Fatalf("limit=%d candidate full scans: %#v", limit, candidate.Explain.FullCollectionScans)
		}
		if !hasRootEndpointCompoundIndex(candidate.Explain) {
			t.Fatalf("limit=%d candidate did not select endpoint compound index: %#v", limit, candidate.Explain.Indexes)
		}
		if limit == 1000 && candidate.MedianSeconds >= control.MedianSeconds*0.90 {
			t.Fatalf("1,000-row endpoint gate missed 10%% improvement: control=%.6fs candidate=%.6fs", control.MedianSeconds, candidate.MedianSeconds)
		}
	}
	writeRootEndpointGateEvidence(t, results)
}

func compileRootEndpointGateRequest(t *testing.T, limit int, endpoint bool) dataframe.CompiledQuery {
	old, had := os.LookupEnv("LOOM_PHYSICAL_RULE_ENDPOINT_TRAVERSAL")
	if endpoint {
		_ = os.Unsetenv("LOOM_PHYSICAL_RULE_ENDPOINT_TRAVERSAL")
	} else {
		_ = os.Setenv("LOOM_PHYSICAL_RULE_ENDPOINT_TRAVERSAL", "off")
	}
	defer func() {
		if had {
			_ = os.Setenv("LOOM_PHYSICAL_RULE_ENDPOINT_TRAVERSAL", old)
		} else {
			_ = os.Unsetenv("LOOM_PHYSICAL_RULE_ENDPOINT_TRAVERSAL")
		}
	}()
	return compileActualGDC(t, limit)
}

func runRootEndpointGateShape(ctx context.Context, client *arangostore.Client, compiled dataframe.CompiledQuery, limit int, endpoint bool) (rootEndpointGateShape, error) {
	shape := rootEndpointGateShape{Name: fmt.Sprintf("limit_%d_endpoint_%t", limit, endpoint), Limit: limit, Endpoint: endpoint, AQLSHA256: sha256Hex(compiled.Query), Query: compiled.Query, BindVars: compiled.BindVars}
	runs := 5
	if limit == 25 {
		runs = 3
	}
	for run := 0; run < runs; run++ {
		query, binds := cacheBust(compiled.Query, compiled.BindVars, 18000+limit+run)
		seconds, bytes, hash, err := executeOrdinary(ctx, client, query, binds)
		if err != nil {
			return shape, err
		}
		shape.WarmSeconds = append(shape.WarmSeconds, seconds)
		shape.Bytes = append(shape.Bytes, bytes)
		shape.ResultSHA256 = hash
	}
	shape.MedianSeconds = median(shape.WarmSeconds)
	shape.Rows = limit
	query, binds := cacheBust(compiled.Query, compiled.BindVars, 19000+limit+boolInt(endpoint))
	explain, err := client.Explain(ctx, arangostore.ExplainRequest{Query: query, BindVars: binds})
	if err != nil {
		return shape, err
	}
	shape.Explain = arangostore.AssessExplainResult(explain)
	profile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: query, BindVars: binds, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		return shape, err
	}
	shape.RawProfile = profile
	summary := arangostore.SummarizeProfile(profile)
	nodes := append([]arangostore.ProfileNodeSummary(nil), summary.Nodes...)
	if len(nodes) > 20 {
		nodes = nodes[:20]
	}
	shape.Profile = rootEndpointGateProfile{ScannedFull: summary.ScannedFull, ScannedIndex: summary.ScannedIndex, PeakMemoryBytes: summary.PeakMemory, Phases: profile.Extra.Profile, TopNodes: nodes}
	return shape, nil
}

func hasRootEndpointCompoundIndex(assessment arangostore.ExplainAssessment) bool {
	want := []string{"_to", "project", "dataset_generation", "label", "from_type"}
	for _, index := range assessment.Indexes {
		if index.Collection != "fhir_edge" || len(index.Fields) != len(want) {
			continue
		}
		match := true
		for i := range want {
			if index.Fields[i] != want[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func findRootGateShape(results []rootEndpointGateShape, limit int, endpoint bool) rootEndpointGateShape {
	for _, result := range results {
		if result.Limit == limit && result.Endpoint == endpoint {
			return result
		}
	}
	return rootEndpointGateShape{}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func writeRootEndpointGateEvidence(t *testing.T, results []rootEndpointGateShape) {
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(source), "..", "..")
	directory := filepath.Join(root, "docs", "benchmarks", "round4", "root_endpoint_integration")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	serializable := make([]map[string]any, 0, len(results))
	for _, result := range results {
		name := fmt.Sprintf("limit_%d_endpoint_%t", result.Limit, result.Endpoint)
		if err := os.WriteFile(filepath.Join(directory, name+".aql"), []byte(result.Query+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.MarshalIndent(result.RawProfile, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, name+".profile.json"), append(encoded, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		serializable = append(serializable, map[string]any{"name": result.Name, "limit": result.Limit, "endpoint": result.Endpoint, "aql_sha256": result.AQLSHA256, "result_sha256": result.ResultSHA256, "rows": result.Rows, "bytes": result.Bytes, "warm_seconds": result.WarmSeconds, "median_seconds": result.MedianSeconds, "explain": result.Explain, "profile": result.Profile})
	}
	encoded, err := json.MarshalIndent(map[string]any{"results": serializable, "decision": "pending-coordinator-threshold-review"}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "RESULTS.json"), append(encoded, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
