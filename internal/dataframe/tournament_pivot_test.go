package dataframe_test

// Round 4 pivot tournament. This is an isolated AQL experiment over the
// endpoint+typed-selector production shape; no production compiler changes.

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

	arangostore "github.com/calypr/loom/internal/store/arango"
)

type pivotTournamentRun struct {
	ControlSeconds   []float64                 `json:"control_seconds"`
	CandidateSeconds []float64                 `json:"candidate_seconds"`
	ControlBytes     []int                     `json:"control_bytes"`
	CandidateBytes   []int                     `json:"candidate_bytes"`
	ControlHash      string                    `json:"control_result_sha256"`
	CandidateHash    string                    `json:"candidate_result_sha256"`
	ControlProfile   arangostore.ProfileResult `json:"control_profile"`
	CandidateProfile arangostore.ProfileResult `json:"candidate_profile"`
}

type pivotTournamentMeasurement struct {
	Seconds []float64                 `json:"seconds"`
	Bytes   []int                     `json:"bytes"`
	Hash    string                    `json:"result_sha256"`
	Profile arangostore.ProfileResult `json:"profile"`
}

func TestPivotCandidateStructure(t *testing.T) {
	control := readPivotAQL(t)
	candidate, err := buildPivotCandidate(control)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(candidate, "FOR __pivot_key IN @pivot_child_set_6_observation_values_columns") || !strings.Contains(candidate, "SORTED_UNIQUE(FLATTEN((") {
		t.Fatalf("candidate did not build fixed-column pivot reduction:\n%s", candidate)
	}
	if strings.Contains(candidate, "COLLECT __pivot_key = __pair.key") {
		t.Fatal("candidate retained the old per-pair COLLECT pivot reduction")
	}
	if !strings.Contains(candidate, "FOR __pivot_key IN @pivot_child_set_6_observation_values_columns") {
		t.Fatal("candidate lost fixed pivot-column order")
	}
	t.Logf("pivot candidate control_hash=%s candidate_hash=%s", sha256Hex(control), sha256Hex(candidate))
}

func TestPivotCandidateProfilesActualGDC(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run pivot tournament")
	}
	control := readPivotAQL(t)
	candidate, err := buildPivotCandidate(control)
	if err != nil {
		t.Fatal(err)
	}
	binds := readPivotBinds(t)
	url, database, _ := compilerArangoTarget()
	client, err := arangostore.Open(context.Background(), url, database)
	if err != nil {
		t.Fatalf("open Arango: %v", err)
	}
	defer client.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	run := pivotTournamentRun{}
	for index := 0; index < 5; index++ {
		controlQuery, controlBinds := cacheBust(control, binds, 68000+index*2)
		candidateQuery, candidateBinds := cacheBust(candidate, binds, 68001+index*2)
		seconds, bytes, hash, err := executeOrdinary(ctx, client, controlQuery, controlBinds)
		if err != nil {
			t.Fatalf("control run %d: %v", index+1, err)
		}
		run.ControlSeconds = append(run.ControlSeconds, seconds)
		run.ControlBytes = append(run.ControlBytes, bytes)
		run.ControlHash = hash
		seconds, bytes, hash, err = executeOrdinary(ctx, client, candidateQuery, candidateBinds)
		if err != nil {
			t.Fatalf("candidate run %d: %v", index+1, err)
		}
		run.CandidateSeconds = append(run.CandidateSeconds, seconds)
		run.CandidateBytes = append(run.CandidateBytes, bytes)
		run.CandidateHash = hash
	}
	if run.ControlHash != run.CandidateHash {
		t.Fatalf("pivot result parity mismatch control=%s candidate=%s", run.ControlHash, run.CandidateHash)
	}
	controlProfile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: control, BindVars: binds, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		t.Fatalf("control PROFILE: %v", err)
	}
	candidateProfile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: candidate, BindVars: binds, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		t.Fatalf("candidate PROFILE: %v", err)
	}
	if hashRawRows(controlProfile.Result) != hashRawRows(candidateProfile.Result) {
		t.Fatalf("pivot PROFILE result parity mismatch")
	}
	run.ControlProfile = controlProfile
	run.CandidateProfile = candidateProfile
	t.Logf("pivot control_median=%.6f candidate_median=%.6f control_profile=%+v candidate_profile=%+v", median(run.ControlSeconds), median(run.CandidateSeconds), arangostore.SummarizeProfile(controlProfile), arangostore.SummarizeProfile(candidateProfile))
	writePivotEvidence(t, control, candidate, run)
}

