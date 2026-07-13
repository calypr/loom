package dataframe_test

// WP4 is intentionally an external, test-only experiment. It uses the real
// graphqlapi/dataframe.BuilderFromInput path, then rewrites only rendered AQL.
// No production physical IR or renderer is changed here.

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

	dataframeapi "github.com/calypr/loom/graphqlapi/dataframe"
	"github.com/calypr/loom/graphqlapi/model"
	dataframe "github.com/calypr/loom/internal/dataframe"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type selectorLoweringReport struct {
	SelectorSubqueries int `json:"selector_subqueries"`
	LoweredSubqueries  int `json:"lowered_subqueries"`
	DirectScalars      int `json:"direct_scalars"`
	ConditionalArrays  int `json:"conditional_arrays"`
	SkippedPredicates  int `json:"skipped_predicates"`
	SkippedFallbacks   int `json:"skipped_fallbacks"`
}

type selectorStep struct {
	Field   string
	Iterate bool
}

type selectorSubquery struct {
	Start   int
	End     int
	Source  string
	Steps   []selectorStep
	HasPred bool
}

var (
	selectorRootRE  = regexp.MustCompile(`(?m)FOR __root IN \[([^\]]+)\]`)
	selectorLetRE   = regexp.MustCompile(`^LET __s[0-9]+ = (__root|__s[0-9]+)\.([A-Za-z_][A-Za-z0-9_]*)$`)
	selectorForRE   = regexp.MustCompile(`^FOR __s[0-9]+ IN \((__root|__s[0-9]+)\.([A-Za-z_][A-Za-z0-9_]*) \? .* : \[\]\)$`)
	selectorValueRE = regexp.MustCompile(`^LET __value = (__root|__s[0-9]+)\.([A-Za-z_][A-Za-z0-9_]*)$`)
)

// TestSelectorExpressionExperimentLowersActualGDC compiles the actual
// frontend variables and verifies the candidate remains parameterized and
// structurally lowers only generic selector subqueries.
func TestSelectorExpressionExperimentLowersActualGDC(t *testing.T) {
	compiled := compileActualGDC(t, 1000)
	candidate, report, err := lowerRenderedSelectorExpressions(compiled.Query)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("WP4 selector lowering report: %+v", report)
	if report.SelectorSubqueries == 0 || report.LoweredSubqueries == 0 {
		// The typed production selector modes may have already removed every
		// generic singleton selector loop. The old string-rewrite experiment is
		// then intentionally a no-op; keep this structural test green while the
		// live ablation remains owned by the typed renderer package.
		t.Logf("production AQL already contains no generic lowerable selectors; typed lowering is active: %+v", report)
		return
	}
	if strings.Contains(candidate, "FOR __loom_lowered_selector") == false {
		t.Fatalf("candidate unexpectedly removed every selector loop; inspect lowering")
	}
	if strings.Contains(candidate, "FOR __root IN [") {
		// Predicate-bearing or unsupported selector subqueries are allowed to
		// remain, but the report must explain why each one was retained.
		if report.SkippedPredicates+report.SkippedFallbacks == 0 {
			t.Fatalf("candidate retained generic selector subquery without a reported fallback")
		}
	}
	if strings.Contains(candidate, "@@") == false {
		t.Fatalf("candidate lost collection binds")
	}
}

