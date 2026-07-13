package dataframe_test

// Tournament C is an isolated AQL experiment. It freezes the current
// endpoint-first plus typed-selector query produced by the real compiler and
// replaces only the three nested DOCUMENT(edge._from) lookups. No production
// IR, renderer, storage route, or index is changed by this file.

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

func TestMaterializationTournamentCandidateBuilds(t *testing.T) {
	compiled := compileActualGDC(t, 1000)
	candidate, binds, err := compactMaterializationCandidate(compiled.Query, compiled.BindVars)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(compiled.Query, "DOCUMENT(child_set_3_edge._from)") != 1 ||
		strings.Count(compiled.Query, "DOCUMENT(child_set_4_edge._from)") != 1 ||
		strings.Count(compiled.Query, "DOCUMENT(child_set_5_edge._from)") != 1 {
		t.Fatalf("current compiled request is not the expected endpoint-first baseline; document lookups=%d/%d/%d", strings.Count(compiled.Query, "DOCUMENT(child_set_3_edge._from)"), strings.Count(compiled.Query, "DOCUMENT(child_set_4_edge._from)"), strings.Count(compiled.Query, "DOCUMENT(child_set_5_edge._from)"))
	}
	for _, set := range []string{"3", "4", "5"} {
		for _, fragment := range []string{
			"FOR child_set_" + set + "_doc IN @@child_set_" + set + "_node_collection",
			"PARSE_IDENTIFIER(child_set_" + set + "_edge._from).key",
			"KEEP(child_set_" + set + "_doc",
		} {
			if !strings.Contains(candidate, fragment) {
				t.Fatalf("candidate missing compact materialization fragment %q", fragment)
			}
		}
		if _, ok := binds["@child_set_"+set+"_node_collection"]; !ok {
			t.Fatalf("candidate missing node collection bind for child_set_%s", set)
		}
	}
	if strings.Contains(candidate, "DOCUMENT(child_set_3_edge._from)") || strings.Contains(candidate, "DOCUMENT(child_set_4_edge._from)") || strings.Contains(candidate, "DOCUMENT(child_set_5_edge._from)") {
		t.Fatal("candidate retained a DOCUMENT endpoint lookup")
	}
}

func TestMaterializationTournamentProfilesActualGDC(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run materialization tournament against Arango")
	}
	compiled := compileActualGDC(t, 1000)
	candidate, candidateBinds, err := compactMaterializationCandidate(compiled.Query, compiled.BindVars)
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
		controlQuery, controlBinds := cacheBust(compiled.Query, compiled.BindVars, 41000+run)
		candidateQuery, binds := cacheBust(candidate, candidateBinds, 42000+run)
		controlDuration, bytes, hash, err := executeOrdinary(ctx, client, controlQuery, controlBinds)
		if err != nil {
			t.Fatalf("control run %d: %v", run+1, err)
		}
		candidateDuration, candidateSize, candidateResultHash, err := executeOrdinary(ctx, client, candidateQuery, binds)
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

	controlProfileQuery, controlProfileBinds := cacheBust(compiled.Query, compiled.BindVars, 41999)
	candidateProfileQuery, candidateProfileBinds := cacheBust(candidate, candidateBinds, 42999)
	controlProfile, err := client.Profile(ctx, arangostore.ProfileRequest{
		Query: controlProfileQuery, BindVars: controlProfileBinds, BatchSize: 10000, Count: true,
		Options: arangostore.ProfileOptions{Profile: 2},
	})
	if err != nil {
		t.Fatalf("control PROFILE: %v", err)
	}
	candidateProfile, err := client.Profile(ctx, arangostore.ProfileRequest{
		Query: candidateProfileQuery, BindVars: candidateProfileBinds, BatchSize: 10000, Count: true,
		Options: arangostore.ProfileOptions{Profile: 2},
	})
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
	t.Logf("materialization tournament control median=%.6f candidate median=%.6f control profile=%+v candidate profile=%+v bytes=%v/%v", median(controlTimes), median(candidateTimes), controlSummary, candidateSummary, controlBytes, candidateBytes)
	writeMaterializationEvidence(t, compiled, candidate, controlTimes, candidateTimes, controlBytes, candidateBytes, controlHash, candidateHash, controlProfile, candidateProfile, controlExplain, candidateExplain)
	if median(candidateTimes) >= median(controlTimes)*0.95 {
		t.Logf("candidate rejected: median does not clear 5%% whole-query gate control=%.6f candidate=%.6f", median(controlTimes), median(candidateTimes))
	}
}

