package dataframe_test

// Opt-in, read-only tournament for the broad root sibling hop. The incumbent
// is the current endpoint+typed-selector AQL artifact; this test rewrites only
// its root depth-one traversal and never edits compiler or index definitions.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

const incumbentSelectorAQLSHA = "ad8542e8f158d6443b047c63f5cbffd54264c6d2c63fa5bfbbfe87d84e0fa79d"

type rootEndpointRun struct {
	Name          string                        `json:"name"`
	QuerySHA256   string                        `json:"query_sha256"`
	ResultSHA256  string                        `json:"result_sha256"`
	Rows          int                           `json:"rows"`
	Bytes         []int                         `json:"bytes"`
	WarmSeconds   []float64                     `json:"warm_seconds"`
	MedianSeconds float64                       `json:"median_seconds"`
	MinSeconds    float64                       `json:"min_seconds"`
	Explain       arangostore.ExplainAssessment `json:"explain"`
	Profile       rootEndpointProfile           `json:"profile"`
	RawProfile    arangostore.ProfileResult     `json:"-"`
}

type rootEndpointProfile struct {
	ScannedFull     int                              `json:"scanned_full"`
	ScannedIndex    int                              `json:"scanned_index"`
	PeakMemoryBytes uint64                           `json:"peak_memory_bytes"`
	Phases          arangostore.ProfilePhases        `json:"phases"`
	TopNodes        []arangostore.ProfileNodeSummary `json:"top_nodes"`
}

func TestRootEndpointCandidateRendersFromFrozenIncumbent(t *testing.T) {
	incumbent, binds := loadFrozenIncumbent(t)
	candidate, rewrite, err := rewriteRootEndpoint(incumbent)
	if err != nil {
		t.Fatal(err)
	}
	if rewrite.Direction != "INBOUND" || rewrite.Endpoint != "_to" || rewrite.TargetEndpoint != "_from" {
		t.Fatalf("unexpected root rewrite metadata: %#v", rewrite)
	}
	if !strings.Contains(candidate, "FILTER "+rewrite.Edge+"._to == root._id") || !strings.Contains(candidate, "DOCUMENT("+rewrite.Edge+"._from)") {
		t.Fatalf("candidate omitted endpoint equality/document lookup:\n%s", candidate[:minRootLen(len(candidate), 5000)])
	}
	if strings.Contains(candidate, "IN 1..1 INBOUND root @@") {
		t.Fatalf("candidate retained native root traversal")
	}
	if _, ok := binds["@root_collection"]; !ok {
		t.Fatalf("frozen incumbent bind variables lost root collection bind")
	}
}

func TestRootEndpointProfilesAgainstFrozenIncumbent(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run root endpoint tournament against Arango")
	}
	incumbent, binds := loadFrozenIncumbent(t)
	candidate, rewrite, err := rewriteRootEndpoint(incumbent)
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
	control, candidateRun, err := runRootEndpointAlternating(ctx, client, incumbent, candidate, binds)
	if err != nil {
		t.Fatal(err)
	}
	if control.ResultSHA256 != candidateRun.ResultSHA256 {
		t.Fatalf("result parity mismatch control=%s candidate=%s", control.ResultSHA256, candidateRun.ResultSHA256)
	}
	if candidateRun.MedianSeconds >= control.MedianSeconds {
		t.Logf("root endpoint candidate rejected for regression: control=%.6fs candidate=%.6fs", control.MedianSeconds, candidateRun.MedianSeconds)
	} else {
		t.Logf("root endpoint candidate improvement=%.2f%% control=%.6fs candidate=%.6fs direction=%s endpoint=%s target_endpoint=%s", 100*(control.MedianSeconds-candidateRun.MedianSeconds)/control.MedianSeconds, control.MedianSeconds, candidateRun.MedianSeconds, rewrite.Direction, rewrite.Endpoint, rewrite.TargetEndpoint)
	}
	writeRootEndpointArtifacts(t, incumbent, candidate, binds, rewrite, control, candidateRun)
}

