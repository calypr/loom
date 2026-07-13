package dataframe_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	dataframe "github.com/calypr/loom/internal/dataframe"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestEndpointStrategyUsesTypedLookupForNestedGDCAndFallsBack(t *testing.T) {
	compiled := compileActualGDC(t, 1000)
	if !strings.Contains(compiled.Query, "FOR child_set_3_edge IN @@child_set_3_edge_collection") {
		t.Fatalf("default generic plan did not render endpoint lookup for nested route:\n%s", compiled.Query)
	}
	if !strings.Contains(compiled.Query, "FILTER child_set_3_edge._to == __loom_physical_parent_set_4._id") || !strings.Contains(compiled.Query, "DOCUMENT(child_set_3_edge._from)") {
		t.Fatalf("endpoint lookup lost inbound endpoint/join fields:\n%s", compiled.Query)
	}

	nativePolicy := dataframe.DefaultPhysicalOptimizationPolicy().WithRule(dataframe.PhysicalOptimizationRuleEndpointTraversal, false)
	native, err := dataframe.CompileRequestWithPolicy(dataframe.Builder{
		Project: "ARANGODB_PROTO", RootResourceType: "Patient",
		Traversals: []dataframe.TraversalStep{{Label: "subject_Patient", ToResourceType: "Specimen", Alias: "specimen", Traversals: []dataframe.TraversalStep{{Label: "subject_Specimen", ToResourceType: "DocumentReference", Alias: "file"}}}},
	}, 25, nativePolicy)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(native.Query, "INBOUND __loom_physical_parent_2 @@traversal_2_edge_collection") {
		t.Fatalf("disabled endpoint policy did not retain native traversal:\n%s", native.Query)
	}
}

func TestEndpointStrategySupportsProvenResearchSubjectStudyOutbound(t *testing.T) {
	builder := dataframe.Builder{
		Project: "ARANGODB_PROTO", RootResourceType: "ResearchSubject",
		Traversals: []dataframe.TraversalStep{{Label: "study", ToResourceType: "ResearchStudy", Alias: "study"}},
	}
	semantic, err := dataframe.BuildSemanticPlan(builder)
	if err != nil {
		t.Fatal(err)
	}
	physical, err := dataframe.BuildPhysicalPlanWithPolicy(semantic, dataframe.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range physical.Operations {
		if operation.Traversal != nil {
			t.Logf("outbound physical strategy=%q endpoint=%q join=%q index=%#v", operation.Traversal.Strategy, operation.Traversal.EndpointField, operation.Traversal.EndpointJoinField, operation.Traversal.EndpointIndexFields)
		}
	}
	compiled, err := dataframe.CompileRequestWithPolicy(builder, 25, dataframe.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compiled.Query, "FOR edge_1 IN @@traversal_1_edge_collection") {
		t.Fatalf("outbound route did not use typed endpoint lookup:\n%s", compiled.Query)
	}
	for _, want := range []string{
		"FILTER edge_1._from == root._id",
		"FILTER edge_1.to_type == @traversal_1_target_type",
		"LET node_1 = DOCUMENT(edge_1._to)",
		"FILTER node_1.resourceType == @traversal_1_target_type",
	} {
		if !strings.Contains(compiled.Query, want) {
			t.Fatalf("outbound endpoint lookup missing %q:\n%s", want, compiled.Query)
		}
	}
}

func TestEndpointStrategyProfilesActualGDCAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_COMPILER_ARANGO_INTEGRATION") == "" {
		t.Skip("set LOOM_COMPILER_ARANGO_INTEGRATION=1 to run endpoint strategy live gate")
	}
	native := compileActualGDCWithEndpointRule(t, false)
	candidate := compileActualGDCWithEndpointRule(t, true)
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
		controlQuery, controlBinds := cacheBust(native.Query, native.BindVars, 51000+run)
		candidateQuery, candidateBinds := cacheBust(candidate.Query, candidate.BindVars, 52000+run)
		seconds, _, hash, err := executeOrdinary(ctx, client, controlQuery, controlBinds)
		if err != nil {
			t.Fatalf("native control run %d: %v", run+1, err)
		}
		controlTimes = append(controlTimes, seconds)
		controlHash = hash
		seconds, _, hash, err = executeOrdinary(ctx, client, candidateQuery, candidateBinds)
		if err != nil {
			t.Fatalf("endpoint candidate run %d: %v", run+1, err)
		}
		candidateTimes = append(candidateTimes, seconds)
		candidateHash = hash
	}
	if controlHash != candidateHash {
		t.Fatalf("endpoint result parity mismatch control=%s candidate=%s", controlHash, candidateHash)
	}
	controlExplain, err := client.Explain(ctx, arangostore.ExplainRequest{Query: native.Query, BindVars: native.BindVars})
	if err != nil {
		t.Fatalf("native Explain: %v", err)
	}
	candidateExplain, err := client.Explain(ctx, arangostore.ExplainRequest{Query: candidate.Query, BindVars: candidate.BindVars})
	if err != nil {
		t.Fatalf("endpoint Explain: %v", err)
	}
	controlProfile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: native.Query, BindVars: native.BindVars, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		t.Fatalf("native PROFILE: %v", err)
	}
	candidateProfile, err := client.Profile(ctx, arangostore.ProfileRequest{Query: candidate.Query, BindVars: candidate.BindVars, BatchSize: 10000, Count: true, Options: arangostore.ProfileOptions{Profile: 2}})
	if err != nil {
		t.Fatalf("endpoint PROFILE: %v", err)
	}
	t.Logf("endpoint live control_hash=%s candidate_hash=%s control_median=%.6f candidate_median=%.6f control_explain=%+v candidate_explain=%+v control_profile=%+v candidate_profile=%+v", controlHash, candidateHash, median(controlTimes), median(candidateTimes), arangostore.AssessExplainResult(controlExplain), arangostore.AssessExplainResult(candidateExplain), arangostore.SummarizeProfile(controlProfile), arangostore.SummarizeProfile(candidateProfile))
	writeEndpointIntegrationEvidence(t, native, candidate, controlHash, candidateHash, controlTimes, candidateTimes, controlExplain, candidateExplain, controlProfile, candidateProfile)
}