// compactMaterializationCandidate replaces DOCUMENT endpoint retrieval with a
// type-directed primary-key lookup. KEEP retains only fields used by the
// current child projections and scope checks. The node collection binds are
// derived from the compiled target-type binds; no FHIR route is hard-coded in
// production code.
func compactMaterializationCandidate(query string, sourceBinds map[string]any) (string, map[string]any, error) {
	candidate := query
	binds := make(map[string]any, len(sourceBinds)+3)
	for key, value := range sourceBinds {
		binds[key] = value
	}
	for _, set := range []string{"3", "4", "5"} {
		setPrefix := "child_set_" + set
		old := fmt.Sprintf("LET %s_node = DOCUMENT(%s_edge._from)", setPrefix, setPrefix)
		new := fmt.Sprintf("FOR %s_doc IN @@%s_node_collection\n        FILTER %s_doc._key == PARSE_IDENTIFIER(%s_edge._from).key\n        FILTER %s_doc._id == %s_edge._from\n        LET %s_node = KEEP(%s_doc, \"_id\", \"_key\", \"id\", \"resourceType\", \"project\", \"dataset_generation\", \"auth_resource_path\", \"payload\")", setPrefix, setPrefix, setPrefix, setPrefix, setPrefix, setPrefix, setPrefix, setPrefix)
		if strings.Count(candidate, old) != 1 {
			return "", nil, fmt.Errorf("expected one %s in endpoint baseline, found %d", old, strings.Count(candidate, old))
		}
		candidate = strings.Replace(candidate, old, new, 1)
		target, ok := binds[setPrefix+"_target_type"].(string)
		if !ok || target == "" {
			return "", nil, fmt.Errorf("missing compiled target type bind %s_target_type", setPrefix)
		}
		binds["@"+setPrefix+"_node_collection"] = target
	}
	return candidate, binds, nil
}

func writeMaterializationEvidence(t *testing.T, compiled dataframe.CompiledQuery, candidate string, controlTimes, candidateTimes []float64, controlBytes, candidateBytes []int, controlHash, candidateHash string, controlProfile, candidateProfile arangostore.ProfileResult, controlExplain, candidateExplain arangostore.ExplainResult) {
	_, source, _, _ := runtime.Caller(0)
	directory := filepath.Join(filepath.Dir(source), "..", "..", "docs", "benchmarks", "round4", "tournament_materialization")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create materialization evidence directory: %v", err)
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
		"fixture":                 "examples/meta_gdc_case_matrix.variables.json",
		"limit":                   1000,
		"control_aql_sha256":      sha256Hex(compiled.Query),
		"candidate_aql_sha256":    sha256Hex(candidate),
		"control_result_sha256":   controlHash,
		"candidate_result_sha256": candidateHash,
		"control_seconds":         controlTimes,
		"candidate_seconds":       candidateTimes,
		"control_bytes":           controlBytes,
		"candidate_bytes":         candidateBytes,
		"control_profile":         arangostore.SummarizeProfile(controlProfile),
		"candidate_profile":       arangostore.SummarizeProfile(candidateProfile),
		"control_explain":         arangostore.AssessExplainResult(controlExplain),
		"candidate_explain":       arangostore.AssessExplainResult(candidateExplain),
		"decision":                "pending-threshold-review",
	})
}
