package dataframe_test

// WP3 Round 4 is an experiment-only, full-query identity-deduplication
// tournament.  It starts with the AQL produced by compileActualGDC (the real
// graphqlapi/dataframe.BuilderFromInput path) and edits only the rendered
// child-set expressions.  No production compiler or renderer code is changed.

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

type identityDedupReport struct {
	Input                     string    `json:"input"`
	Limit                     int       `json:"limit"`
	ChildSets                 int       `json:"child_sets"`
	TransformedSets           int       `json:"transformed_sets"`
	ControlObjectUnique       int       `json:"control_object_unique"`
	CandidateObjectUnique     int       `json:"candidate_object_unique"`
	CandidateIdentityCollects int       `json:"candidate_identity_collects"`
	ControlAQLSHA256          string    `json:"control_aql_sha256"`
	CandidateAQLSHA256        string    `json:"candidate_aql_sha256"`
	ControlResultSHA256       string    `json:"control_result_sha256,omitempty"`
	CandidateResultSHA256     string    `json:"candidate_result_sha256,omitempty"`
	ControlRows               int       `json:"control_rows,omitempty"`
	CandidateRows             int       `json:"candidate_rows,omitempty"`
	ControlBytes              []int     `json:"control_bytes,omitempty"`
	CandidateBytes            []int     `json:"candidate_bytes,omitempty"`
	ControlSeconds            []float64 `json:"control_seconds,omitempty"`
	CandidateSeconds          []float64 `json:"candidate_seconds,omitempty"`
	ControlProfile            any       `json:"control_profile,omitempty"`
	CandidateProfile          any       `json:"candidate_profile,omitempty"`
	Decision                  string    `json:"decision"`
	Blocker                   string    `json:"blocker,omitempty"`
}

// TestIdentityDedupCandidateBuildsActualGDC proves that the candidate is
// generated from the actual frontend request and keeps all user values bound.
func TestIdentityDedupCandidateBuildsActualGDC(t *testing.T) {
	compiled := compileActualGDC(t, 1000)
	candidate, report, err := buildIdentityDedupCandidate(compiled.Query)
	if err != nil {
		t.Fatal(err)
	}
	if report.ChildSets == 0 || report.TransformedSets == 0 {
		t.Fatalf("no child sets transformed: %+v", report)
	}
	// Shared child sets may remain on the conservative object-shaping path;
	// only independently materialized sets are eligible for identity-first
	// grouping in this experiment.
	if report.TransformedSets > report.ChildSets {
		t.Fatalf("candidate transformed more sets than exist: %+v", report)
	}
	if report.CandidateObjectUnique >= report.ControlObjectUnique {
		t.Fatalf("candidate did not remove child-set object UNIQUE wrappers: %+v", report)
	}
	if report.CandidateIdentityCollects != report.TransformedSets {
		t.Fatalf("candidate identity collect count = %d, want eligible transformed set count %d", report.CandidateIdentityCollects, report.TransformedSets)
	}
	if !strings.Contains(candidate, "COLLECT __loom_identity_key_") {
		t.Fatalf("candidate has no identity grouping:\n%s", candidate)
	}
	if strings.Contains(candidate, "LET child_set_1 = UNIQUE((") {
		t.Fatal("candidate retained object-level child_set_1 UNIQUE")
	}
	if !strings.Contains(candidate, "@@child_set_1_edge_collection") || !strings.Contains(candidate, "@dataset_generation") {
		t.Fatal("candidate lost route or scope binds")
	}
	t.Logf("WP3 identity candidate report: %+v", report)
}

