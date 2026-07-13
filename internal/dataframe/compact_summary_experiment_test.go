package dataframe_test

// Read-only candidate E tournament. The incumbent is the frozen current
// root-endpoint plus selector-lowering AQL. The candidate adds one compact
// summary for a structurally selected leaf set with two distinct-value
// consumers and a representative slice. No production compiler files are
// touched.

import (
	"context"
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

const compactSummaryIncumbentSHA = "2c5c598d96f161ac74129b532d2d05d8933a348d3032666d5b5262b7a654704d"

type compactSummaryRun struct {
	Name          string                        `json:"name"`
	QuerySHA256   string                        `json:"query_sha256"`
	ResultSHA256  string                        `json:"result_sha256"`
	Rows          int                           `json:"rows"`
	Bytes         []int                         `json:"bytes"`
	WarmSeconds   []float64                     `json:"warm_seconds"`
	MedianSeconds float64                       `json:"median_seconds"`
	MinSeconds    float64                       `json:"min_seconds"`
	Explain       arangostore.ExplainAssessment `json:"explain"`
	Profile       compactSummaryProfile         `json:"profile"`
	RawProfile    arangostore.ProfileResult     `json:"-"`
}

type compactSummaryProfile struct {
	ScannedFull     int                              `json:"scanned_full"`
	ScannedIndex    int                              `json:"scanned_index"`
	PeakMemoryBytes uint64                           `json:"peak_memory_bytes"`
	Phases          arangostore.ProfilePhases        `json:"phases"`
	TopNodes        []arangostore.ProfileNodeSummary `json:"top_nodes"`
}

type compactSummaryRewrite struct {
	SetVariable string   `json:"set_variable"`
	Fields      []string `json:"fields"`
	BeforeLoops int      `json:"before_loops"`
	AfterLoops  int      `json:"after_loops"`
}

func TestCompactSummaryCandidateStructure(t *testing.T) {
	incumbent, _ := loadCompactSummaryIncumbent(t)
	candidate, rewrite, err := buildCompactSummaryCandidate(incumbent)
	if err != nil {
		t.Fatal(err)
	}
	if len(rewrite.Fields) < 2 || rewrite.BeforeLoops < 2 || rewrite.AfterLoops >= rewrite.BeforeLoops {
		t.Fatalf("summary did not select a rich leaf or remove consumers: %+v", rewrite)
	}
	if !strings.Contains(candidate, "COLLECT AGGREGATE") {
		t.Fatalf("candidate omitted typed summary aggregation")
	}
	if !strings.Contains(candidate, "representative_files_limit") && !strings.Contains(candidate, "representative_samples_limit") && !strings.Contains(candidate, "representative_diagnoses_limit") {
		t.Fatalf("candidate removed representative slice")
	}
	t.Logf("summary candidate bindings:\n%s", compactSummaryLines(candidate))
	t.Logf("compact summary structure: %+v", rewrite)
}

func compactSummaryLines(query string) string {
	lines := strings.Split(query, "\n")
	selected := make([]string, 0, 20)
	for _, line := range lines {
		if strings.Contains(line, "__loom_summary") || strings.Contains(line, "__loom_physical_projection_10_name") || strings.Contains(line, "__loom_physical_projection_11_name") || strings.Contains(line, "__loom_physical_projection_12_name") {
			selected = append(selected, line)
		}
	}
	return strings.Join(selected, "\n")
}

func TestCompactSummaryProfilesAgainstCurrentIncumbent(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run compact summary tournament against Arango")
	}
	incumbent, binds := loadCompactSummaryIncumbent(t)
	candidate, rewrite, err := buildCompactSummaryCandidate(incumbent)
	if err != nil {
		t.Fatal(err)
	}
	url, database, _ := compilerArangoTarget()
	client, err := arangostore.Open(context.Background(), url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	control, candidateRun, err := runCompactSummaryAlternating(ctx, client, incumbent, candidate, binds)
	if err != nil {
		t.Fatal(err)
	}
	if control.ResultSHA256 != candidateRun.ResultSHA256 {
		t.Fatalf("summary result parity mismatch control=%s candidate=%s", control.ResultSHA256, candidateRun.ResultSHA256)
	}
	if candidateRun.Profile.PeakMemoryBytes > 225000000 {
		t.Fatalf("summary candidate exceeds 225 MB gate: %d", candidateRun.Profile.PeakMemoryBytes)
	}
	if candidateRun.MedianSeconds >= control.MedianSeconds*0.95 {
		t.Logf("compact summary rejected: control=%.6fs candidate=%.6fs improvement=%.2f%% rewrite=%+v", control.MedianSeconds, candidateRun.MedianSeconds, 100*(control.MedianSeconds-candidateRun.MedianSeconds)/control.MedianSeconds, rewrite)
	} else {
		t.Logf("compact summary passes runtime gate: control=%.6fs candidate=%.6fs improvement=%.2f%% rewrite=%+v", control.MedianSeconds, candidateRun.MedianSeconds, 100*(control.MedianSeconds-candidateRun.MedianSeconds)/control.MedianSeconds, rewrite)
	}
	writeCompactSummaryArtifacts(t, incumbent, candidate, binds, rewrite, control, candidateRun)
}

func loadCompactSummaryIncumbent(t *testing.T) (string, map[string]any) {
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(source), "..", "..")
	queryBytes, err := os.ReadFile(filepath.Join(root, "docs", "benchmarks", "round4", "tournament_root_endpoint", "candidate.aql"))
	if err != nil {
		t.Fatalf("read compact-summary incumbent: %v", err)
	}
	query := strings.TrimSuffix(strings.TrimSuffix(string(queryBytes), "\n"), "\r")
	if sha256Hex(query) != compactSummaryIncumbentSHA {
		t.Fatalf("compact-summary incumbent hash changed: got %s want %s", sha256Hex(query), compactSummaryIncumbentSHA)
	}
	reportBytes, err := os.ReadFile(filepath.Join(root, "docs", "benchmarks", "round4", "wp2", "integrated.json"))
	if err != nil {
		t.Fatalf("read compact-summary bind report: %v", err)
	}
	var report struct {
		BindVars map[string]any `json:"bind_vars"`
	}
	if err := json.Unmarshal(reportBytes, &report); err != nil {
		t.Fatalf("decode compact-summary bind report: %v", err)
	}
	return query, report.BindVars
}

