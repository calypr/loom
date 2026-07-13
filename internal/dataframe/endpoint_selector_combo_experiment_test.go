package dataframe_test

// This is a read-only, opt-in tournament harness. It composes the two
// previously measured test-only rewrites (WP2 endpoint equality and WP4
// selector lowering) after compiling the real GraphQL input through
// BuilderFromInput. It does not alter compiler IR or production rendering.

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

	dataframe "github.com/calypr/loom/internal/dataframe"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type endpointSelectorComboRun struct {
	Name          string                         `json:"name"`
	QuerySHA256   string                         `json:"query_sha256"`
	ResultSHA256  string                         `json:"result_sha256"`
	Rows          int                            `json:"rows"`
	Bytes         []int                          `json:"bytes"`
	WarmSeconds   []float64                      `json:"warm_seconds"`
	MedianSeconds float64                        `json:"median_seconds"`
	MinSeconds    float64                        `json:"min_seconds"`
	Explain       arangostore.ExplainAssessment  `json:"explain"`
	Profile       endpointSelectorProfileSummary `json:"profile"`
}

type endpointSelectorProfileSummary struct {
	ScannedFull     int                              `json:"scanned_full"`
	ScannedIndex    int                              `json:"scanned_index"`
	PeakMemoryBytes uint64                           `json:"peak_memory_bytes"`
	Phases          arangostore.ProfilePhases        `json:"phases"`
	TopNodes        []arangostore.ProfileNodeSummary `json:"top_nodes"`
}

// TestEndpointSelectorComboRendersActualGDC is compile-safe and does not need
// Arango. It proves the composition starts with BuilderFromInput, rewrites all
// three eligible nested routes, and retains bind-backed AQL.
func TestEndpointSelectorComboRendersActualGDC(t *testing.T) {
	compiled := compileActualGDC(t, 1000)
	compiled = pinComboControlIfCompilerMoved(t, compiled)
	endpointOnly, routes, err := prepareEndpointSelectorBase(compiled.Query)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 3 {
		t.Fatalf("rewrote %d nested routes, want 3: %#v", len(routes), routes)
	}
	combo, lowering, err := lowerRenderedSelectorExpressions(endpointOnly)
	if err != nil {
		t.Fatalf("lower selectors after endpoint rewrite: %v", err)
	}
	if lowering.LoweredSubqueries == 0 {
		t.Fatalf("combined candidate lowered no selectors: %+v", lowering)
	}
	if strings.Contains(combo, "IN 1..1 INBOUND __loom_physical_parent_set") || strings.Contains(combo, "IN 1..1 OUTBOUND __loom_physical_parent_set") {
		t.Fatalf("combined candidate retained a nested native traversal:\n%s", combo)
	}
	if !strings.Contains(combo, "FILTER child_set_3_edge._to == __loom_physical_parent_set_4._id") ||
		!strings.Contains(combo, "FILTER child_set_4_edge._to == __loom_physical_parent_set_5._id") ||
		!strings.Contains(combo, "FILTER child_set_5_edge._to == __loom_physical_parent_set_6._id") {
		t.Fatalf("combined candidate omitted one endpoint equality routes=%#v has3=%t has4=%t has5=%t\n%s", routes, strings.Contains(combo, "FILTER child_set_3_edge._to == __loom_physical_parent_set_4._id"), strings.Contains(combo, "FILTER child_set_4_edge._to == __loom_physical_parent_set_5._id"), strings.Contains(combo, "FILTER child_set_5_edge._to == __loom_physical_parent_set_6._id"), combo[:minComboLen(len(combo), 7000)])
	}
	if !strings.Contains(combo, "@@child_set_3_edge_collection") || !strings.Contains(combo, "@project") {
		t.Fatalf("combined candidate lost collection or value binds")
	}
}

func minComboLen(length, maximum int) int {
	if length < maximum {
		return length
	}
	return maximum
}

