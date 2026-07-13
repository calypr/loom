package dataframe_test

// Tournament F is an isolated compact batch experiment. It freezes the
// endpoint-first plus typed-selector incumbent and batches the child_set_3
// edge lookup per root. Only identity, scope fields, and selector projections
// are retained in the grouped result; no FHIR payload is carried through the
// batch. No production compiler or index files are changed.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	dataframe "github.com/calypr/loom/internal/dataframe"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestCompactBatchTournamentCandidateBuilds(t *testing.T) {
	compiled := compileActualGDC(t, 1000)
	candidate, err := compactBatchChildSet3(compiled.Query)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"LET child_set_3_by_parent = (",
		"FILTER child_set_3_edge._to IN child_set_2[*]._id",
		"COLLECT __loom_child_set_3_parent_id = child_set_3_edge._to",
		"LET __loom_compact_child_set_3 = {",
		"nodes: __loom_child_set_3_rows[*].__loom_compact_child_set_3",
		"LET child_set_3 = UNIQUE((",
	} {
		if !strings.Contains(candidate, fragment) {
			t.Fatalf("compact batch candidate missing %q", fragment)
		}
	}
	if strings.Contains(candidate, "FOR __loom_physical_parent_set_4 IN child_set_2\n      FOR child_set_3_edge") {
		t.Fatal("candidate retained the per-parent edge loop")
	}
	if strings.Contains(candidate, "payload: child_set_3") || strings.Contains(candidate, "RETURN child_set_3_node") {
		t.Fatal("candidate appears to retain a full child payload")
	}
}

func TestCompactBatchTournamentProfilesActualGDC(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run compact batch tournament against Arango")
	}
	compiled := compileActualGDC(t, 1000)
	candidate, err := compactBatchChildSet3(compiled.Query)
	if err != nil {
		t.Fatal(err)
	}
	url, database, _ := compilerArangoTarget()
	client, err := arangostore.Open(context.Background(), url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	controlTimes := make([]float64, 0, 5)
	candidateTimes := make([]float64, 0, 5)
	controlBytes := make([]int, 0, 5)
	candidateBytes := make([]int, 0, 5)
	var controlHash, candidateHash string
	for run := 0; run < 5; run++ {
		controlQuery, controlBinds := cacheBust(compiled.Query, compiled.BindVars, 51000+run)
		candidateQuery, candidateBinds := cacheBust(candidate, compiled.BindVars, 52000+run)
		controlDuration, bytes, hash, err := executeOrdinary(ctx, client, controlQuery, controlBinds)
		if err != nil {
			t.Fatalf("control run %d: %v", run+1, err)
		}
		candidateDuration, candidateSize, candidateResultHash, err := executeOrdinary(ctx, client, candidateQuery, candidateBinds)
		if err != nil {
			t.Fatalf("candidate run %d: %v", run+1, err)
		}
		controlTimes = append(controlTimes, controlDuration)
		candidateTimes = append(candidateTimes, candidateDuration)
		controlBytes = append(controlBytes, bytes)
		candidateBytes = append(candidateBytes, candidateSize)
		controlHash, candidateHash = hash, candidateResultHash
	}
	if controlHash != candidateHash {
		t.Fatalf("result parity mismatch control=%s candidate=%s", controlHash, candidateHash)
	}
	controlProfileQuery, controlProfileBinds := cacheBust(compiled.Query, compiled.BindVars, 51999)
	candidateProfileQuery, candidateProfileBinds := cacheBust(candidate, compiled.BindVars, 52999)
	controlProfile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: controlProfileQuery, BindVars: controlProfileBinds, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		t.Fatalf("control PROFILE: %v", err)
	}
	candidateProfile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: candidateProfileQuery, BindVars: candidateProfileBinds, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		t.Fatalf("candidate PROFILE: %v", err)
	}
	if hashRawRows(controlProfile.Result) != hashRawRows(candidateProfile.Result) {
		t.Fatalf("PROFILE result parity mismatch control=%s candidate=%s", hashRawRows(controlProfile.Result), hashRawRows(candidateProfile.Result))
	}
	controlExplain, err := client.Explain(ctx, arangostore.ExplainRequest{Query: controlProfileQuery, BindVars: controlProfileBinds})
	if err != nil {
		t.Fatalf("control EXPLAIN: %v", err)
	}
	candidateExplain, err := client.Explain(ctx, arangostore.ExplainRequest{Query: candidateProfileQuery, BindVars: candidateProfileBinds})
	if err != nil {
		t.Fatalf("candidate EXPLAIN: %v", err)
	}
	controlSummary := arangostore.SummarizeProfile(controlProfile)
	candidateSummary := arangostore.SummarizeProfile(candidateProfile)
	t.Logf("compact batch control median=%.6f candidate median=%.6f control profile=%+v candidate profile=%+v bytes=%v/%v", median(controlTimes), median(candidateTimes), controlSummary, candidateSummary, controlBytes, candidateBytes)
	writeCompactBatchEvidence(t, compiled, candidate, controlTimes, candidateTimes, controlBytes, candidateBytes, controlHash, candidateHash, controlProfile, candidateProfile, controlExplain, candidateExplain)
	if median(candidateTimes) > median(controlTimes)*1.05 || candidateSummary.PeakMemory > 225*1024*1024 {
		t.Logf("compact batch candidate rejected by hard runtime/memory gate")
	}
}

