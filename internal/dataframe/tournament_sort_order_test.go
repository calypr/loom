package dataframe_test

// Round 4 sort/order tournament. This is deliberately an AQL-only experiment
// over the current endpoint+typed-selector production shape; no optimizer or
// renderer code is changed here.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

type sortOrderRun struct {
	ControlSeconds   []float64                 `json:"control_seconds"`
	CandidateSeconds []float64                 `json:"candidate_seconds"`
	ControlBytes     []int                     `json:"control_bytes"`
	CandidateBytes   []int                     `json:"candidate_bytes"`
	ControlHash      string                    `json:"control_result_sha256"`
	CandidateHash    string                    `json:"candidate_result_sha256"`
	ControlProfile   arangostore.ProfileResult `json:"control_profile"`
	CandidateProfile arangostore.ProfileResult `json:"candidate_profile"`
}

func TestSortOrderCandidateStructure(t *testing.T) {
	control := readSortOrderAQL(t)
	candidate, removed, err := buildSortOrderCandidate(control)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 6 {
		t.Fatalf("removed sort operations = %d, want 6 (three child sorts plus three duplicate slice keys)", removed)
	}
	for _, fragment := range []string{
		"SORT child_set_1_node._key",
		"SORT child_set_2_item._key",
		"SORT child_set_3_node._key",
		"SORT __loom_physical_slice_item._key ASC\n",
		"SORT __loom_physical_slice_item_1._key ASC\n",
		"SORT __loom_physical_slice_item_2._key ASC\n",
	} {
		if !strings.Contains(candidate, fragment) {
			t.Fatalf("candidate removed required order fragment %q:\n%s", fragment, candidate)
		}
	}
	if strings.Contains(candidate, "SORT child_set_4_node._key") || strings.Contains(candidate, "SORT child_set_5_node._key") || strings.Contains(candidate, "SORT child_set_6_item._key") {
		t.Fatalf("candidate retained an order-insensitive child sort")
	}
	if strings.Contains(candidate, "ASC, __loom_physical_slice_item._key ASC") {
		t.Fatalf("candidate retained duplicate slice sort key")
	}
	t.Logf("sort/order candidate removed=%d control_hash=%s candidate_hash=%s", removed, sha256Hex(control), sha256Hex(candidate))
}

func TestSortOrderCandidateProfilesActualGDC(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run sort/order tournament")
	}
	control := readSortOrderAQL(t)
	candidate, removed, err := buildSortOrderCandidate(control)
	if err != nil {
		t.Fatal(err)
	}
	url, database, _ := compilerArangoTarget()
	client, err := arangostore.Open(context.Background(), url, database)
	if err != nil {
		t.Fatalf("open Arango: %v", err)
	}
	defer client.Close(context.Background())
	binds := readSortOrderBinds(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	run := sortOrderRun{}
	for index := 0; index < 5; index++ {
		controlQuery, controlBinds := cacheBust(control, binds, 67000+index*2)
		candidateQuery, candidateBinds := cacheBust(candidate, binds, 67001+index*2)
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
		t.Fatalf("sort/order result parity mismatch control=%s candidate=%s", run.ControlHash, run.CandidateHash)
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
		t.Fatalf("sort/order PROFILE result parity mismatch")
	}
	run.ControlProfile = controlProfile
	run.CandidateProfile = candidateProfile
	t.Logf("sort/order removed=%d control_median=%.6f candidate_median=%.6f control_profile=%+v candidate_profile=%+v", removed, median(run.ControlSeconds), median(run.CandidateSeconds), arangostore.SummarizeProfile(controlProfile), arangostore.SummarizeProfile(candidateProfile))
	writeSortOrderEvidence(t, control, candidate, run, removed)
}

func readSortOrderAQL(t *testing.T) string {
	_, source, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(source), "..", "..", "docs", "benchmarks", "round4", "wp2", "selector-integration-endpoint_typed_selector.aql")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read frozen endpoint+typed-selector AQL %q: %v", path, err)
	}
	return string(data)
}

func readSortOrderBinds(t *testing.T) map[string]any {
	_, source, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(source), "..", "..", "docs", "benchmarks", "round3", "wp4", "production.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read frozen production binds %q: %v", path, err)
	}
	var payload struct {
		BindVars map[string]any `json:"bind_vars"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode frozen production binds: %v", err)
	}
	return payload.BindVars
}

func buildSortOrderCandidate(control string) (string, int, error) {
	candidate := control
	removed := 0
	for _, line := range []string{
		"        SORT child_set_4_node._key\n",
		"        SORT child_set_5_node._key\n",
		"      SORT child_set_6_item._key\n",
	} {
		if !strings.Contains(candidate, line) {
			return "", removed, nilError("required order-insensitive sort not found: " + strings.TrimSpace(line))
		}
		candidate = strings.Replace(candidate, line, "", 1)
		removed++
	}
	for _, name := range []string{"__loom_physical_slice_item", "__loom_physical_slice_item_1", "__loom_physical_slice_item_2"} {
		old := "SORT " + name + "._key ASC, " + name + "._key ASC\n"
		if !strings.Contains(candidate, old) {
			return "", removed, nilError("duplicate slice sort not found: " + name)
		}
		candidate = strings.Replace(candidate, old, "SORT "+name+"._key ASC\n", 1)
		removed++
	}
	return candidate, removed, nil
}

type sortOrderError string

func (e sortOrderError) Error() string { return string(e) }
func nilError(message string) error    { return sortOrderError(message) }

func writeSortOrderEvidence(t *testing.T, control, candidate string, run sortOrderRun, removed int) {
	_, source, _, _ := runtime.Caller(0)
	directory := filepath.Join(filepath.Dir(source), "..", "..", "docs", "benchmarks", "round4", "tournament_sort_order")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create sort/order evidence directory: %v", err)
	}
	for name, value := range map[string]string{"control.aql": control, "candidate.aql": candidate} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(value), 0o644); err != nil {
			t.Fatalf("write sort/order %s: %v", name, err)
		}
	}
	payload, err := json.MarshalIndent(map[string]any{"control_aql_sha256": sha256Hex(control), "candidate_aql_sha256": sha256Hex(candidate), "removed_sort_operations": removed, "run": run, "decision": "pending-coordinator-threshold-review"}, "", "  ")
	if err != nil {
		t.Fatalf("encode sort/order evidence: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "evidence.json"), append(payload, '\n'), 0o644); err != nil {
		t.Fatalf("write sort/order evidence: %v", err)
	}
}