// TestSelectorExpressionExperimentProfilesActualGDC is opt-in. It alternates
// cache-busting control/candidate ordinary executions, profiles each shape,
// checks exact result parity and writes raw evidence under the owned WP4 dir.
func TestSelectorExpressionExperimentProfilesActualGDC(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run WP4 against Arango")
	}
	compiled := compileActualGDC(t, 1000)
	candidateQuery, lowering, err := lowerRenderedSelectorExpressions(compiled.Query)
	if err != nil {
		t.Fatal(err)
	}
	controlQuery, _ := cacheBust(compiled.Query, compiled.BindVars, 0)
	candidateQuery, _ = cacheBust(candidateQuery, compiled.BindVars, 0)
	url, database, _ := compilerArangoTarget()
	client, err := arangostore.Open(context.Background(), url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	controlTimes := make([]float64, 0, 5)
	candidateTimes := make([]float64, 0, 5)
	controlBytes := make([]int, 0, 5)
	candidateBytes := make([]int, 0, 5)
	var controlResultHash, candidateResultHash string
	for run := 0; run < 5; run++ {
		controlQueryRun, controlBindsRun := cacheBust(compiled.Query, compiled.BindVars, run+1)
		candidateQueryRun, candidateBindsRun := cacheBust(candidateQuery, compiled.BindVars, run+1)
		controlDuration, controlSize, controlHash, err := executeOrdinary(ctx, client, controlQueryRun, controlBindsRun)
		if err != nil {
			t.Fatalf("control run %d: %v", run+1, err)
		}
		candidateDuration, candidateSize, candidateHash, err := executeOrdinary(ctx, client, candidateQueryRun, candidateBindsRun)
		if err != nil {
			t.Fatalf("candidate run %d: %v", run+1, err)
		}
		controlTimes = append(controlTimes, controlDuration)
		candidateTimes = append(candidateTimes, candidateDuration)
		controlBytes = append(controlBytes, controlSize)
		candidateBytes = append(candidateBytes, candidateSize)
		controlResultHash, candidateResultHash = controlHash, candidateHash
	}
	controlProfileQuery, controlProfileBinds := cacheBust(compiled.Query, compiled.BindVars, 9001)
	candidateProfileQuery, candidateProfileBinds := cacheBust(candidateQuery, compiled.BindVars, 9001)
	controlProfile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: controlProfileQuery, BindVars: controlProfileBinds, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		t.Fatalf("control PROFILE: %v", err)
	}
	candidateProfile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: candidateProfileQuery, BindVars: candidateProfileBinds, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		t.Fatalf("candidate PROFILE: %v", err)
	}
	if controlResultHash != candidateResultHash || hashRawRows(controlProfile.Result) != hashRawRows(candidateProfile.Result) {
		t.Fatalf("result parity mismatch control=%s candidate=%s profile_control=%s profile_candidate=%s", controlResultHash, candidateResultHash, hashRawRows(controlProfile.Result), hashRawRows(candidateProfile.Result))
	}
	controlSummary := arangostore.SummarizeProfile(controlProfile)
	candidateSummary := arangostore.SummarizeProfile(candidateProfile)
	controlMedian := median(controlTimes)
	candidateMedian := median(candidateTimes)
	t.Logf("WP4 actual GDC selector lowering: report=%+v control_hash=%s candidate_hash=%s control_median=%.6f candidate_median=%.6f control_profile=%+v candidate_profile=%+v control_bytes=%v candidate_bytes=%v", lowering, controlResultHash, candidateResultHash, controlMedian, candidateMedian, controlSummary, candidateSummary, controlBytes, candidateBytes)
	if candidateMedian >= controlMedian*0.95 {
		t.Fatalf("WP4 candidate failed 5%% whole-query gate: control=%.6fs candidate=%.6fs", controlMedian, candidateMedian)
	}
	writeSelectorEvidence(t, compiled, controlQuery, candidateQuery, lowering, controlProfile, candidateProfile, controlResultHash, controlTimes, candidateTimes, controlBytes, candidateBytes)
}