func compileActualGDCWithEndpointRule(t *testing.T, enabled bool) dataframe.CompiledQuery {
	old, present := os.LookupEnv("LOOM_PHYSICAL_RULE_ENDPOINT_TRAVERSAL")
	if enabled {
		_ = os.Unsetenv("LOOM_PHYSICAL_RULE_ENDPOINT_TRAVERSAL")
	} else {
		_ = os.Setenv("LOOM_PHYSICAL_RULE_ENDPOINT_TRAVERSAL", "off")
	}
	defer func() {
		if present {
			_ = os.Setenv("LOOM_PHYSICAL_RULE_ENDPOINT_TRAVERSAL", old)
		} else {
			_ = os.Unsetenv("LOOM_PHYSICAL_RULE_ENDPOINT_TRAVERSAL")
		}
	}()
	return compileActualGDC(t, 1000)
}

func writeEndpointIntegrationEvidence(t *testing.T, control, candidate dataframe.CompiledQuery, controlHash, candidateHash string, controlTimes, candidateTimes []float64, controlExplain, candidateExplain arangostore.ExplainResult, controlProfile, candidateProfile arangostore.ProfileResult) {
	_, source, _, _ := runtime.Caller(0)
	directory := filepath.Join(filepath.Dir(source), "..", "..", "docs", "benchmarks", "round4", "wp2")
	if err := os.WriteFile(filepath.Join(directory, "integration-control.aql"), []byte(control.Query+"\n"), 0o644); err != nil {
		t.Fatalf("write endpoint control AQL: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "integration-candidate.aql"), []byte(candidate.Query+"\n"), 0o644); err != nil {
		t.Fatalf("write endpoint candidate AQL: %v", err)
	}
	payload := map[string]any{
		"control_aql_sha256": sha256Hex(control.Query), "candidate_aql_sha256": sha256Hex(candidate.Query),
		"control_result_sha256": controlHash, "candidate_result_sha256": candidateHash,
		"control_seconds": controlTimes, "candidate_seconds": candidateTimes,
		"control_explain": controlExplain, "candidate_explain": candidateExplain,
		"control_profile": controlProfile, "candidate_profile": candidateProfile,
		"decision": "pending-coordinator-threshold-review",
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("encode endpoint integration evidence: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "integration-evidence.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write endpoint integration evidence: %v", err)
	}
}