// TestPivotMiniTournamentProfilesReductionShapes compares only pivot reduction
// shapes. The source child_set_6 and every non-pivot expression remain frozen,
// so a result or timing change is attributable to the pivot lowering.
func TestPivotMiniTournamentProfilesReductionShapes(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run pivot tournament")
	}
	control := readPivotAQL(t)
	columns := readPivotColumns(t)
	candidates := map[string]string{
		"min_zip":       buildPivotReduction(control, pivotMinZipBody(false, false)),
		"min_zip_hash":  buildPivotReduction(control, pivotMinZipBody(true, false)),
		"min_zip_has":   buildPivotReduction(control, pivotMinZipBody(false, true)),
		"sorted_unique": buildPivotReduction(control, pivotSortedUniqueZipBody()),
	}
	binds := readPivotBinds(t)
	allowed := make(map[string]bool, len(columns))
	for _, column := range columns {
		allowed[column] = true
	}
	candidateBinds := make(map[string]any, len(binds)+1)
	for key, value := range binds {
		candidateBinds[key] = value
	}
	candidateBinds["pivot_allowed"] = allowed
	mapCandidateBinds := make(map[string]any, len(candidateBinds))
	for key, value := range candidateBinds {
		if key != "pivot_child_set_6_observation_values_columns" {
			mapCandidateBinds[key] = value
		}
	}
	url, database, _ := compilerArangoTarget()
	client, err := arangostore.Open(context.Background(), url, database)
	if err != nil {
		t.Fatalf("open Arango: %v", err)
	}
	defer client.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	runs := map[string]*pivotTournamentMeasurement{"control": {}}
	for name := range candidates {
		runs[name] = &pivotTournamentMeasurement{}
	}
	runCount := 5
	if value := os.Getenv("LOOM_PIVOT_TOURNAMENT_RUNS"); value != "" {
		if _, scanErr := fmt.Sscanf(value, "%d", &runCount); scanErr != nil || runCount < 1 {
			t.Fatalf("LOOM_PIVOT_TOURNAMENT_RUNS must be a positive integer, got %q", value)
		}
	}
	candidateNames := sortedPivotCandidateNames(candidates)
	for index := 0; index < runCount; index++ {
		t.Logf("pivot tournament run %d/%d: control", index+1, runCount)
		query, queryBinds := cacheBust(control, binds, 72000+index*10)
		seconds, bytes, hash, runErr := executeOrdinary(ctx, client, query, queryBinds)
		if runErr != nil {
			t.Fatalf("control run %d: %v", index+1, runErr)
		}
		runs["control"].Seconds = append(runs["control"].Seconds, seconds)
		runs["control"].Bytes = append(runs["control"].Bytes, bytes)
		runs["control"].Hash = hash
		for candidateIndex, name := range candidateNames {
			t.Logf("pivot tournament run %d/%d: candidate %s", index+1, runCount, name)
			candidateRunBindsBase := binds
			if name == "min_zip_has" {
				candidateRunBindsBase = mapCandidateBinds
			}
			candidateQuery, candidateRunBinds := cacheBust(candidates[name], candidateRunBindsBase, 72001+index*10+candidateIndex)
			seconds, bytes, hash, runErr = executeOrdinary(ctx, client, candidateQuery, candidateRunBinds)
			if runErr != nil {
				t.Fatalf("%s run %d: %v", name, index+1, runErr)
			}
			runs[name].Seconds = append(runs[name].Seconds, seconds)
			runs[name].Bytes = append(runs[name].Bytes, bytes)
			runs[name].Hash = hash
		}
	}
	for _, name := range append([]string{"control"}, candidateNames...) {
		run := runs[name]
		if run.Hash != runs["control"].Hash {
			t.Logf("pivot result parity mismatch candidate=%s control=%s candidate_hash=%s", name, runs["control"].Hash, run.Hash)
		}
	}
	queries := map[string]string{"control": control}
	for _, name := range candidateNames {
		queries[name] = candidates[name]
	}
	writePivotTournamentEvidence(t, control, candidates, runs)
	if os.Getenv("LOOM_PIVOT_TOURNAMENT_SKIP_PROFILE") != "" {
		for _, name := range append([]string{"control"}, candidateNames...) {
			run := runs[name]
			t.Logf("pivot %s median=%.6fs bytes=%d hash=%s", name, median(run.Seconds), run.Bytes[0], run.Hash)
		}
		return
	}
	for name, query := range queries {
		profileBinds := binds
		if name == "min_zip_has" {
			profileBinds = mapCandidateBinds
		}
		profile, profileErr := client.Profile(ctx, arangostore.ProfileRequest{Query: query, BindVars: profileBinds, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
		if profileErr != nil {
			t.Fatalf("%s PROFILE: %v", name, profileErr)
		}
		runs[name].Profile = profile
		assessment := arangostore.SummarizeProfile(profile)
		t.Logf("pivot %s median=%.6fs bytes=%d profile_runtime=%.6fs scanned_index=%d scanned_full=%d peak_memory=%d", name, median(runs[name].Seconds), runs[name].Bytes[0], assessment.RuntimeSeconds, assessment.ScannedIndex, assessment.ScannedFull, assessment.PeakMemory)
	}
}

func readPivotColumns(t *testing.T) []string {
	binds := readPivotBinds(t)
	columns, ok := binds["pivot_child_set_6_observation_values_columns"].([]any)
	if !ok {
		t.Fatalf("pivot columns bind has unexpected type %T", binds["pivot_child_set_6_observation_values_columns"])
	}
	result := make([]string, 0, len(columns))
	for _, column := range columns {
		value, ok := column.(string)
		if !ok {
			t.Fatalf("pivot column has unexpected type %T", column)
		}
		result = append(result, value)
	}
	return result
}

func sortedPivotCandidateNames(candidates map[string]string) []string {
	if only := os.Getenv("LOOM_PIVOT_TOURNAMENT_ONLY"); only != "" {
		if _, ok := candidates[only]; ok {
			return []string{only}
		}
	}
	return []string{"min_zip", "min_zip_hash", "min_zip_has", "sorted_unique"}
}

func buildPivotReduction(control, body string) string {
	marker := ", [@__loom_physical_projection_20_name]: MERGE("
	start := strings.Index(control, marker)
	if start < 0 {
		panic("pivot marker not found")
	}
	endRel := strings.Index(control[start:], "\n) }\n")
	if endRel < 0 {
		panic("pivot expression end not found")
	}
	return control[:start] + ", [@__loom_physical_projection_20_name]: " + body + control[start+endRel:]
}

func pivotMinZipBody(hash, hasMap bool) string {
	membership := "POSITION(@pivot_child_set_6_observation_values_columns, __pivot_key)"
	if hasMap {
		membership = "HAS(@pivot_allowed, __pivot_key)"
	}
	method := ""
	if hash {
		method = " OPTIONS { method: \"hash\" }"
	}
	return fmt.Sprintf(`FIRST((
  LET __pivot_pairs = (
    FOR __pivot_item IN child_set_6
      LET __pivot_keys = UNIQUE(__pivot_item.__loom_projection_0)
      LET __pivot_values = __pivot_item.__loom_projection_1
      FILTER LENGTH(__pivot_values) > 0
      FOR __pivot_key IN __pivot_keys
        FILTER %s
        FOR __pivot_value IN __pivot_values
          COLLECT __pivot_group_key = __pivot_key AGGREGATE __pivot_selected = MIN(__pivot_value)%s
          RETURN [__pivot_group_key, __pivot_selected]
  )
  RETURN ZIP(__pivot_pairs[*][0], __pivot_pairs[*][1])
)
`, membership, method)
}

func pivotSortedUniqueZipBody() string {
	return `FIRST((
  LET __pivot_pairs = (
    FOR __pivot_item IN child_set_6
      LET __pivot_keys = UNIQUE(__pivot_item.__loom_projection_0)
      LET __pivot_values = __pivot_item.__loom_projection_1
      FILTER LENGTH(__pivot_values) > 0
      FOR __pivot_key IN __pivot_keys
        FILTER POSITION(@pivot_child_set_6_observation_values_columns, __pivot_key)
        FOR __pivot_value IN __pivot_values
          COLLECT __pivot_group_key = __pivot_key AGGREGATE __pivot_values_sorted = SORTED_UNIQUE(__pivot_value)
          RETURN [__pivot_group_key, FIRST(__pivot_values_sorted)]
  )
  RETURN ZIP(__pivot_pairs[*][0], __pivot_pairs[*][1])
)
`
}

func writePivotTournamentEvidence(t *testing.T, control string, candidates map[string]string, runs map[string]*pivotTournamentMeasurement) {
	_, source, _, _ := runtime.Caller(0)
	directory := filepath.Join(filepath.Dir(source), "..", "..", "docs", "benchmarks", "round4", "tournament_pivot_mini")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create pivot tournament evidence directory: %v", err)
	}
	files := map[string]string{"incumbent.aql": control}
	for name, query := range candidates {
		files[name+".aql"] = query
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o644); err != nil {
			t.Fatalf("write pivot tournament %s: %v", name, err)
		}
	}
	hashes := make(map[string]string, len(candidates))
	for name, query := range candidates {
		hashes[name] = sha256Hex(query)
	}
	payload := map[string]any{
		"incumbent_aql_sha256": sha256Hex(control),
		"candidate_aql_sha256": hashes,
		"runs":                 runs,
		"candidate_names":      sortedPivotCandidateNames(candidates),
		"promotion_threshold":  "10% wall-time or peak-memory improvement with exact result parity",
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("encode pivot tournament evidence: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "RESULTS.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write pivot tournament RESULTS.json: %v", err)
	}
}