// pinComboControlIfCompilerMoved protects this experiment from concurrent
// production renderer work. BuilderFromInput is still executed first; when
// its AQL no longer matches the locked WP2 control, the exact previously
// profiled AQL and bind variables are used so the comparison does not mix
// compiler baselines.
func pinComboControlIfCompilerMoved(t *testing.T, compiled dataframe.CompiledQuery) dataframe.CompiledQuery {
	const expectedAQLSHA = "4081527f4d893c7fc8b4957ad75ffbf51a975a8b646c315f01d09093444aad68"
	const integratedAQLSHA = "988775e708a0f836ed34de0815e74cdbf38172e75c12a80149a9ce6096b48925"
	if sha256Hex(compiled.Query) == expectedAQLSHA || sha256Hex(compiled.Query) == integratedAQLSHA {
		return compiled
	}
	t.Logf("BuilderFromInput control AQL changed during concurrent integration (got %s); pinning WP2 control %s", sha256Hex(compiled.Query), expectedAQLSHA)
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(source), "..", "..")
	queryPath := filepath.Join(root, "docs", "benchmarks", "round4", "wp2", "production.aql")
	reportPath := filepath.Join(root, "docs", "benchmarks", "round4", "wp2", "production.json")
	queryBytes, err := os.ReadFile(queryPath)
	if err != nil {
		t.Fatalf("read pinned control AQL: %v", err)
	}
	query := strings.TrimSuffix(strings.TrimSuffix(string(queryBytes), "\n"), "\r")
	if sha256Hex(query) != expectedAQLSHA {
		t.Fatalf("pinned control AQL hash changed: got %s want %s", sha256Hex(query), expectedAQLSHA)
	}
	reportBytes, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read pinned control bind vars: %v", err)
	}
	var report struct {
		BindVars map[string]any `json:"bind_vars"`
	}
	if err := json.Unmarshal(reportBytes, &report); err != nil {
		t.Fatalf("decode pinned control bind vars: %v", err)
	}
	return dataframe.CompiledQuery{Query: query, BindVars: report.BindVars, Limit: 1000}
}

// TestEndpointSelectorComboProfilesActualGDC is deliberately opt-in. It
// alternates unchanged production control and the composed candidate, consumes
// every row, then captures Explain/Profile and raw artifacts. The endpoint-only
// median is run separately to answer whether selector lowering compounds the
// proven WP2 4.211s result.
func TestEndpointSelectorComboProfilesActualGDC(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run endpoint/selector combo against Arango")
	}
	compiled := compileActualGDC(t, 1000)
	compiled = pinComboControlIfCompilerMoved(t, compiled)
	endpointOnly, routes, err := prepareEndpointSelectorBase(compiled.Query)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 3 {
		t.Fatalf("rewrote %d nested routes, want 3: %#v", len(routes), routes)
	}
	combo, lowering, err := lowerRenderedSelectorExpressions(endpointOnly)
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

	control, candidate, err := runAlternatingCombo(ctx, client, compiled.Query, combo, compiled.BindVars, "control", "endpoint_selector_combo")
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := runShapeFive(ctx, client, endpointOnly, compiled.BindVars, "endpoint_only")
	if err != nil {
		t.Fatal(err)
	}
	if control.ResultSHA256 != candidate.ResultSHA256 || control.ResultSHA256 != endpoint.ResultSHA256 {
		t.Fatalf("result parity mismatch control=%s endpoint=%s combo=%s", control.ResultSHA256, endpoint.ResultSHA256, candidate.ResultSHA256)
	}
	if candidate.MedianSeconds >= endpoint.MedianSeconds {
		t.Logf("combined selector lowering did not beat endpoint-only: endpoint=%.6fs combo=%.6fs", endpoint.MedianSeconds, candidate.MedianSeconds)
	}
	if candidate.MedianSeconds >= control.MedianSeconds {
		t.Fatalf("combined candidate regressed control: control=%.6fs combo=%.6fs", control.MedianSeconds, candidate.MedianSeconds)
	}

	writeEndpointSelectorComboArtifacts(t, compiled, endpointOnly, combo, routes, lowering, control, endpoint, candidate)
	t.Logf("WP2+WP4 control=%.6fs endpoint_only=%.6fs combo=%.6fs improvement_vs_control=%.2f%% improvement_vs_endpoint=%.2f%% control_profile=%.6fs combo_profile=%.6fs", control.MedianSeconds, endpoint.MedianSeconds, candidate.MedianSeconds, 100*(control.MedianSeconds-candidate.MedianSeconds)/control.MedianSeconds, 100*(endpoint.MedianSeconds-candidate.MedianSeconds)/endpoint.MedianSeconds, control.Profile.Phases.Executing, candidate.Profile.Phases.Executing)
}

