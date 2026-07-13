package dataframe

// Round 4 WP8 is deliberately an experiment-only renderer.  It starts from
// the real GraphQL production AQL captured by dataframe-profile and rewrites
// one leaf set's aggregate consumers into a typed summary object.  No
// production physical IR or renderer is changed here.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

type wp8SummaryReport struct {
	SetVariable   string   `json:"set_variable"`
	Fields        []string `json:"aggregate_projection_fields"`
	BeforeLoops   int      `json:"before_aggregate_loops"`
	AfterLoops    int      `json:"after_aggregate_loops"`
	BeforeSorts   int      `json:"before_sorts"`
	AfterSorts    int      `json:"after_sorts"`
	BeforeUnique  int      `json:"before_unique"`
	AfterUnique   int      `json:"after_unique"`
	CandidateHash string   `json:"candidate_aql_hash"`
	ControlHash   string   `json:"control_aql_hash"`
	Decision      string   `json:"decision"`
}

type wp8ProfileArtifact struct {
	ControlHash   string                    `json:"control_result_hash"`
	CandidateHash string                    `json:"candidate_result_hash"`
	ControlWarm   []float64                 `json:"control_warm_seconds"`
	CandidateWarm []float64                 `json:"candidate_warm_seconds"`
	Control       arangostore.ProfileResult `json:"control_profile"`
	Candidate     arangostore.ProfileResult `json:"candidate_profile"`
	Report        wp8SummaryReport          `json:"report"`
}

func TestWP8SummaryPushdownCandidateStructure(t *testing.T) {
	control := wp8ReadProductionAQL(t)
	candidate, report, err := wp8BuildSummaryCandidate(control)
	if err != nil {
		t.Fatal(err)
	}
	report.ControlHash = wp8Hash(control)
	report.CandidateHash = wp8Hash(candidate)
	if report.SetVariable == "" || len(report.Fields) < 2 {
		t.Fatalf("did not find a rich leaf set: %+v", report)
	}
	if !strings.Contains(candidate, "COLLECT AGGREGATE") {
		t.Fatalf("candidate has no typed summary aggregation:\n%s", candidate)
	}
	if report.AfterLoops >= report.BeforeLoops {
		t.Fatalf("summary did not remove aggregate loops: %+v", report)
	}
	if !strings.Contains(candidate, "representative_files_limit") && !strings.Contains(candidate, "representative_diagnoses_limit") && !strings.Contains(candidate, "representative_samples_limit") {
		t.Fatalf("candidate unexpectedly removed all representative slices")
	}
	t.Logf("WP8 structural summary candidate: %+v", report)
}

// TestWP8SummaryPushdownProfilesGDC is opt-in. It consumes the exact AQL
// generated from examples/meta_gdc_case_matrix.variables.json (production.json
// records that BuilderFromInput path) and alternates control/candidate runs.
func TestWP8SummaryPushdownProfilesGDC(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run WP8 against Arango")
	}
	control := wp8ReadProductionAQL(t)
	candidate, report, err := wp8BuildSummaryCandidate(control)
	if err != nil {
		t.Fatal(err)
	}
	bindVars := wp8ReadProductionBinds(t)
	url, database, _ := compilerArangoTarget()
	client, err := arangostore.Open(context.Background(), url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	controlRows, controlHash, controlBytes, controlTimes := wp8RunAlternating(ctx, client, control, candidate, bindVars, true)
	candidateRows, candidateHash, candidateBytes, candidateTimes := wp8RunAlternating(ctx, client, control, candidate, bindVars, false)
	if controlRows != candidateRows || controlHash != candidateHash {
		t.Fatalf("summary result parity mismatch control rows/hash=%d/%s candidate=%d/%s", controlRows, controlHash, candidateRows, candidateHash)
	}
	controlProfile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: control, BindVars: bindVars, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		t.Fatal(err)
	}
	candidateProfile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: candidate, BindVars: bindVars, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		t.Fatal(err)
	}
	report.ControlHash = wp8Hash(control)
	report.CandidateHash = wp8Hash(candidate)
	artifact := wp8ProfileArtifact{ControlHash: controlHash, CandidateHash: candidateHash, ControlWarm: controlTimes, CandidateWarm: candidateTimes, Control: controlProfile, Candidate: candidateProfile, Report: report}
	if os.Getenv("LOOM_WP8_WRITE_ARTIFACTS") != "" {
		wp8WriteArtifacts(t, control, candidate, artifact)
	}
	t.Logf("WP8 report=%+v control_rows=%d candidate_rows=%d control_bytes=%d candidate_bytes=%d control_times=%v candidate_times=%v", report, controlRows, candidateRows, controlBytes, candidateBytes, controlTimes, candidateTimes)
}

func wp8ReadProductionAQL(t *testing.T) string {
	_, source, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(source), "..", "..", "docs", "benchmarks", "round3", "wp4", "production.aql")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read production AQL %q: %v", path, err)
	}
	return string(data)
}

