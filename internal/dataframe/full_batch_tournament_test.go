package dataframe_test

// This file is an isolated, test-only WP9 experiment. It hand-edits the
// production AQL emitted by BuilderFromInput; it intentionally does not add a
// compiler strategy or renderer branch. Promotion requires exact output
// parity and a whole-query runtime win on the complete GDC request.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	dataframe "github.com/calypr/loom/internal/dataframe"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

// TestFullBatchTournamentCandidateBuilds verifies that the candidate is
// generated from the real frontend input and remains parameterized. It does
// not require Arango and therefore protects the experiment when the live
// database is unavailable.
func TestFullBatchTournamentCandidateBuilds(t *testing.T) {
	compiled := compileActualGDC(t, 1000)
	candidate, err := batchRootChildSet1Query(compiled.Query)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"LET root_window = (",
		"LET batch_child_set_1_grouped = (",
		"FILTER edge._to IN SLICE(root_window[*]._id",
		"COLLECT __batch_root_id = edge._to",
		"FOR child_set_1_item IN (__batch_child_set_1_group ?",
		"DOCUMENT(edge._from)",
	} {
		if !strings.Contains(candidate, fragment) {
			t.Fatalf("batch candidate missing %q", fragment)
		}
	}
	if strings.Contains(candidate, "FOR __batch_child_set_1 IN shared_root_subject_Patient_neighbors") {
		t.Fatal("candidate still consumes child_set_1 from the correlated shared traversal")
	}
	if !strings.Contains(candidate, "@@child_set_1_edge_collection") || !strings.Contains(candidate, "@project") {
		t.Fatal("candidate lost collection or scope binds")
	}
	if strings.Contains(candidate, "ARANGODB_PROTO") || strings.Contains(candidate, "Patient") == false {
		t.Fatal("candidate unexpectedly lost the generic root shape")
	}
}