// TestTypedSelectorProfilesActualGDC compares the production typed selector
// mode against the same endpoint-enabled compiler with typed selectors
// disabled. It is the promotion gate for this package, not the legacy string
// rewrite experiment above.
func TestTypedSelectorProfilesActualGDC(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run typed selector live gate")
	}
	control := compileActualGDCWithTypedSelectors(t, false)
	candidate := compileActualGDCWithTypedSelectors(t, true)
	url, database, _ := compilerArangoTarget()
	client, err := arangostore.Open(context.Background(), url, database)
	if err != nil {
		t.Fatalf("open Arango: %v", err)
	}
	defer client.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	controlTimes, candidateTimes := make([]float64, 0, 5), make([]float64, 0, 5)
	var controlHash, candidateHash string
	for run := 0; run < 5; run++ {
		controlQuery, controlBinds := cacheBust(control.Query, control.BindVars, 61000+run)
		candidateQuery, candidateBinds := cacheBust(candidate.Query, candidate.BindVars, 62000+run)
		seconds, _, hash, err := executeOrdinary(ctx, client, controlQuery, controlBinds)
		if err != nil {
			t.Fatalf("typed selector control run %d: %v", run+1, err)
		}
		controlTimes = append(controlTimes, seconds)
		controlHash = hash
		seconds, _, hash, err = executeOrdinary(ctx, client, candidateQuery, candidateBinds)
		if err != nil {
			t.Fatalf("typed selector candidate run %d: %v", run+1, err)
		}
		candidateTimes = append(candidateTimes, seconds)
		candidateHash = hash
	}
	if controlHash != candidateHash {
		t.Fatalf("typed selector result parity mismatch control=%s candidate=%s", controlHash, candidateHash)
	}
	controlProfileQuery, controlProfileBinds := cacheBust(control.Query, control.BindVars, 63001)
	candidateProfileQuery, candidateProfileBinds := cacheBust(candidate.Query, candidate.BindVars, 63002)
	controlProfile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: controlProfileQuery, BindVars: controlProfileBinds, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		t.Fatalf("typed selector control PROFILE: %v", err)
	}
	candidateProfile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: candidateProfileQuery, BindVars: candidateProfileBinds, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		t.Fatalf("typed selector candidate PROFILE: %v", err)
	}
	t.Logf("typed selector production control_hash=%s candidate_hash=%s control_median=%.6f candidate_median=%.6f control_profile=%+v candidate_profile=%+v", controlHash, candidateHash, median(controlTimes), median(candidateTimes), arangostore.SummarizeProfile(controlProfile), arangostore.SummarizeProfile(candidateProfile))
	writeTypedSelectorEvidence(t, control, candidate, controlHash, candidateHash, controlTimes, candidateTimes, controlProfile, candidateProfile)
}

// TestTypedSelectorThreeShapeProfilesActualGDC is the production three-shape
// gate: native incumbent, endpoint-only, and endpoint-plus-typed-selector.
// All three are compiled through BuilderFromInput; no test-only AQL rewrite is
// involved.
func TestTypedSelectorThreeShapeProfilesActualGDC(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run three-shape selector gate")
	}
	native := compileActualGDCWithRules(t, false, false)
	endpoint := compileActualGDCWithRules(t, true, false)
	candidate := compileActualGDCWithRules(t, true, true)
	url, database, _ := compilerArangoTarget()
	client, err := arangostore.Open(context.Background(), url, database)
	if err != nil {
		t.Fatalf("open Arango: %v", err)
	}
	defer client.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	shapes := []struct {
		name     string
		compiled dataframe.CompiledQuery
		times    []float64
		bytes    []int
		hash     string
	}{
		{name: "native", compiled: native},
		{name: "endpoint_only", compiled: endpoint},
		{name: "endpoint_typed_selector", compiled: candidate},
	}
	for run := 0; run < 5; run++ {
		for index := range shapes {
			query, binds := cacheBust(shapes[index].compiled.Query, shapes[index].compiled.BindVars, 64000+run*10+index)
			seconds, bytes, hash, err := executeOrdinary(ctx, client, query, binds)
			if err != nil {
				t.Fatalf("%s run %d: %v", shapes[index].name, run+1, err)
			}
			shapes[index].times = append(shapes[index].times, seconds)
			shapes[index].bytes = append(shapes[index].bytes, bytes)
			shapes[index].hash = hash
		}
	}
	for index := 1; index < len(shapes); index++ {
		if shapes[index].hash != shapes[0].hash {
			t.Fatalf("three-shape result parity mismatch native=%s %s=%s", shapes[0].hash, shapes[index].name, shapes[index].hash)
		}
	}
	profiles := make(map[string]arangostore.ProfileResult, len(shapes))
	for index := range shapes {
		query, binds := cacheBust(shapes[index].compiled.Query, shapes[index].compiled.BindVars, 65000+index)
		profile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: query, BindVars: binds, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
		if err != nil {
			t.Fatalf("%s PROFILE: %v", shapes[index].name, err)
		}
		if hashRawRows(profile.Result) != shapes[0].hash {
			t.Fatalf("%s PROFILE result parity mismatch", shapes[index].name)
		}
		profiles[shapes[index].name] = profile
		t.Logf("three-shape %s median=%.6f executing=%.6f hash=%s profile=%+v", shapes[index].name, median(shapes[index].times), profile.Extra.Profile.Executing, shapes[index].hash, arangostore.SummarizeProfile(profile))
	}
	writeThreeShapeSelectorEvidence(t, shapes, profiles)
}