func runAlternatingCombo(ctx context.Context, client *arangostore.Client, controlQuery, candidateQuery string, baseBinds map[string]any, controlName, candidateName string) (endpointSelectorComboRun, endpointSelectorComboRun, error) {
	control := endpointSelectorComboRun{Name: controlName, QuerySHA256: sha256Hex(controlQuery)}
	candidate := endpointSelectorComboRun{Name: candidateName, QuerySHA256: sha256Hex(candidateQuery)}
	for i := 0; i < 5; i++ {
		controlQueryRun, controlBinds := cacheBust(controlQuery, baseBinds, 7000+i)
		candidateQueryRun, candidateBinds := cacheBust(candidateQuery, baseBinds, 8000+i)
		controlSeconds, controlBytes, controlHash, err := executeOrdinary(ctx, client, controlQueryRun, controlBinds)
		if err != nil {
			return control, candidate, fmt.Errorf("control run %d: %w", i+1, err)
		}
		candidateSeconds, candidateBytes, candidateHash, err := executeOrdinary(ctx, client, candidateQueryRun, candidateBinds)
		if err != nil {
			return control, candidate, fmt.Errorf("candidate run %d: %w", i+1, err)
		}
		control.WarmSeconds = append(control.WarmSeconds, controlSeconds)
		control.Bytes = append(control.Bytes, controlBytes)
		control.Rows = 1000
		control.ResultSHA256 = controlHash
		candidate.WarmSeconds = append(candidate.WarmSeconds, candidateSeconds)
		candidate.Bytes = append(candidate.Bytes, candidateBytes)
		candidate.Rows = 1000
		candidate.ResultSHA256 = candidateHash
	}
	control.MedianSeconds, control.MinSeconds = median(control.WarmSeconds), minFloat(control.WarmSeconds)
	candidate.MedianSeconds, candidate.MinSeconds = median(candidate.WarmSeconds), minFloat(candidate.WarmSeconds)
	controlProfileQuery, controlProfileBinds := cacheBust(controlQuery, baseBinds, 9001)
	candidateProfileQuery, candidateProfileBinds := cacheBust(candidateQuery, baseBinds, 9002)
	controlProfile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: controlProfileQuery, BindVars: controlProfileBinds, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		return control, candidate, fmt.Errorf("control PROFILE: %w", err)
	}
	candidateProfile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: candidateProfileQuery, BindVars: candidateProfileBinds, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		return control, candidate, fmt.Errorf("candidate PROFILE: %w", err)
	}
	control.Explain, err = explainShape(ctx, client, controlQuery, baseBinds)
	if err != nil {
		return control, candidate, err
	}
	candidate.Explain, err = explainShape(ctx, client, candidateQuery, baseBinds)
	if err != nil {
		return control, candidate, err
	}
	control.Profile = summarizeComboProfile(controlProfile)
	candidate.Profile = summarizeComboProfile(candidateProfile)
	return control, candidate, nil
}

func runShapeFive(ctx context.Context, client *arangostore.Client, query string, baseBinds map[string]any, name string) (endpointSelectorComboRun, error) {
	run := endpointSelectorComboRun{Name: name, QuerySHA256: sha256Hex(query)}
	for i := 0; i < 5; i++ {
		queryRun, binds := cacheBust(query, baseBinds, 10000+i)
		seconds, bytes, hash, err := executeOrdinary(ctx, client, queryRun, binds)
		if err != nil {
			return run, fmt.Errorf("%s run %d: %w", name, i+1, err)
		}
		run.WarmSeconds = append(run.WarmSeconds, seconds)
		run.Bytes = append(run.Bytes, bytes)
		run.Rows = 1000
		run.ResultSHA256 = hash
	}
	run.MedianSeconds, run.MinSeconds = median(run.WarmSeconds), minFloat(run.WarmSeconds)
	profileQuery, profileBinds := cacheBust(query, baseBinds, 11001)
	profile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: profileQuery, BindVars: profileBinds, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		return run, fmt.Errorf("%s PROFILE: %w", name, err)
	}
	run.Explain, err = explainShape(ctx, client, query, baseBinds)
	if err != nil {
		return run, err
	}
	run.Profile = summarizeComboProfile(profile)
	return run, nil
}

func explainShape(ctx context.Context, client *arangostore.Client, query string, binds map[string]any) (arangostore.ExplainAssessment, error) {
	explain, err := client.Explain(ctx, arangostore.ExplainRequest{Query: query, BindVars: binds})
	if err != nil {
		return arangostore.ExplainAssessment{}, err
	}
	assessment := arangostore.AssessExplainResult(explain)
	if len(assessment.FullCollectionScans) != 0 {
		return assessment, fmt.Errorf("candidate introduced full collection scans: %#v", assessment.FullCollectionScans)
	}
	return assessment, nil
}

func summarizeComboProfile(profile arangostore.ProfileResult) endpointSelectorProfileSummary {
	summary := arangostore.SummarizeProfile(profile)
	nodes := append([]arangostore.ProfileNodeSummary(nil), summary.Nodes...)
	if len(nodes) > 20 {
		nodes = nodes[:20]
	}
	return endpointSelectorProfileSummary{ScannedFull: summary.ScannedFull, ScannedIndex: summary.ScannedIndex, PeakMemoryBytes: summary.PeakMemory, Phases: profile.Extra.Profile, TopNodes: nodes}
}