func wp8ReadProductionBinds(t *testing.T) map[string]any {
	_, source, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(source), "..", "..", "docs", "benchmarks", "round3", "wp4", "production.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read production profile %q: %v", path, err)
	}
	var payload struct {
		BindVars map[string]any `json:"bind_vars"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode production profile: %v", err)
	}
	return payload.BindVars
}

func wp8BuildSummaryCandidate(control string) (string, wp8SummaryReport, error) {
	// Locate the richest leaf set structurally. The regex only recognizes
	// renderer-owned set syntax and never names a FHIR type or example alias.
	setRE := regexp.MustCompile(`(?ms)^  LET ([A-Za-z0-9_]+) = UNIQUE\(\(.*?^ {2,}\)\)\n`)
	blocks := setRE.FindAllStringSubmatchIndex(control, -1)
	if len(blocks) == 0 {
		return "", wp8SummaryReport{}, fmt.Errorf("no child set materialization found")
	}
	fieldRE := regexp.MustCompile(`__loom_projection_[0-9]+`)
	chosen := blocks[0]
	chosenFields := fieldRE.FindAllString(control[chosen[0]:chosen[1]], -1)
	for _, block := range blocks[1:] {
		fields := fieldRE.FindAllString(control[block[0]:block[1]], -1)
		if len(fields) > len(chosenFields) {
			chosen, chosenFields = block, fields
		}
	}
	setVariable := control[chosen[2]:chosen[3]]
	// Keep one occurrence of each projection field and only select fields used
	// by aggregate loops in RETURN. Slice projections remain independent and
	// retain their exact sort-before-limit semantics.
	seen := map[string]bool{}
	fields := make([]string, 0, len(chosenFields))
	returnStart := strings.LastIndex(control, "\nRETURN ")
	if returnStart < 0 {
		return "", wp8SummaryReport{}, fmt.Errorf("production AQL has no RETURN")
	}
	returnText := control[returnStart:]
	for _, field := range chosenFields {
		if seen[field] || !strings.Contains(returnText, "FOR __loom_prepared_value IN "+setVariable+" RETURN __loom_prepared_value."+field) {
			continue
		}
		seen[field] = true
		fields = append(fields, field)
	}
	if len(fields) < 2 {
		return "", wp8SummaryReport{}, fmt.Errorf("rich set %q has fewer than two aggregate selector fields: candidates=%v return=%s", setVariable, chosenFields, returnText)
	}
	sort.Strings(fields)
	summaryVariable := "__loom_summary_" + setVariable
	parts := make([]string, 0, len(fields))
	outputs := make([]string, 0, len(fields)+1)
	for index, field := range fields {
		name := fmt.Sprintf("values_%d", index)
		parts = append(parts, fmt.Sprintf("      __loom_summary_%d = UNIQUE(__loom_summary_item.%s)", index, field))
		outputs = append(outputs, fmt.Sprintf("%s: SORTED_UNIQUE(FLATTEN(__loom_summary_%d))", name, index))
	}
	summary := fmt.Sprintf("  LET %s = FIRST((\n    FOR __loom_summary_item IN %s\n      COLLECT AGGREGATE\n        __loom_summary_count = COUNT(),\n%s\n      RETURN { count: __loom_summary_count, %s }\n  )) || { count: 0, %s }\n", summaryVariable, setVariable, strings.Join(parts, ",\n"), strings.Join(outputs, ", "), strings.Join(outputs, ", "))
	// Inject immediately after the selected set's materialization. The source
	// set remains unchanged, so scope, identity, ordering, and optional roots
	// are untouched; only rich aggregate reads are redirected.
	candidate := control[:chosen[1]] + summary + control[chosen[1]:]
	candidateReturnStart := strings.LastIndex(candidate, "\nRETURN ")
	candidateReturn := candidate[candidateReturnStart:]
	candidateReturn = strings.ReplaceAll(candidateReturn, "LENGTH("+setVariable+")", summaryVariable+".count")
	for index, field := range fields {
		old := "SORTED_UNIQUE(FLATTEN((FOR __loom_prepared_value IN " + setVariable + " RETURN __loom_prepared_value." + field + ")))"
		newValue := fmt.Sprintf("%s.values_%d", summaryVariable, index)
		candidateReturn = strings.ReplaceAll(candidateReturn, old, newValue)
	}
	candidate = candidate[:candidateReturnStart] + candidateReturn
	report := wp8SummaryReport{SetVariable: setVariable, Fields: fields, BeforeLoops: strings.Count(controlReturnText(control), "FOR __loom_prepared_value IN "+setVariable), AfterLoops: strings.Count(controlReturnText(candidate), "FOR __loom_prepared_value IN "+setVariable), BeforeSorts: strings.Count(control, "\n  SORT "), AfterSorts: strings.Count(candidate, "\n  SORT "), BeforeUnique: strings.Count(control, "UNIQUE("), AfterUnique: strings.Count(candidate, "UNIQUE(")}
	return candidate, report, nil
}

func controlReturnText(aql string) string {
	if index := strings.LastIndex(aql, "\nRETURN "); index >= 0 {
		return aql[index:]
	}
	return aql
}

func wp8Hash(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func wp8RunAlternating(ctx context.Context, client *arangostore.Client, control, candidate string, binds map[string]any, runControl bool) (int, string, int, []float64) {
	times := make([]float64, 0, 5)
	rows := 0
	bytes := 0
	hash := sha256.New()
	for run := 0; run < 5; run++ {
		query := candidate
		if runControl {
			query = control
		}
		started := time.Now()
		_ = client.QueryRows(ctx, query, 10000, binds, func(row map[string]any) error {
			rows++
			encoded, _ := json.Marshal(row)
			bytes += len(encoded)
			_, _ = hash.Write(encoded)
			_, _ = hash.Write([]byte{'\n'})
			return nil
		})
		times = append(times, time.Since(started).Seconds())
	}
	return rows / 5, hex.EncodeToString(hash.Sum(nil)), bytes / 5, times
}

func wp8WriteArtifacts(t *testing.T, control, candidate string, artifact wp8ProfileArtifact) {
	_, source, _, _ := runtime.Caller(0)
	directory := filepath.Join(filepath.Dir(source), "..", "..", "docs", "benchmarks", "round4", "wp8")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "control.aql"), []byte(control), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "candidate.aql"), []byte(candidate), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "evidence.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