func readPivotAQL(t *testing.T) string {
	_, source, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(source), "..", "..", "docs", "benchmarks", "round4", "wp2", "selector-integration-endpoint_typed_selector.aql")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read frozen pivot AQL: %v", err)
	}
	return string(data)
}

func readPivotBinds(t *testing.T) map[string]any {
	_, source, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(source), "..", "..", "docs", "benchmarks", "round3", "wp4", "production.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pivot binds: %v", err)
	}
	var payload struct {
		BindVars map[string]any `json:"bind_vars"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode pivot binds: %v", err)
	}
	return payload.BindVars
}

func buildPivotCandidate(control string) (string, error) {
	marker := ", [@__loom_physical_projection_20_name]: MERGE("
	start := strings.Index(control, marker)
	if start < 0 {
		return "", fmt.Errorf("pivot marker not found")
	}
	endRel := strings.Index(control[start:], "\n) }\n")
	if endRel < 0 {
		return "", fmt.Errorf("pivot expression end not found")
	}
	end := start + endRel
	candidatePivot := `, [@__loom_physical_projection_20_name]: MERGE(
  FOR __pivot_key IN @pivot_child_set_6_observation_values_columns
    LET __pivot_values = SORTED_UNIQUE(FLATTEN((
      FOR __pivot_item IN child_set_6
        FILTER POSITION(__pivot_item.__loom_projection_0, __pivot_key)
        RETURN __pivot_item.__loom_projection_1
    )))
    FILTER LENGTH(__pivot_values) > 0
    RETURN { [__pivot_key]: FIRST(__pivot_values) }
`
	return control[:start] + candidatePivot + control[end:], nil
}

func writePivotEvidence(t *testing.T, control, candidate string, run pivotTournamentRun) {
	_, source, _, _ := runtime.Caller(0)
	directory := filepath.Join(filepath.Dir(source), "..", "..", "docs", "benchmarks", "round4", "tournament_pivot")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create pivot evidence directory: %v", err)
	}
	for name, value := range map[string]string{"control.aql": control, "candidate.aql": candidate} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(value), 0o644); err != nil {
			t.Fatalf("write pivot %s: %v", name, err)
		}
	}
	payload, err := json.MarshalIndent(map[string]any{"control_aql_sha256": sha256Hex(control), "candidate_aql_sha256": sha256Hex(candidate), "run": run, "decision": "pending-coordinator-threshold-review"}, "", "  ")
	if err != nil {
		t.Fatalf("encode pivot evidence: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "evidence.json"), append(payload, '\n'), 0o644); err != nil {
		t.Fatalf("write pivot evidence: %v", err)
	}
}