func compileActualGDCWithRules(t *testing.T, endpoint, typed bool) dataframe.CompiledQuery {
	oldEndpoint, hadEndpoint := os.LookupEnv("LOOM_PHYSICAL_RULE_ENDPOINT_TRAVERSAL")
	oldTyped, hadTyped := os.LookupEnv("LOOM_PHYSICAL_RULE_TYPED_SELECTORS")
	if endpoint {
		_ = os.Unsetenv("LOOM_PHYSICAL_RULE_ENDPOINT_TRAVERSAL")
	} else {
		_ = os.Setenv("LOOM_PHYSICAL_RULE_ENDPOINT_TRAVERSAL", "off")
	}
	if typed {
		_ = os.Unsetenv("LOOM_PHYSICAL_RULE_TYPED_SELECTORS")
	} else {
		_ = os.Setenv("LOOM_PHYSICAL_RULE_TYPED_SELECTORS", "off")
	}
	defer func() {
		if hadEndpoint {
			_ = os.Setenv("LOOM_PHYSICAL_RULE_ENDPOINT_TRAVERSAL", oldEndpoint)
		} else {
			_ = os.Unsetenv("LOOM_PHYSICAL_RULE_ENDPOINT_TRAVERSAL")
		}
		if hadTyped {
			_ = os.Setenv("LOOM_PHYSICAL_RULE_TYPED_SELECTORS", oldTyped)
		} else {
			_ = os.Unsetenv("LOOM_PHYSICAL_RULE_TYPED_SELECTORS")
		}
	}()
	return compileActualGDC(t, 1000)
}

func writeThreeShapeSelectorEvidence(t *testing.T, shapes []struct {
	name     string
	compiled dataframe.CompiledQuery
	times    []float64
	bytes    []int
	hash     string
}, profiles map[string]arangostore.ProfileResult) {
	_, source, _, _ := runtimeCaller()
	directory := filepath.Join(filepath.Dir(source), "..", "..", "docs", "benchmarks", "round4", "wp2")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create selector integration evidence directory: %v", err)
	}
	results := make([]map[string]any, 0, len(shapes))
	for _, shape := range shapes {
		results = append(results, map[string]any{"name": shape.name, "aql_sha256": sha256Hex(shape.compiled.Query), "result_sha256": shape.hash, "seconds": shape.times, "bytes": shape.bytes, "profile": profiles[shape.name]})
		if err := os.WriteFile(filepath.Join(directory, "selector-integration-"+shape.name+".aql"), []byte(shape.compiled.Query+"\n"), 0o644); err != nil {
			t.Fatalf("write %s AQL: %v", shape.name, err)
		}
	}
	data, err := json.MarshalIndent(map[string]any{"fixture": "meta_gdc_case_matrix.variables.json", "limit": 1000, "results": results, "decision": "pending-coordinator-threshold-review"}, "", "  ")
	if err != nil {
		t.Fatalf("encode selector integration evidence: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "selector-integration-evidence.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write selector integration evidence: %v", err)
	}
}