var compactSetRE = regexp.MustCompile(`(?ms)^  LET ([A-Za-z0-9_]+) = UNIQUE\(\(.*?^ {2,}\)\)\n`)
var compactConsumerRE = regexp.MustCompile(`SORTED_UNIQUE\(FLATTEN\(\(FOR __loom_prepared_value IN ([A-Za-z0-9_]+) RETURN __loom_prepared_value\.([A-Za-z0-9_]+)\)\)\)`)

func buildCompactSummaryCandidate(control string) (string, compactSummaryRewrite, error) {
	blocks := compactSetRE.FindAllStringSubmatchIndex(control, -1)
	if len(blocks) == 0 {
		return "", compactSummaryRewrite{}, fmt.Errorf("no child-set materializations found")
	}
	returnStart := strings.LastIndex(control, "\nRETURN ")
	if returnStart < 0 {
		return "", compactSummaryRewrite{}, fmt.Errorf("no root RETURN found")
	}
	returnText := control[returnStart:]
	chosenSet := ""
	fieldSet := map[string]bool{}
	for _, block := range blocks {
		setVariable := control[block[2]:block[3]]
		for _, match := range compactConsumerRE.FindAllStringSubmatch(returnText, -1) {
			if match[1] == setVariable {
				fieldSet[match[2]] = true
			}
		}
		if len(fieldSet) >= 2 {
			chosenSet = setVariable
			break
		}
		fieldSet = map[string]bool{}
	}
	if chosenSet == "" {
		return "", compactSummaryRewrite{}, fmt.Errorf("no leaf set has two distinct-value consumers")
	}
	fields := make([]string, 0, len(fieldSet))
	for field := range fieldSet {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	parts := make([]string, 0, len(fields))
	outputs := make([]string, 0, len(fields))
	defaults := make([]string, 0, len(fields))
	for index, field := range fields {
		parts = append(parts, fmt.Sprintf("      __loom_summary_%d = UNIQUE(__loom_summary_item.%s)", index, field))
		outputs = append(outputs, fmt.Sprintf("values_%d: SORTED_UNIQUE(FLATTEN(__loom_summary_%d))", index, index))
		defaults = append(defaults, fmt.Sprintf("values_%d: []", index))
	}
	summaryVariable := "__loom_summary_" + chosenSet
	summary := fmt.Sprintf("  LET %s = FIRST((\n    FOR __loom_summary_item IN %s\n      COLLECT AGGREGATE\n        __loom_summary_count = COUNT(),\n%s\n      RETURN { count: __loom_summary_count, %s }\n  )) || { count: 0, %s }\n", summaryVariable, chosenSet, strings.Join(parts, ",\n"), strings.Join(outputs, ", "), strings.Join(defaults, ", "))
	chosenBlock := -1
	for index, block := range blocks {
		if control[block[2]:block[3]] == chosenSet {
			chosenBlock = index
			break
		}
	}
	if chosenBlock < 0 {
		return "", compactSummaryRewrite{}, fmt.Errorf("selected set block disappeared")
	}
	insertAt := blocks[chosenBlock][1]
	candidate := control[:insertAt] + summary + control[insertAt:]
	candidateReturnStart := strings.LastIndex(candidate, "\nRETURN ")
	candidateReturn := candidate[candidateReturnStart:]
	candidateReturn = strings.ReplaceAll(candidateReturn, "LENGTH("+chosenSet+")", summaryVariable+".count")
	for index, field := range fields {
		old := "SORTED_UNIQUE(FLATTEN((FOR __loom_prepared_value IN " + chosenSet + " RETURN __loom_prepared_value." + field + ")))"
		candidateReturn = strings.ReplaceAll(candidateReturn, old, fmt.Sprintf("%s.values_%d", summaryVariable, index))
	}
	candidate = candidate[:candidateReturnStart] + candidateReturn
	return candidate, compactSummaryRewrite{SetVariable: chosenSet, Fields: fields, BeforeLoops: strings.Count(returnText, "FOR __loom_prepared_value IN "+chosenSet), AfterLoops: strings.Count(candidateReturn, "FOR __loom_prepared_value IN "+chosenSet)}, nil
}

func runCompactSummaryAlternating(ctx context.Context, client *arangostore.Client, control, candidate string, baseBinds map[string]any) (compactSummaryRun, compactSummaryRun, error) {
	controlRun := compactSummaryRun{Name: "endpoint_selector_incumbent", QuerySHA256: sha256Hex(control)}
	candidateRun := compactSummaryRun{Name: "compact_leaf_summary", QuerySHA256: sha256Hex(candidate)}
	for i := 0; i < 5; i++ {
		controlQuery, controlBinds := cacheBust(control, baseBinds, 15000+i)
		candidateQuery, candidateBinds := cacheBust(candidate, baseBinds, 16000+i)
		controlSeconds, controlBytes, controlHash, err := executeOrdinary(ctx, client, controlQuery, controlBinds)
		if err != nil {
			return controlRun, candidateRun, fmt.Errorf("control run %d: %w", i+1, err)
		}
		candidateSeconds, candidateBytes, candidateHash, err := executeOrdinary(ctx, client, candidateQuery, candidateBinds)
		if err != nil {
			return controlRun, candidateRun, fmt.Errorf("candidate run %d: %w", i+1, err)
		}
		controlRun.WarmSeconds = append(controlRun.WarmSeconds, controlSeconds)
		controlRun.Bytes = append(controlRun.Bytes, controlBytes)
		controlRun.ResultSHA256 = controlHash
		controlRun.Rows = 1000
		candidateRun.WarmSeconds = append(candidateRun.WarmSeconds, candidateSeconds)
		candidateRun.Bytes = append(candidateRun.Bytes, candidateBytes)
		candidateRun.ResultSHA256 = candidateHash
		candidateRun.Rows = 1000
	}
	controlRun.MedianSeconds, controlRun.MinSeconds = median(controlRun.WarmSeconds), minCompact(controlRun.WarmSeconds)
	candidateRun.MedianSeconds, candidateRun.MinSeconds = median(candidateRun.WarmSeconds), minCompact(candidateRun.WarmSeconds)
	controlProfileQuery, controlProfileBinds := cacheBust(control, baseBinds, 17001)
	candidateProfileQuery, candidateProfileBinds := cacheBust(candidate, baseBinds, 17002)
	controlProfile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: controlProfileQuery, BindVars: controlProfileBinds, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		return controlRun, candidateRun, err
	}
	candidateProfile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: candidateProfileQuery, BindVars: candidateProfileBinds, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		return controlRun, candidateRun, err
	}
	controlRun.Explain, err = explainCompact(ctx, client, control, baseBinds)
	if err != nil {
		return controlRun, candidateRun, err
	}
	candidateRun.Explain, err = explainCompact(ctx, client, candidate, baseBinds)
	if err != nil {
		return controlRun, candidateRun, err
	}
	controlRun.RawProfile, candidateRun.RawProfile = controlProfile, candidateProfile
	controlRun.Profile, candidateRun.Profile = summarizeCompact(controlProfile), summarizeCompact(candidateProfile)
	return controlRun, candidateRun, nil
}