func minFloat(values []float64) float64 {
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

type endpointSelectorRouteRewrite struct {
	Node       string `json:"node"`
	Edge       string `json:"edge"`
	Parent     string `json:"parent"`
	Direction  string `json:"direction"`
	Collection string `json:"collection"`
}

var nestedEndpointHeaderRE = regexp.MustCompile(`(?m)^(\s*)FOR ([A-Za-z_][A-Za-z0-9_]*)_node, ([A-Za-z_][A-Za-z0-9_]*)_edge IN 1\.\.1 (INBOUND|OUTBOUND) ([A-Za-z_][A-Za-z0-9_]*) (@@[A-Za-z_][A-Za-z0-9_]*_edge_collection)\n`)

func rewriteNestedEndpointTraversals(query string) (string, []endpointSelectorRouteRewrite, error) {
	matches := nestedEndpointHeaderRE.FindAllStringSubmatchIndex(query, -1)
	var builder strings.Builder
	last := 0
	routes := make([]endpointSelectorRouteRewrite, 0, len(matches))
	for _, match := range matches {
		node := query[match[4]:match[5]] + "_node"
		edge := query[match[6]:match[7]] + "_edge"
		direction := query[match[8]:match[9]]
		parent := query[match[10]:match[11]]
		collection := query[match[12]:match[13]]
		if !strings.HasPrefix(parent, "__loom_physical_parent_set_") {
			continue
		}
		endpoint, target := "_to", "_from"
		if direction == "OUTBOUND" {
			endpoint, target = "_from", "_to"
		}
		builder.WriteString(query[last:match[0]])
		builder.WriteString("FOR " + edge + " IN " + collection + "\n")
		builder.WriteString("  FILTER " + edge + "." + endpoint + " == " + parent + "._id\n")
		builder.WriteString("  LET " + node + " = DOCUMENT(" + edge + "." + target + ")\n")
		last = match[1]
		routes = append(routes, endpointSelectorRouteRewrite{Node: node, Edge: edge, Parent: parent, Direction: direction, Collection: collection})
	}
	if len(routes) == 0 {
		return query, nil, fmt.Errorf("no nested physical traversal headers found")
	}
	builder.WriteString(query[last:])
	return builder.String(), routes, nil
}

var explicitEndpointHeaderRE = regexp.MustCompile(`(?m)^\s*FOR ([A-Za-z_][A-Za-z0-9_]*)_edge IN (@@[A-Za-z_][A-Za-z0-9_]*_edge_collection)\n\s+FILTER ([A-Za-z_][A-Za-z0-9_]*)_edge\.(\w+) == ([A-Za-z_][A-Za-z0-9_]*)\._id`)

// prepareEndpointSelectorBase uses the test-only WP2 rewrite for the pinned
// native control. If the coordinator has already enabled WP2 in production,
// the current integrated AQL is itself the endpoint-only control and is used
// unchanged. This keeps the selector comparison meaningful while the shared
// compiler is moving.
func prepareEndpointSelectorBase(query string) (string, []endpointSelectorRouteRewrite, error) {
	endpointOnly, routes, err := rewriteNestedEndpointTraversals(query)
	if err == nil {
		return endpointOnly, routes, nil
	}
	if sha256Hex(query) != "988775e708a0f836ed34de0815e74cdbf38172e75c12a80149a9ce6096b48925" {
		return query, nil, err
	}
	matches := explicitEndpointHeaderRE.FindAllStringSubmatch(query, -1)
	if len(matches) != 3 {
		return query, nil, fmt.Errorf("integrated endpoint control has %d explicit nested routes, want 3", len(matches))
	}
	routes = make([]endpointSelectorRouteRewrite, 0, len(matches))
	for _, match := range matches {
		direction := "INBOUND"
		if match[4] == "_from" {
			direction = "OUTBOUND"
		}
		routes = append(routes, endpointSelectorRouteRewrite{Node: match[1] + "_node", Edge: match[1] + "_edge", Parent: match[5], Direction: direction, Collection: match[2]})
	}
	return query, routes, nil
}

func writeEndpointSelectorComboArtifacts(t *testing.T, compiled dataframe.CompiledQuery, endpointOnly, combo string, routes []endpointSelectorRouteRewrite, lowering selectorLoweringReport, control, endpoint, candidate endpointSelectorComboRun) {
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(source), "..", "..")
	directory := filepath.Join(root, "docs", "benchmarks", "round4", "wp2_selector_combo")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, query := range map[string]string{"production.aql": compiled.Query, "endpoint_only.aql": endpointOnly, "candidate.aql": combo} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(query+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	payload := map[string]any{"control": control, "endpoint_only": endpoint, "candidate": candidate, "routes": routes, "selector_lowering": lowering, "compiled_columns": compiled.Columns}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "RESULTS.json"), append(encoded, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