// TestFullBatchTournamentProfilesActualGDC is opt-in because it executes the
// complete 1,000-row request against the local Arango instance. The control
// and every candidate run alternate, consume all output, and add a harmless
// bind-backed predicate so Arango's result cache cannot satisfy the request.
func TestFullBatchTournamentProfilesActualGDC(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run WP9 against Arango")
	}
	compiled := compileActualGDC(t, 1000)
	candidate, err := batchRootChildSet1Query(compiled.Query)
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

	batchSizes := []int{25, 100, 250, 500, 1000}
	if raw := os.Getenv("LOOM_WP9_BATCH_SIZES"); raw != "" {
		batchSizes = nil
		for _, value := range strings.Split(raw, ",") {
			size, parseErr := strconv.Atoi(strings.TrimSpace(value))
			if parseErr != nil || size <= 0 {
				t.Fatalf("invalid LOOM_WP9_BATCH_SIZES value %q", value)
			}
			batchSizes = append(batchSizes, size)
		}
	}
	runs := 5
	if raw := os.Getenv("LOOM_WP9_RUNS"); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed <= 0 {
			t.Fatalf("invalid LOOM_WP9_RUNS value %q", raw)
		}
		runs = parsed
	}
	results := make([]batchTournamentResult, 0, len(batchSizes))
	for _, batchSize := range batchSizes {
		candidateSized := strings.ReplaceAll(candidate, "@__loom_batch_size", fmt.Sprintf("%d", batchSize))
		controlTimes := make([]float64, 0, 5)
		candidateTimes := make([]float64, 0, 5)
		controlBytes := make([]int, 0, 5)
		candidateBytes := make([]int, 0, 5)
		var controlHash, candidateHash string
		for run := 0; run < runs; run++ {
			controlQuery, controlBinds := cacheBust(compiled.Query, compiled.BindVars, 21000+batchSize*10+run)
			candidateQuery, candidateBinds := cacheBust(candidateSized, compiled.BindVars, 22000+batchSize*10+run)
			started := time.Now()
			controlDuration, bytes, hash, err := executeOrdinary(ctx, client, controlQuery, controlBinds)
			if err != nil {
				t.Fatalf("batch size %d control run %d: %v", batchSize, run+1, err)
			}
			_ = started
			controlTimes = append(controlTimes, controlDuration)
			controlBytes = append(controlBytes, bytes)
			controlHash = hash
			candidateDuration, bytes, hash, err := executeOrdinary(ctx, client, candidateQuery, candidateBinds)
			if err != nil {
				t.Fatalf("batch size %d candidate run %d: %v", batchSize, run+1, err)
			}
			candidateTimes = append(candidateTimes, candidateDuration)
			candidateBytes = append(candidateBytes, bytes)
			candidateHash = hash
		}
		if controlHash != candidateHash {
			t.Fatalf("batch size %d result parity mismatch control=%s candidate=%s", batchSize, controlHash, candidateHash)
		}
		controlProfileQuery, controlProfileBinds := cacheBust(compiled.Query, compiled.BindVars, 31000+batchSize)
		candidateProfileQuery, candidateProfileBinds := cacheBust(candidateSized, compiled.BindVars, 32000+batchSize)
		controlProfile, err := client.Profile(ctx, arangostore.ProfileRequest{
			Query: controlProfileQuery, BindVars: controlProfileBinds, BatchSize: 10000, Count: true,
			Options: arangostore.ProfileOptions{Profile: 2},
		})
		if err != nil {
			t.Fatalf("batch size %d control PROFILE: %v", batchSize, err)
		}
		candidateProfile, err := client.Profile(ctx, arangostore.ProfileRequest{
			Query: candidateProfileQuery, BindVars: candidateProfileBinds, BatchSize: 10000, Count: true,
			Options: arangostore.ProfileOptions{Profile: 2},
		})
		if err != nil {
			t.Fatalf("batch size %d candidate PROFILE: %v", batchSize, err)
		}
		if hashRawRows(controlProfile.Result) != hashRawRows(candidateProfile.Result) {
			t.Fatalf("batch size %d PROFILE result parity mismatch", batchSize)
		}
		controlSummary := arangostore.SummarizeProfile(controlProfile)
		candidateSummary := arangostore.SummarizeProfile(candidateProfile)
		results = append(results, batchTournamentResult{
			BatchSize: batchSize, ControlSeconds: controlTimes, CandidateSeconds: candidateTimes,
			ControlBytes: controlBytes, CandidateBytes: candidateBytes, ControlHash: controlHash,
			CandidateHash: candidateHash, ControlProfile: controlSummary, CandidateProfile: candidateSummary,
		})
		t.Logf("WP9 batch size=%d control_median=%.6f candidate_median=%.6f control_profile=%+v candidate_profile=%+v bytes=%v/%v", batchSize, median(controlTimes), median(candidateTimes), controlSummary, candidateSummary, controlBytes, candidateBytes)
	}
	writeBatchTournamentEvidence(t, compiled, candidate, results)
}

type batchTournamentResult struct {
	BatchSize        int                        `json:"batch_size"`
	ControlSeconds   []float64                  `json:"control_seconds"`
	CandidateSeconds []float64                  `json:"candidate_seconds"`
	ControlBytes     []int                      `json:"control_bytes"`
	CandidateBytes   []int                      `json:"candidate_bytes"`
	ControlHash      string                     `json:"control_result_sha256"`
	CandidateHash    string                     `json:"candidate_result_sha256"`
	ControlProfile   arangostore.ProfileSummary `json:"control_profile"`
	CandidateProfile arangostore.ProfileSummary `json:"candidate_profile"`
}