type rootEndpointRewrite struct {
	Node           string `json:"node"`
	Edge           string `json:"edge"`
	Direction      string `json:"direction"`
	Endpoint       string `json:"endpoint"`
	TargetEndpoint string `json:"target_endpoint"`
	Collection     string `json:"collection"`
}

var rootTraversalHeaderRE = regexp.MustCompile(`(?m)^(\s*)FOR ([A-Za-z_][A-Za-z0-9_]*_node), ([A-Za-z_][A-Za-z0-9_]*_edge) IN 1\.\.1 (INBOUND|OUTBOUND) root (@@[A-Za-z_][A-Za-z0-9_]*_edge_collection)\n`)

func rewriteRootEndpoint(query string) (string, rootEndpointRewrite, error) {
	matches := rootTraversalHeaderRE.FindAllStringSubmatchIndex(query, -1)
	if len(matches) != 1 {
		return query, rootEndpointRewrite{}, fmt.Errorf("expected one root depth-one traversal, found %d", len(matches))
	}
	m := matches[0]
	indent := query[m[2]:m[3]]
	node := query[m[4]:m[5]]
	edge := query[m[6]:m[7]]
	direction := query[m[8]:m[9]]
	collection := query[m[10]:m[11]]
	endpoint, target := "_to", "_from"
	if direction == "OUTBOUND" {
		endpoint, target = "_from", "_to"
	}
	replacement := indent + "FOR " + edge + " IN " + collection + "\n" +
		indent + "  FILTER " + edge + "." + endpoint + " == root._id\n" +
		indent + "  LET " + node + " = DOCUMENT(" + edge + "." + target + ")\n"
	return query[:m[0]] + replacement + query[m[1]:], rootEndpointRewrite{Node: node, Edge: edge, Direction: direction, Endpoint: endpoint, TargetEndpoint: target, Collection: collection}, nil
}

func loadFrozenIncumbent(t *testing.T) (string, map[string]any) {
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(source), "..", "..")
	queryPath := filepath.Join(root, "docs", "benchmarks", "round4", "wp2_selector_combo", "candidate.aql")
	reportPath := filepath.Join(root, "docs", "benchmarks", "round4", "wp2", "integrated.json")
	queryBytes, err := os.ReadFile(queryPath)
	if err != nil {
		t.Fatalf("read incumbent selector AQL: %v", err)
	}
	query := strings.TrimSuffix(strings.TrimSuffix(string(queryBytes), "\n"), "\r")
	if sha256Hex(query) != incumbentSelectorAQLSHA {
		t.Fatalf("incumbent selector AQL hash changed: got %s want %s", sha256Hex(query), incumbentSelectorAQLSHA)
	}
	reportBytes, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read incumbent bind report: %v", err)
	}
	var report struct {
		BindVars map[string]any `json:"bind_vars"`
	}
	if err := json.Unmarshal(reportBytes, &report); err != nil {
		t.Fatalf("decode incumbent bind report: %v", err)
	}
	return query, report.BindVars
}