func compactBatchChildSet3(query string) (string, error) {
	start := strings.Index(query, "  LET child_set_3 = UNIQUE((")
	end := strings.Index(query, "  LET child_set_4 = UNIQUE((")
	if start < 0 || end < 0 || end <= start {
		return "", fmt.Errorf("child_set_3/child_set_4 markers not found")
	}
	block := query[start:end]
	returnIndex := strings.Index(block, "        RETURN {")
	if returnIndex < 0 {
		return "", fmt.Errorf("child_set_3 projection return not found")
	}
	projection := block[returnIndex+len("        RETURN "):]
	if closeIndex := strings.Index(projection, "\n  ))"); closeIndex >= 0 {
		projection = projection[:closeIndex]
	}
	prefix := block[:returnIndex]
	edgeIndex := strings.Index(prefix, "FOR child_set_3_edge IN @@child_set_3_edge_collection")
	if edgeIndex < 0 {
		return "", fmt.Errorf("child_set_3 edge loop not found")
	}
	edgePrefix := prefix[edgeIndex:]
	if strings.Contains(edgePrefix, "FOR __loom_physical_parent_set_4 IN child_set_2") {
		return "", fmt.Errorf("parent loop unexpectedly inside edge prefix")
	}
	edgePrefix = strings.Replace(edgePrefix, "FILTER child_set_3_edge._to == __loom_physical_parent_set_4._id", "FILTER child_set_3_edge._to IN child_set_2[*]._id", 1)
	edgePrefix = strings.Replace(edgePrefix, "        SORT child_set_3_node._key\n", "", 1)
	if !strings.Contains(edgePrefix, "FILTER child_set_3_edge._to IN child_set_2[*]._id") {
		return "", fmt.Errorf("child_set_3 endpoint filter was not replaced")
	}
	// Strip the final edge-loop indentation only for readability; AQL ignores
	// whitespace. The projection object is compact and contains no payload key.
	batch := "  LET child_set_3_by_parent = (\n    " + strings.ReplaceAll(edgePrefix, "\n", "\n    ") +
		"        LET __loom_compact_child_set_3 = " + projection + "\n" +
		"        COLLECT __loom_child_set_3_parent_id = child_set_3_edge._to INTO __loom_child_set_3_rows\n" +
		"        RETURN {root_id: root._id, parent_id: __loom_child_set_3_parent_id, nodes: __loom_child_set_3_rows[*].__loom_compact_child_set_3}\n" +
		"  )\n" +
		"  LET child_set_3 = UNIQUE((\n" +
		"    FOR __loom_physical_parent_set_4 IN child_set_2\n" +
		"      LET __loom_child_set_3_group = FIRST((\n" +
		"        FOR __loom_group IN child_set_3_by_parent\n" +
		"          FILTER __loom_group.root_id == root._id AND __loom_group.parent_id == __loom_physical_parent_set_4._id\n" +
		"          RETURN __loom_group\n" +
		"      ))\n" +
		"      FOR child_set_3_item IN (__loom_child_set_3_group ? __loom_child_set_3_group.nodes : [])\n" +
		"        SORT child_set_3_item._key\n" +
		"        RETURN child_set_3_item\n" +
		"  ))\n"
	return query[:start] + batch + query[end:], nil
}

func writeCompactBatchEvidence(t *testing.T, compiled dataframe.CompiledQuery, candidate string, controlTimes, candidateTimes []float64, controlBytes, candidateBytes []int, controlHash, candidateHash string, controlProfile, candidateProfile arangostore.ProfileResult, controlExplain, candidateExplain arangostore.ExplainResult) {
	_, source, _, _ := runtime.Caller(0)
	directory := filepath.Join(filepath.Dir(source), "..", "..", "docs", "benchmarks", "round4", "tournament_compact_batch")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create compact batch evidence directory: %v", err)
	}
	writeJSON := func(name string, value any) {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatalf("encode %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(directory, name), append(data, '\n'), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(directory, "control.aql"), []byte(compiled.Query+"\n"), 0o644); err != nil {
		t.Fatalf("write control AQL: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "candidate.aql"), []byte(candidate+"\n"), 0o644); err != nil {
		t.Fatalf("write candidate AQL: %v", err)
	}
	writeJSON("control.profile.json", controlProfile)
	writeJSON("candidate.profile.json", candidateProfile)
	writeJSON("evidence.json", map[string]any{
		"fixture": "examples/meta_gdc_case_matrix.variables.json", "limit": 1000,
		"control_aql_sha256": sha256Hex(compiled.Query), "candidate_aql_sha256": sha256Hex(candidate),
		"control_result_sha256": controlHash, "candidate_result_sha256": candidateHash,
		"control_seconds": controlTimes, "candidate_seconds": candidateTimes,
		"control_bytes": controlBytes, "candidate_bytes": candidateBytes,
		"control_profile": arangostore.SummarizeProfile(controlProfile), "candidate_profile": arangostore.SummarizeProfile(candidateProfile),
		"control_explain": arangostore.AssessExplainResult(controlExplain), "candidate_explain": arangostore.AssessExplainResult(candidateExplain),
		"decision": "pending-threshold-review",
	})
}