// batchRootChildSet1Query converts only the Condition relationship to a
// batched endpoint lookup. The existing shared root traversal is still used
// by Specimen and Observation, but Condition nodes are excluded from it so the
// candidate does not pay for the same relationship twice.
func batchRootChildSet1Query(query string) (string, error) {
	rootStart := strings.Index(query, "FOR root IN @@root_collection")
	sharedStart := strings.Index(query, "  LET shared_root_subject_Patient_neighbors =")
	child1Start := strings.Index(query, "  LET child_set_1 = UNIQUE((")
	child2Start := strings.Index(query, "  LET child_set_2 = UNIQUE((")
	if rootStart < 0 || sharedStart < 0 || child1Start < 0 || child2Start < 0 || !(rootStart < sharedStart && sharedStart < child1Start && child1Start < child2Start) {
		return "", fmt.Errorf("production AQL markers for root/child_set_1 not found")
	}
	rootPrefix := strings.TrimSpace(query[rootStart:sharedStart])
	rootWindow := "LET root_window = (\n" + batchIndent(rootPrefix, 2) + "\n  RETURN root\n)\n"

	shared := query[sharedStart:child1Start]
	nodeFilter := "      FILTER POSITION(@shared_root_subject_Patient_neighbors_target_types, child_set_1_node.resourceType)\n"
	if !strings.Contains(shared, nodeFilter) {
		return "", fmt.Errorf("shared traversal node type filter not found")
	}
	shared = strings.Replace(shared, nodeFilter, nodeFilter+"      FILTER child_set_1_node.resourceType != @child_set_1_target_type\n", 1)

	child1 := query[child1Start:child2Start]
	oldConsumer := "FOR child_set_1_item IN shared_root_subject_Patient_neighbors"
	newConsumer := "LET __batch_child_set_1_group = FIRST((\n    FOR __batch_group IN batch_child_set_1_grouped\n      FILTER __batch_group.root_id == root._id\n      RETURN __batch_group\n  ))\n  LET child_set_1 = UNIQUE((\n    FOR child_set_1_item IN (__batch_child_set_1_group ? __batch_child_set_1_group.nodes : [])"
	if !strings.Contains(child1, oldConsumer) {
		return "", fmt.Errorf("child_set_1 shared consumer not found")
	}
	child1 = strings.Replace(child1, "  LET child_set_1 = UNIQUE((\n    FOR "+oldConsumer[len("FOR "):], newConsumer, 1)
	// The replacement above intentionally retains the original projection and
	// closes; it changes only the input collection and inserts the left join.
	if strings.Contains(child1, oldConsumer) {
		return "", fmt.Errorf("child_set_1 consumer replacement incomplete")
	}

	batch := `LET batch_child_set_1_grouped = (
  FOR __batch_offset IN RANGE(0, LENGTH(root_window), @__loom_batch_size)
    FOR edge IN @@child_set_1_edge_collection
      FILTER edge._to IN SLICE(root_window[*]._id, __batch_offset, @__loom_batch_size)
      FILTER edge.label == @child_set_1_label
      FILTER edge.project == @project
      FILTER edge.dataset_generation == @dataset_generation
      FILTER @auth_resource_paths_unrestricted == true OR edge.auth_resource_path IN @auth_resource_paths
      LET node = DOCUMENT(edge._from)
      FILTER node != null
      FILTER node.resourceType == @child_set_1_target_type
      FILTER node.project == @project
      FILTER node.dataset_generation == @dataset_generation
      FILTER @auth_resource_paths_unrestricted == true OR node.auth_resource_path IN @auth_resource_paths
      COLLECT __batch_root_id = edge._to INTO __batch_rows
      RETURN {root_id: __batch_root_id, nodes: __batch_rows[*].node}
)
FOR root IN root_window
`
	return rootWindow + batch + shared + child1 + query[child2Start:], nil
}

func batchIndent(value string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	lines := strings.Split(strings.TrimSpace(value), "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

func writeBatchTournamentEvidence(t *testing.T, compiled dataframe.CompiledQuery, candidate string, results []batchTournamentResult) {
	_, source, _, _ := runtime.Caller(0)
	directory := filepath.Join(filepath.Dir(source), "..", "..", "docs", "benchmarks", "round4", "wp9")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create WP9 evidence directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "control.aql"), []byte(compiled.Query+"\n"), 0o644); err != nil {
		t.Fatalf("write WP9 control AQL: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "candidate.aql"), []byte(candidate+"\n"), 0o644); err != nil {
		t.Fatalf("write WP9 candidate AQL: %v", err)
	}
	payload := map[string]any{
		"input":                "examples/meta_gdc_case_matrix.variables.json",
		"limit":                1000,
		"control_aql_sha256":   sha256Hex(compiled.Query),
		"candidate_aql_sha256": sha256Hex(candidate),
		"results":              results,
		"decision":             "pending-live-evidence",
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("encode WP9 evidence: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "evidence.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write WP9 evidence: %v", err)
	}
}