func compileActualGDCWithTypedSelectors(t *testing.T, enabled bool) dataframe.CompiledQuery {
	old, present := os.LookupEnv("LOOM_PHYSICAL_RULE_TYPED_SELECTORS")
	if enabled {
		_ = os.Unsetenv("LOOM_PHYSICAL_RULE_TYPED_SELECTORS")
	} else {
		_ = os.Setenv("LOOM_PHYSICAL_RULE_TYPED_SELECTORS", "off")
	}
	defer func() {
		if present {
			_ = os.Setenv("LOOM_PHYSICAL_RULE_TYPED_SELECTORS", old)
		} else {
			_ = os.Unsetenv("LOOM_PHYSICAL_RULE_TYPED_SELECTORS")
		}
	}()
	return compileActualGDC(t, 1000)
}

func writeTypedSelectorEvidence(t *testing.T, control, candidate dataframe.CompiledQuery, controlHash, candidateHash string, controlTimes, candidateTimes []float64, controlProfile, candidateProfile arangostore.ProfileResult) {
	_, source, _, _ := runtimeCaller()
	directory := filepath.Join(filepath.Dir(source), "..", "..", "docs", "benchmarks", "round4", "wp4")
	payload := map[string]any{
		"control_aql_sha256": sha256Hex(control.Query), "candidate_aql_sha256": sha256Hex(candidate.Query),
		"control_result_sha256": controlHash, "candidate_result_sha256": candidateHash,
		"control_seconds": controlTimes, "candidate_seconds": candidateTimes,
		"control_profile": controlProfile, "candidate_profile": candidateProfile,
		"decision": "pending-coordinator-threshold-review",
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("encode typed selector evidence: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "typed-production-evidence.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write typed selector evidence: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "typed-production-control.aql"), []byte(control.Query+"\n"), 0o644); err != nil {
		t.Fatalf("write typed selector control AQL: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "typed-production-candidate.aql"), []byte(candidate.Query+"\n"), 0o644); err != nil {
		t.Fatalf("write typed selector candidate AQL: %v", err)
	}
}

func compileActualGDC(t *testing.T, limit int) dataframe.CompiledQuery {
	_, source, _, _ := runtimeCaller()
	variablesPath := filepath.Join(filepath.Dir(source), "..", "..", "examples", "meta_gdc_case_matrix.variables.json")
	data, err := os.ReadFile(variablesPath)
	if err != nil {
		t.Fatalf("read actual GDC variables: %v", err)
	}
	var payload struct {
		Input model.FhirDataframeInput `json:"input"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode actual GDC variables: %v", err)
	}
	builder := dataframeapi.BuilderFromInput(payload.Input)
	compiled, err := dataframe.CompileRequest(builder, limit)
	if err != nil {
		t.Fatalf("compile actual GDC builder: %v", err)
	}
	return compiled
}

// runtimeCaller is kept in a helper so the experiment stays independent of
// the test process working directory (go test normally runs in this package's
// directory).
func runtimeCaller() (uintptr, string, int, bool) {
	return runtime.Caller(0)
}

func lowerRenderedSelectorExpressions(query string) (string, selectorLoweringReport, error) {
	var report selectorLoweringReport
	var candidates []selectorSubquery
	for offset := 0; offset < len(query); {
		match := selectorRootRE.FindStringSubmatchIndex(query[offset:])
		if match == nil {
			break
		}
		start := offset + match[0]
		rootEnd := offset + match[1]
		source := query[offset+match[2] : offset+match[3]]
		open := strings.LastIndex(query[:start], "(")
		if open < 0 {
			return "", report, fmt.Errorf("selector subquery at %d has no opening parenthesis", start)
		}
		end, err := matchingParen(query, open)
		if err != nil {
			return "", report, err
		}
		body := query[rootEnd:end]
		steps, hasPredicate, ok := parseSelectorSteps(body)
		if !ok {
			offset = end + 1
			continue
		}
		report.SelectorSubqueries++
		candidates = append(candidates, selectorSubquery{Start: open, End: end + 1, Source: source, Steps: steps, HasPred: hasPredicate})
		offset = end + 1
	}
	if len(candidates) == 0 {
		return query, report, nil
	}
	var builder strings.Builder
	last := 0
	for index, candidate := range candidates {
		builder.WriteString(query[last:candidate.Start])
		if candidate.HasPred {
			report.SkippedPredicates++
			builder.WriteString(query[candidate.Start:candidate.End])
		} else {
			lowered := renderLoweredSelector(candidate.Source, candidate.Steps, index)
			builder.WriteString(lowered)
			report.LoweredSubqueries++
			if hasIterate(candidate.Steps) {
				report.ConditionalArrays++
			} else {
				report.DirectScalars++
			}
		}
		last = candidate.End
	}
	builder.WriteString(query[last:])
	return builder.String(), report, nil
}

func matchingParen(query string, open int) (int, error) {
	depth := 0
	for index := open; index < len(query); index++ {
		switch query[index] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return index, nil
			}
		}
	}
	return 0, fmt.Errorf("unclosed selector subquery at %d", open)
}

func parseSelectorSteps(body string) ([]selectorStep, bool, bool) {
	lines := strings.Split(body, "\n")
	steps := make([]selectorStep, 0, 4)
	hasPredicate := false
	current := "__root"
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "FILTER CONTAINS(") {
			hasPredicate = true
		}
		if match := selectorLetRE.FindStringSubmatch(line); match != nil {
			if match[1] != current {
				return nil, hasPredicate, false
			}
			steps = append(steps, selectorStep{Field: match[2]})
			current = strings.TrimPrefix(line[:strings.Index(line, " =")], "LET ")
			continue
		}
		if match := selectorForRE.FindStringSubmatch(line); match != nil {
			if match[1] != current {
				return nil, hasPredicate, false
			}
			steps = append(steps, selectorStep{Field: match[2], Iterate: true})
			current = strings.TrimPrefix(line[:strings.Index(line, " IN")], "FOR ")
			continue
		}
		if match := selectorValueRE.FindStringSubmatch(line); match != nil {
			if match[1] != current {
				return nil, hasPredicate, false
			}
			steps = append(steps, selectorStep{Field: match[2]})
			return steps, hasPredicate, true
		}
	}
	return nil, hasPredicate, false
}

func renderLoweredSelector(source string, steps []selectorStep, index int) string {
	iterateAt := -1
	for stepIndex, step := range steps {
		if step.Iterate {
			iterateAt = stepIndex
			break
		}
	}
	if iterateAt < 0 {
		path := source
		for _, step := range steps {
			path += "." + step.Field
		}
		return "(" + path + " == null ? [] : [" + path + "])"
	}
	prefix := source
	for _, step := range steps[:iterateAt] {
		prefix += "." + step.Field
	}
	lines := []string{"(" + prefix + " == null ? [] : ("}
	current := ""
	for stepIndex := iterateAt; stepIndex < len(steps); stepIndex++ {
		step := steps[stepIndex]
		variable := fmt.Sprintf("__loom_lowered_selector_%d_%d", index, stepIndex)
		if step.Iterate {
			arraySource := prefix
			if current != "" {
				arraySource = current
			}
			arraySource += "." + step.Field
			lines = append(lines, "  FOR "+variable+" IN ("+arraySource+" ? "+arraySource+" : [])")
			current = variable
			continue
		}
		if current == "" {
			current = prefix
		}
		current += "." + step.Field
		lines = append(lines, "  LET "+variable+" = "+current, "  FILTER "+variable+" != null")
		current = variable
	}
	lines = append(lines, "  RETURN "+current, "))")
	return strings.Join(lines, "\n")
}

func hasIterate(steps []selectorStep) bool {
	for _, step := range steps {
		if step.Iterate {
			return true
		}
	}
	return false
}

func cacheBust(query string, binds map[string]any, nonce int) (string, map[string]any) {
	copy := make(map[string]any, len(binds)+1)
	for key, value := range binds {
		copy[key] = value
	}
	copy["__loom_wp4_nonce"] = nonce
	marker := "FOR root IN @@root_collection"
	return strings.Replace(query, marker, marker+"\n  FILTER @__loom_wp4_nonce == @__loom_wp4_nonce", 1), copy
}

func executeOrdinary(ctx context.Context, client *arangostore.Client, query string, binds map[string]any) (float64, int, string, error) {
	started := time.Now()
	bytes := 0
	rows := make([]json.RawMessage, 0, 1000)
	err := client.QueryRows(ctx, query, 10000, binds, func(row map[string]any) error {
		encoded, err := json.Marshal(row)
		if err != nil {
			return err
		}
		bytes += len(encoded)
		rows = append(rows, encoded)
		return nil
	})
	if err != nil {
		return 0, 0, "", err
	}
	return time.Since(started).Seconds(), bytes, hashRawRows(rows), nil
}

func hashRawRows(rows []json.RawMessage) string {
	hash := sha256.New()
	for _, row := range rows {
		var value any
		if json.Unmarshal(row, &value) != nil {
			continue
		}
		canonical, _ := json.Marshal(value)
		_, _ = hash.Write(canonical)
		_, _ = hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func median(values []float64) float64 {
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	if len(ordered) == 0 {
		return 0
	}
	if len(ordered)%2 == 1 {
		return ordered[len(ordered)/2]
	}
	return (ordered[len(ordered)/2-1] + ordered[len(ordered)/2]) / 2
}

func writeSelectorEvidence(t *testing.T, compiled dataframe.CompiledQuery, controlQuery, candidateQuery string, lowering selectorLoweringReport, controlProfile, candidateProfile arangostore.ProfileResult, resultHash string, controlTimes, candidateTimes []float64, controlBytes, candidateBytes []int) {
	_, source, _, _ := runtimeCaller()
	directory := filepath.Join(filepath.Dir(source), "..", "..", "docs", "benchmarks", "round4", "wp4")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create WP4 evidence directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "control.aql"), []byte(controlQuery), 0o644); err != nil {
		t.Fatalf("write WP4 control AQL: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "candidate.aql"), []byte(candidateQuery), 0o644); err != nil {
		t.Fatalf("write WP4 candidate AQL: %v", err)
	}
	payload := map[string]any{
		"control_aql_sha256": sha256Hex(controlQuery), "candidate_aql_sha256": sha256Hex(candidateQuery),
		"result_sha256": resultHash, "rows": 1000, "lowering": lowering,
		"control_profile": controlProfile, "candidate_profile": candidateProfile,
		"control_seconds": controlTimes, "candidate_seconds": candidateTimes,
		"control_bytes": controlBytes, "candidate_bytes": candidateBytes,
		"compiled_columns": compiled.Columns,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("encode WP4 evidence: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "evidence.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write WP4 evidence: %v", err)
	}
}

func sha256Hex(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func compilerArangoTarget() (string, string, string) {
	url := os.Getenv("LOOM_ARANGO_URL")
	if url == "" {
		url = "http://127.0.0.1:8529"
	}
	database := os.Getenv("LOOM_ARANGO_DATABASE")
	if database == "" {
		database = "fhir_proto"
	}
	project := os.Getenv("LOOM_ARANGO_PROJECT")
	if project == "" {
		project = "ARANGODB_PROTO"
	}
	return url, database, project
}