// TestIdentityDedupProfilesActualGDC is opt-in because it executes the full
// 1,000-row request against the local Arango instance.  It alternates control
// and candidate with a bind-backed harmless predicate, consumes every row,
// captures PROFILE/Explain-derived node statistics, and always writes the
// decision artifact when live execution is available.
func TestIdentityDedupProfilesActualGDC(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run WP3 identity tournament")
	}
	compiled := compileActualGDC(t, 1000)
	candidate, report, err := buildIdentityDedupCandidate(compiled.Query)
	if err != nil {
		t.Fatal(err)
	}
	url, database, _ := compilerArangoTarget()
	client, err := arangostore.Open(context.Background(), url, database)
	if err != nil {
		t.Fatalf("open Arango: %v", err)
	}
	defer client.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	controlSeconds := make([]float64, 0, 5)
	candidateSeconds := make([]float64, 0, 5)
	controlBytes := make([]int, 0, 5)
	candidateBytes := make([]int, 0, 5)
	var controlHash, candidateHash string
	var controlRows, candidateRows int
	for run := 0; run < 5; run++ {
		controlQuery, controlBinds := cacheBust(compiled.Query, compiled.BindVars, 41000+run)
		candidateQuery, candidateBinds := cacheBust(candidate, compiled.BindVars, 42000+run)
		seconds, bytes, hash, err := executeOrdinary(ctx, client, controlQuery, controlBinds)
		if err != nil {
			t.Fatalf("control run %d: %v", run+1, err)
		}
		controlSeconds = append(controlSeconds, seconds)
		controlBytes = append(controlBytes, bytes)
		controlHash = hash
		if run == 0 {
			controlRows = 1000
		}
		seconds, bytes, hash, err = executeOrdinary(ctx, client, candidateQuery, candidateBinds)
		if err != nil {
			t.Fatalf("candidate run %d: %v", run+1, err)
		}
		candidateSeconds = append(candidateSeconds, seconds)
		candidateBytes = append(candidateBytes, bytes)
		candidateHash = hash
		if run == 0 {
			candidateRows = 1000
		}
	}
	report.ControlResultSHA256 = controlHash
	report.CandidateResultSHA256 = candidateHash
	report.ControlRows = controlRows
	report.CandidateRows = candidateRows
	report.ControlBytes = controlBytes
	report.CandidateBytes = candidateBytes
	report.ControlSeconds = controlSeconds
	report.CandidateSeconds = candidateSeconds

	controlProfileQuery, controlProfileBinds := cacheBust(compiled.Query, compiled.BindVars, 43001)
	candidateProfileQuery, candidateProfileBinds := cacheBust(candidate, compiled.BindVars, 43002)
	controlProfile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: controlProfileQuery, BindVars: controlProfileBinds, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		t.Fatalf("control PROFILE: %v", err)
	}
	candidateProfile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: candidateProfileQuery, BindVars: candidateProfileBinds, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		t.Fatalf("candidate PROFILE: %v", err)
	}
	controlSummary := arangostore.SummarizeProfile(controlProfile)
	candidateSummary := arangostore.SummarizeProfile(candidateProfile)
	report.ControlProfile = controlSummary
	report.CandidateProfile = candidateSummary
	report.ControlAQLSHA256 = sha256Hex(compiled.Query)
	report.CandidateAQLSHA256 = sha256Hex(candidate)
	if controlHash != candidateHash || hashRawRows(controlProfile.Result) != hashRawRows(candidateProfile.Result) {
		report.Decision = "reject-parity"
		writeIdentityEvidence(t, compiled, candidate, report)
		t.Fatalf("identity candidate result parity mismatch control=%s candidate=%s", controlHash, candidateHash)
	}
	controlMedian := median(controlSeconds)
	candidateMedian := median(candidateSeconds)
	if candidateMedian < controlMedian*0.90 {
		report.Decision = "pass-10-percent-gate"
	} else {
		report.Decision = "reject-under-10-percent-gate"
	}
	writeIdentityEvidence(t, compiled, candidate, report)
	t.Logf("WP3 identity tournament control_median=%.6fs candidate_median=%.6fs control_profile=%+v candidate_profile=%+v report=%+v", controlMedian, candidateMedian, controlSummary, candidateSummary, report)
}