func explainCompact(ctx context.Context, client *arangostore.Client, query string, binds map[string]any) (arangostore.ExplainAssessment, error) {
	explain, err := client.Explain(ctx, arangostore.ExplainRequest{Query: query, BindVars: binds})
	if err != nil {
		return arangostore.ExplainAssessment{}, err
	}
	assessment := arangostore.AssessExplainResult(explain)
	if len(assessment.FullCollectionScans) != 0 {
		return assessment, fmt.Errorf("summary candidate introduced full scans: %#v", assessment.FullCollectionScans)
	}
	return assessment, nil
}

func summarizeCompact(profile arangostore.ProfileResult) compactSummaryProfile {
	summary := arangostore.SummarizeProfile(profile)
	nodes := append([]arangostore.ProfileNodeSummary(nil), summary.Nodes...)
	if len(nodes) > 20 {
		nodes = nodes[:20]
	}
	return compactSummaryProfile{ScannedFull: summary.ScannedFull, ScannedIndex: summary.ScannedIndex, PeakMemoryBytes: summary.PeakMemory, Phases: profile.Extra.Profile, TopNodes: nodes}
}

func minCompact(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	minimum := values[0]
	for _, value := range values[1:] {
		if value < minimum {
			minimum = value
		}
	}
	return minimum
}

func writeCompactSummaryArtifacts(t *testing.T, incumbent, candidate string, binds map[string]any, rewrite compactSummaryRewrite, control, candidateRun compactSummaryRun) {
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(source), "..", "..")
	directory := filepath.Join(root, "docs", "benchmarks", "round4", "tournament_summaries")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, query := range map[string]string{"incumbent.aql": incumbent, "candidate.aql": candidate} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(query+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, profile := range map[string]arangostore.ProfileResult{"incumbent.profile.json": control.RawProfile, "candidate.profile.json": candidateRun.RawProfile} {
		encoded, err := json.MarshalIndent(profile, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, name), append(encoded, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	payload := map[string]any{"incumbent": control, "candidate": candidateRun, "bind_vars": binds, "rewrite": rewrite}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "RESULTS.json"), append(encoded, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