func runRootEndpointAlternating(ctx context.Context, client *arangostore.Client, incumbent, candidate string, baseBinds map[string]any) (rootEndpointRun, rootEndpointRun, error) {
	control := rootEndpointRun{Name: "incumbent_endpoint_selector", QuerySHA256: sha256Hex(incumbent)}
	candidateRun := rootEndpointRun{Name: "root_endpoint_endpoint_selector", QuerySHA256: sha256Hex(candidate)}
	for i := 0; i < 5; i++ {
		controlQuery, controlBinds := cacheBust(incumbent, baseBinds, 12000+i)
		candidateQuery, candidateBinds := cacheBust(candidate, baseBinds, 13000+i)
		controlSeconds, controlBytes, controlHash, err := executeOrdinary(ctx, client, controlQuery, controlBinds)
		if err != nil {
			return control, candidateRun, fmt.Errorf("control run %d: %w", i+1, err)
		}
		candidateSeconds, candidateBytes, candidateHash, err := executeOrdinary(ctx, client, candidateQuery, candidateBinds)
		if err != nil {
			return control, candidateRun, fmt.Errorf("candidate run %d: %w", i+1, err)
		}
		control.WarmSeconds = append(control.WarmSeconds, controlSeconds)
		control.Bytes = append(control.Bytes, controlBytes)
		control.ResultSHA256 = controlHash
		control.Rows = 1000
		candidateRun.WarmSeconds = append(candidateRun.WarmSeconds, candidateSeconds)
		candidateRun.Bytes = append(candidateRun.Bytes, candidateBytes)
		candidateRun.ResultSHA256 = candidateHash
		candidateRun.Rows = 1000
	}
	control.MedianSeconds, control.MinSeconds = median(control.WarmSeconds), minFloatRoot(control.WarmSeconds)
	candidateRun.MedianSeconds, candidateRun.MinSeconds = median(candidateRun.WarmSeconds), minFloatRoot(candidateRun.WarmSeconds)
	controlProfileQuery, controlProfileBinds := cacheBust(incumbent, baseBinds, 14001)
	candidateProfileQuery, candidateProfileBinds := cacheBust(candidate, baseBinds, 14002)
	controlProfile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: controlProfileQuery, BindVars: controlProfileBinds, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		return control, candidateRun, fmt.Errorf("control PROFILE: %w", err)
	}
	candidateProfile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: candidateProfileQuery, BindVars: candidateProfileBinds, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		return control, candidateRun, fmt.Errorf("candidate PROFILE: %w", err)
	}
	control.Explain, err = explainRoot(ctx, client, incumbent, baseBinds)
	if err != nil {
		return control, candidateRun, err
	}
	candidateRun.Explain, err = explainRoot(ctx, client, candidate, baseBinds)
	if err != nil {
		return control, candidateRun, err
	}
	control.RawProfile, candidateRun.RawProfile = controlProfile, candidateProfile
	control.Profile, candidateRun.Profile = summarizeRootProfile(controlProfile), summarizeRootProfile(candidateProfile)
	return control, candidateRun, nil
}

func explainRoot(ctx context.Context, client *arangostore.Client, query string, binds map[string]any) (arangostore.ExplainAssessment, error) {
	explain, err := client.Explain(ctx, arangostore.ExplainRequest{Query: query, BindVars: binds})
	if err != nil {
		return arangostore.ExplainAssessment{}, err
	}
	assessment := arangostore.AssessExplainResult(explain)
	if len(assessment.FullCollectionScans) != 0 {
		return assessment, fmt.Errorf("root endpoint candidate has full scans: %#v", assessment.FullCollectionScans)
	}
	return assessment, nil
}

func summarizeRootProfile(profile arangostore.ProfileResult) rootEndpointProfile {
	summary := arangostore.SummarizeProfile(profile)
	nodes := append([]arangostore.ProfileNodeSummary(nil), summary.Nodes...)
	if len(nodes) > 20 {
		nodes = nodes[:20]
	}
	return rootEndpointProfile{ScannedFull: summary.ScannedFull, ScannedIndex: summary.ScannedIndex, PeakMemoryBytes: summary.PeakMemory, Phases: profile.Extra.Profile, TopNodes: nodes}
}

func minFloatRoot(values []float64) float64 {
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

func minRootLen(length, maximum int) int {
	if length < maximum {
		return length
	}
	return maximum
}

func writeRootEndpointArtifacts(t *testing.T, incumbent, candidate string, binds map[string]any, rewrite rootEndpointRewrite, control, candidateRun rootEndpointRun) {
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(source), "..", "..")
	directory := filepath.Join(root, "docs", "benchmarks", "round4", "tournament_root_endpoint")
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