// buildIdentityDedupCandidate inserts identity grouping before the existing
// child projection.  It intentionally retains the original scope filters and
// sorts, then sorts the deduplicated node stream again because COLLECT does
// not provide a documented order guarantee.  The outer object projection is
// unchanged except that it reads the grouped node variable.
func buildIdentityDedupCandidate(control string) (string, identityDedupReport, error) {
	report := identityDedupReport{
		Input:               "examples/meta_gdc_case_matrix.variables.json",
		Limit:               1000,
		ControlObjectUnique: strings.Count(control, "UNIQUE(("),
	}
	startRE := regexp.MustCompile(`(?m)^  LET (child_set_[0-9]+) = UNIQUE\(\(\n`)
	matches := startRE.FindAllStringSubmatchIndex(control, -1)
	if len(matches) == 0 {
		return "", report, fmt.Errorf("no typed child-set materializations in actual production AQL")
	}
	report.ChildSets = len(matches)
	candidate := control
	for index := len(matches) - 1; index >= 0; index-- {
		match := matches[index]
		name := control[match[2]:match[3]]
		start := match[0]
		bodyStart := match[1]
		closeRel := strings.Index(control[bodyStart:], "\n  ))")
		if closeRel < 0 {
			return "", report, fmt.Errorf("child set %s has no closing wrapper", name)
		}
		closeStart := bodyStart + closeRel
		closeEnd := closeStart + len("\n  ))")
		block := control[start:closeEnd]
		loopRE := regexp.MustCompile(`(?m)^    FOR ([A-Za-z0-9_]+) IN `)
		loop := loopRE.FindStringSubmatch(block)
		if len(loop) != 2 {
			return "", report, fmt.Errorf("child set %s has no source loop", name)
		}
		loopVar := loop[1]
		returnAt := strings.Index(block, "\n      RETURN ")
		if returnAt < 0 {
			return "", report, fmt.Errorf("child set %s has no projection return", name)
		}
		identityKey := fmt.Sprintf("__loom_identity_key_%d", index)
		identityRows := fmt.Sprintf("__loom_identity_rows_%d", index)
		identityItem := fmt.Sprintf("__loom_identity_item_%d", index)
		insert := fmt.Sprintf("\n      COLLECT %s = %s._id INTO %s\n      LET %s = FIRST(%s).%s\n      SORT %s._key", identityKey, loopVar, identityRows, identityItem, identityRows, loopVar, identityItem)
		block = block[:returnAt] + insert + block[returnAt:]
		projectionStart := strings.Index(block, "\n      RETURN ") + len("\n      RETURN ")
		projectionEnd := strings.LastIndex(block, "\n  ))")
		if projectionEnd < 0 {
			return "", report, fmt.Errorf("child set %s projection boundary not found", name)
		}
		projection := strings.ReplaceAll(block[projectionStart:projectionEnd], loopVar+".", identityItem+".")
		block = block[:projectionStart] + projection + block[projectionEnd:]
		block = strings.Replace(block, " = UNIQUE((\n", " = (\n", 1)
		block = strings.Replace(block, "\n  ))", "\n  )", 1)
		candidate = candidate[:start] + block + candidate[closeEnd:]
		report.TransformedSets++
	}
	report.CandidateObjectUnique = strings.Count(candidate, "UNIQUE((")
	report.CandidateIdentityCollects = strings.Count(candidate, "COLLECT __loom_identity_key_")
	// Count the materialized identity groups that survived the textual rewrite;
	// shared/nested blocks may be conservatively left unchanged.
	report.TransformedSets = report.CandidateIdentityCollects
	report.ControlAQLSHA256 = sha256Hex(control)
	report.CandidateAQLSHA256 = sha256Hex(candidate)
	return candidate, report, nil
}

func writeIdentityEvidence(t *testing.T, compiled dataframe.CompiledQuery, candidate string, report identityDedupReport) {
	_, source, _, _ := runtime.Caller(0)
	directory := filepath.Join(filepath.Dir(source), "..", "..", "docs", "benchmarks", "round4", "wp3")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create WP3 evidence directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "control.aql"), []byte(compiled.Query+"\n"), 0o644); err != nil {
		t.Fatalf("write WP3 control AQL: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "candidate.aql"), []byte(candidate+"\n"), 0o644); err != nil {
		t.Fatalf("write WP3 candidate AQL: %v", err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("encode WP3 evidence: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "evidence.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write WP3 evidence: %v", err)
	}
}
